package service

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5" //nolint:gosec // checksum for change detection only
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/mdm/android/service/androidmgmt"
	"google.golang.org/api/androidmanagement/v1"
)

const policyEnforcementRulesKey = "policyEnforcementRules"

// automdmDefaultPolicyPatchTimeout bounds each default policy patch: the AMAPI client has no timeout, and the
// reconciler syncs these before any host, so a hung call would hold up profile delivery to every host.
var automdmDefaultPolicyPatchTimeout = time.Minute

// buildAutoMDMDefaultPolicy builds the policy a team's hosts enroll with from the team's label-less profiles,
// so the end user sets the passcode during setup. Only the passcode settings are copied, and each key goes
// to the profile whose name sorts last, as in the host's own policy, so the passcode asked for during setup
// matches the one enforced afterwards. policyEnforcementRules is copied only when a passcode profile wins
// it. Profiles with variables (only possible if saved before passcode profiles rejected them) are skipped
// and their names returned.
func buildAutoMDMDefaultPolicy(profiles []*automdm.MDMAndroidConfigProfile) (*androidmanagement.Policy, []string, error) {
	sorted := slices.Clone(profiles)
	slices.SortFunc(sorted, func(a, b *automdm.MDMAndroidConfigProfile) int {
		return cmp.Compare(a.Name, b.Name)
	})

	type winner struct {
		value      json.RawMessage
		isPasscode bool
	}
	winners := make(map[string]winner)
	var skipped []string
	for _, p := range sorted {
		if err := automdm.ValidateAndroidPasscodeProfileVariables(p.RawJSON); err != nil {
			skipped = append(skipped, p.Name)
			continue
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(p.RawJSON, &keys); err != nil {
			return nil, nil, fmt.Errorf("unmarshal profile %s: %w", p.ProfileUUID, err)
		}
		isPasscode := automdm.IsAndroidPasscodeProfile(p.RawJSON)
		for _, k := range append(slices.Clone(automdm.AndroidPasscodeKeys), policyEnforcementRulesKey) {
			if v, ok := keys[k]; ok {
				winners[k] = winner{value: v, isPasscode: isPasscode}
			}
		}
	}

	settings := make(map[string]json.RawMessage, len(winners))
	for k, w := range winners {
		if k == policyEnforcementRulesKey && !w.isPasscode {
			continue
		}
		settings[k] = w.value
	}

	var policy androidmanagement.Policy
	if len(settings) > 0 {
		b, err := json.Marshal(settings)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal default policy settings: %w", err)
		}
		if err := json.Unmarshal(b, &policy); err != nil {
			return nil, nil, fmt.Errorf("unmarshal default policy settings: %w", err)
		}
	}
	applyTeamEnforcedSettings(&policy)
	return &policy, skipped, nil
}

func automdmDefaultPolicyChecksum(policy *androidmanagement.Policy) ([]byte, error) {
	b, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	sum := md5.Sum(b) //nolint:gosec // checksum for change detection only
	return sum[:], nil
}

func automdmDefaultPolicyName(enterpriseName string, teamID uint) string {
	return fmt.Sprintf("%s/policies/%s", enterpriseName, android.AutoMDMDefaultPolicyID(teamID))
}

// prepareAutoMDMDefaultPolicy builds a team's default policy and its checksum, and returns the names of the
// profiles left out of it for using variables.
func prepareAutoMDMDefaultPolicy(ctx context.Context, teamID uint, profiles []*automdm.MDMAndroidConfigProfile,
) (*androidmanagement.Policy, []byte, []string, error) {
	policy, skipped, err := buildAutoMDMDefaultPolicy(profiles)
	if err != nil {
		return nil, nil, nil, ctxerr.Wrapf(ctx, err, "build default policy for team %d", teamID)
	}
	checksum, err := automdmDefaultPolicyChecksum(policy)
	if err != nil {
		return nil, nil, nil, ctxerr.Wrapf(ctx, err, "checksum default policy for team %d", teamID)
	}
	return policy, checksum, skipped, nil
}

// sendAutoMDMDefaultPolicy patches a team's default policy in AMAPI. If AMAPI rejects the passcode settings,
// it sends the base settings instead, so the policy that enrollment tokens point at always exists; the
// passcode then arrives with the host's own policy, which reports the rejection on the profile.
func sendAutoMDMDefaultPolicy(ctx context.Context, client androidmgmt.Client, logger *slog.Logger,
	enterpriseName string, teamID uint, policy *androidmanagement.Policy, skipped []string,
) error {
	// Logged only when the policy is sent, not on every reconcile run.
	if len(skipped) > 0 {
		logger.WarnContext(ctx, "android passcode profiles with variables are left out of the team's default policy",
			"team_id", teamID, "profiles", skipped)
	}
	policyName := automdmDefaultPolicyName(enterpriseName, teamID)
	patch := func(p *androidmanagement.Policy) error {
		patchCtx, cancel := context.WithTimeout(ctx, automdmDefaultPolicyPatchTimeout)
		defer cancel()
		_, err := client.EnterprisesPoliciesPatch(patchCtx, policyName, p, androidmgmt.PoliciesPatchOpts{ExcludeApps: true})
		if err != nil && !androidmgmt.IsNotModifiedError(err) {
			return err
		}
		return nil
	}
	err := patch(policy)
	if err == nil {
		return nil
	}
	if !androidmgmt.IsBadRequestError(err) {
		return ctxerr.Wrapf(ctx, err, "patch default policy %s", policyName)
	}
	logger.ErrorContext(ctx, "android rejected the passcode settings of the team's default policy, sending it without them",
		"team_id", teamID, "err", err)
	var base androidmanagement.Policy
	applyTeamEnforcedSettings(&base)
	if err := patch(&base); err != nil {
		return ctxerr.Wrapf(ctx, err, "patch base default policy %s", policyName)
	}
	return nil
}

// reconcileAutoMDMDefaultPolicies sends the default policy of every team whose passcode profiles changed,
// including teams whose last passcode profile was removed, which go back to the base settings. Teams
// without passcode profiles get theirs when an enrollment token is first created for them.
func (r *profileReconciler) reconcileAutoMDMDefaultPolicies(ctx context.Context) error {
	profiles, err := r.DS.ListAndroidAutoMDMDefaultPolicyProfiles(ctx, nil)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "list android team default policy profiles")
	}
	checksums, err := r.DS.GetAndroidAutoMDMDefaultPolicyChecksums(ctx)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "get android team default policy checksums")
	}
	deletedTeamIDs, err := r.DS.ListAndroidAutoMDMDefaultPolicyDeletedTeams(ctx)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "list deleted teams with android default policies")
	}

	profilesByTeam := make(map[uint][]*automdm.MDMAndroidConfigProfile)
	for _, p := range profiles {
		var teamID uint
		if p.TeamID != nil {
			teamID = *p.TeamID
		}
		profilesByTeam[teamID] = append(profilesByTeam[teamID], p)
	}
	teamIDs := make(map[uint]struct{}, len(profilesByTeam)+len(checksums))
	for teamID := range profilesByTeam {
		teamIDs[teamID] = struct{}{}
	}
	for teamID := range checksums {
		teamIDs[teamID] = struct{}{}
	}

	// A deleted team's zero-touch token still enrolls hosts, into Unassigned, so they set up with its settings.
	for _, teamID := range deletedTeamIDs {
		profilesByTeam[teamID] = profilesByTeam[0]
	}

	for teamID := range teamIDs {
		// One team's failure (e.g. AMAPI quota) must not hold back the others; it's retried next run.
		if err := r.syncAutoMDMDefaultPolicy(ctx, teamID, profilesByTeam[teamID], checksums[teamID]); err != nil {
			r.Logger.ErrorContext(ctx, "failed to sync android team default policy", "team_id", teamID, "err", err)
		}
	}
	return nil
}

func (r *profileReconciler) syncAutoMDMDefaultPolicy(ctx context.Context, teamID uint, profiles []*automdm.MDMAndroidConfigProfile,
	storedChecksum []byte,
) error {
	policy, checksum, skipped, err := prepareAutoMDMDefaultPolicy(ctx, teamID, profiles)
	if err != nil {
		return err
	}
	if bytes.Equal(checksum, storedChecksum) {
		return nil
	}
	if err := sendAutoMDMDefaultPolicy(ctx, r.Client, r.Logger, r.Enterprise.Name(), teamID, policy, skipped); err != nil {
		return err
	}
	// Only replaces what was read, so a policy that EnsureAutoMDMDefaultPolicy marked unverified meanwhile
	// is sent again next run instead of being recorded as up to date.
	if err := r.DS.SetAndroidAutoMDMDefaultPolicyChecksum(ctx, teamID, checksum, storedChecksum); err != nil {
		return ctxerr.Wrapf(ctx, err, "save default policy checksum for team %d", teamID)
	}
	return nil
}

// PrepareHostPolicy replaces every setting of a host's own policy with its team's default policy, before
// the device is moved to it. The reconciler may already have moved a device that is still in setup, and the
// base settings alone would drop the passcode it enrolled with, so the setup wizard would skip the passcode.
// If AMAPI rejects the passcode settings, the base settings are sent instead, as for the team's policy. Any
// other AMAPI error is returned, so the worker retries instead of moving the device to a policy that may lack
// the passcode.
func (svc *Service) PrepareHostPolicy(ctx context.Context, enterpriseName, hostUUID string, teamID uint) error {
	profiles, err := svc.automdmDS.ListAndroidAutoMDMDefaultPolicyProfiles(ctx, &teamID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "list android team default policy profiles")
	}
	policy, _, _, err := prepareAutoMDMDefaultPolicy(ctx, teamID, profiles)
	if err != nil {
		return err
	}
	policyName := fmt.Sprintf("%s/policies/%s", enterpriseName, hostUUID)
	_, apiErr, err := svc.patchPolicy(ctx, hostUUID, policyName, policy, nil)
	switch {
	case err != nil:
		return err
	case apiErr == nil:
		return nil
	case !androidmgmt.IsBadRequestError(apiErr):
		return ctxerr.Wrapf(ctx, apiErr, "patch android host policy %s", policyName)
	}
	svc.logger.ErrorContext(ctx, "android rejected the passcode settings of the host's policy, sending it without them",
		"host_uuid", hostUUID, "team_id", teamID, "err", apiErr)
	var base androidmanagement.Policy
	applyTeamEnforcedSettings(&base)
	_, apiErr, err = svc.patchPolicy(ctx, hostUUID, policyName, &base, nil)
	if err != nil {
		return err
	}
	if apiErr != nil {
		return ctxerr.Wrapf(ctx, apiErr, "patch android host policy %s with the base settings", policyName)
	}
	return nil
}

// EnsureAutoMDMDefaultPolicy makes sure the default policy of a team (0 is Unassigned) was sent to AMAPI
// and returns its name, for enrollment tokens to point at.
func (svc *Service) EnsureAutoMDMDefaultPolicy(ctx context.Context, enterpriseName string, teamID uint) (string, error) {
	// A lagging replica could miss the policy or its profiles, and this would overwrite the policy with less.
	ctx = ctxdb.RequirePrimary(ctx, true)
	checksums, err := svc.automdmDS.GetAndroidAutoMDMDefaultPolicyChecksums(ctx)
	if err != nil {
		return "", ctxerr.Wrap(ctx, err, "get android team default policy checksums")
	}
	if _, ok := checksums[teamID]; !ok {
		profiles, err := svc.automdmDS.ListAndroidAutoMDMDefaultPolicyProfiles(ctx, &teamID)
		if err != nil {
			return "", ctxerr.Wrap(ctx, err, "list android team default policy profiles")
		}
		policy, _, skipped, err := prepareAutoMDMDefaultPolicy(ctx, teamID, profiles)
		if err != nil {
			return "", err
		}
		// The premium service calls this with its own client, so this one may not be authenticated yet.
		secret, err := svc.getClientAuthenticationSecret(ctx)
		if err != nil {
			return "", ctxerr.Wrap(ctx, err, "getting client authentication secret")
		}
		_ = svc.androidAPIClient.SetAuthenticationSecret(secret)

		if err := sendAutoMDMDefaultPolicy(ctx, svc.androidAPIClient, svc.logger, enterpriseName, teamID, policy, skipped); err != nil {
			return "", err
		}
		// The reconciler may be sending newer profiles at the same time, and either patch can land last, so
		// this doesn't vouch for the content: the reconciler sends the policy again on its next run.
		if err := svc.automdmDS.MarkAndroidAutoMDMDefaultPolicyUnverified(ctx, teamID); err != nil {
			return "", ctxerr.Wrapf(ctx, err, "save default policy of team %d", teamID)
		}
	}
	return automdmDefaultPolicyName(enterpriseName, teamID), nil
}

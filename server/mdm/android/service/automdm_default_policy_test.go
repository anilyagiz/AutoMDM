package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	android_mock "github.com/anilyagiz/automdm/v4/server/mdm/android/mock"
	"github.com/anilyagiz/automdm/v4/server/mdm/android/service/androidmgmt"
	ds_mock "github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/androidmanagement/v1"
	"google.golang.org/api/googleapi"
)

func TestBuildAutoMDMDefaultPolicy(t *testing.T) {
	profile := func(name, rawJSON string) *automdm.MDMAndroidConfigProfile {
		return &automdm.MDMAndroidConfigProfile{ProfileUUID: "g-" + name, Name: name, RawJSON: []byte(rawJSON)}
	}
	// settings returns the policy's JSON without the status reporting settings every policy gets.
	settings := func(t *testing.T, policy *androidmanagement.Policy) map[string]any {
		require.NotNil(t, policy.StatusReportingSettings)
		b, err := json.Marshal(policy)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(b, &got))
		delete(got, "statusReportingSettings")
		return got
	}
	decode := func(t *testing.T, s string) map[string]any {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(s), &m))
		return m
	}

	cases := []struct {
		name        string
		profiles    []*automdm.MDMAndroidConfigProfile
		want        string
		wantSkipped []string
	}{
		{
			name: "no profiles",
			want: `{}`,
		},
		{
			name: "only passcode keys are copied",
			profiles: []*automdm.MDMAndroidConfigProfile{
				profile("pin", `{"passwordPolicies": [{"passwordMinimumLength": 6}], "cameraDisabled": true}`),
			},
			want: `{"passwordPolicies": [{"passwordMinimumLength": 6}]}`,
		},
		{
			name: "the name that sorts last wins a key",
			profiles: []*automdm.MDMAndroidConfigProfile{
				profile("b", `{"passwordPolicies": [{"passwordMinimumLength": 8}]}`),
				profile("a", `{"passwordPolicies": [{"passwordMinimumLength": 4}]}`),
			},
			want: `{"passwordPolicies": [{"passwordMinimumLength": 8}]}`,
		},
		{
			name: "passwordPolicies and passwordRequirements from different profiles are both copied",
			profiles: []*automdm.MDMAndroidConfigProfile{
				profile("a", `{"passwordRequirements": {"passwordMinimumLength": 4}}`),
				profile("b", `{"passwordPolicies": [{"passwordMinimumLength": 8}]}`),
			},
			want: `{"passwordRequirements": {"passwordMinimumLength": 4}, "passwordPolicies": [{"passwordMinimumLength": 8}]}`,
		},
		{
			name: "enforcement rules from the winning passcode profile",
			profiles: []*automdm.MDMAndroidConfigProfile{
				profile("pin", `{"passwordPolicies": [{"passwordMinimumLength": 6}], "policyEnforcementRules": [{"settingName": "passwordPolicies", "blockAction": {"blockAfterDays": 1}}]}`),
			},
			want: `{"passwordPolicies": [{"passwordMinimumLength": 6}], "policyEnforcementRules": [{"settingName": "passwordPolicies", "blockAction": {"blockAfterDays": 1}}]}`,
		},
		{
			name: "enforcement rules won by a non-passcode profile are left out",
			profiles: []*automdm.MDMAndroidConfigProfile{
				profile("a pin", `{"passwordPolicies": [{"passwordMinimumLength": 6}], "policyEnforcementRules": [{"settingName": "passwordPolicies"}]}`),
				profile("b rules", `{"policyEnforcementRules": [{"settingName": "cameraDisabled"}]}`),
			},
			want: `{"passwordPolicies": [{"passwordMinimumLength": 6}]}`,
		},
		{
			name: "a non-passcode profile's rules lose to a later passcode profile",
			profiles: []*automdm.MDMAndroidConfigProfile{
				profile("a rules", `{"policyEnforcementRules": [{"settingName": "cameraDisabled"}]}`),
				profile("b pin", `{"passwordPolicies": [{"passwordMinimumLength": 6}], "policyEnforcementRules": [{"settingName": "passwordPolicies"}]}`),
			},
			want: `{"passwordPolicies": [{"passwordMinimumLength": 6}], "policyEnforcementRules": [{"settingName": "passwordPolicies"}]}`,
		},
		{
			name: "profiles with variables are skipped",
			profiles: []*automdm.MDMAndroidConfigProfile{
				profile("a", `{"passwordPolicies": [{"passwordMinimumLength": 4}]}`),
				profile("b vars", `{"passwordPolicies": [{"passwordMinimumLength": 8}], "name": "$AUTOMDM_VAR_HOST_UUID"}`),
			},
			want:        `{"passwordPolicies": [{"passwordMinimumLength": 4}]}`,
			wantSkipped: []string{"b vars"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			policy, skipped, err := buildAutoMDMDefaultPolicy(c.profiles)
			require.NoError(t, err)
			assert.Equal(t, decode(t, c.want), settings(t, policy))
			assert.Equal(t, c.wantSkipped, skipped)
		})
	}

	t.Run("checksum is stable and follows the content", func(t *testing.T) {
		pinFour := []*automdm.MDMAndroidConfigProfile{profile("a", `{"passwordPolicies": [{"passwordMinimumLength": 4}]}`)}
		pinSix := []*automdm.MDMAndroidConfigProfile{profile("a", `{"passwordPolicies": [{"passwordMinimumLength": 6}]}`)}
		checksum := func(profiles []*automdm.MDMAndroidConfigProfile) []byte {
			policy, _, err := buildAutoMDMDefaultPolicy(profiles)
			require.NoError(t, err)
			sum, err := automdmDefaultPolicyChecksum(policy)
			require.NoError(t, err)
			return sum
		}
		first, second := checksum(pinFour), checksum(pinFour)
		assert.Equal(t, first, second)
		assert.NotEqual(t, first, checksum(pinSix))
		assert.NotEqual(t, checksum(nil), checksum(pinFour))
	})
}

func TestReconcileAutoMDMDefaultPolicies(t *testing.T) {
	const enterpriseName = "enterprises/LC00test"
	teamA, teamB, teamGone := uint(3), uint(5), uint(9)
	pin := func(teamID uint, minLength int) *automdm.MDMAndroidConfigProfile {
		raw, err := json.Marshal(map[string]any{"passwordPolicies": []map[string]any{{"passwordMinimumLength": minLength}}})
		require.NoError(t, err)
		return &automdm.MDMAndroidConfigProfile{Name: "pin", TeamID: &teamID, RawJSON: raw}
	}
	checksumFor := func(profiles ...*automdm.MDMAndroidConfigProfile) []byte {
		policy, _, err := buildAutoMDMDefaultPolicy(profiles)
		require.NoError(t, err)
		sum, err := automdmDefaultPolicyChecksum(policy)
		require.NoError(t, err)
		return sum
	}

	ds := new(ds_mock.Store)
	client := &android_mock.Client{}
	var logs bytes.Buffer
	reconciler := &profileReconciler{
		DS:         ds,
		Enterprise: &android.Enterprise{EnterpriseID: "LC00test"},
		Client:     client,
		Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
	}

	var (
		profiles       []*automdm.MDMAndroidConfigProfile
		checksums      map[uint][]byte
		deleted        []uint
		patched        map[string]*androidmanagement.Policy
		patchCount     int
		saved          map[uint][]byte
		savedPrevious  map[uint][]byte
		patchErrs      map[string]error
		rejectPasscode bool
		hang           string
	)
	ds.ListAndroidAutoMDMDefaultPolicyProfilesFunc = func(_ context.Context, teamID *uint) ([]*automdm.MDMAndroidConfigProfile, error) {
		require.Nil(t, teamID)
		return profiles, nil
	}
	ds.GetAndroidAutoMDMDefaultPolicyChecksumsFunc = func(_ context.Context) (map[uint][]byte, error) {
		return checksums, nil
	}
	ds.ListAndroidAutoMDMDefaultPolicyDeletedTeamsFunc = func(_ context.Context) ([]uint, error) {
		return deleted, nil
	}
	ds.SetAndroidAutoMDMDefaultPolicyChecksumFunc = func(_ context.Context, teamID uint, checksum, previous []byte) error {
		saved[teamID] = checksum
		savedPrevious[teamID] = previous
		return nil
	}
	client.EnterprisesPoliciesPatchFunc = func(ctx context.Context, policyName string, policy *androidmanagement.Policy, opts androidmgmt.PoliciesPatchOpts) (*androidmanagement.Policy, error) {
		require.True(t, opts.ExcludeApps)
		patchCount++
		patched[policyName] = policy
		if policyName == hang {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if rejectPasscode && policy.PasswordPolicies != nil {
			return nil, &googleapi.Error{Code: http.StatusBadRequest}
		}
		return policy, patchErrs[policyName]
	}
	run := func(t *testing.T) {
		patched, saved, savedPrevious, patchCount = map[string]*androidmanagement.Policy{}, map[uint][]byte{}, map[uint][]byte{}, 0
		require.NoError(t, reconciler.reconcileAutoMDMDefaultPolicies(t.Context()))
	}
	policyName := func(teamID uint) string {
		return enterpriseName + "/policies/" + android.AutoMDMDefaultPolicyID(teamID)
	}

	t.Run("first run creates the policies of teams with passcode profiles", func(t *testing.T) {
		profiles = []*automdm.MDMAndroidConfigProfile{pin(0, 4), pin(teamA, 6)}
		checksums, patchErrs = nil, nil
		run(t)
		require.Len(t, patched, 2)
		require.Equal(t, int64(4), patched[policyName(0)].PasswordPolicies[0].PasswordMinimumLength)
		require.Equal(t, int64(6), patched[policyName(teamA)].PasswordPolicies[0].PasswordMinimumLength)
		require.Equal(t, map[uint][]byte{0: checksumFor(pin(0, 4)), teamA: checksumFor(pin(teamA, 6))}, saved)
	})

	t.Run("unchanged policies are not sent again", func(t *testing.T) {
		checksums = map[uint][]byte{0: checksumFor(pin(0, 4)), teamA: checksumFor(pin(teamA, 6))}
		run(t)
		require.Empty(t, patched)
		require.Empty(t, saved)
	})

	t.Run("only the changed team is sent", func(t *testing.T) {
		profiles = []*automdm.MDMAndroidConfigProfile{pin(0, 4), pin(teamA, 8)}
		run(t)
		require.Len(t, patched, 1)
		require.Equal(t, int64(8), patched[policyName(teamA)].PasswordPolicies[0].PasswordMinimumLength)
		require.Equal(t, map[uint][]byte{teamA: checksumFor(pin(teamA, 8))}, saved)
		require.Equal(t, checksumFor(pin(teamA, 6)), savedPrevious[teamA], "the checksum is only replaced if it's still the one read")
	})

	t.Run("a policy marked unverified is sent again", func(t *testing.T) {
		checksums = map[uint][]byte{0: checksumFor(pin(0, 4)), teamA: make([]byte, 16)}
		run(t)
		require.Len(t, patched, 1)
		require.Equal(t, int64(8), patched[policyName(teamA)].PasswordPolicies[0].PasswordMinimumLength)
		require.Equal(t, map[uint][]byte{teamA: checksumFor(pin(teamA, 8))}, saved)
		require.Equal(t, make([]byte, 16), savedPrevious[teamA])
	})

	t.Run("a team whose passcode profiles are gone goes back to the base settings", func(t *testing.T) {
		profiles = []*automdm.MDMAndroidConfigProfile{pin(0, 4)}
		checksums = map[uint][]byte{0: checksumFor(pin(0, 4)), teamA: checksumFor(pin(teamA, 8))}
		run(t)
		require.Len(t, patched, 1)
		require.Empty(t, patched[policyName(teamA)].PasswordPolicies)
		require.NotNil(t, patched[policyName(teamA)].StatusReportingSettings)
		require.Equal(t, map[uint][]byte{teamA: checksumFor()}, saved)
	})

	t.Run("a team with a base policy and no passcode profiles is left alone", func(t *testing.T) {
		checksums = map[uint][]byte{0: checksumFor(pin(0, 4)), teamB: checksumFor()}
		run(t)
		require.Empty(t, patched)
	})

	t.Run("an AMAPI failure for one team doesn't stop the others and isn't saved", func(t *testing.T) {
		profiles = []*automdm.MDMAndroidConfigProfile{pin(teamA, 6), pin(teamB, 7)}
		checksums = nil
		patchErrs = map[string]error{policyName(teamA): errors.New("quota")}
		run(t)
		require.Len(t, patched, 2)
		require.Equal(t, map[uint][]byte{teamB: checksumFor(pin(teamB, 7))}, saved)
	})

	t.Run("not modified counts as sent", func(t *testing.T) {
		profiles = []*automdm.MDMAndroidConfigProfile{pin(teamGone, 6)}
		checksums = nil
		patchErrs = map[string]error{policyName(teamGone): &googleapi.Error{Code: http.StatusNotModified}}
		run(t)
		require.Equal(t, map[uint][]byte{teamGone: checksumFor(pin(teamGone, 6))}, saved)
		require.Nil(t, savedPrevious[teamGone], "a team without a row is only inserted if still absent")
	})

	t.Run("rejected passcode settings are replaced by the base settings and not retried", func(t *testing.T) {
		profiles = []*automdm.MDMAndroidConfigProfile{pin(teamA, 6)}
		checksums, patchErrs, rejectPasscode = nil, nil, true
		t.Cleanup(func() { rejectPasscode = false })
		run(t)
		require.Equal(t, 2, patchCount)
		require.Empty(t, patched[policyName(teamA)].PasswordPolicies, "the last patch is the base policy")
		require.NotNil(t, patched[policyName(teamA)].StatusReportingSettings)
		require.Equal(t, map[uint][]byte{teamA: checksumFor(pin(teamA, 6))}, saved)

		checksums = saved
		run(t)
		require.Zero(t, patchCount, "nothing is sent again until the profiles change")
	})

	t.Run("a hung patch times out without holding up the other teams", func(t *testing.T) {
		timeout := automdmDefaultPolicyPatchTimeout
		automdmDefaultPolicyPatchTimeout = 10 * time.Millisecond
		t.Cleanup(func() { automdmDefaultPolicyPatchTimeout, hang = timeout, "" })
		profiles = []*automdm.MDMAndroidConfigProfile{pin(teamA, 6), pin(teamB, 7)}
		checksums, patchErrs, hang = nil, nil, policyName(teamA)
		run(t)
		require.Len(t, patched, 2)
		require.Equal(t, map[uint][]byte{teamB: checksumFor(pin(teamB, 7))}, saved, "the hung team is retried next run")
	})

	t.Run("a deleted team's policy follows Unassigned's", func(t *testing.T) {
		// teamGone's zero-touch token still enrolls hosts, into Unassigned.
		profiles = []*automdm.MDMAndroidConfigProfile{pin(0, 4), pin(teamA, 6)}
		checksums = map[uint][]byte{0: checksumFor(pin(0, 4)), teamA: checksumFor(pin(teamA, 6)), teamGone: checksumFor(pin(teamGone, 8))}
		deleted, patchErrs = []uint{teamGone}, nil
		t.Cleanup(func() { deleted = nil })
		run(t)
		require.Len(t, patched, 1)
		require.Equal(t, int64(4), patched[policyName(teamGone)].PasswordPolicies[0].PasswordMinimumLength)
		require.Equal(t, map[uint][]byte{teamGone: checksumFor(pin(0, 4))}, saved)

		// It keeps following Unassigned's changes, including losing the passcode.
		checksums = map[uint][]byte{0: checksumFor(pin(0, 4)), teamA: checksumFor(pin(teamA, 6)), teamGone: checksumFor(pin(0, 4))}
		profiles = []*automdm.MDMAndroidConfigProfile{pin(teamA, 6)}
		run(t)
		require.Len(t, patched, 2)
		require.Empty(t, patched[policyName(0)].PasswordPolicies)
		require.Empty(t, patched[policyName(teamGone)].PasswordPolicies)
	})

	t.Run("a profile left out for using variables is logged when the policy is sent, not every run", func(t *testing.T) {
		withVar := &automdm.MDMAndroidConfigProfile{
			Name: "pin-var", TeamID: &teamB,
			RawJSON: json.RawMessage(`{"passwordPolicies":[{"passwordScope":"$AUTOMDM_VAR_HOST_UUID"}]}`),
		}
		profiles = []*automdm.MDMAndroidConfigProfile{withVar}
		checksums, patchErrs = nil, nil
		logs.Reset()
		run(t)
		require.Len(t, patched, 1)
		require.Empty(t, patched[policyName(teamB)].PasswordPolicies)
		require.Equal(t, 1, strings.Count(logs.String(), "left out of the team's default policy"))
		require.Contains(t, logs.String(), "pin-var")

		checksums = saved
		logs.Reset()
		run(t)
		require.Empty(t, patched)
		require.NotContains(t, logs.String(), "left out of the team's default policy")
	})
}

// The premium zero-touch service calls EnsureAutoMDMDefaultPolicy with a client of its own, so this service's
// client may never have been authenticated; it must authenticate before sending the policy.
func TestEnsureAutoMDMDefaultPolicyAuthenticatesItsClient(t *testing.T) {
	client := android_mock.Client{}
	client.InitCommonMocks()
	var calls []string
	client.SetAuthenticationSecretFunc = func(secret string) error {
		calls = append(calls, "auth "+secret)
		return nil
	}
	client.EnterprisesPoliciesPatchFunc = func(_ context.Context, policyName string, policy *androidmanagement.Policy, _ androidmgmt.PoliciesPatchOpts) (*androidmanagement.Policy, error) {
		calls = append(calls, "patch "+policyName)
		return policy, nil
	}

	automdmDS := InitCommonDSMocks()
	automdmDS.Store.GetAllMDMConfigAssetsByNameFunc = func(_ context.Context, _ []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{automdm.MDMAssetAndroidAutoMDMServerSecret: {Value: []byte("server-secret")}}, nil
	}
	// Read from the primary: a lagging replica could miss the policy and have it overwritten with less.
	automdmDS.Store.GetAndroidAutoMDMDefaultPolicyChecksumsFunc = func(ctx context.Context) (map[uint][]byte, error) {
		require.True(t, ctxdb.IsPrimaryRequired(ctx))
		return nil, nil
	}
	automdmDS.Store.ListAndroidAutoMDMDefaultPolicyProfilesFunc = func(ctx context.Context, _ *uint) ([]*automdm.MDMAndroidConfigProfile, error) {
		require.True(t, ctxdb.IsPrimaryRequired(ctx))
		return nil, nil
	}
	automdmDS.Store.MarkAndroidAutoMDMDefaultPolicyUnverifiedFunc = func(_ context.Context, _ uint) error { return nil }
	svc, err := NewServiceWithClient(slog.New(slog.DiscardHandler), automdmDS, &client, "test-private-key", &automdmDS.DataStore, noopNewActivity, config.AndroidAgentConfig{})
	require.NoError(t, err)
	calls = nil

	policyName, err := svc.EnsureAutoMDMDefaultPolicy(t.Context(), "enterprises/LC00test", 2)
	require.NoError(t, err)
	require.Equal(t, "enterprises/LC00test/policies/automdm2", policyName)
	require.Equal(t, []string{"auth server-secret", "patch enterprises/LC00test/policies/automdm2"}, calls)
}

// The reconciler can move a device that is still in setup to its own policy before the software worker
// replaces that policy's settings, so the worker must keep the team's passcode or the setup wizard skips it.
func TestPrepareHostPolicy(t *testing.T) {
	const (
		enterpriseName = "enterprises/LC00test"
		hostUUID       = "host-uuid"
		hostPolicyName = enterpriseName + "/policies/" + hostUUID
	)
	pin := func(teamID uint, minLength int) *automdm.MDMAndroidConfigProfile {
		raw, err := json.Marshal(map[string]any{
			"passwordPolicies":       []map[string]any{{"passwordMinimumLength": minLength}},
			"policyEnforcementRules": []map[string]any{{"settingName": "passwordPolicies"}},
		})
		require.NoError(t, err)
		return &automdm.MDMAndroidConfigProfile{Name: "pin", TeamID: &teamID, RawJSON: raw}
	}
	profilesByTeam := map[uint][]*automdm.MDMAndroidConfigProfile{0: {pin(0, 4)}, 3: {pin(3, 6)}}

	client := android_mock.Client{}
	client.InitCommonMocks()
	var patched []*androidmanagement.Policy
	var patchErr, basePatchErr error
	client.EnterprisesPoliciesPatchFunc = func(_ context.Context, policyName string, policy *androidmanagement.Policy, opts androidmgmt.PoliciesPatchOpts) (*androidmanagement.Policy, error) {
		require.Equal(t, hostPolicyName, policyName)
		require.True(t, opts.ExcludeApps, "the worker sends the app list itself")
		patched = append(patched, policy)
		if len(policy.PasswordPolicies) > 0 && patchErr != nil {
			return nil, patchErr
		}
		if len(policy.PasswordPolicies) == 0 && basePatchErr != nil {
			return nil, basePatchErr
		}
		return &androidmanagement.Policy{Version: int64(len(patched))}, nil
	}
	automdmDS := InitCommonDSMocks()
	automdmDS.Store.ListAndroidAutoMDMDefaultPolicyProfilesFunc = func(_ context.Context, teamID *uint) ([]*automdm.MDMAndroidConfigProfile, error) {
		require.NotNil(t, teamID)
		return profilesByTeam[*teamID], nil
	}
	var requests []*android.MDMAndroidPolicyRequest
	automdmDS.Store.NewAndroidPolicyRequestFunc = func(_ context.Context, req *android.MDMAndroidPolicyRequest) error {
		requests = append(requests, req)
		return nil
	}
	var logs bytes.Buffer
	svc, err := NewServiceWithClient(slog.New(slog.NewTextHandler(&logs, nil)), automdmDS, &client, "test-private-key",
		&automdmDS.DataStore, noopNewActivity, config.AndroidAgentConfig{})
	require.NoError(t, err)
	prepareErr := func(t *testing.T, teamID uint) error {
		patched, requests = nil, nil
		logs.Reset()
		return svc.PrepareHostPolicy(t.Context(), enterpriseName, hostUUID, teamID)
	}
	prepare := func(t *testing.T, teamID uint) {
		require.NoError(t, prepareErr(t, teamID))
	}

	for _, c := range []struct {
		name      string
		teamID    uint
		wantMinPw int64
	}{
		{"automdm", 3, 6},
		{"Unassigned", 0, 4},
	} {
		t.Run("the "+c.name+"'s passcode is kept", func(t *testing.T) {
			patchErr = nil
			prepare(t, c.teamID)
			require.Len(t, patched, 1)
			require.Len(t, patched[0].PasswordPolicies, 1)
			assert.Equal(t, c.wantMinPw, patched[0].PasswordPolicies[0].PasswordMinimumLength)
			assert.Len(t, patched[0].PolicyEnforcementRules, 1)
			assert.NotNil(t, patched[0].StatusReportingSettings)
			require.Len(t, requests, 1)
			assert.Equal(t, http.StatusOK, requests[0].StatusCode)
		})
	}

	t.Run("a team without passcode profiles gets the base settings", func(t *testing.T) {
		patchErr = nil
		prepare(t, 5)
		require.Len(t, patched, 1)
		assert.Empty(t, patched[0].PasswordPolicies)
		assert.NotNil(t, patched[0].StatusReportingSettings)
	})

	t.Run("rejected passcode settings are replaced by the base settings", func(t *testing.T) {
		patchErr, basePatchErr = &googleapi.Error{Code: http.StatusBadRequest}, nil
		prepare(t, 3)
		require.Len(t, patched, 2)
		assert.Empty(t, patched[1].PasswordPolicies)
		assert.NotNil(t, patched[1].StatusReportingSettings)
		require.Len(t, requests, 2)
		assert.Equal(t, http.StatusBadRequest, requests[0].StatusCode)
		assert.Equal(t, http.StatusOK, requests[1].StatusCode)
		assert.Contains(t, logs.String(), "rejected the passcode settings of the host's policy")
	})

	// The worker retries on an error instead of moving the device to a policy that may lack the passcode.
	for _, c := range []struct {
		name         string
		patchErr     error
		basePatchErr error
		wantPatches  int
		wantStatuses []int
	}{
		{
			name:         "other AMAPI errors are returned",
			patchErr:     &googleapi.Error{Code: http.StatusServiceUnavailable},
			wantPatches:  1,
			wantStatuses: []int{http.StatusServiceUnavailable},
		},
		{
			name:         "a failed fallback to the base settings is returned",
			patchErr:     &googleapi.Error{Code: http.StatusBadRequest},
			basePatchErr: &googleapi.Error{Code: http.StatusInternalServerError},
			wantPatches:  2,
			wantStatuses: []int{http.StatusBadRequest, http.StatusInternalServerError},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			patchErr, basePatchErr = c.patchErr, c.basePatchErr
			err := prepareErr(t, 3)
			require.Error(t, err)
			assert.Contains(t, err.Error(), hostPolicyName)
			require.Len(t, patched, c.wantPatches)
			require.Len(t, requests, len(c.wantStatuses))
			for i, status := range c.wantStatuses {
				assert.Equal(t, status, requests[i].StatusCode)
			}
		})
	}
}

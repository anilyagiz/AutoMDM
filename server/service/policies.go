package service

import (
	"context"
	"fmt"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
)

// maxPolicyAutomationActivitiesPerPage is the upper bound for per_page on the
// list-policy-automation-activities endpoint.
const maxPolicyAutomationActivitiesPerPage = 10_000

/////////////////////////////////////////////////////////////////////////////////
// Get policy by id.
/////////////////////////////////////////////////////////////////////////////////

func getPolicyByIDEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.GetPolicyByIDRequest)
	policy, err := svc.GetPolicyByID(ctx, req.PolicyID)
	if err != nil {
		return automdm.GetPolicyByIDResponse{Err: err}, nil
	}
	return automdm.GetPolicyByIDResponse{Policy: policy}, nil
}

func (svc Service) GetPolicyByID(ctx context.Context, policyID uint) (*automdm.Policy, error) {
	// First, fetch policy to extract team for authorization checks.
	policy, err := svc.ds.Policy(ctx, policyID)
	if err != nil {
		svc.SkipAuth(ctx)
		return nil, err
	}

	// Now, authorize against the fetched policy so that team-scoped users cannot
	// read policies belonging to teams they have no role on.
	if err := svc.authz.Authorize(ctx, policy, automdm.ActionRead); err != nil {
		return nil, err
	}

	// If it's a team policy we populate the automations on the policy.
	if err := svc.populateAutomationsForTeamPolicy(ctx, policy); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "populate automations")
	}
	if err := svc.populateSoftwareIconURLs(ctx, []*automdm.Policy{policy}); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "populate software icon urls")
	}

	return policy, nil
}

/////////////////////////////////////////////////////////////////////////////////
// Reset policy.
/////////////////////////////////////////////////////////////////////////////////

func resetPolicyEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.ResetPolicyRequest)
	err := svc.ResetPolicy(ctx, req.PolicyID, req.HostID)
	return automdm.ResetPolicyResponse{Err: err}, nil
}

func (svc Service) ResetPolicy(ctx context.Context, policyID uint, hostID *uint) error {
	// Load first to authorize against the policy's actual team.
	policy, err := svc.ds.Policy(ctx, policyID)
	if err != nil {
		svc.SkipAuth(ctx)
		return err
	}
	if err := svc.authz.Authorize(ctx, policy, automdm.ActionWrite); err != nil {
		return err
	}

	var activityHostID *uint
	var activityHostName *string
	if hostID != nil {
		host, err := svc.ds.HostLite(ctx, *hostID)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "load host for policy reset")
		}
		// A team policy only applies to hosts on that team. Treat other hosts as not
		// found so a team-scoped caller cannot probe host IDs outside their team.
		if policy.TeamID != nil {
			hostTeamID := uint(0)
			if host.TeamID != nil {
				hostTeamID = *host.TeamID
			}
			if hostTeamID != *policy.TeamID {
				return ctxerr.Wrap(ctx, newNotFoundError(), "host is not in the policy's team")
			}
		}
		if err := svc.ds.ResetPolicyForHost(ctx, host.ID, policyID); err != nil {
			return ctxerr.Wrap(ctx, err, "reset policy for host")
		}
		activityHostID = &host.ID
		activityHostName = new(host.DisplayName())
	} else {
		if err := svc.ds.ResetPolicy(ctx, policyID); err != nil {
			return ctxerr.Wrap(ctx, err, "reset policy")
		}
		svc.TriggerPolicyMembershipCleanup(ctx)
	}

	var activityTeamID *int64
	var teamName *string
	switch {
	case policy.TeamID == nil:
		id := int64(-1)
		activityTeamID = &id
	case *policy.TeamID == 0:
		id := int64(0)
		activityTeamID = &id
	default:
		id := int64(*policy.TeamID) //nolint:gosec // policy team IDs are small
		activityTeamID = &id
		if svc.EnterpriseOverrides != nil && svc.EnterpriseOverrides.TeamByIDOrName != nil {
			team, err := svc.EnterpriseOverrides.TeamByIDOrName(ctx, policy.TeamID, nil)
			if err != nil {
				return ctxerr.Wrap(ctx, err, "fetching team details")
			}
			teamName = &team.Name
		}
	}

	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityTypeResetPolicy{
		ID:              policy.ID,
		Name:            policy.Name,
		TeamID:          activityTeamID,
		TeamName:        teamName,
		HostID:          activityHostID,
		HostDisplayName: activityHostName,
	}); err != nil {
		return ctxerr.Wrap(ctx, err, "create activity for policy reset")
	}
	return nil
}

/////////////////////////////////////////////////////////////////////////////////
// List policy automation activities.
/////////////////////////////////////////////////////////////////////////////////

func listPolicyAutomationActivitiesEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.ListPolicyAutomationActivitiesRequest)
	activities, meta, err := svc.ListPolicyAutomationActivities(ctx, req.PolicyID, req.Opts, req.Status)
	if err != nil {
		return automdm.ListPolicyAutomationActivitiesResponse{Err: err}, nil
	}
	resp := automdm.ListPolicyAutomationActivitiesResponse{
		Activities: activities,
		Meta:       meta,
	}
	if meta != nil {
		resp.Count = meta.TotalResults
	}
	return resp, nil
}

func (svc Service) ListPolicyAutomationActivities(ctx context.Context, policyID uint, opts automdm.ListOptions, status string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
	policy, err := svc.ds.Policy(ctx, policyID)
	if err != nil {
		svc.SkipAuth(ctx)
		return nil, nil, err
	}

	if err := svc.authz.Authorize(ctx, policy, automdm.ActionRead); err != nil {
		return nil, nil, err
	}

	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return nil, nil, automdm.ErrNoContext
	}
	filter := automdm.TeamFilter{User: vc.User, IncludeObserver: true}

	switch status {
	case "", "error", "success":
		// valid
	default:
		return nil, nil, automdm.NewInvalidArgumentError("status", `must be "error", "success", or empty`)
	}

	if opts.PerPage == 0 {
		opts.PerPage = 50
	} else if opts.PerPage > maxPolicyAutomationActivitiesPerPage {
		return nil, nil, automdm.NewInvalidArgumentError("per_page", fmt.Sprintf("must be no greater than %d", maxPolicyAutomationActivitiesPerPage))
	}
	if opts.OrderKey == "" {
		// Default to newest activity first.
		opts.OrderKey = "created_at"
		opts.OrderDirection = automdm.OrderDescending
	}
	opts.IncludeMetadata = true

	activities, meta, err := svc.ds.ListPolicyAutomationActivities(ctx, policyID, filter, opts, status)
	if err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "list policy automation activities")
	}
	return activities, meta, nil
}

/////////////////////////////////////////////////////////////////////////////////
// Policies in any scope.
/////////////////////////////////////////////////////////////////////////////////

// authorizePolicyScope authorizes an action on the policies of a scope. A nil teamID is "All teams" and 0 is
// "Unassigned"; any team_id is Premium.
func (svc *Service) authorizePolicyScope(ctx context.Context, teamID *uint, action string) error {
	if teamID != nil && !license.IsPremium(ctx) {
		svc.authz.SkipAuthorization(ctx)
		return automdm.ErrMissingLicense
	}
	return svc.authz.Authorize(ctx, &automdm.Policy{TeamID: teamID}, action)
}

func listPoliciesEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.ListPoliciesRequest)
	policies, err := svc.ListPolicies(ctx, req.TeamID, req.MergeInherited, req.Opts, automdm.PolicyListFilter{
		AutomationType: req.AutomationType, Type: req.Type, Platform: req.Platform,
	})
	if err != nil {
		return automdm.ListPoliciesResponse{Err: err}, nil
	}
	return automdm.ListPoliciesResponse{Policies: policies}, nil
}

func (svc *Service) ListPolicies(ctx context.Context, teamID *uint, mergeInherited bool, opts automdm.ListOptions,
	filter automdm.PolicyListFilter,
) ([]*automdm.Policy, error) {
	if err := svc.authorizePolicyScope(ctx, teamID, automdm.ActionRead); err != nil {
		return nil, err
	}
	if err := svc.checkPolicyScope(ctx, teamID, filter); err != nil {
		return nil, err
	}
	policies, err := svc.ds.ListPoliciesInScope(ctx, teamID, mergeInherited, opts, filter)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "list policies")
	}
	for _, p := range policies {
		if err := svc.populateAutomationsForTeamPolicy(ctx, p); err != nil {
			return nil, ctxerr.Wrapf(ctx, err, "populate automations for policy_id: %d", p.ID)
		}
	}
	if err := svc.populateSoftwareIconURLs(ctx, policies); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "populate software icon urls")
	}
	return policies, nil
}

func countPoliciesEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.CountPoliciesRequest)
	count, err := svc.CountPolicies(ctx, req.TeamID, req.MergeInherited, req.ListOptions.MatchQuery, automdm.PolicyListFilter{
		AutomationType: req.AutomationType, Type: req.Type, Platform: req.Platform,
	})
	if err != nil {
		return automdm.CountPoliciesResponse{Err: err}, nil
	}
	return automdm.CountPoliciesResponse{Count: count}, nil
}

func (svc *Service) CountPolicies(ctx context.Context, teamID *uint, mergeInherited bool, matchQuery string,
	filter automdm.PolicyListFilter,
) (int, error) {
	if err := svc.authorizePolicyScope(ctx, teamID, automdm.ActionRead); err != nil {
		return 0, err
	}
	if err := svc.checkPolicyScope(ctx, teamID, filter); err != nil {
		return 0, err
	}
	count, err := svc.ds.CountPoliciesInScope(ctx, teamID, mergeInherited, matchQuery, filter)
	if err != nil {
		return 0, ctxerr.Wrap(ctx, err, "count policies")
	}
	return count, nil
}

// checkPolicyScope validates the filter and that the team exists.
func (svc *Service) checkPolicyScope(ctx context.Context, teamID *uint, filter automdm.PolicyListFilter) error {
	if err := filter.Validate(); err != nil {
		return ctxerr.Wrap(ctx, err, "validate policy filter")
	}
	if teamID != nil && *teamID > 0 {
		if _, err := svc.ds.TeamLite(ctx, *teamID); err != nil {
			return ctxerr.Wrapf(ctx, err, "loading team %d", *teamID)
		}
	}
	return nil
}

func createPolicyEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.CreatePolicyRequest)
	policy, err := svc.NewPolicy(ctx, *req)
	if err != nil {
		return automdm.CreatePolicyResponse{Err: err}, nil
	}
	return automdm.CreatePolicyResponse{Policy: policy}, nil
}

func (svc *Service) NewPolicy(ctx context.Context, req automdm.CreatePolicyRequest) (*automdm.Policy, error) {
	if err := checkCreateCompanions(req); err != nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, err, "check automation fields")
	}
	if req.TeamID != nil {
		if !license.IsPremium(ctx) {
			svc.authz.SkipAuthorization(ctx)
			return nil, automdm.ErrMissingLicense
		}
		return svc.NewTeamPolicy(ctx, *req.TeamID, automdm.NewTeamPolicyPayload{
			Name:                         req.Name,
			Query:                        req.Query,
			Description:                  req.Description,
			Resolution:                   req.Resolution,
			Platform:                     req.Platform,
			Critical:                     req.Critical,
			SoftwareTitleID:              req.SoftwareTitleID,
			SoftwareInstallerID:          req.SoftwareInstallerID,
			ScriptID:                     req.ScriptID,
			LabelsIncludeAny:             req.LabelsIncludeAny,
			LabelsIncludeAll:             req.LabelsIncludeAll,
			LabelsExcludeAny:             req.LabelsExcludeAny,
			LabelsExcludeAll:             req.LabelsExcludeAll,
			Hidden:                       req.Hidden,
			ContinuousAutomationsEnabled: req.ContinuousAutomationsEnabled,
			Type:                         req.Type,
			PatchWhenClosed:              req.PatchWhenClosed,
			NotifyBeforePatching:         req.NotifyBeforePatching,
			PatchDeadline:                req.PatchDeadline,
			ProfileUUID:                  req.ProfileUUID,
			AutomationType:               req.AutomationType,
		})
	}

	// Without team_id the policy is an "All teams" policy, which users with only team roles can't create. Say so,
	// rather than a generic forbidden.
	if vc, ok := viewer.FromContext(ctx); ok && vc.User != nil && vc.User.GlobalRole == nil && len(vc.User.Teams) > 0 {
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("team_id",
			"team_id is required for users who only have team roles"))
	}
	if err := svc.authz.Authorize(ctx, &automdm.Policy{}, automdm.ActionWrite); err != nil {
		return nil, err
	}
	if err := checkAllTeamsPolicyRequest(req); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "check All teams policy")
	}
	return svc.NewGlobalPolicy(ctx, automdm.PolicyPayload{
		Query:            req.Query,
		Name:             req.Name,
		Description:      req.Description,
		Resolution:       req.Resolution,
		Platform:         req.Platform,
		Critical:         req.Critical,
		Hidden:           req.Hidden,
		LabelsIncludeAny: req.LabelsIncludeAny,
		LabelsIncludeAll: req.LabelsIncludeAll,
		LabelsExcludeAny: req.LabelsExcludeAny,
		LabelsExcludeAll: req.LabelsExcludeAll,
		Type:             automdm.PolicyTypeDynamic,
		AutomationType:   req.AutomationType,
	})
}

// checkAllTeamsPolicyRequest rejects the fields "All teams" policies can't have, which the "All teams" payload doesn't
// carry.
func checkAllTeamsPolicyRequest(req automdm.CreatePolicyRequest) error {
	for _, f := range []struct {
		set bool
		msg string
	}{
		{req.SoftwareTitleID != nil && *req.SoftwareTitleID != 0, errPolicyAllTeamsForSoftware},
		{req.ScriptID != nil && *req.ScriptID != 0, errPolicyAllTeamsForScripts},
		{req.ProfileUUID != nil && *req.ProfileUUID != "", errPolicyAllTeamsForProfiles},
		{req.SoftwareInstallerID != nil && *req.SoftwareInstallerID != 0, errPolicyAllTeamsForSoftwarePackage},
		{req.ContinuousAutomationsEnabled, errPolicyAllTeamsForContinuousAutomations},
		{req.PatchWhenClosed || req.NotifyBeforePatching || req.PatchDeadline, errPolicyAllTeamsForPatchOptions},
		{req.Type != nil && *req.Type == automdm.PolicyTypePatch,
			`"All teams" policy cannot be a patch policy`},
	} {
		if f.set {
			return &automdm.BadRequestError{Message: fmt.Sprintf("policy payload verification: %s", f.msg)}
		}
	}
	return nil
}

func modifyPolicyEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.ModifyPolicyRequest)
	policy, err := svc.ModifyPolicy(ctx, req.PolicyID, req.ModifyPolicyPayload)
	if err != nil {
		return automdm.ModifyPolicyResponse{Err: err}, nil
	}
	return automdm.ModifyPolicyResponse{Policy: policy}, nil
}

// ModifyPolicy updates a policy in any scope. The policy keeps its scope.
func (svc *Service) ModifyPolicy(ctx context.Context, id uint, p automdm.ModifyPolicyPayload) (*automdm.Policy, error) {
	policy, err := svc.ds.Policy(ctx, id)
	if err != nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, err
	}
	if !p.AutomationType.Set {
		if err := svc.authz.Authorize(ctx, &automdm.Policy{TeamID: policy.TeamID}, automdm.ActionWrite); err != nil {
			return nil, err
		}
		if err := checkUpdateCompanions(policy, p); err != nil {
			return nil, ctxerr.Wrap(ctx, err, "check automation fields")
		}
	}
	return svc.modifyPolicy(ctx, policy.TeamID, id, p)
}

func deletePoliciesEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.DeletePoliciesRequest)
	deleted, err := svc.DeletePolicies(ctx, req.IDs)
	if err != nil {
		return automdm.DeletePoliciesResponse{Err: err}, nil
	}
	return automdm.DeletePoliciesResponse{Deleted: deleted}, nil
}

// DeletePolicies deletes policies from any scopes. Each policy is authorized on its own, and nothing is deleted unless
// the user can delete all of them.
func (svc *Service) DeletePolicies(ctx context.Context, ids []uint) ([]uint, error) {
	if len(ids) == 0 {
		return svc.DeleteGlobalPolicies(ctx, ids)
	}
	policiesByID, err := svc.ds.PoliciesByID(ctx, ids)
	if err != nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, err, "getting policies by ID")
	}
	var globalIDs []uint
	teamIDs := make(map[uint][]uint)
	for _, id := range ids {
		policy, ok := policiesByID[id]
		if !ok || policy == nil {
			svc.authz.SkipAuthorization(ctx)
			return nil, ctxerr.Wrap(ctx, newNotFoundError(), "policy not found")
		}
		if err := svc.authz.Authorize(ctx, policy, automdm.ActionWrite); err != nil {
			return nil, err
		}
		if policy.TeamID == nil {
			globalIDs = append(globalIDs, id)
		} else {
			teamIDs[*policy.TeamID] = append(teamIDs[*policy.TeamID], id)
		}
	}

	var deleted []uint
	if len(globalIDs) > 0 {
		d, err := svc.DeleteGlobalPolicies(ctx, globalIDs)
		if err != nil {
			return deleted, err
		}
		deleted = append(deleted, d...)
	}
	for teamID, policyIDs := range teamIDs {
		d, err := svc.DeleteTeamPolicies(ctx, teamID, policyIDs)
		if err != nil {
			return deleted, err
		}
		deleted = append(deleted, d...)
	}
	return deleted, nil
}

package service

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/pkg/removednames"
	"golang.org/x/text/unicode/norm"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

////////////////////////////////////////////////////////////////////////////////
// List Teams
////////////////////////////////////////////////////////////////////////////////

type listTeamsRequest struct {
	ListOptions automdm.ListOptions `url:"list_options"`
}

type listTeamsResponse struct {
	Teams []automdm.Team `json:"teams"`
	Err   error          `json:"error,omitempty"`
}

func (r listTeamsResponse) Error() error { return r.Err }

func listTeamsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*listTeamsRequest)
	teams, err := svc.ListTeams(ctx, req.ListOptions)
	if err != nil {
		return listTeamsResponse{Err: err}, nil
	}

	resp := listTeamsResponse{Teams: []automdm.Team{}}
	for _, team := range teams {
		resp.Teams = append(resp.Teams, *team)
	}
	return resp, nil
}

func (svc *Service) ListTeams(ctx context.Context, opt automdm.ListOptions) ([]*automdm.Team, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Get Team
////////////////////////////////////////////////////////////////////////////////

type getTeamRequest struct {
	ID uint `url:"id"`
}

type getTeamResponse struct {
	Team *automdm.Team `json:"team"`
	Err  error         `json:"error,omitempty"`
}

func (r getTeamResponse) Error() error { return r.Err }

type defaultTeamResponse struct {
	Team *automdm.DefaultTeam `json:"team"`
	Err  error                `json:"error,omitempty"`
}

func (r defaultTeamResponse) Error() error { return r.Err }

func getTeamEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*getTeamRequest)

	team, err := svc.GetTeam(ctx, req.ID)
	if err != nil {
		return getTeamResponse{Err: err}, nil
	}

	// Special handling for team ID 0 - return DefaultTeam structure
	if team.ID == 0 {
		defaultTeam := &automdm.DefaultTeam{
			ID:   team.ID,
			Name: team.Name,
			DefaultTeamConfig: automdm.DefaultTeamConfig{
				WebhookSettings: automdm.DefaultTeamWebhookSettings{
					FailingPoliciesWebhook: team.Config.WebhookSettings.FailingPoliciesWebhook,
					HostActivitiesWebhook:  team.Config.WebhookSettings.HostActivitiesWebhook,
				},
				Integrations: automdm.DefaultTeamIntegrations{
					Jira:    team.Config.Integrations.Jira,
					Zendesk: team.Config.Integrations.Zendesk,
				},
			},
		}
		return defaultTeamResponse{Team: defaultTeam}, nil
	}

	return getTeamResponse{Team: team}, nil
}

func (svc *Service) GetTeam(ctx context.Context, tid uint) (*automdm.Team, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Create Team
////////////////////////////////////////////////////////////////////////////////

type createTeamRequest struct {
	automdm.TeamPayload
}

type teamResponse struct {
	Team *automdm.Team `json:"team,omitempty"`
	Err  error         `json:"error,omitempty"`
}

func (r teamResponse) Error() error { return r.Err }

func createTeamEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*createTeamRequest)

	team, err := svc.NewTeam(ctx, req.TeamPayload)
	if err != nil {
		return teamResponse{Err: err}, nil
	}
	return teamResponse{Team: team}, nil
}

func (svc *Service) NewTeam(ctx context.Context, p automdm.TeamPayload) (*automdm.Team, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Modify Team
////////////////////////////////////////////////////////////////////////////////

type modifyTeamRequest struct {
	ID uint `json:"-" url:"id"`
	automdm.TeamPayload
}

func modifyTeamEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*modifyTeamRequest)

	// AppleOSUpdateSettings.UpdateNewHosts is only used in macOS ... so ignore any values sent for iOS/iPadOS
	if req.TeamPayload.MDM != nil {
		if req.TeamPayload.MDM.IOSUpdates != nil {
			req.TeamPayload.MDM.IOSUpdates.UpdateNewHosts = optjson.Bool{}
		}
		if req.TeamPayload.MDM.IPadOSUpdates != nil {
			req.TeamPayload.MDM.IPadOSUpdates.UpdateNewHosts = optjson.Bool{}
		}
	}

	team, err := svc.ModifyTeam(ctx, req.ID, req.TeamPayload)
	if err != nil {
		return teamResponse{Err: err}, nil
	}

	// Special handling for team ID 0 - return limited fields
	if req.ID == 0 {
		// Convert to DefaultTeam with limited fields
		defaultTeam := &automdm.DefaultTeam{
			ID:   team.ID,
			Name: team.Name,
			DefaultTeamConfig: automdm.DefaultTeamConfig{
				WebhookSettings: automdm.DefaultTeamWebhookSettings{
					FailingPoliciesWebhook: team.Config.WebhookSettings.FailingPoliciesWebhook,
					HostActivitiesWebhook:  team.Config.WebhookSettings.HostActivitiesWebhook,
				},
				Integrations: automdm.DefaultTeamIntegrations{
					Jira:    team.Config.Integrations.Jira,
					Zendesk: team.Config.Integrations.Zendesk,
				},
			},
		}
		return defaultTeamResponse{Team: defaultTeam}, nil
	}

	return teamResponse{Team: team}, err
}

func (svc *Service) ModifyTeam(ctx context.Context, id uint, payload automdm.TeamPayload) (*automdm.Team, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Delete Team
////////////////////////////////////////////////////////////////////////////////

type deleteTeamRequest struct {
	ID uint `url:"id"`
}

type deleteTeamResponse struct {
	Err error `json:"error,omitempty"`
}

func (r deleteTeamResponse) Error() error { return r.Err }

func deleteTeamEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*deleteTeamRequest)
	err := svc.DeleteTeam(ctx, req.ID)
	if err != nil {
		return deleteTeamResponse{Err: err}, nil
	}
	return deleteTeamResponse{}, nil
}

func (svc *Service) DeleteTeam(ctx context.Context, tid uint) error {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Apply Team Specs
////////////////////////////////////////////////////////////////////////////////

type applyTeamSpecsRequest struct {
	Force             bool                                `json:"-" query:"force,optional"`   // if true, bypass strict incoming json validation
	DryRun            bool                                `json:"-" query:"dry_run,optional"` // if true, apply validation but do not save changes
	DryRunAssumptions *automdm.TeamSpecsDryRunAssumptions `json:"dry_run_assumptions,omitempty"`
	Specs             []*automdm.TeamSpec                 `json:"specs"`
}

func (req *applyTeamSpecsRequest) DecodeBody(ctx context.Context, r io.Reader, u url.Values, c []*x509.Certificate) error {
	// force accepts specs containing unknown fields, so decode without rejecting them rather than
	// decoding strictly and ignoring the resulting error. Every other error still has to surface, and
	// other kinds of error can be forced later, after agent options' validations.
	decode := automdm.JSONStrictDecode
	if req.Force {
		decode = automdm.JSONDecode
	}
	if err := decode(r, req); err != nil {
		return ctxerr.Wrap(ctx, automdm.NewUserMessageError(err, http.StatusBadRequest), "strict decode team specs")
	}

	// the MacOSSettings field must be validated separately, since it
	// JSON-decodes into a free-form map.
	for _, spec := range req.Specs {
		if spec == nil || spec.MDM.MacOSSettings == nil {
			continue
		}

		var macOSSettings automdm.MacOSSettings
		validMap := macOSSettings.ToMap()

		// the keys provided must be valid
		for k := range spec.MDM.MacOSSettings {
			if _, ok := validMap[k]; !ok {
				return ctxerr.Wrap(ctx, automdm.NewUserMessageError(
					fmt.Errorf("json: unknown field %q", k),
					http.StatusBadRequest), "strict decode team specs")
			}
		}
	}
	return nil
}

type applyTeamSpecsResponse struct {
	Err           error           `json:"error,omitempty"`
	TeamIDsByName map[string]uint `json:"team_ids_by_name,omitempty"`
}

func (r applyTeamSpecsResponse) Error() error { return r.Err }

func applyTeamSpecsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*applyTeamSpecsRequest)
	if !req.DryRun {
		req.DryRunAssumptions = nil
	}

	// remove any nil spec (may happen in conversion from YAML to JSON with automdmctl, but also
	// with the API should someone send such JSON)
	actualSpecs := make([]*automdm.TeamSpec, 0, len(req.Specs))
	for _, spec := range req.Specs {
		if spec != nil {
			// Normalize the team name for full Unicode support to prevent potential issue further in the spec flow
			spec.Name = norm.NFC.String(spec.Name)
			actualSpecs = append(actualSpecs, spec)
		}
	}

	idsByName, err := svc.ApplyTeamSpecs(
		ctx, actualSpecs, automdm.ApplyTeamSpecOptions{
			ApplySpecOptions: automdm.ApplySpecOptions{
				Force:  req.Force,
				DryRun: req.DryRun,
			},
			DryRunAssumptions: req.DryRunAssumptions,
		})
	if err != nil {
		return applyTeamSpecsResponse{Err: err}, nil
	}
	return applyTeamSpecsResponse{TeamIDsByName: idsByName}, nil
}

func (svc Service) ApplyTeamSpecs(ctx context.Context, _ []*automdm.TeamSpec, _ automdm.ApplyTeamSpecOptions) (map[string]uint, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Modify Team Agent Options
////////////////////////////////////////////////////////////////////////////////

type modifyTeamAgentOptionsRequest struct {
	ID     uint `json:"-" url:"id"`
	Force  bool `json:"-" query:"force,optional"`   // if true, bypass strict incoming json validation
	DryRun bool `json:"-" query:"dry_run,optional"` // if true, apply validation but do not save changes
	json.RawMessage
}

func modifyTeamAgentOptionsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*modifyTeamAgentOptionsRequest)
	team, err := svc.ModifyTeamAgentOptions(ctx, req.ID, req.RawMessage, automdm.ApplySpecOptions{
		Force:  req.Force,
		DryRun: req.DryRun,
	})
	if err != nil {
		return teamResponse{Err: err}, nil
	}
	return teamResponse{Team: team}, err
}

func (svc *Service) ModifyTeamAgentOptions(ctx context.Context, id uint, teamOptions json.RawMessage, applyOptions automdm.ApplySpecOptions) (*automdm.Team, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// List Team Users
////////////////////////////////////////////////////////////////////////////////

type listTeamUsersRequest struct {
	TeamID      uint                `url:"id"`
	ListOptions automdm.ListOptions `url:"list_options"`
}

func listTeamUsersEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*listTeamUsersRequest)
	users, err := svc.ListTeamUsers(ctx, req.TeamID, req.ListOptions)
	if err != nil {
		return listUsersResponse{Err: err}, nil
	}

	resp := listUsersResponse{Users: []automdm.User{}}
	for _, user := range users {
		resp.Users = append(resp.Users, *user)
	}
	return resp, nil
}

func (svc *Service) ListTeamUsers(ctx context.Context, teamID uint, opt automdm.ListOptions) ([]*automdm.User, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Add / Delete Team Users
////////////////////////////////////////////////////////////////////////////////

// same request struct for add and delete
type modifyTeamUsersRequest struct {
	TeamID uint `json:"-" url:"id"`
	// User ID and role must be specified for add users, user ID must be
	// specified for delete users.
	Users []automdm.TeamUser `json:"users"`
}

func addTeamUsersEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*modifyTeamUsersRequest)
	team, err := svc.AddTeamUsers(ctx, req.TeamID, req.Users)
	if err != nil {
		return teamResponse{Err: err}, nil
	}
	return teamResponse{Team: team}, err
}

func (svc *Service) AddTeamUsers(ctx context.Context, teamID uint, users []automdm.TeamUser) (*automdm.Team, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

func deleteTeamUsersEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*modifyTeamUsersRequest)
	team, err := svc.DeleteTeamUsers(ctx, req.TeamID, req.Users)
	if err != nil {
		return teamResponse{Err: err}, nil
	}
	return teamResponse{Team: team}, err
}

func (svc *Service) DeleteTeamUsers(ctx context.Context, teamID uint, users []automdm.TeamUser) (*automdm.Team, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Get enroll secrets for team
////////////////////////////////////////////////////////////////////////////////

type teamEnrollSecretsRequest struct {
	TeamID uint `url:"id"`
}

type teamEnrollSecretsResponse struct {
	Secrets []*automdm.EnrollSecret `json:"secrets"`
	Err     error                   `json:"error,omitempty"`
}

func (r teamEnrollSecretsResponse) Error() error { return r.Err }

func teamEnrollSecretsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*teamEnrollSecretsRequest)
	secrets, err := svc.TeamEnrollSecrets(ctx, req.TeamID)
	if err != nil {
		return teamEnrollSecretsResponse{Err: err}, nil
	}

	return teamEnrollSecretsResponse{Secrets: secrets}, err
}

func (svc *Service) TeamEnrollSecrets(ctx context.Context, teamID uint) ([]*automdm.EnrollSecret, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Modify enroll secrets for team
////////////////////////////////////////////////////////////////////////////////

type modifyTeamEnrollSecretsRequest struct {
	TeamID  uint                   `url:"team_id"`
	Secrets []automdm.EnrollSecret `json:"secrets"`
}

func modifyTeamEnrollSecretsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*modifyTeamEnrollSecretsRequest)
	secrets, err := svc.ModifyTeamEnrollSecrets(ctx, req.TeamID, req.Secrets)
	if err != nil {
		return teamEnrollSecretsResponse{Err: err}, nil
	}

	return teamEnrollSecretsResponse{Secrets: secrets}, err
}

func (svc *Service) ModifyTeamEnrollSecrets(ctx context.Context, teamID uint, secrets []automdm.EnrollSecret) ([]*automdm.EnrollSecret, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

// removedAutoMDMName returns the error reason when name is "No team". "All teams" is the current name of all teams, so it is
// never rejected; acceptsAllTeams is kept for the callers.
func removedAutoMDMName(name string, acceptsAllTeams bool) (string, bool) {
	_ = acceptsAllTeams
	if strings.EqualFold(name, automdm.ReservedNameNoTeam) {
		return removednames.Message(fmt.Sprintf("%q", automdm.ReservedNameNoTeam), fmt.Sprintf("%q", automdm.DisplayNameNoTeam)), true
	}
	return "", false
}

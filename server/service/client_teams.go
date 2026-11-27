package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/platform/endpointer"
)

// ListTeams retrieves the list of teams.
func (c *Client) ListTeams(query string) ([]automdm.Team, error) {
	verb, path := "GET", "/api/latest/automdm/teams"
	var responseBody listTeamsResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query)
	if err != nil {
		return nil, err
	}
	return responseBody.Teams, nil
}

// CreateTeam creates a new team.
func (c *Client) CreateTeam(teamPayload automdm.TeamPayload) (*automdm.Team, error) {
	req := createTeamRequest{
		TeamPayload: teamPayload,
	}
	verb, path := "POST", "/api/latest/automdm/teams"
	var responseBody teamResponse
	err := c.authenticatedRequest(req, verb, path, &responseBody)
	if err != nil {
		return nil, err
	}
	return responseBody.Team, nil
}

func (c *Client) GetTeam(teamID uint) (*automdm.Team, error) {
	verb, path := "GET", fmt.Sprintf("/api/latest/automdm/teams/%d", teamID)
	var responseBody getTeamResponse
	if err := c.authenticatedRequest(nil, verb, path, &responseBody); err != nil {
		return nil, err
	}
	return responseBody.Team, nil
}

// DeleteTeam deletes a team.
func (c *Client) DeleteTeam(teamID uint) error {
	verb, path := "DELETE", "/api/latest/automdm/teams/"+strconv.FormatUint(uint64(teamID), 10)
	var responseBody deleteTeamResponse
	return c.authenticatedRequest(nil, verb, path, &responseBody)
}

// ApplyTeams sends the list of Teams to be applied to the
// AutoMDM instance.
func (c *Client) ApplyTeams(specs []json.RawMessage, opts automdm.ApplyTeamSpecOptions) (map[string]uint, error) {
	verb, path := "POST", "/api/latest/automdm/spec/teams"
	var responseBody applyTeamSpecsResponse
	// Rewrite deprecated key names in each team spec to use the new names.
	rules := endpointer.ExtractAliasRules(automdm.TeamSpec{})
	rewritten := make([]json.RawMessage, len(specs))
	for i, spec := range specs {
		updated, err := endpointer.RewriteOldToNewKeys(spec, rules)
		if err != nil {
			return nil, err
		}
		rewritten[i] = updated
	}
	params := map[string]any{"specs": rewritten}
	if opts.DryRun && opts.DryRunAssumptions != nil {
		params["dry_run_assumptions"] = opts.DryRunAssumptions
	}
	err := c.authenticatedRequestWithQuery(params, verb, path, &responseBody, opts.RawQuery())
	if err != nil {
		return nil, err
	}
	return responseBody.TeamIDsByName, nil
}

// PatchAutoMDM sends a partial update to the specified team.
func (c *Client) PatchAutoMDM(teamID uint, payload automdm.TeamPayload) error {
	verb, path := "PATCH", "/api/latest/automdm/teams/"+strconv.FormatUint(uint64(teamID), 10)
	var resp teamResponse
	return c.authenticatedRequest(payload, verb, path, &resp)
}

// ApplyTeamProfiles sends the list of profiles to be applied for the specified
// team.
func (c *Client) ApplyTeamProfiles(tmName string, profiles []automdm.MDMProfileBatchPayload, opts automdm.ApplyTeamSpecOptions) error {
	verb, path := "POST", "/api/latest/automdm/mdm/profiles/batch"
	query, err := url.ParseQuery(opts.RawQuery())
	if err != nil {
		return err
	}
	query.Add("team_name", tmName)
	if opts.DryRunAssumptions != nil && opts.DryRunAssumptions.WindowsEnabledAndConfigured.Valid {
		query.Add("assume_enabled", strconv.FormatBool(opts.DryRunAssumptions.WindowsEnabledAndConfigured.Value))
	}
	if opts.DryRunAssumptions != nil && opts.DryRunAssumptions.EnableProfileValidation.Valid {
		query.Add("assume_profile_validation", strconv.FormatBool(opts.DryRunAssumptions.EnableProfileValidation.Value))
	}
	err = c.authenticatedRequestWithQuery(map[string]any{"profiles": profiles}, verb, path, nil, query.Encode())
	return rewrapProfileBatchNameErr(err, profiles)
}

// applyDDMAssets sets the complete desired set of Apple DDM assets for the
// given team (empty team name targets "No team"). It is used by GitOps.
func (c *Client) applyDDMAssets(tmName string, assets []automdm.MDMAppleDDMAssetBatchPayload, opts automdm.ApplySpecOptions) error {
	verb, path := "POST", "/api/latest/automdm/assets/batch"
	query, err := url.ParseQuery(opts.RawQuery())
	if err != nil {
		return err
	}
	if tmName != "" {
		query.Add("team_name", tmName)
	}
	return c.authenticatedRequestWithQuery(map[string]any{"assets": assets}, verb, path, nil, query.Encode())
}

// ApplyTeamScripts sends the list of scripts to be applied for the specified
// team.
func (c *Client) ApplyTeamScripts(tmName string, scripts []automdm.ScriptPayload, opts automdm.ApplySpecOptions) ([]automdm.ScriptResponse, error) {
	verb, path := "POST", "/api/latest/automdm/scripts/batch"
	query, err := url.ParseQuery(opts.RawQuery())
	if err != nil {
		return nil, err
	}
	query.Add("team_name", tmName)

	var resp automdm.BatchSetScriptsResponse
	err = c.authenticatedRequestWithQuery(map[string]interface{}{"scripts": scripts}, verb, path, &resp, query.Encode())
	return resp.Scripts, rewrapScriptBatchIndexErr(err, scripts)
}

func (c *Client) ApplyTeamSoftwareInstallers(
	tmName string,
	softwareInstallers []automdm.SoftwareInstallerPayload,
	opts automdm.ApplySpecOptions,
	logFn func(format string, args ...any),
) ([]automdm.SoftwarePackageResponse, []automdm.DeletedSoftwarePackage, []string, error) {
	query, err := url.ParseQuery(opts.RawQuery())
	if err != nil {
		return nil, nil, nil, err
	}
	query.Add("team_name", tmName)
	return c.applySoftwareInstallers(softwareInstallers, query, opts.DryRun, logFn)
}

func (c *Client) ApplyTeamAppStoreAppsAssociation(tmName string, vppBatchPayload []automdm.VPPBatchPayload, opts automdm.ApplySpecOptions) ([]automdm.VPPAppResponse, []string, error) {
	query, err := url.ParseQuery(opts.RawQuery())
	if err != nil {
		return nil, nil, err
	}
	query.Add("team_name", tmName)
	return c.applyAppStoreAppsAssociation(vppBatchPayload, query)
}

func (c *Client) ApplyNoTeamAppStoreAppsAssociation(vppBatchPayload []automdm.VPPBatchPayload, opts automdm.ApplySpecOptions) ([]automdm.VPPAppResponse, []string, error) {
	query, err := url.ParseQuery(opts.RawQuery())
	if err != nil {
		return nil, nil, err
	}
	return c.applyAppStoreAppsAssociation(vppBatchPayload, query)
}

func (c *Client) applyAppStoreAppsAssociation(vppBatchPayload []automdm.VPPBatchPayload, query url.Values) ([]automdm.VPPAppResponse, []string, error) {
	verb, path := "POST", "/api/latest/automdm/software/app_store_apps/batch"
	var appsResponse batchAssociateAppStoreAppsResponse
	err := c.authenticatedRequestWithQuery(map[string]interface{}{"app_store_apps": vppBatchPayload}, verb, path, &appsResponse, query.Encode())
	if err != nil {
		return nil, nil, err
	}
	return matchAppStoreAppCustomIcons(vppBatchPayload, appsResponse.Apps), appsResponse.Categories, nil
}

// matchAppStoreAppCustomIcons hydrates VPP responses with references to icons in the request payload, so we can track
// which API calls to make to add/update/delete icons
func matchAppStoreAppCustomIcons(request []automdm.VPPBatchPayload, response []automdm.VPPAppResponse) []automdm.VPPAppResponse {
	byAdamID := make(map[string]automdm.VPPBatchPayload)
	for _, clientSide := range request {
		byAdamID[clientSide.AppStoreID] = clientSide
	}

	for i := range response {
		serverSide := &response[i]
		if clientSide, ok := byAdamID[serverSide.AppStoreID]; ok {
			serverSide.LocalIconHash = clientSide.IconHash
			serverSide.LocalIconPath = clientSide.IconPath
		}
	}

	return response
}

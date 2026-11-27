package service

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/google/uuid"
	"github.com/micromdm/plist"
	"github.com/stretchr/testify/require"
)

// TestHostNameTemplateSecretScope pins the line between reading a custom
// variable's name (any role) and resolving its value (global roles only): a
// resolved template puts the plaintext where every role on the team can read it.
func (s *integrationMDMTestSuite) TestHostNameTemplateSecretScope() {
	t := s.T()
	ctx := t.Context()

	secretName := "HOST_NAME_SCOPE_" + "VARIABLE"
	secretValue := "canary-3f9a2b7c-" + "resolved-value"
	secretRef := "$AUTOMDM_SECRET_" + secretName

	s.token = s.getTestAdminToken()
	s.Do("PUT", "/api/latest/automdm/spec/secret_variables", automdm.CreateSecretVariablesRequest{
		SecretVariables: []automdm.SecretVariable{{Name: secretName, Value: secretValue}},
	}, http.StatusOK)

	team, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name()})
	require.NoError(t, err)

	mkUser := func(prefix, role string) automdm.User {
		u := automdm.User{
			Name:  prefix,
			Email: fmt.Sprintf("%s-%s@example.com", prefix, uuid.NewString()[:8]),
			Teams: []automdm.UserTeam{{Team: *team, Role: role}},
		}
		require.NoError(t, u.SetPassword(test.GoodPassword, 10, 10))
		_, err := s.ds.NewUser(ctx, &u)
		require.NoError(t, err)
		return u
	}
	teamAdmin := mkUser("hnt-admin", automdm.RoleAdmin)
	teamMaintainer := mkUser("hnt-maint", automdm.RoleMaintainer)
	teamGitOps := mkUser("hnt-gitops", automdm.RoleGitOps)
	teamObserver := mkUser("hnt-observer", automdm.RoleObserver)

	iosHost, iosDevice := s.createAppleMobileHostThenEnrollMDM("ios")
	require.NoError(t, s.ds.SetOrUpdateMDMData(ctx, iosHost.ID, false, true, s.server.URL, false, automdm.WellKnownMDMAutoMDM, "", automdm.PersonalEnrollmentTypeNone))
	require.NoError(t, s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team.ID, []uint{iosHost.ID})))

	asUser := func(u automdm.User) { s.token = s.getCachedUserToken(u.Email, test.GoodPassword) }

	storedTemplate := func() string {
		tm, err := s.ds.TeamWithExtras(ctx, team.ID)
		require.NoError(t, err)
		return tm.Config.MDM.HostNameTemplate
	}
	// No enforcement row means the cron never resolved the template.
	requireNothingResolved := func() {
		require.NoError(t, ReconcileHostDeviceNames(ctx, s.ds, s.mdmCommander, s.logger))
		_, err := s.ds.GetHostDeviceNameEnforcement(ctx, iosHost.UUID)
		require.True(t, automdm.IsNotFound(err), "no enforcement row should exist")

		asUser(teamObserver)
		var cmds listMDMCommandsResponse
		s.DoJSON("GET", "/api/latest/automdm/commands", nil, http.StatusOK, &cmds, "host_identifier", iosHost.UUID)
		for _, c := range cmds.Results {
			require.NotContains(t, c.CommandUUID, automdm.DeviceNameCommandUUIDPrefix)
		}
		var hosts listHostsResponse
		s.DoJSON("GET", "/api/latest/automdm/hosts", nil, http.StatusOK, &hosts, "team_id", fmt.Sprint(team.ID))
		for _, h := range hosts.Hosts {
			require.NotEqual(t, secretValue, h.DisplayName)
		}
	}

	// --- a team-scoped role can read the name but not the value ---
	asUser(teamMaintainer)
	var listResp automdm.ListSecretVariablesResponse
	s.DoJSON("GET", "/api/latest/automdm/custom_variables", nil, http.StatusOK, &listResp)
	var sawName bool
	for _, sv := range listResp.CustomVariables {
		if sv.Name == secretName {
			sawName = true
		}
	}
	require.True(t, sawName)
	s.Do("PUT", "/api/latest/automdm/spec/secret_variables", automdm.CreateSecretVariablesRequest{
		SecretVariables: []automdm.SecretVariable{{Name: secretName, Value: "attacker-overwrite"}},
	}, http.StatusForbidden)

	// --- every team-scoped write path rejects a secret reference ---
	for _, tc := range []struct {
		name string
		user automdm.User
		do   func()
	}{
		{"host_name_template endpoint, team maintainer", teamMaintainer, func() {
			res := s.Do("POST", "/api/latest/automdm/host_name_template",
				updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: secretRef}, http.StatusUnprocessableEntity)
			require.Contains(t, extractServerErrorText(res.Body), secretRef+" can only be used")
		}},
		{"host_name_template endpoint, team admin", teamAdmin, func() {
			s.Do("POST", "/api/latest/automdm/host_name_template",
				updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: secretRef}, http.StatusUnprocessableEntity)
		}},
		{"host_name_template endpoint, automdm gitops", teamGitOps, func() {
			s.Do("POST", "/api/latest/automdm/host_name_template",
				updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: secretRef}, http.StatusUnprocessableEntity)
		}},
		{"modify team, team admin", teamAdmin, func() {
			s.Do("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), automdm.TeamPayload{
				MDM: &automdm.TeamPayloadMDM{HostNameTemplate: optjson.SetString(secretRef)},
			}, http.StatusUnprocessableEntity)
		}},
		{"team spec, automdm gitops", teamGitOps, func() {
			s.Do("POST", "/api/latest/automdm/spec/teams", map[string]any{
				"specs": []map[string]any{{"name": team.Name, "mdm": map[string]any{"name_template": secretRef}}},
			}, http.StatusUnprocessableEntity)
		}},
		{"team spec dry run, automdm gitops", teamGitOps, func() {
			s.Do("POST", "/api/latest/automdm/spec/teams", map[string]any{
				"specs": []map[string]any{{"name": team.Name, "mdm": map[string]any{"name_template": secretRef}}},
			}, http.StatusUnprocessableEntity, "dry_run", "true")
		}},
		{"braced form is not a bypass", teamMaintainer, func() {
			s.Do("POST", "/api/latest/automdm/host_name_template",
				updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: "WS-${AUTOMDM_SECRET_" + secretName + "}"},
				http.StatusUnprocessableEntity)
		}},
		{"an undefined secret gives the same error, not an existence oracle", teamMaintainer, func() {
			res := s.Do("POST", "/api/latest/automdm/host_name_template",
				updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: "$AUTOMDM_SECRET_NOT_DEFINED"},
				http.StatusUnprocessableEntity)
			body := extractServerErrorText(res.Body)
			require.Contains(t, body, "can only be used")
			require.NotContains(t, body, "missing from database")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asUser(tc.user)
			tc.do()
			require.Empty(t, storedTemplate())
			requireNothingResolved()
		})
	}

	// --- a team-scoped role can still use the built-in variables ---
	asUser(teamMaintainer)
	s.Do("POST", "/api/latest/automdm/host_name_template",
		updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"}, http.StatusNoContent)
	require.Equal(t, "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", storedTemplate())

	// --- a global role may reference a secret ---
	s.token = s.getTestAdminToken()
	s.Do("POST", "/api/latest/automdm/host_name_template",
		updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: secretRef}, http.StatusNoContent)
	require.Equal(t, secretRef, storedTemplate())

	require.NoError(t, ReconcileHostDeviceNames(ctx, s.ds, s.mdmCommander, s.logger))
	row, err := s.ds.GetHostDeviceNameEnforcement(ctx, iosHost.UUID)
	require.NoError(t, err)
	require.NotNil(t, row.ExpectedDeviceName)
	require.Equal(t, secretValue, *row.ExpectedDeviceName)

	cmd, err := iosDevice.Idle()
	require.NoError(t, err)
	require.NotNil(t, cmd)
	var settingsCmd struct {
		Command struct {
			Settings []struct {
				Item       string
				DeviceName string
			}
		}
	}
	require.NoError(t, plist.Unmarshal(cmd.Raw, &settingsCmd))
	require.Len(t, settingsCmd.Command.Settings, 1)
	require.Equal(t, secretValue, settingsCmd.Command.Settings[0].DeviceName)

	// --- re-saving the global admin's template is a no-op, not a rejection ---
	asUser(teamAdmin)
	s.Do("POST", "/api/latest/automdm/host_name_template",
		updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: "  " + secretRef + " "}, http.StatusNoContent)
	require.Equal(t, secretRef, storedTemplate())
	s.Do("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), automdm.TeamPayload{
		MDM: &automdm.TeamPayloadMDM{HostNameTemplate: optjson.SetString(secretRef)},
	}, http.StatusOK)
	require.Equal(t, secretRef, storedTemplate())

	// --- changing it is still rejected ---
	res := s.Do("POST", "/api/latest/automdm/host_name_template",
		updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: "X-" + secretRef}, http.StatusUnprocessableEntity)
	require.Contains(t, extractServerErrorText(res.Body), "can only be used")
	require.Equal(t, secretRef, storedTemplate())

	s.token = s.getTestAdminToken()
	s.Do("POST", "/api/latest/automdm/host_name_template",
		updateHostNameTemplateRequest{AutoMDMID: &team.ID, HostNameTemplate: ""}, http.StatusNoContent)
}

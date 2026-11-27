package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func (s *integrationMDMTestSuite) TestRemovedAppleSettingsKeys() {
	t := s.T()

	team, err := s.ds.NewTeam(t.Context(), &automdm.Team{Name: t.Name()})
	require.NoError(t, err)
	automdmPath := fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID)
	const configPath = "/api/latest/automdm/config"
	const specPath = "/api/latest/automdm/spec/teams"

	removedMsg := func(key string) string {
		return `The "` + key + `" parameter is not available anymore. What to use instead: https://automdm.com/learn-more-about/automdm-5-upgrade`
	}
	spec := func(mdm string) string {
		return fmt.Sprintf(`{"specs": [{"name": %q, "mdm": %s}]}`, team.Name, mdm)
	}

	before := appConfigResponse{}
	s.DoJSON("GET", configPath, nil, http.StatusOK, &before)

	for _, tc := range []struct {
		name, method, path, body, removed string
	}{
		{"config macos_settings", "PATCH", configPath, `{"mdm": {"macos_settings": {"enable_disk_encryption": true}}}`, "macos_settings"},
		{"config macos_settings dry run", "PATCH", configPath + "?dry_run=true", `{"mdm": {"macos_settings": {}}}`, "macos_settings"},
		{"config macos_settings force", "PATCH", configPath + "?force=true", `{"mdm": {"macos_settings": {}}}`, "macos_settings"},
		{
			"config old and new together", "PATCH", configPath,
			`{"mdm": {"apple_settings": {"enable_disk_encryption": true}, "macos_settings": {"enable_disk_encryption": true}}}`, "macos_settings",
		},
		{
			"config with another setting", "PATCH", configPath,
			`{"org_info": {"org_name": "set by a rejected request"}, "mdm": {"macos_settings": {}}}`, "macos_settings",
		},
		{"team macos_settings", "PATCH", automdmPath, `{"mdm": {"macos_settings": {"enable_disk_encryption": true}}}`, "macos_settings"},
		{"team spec macos_settings", "POST", specPath, spec(`{"macos_settings": {"enable_disk_encryption": true}}`), "macos_settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := s.DoRaw(tc.method, tc.path, []byte(tc.body), http.StatusUnprocessableEntity)
			require.Contains(t, extractServerErrorText(res.Body), removedMsg(tc.removed))
		})
	}

	for _, path := range []string{"/api/latest/automdm/hosts", "/api/latest/automdm/hosts/count"} {
		res := s.DoRaw("GET", path+"?macos_settings=failed", nil, http.StatusUnprocessableEntity)
		require.Contains(t, extractServerErrorText(res.Body), removedMsg("macos_settings"), path)
		s.DoRaw("GET", path+"?apple_settings=failed", nil, http.StatusOK)
		s.DoRaw("GET", path+"?apple_settings=bogus", nil, http.StatusBadRequest)
	}

	// nothing was saved by the rejected requests
	acResp := appConfigResponse{}
	s.DoJSON("GET", configPath, nil, http.StatusOK, &acResp)
	require.NotEqual(t, "set by a rejected request", acResp.OrgInfo.OrgName)
	require.Equal(t, before.MDM.MacOSSettings.EnableDiskEncryption.Value, acResp.MDM.MacOSSettings.EnableDiskEncryption.Value)
	teamResp := getTeamResponse{}
	s.DoJSON("GET", automdmPath, nil, http.StatusOK, &teamResp)
	require.False(t, teamResp.Team.Config.MDM.MacOSSettings.EnableDiskEncryption.Value)

	// the same words are still accepted where they aren't the removed keys
	s.DoRaw("POST", "/api/latest/automdm/disk_encryption",
		[]byte(fmt.Sprintf(`{"team_id": %d, "macos_settings": {"enable_escrow_disk_encryption_key": true}}`, team.ID)), http.StatusNoContent)

	// the new names are returned alone
	oldKeys := []string{`"macos_settings"`, `"custom_settings"`}
	newKeys := []string{`"apple_settings"`}
	rawBody := func(path string) []byte {
		res := s.DoRaw("GET", path, nil, http.StatusOK)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return body
	}
	for _, path := range []string{configPath, automdmPath, "/api/latest/automdm/teams"} {
		body := string(rawBody(path))
		for _, key := range newKeys {
			require.Contains(t, body, key+":", path)
		}
		for _, key := range oldKeys {
			require.NotContains(t, body, key+":", path)
		}
	}

	// a team's mdm object can be sent back as it was returned
	var got struct {
		AutoMDM struct {
			MDM json.RawMessage `json:"mdm"`
		} `json:"team"`
	}
	require.NoError(t, json.Unmarshal(rawBody(automdmPath), &got))
	require.NotEmpty(t, got.AutoMDM.MDM)
	s.DoRaw("PATCH", automdmPath, []byte(`{"mdm": `+string(got.AutoMDM.MDM)+`}`), http.StatusOK)
	var again struct {
		AutoMDM struct {
			MDM json.RawMessage `json:"mdm"`
		} `json:"team"`
	}
	require.NoError(t, json.Unmarshal(rawBody(automdmPath), &again))
	require.JSONEq(t, string(got.AutoMDM.MDM), string(again.AutoMDM.MDM))

	// host details name the object by its current name only
	host := createOrbitEnrolledHost(t, "darwin", "removed-apple-settings-keys", s.ds)
	var hostResp struct {
		Host struct {
			MDM map[string]json.RawMessage `json:"mdm"`
		} `json:"host"`
	}
	require.NoError(t, json.Unmarshal(rawBody(fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID)), &hostResp))
	require.Contains(t, hostResp.Host.MDM, "apple_settings")
	require.NotContains(t, hostResp.Host.MDM, "macos_settings")
}

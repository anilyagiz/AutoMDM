package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/removednames"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

// TestRemovedTeamNames checks that the old team names are rejected, not silently ignored, on the endpoints whose request types
// already use the team names. A request that still sends one would otherwise apply to No team or to every team.
func (s *integrationEnterpriseTestSuite) TestRemovedTeamNames() {
	t := s.T()
	reportsBefore := len(s.listReports())

	// rejectNamed expects the error to name the field param, which differs from key only when key is a removed value.
	rejectNamed := func(method, path, body, param, key string, query ...string) {
		t.Run(method+" "+path+" "+key, func(t *testing.T) {
			resp := s.DoRaw(method, "/api/latest/automdm"+path, []byte(body), http.StatusUnprocessableEntity, query...)
			var got struct {
				Message string              `json:"message"`
				Errors  []map[string]string `json:"errors"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			require.Equal(t, "Validation Failed", got.Message)
			require.Equal(t, []map[string]string{{
				"name": param, "reason": `The "` + key + `" parameter is not available anymore. What to use instead: ` + removednames.GuideURL,
			}}, got.Errors)
		})
	}
	reject := func(method, path, body, key string, query ...string) {
		rejectNamed(method, path, body, key, key, query...)
	}

	// Request body keys: team roles, selected teams and the team of a created or targeted entity.
	for _, tc := range [][]string{
		{"POST", "/invites", `{"email": "a@example.com", "name": "a", "teams": []}`, "teams"},
		{"PATCH", "/invites/1", `{"teams": []}`, "teams"},
		{"POST", "/users/admin", `{"name": "a", "email": "a@example.com", "teams": []}`, "teams"},
		{"PATCH", "/users/1", `{"teams": []}`, "teams"},
		{"POST", "/users/api_only", `{"name": "a", "teams": []}`, "teams"},
		{"PATCH", "/users/api_only/1", `{"teams": []}`, "teams"},
		{"POST", "/targets", `{"selected": {"teams": [1]}}`, "teams"},
		{"POST", "/targets/count", `{"selected": {"teams": [1]}}`, "teams"},
		{"POST", "/spec/labels", `{"specs": [{"name": "a", "team_id": 1}]}`, "team_id"},
		{"POST", "/reports", `{"name": "removed names", "query": "SELECT 1", "team_id": 1}`, "team_id"},
		{"POST", "/spec/reports", `{"specs": [{"name": "a", "query": "SELECT 1", "team": "a"}]}`, "team"},
		{"POST", "/spec/policies", `{"specs": [{"name": "a", "query": "SELECT 1", "team": "a"}]}`, "team"},
		{"POST", "/automations/reset", `{"team_ids": [1]}`, "team_ids"},
		{"POST", "/scripts/run", `{"script_id": 1, "host_id": 1, "team_id": 1}`, "team_id"},
		{"POST", "/scripts/run/sync", `{"script_id": 1, "host_id": 1, "team_id": 1}`, "team_id"},
		{"PUT", "/setup_experience/software", `{"team_id": 1, "platform": "macos"}`, "team_id"},
		{"POST", "/certificates", `{"name": "a", "team_id": 1}`, "team_id"},
		{"DELETE", "/spec/certificates", `{"team_id": 1, "names": ["a"]}`, "team_id"},
		{"POST", "/software/app_store_apps", `{"app_store_id": "a", "team_id": 1}`, "team_id"},
		{"PATCH", "/software/titles/1/app_store_app", `{"team_id": 1}`, "team_id"},
		{"PATCH", "/teams/1/secrets", `{"secrets": [{"secret": "a", "team_id": 1}]}`, "team_id"},
		{"POST", "/software/automdm_maintained_apps", `{"automdm_maintained_app_id": 1, "team_id": 1}`, "team_id"},
	} {
		reject(tc[0], tc[1], tc[2], tc[3])
	}

	// Keys inside the free-form host filters and the AB and VPP token arrays of the config, with force too, which skips strict
	// decoding.
	for _, tc := range [][]string{
		{"POST", "/hosts/transfer/filter", `{"team_id": null, "filters": {"team_id": 1}}`, "team_id"},
		{"POST", "/hosts/delete", `{"filters": {"team_id": 1}}`, "team_id"},
		{"PATCH", "/config", `{"mdm": {"apple_business": [{"organization_name": "a", "macos_team": "a"}]}}`, "macos_team"},
		{"PATCH", "/config?force=true", `{"mdm": {"apple_business": [{"organization_name": "a", "byod_team": "a"}]}}`, "byod_team"},
		{"PATCH", "/config?force=true", `{"mdm": {"volume_purchasing_program": [{"location": "a", "teams": ["a"]}]}}`, "teams"},
	} {
		reject(tc[0], tc[1], tc[2], tc[3])
	}
	// The old names of Unassigned and All teams, used as team names, in any letter case.
	for _, tc := range [][]string{
		{"PATCH", "/config", `{"mdm": {"apple_business": [{"organization_name": "a", "macos_team": "No team"}]}}`, "mdm.apple_business", "No team", "Unassigned"},
		{"PATCH", "/config", `{"mdm": {"volume_purchasing_program": [{"location": "a", "teams": ["All teams"]}]}}`, "mdm.volume_purchasing_program", "All teams", "All teams"},
		{"POST", "/spec/policies", `{"specs": [{"name": "a", "query": "SELECT 1", "team": "no team"}]}`, "specs.team", "No team", "Unassigned"},
	} {
		t.Run(tc[0]+" "+tc[1]+" "+tc[4], func(t *testing.T) {
			resp := s.DoRaw(tc[0], "/api/latest/automdm"+tc[1], []byte(tc[2]), http.StatusUnprocessableEntity)
			var got struct {
				Errors []map[string]string `json:"errors"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			require.Equal(t, []map[string]string{{"name": tc[3], "reason": removednames.Message(`"`+tc[4]+`"`, `"`+tc[5]+`"`)}}, got.Errors)
		})
	}
	// Policies never took "All teams" as a team name, so "All teams" stays an unknown team.
	s.DoRaw("POST", "/api/latest/automdm/spec/policies", []byte(`{"specs": [{"name": "a", "query": "SELECT 1", "team": "All teams"}]}`),
		http.StatusNotFound)
	// Filters never took other old names, so those stay unknown keys.
	s.DoRaw("POST", "/api/latest/automdm/hosts/delete", []byte(`{"filters": {"teams": 1}}`), http.StatusBadRequest)

	// Multipart uploads: the old field is rejected whether or not team_id is also sent, and in the query string too.
	for _, path := range []string{"/software/package", "/scripts", "/configuration_profiles", "/setup_experience/script", "/bootstrap"} {
		for _, tc := range []struct {
			query  string
			fields map[string]string
		}{
			{fields: map[string]string{"team_id": "0"}},
			{fields: map[string]string{"team_id": "0", "team_id": "0"}},
			{query: "?team_id=0", fields: map[string]string{"team_id": "0"}},
		} {
			t.Run("POST "+path+tc.query+" form field team_id", func(t *testing.T) {
				var body bytes.Buffer
				w := multipart.NewWriter(&body)
				for name, value := range tc.fields {
					require.NoError(t, w.WriteField(name, value))
				}
				require.NoError(t, w.Close())
				resp := s.DoRawWithHeaders("POST", "/api/latest/automdm"+path+tc.query, body.Bytes(), http.StatusUnprocessableEntity,
					map[string]string{"Content-Type": w.FormDataContentType(), "Authorization": "Bearer " + s.token})
				var got struct {
					Errors []map[string]string `json:"errors"`
				}
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
				require.Equal(t, []map[string]string{{
					"name": "team_id", "reason": `The "team_id" parameter is not available anymore. What to use instead: ` + removednames.GuideURL,
				}}, got.Errors)
			})
		}
	}

	// Query parameters are rejected before the body is read, so these requests carry none. The new name next to the old one does not win.
	for _, tc := range [][]string{
		{"GET", "/labels"}, {"GET", "/labels/summary"}, {"GET", "/spec/labels"}, {"POST", "/spec/labels"},
		{"GET", "/reports"}, {"GET", "/reports/1/report"}, {"GET", "/spec/reports/a"},
		{"GET", "/scripts"}, {"GET", "/scripts/batch"}, {"POST", "/scripts/batch"},
		{"GET", "/setup_experience/software"}, {"GET", "/setup_experience/script"}, {"DELETE", "/setup_experience/script"},
		{"GET", "/certificates"},
		{"GET", "/hosts"}, {"GET", "/hosts/count"}, {"GET", "/hosts/report"}, {"GET", "/users"},
		{"GET", "/vulnerabilities"}, {"GET", "/vulnerabilities/CVE-2021-1234"},
		{"GET", "/software/automdm_maintained_apps"}, {"GET", "/software/automdm_maintained_apps/1"}, {"GET", "/software/app_store_apps"},
		{"GET", "/software/titles/1/icon"}, {"PUT", "/software/titles/1/icon"}, {"DELETE", "/software/titles/1/icon"},
	} {
		reject(tc[0], tc[1], "", "team_id", "team_id", "0", "team_id", "1")
	}
	reject("POST", "/scripts/batch", "", "team_name", "team_name", "a")
	reject("POST", "/software/app_store_apps/batch", "", "team_name", "team_name", "a")

	// Removed sort keys are values of order_key, so the error names that parameter.
	for _, tc := range [][]string{
		{"/hosts", "team_name"}, {"/hosts", "team_id"}, {"/labels", "team_id"}, {"/labels/1/hosts", "team_name"},
		{"/policies", "team_id"}, {"/reports", "team_id"}, {"/configuration_profiles", "team_id"},
	} {
		rejectNamed("GET", tc[0], "", "order_key", tc[1], "order_key", tc[1])
	}
	rejectNamed("POST", "/translate", `{"list": [{"type": "team", "payload": {"identifier": "a"}}]}`, "type", "team")

	// The rejected create calls did not create anything, and the new name is accepted.
	require.Len(t, s.listReports(), reportsBefore)

	// A report's result column can be called team_id, so sorting its results by it is allowed.
	var created automdm.CreateQueryResponse
	s.DoJSON("POST", "/api/latest/automdm/reports", &automdm.QueryPayload{Name: new(t.Name()), Query: new("SELECT 1 AS team_id")}, http.StatusOK,
		&created)
	s.Do("GET", fmt.Sprintf("/api/latest/automdm/reports/%d/report", created.Report.ID), nil, http.StatusOK, "order_key", "team_id")

	// Uploads take team_id from the query string, like they took team_id there.
	team, err := s.ds.NewTeam(t.Context(), &automdm.Team{Name: t.Name()})
	require.NoError(t, err)
	body, headers := generateNewScriptMultipartRequest(t, "query_team_id.sh", []byte("echo"), s.token, nil)
	resp := s.DoRawWithHeaders("POST", fmt.Sprintf("/api/latest/automdm/scripts?team_id=%d", team.ID), body.Bytes(), http.StatusOK, headers)
	var createScriptResp automdm.CreateScriptResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&createScriptResp))
	script, err := s.ds.Script(t.Context(), createScriptResp.ScriptID)
	require.NoError(t, err)
	require.Equal(t, &team.ID, script.TeamID)
}

func (s *integrationEnterpriseTestSuite) listReports() []automdm.Query {
	var listResp automdm.ListQueriesResponse
	s.DoJSON("GET", "/api/latest/automdm/reports", nil, http.StatusOK, &listResp, "per_page", "1000", "team_id", "0")
	return listResp.Queries
}

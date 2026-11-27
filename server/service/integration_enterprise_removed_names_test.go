package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/removednames"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

// TestRemovedTeamNames checks the team names around the AutoMDM 5 rename: the old name of Unassigned is rejected, and the
// team names themselves (team_id, teams, ...) are accepted.
func (s *integrationEnterpriseTestSuite) TestRemovedTeamNames() {
	t := s.T()

	// "No team", the old name of Unassigned, used as a team name, in any letter case.
	for _, tc := range [][]string{
		{"PATCH", "/config", `{"mdm": {"apple_business": [{"organization_name": "a", "macos_team": "No team"}]}}`, "mdm.apple_business", "No team", "Unassigned"},
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

	// team_id is accepted as a sort key.
	var created automdm.CreateQueryResponse
	s.DoJSON("POST", "/api/latest/automdm/reports", &automdm.QueryPayload{Name: new(t.Name()), Query: new("SELECT 1 AS team_id")}, http.StatusOK,
		&created)
	s.Do("GET", fmt.Sprintf("/api/latest/automdm/reports/%d/report", created.Report.ID), nil, http.StatusOK, "order_key", "team_id")
	s.Do("GET", "/api/latest/automdm/hosts", nil, http.StatusOK, "order_key", "team_id")

	// Uploads take team_id from the query string.
	team, err := s.ds.NewTeam(t.Context(), &automdm.Team{Name: t.Name()})
	require.NoError(t, err)
	body, headers := generateNewScriptMultipartRequest(t, "query_team_id.sh", []byte("echo"), s.token, nil)
	resp := s.DoRawWithHeaders("POST", fmt.Sprintf("/api/latest/automdm/scripts?team_id=%d", team.ID), body.Bytes(), http.StatusOK, headers)
	var createScriptResp automdm.CreateScriptResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&createScriptResp))
	script, err := s.ds.Script(t.Context(), createScriptResp.ScriptID)
	require.NoError(t, err)
	require.Equal(t, &team.ID, script.TeamID)

	// A request body with team_id is decoded, not rejected.
	s.Do("POST", "/api/latest/automdm/automations/reset", map[string]any{"team_ids": []uint{team.ID}}, http.StatusOK)
}

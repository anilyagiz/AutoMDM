package service

// Policy tests for the core (no-license) suite.
//
// Belongs here: global and team policy CRUD (both the spec and proprietary
// endpoints), cross-team access rules and browsing, autofill, a host's policy
// membership, and the policy count bookkeeping kept on hosts.
//
// Does not belong here: the webhook/automation configuration that fires on policy
// failure (integration_core_activities_test.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/service/contract"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *integrationTestSuite) TestPolicyDeletionLogsActivity() {
	t := s.T()

	admin1 := s.users["admin1@example.com"]
	admin1.GravatarURL = "http://iii.com"
	err := s.ds.SaveUser(context.Background(), &admin1)
	require.NoError(t, err)

	testPolicies := []automdm.PolicyPayload{{
		Name:  "policy1",
		Query: "select * from time;",
	}, {
		Name:  "policy2",
		Query: "select * from osquery_info;",
	}}

	var policyIDs []uint
	for _, policy := range testPolicies {
		var resp automdm.CreatePolicyResponse
		s.DoJSON("POST", "/api/latest/automdm/policies", policy, http.StatusOK, &resp)
		policyIDs = append(policyIDs, resp.Policy.PolicyData.ID)
	}

	// critical is premium only.
	s.DoJSON("POST", "/api/latest/automdm/policies", automdm.PolicyPayload{
		Name:     "policy3",
		Query:    "select * from time;",
		Critical: true,
	}, http.StatusBadRequest, new(struct{}))

	prevActivities := listActivitiesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusOK, &prevActivities)
	require.GreaterOrEqual(t, len(prevActivities.Activities), 2)

	var deletePoliciesResp automdm.DeletePoliciesResponse
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{IDs: policyIDs}, http.StatusOK, &deletePoliciesResp)
	require.Len(t, deletePoliciesResp.Deleted, len(policyIDs))

	newActivities := listActivitiesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusOK, &newActivities)
	require.Equal(t, len(newActivities.Activities), (len(prevActivities.Activities) + 2))

	var prevDeletes []*automdm.Activity
	for _, a := range prevActivities.Activities {
		if a.Type == "deleted_policy" {
			prevDeletes = append(prevDeletes, a)
		}
	}
	var newDeletes []*automdm.Activity
	for _, a := range newActivities.Activities {
		if a.Type == "deleted_policy" {
			newDeletes = append(newDeletes, a)
		}
	}
	require.Equal(t, len(newDeletes), (len(prevDeletes) + 2))

	type policyDetails struct {
		PolicyID   uint   `json:"policy_id"`
		PolicyName string `json:"policy_name"`
	}
	for _, id := range policyIDs {
		found := false
		for _, d := range newDeletes {
			var details policyDetails
			err := json.Unmarshal([]byte(*d.Details), &details)
			require.NoError(t, err)
			require.NotNil(t, details.PolicyID)
			if id == details.PolicyID {
				found = true
			}

		}
		require.True(t, found)
	}
	for _, p := range testPolicies {
		found := false
		for _, d := range newDeletes {
			var details policyDetails
			err := json.Unmarshal([]byte(*d.Details), &details)
			require.NoError(t, err)
			require.NotNil(t, details.PolicyName)
			if p.Name == details.PolicyName {
				found = true
			}

		}
		require.True(t, found)
	}
}

func (s *integrationTestSuite) TestGlobalPolicies() {
	t := s.T()

	// create 3 hosts
	for i := range 3 {
		_, err := s.ds.NewHost(context.Background(), &automdm.Host{
			DetailUpdatedAt: time.Now(),
			LabelUpdatedAt:  time.Now(),
			PolicyUpdatedAt: time.Now(),
			SeenTime:        time.Now().Add(-time.Duration(i) * time.Minute),
			OsqueryHostID:   new(fmt.Sprintf("%s%d", t.Name(), i)),
			NodeKey:         new(fmt.Sprintf("%s%d", t.Name(), i)),
			UUID:            fmt.Sprintf("%s%d", t.Name(), i),
			Hostname:        fmt.Sprintf("%sfoo.local%d", t.Name(), i),
		})
		require.NoError(t, err)
	}

	qr, err := s.ds.NewQuery(context.Background(), &automdm.Query{
		Name:           "TestQuery3",
		Description:    "Some description",
		Query:          "select * from osquery;",
		ObserverCanRun: true,
		Logging:        automdm.LoggingSnapshot,
	})
	require.NoError(t, err)

	// create a global policy
	gpParams := automdm.CreatePolicyRequest{
		Name:        qr.Name,
		Query:       qr.Query,
		Description: qr.Description,
		Resolution:  "some global resolution",
	}
	gpResp := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", gpParams, http.StatusOK, &gpResp)
	require.NotNil(t, gpResp.Policy)
	assert.Equal(t, qr.Name, gpResp.Policy.Name)
	assert.Equal(t, qr.Query, gpResp.Policy.Query)
	assert.Equal(t, qr.Description, gpResp.Policy.Description)
	require.NotNil(t, gpResp.Policy.Resolution)
	assert.Equal(t, "some global resolution", *gpResp.Policy.Resolution)

	// list global policies
	policiesResponse := automdm.ListPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Len(t, policiesResponse.Policies, 1)
	assert.Equal(t, qr.Name, policiesResponse.Policies[0].Name)
	assert.Equal(t, qr.Query, policiesResponse.Policies[0].Query)
	assert.Equal(t, qr.Description, policiesResponse.Policies[0].Description)

	// invalid order_key returns 422
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusUnprocessableEntity, &automdm.ListPoliciesResponse{}, "order_key", "invalid")

	// Get an unexistent policy
	s.Do("GET", fmt.Sprintf("/api/latest/automdm/policies/%d", 9999), nil, http.StatusNotFound)

	singlePolicyResponse := automdm.GetPolicyByIDResponse{}
	singlePolicyURL := fmt.Sprintf("/api/latest/automdm/policies/%d", policiesResponse.Policies[0].ID)
	s.DoJSON("GET", singlePolicyURL, nil, http.StatusOK, &singlePolicyResponse)
	assert.Equal(t, qr.Name, singlePolicyResponse.Policy.Name)
	assert.Equal(t, qr.Query, singlePolicyResponse.Policy.Query)
	assert.Equal(t, qr.Description, singlePolicyResponse.Policy.Description)

	listHostsURL := fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d", policiesResponse.Policies[0].ID)
	listHostsResp := listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Len(t, listHostsResp.Hosts, 3)

	h1 := listHostsResp.Hosts[0]
	h2 := listHostsResp.Hosts[1]

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=passing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Empty(t, listHostsResp.Hosts)

	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(context.Background(), h1.Host, map[uint]*bool{policiesResponse.Policies[0].ID: new(true)}, time.Now(), false, nil)))
	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(context.Background(), h2.Host, map[uint]*bool{policiesResponse.Policies[0].ID: nil}, time.Now(), false, nil)))

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=passing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Len(t, listHostsResp.Hosts, 1)

	// count global policies
	cGPRes := automdm.CountPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies/count", nil, http.StatusOK, &cGPRes)
	assert.Equal(t, 1, cGPRes.Count)

	// count global policies with matching search query
	cGPRes = automdm.CountPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies/count", nil, http.StatusOK, &cGPRes, "query", "estQue")
	assert.Equal(t, 1, cGPRes.Count)

	// count global policies with matching search query containing leading/trailing whitespace
	cGPRes = automdm.CountPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies/count", nil, http.StatusOK, &cGPRes, "query", " estQue    ")
	assert.Equal(t, 1, cGPRes.Count)

	// count global policies with non-matching search query
	cGPRes = automdm.CountPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies/count", nil, http.StatusOK, &cGPRes, "query", "Query4")
	assert.Equal(t, 0, cGPRes.Count)

	// delete the policy
	deletePolicyParams := automdm.DeletePoliciesRequest{IDs: []uint{policiesResponse.Policies[0].ID}}
	deletePolicyResp := automdm.DeletePoliciesResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", deletePolicyParams, http.StatusOK, &deletePolicyResp)

	policiesResponse = automdm.ListPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Empty(t, policiesResponse.Policies)
}

func (s *integrationTestSuite) TestGlobalPoliciesProprietary() {
	t := s.T()

	for i := range 3 {
		_, err := s.ds.NewHost(context.Background(), &automdm.Host{
			DetailUpdatedAt: time.Now(),
			LabelUpdatedAt:  time.Now(),
			PolicyUpdatedAt: time.Now(),
			SeenTime:        time.Now().Add(-time.Duration(i) * time.Minute),
			OsqueryHostID:   new(fmt.Sprintf("%s%d", t.Name(), i)),
			NodeKey:         new(fmt.Sprintf("%s%d", t.Name(), i)),
			UUID:            fmt.Sprintf("%s%d", t.Name(), i),
			Hostname:        fmt.Sprintf("%sfoo.local%d", t.Name(), i),
			Platform:        "darwin",
		})
		require.NoError(t, err)
	}

	qr, err := s.ds.NewQuery(context.Background(), &automdm.Query{
		Name:           "TestQuery321",
		Description:    "Some description",
		Query:          "select * from osquery;",
		ObserverCanRun: true,
		Logging:        automdm.LoggingSnapshot,
	})
	require.NoError(t, err)
	// report_id no longer copies a report into the policy: alone it leaves nothing to save, and with a body the
	// body's SQL is saved, never the report's.
	gpResp0 := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", map[string]any{"report_id": qr.ID}, http.StatusBadRequest, &gpResp0)
	require.Nil(t, gpResp0.Policy)
	s.DoJSON("POST", "/api/latest/automdm/policies", map[string]any{"report_id": qr.ID, "name": "From body", "query": "SELECT 2;"},
		http.StatusOK, &gpResp0)
	require.NotNil(t, gpResp0.Policy)
	require.Equal(t, "From body", gpResp0.Policy.Name)
	require.Equal(t, "SELECT 2;", gpResp0.Policy.Query)
	s.Do("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{IDs: []uint{gpResp0.Policy.ID}}, http.StatusOK)

	gpParams := automdm.CreatePolicyRequest{
		Name:        "TestQuery3",
		Query:       "select * from osquery;",
		Description: "Some description",
		Resolution:  "some global resolution",
		Platform:    "darwin",
	}
	gpResp := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", gpParams, http.StatusOK, &gpResp)
	require.NotNil(t, gpResp.Policy)
	require.NotEmpty(t, gpResp.Policy.ID)
	assert.Equal(t, "TestQuery3", gpResp.Policy.Name)
	assert.Equal(t, "select * from osquery;", gpResp.Policy.Query)
	assert.Equal(t, "Some description", gpResp.Policy.Description)
	require.NotNil(t, gpResp.Policy.Resolution)
	assert.Equal(t, "some global resolution", *gpResp.Policy.Resolution)
	assert.NotNil(t, gpResp.Policy.AuthorID)
	assert.Equal(t, "Test Name admin1@example.com", gpResp.Policy.AuthorName)
	assert.Equal(t, "admin1@example.com", gpResp.Policy.AuthorEmail)
	assert.Equal(t, "darwin", gpResp.Policy.Platform)

	response := s.DoRaw("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", gpResp.Policy.ID), []byte(`{
		"name": "TestQuery4",
		"query": "select * from osquery_info;",
		"description": "Some description updated",
		"resolution": "some global resolution updated"
	}`), http.StatusOK)
	var mgpResp automdm.ModifyPolicyResponse
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	err = json.Unmarshal(responseBody, &mgpResp)
	require.NoError(t, err)

	require.NotNil(t, gpResp.Policy)
	assert.Equal(t, "TestQuery4", mgpResp.Policy.Name)
	assert.Equal(t, "select * from osquery_info;", mgpResp.Policy.Query)
	assert.Equal(t, "Some description updated", mgpResp.Policy.Description)
	require.NotNil(t, mgpResp.Policy.Resolution)
	assert.Equal(t, "some global resolution updated", *mgpResp.Policy.Resolution)
	assert.Equal(t, "darwin", mgpResp.Policy.Platform)
	assert.Equal(t, uint(0), mgpResp.Policy.FailingHostCount)
	assert.Equal(t, uint(0), mgpResp.Policy.PassingHostCount)

	ggpResp := automdm.GetPolicyByIDResponse{}
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/policies/%d", gpResp.Policy.ID), automdm.GetPolicyByIDRequest{}, http.StatusOK, &ggpResp)
	require.NotNil(t, ggpResp.Policy)
	assert.Equal(t, "TestQuery4", ggpResp.Policy.Name)
	assert.Equal(t, "select * from osquery_info;", ggpResp.Policy.Query)
	assert.Equal(t, "Some description updated", ggpResp.Policy.Description)
	require.NotNil(t, ggpResp.Policy.Resolution)
	assert.Equal(t, "some global resolution updated", *ggpResp.Policy.Resolution)
	assert.Equal(t, "darwin", mgpResp.Policy.Platform)
	assert.Equal(t, uint(0), mgpResp.Policy.FailingHostCount)
	assert.Equal(t, uint(0), mgpResp.Policy.PassingHostCount)

	policiesResponse := automdm.ListPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Len(t, policiesResponse.Policies, 1)
	assert.Equal(t, "TestQuery4", policiesResponse.Policies[0].Name)
	assert.Equal(t, "select * from osquery_info;", policiesResponse.Policies[0].Query)
	assert.Equal(t, "Some description updated", policiesResponse.Policies[0].Description)
	require.NotNil(t, policiesResponse.Policies[0].Resolution)
	assert.Equal(t, "some global resolution updated", *policiesResponse.Policies[0].Resolution)
	assert.Equal(t, "darwin", policiesResponse.Policies[0].Platform)
	assert.Equal(t, uint(0), policiesResponse.Policies[0].FailingHostCount)
	assert.Equal(t, uint(0), policiesResponse.Policies[0].PassingHostCount)

	listHostsURL := fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d", policiesResponse.Policies[0].ID)
	listHostsResp := listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Len(t, listHostsResp.Hosts, 3)
	h1 := listHostsResp.Hosts[0]
	h2 := listHostsResp.Hosts[1]

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=passing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Empty(t, listHostsResp.Hosts)

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=failing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Empty(t, listHostsResp.Hosts)

	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(context.Background(), h1.Host, map[uint]*bool{policiesResponse.Policies[0].ID: new(true)}, time.Now(), false, nil)))
	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(context.Background(), h2.Host, map[uint]*bool{policiesResponse.Policies[0].ID: nil}, time.Now(), false, nil)))

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=passing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Len(t, listHostsResp.Hosts, 1)

	response = s.DoRaw("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", gpResp.Policy.ID), []byte(`{
		"query": "select * from users;"
	}`), http.StatusOK)
	responseBody, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	err = json.Unmarshal(responseBody, &mgpResp)
	require.NoError(t, err)

	require.NotNil(t, gpResp.Policy)
	assert.Equal(t, "TestQuery4", mgpResp.Policy.Name)
	assert.Equal(t, "select * from users;", mgpResp.Policy.Query)
	assert.Equal(t, "Some description updated", mgpResp.Policy.Description)
	require.NotNil(t, mgpResp.Policy.Resolution)
	assert.Equal(t, "some global resolution updated", *mgpResp.Policy.Resolution)
	assert.Equal(t, "darwin", mgpResp.Policy.Platform)
	assert.Equal(t, uint(0), mgpResp.Policy.FailingHostCount)
	assert.Equal(t, uint(0), mgpResp.Policy.PassingHostCount)

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=passing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Empty(t, listHostsResp.Hosts)

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=failing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Empty(t, listHostsResp.Hosts)

	policiesResponse = automdm.ListPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Len(t, policiesResponse.Policies, 1)
	assert.Equal(t, "TestQuery4", policiesResponse.Policies[0].Name)
	assert.Equal(t, "select * from users;", policiesResponse.Policies[0].Query)
	assert.Equal(t, "Some description updated", policiesResponse.Policies[0].Description)
	require.NotNil(t, policiesResponse.Policies[0].Resolution)
	assert.Equal(t, "some global resolution updated", *policiesResponse.Policies[0].Resolution)
	assert.Equal(t, "darwin", policiesResponse.Policies[0].Platform)
	assert.Equal(t, uint(0), policiesResponse.Policies[0].FailingHostCount)
	assert.Equal(t, uint(0), policiesResponse.Policies[0].PassingHostCount)

	// Record query executions
	require.NoError(
		t, errOnly(s.ds.RecordPolicyQueryExecutions(
			context.Background(), h1.Host, map[uint]*bool{policiesResponse.Policies[0].ID: new(true)}, time.Now(), false, nil,
		)),
	)
	require.NoError(
		t, errOnly(s.ds.RecordPolicyQueryExecutions(
			context.Background(), h2.Host, map[uint]*bool{policiesResponse.Policies[0].ID: nil}, time.Now(), false, nil,
		)),
	)
	// Update policy stats
	require.NoError(t, s.ds.UpdateHostPolicyCounts(context.Background()))

	// Fetch policy to make sure stats are updated
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Len(t, policiesResponse.Policies, 1)
	assert.Equal(t, uint(0), policiesResponse.Policies[0].FailingHostCount)
	assert.Equal(t, uint(1), policiesResponse.Policies[0].PassingHostCount)

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=passing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Len(t, listHostsResp.Hosts, 1)

	// Modify the platform for the policy, which should clear the policy stats
	response = s.DoRaw("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", gpResp.Policy.ID), []byte(`{
		"platform": "linux"
	}`), http.StatusOK)
	responseBody, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	err = json.Unmarshal(responseBody, &mgpResp)
	require.NoError(t, err)

	require.NotNil(t, gpResp.Policy)
	assert.Equal(t, "TestQuery4", mgpResp.Policy.Name)
	assert.Equal(t, "select * from users;", mgpResp.Policy.Query)
	assert.Equal(t, "Some description updated", mgpResp.Policy.Description)
	require.NotNil(t, mgpResp.Policy.Resolution)
	assert.Equal(t, "some global resolution updated", *mgpResp.Policy.Resolution)
	assert.Equal(t, "linux", mgpResp.Policy.Platform)
	assert.Equal(t, uint(0), mgpResp.Policy.FailingHostCount)
	assert.Equal(t, uint(0), mgpResp.Policy.PassingHostCount)

	// Fetch policy to make sure stats are updated
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Len(t, policiesResponse.Policies, 1)
	assert.Equal(t, uint(0), policiesResponse.Policies[0].FailingHostCount)
	assert.Equal(t, uint(0), policiesResponse.Policies[0].PassingHostCount)

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=passing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Empty(t, listHostsResp.Hosts)

	listHostsURL = fmt.Sprintf("/api/latest/automdm/hosts?policy_id=%d&policy_response=failing", policiesResponse.Policies[0].ID)
	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", listHostsURL, nil, http.StatusOK, &listHostsResp)
	require.Empty(t, listHostsResp.Hosts)

	deletePolicyParams := automdm.DeletePoliciesRequest{IDs: []uint{policiesResponse.Policies[0].ID}}
	deletePolicyResp := automdm.DeletePoliciesResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", deletePolicyParams, http.StatusOK, &deletePolicyResp)

	policiesResponse = automdm.ListPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Empty(t, policiesResponse.Policies)
}

// TestGlobalPoliciesBrowsing tests that team users can browse (read) global policies (see #3722).
func (s *integrationTestSuite) TestGlobalPoliciesBrowsing() {
	t := s.T()

	team, err := s.ds.NewTeam(context.Background(), &automdm.Team{
		ID:          42,
		Name:        "team_for_global_policies_browsing",
		Description: "desc team1",
	})
	require.NoError(t, err)

	gpParams0 := automdm.CreatePolicyRequest{
		Name:  "global policy",
		Query: "select * from osquery;",
	}
	gpResp0 := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", gpParams0, http.StatusOK, &gpResp0)
	require.NotNil(t, gpResp0.Policy)

	email := "team.observer@example.com"
	teamObserver := &automdm.User{
		Name:       "team observer user",
		Email:      email,
		GlobalRole: nil,
		Teams: []automdm.UserTeam{
			{
				Team: *team,
				Role: automdm.RoleObserver,
			},
		},
	}
	password := test.GoodPassword
	require.NoError(t, teamObserver.SetPassword(password, 10, 10))
	_, err = s.ds.NewUser(context.Background(), teamObserver)
	require.NoError(t, err)

	oldToken := s.token
	s.token = s.getTestToken(email, password)
	t.Cleanup(func() {
		s.token = oldToken
	})

	policiesResponse := automdm.ListPoliciesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/policies", nil, http.StatusOK, &policiesResponse)
	require.Len(t, policiesResponse.Policies, 1)
	assert.Equal(t, "global policy", policiesResponse.Policies[0].Name)
	assert.Equal(t, "select * from osquery;", policiesResponse.Policies[0].Query)
}

// TestGetPolicyByIDCrossTeamAccess verifies that GET /api/latest/automdm/policies/{id}
// returns a team policy to a user that has access to the team it belongs to and
// rejects the request with 403 when the requesting user has no role on that team.
// This guards the security fix for the "Cross-Team Policy Data Exposure" disclosure.
func (s *integrationTestSuite) TestGetPolicyByIDCrossTeamAccess() {
	t := s.T()
	ctx := context.Background()

	team1, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name() + "_team1"})
	require.NoError(t, err)
	team2, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name() + "_team2"})
	require.NoError(t, err)

	policy, err := s.ds.NewTeamPolicy(ctx, team1.ID, nil, automdm.PolicyPayload{
		Name:  t.Name() + "_team1_policy",
		Query: "SELECT 1;",
	})
	require.NoError(t, err)

	password := test.GoodPassword

	team1Observer := &automdm.User{
		Name:  "team1 observer",
		Email: t.Name() + "_team1_observer@example.com",
		Teams: []automdm.UserTeam{{Team: *team1, Role: automdm.RoleObserver}},
	}
	require.NoError(t, team1Observer.SetPassword(password, 10, 10))
	_, err = s.ds.NewUser(ctx, team1Observer)
	require.NoError(t, err)

	team2Observer := &automdm.User{
		Name:  "team2 observer",
		Email: t.Name() + "_team2_observer@example.com",
		Teams: []automdm.UserTeam{{Team: *team2, Role: automdm.RoleObserver}},
	}
	require.NoError(t, team2Observer.SetPassword(password, 10, 10))
	_, err = s.ds.NewUser(ctx, team2Observer)
	require.NoError(t, err)

	policyURL := fmt.Sprintf("/api/latest/automdm/policies/%d", policy.ID) // nolint:nilaway // cannot be nil due to previous require

	// A user with access to the policy's team can read it.
	s.setTokenForTest(t, team1Observer.Email, password)
	var okResp automdm.GetPolicyByIDResponse
	s.DoJSON("GET", policyURL, automdm.GetPolicyByIDRequest{}, http.StatusOK, &okResp)
	require.NotNil(t, okResp.Policy)
	assert.Equal(t, policy.ID, okResp.Policy.ID)
	require.NotNil(t, okResp.Policy.TeamID)
	assert.Equal(t, team1.ID, *okResp.Policy.TeamID)

	// A user with no role on the policy's team is forbidden from reading it.
	s.setTokenForTest(t, team2Observer.Email, password)
	s.Do("GET", policyURL, automdm.GetPolicyByIDRequest{}, http.StatusForbidden)
}

func (s *integrationTestSuite) TestDeleteGlobalPoliciesNotExists() {
	t := s.T()

	var resp automdm.DeletePoliciesResponse
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{IDs: []uint{9999999}}, http.StatusNotFound, &resp)

	resp = automdm.DeletePoliciesResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{IDs: []uint{}}, http.StatusOK, &resp)
	require.Empty(t, resp.Deleted)
}

func (s *integrationTestSuite) TestReenrollHostCleansPolicies() {
	t := s.T()
	ctx := context.Background()
	host := s.createHosts(t)[0]

	// set the enroll secret
	var applyResp applyEnrollSecretSpecResponse
	s.DoJSON("POST", "/api/latest/automdm/spec/enroll_secret", applyEnrollSecretSpecRequest{
		Spec: &automdm.EnrollSecretSpec{
			Secrets: []*automdm.EnrollSecret{{Secret: t.Name()}},
		},
	}, http.StatusOK, &applyResp)

	var getHostResp getHostResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID), nil, http.StatusOK, &getHostResp)
	require.Empty(t, getHostResp.Host.Policies)

	// create a policy and make the host fail it
	pol, err := s.ds.NewGlobalPolicy(ctx, nil, automdm.PolicyPayload{Name: t.Name(), Query: "SELECT 1", Platform: host.AutoMDMPlatform()})
	require.NoError(t, err)
	_, err = s.ds.RecordPolicyQueryExecutions(ctx, &automdm.Host{ID: host.ID}, map[uint]*bool{pol.ID: new(false)}, time.Now(), false, nil)
	require.NoError(t, err)

	// refetch the host details
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID), nil, http.StatusOK, &getHostResp)
	require.Len(t, *getHostResp.Host.Policies, 1)

	// re-enroll the host, but using a different platform
	j, err := json.Marshal(&contract.EnrollOsqueryAgentRequest{
		EnrollSecret:   t.Name(),
		HostIdentifier: *host.OsqueryHostID,
		HostDetails:    map[string](map[string]string){"os_version": map[string]string{"platform": "windows"}},
	})
	require.NoError(t, err)

	// prevent the enroll cooldown from being applied
	mysqltest.ExecAdhocSQL(t, s.ds, func(db sqlx.ExtContext) error {
		_, err := db.ExecContext(
			context.Background(),
			"UPDATE hosts SET last_enrolled_at = DATE_SUB(NOW(), INTERVAL '1' HOUR) WHERE id = ?",
			host.ID,
		)
		return err
	})
	var resp contract.EnrollOsqueryAgentResponse
	hres := s.DoRawNoAuth("POST", "/api/osquery/enroll", j, http.StatusOK)
	defer hres.Body.Close()
	require.NoError(t, json.NewDecoder(hres.Body).Decode(&resp))
	require.NotEmpty(t, resp.NodeKey)

	// refetch the host details
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID), nil, http.StatusOK, &getHostResp)

	// policies should be gone
	require.Empty(t, getHostResp.Host.Policies)
}

func (s *integrationTestSuite) TestAutofillPolicies() {
	t := s.T()

	// Ensure AI features are enabled (may have been disabled by a previous test).
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"server_settings": {"ai_features_disabled": false}
	}`), http.StatusOK)

	startMockServer := func(t *testing.T) string {
		// create a test http server
		srv := httptest.NewServer(
			http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "POST" {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					switch r.URL.Path {
					case "/ok":
						var body map[string]any
						err := json.NewDecoder(r.Body).Decode(&body)
						if err != nil {
							t.Log(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						_, _ = w.Write([]byte(`{"risks":"description", "whatWillProbablyHappenDuringMaintenance":"resolution"}`))
					case "/error":
						w.WriteHeader(http.StatusTeapot)
						_, _ = w.Write([]byte(`{}`))
					case "/badBody":
						_, _ = w.Write([]byte(`{bad json}`))
					case "/timeout":
						time.Sleep(2 * time.Second)
						_, _ = w.Write([]byte(`{"risks":"description", "whatWillProbablyHappenDuringMaintenance":"resolution"}`))
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				},
			),
		)
		t.Cleanup(srv.Close)
		return srv.URL
	}
	mockUrl := startMockServer(t)
	originalUrl := getHumanInterpretationFromOsquerySqlUrl
	originalTimeout := getHumanInterpretationFromOsquerySqlTimeout
	t.Cleanup(
		func() {
			getHumanInterpretationFromOsquerySqlUrl = originalUrl
			getHumanInterpretationFromOsquerySqlTimeout = originalTimeout
		},
	)

	req := automdm.AutofillPoliciesRequest{
		SQL: "  ", // empty
	}
	getHumanInterpretationFromOsquerySqlUrl = mockUrl + "/ok"
	// empty sql
	resp := s.Do("POST", "/api/latest/automdm/autofill/policy", req, http.StatusBadRequest)
	assertBodyContains(t, resp, "cannot be empty")

	// good request
	req.SQL = "select 1"
	var res automdm.AutofillPoliciesResponse
	s.DoJSON("POST", "/api/latest/automdm/autofill/policy", req, http.StatusOK, &res)
	assert.Equal(t, "description", res.Description)
	assert.Equal(t, "resolution", res.Resolution)

	// good request with weird characters
	req.SQL = `select * from " with ' and "" \"`
	res = automdm.AutofillPoliciesResponse{}
	s.DoJSON("POST", "/api/latest/automdm/autofill/policy", req, http.StatusOK, &res)
	assert.Equal(t, "description", res.Description)
	assert.Equal(t, "resolution", res.Resolution)

	getHumanInterpretationFromOsquerySqlUrl = mockUrl + "/error"
	resp = s.Do("POST", "/api/latest/automdm/autofill/policy", req, http.StatusUnprocessableEntity)
	assertBodyContains(t, resp, "error from human interpretation of osquery sql")

	getHumanInterpretationFromOsquerySqlUrl = mockUrl + "/badBody"
	resp = s.Do("POST", "/api/latest/automdm/autofill/policy", req, http.StatusUnprocessableEntity)
	assertBodyContains(t, resp, "error unmarshaling response body from human interpretation of osquery sql")

	getHumanInterpretationFromOsquerySqlUrl = mockUrl + "/timeout"
	getHumanInterpretationFromOsquerySqlTimeout = 1 * time.Millisecond
	resp = s.Do("POST", "/api/latest/automdm/autofill/policy", req, http.StatusUnprocessableEntity)
	assertBodyContains(t, resp, "error sending request to get human interpretation from osquery sql")

	// disable AI features
	appConfigSpec := map[string]map[string]bool{
		"server_settings": {"ai_features_disabled": true},
	}
	s.Do("PATCH", "/api/latest/automdm/config", appConfigSpec, http.StatusOK)
	resp = s.Do("POST", "/api/latest/automdm/autofill/policy", req, http.StatusBadRequest)
	assertBodyContains(t, resp, "AI features are disabled")
}

func (s *integrationTestSuite) TestHostWithNoPoliciesClearsPolicyCounts() {
	t := s.T()
	ctx := context.Background()

	team, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "Zoobar"})
	require.NoError(t, err)

	host, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now(),
		NodeKey:         new("foobar"),
		UUID:            "foobar",
		Hostname:        "com.foobar.local",
		Platform:        "linux",
		TeamID:          &team.ID,
	})
	require.NoError(t, err)

	policy, err := s.ds.NewTeamPolicy(ctx, team.ID, nil, automdm.PolicyPayload{
		Name:  "Barfoo",
		Query: "SELECT 1;",
	})
	require.NoError(t, err)

	distributedWriteResp := submitDistributedQueryResultsResponse{}
	s.DoJSON("POST", "/api/osquery/distributed/write", genDistributedReqWithPolicyResults(
		host,
		map[uint]*bool{
			policy.ID: new(false),
		},
	), http.StatusOK, &distributedWriteResp)

	listHostsResp := listHostsResponse{}
	s.DoJSON("GET", "/api/latest/automdm/hosts", nil, http.StatusOK, &listHostsResp)
	require.Len(t, listHostsResp.Hosts, 1)
	require.Equal(t, uint64(1), listHostsResp.Hosts[0].FailingPoliciesCount)

	_, err = s.ds.DeleteTeamPolicies(ctx, team.ID, []uint{policy.ID})
	require.NoError(t, err)

	distributedWriteResp = submitDistributedQueryResultsResponse{}
	results := make(map[string]json.RawMessage)
	results[hostNoPoliciesWildcard] = json.RawMessage("{\"1\": \"1\"}")
	statuses := make(map[string]any)
	statuses[hostNoPoliciesWildcard] = 0
	s.DoJSON("POST", "/api/osquery/distributed/write", submitDistributedQueryResultsRequestShim{
		NodeKey:  *host.NodeKey,
		Results:  results,
		Statuses: statuses,
		Stats:    map[string]*automdm.Stats{},
	}, http.StatusOK, &distributedWriteResp)

	listHostsResp = listHostsResponse{}
	s.DoJSON("GET", "/api/latest/automdm/hosts", nil, http.StatusOK, &listHostsResp)
	require.Len(t, listHostsResp.Hosts, 1)
	require.Equal(t, uint64(0), listHostsResp.Hosts[0].FailingPoliciesCount)
}

func (s *integrationTestSuite) TestTeamPolicyResendConfigProfileRequiresPremium() {
	t := s.T()
	ctx := context.Background()

	team, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name()})
	require.NoError(t, err)

	profileUUID := automdm.MDMAppleProfileUUIDPrefix + uuid.NewString()

	// Create: rejected at decode, before the profile is ever looked up.
	res := s.Do("POST", "/api/latest/automdm/policies",
		&automdm.CreatePolicyRequest{
			TeamID:         &team.ID,
			Name:           "premium resend",
			Query:          "SELECT 1;",
			Platform:       "darwin",
			ProfileUUID:    new(profileUUID),
			AutomationType: new(automdm.PolicyAutomationConfigurationProfile),
		}, http.StatusBadRequest)
	require.Contains(t, extractServerErrorText(res.Body), "requires a premium license")

	// Modify: same decode-time gate.
	pol, err := s.ds.NewTeamPolicy(ctx, team.ID, nil, automdm.PolicyPayload{
		Name:  "premium resend patch",
		Query: "SELECT 1;",
	})
	require.NoError(t, err)

	res = s.Do("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", pol.ID),
		json.RawMessage(fmt.Sprintf(`{"automation_type": "configuration_profile", "profile_uuid": %q}`, profileUUID)), http.StatusBadRequest)
	require.Contains(t, extractServerErrorText(res.Body), "requires a premium license")

	// GitOps spec apply: explicit license check, so this one is a 402.
	res = s.Do("POST", "/api/latest/automdm/spec/policies", automdm.ApplyPolicySpecsRequest{
		Specs: []*automdm.PolicySpec{{
			Name:        "premium resend spec",
			Query:       "SELECT 1;",
			Platform:    "darwin",
			Team:        team.Name,
			ProfileUUID: new(profileUUID),
		}},
	}, http.StatusPaymentRequired)
	require.Contains(t, extractServerErrorText(res.Body), "Requires AutoMDM Premium license")

	// Without profile_uuid the same spec applies cleanly on a free license.
	s.Do("POST", "/api/latest/automdm/spec/policies", automdm.ApplyPolicySpecsRequest{
		Specs: []*automdm.PolicySpec{{
			Name:     "premium resend spec",
			Query:    "SELECT 1;",
			Platform: "darwin",
			Team:     team.Name,
		}},
	}, http.StatusOK)
}

func (s *integrationTestSuite) TestTeamPolicyHiddenRequiresPremium() {
	t := s.T()
	ctx := t.Context()

	team, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name()})
	require.NoError(t, err)

	res := s.Do("POST", "/api/latest/automdm/policies",
		&automdm.CreatePolicyRequest{TeamID: &team.ID, Name: "premium hidden", Query: "SELECT 1;", Hidden: true}, http.StatusBadRequest)
	require.Contains(t, extractServerErrorText(res.Body), "requires a premium license")

	pol, err := s.ds.NewTeamPolicy(ctx, team.ID, nil, automdm.PolicyPayload{Name: "premium hidden patch", Query: "SELECT 1;"})
	require.NoError(t, err)
	res = s.Do("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", pol.ID),
		json.RawMessage(`{"hidden": true}`), http.StatusBadRequest)
	require.Contains(t, extractServerErrorText(res.Body), "requires a premium license")

	// Specs ignore hidden on Free, like critical, so a GitOps run doesn't fail partway.
	s.Do("POST", "/api/latest/automdm/spec/policies", automdm.ApplyPolicySpecsRequest{
		Specs: []*automdm.PolicySpec{{Name: "premium hidden spec", Query: "SELECT 1;", Team: team.Name, Hidden: true}},
	}, http.StatusOK)
	teamPolicies, _, err := s.ds.ListTeamPolicies(ctx, team.ID, automdm.ListOptions{}, automdm.ListOptions{}, "", "")
	require.NoError(t, err)
	specIdx := slices.IndexFunc(teamPolicies, func(p *automdm.Policy) bool { return p.Name == "premium hidden spec" })
	require.NotEqual(t, -1, specIdx)
	require.False(t, teamPolicies[specIdx].Hidden)

	token := "hidden-policies-token"
	createHostAndDeviceToken(t, s.ds, token)
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"?include_hidden_policies=true", nil, http.StatusBadRequest)
	require.Contains(t, extractServerErrorText(res.Body), "option include_hidden_policies requires a premium license")
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/policies?include_hidden_policies=true", nil, http.StatusBadRequest)
	require.Contains(t, extractServerErrorText(res.Body), "option include_hidden_policies requires a premium license")

	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token, nil, http.StatusOK)
	var raw struct {
		Host struct {
			Issues map[string]any `json:"issues"`
		} `json:"host"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
	require.NoError(t, res.Body.Close())
	require.Contains(t, raw.Host.Issues, "failing_policies_count")
	require.NotContains(t, raw.Host.Issues, "failing_unhidden_policies_count")
	require.NotContains(t, raw.Host.Issues, "hidden_policies_count")
}

func (s *integrationTestSuite) TestGlobalPolicyAutomationTypeFree() {
	t := s.T()

	// AutoMDM Free keeps webhook or ticket on "All teams" policies.
	var resp automdm.CreatePolicyResponse
	s.DoJSON("POST", "/api/latest/automdm/policies",
		json.RawMessage(fmt.Sprintf(`{"name": %q, "query": "SELECT 1", "automation_type": "webhook_or_ticket"}`, t.Name())), http.StatusOK, &resp)
	require.NotNil(t, resp.Policy.AutomationType)
	require.Equal(t, automdm.PolicyAutomationWebhookOrTicket, *resp.Policy.AutomationType)
	t.Cleanup(func() {
		_, err := s.ds.DeleteGlobalPolicies(context.Background(), []uint{resp.Policy.ID})
		require.NoError(t, err)
	})

	var modResp automdm.ModifyPolicyResponse
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", resp.Policy.ID), json.RawMessage(`{"automation_type": null}`),
		http.StatusOK, &modResp)
	require.Nil(t, modResp.Policy.AutomationType)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", resp.Policy.ID), json.RawMessage(`{"automation_type": "webhook_or_ticket"}`),
		http.StatusOK, &modResp)
	require.Equal(t, automdm.PolicyAutomationWebhookOrTicket, *modResp.Policy.AutomationType)

	// Other automation types are Premium, checked before the "All teams" rule (covered in the Premium tests).
	s.Do("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", resp.Policy.ID), json.RawMessage(`{"automation_type": "script"}`),
		http.StatusPaymentRequired)
	// On "Unassigned" policies, the other automation types are Premium, as are the fields they stand for. Creating
	// one with team_id is Premium on its own, so the policy is created in the datastore.
	unassigned, err := s.ds.NewTeamPolicy(context.Background(), 0, nil, automdm.PolicyPayload{
		Name: t.Name() + " unassigned", Query: "SELECT 1", Platform: "darwin",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := s.ds.DeleteTeamPolicies(context.Background(), 0, []uint{unassigned.ID})
		require.NoError(t, err)
	})
	s.Do("POST", "/api/latest/automdm/policies", json.RawMessage(fmt.Sprintf(
		`{"name": %q, "query": "SELECT 1", "platform": "darwin", "team_id": 0, "automation_type": "conditional_access"}`, t.Name()+" ca")),
		http.StatusPaymentRequired)
	s.Do("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", unassigned.ID),
		json.RawMessage(`{"automation_type": "conditional_access"}`), http.StatusPaymentRequired)
	var modTeamResp automdm.ModifyPolicyResponse
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", unassigned.ID),
		json.RawMessage(`{"automation_type": "webhook_or_ticket"}`), http.StatusOK, &modTeamResp)
	require.Equal(t, automdm.PolicyAutomationWebhookOrTicket, *modTeamResp.Policy.AutomationType)
	p, err := s.ds.Policy(context.Background(), unassigned.ID)
	require.NoError(t, err)
	require.False(t, p.ConditionalAccessEnabled)
}

func (s *integrationTestSuite) TestCombinedPolicyEndpointsFree() {
	t := s.T()

	// Without team_id, the endpoints serve "All teams" policies on AutoMDM Free, on every API version.
	var created automdm.CreatePolicyResponse
	s.DoJSON("POST", "/api/v1/automdm/policies", json.RawMessage(fmt.Sprintf(`{"name": %q, "query": "SELECT 1"}`, t.Name())), http.StatusOK, &created)
	t.Cleanup(func() {
		_, err := s.ds.DeleteGlobalPolicies(context.Background(), []uint{created.Policy.ID})
		require.NoError(t, err)
	})
	var listResp automdm.ListPoliciesResponse
	s.DoJSON("GET", "/api/v1/automdm/policies", nil, http.StatusOK, &listResp, "query", t.Name())
	require.Len(t, listResp.Policies, 1)
	var countResp automdm.CountPoliciesResponse
	s.DoJSON("GET", "/api/v1/automdm/policies/count", nil, http.StatusOK, &countResp, "query", t.Name())
	require.Equal(t, 1, countResp.Count)
	var getResp automdm.GetPolicyByIDResponse
	s.DoJSON("GET", fmt.Sprintf("/api/v1/automdm/policies/%d", created.Policy.ID), nil, http.StatusOK, &getResp)
	s.Do("PATCH", fmt.Sprintf("/api/v1/automdm/policies/%d", created.Policy.ID), json.RawMessage(`{"description": "x"}`), http.StatusOK)

	// team_id is Premium.
	s.Do("GET", "/api/latest/automdm/policies", nil, http.StatusPaymentRequired, "team_id", "0")
	s.Do("GET", "/api/latest/automdm/policies/count", nil, http.StatusPaymentRequired, "team_id", "0")
	s.Do("POST", "/api/latest/automdm/policies", json.RawMessage(fmt.Sprintf(`{"name": %q, "query": "SELECT 1", "team_id": 0}`, t.Name()+" x")),
		http.StatusPaymentRequired)
}

package service

// GitOps tests for the enterprise (premium) suite.
//
// Belongs here: GitOps mode and exceptions settings, actions taken by GitOps users, and GitOps
// user authorization for specific endpoints such as the org logo upload.
//
// Does not belong here: API-only user endpoint restrictions (integration_enterprise_auth_test.go)
// and the spec endpoints themselves (teams, policies, scripts, software each have their own file).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *integrationEnterpriseTestSuite) TestGitOpsModeConfig() {
	t := s.T()

	res := s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"gitops": { "gitops_mode_enabled": true, "repository_url": "" }
	  }`), http.StatusUnprocessableEntity)
	errMsg := extractServerErrorText(res.Body)
	assert.Contains(t, errMsg, "Repository URL is required when GitOps mode is enabled")

	res = s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"gitops": { "gitops_mode_enabled": true, "repository_url": "a.b.cc" }
	  }`), http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	assert.Contains(t, errMsg, "Git repository URL must include protocol (e.g. https://)")

	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"gitops": { "gitops_mode_enabled": true, "repository_url": "https://a.b.cc" }
	  }`), http.StatusOK)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEnabledGitOpsMode{}.ActivityName(), "", 0)

	// turn off, persists repo url
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"gitops": { "gitops_mode_enabled": false, "repository_url": "https://a.b.cc" }
	  }`), http.StatusOK)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeDisabledGitOpsMode{}.ActivityName(), "", 0)

	// Make sure URL isn't cleared after another setting is changed
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"org_info": {"org_name": "Changes"}
	  }`), http.StatusOK)
	config, err := s.ds.AppConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://a.b.cc", config.GitOpsConfig.RepositoryURL)
}

func (s *integrationEnterpriseTestSuite) TestGitOpsExceptionsConfig() {
	t := s.T()

	// Start from a known state: GitOps mode on, all exceptions disabled.
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"gitops": {
			"gitops_mode_enabled": true,
			"repository_url": "https://example.com/repo",
			"exceptions": { "labels": false, "software": false, "secrets": false }
		}
	}`), http.StatusOK)

	// Enable two exceptions (labels and software); secrets stays false.
	// Two enabled_gitops_exception activities expected — one per flipped field.
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"gitops": { "exceptions": { "labels": true, "software": true, "secrets": false } }
	}`), http.StatusOK)
	enabledExceptions := s.listRecentExceptionActivities(automdm.ActivityTypeEnabledGitOpsException{}.ActivityName())
	assert.Contains(t, enabledExceptions, "labels", "expected activity for enabled labels exception")
	assert.Contains(t, enabledExceptions, "software", "expected activity for enabled software exception")
	assert.NotContains(t, enabledExceptions, "secrets", "no activity expected for unchanged secrets exception")

	config, err := s.ds.AppConfig(context.Background())
	require.NoError(t, err)
	assert.True(t, config.GitOpsConfig.Exceptions.Labels)
	assert.True(t, config.GitOpsConfig.Exceptions.Software)
	assert.False(t, config.GitOpsConfig.Exceptions.Secrets)
	assert.True(t, config.GitOpsConfig.GitopsModeEnabled)
	assert.Equal(t, "https://example.com/repo", config.GitOpsConfig.RepositoryURL)

	// Partial update — only change one exception, others should persist.
	// Capture the last activity id before the update so we can verify no
	// activity is recorded for the fields that did not change.
	lastIDBefore := s.lastActivityMatches("", "", 0)
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"gitops": { "exceptions": { "software": false } }
	}`), http.StatusOK)
	lastID := s.lastActivityMatches(
		automdm.ActivityTypeDisabledGitOpsException{}.ActivityName(),
		`{"exception": "software"}`, 0)
	assert.Greater(t, lastID, lastIDBefore, "a new activity should have been recorded")

	// Save app config with no exception changes — no new exception activity should be recorded.
	lastIDBefore = s.lastActivityMatches("", "", 0)
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"gitops": { "exceptions": { "labels": true, "software": false, "secrets": false } }
	}`), http.StatusOK)
	lastIDAfter := s.lastActivityMatches("", "", 0)
	assert.Equal(t, lastIDBefore, lastIDAfter, "no activity should have been recorded when exceptions are unchanged")

	config, err = s.ds.AppConfig(context.Background())
	require.NoError(t, err)
	assert.True(t, config.GitOpsConfig.Exceptions.Labels, "labels should persist")
	assert.False(t, config.GitOpsConfig.Exceptions.Software, "software should be updated")
	assert.False(t, config.GitOpsConfig.Exceptions.Secrets, "secrets should persist")
	assert.True(t, config.GitOpsConfig.GitopsModeEnabled)
	assert.Equal(t, "https://example.com/repo", config.GitOpsConfig.RepositoryURL)

	// Verify exceptions appear in GET response
	var getResp appConfigResponse
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &getResp)
	assert.True(t, getResp.GitOpsConfig.Exceptions.Labels)
	assert.False(t, getResp.GitOpsConfig.Exceptions.Software)
	assert.False(t, getResp.GitOpsConfig.Exceptions.Secrets)
	assert.True(t, getResp.GitOpsConfig.GitopsModeEnabled)
	assert.Equal(t, "https://example.com/repo", getResp.GitOpsConfig.RepositoryURL)
}

// listRecentExceptionActivities returns the set of exception names found among
// the recent activities of the given type (enabled_gitops_exception or
// disabled_gitops_exception). Used to assert on exception activities without
// depending on ordering when multiple are emitted by a single PATCH.
func (s *integrationEnterpriseTestSuite) listRecentExceptionActivities(activityType string) map[string]struct{} {
	t := s.T()
	var listActivities listActivitiesResponse
	s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusOK,
		&listActivities, "order_key", "id", "order_direction", "desc", "per_page", "10")
	found := map[string]struct{}{}
	for _, act := range listActivities.Activities {
		if act.Type != activityType || act.Details == nil {
			continue
		}
		var d struct {
			Exception string `json:"exception"`
		}
		require.NoError(t, json.Unmarshal([]byte(*act.Details), &d))
		found[d.Exception] = struct{}{}
	}
	return found
}

// TestGitOpsUserActions tests the (non-MDM) permissions listed in ../../docs/Using\ AutoMDM/manage-access.md
func (s *integrationEnterpriseTestSuite) TestGitOpsUserActions() {
	t := s.T()
	ctx := context.Background()

	s.lq.SetQueryResultsCountOverride = func(queryID uint, count int) error {
		return nil
	}
	//
	// Setup test data.
	// All setup actions are authored by a global admin.
	//

	admin, err := s.ds.UserByEmail(ctx, "admin1@example.com")
	require.NoError(t, err)
	h1, err := s.ds.NewHost(ctx, &automdm.Host{
		NodeKey:  new(t.Name() + "1"),
		UUID:     t.Name() + "1",
		Hostname: strings.ReplaceAll(t.Name()+"foo.local", "/", "_"),
	})
	require.NoError(t, err)
	t1, err := s.ds.NewTeam(ctx, &automdm.Team{
		Name: "Foo",
	})
	require.NoError(t, err)
	t2, err := s.ds.NewTeam(ctx, &automdm.Team{
		Name: "Bar",
	})
	require.NoError(t, err)
	t3, err := s.ds.NewTeam(ctx, &automdm.Team{
		Name: "Zoo",
	})
	require.NoError(t, err)
	team1Host, err := s.ds.NewHost(ctx, &automdm.Host{
		NodeKey:  new(t.Name() + "2"),
		UUID:     t.Name() + "2",
		Hostname: strings.ReplaceAll(t.Name()+"zoo.local", "/", "_"),
		TeamID:   &t1.ID,
	})
	require.NoError(t, err)
	globalHost, err := s.ds.NewHost(ctx, &automdm.Host{
		NodeKey:  new(t.Name() + "3"),
		UUID:     t.Name() + "3",
		Hostname: strings.ReplaceAll(t.Name()+"global.local", "/", "_"),
	})
	require.NoError(t, err)
	acr := appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"webhook_settings": {
			"vulnerabilities_webhook": {
				"enable_vulnerabilities_webhook": false
			}
		}
	}`), http.StatusOK, &acr)
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acr)
	require.False(t, acr.WebhookSettings.VulnerabilitiesWebhook.Enable)
	q1, err := s.ds.NewQuery(ctx, &automdm.Query{
		Name:    "Foo",
		Query:   "SELECT * from time;",
		Logging: automdm.LoggingSnapshot,
	})
	require.NoError(t, err)
	cur := createUserResponse{}
	s.DoJSON("POST", "/api/latest/automdm/users/admin", createUserRequest{
		Email:      new("foo42@example.com"),
		Password:   new("p4ssw0rd.123"),
		Name:       new("foo42"),
		GlobalRole: new("maintainer"),
	}, http.StatusOK, &cur)
	maintainer := cur.User
	var carveBeginResp automdm.CarveBeginResponse
	s.DoJSON("POST", "/api/osquery/carve/begin", automdm.CarveBeginRequest{
		NodeKey:    *h1.NodeKey,
		BlockCount: 3,
		BlockSize:  3,
		CarveSize:  8,
		CarveId:    "c1",
		RequestId:  "r1",
	}, http.StatusOK, &carveBeginResp)
	require.NotEmpty(t, carveBeginResp.SessionId)
	lcr := automdm.ListCarvesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/carves", automdm.ListCarvesRequest{}, http.StatusOK, &lcr)
	require.NotEmpty(t, lcr.Carves)
	carveID := lcr.Carves[0].ID
	// Create the global GitOps user we'll use in tests.
	u := &automdm.User{
		Name:       "GitOps",
		Email:      "gitops1@example.com",
		GlobalRole: new(automdm.RoleGitOps),
	}
	require.NoError(t, u.SetPassword(test.GoodPassword, 10, 10))
	_, err = s.ds.NewUser(context.Background(), u)
	require.NoError(t, err)
	// Create a GitOps user for team t1 we'll use in tests.
	u2 := &automdm.User{
		Name:       "GitOps 2",
		Email:      "gitops2@example.com",
		GlobalRole: nil,
		Teams: []automdm.UserTeam{
			{
				Team: *t1,
				Role: automdm.RoleGitOps,
			},
			{
				Team: *t3,
				Role: automdm.RoleGitOps,
			},
		},
	}
	require.NoError(t, u2.SetPassword(test.GoodPassword, 10, 10))
	_, err = s.ds.NewUser(context.Background(), u2)
	require.NoError(t, err)
	gp2, err := s.ds.NewGlobalPolicy(ctx, &admin.ID, automdm.PolicyPayload{
		Name:  "Zoo",
		Query: "SELECT 0;",
	})
	require.NoError(t, err)
	t2p, err := s.ds.NewTeamPolicy(ctx, t2.ID, &admin.ID, automdm.PolicyPayload{
		Name:  "Zoo2",
		Query: "SELECT 2;",
	})
	require.NoError(t, err)
	// Create some test user to test moving from/to teams.
	u3 := &automdm.User{
		Name:       "Test Foo Observer",
		Email:      "test-foo-observer@example.com",
		GlobalRole: nil,
		Teams: []automdm.UserTeam{
			{
				Team: *t1,
				Role: automdm.RoleObserver,
			},
		},
	}
	require.NoError(t, u3.SetPassword(test.GoodPassword, 10, 10))
	_, err = s.ds.NewUser(context.Background(), u3)
	require.NoError(t, err)
	manualLabel1, err := s.ds.NewLabel(ctx, &automdm.Label{
		Name:                "manualLabel1",
		Query:               "SELECT 2;",
		LabelMembershipType: automdm.LabelMembershipTypeManual,
	})
	require.NoError(t, err)

	requireForbiddenMessage := func(method, path string, body any, message string) {
		t.Helper()
		res := s.Do(method, path, body, http.StatusForbidden)
		assert.Equal(t, message, extractServerErrorText(res.Body), "%s %s", method, path)
	}
	// GitOps users get a message for getting a host by identifier or id, listing its software by identifier, deleting it, and
	// transferring it to any team, also when the host doesn't exist, so hosts can't be probed. The host stays where it was.
	noSuchHost := &automdm.Host{ID: 999999, Hostname: "no-such-host"}
	requireGitOpsHostMessages := func(h *automdm.Host, toTeamIDs ...*uint) {
		t.Helper()
		filters := &map[string]any{"query": h.Hostname}
		requireForbiddenMessage("GET", "/api/latest/automdm/hosts/"+h.Hostname, nil, automdm.GitOpsViewHostByIdentifierErrorMessage)
		requireForbiddenMessage("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", h.ID), nil, automdm.GitOpsViewHostByIdentifierErrorMessage)
		requireForbiddenMessage("GET", "/api/latest/automdm/hosts/"+h.Hostname+"/software", nil, automdm.GitOpsViewHostByIdentifierErrorMessage)
		requireForbiddenMessage("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d", h.ID), nil, automdm.GitOpsDeleteHostErrorMessage)
		requireForbiddenMessage("POST", "/api/latest/automdm/hosts/delete", deleteHostsRequest{IDs: []uint{h.ID}},
			automdm.GitOpsDeleteHostErrorMessage)
		requireForbiddenMessage("POST", "/api/latest/automdm/hosts/delete", deleteHostsRequest{Filters: filters},
			automdm.GitOpsDeleteHostErrorMessage)
		for _, toTeamID := range toTeamIDs {
			requireForbiddenMessage("POST", "/api/latest/automdm/hosts/transfer", addHostsToTeamRequest{TeamID: toTeamID, HostIDs: []uint{h.ID}},
				automdm.GitOpsTransferHostErrorMessage)
			requireForbiddenMessage("POST", "/api/latest/automdm/hosts/transfer/filter",
				addHostsToTeamByFilterRequest{TeamID: toTeamID, Filters: filters}, automdm.GitOpsTransferHostErrorMessage)
		}
		if h != noSuchHost {
			stored, err := s.ds.HostLite(ctx, h.ID)
			require.NoError(t, err)
			assert.Equal(t, h.TeamID, stored.TeamID)
		}
	}

	//
	// Start running permission tests with user gitops1.
	//
	s.setTokenForTest(t, "gitops1@example.com", test.GoodPassword)

	// Attempt to retrieve activities, should fail.
	s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusForbidden, &listActivitiesResponse{})

	// Attempt to retrieve hosts, should fail.
	s.DoJSON("GET", "/api/latest/automdm/hosts", nil, http.StatusForbidden, &listHostsResponse{})

	// Attempt to get, delete, or transfer hosts, should fail with a message.
	requireGitOpsHostMessages(h1, &t1.ID, nil)
	requireGitOpsHostMessages(noSuchHost, &t1.ID)

	// Attempt to filter hosts using labels, should fail (label ID 6 is the builtin label "All Hosts")
	s.DoJSON("GET", "/api/latest/automdm/labels/6/hosts", nil, http.StatusOK, &listHostsResponse{})

	// Attempt to create a label, should allow.
	clr := automdm.CreateLabelResponse{}
	s.DoJSON("POST", "/api/latest/automdm/labels", automdm.CreateLabelRequest{
		Name:  "foo",
		Query: "SELECT 1;",
	}, http.StatusOK, &clr)

	// Attempt to modify a label, should allow.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/labels/%d", clr.Label.ID), automdm.ModifyLabelRequest{
		Name: new("foo2"),
	}, http.StatusOK, &automdm.ModifyLabelResponse{})

	// Attempt to get a label, should fail.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/labels/%d", clr.Label.ID), automdm.GetLabelRequest{}, http.StatusOK, &automdm.GetLabelResponse{})

	// Attempt to list all labels, should fail.
	s.DoJSON("GET", "/api/latest/automdm/labels", automdm.ListLabelsRequest{}, http.StatusOK, &automdm.ListLabelsResponse{})

	// Attempt to delete a label, should allow.
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/labels/id/%d", clr.Label.ID), nil, http.StatusOK, &automdm.DeleteLabelByIDResponse{})

	// Listing software is allowed for gitops so they can reconcile software state.
	s.DoJSON("GET", "/api/latest/automdm/software/versions", listSoftwareRequest{}, http.StatusOK, &listSoftwareVersionsResponse{})
	s.DoJSON("GET", "/api/latest/automdm/software/titles", listSoftwareTitlesRequest{}, http.StatusOK, &listSoftwareTitlesResponse{})
	// Getting a single software title or version still requires Host:list, which gitops doesn't have.
	s.DoJSON("GET", "/api/latest/automdm/software/titles/1", getSoftwareTitleRequest{}, http.StatusForbidden, &getSoftwareTitleResponse{})
	s.DoJSON("GET", "/api/latest/automdm/software/versions/1", getSoftwareRequest{}, http.StatusForbidden, &getSoftwareResponse{})

	// Attempt to read app config, should pass.
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &appConfigResponse{})

	// Attempt to write app config, should allow.
	acr = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"webhook_settings": {
			"vulnerabilities_webhook": {
				"enable_vulnerabilities_webhook": true,
				"destination_url": "https://foobar.example.com"
			}
		}
	}`), http.StatusOK, &acr)
	require.True(t, acr.AppConfig.WebhookSettings.VulnerabilitiesWebhook.Enable)
	require.Equal(t, "https://foobar.example.com", acr.AppConfig.WebhookSettings.VulnerabilitiesWebhook.DestinationURL)

	// Attempt to add/remove manual labels to/from a host.
	var addLabelsToHostResp addLabelsToHostResponse
	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/labels", h1.ID), addLabelsToHostRequest{
		Labels: []string{manualLabel1.Name},
	}, http.StatusOK, &addLabelsToHostResp)
	var removeLabelsFromHostResp automdm.RemoveLabelsFromHostResponse
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d/labels", h1.ID), automdm.RemoveLabelsFromHostRequest{
		Labels: []string{manualLabel1.Name},
	}, http.StatusOK, &removeLabelsFromHostResp)

	// Attempt to run live queries synchronously, should fail.
	s.DoJSON("GET", "/api/latest/automdm/reports/run", runLiveQueryRequest{
		HostIDs:  []uint{h1.ID},
		QueryIDs: []uint{q1.ID},
	}, http.StatusForbidden, &runLiveQueryResponse{},
	)

	// Attempt to run live queries asynchronously (new unsaved query), should fail.
	s.DoJSON("POST", "/api/latest/automdm/reports/run", createDistributedQueryCampaignRequest{
		QuerySQL: "SELECT * FROM time;",
		Selected: automdm.HostTargets{
			HostIDs: []uint{h1.ID},
		},
	}, http.StatusForbidden, &runLiveQueryResponse{})

	// Attempt to run live queries asynchronously (saved query), should fail.
	s.DoJSON("POST", "/api/latest/automdm/reports/run", createDistributedQueryCampaignRequest{
		QueryID: new(q1.ID),
		Selected: automdm.HostTargets{
			HostIDs: []uint{h1.ID},
		},
	}, http.StatusForbidden, &runLiveQueryResponse{})

	// Attempt to create queries, should allow.
	cqr := automdm.CreateQueryResponse{}
	s.DoJSON("POST", "/api/latest/automdm/reports", automdm.CreateQueryRequest{
		Name:  new("foo4"),
		Query: new("SELECT * from osquery_info;"),
	}, http.StatusOK, &cqr)
	cqr2 := automdm.CreateQueryResponse{}
	s.DoJSON("POST", "/api/latest/automdm/reports", automdm.CreateQueryRequest{
		Name:  new("foo5"),
		Query: new("SELECT * from os_version;"),
	}, http.StatusOK, &cqr2)
	cqr3 := automdm.CreateQueryResponse{}
	s.DoJSON("POST", "/api/latest/automdm/reports", automdm.CreateQueryRequest{
		Name:  new("foo6"),
		Query: new("SELECT * from processes;"),
	}, http.StatusOK, &cqr3)
	cqr4 := automdm.CreateQueryResponse{}
	s.DoJSON("POST", "/api/latest/automdm/reports", automdm.CreateQueryRequest{
		Name:  new("foo7"),
		Query: new("SELECT * from managed_policies;"),
	}, http.StatusOK, &cqr4)

	// Attempt to edit queries, should allow.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/reports/%d", cqr.Report.ID), automdm.ModifyQueryRequest{
		Name:  new("foo4"),
		Query: new("SELECT * FROM system_info;"),
	}, http.StatusOK, &automdm.ModifyQueryResponse{})

	// Attempt to view a query, should work.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/reports/%d", cqr.Report.ID), automdm.GetQueryRequest{}, http.StatusOK, &automdm.GetQueryResponse{})

	// Attempt to list all queries, should work.
	s.DoJSON("GET", "/api/latest/automdm/reports", automdm.ListQueriesRequest{}, http.StatusOK, &automdm.ListQueriesResponse{})

	// Attempt to delete queries, should allow.
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/reports/%d", cqr.Report.ID), automdm.DeleteQueryByIDRequest{}, http.StatusOK, &automdm.DeleteQueryByIDResponse{})
	s.DoJSON("POST", "/api/latest/automdm/reports/delete", automdm.DeleteQueriesRequest{IDs: []uint{cqr2.Report.ID}}, http.StatusOK, &automdm.DeleteQueriesResponse{})
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/reports/%d", cqr3.Report.ID), automdm.DeleteQueryByIDRequest{}, http.StatusOK, &automdm.DeleteQueryByIDResponse{})

	// Attempt to create a global policy, should allow.
	gplr := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", automdm.CreatePolicyRequest{
		Name:  "foo9",
		Query: "SELECT * from plist;",
	}, http.StatusOK, &gplr)

	// Attempt to edit a global policy, should allow.
	mgplr := automdm.ModifyPolicyResponse{}
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", gplr.Policy.ID), automdm.ModifyPolicyRequest{
		Query: new("SELECT * from plist WHERE path = 'foo';"),
	}, http.StatusOK, &mgplr)

	// Attempt to read a global policy, should allow.
	s.DoJSON(
		"GET", fmt.Sprintf("/api/latest/automdm/policies/%d", gplr.Policy.ID), automdm.GetPolicyByIDRequest{}, http.StatusOK,
		&automdm.GetPolicyByIDResponse{},
	)

	// Attempt to delete a global policy, should allow.
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{
		IDs: []uint{gplr.Policy.ID},
	}, http.StatusOK, &automdm.DeletePoliciesResponse{})

	// Attempt to create a team policy, should allow.
	tplr := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", automdm.CreatePolicyRequest{
		TeamID: new(t1.ID),
		Name:   "foo10",
		Query:  "SELECT * from file;",
	}, http.StatusOK, &tplr)

	// Attempt to edit a team policy, should allow.
	mtplr := automdm.ModifyPolicyResponse{}
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", tplr.Policy.ID), automdm.ModifyPolicyRequest{
		Query: new("SELECT * from file WHERE path = 'foo';"),
	}, http.StatusOK, &mtplr)

	// Attempt to view a team policy, should allow.
	s.DoJSON(
		"GET", fmt.Sprintf("/api/latest/automdm/policies/%d", tplr.Policy.ID), nil, http.StatusOK,
		&automdm.GetPolicyByIDResponse{},
	)

	// Attempt to delete a team policy, should allow.
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{
		IDs: []uint{tplr.Policy.ID},
	}, http.StatusOK, &automdm.DeletePoliciesResponse{})

	// Attempt to create a user, should fail.
	s.DoJSON("POST", "/api/latest/automdm/users/admin", createUserRequest{
		Email:      new("foo10@example.com"),
		Name:       new("foo10"),
		GlobalRole: new("admin"),
	}, http.StatusForbidden, &createUserResponse{})

	// Attempt to modify a user, should fail.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/users/%d", admin.ID), modifyUserRequest{
		GlobalRole: new("observer"),
	}, http.StatusForbidden, &modifyUserResponse{})

	// Attempt to view a user, should fail.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/users/%d", admin.ID), getUserRequest{}, http.StatusForbidden, &getUserResponse{})

	// Attempt to delete a user, should fail.
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/users/%d", admin.ID), deleteUserRequest{}, http.StatusForbidden, &deleteUserResponse{})

	// Attempt to add users to team, should fail (gitops cannot manage team membership).
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d/users", t1.ID), modifyTeamUsersRequest{
		Users: []automdm.TeamUser{
			{
				User: *maintainer,
				Role: "admin",
			},
		},
	}, http.StatusForbidden, &teamResponse{})

	// Attempt to delete users from team, should fail (gitops cannot manage team membership).
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/teams/%d/users", t1.ID), modifyTeamUsersRequest{
		Users: []automdm.TeamUser{
			{
				User: *maintainer,
				Role: "admin",
			},
		},
	}, http.StatusForbidden, &teamResponse{})

	// Attempt to create a team, should allow.
	tr := teamResponse{}
	s.DoJSON("POST", "/api/latest/automdm/teams", createTeamRequest{
		Name: new("foo11"),
	}, http.StatusOK, &tr)

	// Attempt to edit a team, should allow.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", tr.Team.ID), modifyTeamRequest{
		Name: new("foo12"),
	}, http.StatusOK, &teamResponse{})

	// Attempt to edit a team's agent options, should allow.
	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/teams/%d/agent_options", tr.Team.ID), json.RawMessage(`{
		"config": {
			"options": {
				"aws_debug": true
			}
		}
	}`), http.StatusOK, &teamResponse{})

	// Attempt to view a team, should fail.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/teams/%d", tr.Team.ID), getTeamRequest{}, http.StatusForbidden, &teamResponse{})

	// Attempt to delete a team, should allow.
	dtr := deleteTeamResponse{}
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/teams/%d", tr.Team.ID), deleteTeamRequest{}, http.StatusOK, &dtr)

	// Attempt to create/edit enroll secrets, should allow.
	s.DoJSON("POST", "/api/latest/automdm/spec/enroll_secret", applyEnrollSecretSpecRequest{
		Spec: &automdm.EnrollSecretSpec{
			Secrets: []*automdm.EnrollSecret{
				{
					Secret: "foo400",
					TeamID: nil,
				},
				{
					Secret: "foo500",
					TeamID: new(t1.ID),
				},
			},
		},
	}, http.StatusOK, &applyEnrollSecretSpecResponse{})

	// Attempt to get enroll secrets, should fail.
	s.DoJSON("GET", "/api/latest/automdm/spec/enroll_secret", nil, http.StatusForbidden, &getEnrollSecretSpecResponse{})

	// Attempt to get team enroll secret, should fail.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/teams/%d/secrets", t1.ID), teamEnrollSecretsRequest{}, http.StatusForbidden, &teamEnrollSecretsResponse{})

	// Attempt to list carved files, should fail.
	s.DoJSON("GET", "/api/latest/automdm/carves", automdm.ListCarvesRequest{}, http.StatusForbidden, &automdm.ListCarvesResponse{})

	// Attempt to get a carved file, should fail.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/carves/%d", carveID), automdm.GetCarveRequest{}, http.StatusForbidden, &automdm.GetCarveResponse{})

	// Attempt to search hosts, should fail.
	s.DoJSON("POST", "/api/latest/automdm/targets", automdm.SearchTargetsRequest{
		MatchQuery: "foo",
		QueryID:    &q1.ID,
	}, http.StatusForbidden, &automdm.SearchTargetsResponse{})

	// Attempt to count target hosts, should fail.
	s.DoJSON("POST", "/api/latest/automdm/targets/count", automdm.CountTargetsRequest{
		Selected: automdm.HostTargets{
			HostIDs:  []uint{h1.ID},
			LabelIDs: []uint{clr.Label.ID},
			TeamIDs:  []uint{t1.ID},
		},
		QueryID: &q1.ID,
	}, http.StatusForbidden, &automdm.CountTargetsResponse{})

	//
	// Start running permission tests with user gitops2 (which is a GitOps use for team t1).
	//

	s.setTokenForTest(t, "gitops2@example.com", test.GoodPassword)

	// Attempt to get, delete, or transfer hosts of its teams or of no team, to any destination, should fail with a message.
	requireGitOpsHostMessages(team1Host, &t3.ID, nil, &t2.ID)
	requireGitOpsHostMessages(globalHost, &t1.ID)
	requireGitOpsHostMessages(noSuchHost, &t1.ID)

	// Attempt to create queries in global domain, should fail.
	tcqr := automdm.CreateQueryResponse{}
	s.DoJSON("POST", "/api/latest/automdm/reports", automdm.CreateQueryRequest{
		Name:  new("foo600"),
		Query: new("SELECT * from orbit_info;"),
	}, http.StatusForbidden, &tcqr)

	// Attempt to create queries in its team, should allow.
	tcqr = automdm.CreateQueryResponse{}
	s.DoJSON("POST", "/api/latest/automdm/reports", automdm.CreateQueryRequest{
		Name:   new("foo600"),
		Query:  new("SELECT * from orbit_info;"),
		TeamID: &t1.ID,
	}, http.StatusOK, &tcqr)

	// Attempt to edit own query, should allow.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/reports/%d", tcqr.Report.ID), automdm.ModifyQueryRequest{
		Name:  new("foo4"),
		Query: new("SELECT * FROM system_info;"),
	}, http.StatusOK, &automdm.ModifyQueryResponse{})

	// Attempt to delete own query, should allow.
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/reports/%d", tcqr.Report.ID), automdm.DeleteQueryByIDRequest{}, http.StatusOK, &automdm.DeleteQueryByIDResponse{})

	// Attempt to edit query created by somebody else, should fail. A team
	// GitOps user can't read global queries either (unlike every other team
	// role), so this reports not-found rather than forbidden: a forbidden
	// would confirm the global query exists to a caller with no view of it.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/reports/%d", cqr4.Report.ID), automdm.ModifyQueryRequest{
		Name:  new("foo4"),
		Query: new("SELECT * FROM system_info;"),
	}, http.StatusNotFound, &automdm.ModifyQueryResponse{})

	// Attempt to delete query created by somebody else, should fail.
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/reports/%d", cqr4.Report.ID), automdm.DeleteQueryByIDRequest{}, http.StatusNotFound, &automdm.DeleteQueryByIDResponse{})

	// Attempt to create a team report, should allow.
	cqrt1 := automdm.CreateQueryResponse{}
	s.DoJSON("POST", "/api/latest/automdm/reports", automdm.CreateQueryRequest{
		Name:   new("foo8"),
		Query:  new("SELECT * from managed_policies;"),
		TeamID: &t1.ID,
	}, http.StatusOK, &cqrt1)

	// Attempt to add/remove a manual label from a team host, should allow.
	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/labels", team1Host.ID), addLabelsToHostRequest{
		Labels: []string{manualLabel1.Name},
	}, http.StatusOK, &addLabelsToHostResp)
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d/labels", team1Host.ID), automdm.RemoveLabelsFromHostRequest{
		Labels: []string{manualLabel1.Name},
	}, http.StatusOK, &removeLabelsFromHostResp)

	// Attempt to add/remove a manual label from a global host, should not allow.
	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/labels", globalHost.ID), addLabelsToHostRequest{
		Labels: []string{manualLabel1.Name},
	}, http.StatusForbidden, &addLabelsToHostResp)
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d/labels", globalHost.ID), automdm.RemoveLabelsFromHostRequest{
		Labels: []string{manualLabel1.Name},
	}, http.StatusForbidden, &removeLabelsFromHostResp)

	// Attempt to read a global policy, should fail.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/policies/%d", gp2.ID), automdm.GetPolicyByIDRequest{}, http.StatusForbidden, &automdm.GetPolicyByIDResponse{})

	// Attempt to delete a global policy, should fail.
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{
		IDs: []uint{gp2.ID},
	}, http.StatusForbidden, &automdm.DeletePoliciesResponse{})

	// Attempt to create a team policy, should allow.
	ttplr := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", automdm.CreatePolicyRequest{
		TeamID: new(t1.ID),
		Name:   "foo1000",
		Query:  "SELECT * from file;",
	}, http.StatusOK, &ttplr)

	// Attempt to edit a team policy, should allow.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", ttplr.Policy.ID), automdm.ModifyPolicyRequest{
		Query: new("SELECT * from file WHERE path = 'foobar';"),
	}, http.StatusOK, &automdm.ModifyPolicyResponse{})

	// Attempt to edit another team's policy, should fail.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/policies/%d", t2p.ID), automdm.ModifyPolicyRequest{
		Query: new("SELECT * from file WHERE path = 'foobar';"),
	}, http.StatusForbidden, &automdm.ModifyPolicyResponse{})

	// Attempt to view a team policy, should allow.
	s.DoJSON(
		"GET", fmt.Sprintf("/api/latest/automdm/policies/%d", ttplr.Policy.ID), nil, http.StatusOK,
		&automdm.GetPolicyByIDResponse{},
	)

	// Attempt to view another team's policy, should fail.
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/policies/%d", t2p.ID), nil, http.StatusForbidden, &automdm.GetPolicyByIDResponse{})

	// Attempt to delete a team policy, should allow.
	s.DoJSON("POST", "/api/latest/automdm/policies/delete", automdm.DeletePoliciesRequest{
		IDs: []uint{ttplr.Policy.ID},
	}, http.StatusOK, &automdm.DeletePoliciesResponse{})

	// Attempt to edit own team, should allow.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", t1.ID), modifyTeamRequest{
		Name: new("foo123456"),
	}, http.StatusOK, &teamResponse{})

	// Attempt to edit another team, should fail.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", t2.ID), modifyTeamRequest{
		Name: new("foo123456"),
	}, http.StatusForbidden, &teamResponse{})

	// Attempt to edit own team's agent options, should allow.
	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/teams/%d/agent_options", t1.ID), json.RawMessage(`{
		"config": {
			"options": {
				"aws_debug": true
			}
		}
	}`), http.StatusOK, &teamResponse{})

	// Attempt to edit another team's agent options, should fail.
	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/teams/%d/agent_options", t2.ID), json.RawMessage(`{
		"config": {
			"options": {
				"aws_debug": true
			}
		}
	}`), http.StatusForbidden, &teamResponse{})

	// Attempt to add users from team it owns to another team it owns, should fail (gitops cannot manage team membership).
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d/users", t3.ID), modifyTeamUsersRequest{
		Users: []automdm.TeamUser{
			{
				User: *u3,
				Role: "maintainer",
			},
		},
	}, http.StatusForbidden, &teamResponse{})

	// Attempt to delete users from team it owns, should fail (gitops cannot manage team membership).
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/teams/%d/users", t3.ID), modifyTeamUsersRequest{
		Users: []automdm.TeamUser{
			{
				User: *u3,
			},
		},
	}, http.StatusForbidden, &teamResponse{})

	// Attempt to add users to another team it doesn't own, should fail.
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d/users", t2.ID), modifyTeamUsersRequest{
		Users: []automdm.TeamUser{
			{
				User: *u3,
				Role: "maintainer",
			},
		},
	}, http.StatusForbidden, &teamResponse{})

	// Attempt to delete users from team it doesn't own, should fail.
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/teams/%d/users", t2.ID), modifyTeamUsersRequest{
		Users: []automdm.TeamUser{
			{
				User: *u2,
			},
		},
	}, http.StatusForbidden, &teamResponse{})

	// Attempt to search hosts, should fail.
	s.DoJSON("POST", "/api/latest/automdm/targets", automdm.SearchTargetsRequest{
		MatchQuery: "foo",
		QueryID:    &q1.ID,
	}, http.StatusForbidden, &automdm.SearchTargetsResponse{})

	// Attempt to count target hosts, should fail.
	s.DoJSON("POST", "/api/latest/automdm/targets/count", automdm.CountTargetsRequest{
		Selected: automdm.HostTargets{
			HostIDs:  []uint{h1.ID},
			LabelIDs: []uint{clr.Label.ID},
			TeamIDs:  []uint{t1.ID},
		},
		QueryID: &q1.ID,
	}, http.StatusForbidden, &automdm.CountTargetsResponse{})

	// Listing software titles for the team it owns is allowed.
	s.DoJSON("GET", "/api/latest/automdm/software/titles", listSoftwareTitlesRequest{}, http.StatusOK, &listSoftwareTitlesResponse{}, "team_id", fmt.Sprint(t1.ID))
	// Listing software titles globally (no team) is forbidden for team gitops.
	s.DoJSON("GET", "/api/latest/automdm/software/titles", listSoftwareTitlesRequest{}, http.StatusForbidden, &listSoftwareTitlesResponse{})
	// Listing software titles for a team it does not own is forbidden.
	s.DoJSON("GET", "/api/latest/automdm/software/titles", listSoftwareTitlesRequest{}, http.StatusForbidden, &listSoftwareTitlesResponse{}, "team_id", fmt.Sprint(t2.ID))

	//
	// Users with GitOps on t1 and another role on t2 can list hosts, so they get the messages only for t1: for its hosts, and
	// as a transfer destination.
	//
	team2Host, err := s.ds.NewHost(ctx, &automdm.Host{
		NodeKey:  new(t.Name() + "4"),
		UUID:     t.Name() + "4",
		Hostname: strings.ReplaceAll(t.Name()+"bar.local", "/", "_"),
		TeamID:   &t2.ID,
	})
	require.NoError(t, err)
	newMixedRoleUser := func(email, t2Role string) {
		u := &automdm.User{
			Name:  email,
			Email: email,
			Teams: []automdm.UserTeam{{Team: *t1, Role: automdm.RoleGitOps}, {Team: *t2, Role: t2Role}},
		}
		require.NoError(t, u.SetPassword(test.GoodPassword, 10, 10))
		_, err := s.ds.NewUser(ctx, u)
		require.NoError(t, err)
		s.setTokenForTest(t, email, test.GoodPassword)
	}

	newMixedRoleUser("gitops-observer@example.com", automdm.RoleObserver)
	requireForbiddenMessage("GET", "/api/latest/automdm/hosts/"+team1Host.Hostname, nil, automdm.GitOpsViewHostByIdentifierErrorMessage)
	requireForbiddenMessage("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d", team1Host.ID), nil, automdm.GitOpsDeleteHostErrorMessage)
	requireForbiddenMessage("POST", "/api/latest/automdm/hosts/delete", deleteHostsRequest{IDs: []uint{team1Host.ID}},
		automdm.GitOpsDeleteHostErrorMessage)
	s.DoJSON("GET", "/api/latest/automdm/hosts/"+team2Host.Hostname, nil, http.StatusOK, &getHostResponse{})
	requireForbiddenMessage("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d", team2Host.ID), nil, authz.ForbiddenErrorMessage)

	newMixedRoleUser("gitops-maintainer@example.com", automdm.RoleMaintainer)
	requireForbiddenMessage("POST", "/api/latest/automdm/hosts/transfer", addHostsToTeamRequest{TeamID: &t2.ID, HostIDs: []uint{team1Host.ID}},
		automdm.GitOpsTransferHostErrorMessage)
	requireForbiddenMessage("POST", "/api/latest/automdm/hosts/transfer", addHostsToTeamRequest{TeamID: &t1.ID, HostIDs: []uint{team2Host.ID}},
		automdm.GitOpsTransferHostErrorMessage)
	s.DoJSON("POST", "/api/latest/automdm/hosts/transfer", addHostsToTeamRequest{TeamID: &t2.ID, HostIDs: []uint{team2Host.ID}},
		http.StatusOK, &addHostsToTeamResponse{})
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d", team2Host.ID), nil, http.StatusOK, &deleteHostResponse{})
	stored, err := s.ds.HostLite(ctx, team1Host.ID)
	require.NoError(t, err)
	assert.Equal(t, &t1.ID, stored.TeamID)
}

func (s *integrationEnterpriseTestSuite) TestOrgLogoUploadGitOpsAuth() {
	t := s.T()

	pngImg := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pngImg.Set(0, 0, color.RGBA{R: 0, G: 128, B: 0, A: 255})
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, pngImg))
	pngBytes := pngBuf.Bytes()

	gitopsEmail := "gitops-logo-enterprise@example.com"
	gitopsUser := &automdm.User{
		Name:       "GitOps Logo",
		Email:      gitopsEmail,
		GlobalRole: new(automdm.RoleGitOps),
	}
	require.NoError(t, gitopsUser.SetPassword(test.GoodPassword, 10, 10))
	_, err := s.ds.NewUser(t.Context(), gitopsUser)
	require.NoError(t, err)

	s.token = s.getCachedUserToken(gitopsEmail, test.GoodPassword)
	defer func() { s.token = s.getTestAdminToken() }()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("logo", "dark.png")
	require.NoError(t, err)
	_, err = io.Copy(fw, bytes.NewReader(pngBytes))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	s.DoRawWithHeaders("PUT", "/api/v1/automdm/logo?mode=dark", body.Bytes(), http.StatusOK, map[string]string{
		"Content-Type":  w.FormDataContentType(),
		"Accept":        "application/json",
		"Authorization": "Bearer " + s.token,
	})

	var acResp appConfigResponse
	s.DoJSON("GET", "/api/v1/automdm/config", nil, http.StatusOK, &acResp)
	require.Contains(t, acResp.OrgInfo.OrgLogoURLDarkMode, "/api/latest/automdm/logo")

	s.token = s.getTestAdminToken()
	s.Do("DELETE", "/api/v1/automdm/logo", nil, http.StatusOK, "mode", "dark")
}

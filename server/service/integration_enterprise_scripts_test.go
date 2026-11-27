package service

// Script tests for the enterprise (premium) suite.
//
// Belongs here: running scripts on hosts (ad hoc, saved, batch, cancelling), saved script CRUD and
// listing, host script details and soft deletes, scripts in app config and team specs, batch
// apply of scripts, and AutoMDM variables in scripts.
//
// Does not belong here: scripts run by policy automations
// (integration_enterprise_policy_automations_test.go), script-only software packages
// (integration_enterprise_software_installers_test.go), and lock/unlock/wipe
// (integration_enterprise_mdm_test.go).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/scripts"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func (s *integrationEnterpriseTestSuite) TestRunHostScript() {
	t := s.T()

	testRunScriptWaitForResult = 2 * time.Second
	defer func() { testRunScriptWaitForResult = 0 }()

	ctx := context.Background()

	host := createOrbitEnrolledHost(t, "linux", "", s.ds)
	otherHost := createOrbitEnrolledHost(t, "linux", "other", s.ds)

	// attempt to run a script on a non-existing host
	var runResp automdm.RunScriptResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID + 100, ScriptContents: "echo"}, http.StatusNotFound, &runResp)

	// attempt to run an empty script
	res := s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: ""}, http.StatusUnprocessableEntity)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Validation Failed: One of 'script_id', 'script_contents', or 'script_name' is required.")

	// attempt to run an overly long script
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: strings.Repeat("a", automdm.UnsavedScriptMaxRuneLen+1)}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Script is too large. It's limited to 10,000 characters")

	// make sure the host is still seen as "online"
	err := s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now())
	require.NoError(t, err)

	// make sure invalid secrets aren't allowed
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo $AUTOMDM_SECRET_INVALID"}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `$AUTOMDM_SECRET_INVALID`)

	// Upload a valid secret
	secretValue := "abc123"
	req := automdm.CreateSecretVariablesRequest{
		SecretVariables: []automdm.SecretVariable{
			{
				Name:  "AUTOMDM_SECRET_TEST_RUN_HOST_SCRIPT",
				Value: secretValue,
			},
		},
	}
	secretResp := automdm.CreateSecretVariablesResponse{}
	s.DoJSON("PUT", "/api/latest/automdm/spec/secret_variables", req, http.StatusOK, &secretResp)

	// create a valid script execution request
	expectedScriptContents := "echo ${AUTOMDM_SECRET_TEST_RUN_HOST_SCRIPT}"
	expectedScriptContentsWithSecret := fmt.Sprintf("echo %s", secretValue)
	s.DoJSON("POST", "/api/latest/automdm/scripts/run",
		automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: expectedScriptContents}, http.StatusAccepted, &runResp)
	require.Equal(t, host.ID, runResp.HostID)
	require.NotEmpty(t, runResp.ExecutionID)

	result, err := s.ds.GetHostScriptExecutionResult(ctx, runResp.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, host.ID, result.HostID)
	require.Equal(t, expectedScriptContents, result.ScriptContents)
	require.Nil(t, result.ExitCode)

	// get script result
	var scriptResultResp automdm.GetScriptResultResponse
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+runResp.ExecutionID, nil, http.StatusOK, &scriptResultResp)
	require.Equal(t, host.ID, scriptResultResp.HostID)
	require.Equal(t, expectedScriptContents, scriptResultResp.ScriptContents)
	require.Nil(t, scriptResultResp.ExitCode)
	require.False(t, scriptResultResp.HostTimeout)
	require.Contains(t, scriptResultResp.Message, automdm.RunScriptAsyncScriptEnqueuedMsg)

	// an async script doesn't care about timeouts
	now := time.Now()
	mysqltest.ExecAdhocSQL(t, s.ds, func(tx sqlx.ExtContext) error {
		_, err := tx.ExecContext(ctx, `UPDATE host_script_results SET created_at = ? WHERE execution_id = ?`,
			now.Add(-1*time.Hour),
			runResp.ExecutionID,
		)
		return err
	})
	scriptResultResp = automdm.GetScriptResultResponse{}
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+runResp.ExecutionID, nil, http.StatusOK, &scriptResultResp)
	require.Equal(t, host.ID, scriptResultResp.HostID)
	require.Equal(t, expectedScriptContents, scriptResultResp.ScriptContents)
	require.Nil(t, scriptResultResp.ExitCode)
	require.False(t, scriptResultResp.HostTimeout)
	require.Contains(t, scriptResultResp.Message, automdm.RunScriptAsyncScriptEnqueuedMsg)

	// send the removed scripts_disabled setting as true and as false, both config updates should fail
	removedSettingMsg := `The "scripts_disabled" parameter is not available anymore. What to use instead: https://automdm.com/learn-more-about/automdm-5-upgrade`
	res = s.DoRaw("PATCH", "/api/latest/automdm/config", []byte(`{"server_settings": {"scripts_disabled": true}}`), http.StatusUnprocessableEntity)
	require.Contains(t, extractServerErrorText(res.Body), removedSettingMsg)
	res = s.DoRaw("PATCH", "/api/latest/automdm/config", []byte(`{"server_settings": {"scripts_disabled": false}}`), http.StatusUnprocessableEntity)
	require.Contains(t, extractServerErrorText(res.Body), removedSettingMsg)

	// read the config, the removed setting should not be returned
	res = s.DoRaw("GET", "/api/latest/automdm/config", nil, http.StatusOK)
	configBody, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NotContains(t, string(configBody), "scripts_disabled")

	var orbitResp automdm.OrbitGetConfigResponse
	// verify that orbit would get the notification that it has a script to run
	s.DoJSON("POST", "/api/automdm/orbit/config",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q}`, *host.OrbitNodeKey)),
		http.StatusOK, &orbitResp)
	require.Equal(t, []string{scriptResultResp.ExecutionID}, orbitResp.Notifications.PendingScriptExecutionIDs)

	// the orbit endpoint to get a pending script to execute returns it
	var orbitGetScriptResp automdm.OrbitGetScriptResponse
	s.DoJSON("POST", "/api/automdm/orbit/scripts/request",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q}`, *host.OrbitNodeKey, scriptResultResp.ExecutionID)),
		http.StatusOK, &orbitGetScriptResp)
	require.Equal(t, host.ID, orbitGetScriptResp.HostID)
	require.Equal(t, scriptResultResp.ExecutionID, orbitGetScriptResp.ExecutionID)
	require.Equal(t, expectedScriptContentsWithSecret, orbitGetScriptResp.ScriptContents)

	// trying to get that script via its execution ID but a different host returns not found
	s.DoJSON("POST", "/api/automdm/orbit/scripts/request",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q}`, *otherHost.OrbitNodeKey, scriptResultResp.ExecutionID)),
		http.StatusNotFound, &orbitGetScriptResp)

	// trying to get an unknown execution id returns not found
	s.DoJSON("POST", "/api/automdm/orbit/scripts/request",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q}`, *host.OrbitNodeKey, scriptResultResp.ExecutionID+"no-such")),
		http.StatusNotFound, &orbitGetScriptResp)

	// attempt to run a sync script on a non-existing host
	var runSyncResp automdm.RunScriptSyncResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID + 100, ScriptContents: "echo"}, http.StatusNotFound, &runSyncResp)

	// attempt to sync run an empty script
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: ""}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "One of 'script_id', 'script_contents', or 'script_name' is required.")

	// attempt to sync run an overly long script
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: strings.Repeat("a", automdm.UnsavedScriptMaxRuneLen+1)}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Script is too large. It's limited to 10,000 characters")

	// make sure the host is still seen as "online"
	err = s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now())
	require.NoError(t, err)

	// attempt to create a valid sync script execution request, fails because the
	// host has a pending script execution
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusConflict)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, automdm.RunScriptAlreadyRunningErrMsg)

	// save a result via the orbit endpoint
	var orbitPostScriptResp automdm.OrbitPostScriptResultResponse
	s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *host.OrbitNodeKey, scriptResultResp.ExecutionID)),
		http.StatusOK, &orbitPostScriptResp)

	// verify that orbit does not receive any pending script anymore
	orbitResp = automdm.OrbitGetConfigResponse{}
	s.DoJSON("POST", "/api/automdm/orbit/config",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q}`, *host.OrbitNodeKey)),
		http.StatusOK, &orbitResp)
	require.Empty(t, orbitResp.Notifications.PendingScriptExecutionIDs)

	// create a valid sync script execution request, fails because the
	// request will time-out waiting for a result.
	runSyncResp = automdm.RunScriptSyncResponse{}
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusRequestTimeout, &runSyncResp)
	require.Equal(t, host.ID, runSyncResp.HostID)
	require.NotEmpty(t, runSyncResp.ExecutionID)
	require.True(t, runSyncResp.HostTimeout)
	require.Contains(t, runSyncResp.Message, automdm.RunScriptHostTimeoutErrMsg)

	s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *host.OrbitNodeKey, runSyncResp.ExecutionID)),
		http.StatusOK, &orbitPostScriptResp)

	// create a valid sync script execution request, and simulate a result
	// arriving before timeout.
	testRunScriptWaitForResult = 5 * time.Second
	ctx, cancel := context.WithTimeout(ctx, testRunScriptWaitForResult)
	defer cancel()

	resultsCh := make(chan *automdm.HostScriptResultPayload, 1)
	go func() {
		for range time.Tick(300 * time.Millisecond) {
			pending, err := s.ds.ListPendingHostScriptExecutions(ctx, host.ID)
			if err != nil {
				t.Log(err)
				return
			}
			if len(pending) > 0 {
				select {
				case <-ctx.Done():
					return
				case r := <-resultsCh:
					r.ExecutionID = pending[0].ExecutionID
					// ignoring errors in this goroutine, the HTTP request below will fail if this fails
					_, _, err = s.ds.SetHostScriptExecutionResult(ctx, r, nil)
					if err != nil {
						t.Log(err)
					}
				}
			}
		}
	}()

	// simulate a successful script result
	resultsCh <- &automdm.HostScriptResultPayload{
		HostID:   host.ID,
		Output:   "ok",
		Runtime:  1,
		ExitCode: 0,
	}
	runSyncResp = automdm.RunScriptSyncResponse{}
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusOK, &runSyncResp)
	require.Equal(t, host.ID, runSyncResp.HostID)
	require.NotEmpty(t, runSyncResp.ExecutionID)
	require.Equal(t, "ok", runSyncResp.Output)
	require.NotNil(t, runSyncResp.ExitCode)
	require.Equal(t, int64(0), *runSyncResp.ExitCode)
	require.False(t, runSyncResp.HostTimeout)

	// simulate a scripts disabled result
	resultsCh <- &automdm.HostScriptResultPayload{
		HostID:   host.ID,
		Output:   "",
		Runtime:  0,
		ExitCode: -2,
	}
	runSyncResp = automdm.RunScriptSyncResponse{}
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusOK, &runSyncResp)
	require.Equal(t, host.ID, runSyncResp.HostID)
	require.NotEmpty(t, runSyncResp.ExecutionID)
	require.Empty(t, runSyncResp.Output)
	require.NotNil(t, runSyncResp.ExitCode)
	require.Equal(t, int64(-2), *runSyncResp.ExitCode)
	require.False(t, runSyncResp.HostTimeout)
	require.Contains(t, runSyncResp.Message, "Scripts are disabled")

	// create a sync execution request.
	runSyncResp = automdm.RunScriptSyncResponse{}
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusRequestTimeout, &runSyncResp)

	// modify the timestamp of the script to simulate an script that has
	// been pending for a long time
	mysqltest.ExecAdhocSQL(t, s.ds, func(tx sqlx.ExtContext) error {
		_, err := tx.ExecContext(context.Background(), "UPDATE host_script_results SET created_at = ? WHERE execution_id = ?", time.Now().Add(-24*time.Hour), runSyncResp.ExecutionID)
		return err
	})

	// fetch the results for the timed-out script
	scriptResultResp = automdm.GetScriptResultResponse{}
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+runSyncResp.ExecutionID, nil, http.StatusOK, &scriptResultResp)
	require.Equal(t, host.ID, scriptResultResp.HostID)
	require.Equal(t, "echo", scriptResultResp.ScriptContents)
	require.Nil(t, scriptResultResp.ExitCode)
	require.True(t, scriptResultResp.HostTimeout)
	require.Contains(t, scriptResultResp.Message, automdm.RunScriptHostTimeoutErrMsg)

	// Disable scripts on the host
	scriptsEnabled := false
	err = s.ds.SetOrUpdateHostOrbitInfo(
		context.Background(), host.ID, "1.22.0", sql.NullString{}, sql.NullBool{Bool: scriptsEnabled, Valid: true},
	)
	require.NoError(t, err)
	s.DoJSON(
		"POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"},
		http.StatusUnprocessableEntity, &runResp,
	)
	s.DoJSON(
		"POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"},
		http.StatusUnprocessableEntity, &runResp,
	)
	// Re-enable scripts on the host
	scriptsEnabled = true
	err = s.ds.SetOrUpdateHostOrbitInfo(
		context.Background(), host.ID, "1.22.0", sql.NullString{}, sql.NullBool{Bool: scriptsEnabled, Valid: true},
	)
	require.NoError(t, err)

	// make the host "offline"
	err = s.ds.MarkHostsSeen(context.Background(), []uint{host.ID}, time.Now().Add(-time.Hour))
	require.NoError(t, err)

	// attempt to create a sync script execution request, fails because the host
	// is offline.
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, automdm.RunScriptHostOfflineErrMsg)

	// attempt to create an async script execution request, succeeds because script is added to queue.
	s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusAccepted)

	// attempt to run a script on a plain osquery host
	plainOsqueryHost, err := s.ds.NewHost(context.Background(), &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-time.Minute),
		OsqueryHostID:   new("plain-osquery-host"),
		NodeKey:         new("plain-osquery-host"),
		UUID:            uuid.New().String(),
		Hostname:        fmt.Sprintf("%s.local", "plain-osquery-host"),
		HardwareSerial:  uuid.New().String(),
		Platform:        "linux",
	})
	require.NoError(t, err)
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: plainOsqueryHost.ID, ScriptContents: "echo"}, http.StatusUnprocessableEntity)
	require.Contains(t, extractServerErrorText(res.Body), automdm.RunScriptDisabledErrMsg)
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: plainOsqueryHost.ID, ScriptContents: "echo"}, http.StatusUnprocessableEntity)
	require.Contains(t, extractServerErrorText(res.Body), automdm.RunScriptDisabledErrMsg)

	// create a execution request that will return a timeout
	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusAccepted, &runResp)

	// simulate a host response
	s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": -1, "output": "script execution error: signal: killed", "timeout": 900}`, *host.OrbitNodeKey, runSyncResp.ExecutionID)),
		http.StatusOK, &orbitPostScriptResp)

	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+runSyncResp.ExecutionID, nil, http.StatusOK, &scriptResultResp)
	require.Equal(t, host.ID, scriptResultResp.HostID)
	require.Equal(t, "echo", scriptResultResp.ScriptContents)
	require.Equal(t, int64(-1), *scriptResultResp.ExitCode)
	require.Equal(t, "Timeout. AutoMDM stopped the script after 900 seconds to protect host performance.", scriptResultResp.Message)
	require.Equal(t, "script execution error: signal: killed", scriptResultResp.Output)
}

func (s *integrationEnterpriseTestSuite) TestRunBatchScript() {
	t := s.T()
	ctx := context.Background()

	team1, err := s.ds.NewTeam(ctx, &automdm.Team{
		Name: "Team 1",
	})
	require.NoError(t, err)

	host1 := createOrbitEnrolledHost(t, "linux", "host1", s.ds)
	host2 := createOrbitEnrolledHost(t, "linux", "host2", s.ds)
	host3Team1 := createOrbitEnrolledHost(t, "linux", "host3team1", s.ds)
	err = s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team1.ID, []uint{host3Team1.ID}))
	require.NoError(t, err)

	script, err := s.ds.NewScript(ctx, &automdm.Script{
		Name:           "script.sh",
		ScriptContents: "echo bonjour",
	})
	require.NoError(t, err)

	var batchRes automdm.BatchScriptRunResponse
	// Team mismatch
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID: script.ID,
		HostIDs:  []uint{host1.ID, host2.ID, host3Team1.ID},
	}, http.StatusUnprocessableEntity, &batchRes)
	require.Empty(t, batchRes.BatchExecutionID)

	// Bad script ID
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID: 999999,
		HostIDs:  []uint{host1.ID},
	}, http.StatusNotFound, &batchRes)
	require.Empty(t, batchRes.BatchExecutionID)

	// verify that no script was queued for orbit
	var orbitRespHost1 automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q}`, *host1.OrbitNodeKey)),
		http.StatusOK, &orbitRespHost1)
	require.Empty(t, orbitRespHost1.Notifications.PendingScriptExecutionIDs)

	// Good request
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID: script.ID,
		HostIDs:  []uint{host1.ID, host2.ID},
	}, http.StatusOK, &batchRes)
	require.NotEmpty(t, batchRes.BatchExecutionID)

	// wait for the background run to queue the script, the batch should be started
	require.Eventually(t, func() bool {
		batchActivity, err := s.ds.GetBatchActivity(ctx, batchRes.BatchExecutionID)
		return err == nil && batchActivity.Status == automdm.ScheduledBatchExecutionStarted
	}, 5*time.Second, 10*time.Millisecond)

	// verify that script was queued for orbit
	s.DoJSON("POST", "/api/automdm/orbit/config",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q}`, *host1.OrbitNodeKey)),
		http.StatusOK, &orbitRespHost1)
	require.Len(t, orbitRespHost1.Notifications.PendingScriptExecutionIDs, 1)

	var orbitRespHost2 automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q}`, *host2.OrbitNodeKey)),
		http.StatusOK, &orbitRespHost2)
	require.Len(t, orbitRespHost2.Notifications.PendingScriptExecutionIDs, 1)

	var hostActivitiesResp listHostUpcomingActivitiesResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/activities/upcoming", host1.ID),
		nil, http.StatusOK, &hostActivitiesResp)

	expectedActivityDetail := fmt.Sprintf(`
	{
		"async": true,
		"host_id": %d,
		"policy_id": null,
		"policy_name": null,
		"batch_execution_id": "%s",
		"script_execution_id": "%s",
		"script_name": "%s",
		"host_display_name": "%s"
	}
	`, host1.ID, batchRes.BatchExecutionID, orbitRespHost1.Notifications.PendingScriptExecutionIDs[0], script.Name, host1.DisplayName())
	require.Len(t, hostActivitiesResp.Activities, 1)
	require.NotNil(t, hostActivitiesResp.Activities[0].Details)
	require.JSONEq(t, expectedActivityDetail, string(*hostActivitiesResp.Activities[0].Details))

	// Check status of the batch execution
	var batchStatusResp automdm.BatchScriptExecutionStatusResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s", batchRes.BatchExecutionID), nil, http.StatusOK, &batchStatusResp)
	require.Equal(t, *batchStatusResp.ScriptID, script.ID)
	require.Equal(t, uint(2), *batchStatusResp.NumTargeted)
	require.Equal(t, uint(2), *batchStatusResp.NumPending)

	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeRanScriptBatch{}.ActivityName(),
		fmt.Sprintf(`{"batch_execution_id":"%s", "team_id":null, "host_count":2, "script_name":"%s"}`, batchRes.BatchExecutionID, script.Name),
		0,
	)

	var batchPendingHostsResp automdm.BatchScriptExecutionHostResultsResponse
	res := s.Do("GET", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s/host_results", batchRes.BatchExecutionID), nil, http.StatusBadRequest)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Param status is required")
	s.Do("GET", "/api/latest/automdm/scripts/batch/00000000-0000-0000-0000-000000000000/host_results?status=ran", nil, http.StatusNotFound)

	t.Run("team user gets 403 for an unknown batch", func(t *testing.T) {
		u := &automdm.User{
			Name:  "team1 observer",
			Email: "team1-observer-batch@example.com",
			Teams: []automdm.UserTeam{{Team: *team1, Role: automdm.RoleObserver}},
		}
		require.NoError(t, u.SetPassword(test.GoodPassword, 10, 10))
		_, err := s.ds.NewUser(ctx, u)
		require.NoError(t, err)

		s.setTokenForTest(t, u.Email, test.GoodPassword)
		s.Do("GET", "/api/latest/automdm/scripts/batch/00000000-0000-0000-0000-000000000000/host_results?status=ran", nil, http.StatusForbidden)
	})

	// List pending hosts
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s/host_results?status=pending", batchRes.BatchExecutionID), nil, http.StatusOK, &batchPendingHostsResp)
	require.Len(t, batchPendingHostsResp.Hosts, 2)
	require.Equal(t, uint(2), batchPendingHostsResp.Count)
	require.False(t, batchPendingHostsResp.Meta.HasNextResults)
	require.False(t, batchPendingHostsResp.Meta.HasPreviousResults)
	require.Equal(t, automdm.BatchScriptExecutionPending, batchPendingHostsResp.Hosts[0].Status)
	require.Equal(t, batchPendingHostsResp.Hosts[0].DisplayName, host1.ComputerName)
	require.Equal(t, batchPendingHostsResp.Hosts[0].ID, host1.ID)
	require.Equal(t, automdm.BatchScriptExecutionPending, batchPendingHostsResp.Hosts[1].Status)
	require.Equal(t, batchPendingHostsResp.Hosts[1].DisplayName, host2.ComputerName)
	require.Equal(t, batchPendingHostsResp.Hosts[1].ID, host2.ID)

	// Another request so we can check the list endpoint
	var batchRes2 automdm.BatchScriptRunResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID: script.ID,
		HostIDs:  []uint{host1.ID},
	}, http.StatusOK, &batchRes2)
	require.NotEmpty(t, batchRes2.BatchExecutionID)

	// wait for the background run to queue the script, the batch should be started
	require.Eventually(t, func() bool {
		batchActivity, err := s.ds.GetBatchActivity(ctx, batchRes2.BatchExecutionID)
		return err == nil && batchActivity.Status == automdm.ScheduledBatchExecutionStarted
	}, 5*time.Second, 10*time.Millisecond)

	// Check with the list endpoint
	var batchListResp automdm.BatchScriptExecutionListResponse
	s.DoJSON("GET", "/api/latest/automdm/scripts/batch?team_id=0&per_page=1", nil, http.StatusOK, &batchListResp)
	require.Len(t, batchListResp.BatchScriptExecutions, 1)
	require.Equal(t, uint(2), batchListResp.Count)
	require.True(t, batchListResp.Meta.HasNextResults)
	require.False(t, batchListResp.Meta.HasPreviousResults)
	require.Equal(t, batchListResp.BatchScriptExecutions[0].BatchExecutionID, batchRes2.BatchExecutionID)
	require.Equal(t, *batchListResp.BatchScriptExecutions[0].ScriptID, script.ID)
	require.Equal(t, uint(1), *batchListResp.BatchScriptExecutions[0].NumTargeted)
	require.Equal(t, uint(1), *batchListResp.BatchScriptExecutions[0].NumPending)

	s.DoJSON("GET", "/api/latest/automdm/scripts/batch?team_id=0&page=1&per_page=1", nil, http.StatusOK, &batchListResp)
	require.Len(t, batchListResp.BatchScriptExecutions, 1)
	require.Equal(t, uint(2), batchListResp.Count)
	require.False(t, batchListResp.Meta.HasNextResults)
	require.True(t, batchListResp.Meta.HasPreviousResults)
	require.Equal(t, batchListResp.BatchScriptExecutions[0].BatchExecutionID, batchRes.BatchExecutionID)
	require.Equal(t, *batchListResp.BatchScriptExecutions[0].ScriptID, script.ID)
	require.Equal(t, uint(2), *batchListResp.BatchScriptExecutions[0].NumTargeted)
	require.Equal(t, uint(2), *batchListResp.BatchScriptExecutions[0].NumPending)

	var batchResUpcoming automdm.BatchScriptRunResponse
	// Queue scripts again, now in upcoming activities queue
	// Scheduling something in the past makes it run right now
	scheduledPast := time.Now().Add(-2 * time.Hour)
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID:  script.ID,
		HostIDs:   []uint{host1.ID, host2.ID},
		NotBefore: &scheduledPast,
	}, http.StatusOK, &batchResUpcoming)
	require.NotEmpty(t, batchResUpcoming.BatchExecutionID)

	// wait for the background run to queue the script, the batch should be started
	require.Eventually(t, func() bool {
		batchActivity, err := s.ds.GetBatchActivity(ctx, batchResUpcoming.BatchExecutionID)
		return err == nil && batchActivity.Status == automdm.ScheduledBatchExecutionStarted
	}, 5*time.Second, 10*time.Millisecond)

	// save a result via the orbit endpoint
	var orbitPostScriptResp automdm.OrbitPostScriptResultResponse
	s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *host1.OrbitNodeKey, orbitRespHost1.Notifications.PendingScriptExecutionIDs[0])),
		http.StatusOK, &orbitPostScriptResp)

	// individual script executions don't get added to the global feed, only the batch job
	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeRanScriptBatch{}.ActivityName(),
		fmt.Sprintf(`{"batch_execution_id":"%s", "team_id":null, "host_count":2, "script_name":"%s"}`, batchResUpcoming.BatchExecutionID, script.Name),
		0,
	)

	// batch script executions do get added to the host feed
	var hostPastActivitiesResp listActivitiesResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/activities", host1.ID),
		nil, http.StatusOK, &hostPastActivitiesResp)

	expectedPastActivityDetail := fmt.Sprintf(`
	{
		"async": true,
		"host_id": %d,
		"policy_id": null,
		"policy_name": null,
		"batch_execution_id": "%s",
		"script_execution_id": "%s",
		"script_name": "%s",
		"host_display_name": "%s",
		"from_setup_experience": false
	}
	`, host1.ID, batchRes.BatchExecutionID, orbitRespHost1.Notifications.PendingScriptExecutionIDs[0], script.Name, host1.DisplayName())
	require.Len(t, hostPastActivitiesResp.Activities, 1)
	require.NotNil(t, hostPastActivitiesResp.Activities[0].Details)
	require.JSONEq(t, expectedPastActivityDetail, string(*hostPastActivitiesResp.Activities[0].Details))

	// Queue upcoming execution
	scheduledTime := time.Now().Add(10 * time.Hour)
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID:  script.ID,
		HostIDs:   []uint{host1.ID, host2.ID},
		NotBefore: &scheduledTime,
	}, http.StatusOK, &batchRes)
	require.NotEmpty(t, batchRes.BatchExecutionID)

	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeBatchScriptScheduled{}.ActivityName(),
		fmt.Sprintf(`{"batch_execution_id":"%s", "team_id":null, "host_count":2, "script_name":"%s", "not_before": "%s"}`, batchRes.BatchExecutionID, script.Name, scheduledTime.UTC().Format(time.RFC3339Nano)),
		0,
	)
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID: script.ID,
		HostIDs:  []uint{host1.ID, host2.ID},
	}, http.StatusOK, &batchResUpcoming)
	require.NotEmpty(t, batchResUpcoming.BatchExecutionID)

	// wait for the background run to queue the script, the batch should be started
	require.Eventually(t, func() bool {
		batchActivity, err := s.ds.GetBatchActivity(ctx, batchResUpcoming.BatchExecutionID)
		return err == nil && batchActivity.Status == automdm.ScheduledBatchExecutionStarted
	}, 5*time.Second, 10*time.Millisecond)

	// Queue again this time using a filter
	filters := map[string]any{
		"team_id": 0,
	}
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID:  script.ID,
		Filters:   &filters,
		NotBefore: &scheduledTime,
	}, http.StatusOK, &batchRes)
	require.NotEmpty(t, batchRes.BatchExecutionID)

	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeBatchScriptScheduled{}.ActivityName(),
		fmt.Sprintf(`{"batch_execution_id":"%s", "team_id":null, "host_count":2, "script_name":"%s", "not_before": "%s"}`, batchRes.BatchExecutionID, script.Name, scheduledTime.UTC().Format(time.RFC3339Nano)),
		0,
	)
}

func (s *integrationEnterpriseTestSuite) TestCancelBatchScripts() {
	t := s.T()
	ctx := context.Background()

	host1 := createOrbitEnrolledHost(t, "linux", "host1", s.ds)
	host2 := createOrbitEnrolledHost(t, "linux", "host2", s.ds)
	host3 := createOrbitEnrolledHost(t, "linux", "host3", s.ds)
	host4 := createOrbitEnrolledHost(t, "linux", "host4", s.ds)

	script, err := s.ds.NewScript(ctx, &automdm.Script{
		Name:           "script.sh",
		ScriptContents: "echo bonjour",
	})
	require.NoError(t, err)

	// Immediate execution
	var batchRes automdm.BatchScriptRunResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID: script.ID,
		HostIDs:  []uint{host1.ID, host2.ID},
	}, http.StatusOK, &batchRes)
	require.NotEmpty(t, batchRes.BatchExecutionID)

	var batchStatusResp automdm.BatchScriptExecutionStatusResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s", batchRes.BatchExecutionID), nil, http.StatusOK, &batchStatusResp)
	require.Equal(t, *batchStatusResp.ScriptID, script.ID)
	require.Equal(t, uint(2), *batchStatusResp.NumTargeted)
	require.Equal(t, uint(2), *batchStatusResp.NumPending)

	var batchCancelResp automdm.BatchScriptCancelResponse
	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s/cancel", batchRes.BatchExecutionID), nil, http.StatusOK, &batchCancelResp)

	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeBatchScriptCanceled{}.ActivityName(),
		fmt.Sprintf(`{"batch_execution_id": "%s", "host_count":2, "canceled_count": 2, "script_name":"%s"}`, batchRes.BatchExecutionID, script.Name),
		0,
	)

	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s", batchRes.BatchExecutionID), nil, http.StatusOK, &batchStatusResp)
	require.Equal(t, *batchStatusResp.ScriptID, script.ID)
	require.Equal(t, uint(2), *batchStatusResp.NumTargeted)
	require.Equal(t, uint(0), *batchStatusResp.NumPending)
	require.Equal(t, uint(2), *batchStatusResp.NumCanceled)

	// Future execution
	var batchResScheduled automdm.BatchScriptRunResponse
	scheduleTime := time.Now().Add(3 * time.Hour)
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/batch", automdm.BatchScriptRunRequest{
		ScriptID:  script.ID,
		HostIDs:   []uint{host3.ID, host4.ID},
		NotBefore: &scheduleTime,
	}, http.StatusOK, &batchResScheduled)
	require.NotEmpty(t, batchResScheduled.BatchExecutionID)

	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s", batchResScheduled.BatchExecutionID), nil, http.StatusOK, &batchStatusResp)
	require.Equal(t, *batchStatusResp.ScriptID, script.ID)
	require.Equal(t, uint(2), *batchStatusResp.NumTargeted)
	require.Equal(t, uint(2), *batchStatusResp.NumPending)

	s.DoJSON("POST", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s/cancel", batchResScheduled.BatchExecutionID), nil, http.StatusOK, &batchCancelResp)

	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeBatchScriptCanceled{}.ActivityName(),
		fmt.Sprintf(`{"batch_execution_id": "%s", "host_count":2, "canceled_count": 2, "script_name":"%s"}`, batchResScheduled.BatchExecutionID, script.Name),
		0,
	)

	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/batch/%s", batchResScheduled.BatchExecutionID), nil, http.StatusOK, &batchStatusResp)
	require.Equal(t, *batchStatusResp.ScriptID, script.ID)
	require.Equal(t, uint(2), *batchStatusResp.NumTargeted)
	require.Equal(t, uint(0), *batchStatusResp.NumPending)
	require.Equal(t, uint(2), *batchStatusResp.NumCanceled)
}

func (s *integrationEnterpriseTestSuite) TestRunHostSavedScript() {
	t := s.T()

	testRunScriptWaitForResult = 2 * time.Second
	defer func() { testRunScriptWaitForResult = 0 }()

	ctx := context.Background()

	host := createOrbitEnrolledHost(t, "linux", "", s.ds)
	tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team 1"})
	require.NoError(t, err)
	savedNoTmScript, err := s.ds.NewScript(ctx, &automdm.Script{
		TeamID:         nil,
		Name:           "no_team_script.sh",
		ScriptContents: "echo 'no team'",
	})
	require.NoError(t, err)
	savedTmScript, err := s.ds.NewScript(ctx, &automdm.Script{
		TeamID:         &tm.ID,
		Name:           "team_script.sh",
		ScriptContents: "echo 'team'",
	})
	require.NoError(t, err)

	// attempt to run a script on a non-existing host
	var runResp automdm.RunScriptResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID + 100, ScriptID: &savedNoTmScript.ID}, http.StatusNotFound, &runResp)

	// attempt to run with both script contents and id
	res := s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo", ScriptID: new(savedTmScript.ID + 999)}, http.StatusUnprocessableEntity)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_id' or 'script_contents' is allowed.`)

	// attempt to run with unknown script id
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: new(savedTmScript.ID + 999)}, http.StatusNotFound)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `No script exists for the provided "script_id".`)

	// make sure the host is still seen as "online"
	err = s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now())
	require.NoError(t, err)

	// attempt to run a team script on a non-team host
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &savedTmScript.ID}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `The script does not belong to the same team`)

	// make sure the host is still seen as "online"
	err = s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now())
	require.NoError(t, err)

	// create a valid script execution request
	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &savedNoTmScript.ID}, http.StatusAccepted, &runResp)
	require.Equal(t, host.ID, runResp.HostID)
	require.NotEmpty(t, runResp.ExecutionID)

	var scriptResultResp automdm.GetScriptResultResponse
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+runResp.ExecutionID, nil, http.StatusOK, &scriptResultResp)
	require.Equal(t, host.ID, scriptResultResp.HostID)
	require.Equal(t, "echo 'no team'", scriptResultResp.ScriptContents)
	require.Nil(t, scriptResultResp.ExitCode)
	require.False(t, scriptResultResp.HostTimeout)
	require.Contains(t, scriptResultResp.Message, automdm.RunScriptAsyncScriptEnqueuedMsg)
	require.NotNil(t, scriptResultResp.ScriptID)
	require.Equal(t, savedNoTmScript.ID, *scriptResultResp.ScriptID)

	// verify that orbit would get the notification that it has a script to run
	var orbitResp automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q}`, *host.OrbitNodeKey)),
		http.StatusOK, &orbitResp)
	require.Equal(t, []string{scriptResultResp.ExecutionID}, orbitResp.Notifications.PendingScriptExecutionIDs)

	// the orbit endpoint to get a pending script to execute returns it
	var orbitGetScriptResp automdm.OrbitGetScriptResponse
	s.DoJSON("POST", "/api/automdm/orbit/scripts/request",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q}`, *host.OrbitNodeKey, scriptResultResp.ExecutionID)),
		http.StatusOK, &orbitGetScriptResp)
	require.Equal(t, host.ID, orbitGetScriptResp.HostID)
	require.Equal(t, scriptResultResp.ExecutionID, orbitGetScriptResp.ExecutionID)
	require.Equal(t, "echo 'no team'", orbitGetScriptResp.ScriptContents)

	// make sure the host is still seen as "online"
	err = s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now())
	require.NoError(t, err)

	// save a result via the orbit endpoint
	var orbitPostScriptResp automdm.OrbitPostScriptResultResponse
	s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *host.OrbitNodeKey, scriptResultResp.ExecutionID)),
		http.StatusOK, &orbitPostScriptResp)

	// an activity was created for the script execution
	s.lastActivityMatches(
		automdm.ActivityTypeRanScript{}.ActivityName(),
		fmt.Sprintf(
			`{"host_id": %d, "host_display_name": %q, "script_name": %q, "script_execution_id": %q, "async": true, "policy_id": null, "policy_name": null, "batch_execution_id": null, "from_setup_experience": false}`,
			host.ID, host.DisplayName(), savedNoTmScript.Name, scriptResultResp.ExecutionID,
		),
		0,
	)

	// verify that orbit does not receive any pending script anymore
	orbitResp = automdm.OrbitGetConfigResponse{}
	s.DoJSON("POST", "/api/automdm/orbit/config",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q}`, *host.OrbitNodeKey)),
		http.StatusOK, &orbitResp)
	require.Empty(t, orbitResp.Notifications.PendingScriptExecutionIDs)

	// create a valid sync script execution request, fails because the
	// request will time-out waiting for a result.
	var runSyncResp automdm.RunScriptSyncResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &savedNoTmScript.ID}, http.StatusRequestTimeout, &runSyncResp)
	require.Equal(t, host.ID, runSyncResp.HostID)
	require.NotEmpty(t, runSyncResp.ExecutionID)
	require.NotNil(t, runSyncResp.ScriptID)
	require.Equal(t, savedNoTmScript.ID, *runSyncResp.ScriptID)
	require.Equal(t, "echo 'no team'", runSyncResp.ScriptContents)
	require.True(t, runSyncResp.HostTimeout)
	require.Contains(t, runSyncResp.Message, automdm.RunScriptHostTimeoutErrMsg)

	// attempt to run sync with both script contents and script id
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo", ScriptID: new(savedTmScript.ID + 999)}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_id' or 'script_contents' is allowed.`)

	// attempt to run sync with both script contents and script name
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo", ScriptName: savedTmScript.Name}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_contents' or 'script_name' is allowed.`)

	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo", ScriptName: savedTmScript.Name}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_contents' or 'script_name' is allowed.`)

	// attempt to run sync with both script id and script name
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: new(savedTmScript.ID + 999), ScriptName: savedTmScript.Name}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_id' or 'script_name' is allowed.`)

	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: new(savedTmScript.ID + 999), ScriptName: savedTmScript.Name}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_id' or 'script_name' is allowed.`)

	// attempt to run sync with both script contents and team id
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo", TeamID: 1}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_contents' or 'team_id' is allowed.`)

	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo", TeamID: 1}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_contents' or 'team_id' is allowed.`)

	// attempt to run sync with both script id and team id
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: new(savedTmScript.ID + 999), TeamID: 1}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_id' or 'team_id' is allowed.`)

	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: new(savedTmScript.ID + 999), TeamID: 1}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Only one of 'script_id' or 'team_id' is allowed.`)

	// attempt to run sync without script contents, script id, or script name
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `One of 'script_id', 'script_contents', or 'script_name' is required.`)

	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `One of 'script_id', 'script_contents', or 'script_name' is required.`)

	// deleting the saved script should delete the pending script
	s.Do("DELETE", fmt.Sprintf("/api/latest/automdm/scripts/%d", savedNoTmScript.ID), nil, http.StatusNoContent)
	scriptResultResp = automdm.GetScriptResultResponse{}
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+runSyncResp.ExecutionID, nil, http.StatusNotFound, &scriptResultResp)

	// Verify that we can't enqueue more than 1k scripts

	// Make the host offline so that scripts enqueue
	err = s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	for i := 1; i <= 1000; i++ {
		script, err := s.ds.NewScript(ctx, &automdm.Script{
			TeamID:         nil,
			Name:           fmt.Sprintf("script_1k_%d.sh", i),
			ScriptContents: fmt.Sprintf("echo %d", i),
		})
		require.NoError(t, err)

		_, err = s.ds.NewHostScriptExecutionRequest(ctx, &automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &script.ID})
		require.NoError(t, err)
	}

	script, err := s.ds.NewScript(ctx, &automdm.Script{
		TeamID:         nil,
		Name:           "script_1k_1001.sh",
		ScriptContents: "echo 1001",
	})
	require.NoError(t, err)

	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &script.ID}, http.StatusConflict, &runResp)

	// set up a new host, new team, and some new scripts
	host2 := createOrbitEnrolledHost(t, "linux", "f1337", s.ds)
	tm2, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team 2"})
	require.NoError(t, err)
	savedNoTmScript2, err := s.ds.NewScript(ctx, &automdm.Script{
		TeamID:         nil,
		Name:           "f1337.sh",
		ScriptContents: "echo 'ALL YOUR BASE ARE BELONG TO US'",
	})
	require.NoError(t, err)
	savedTmScript2, err := s.ds.NewScript(ctx, &automdm.Script{
		TeamID:         &tm2.ID,
		Name:           "f1337.sh",
		ScriptContents: "echo 'ALL YOUR BASE ARE BELONG TO US'",
	})
	require.NoError(t, err)
	require.NotEqual(t, savedNoTmScript2.ID, savedTmScript2.ID)

	_, err = s.ds.NewScript(ctx, &automdm.Script{
		TeamID:         nil,
		Name:           "f13372.sh",
		ScriptContents: "echo 'ALL YOUR BASE ARE BELONG TO US'",
	})
	require.NoError(t, err)

	// make sure the new host is seen as "online"
	err = s.ds.MarkHostsSeen(ctx, []uint{host2.ID}, time.Now())
	require.NoError(t, err)

	// attempt to run sync with a script that does not exist on the specified team
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host2.ID, ScriptName: "f1337.sh", TeamID: tm.ID}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Script 'f1337.sh' doesn’t exist.`)

	// attempt to run sync with an existing team script that belongs to a team different from the host's team
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host2.ID, ScriptName: "f1337.sh", TeamID: tm2.ID}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `The script does not belong to the same team`)

	// create a valid sync script execution request by script name, fails because the
	// request will time-out waiting for a result.
	var runSyncResp2 automdm.RunScriptSyncResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host2.ID, ScriptName: "f1337.sh"}, http.StatusRequestTimeout, &runSyncResp2)
	require.Equal(t, host2.ID, runSyncResp2.HostID)
	require.NotEmpty(t, runSyncResp2.ExecutionID)
	require.NotNil(t, runSyncResp2.ScriptID)
	require.Equal(t, savedNoTmScript2.ID, *runSyncResp2.ScriptID)
	require.Equal(t, "echo 'ALL YOUR BASE ARE BELONG TO US'", runSyncResp2.ScriptContents)
	require.True(t, runSyncResp2.HostTimeout)
	require.Contains(t, runSyncResp2.Message, automdm.RunScriptHostTimeoutErrMsg)

	// attempt to run a script on a plain osquery host
	plainOsqueryHost, err := s.ds.NewHost(context.Background(), &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-time.Minute),
		OsqueryHostID:   new("plain-osquery-host-2"),
		NodeKey:         new("plain-osquery-host-2"),
		UUID:            uuid.New().String(),
		Hostname:        fmt.Sprintf("%s.local", "plain-osquery-host-2"),
		HardwareSerial:  uuid.New().String(),
		Platform:        "linux",
	})
	require.NoError(t, err)
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: plainOsqueryHost.ID, ScriptID: &script.ID}, http.StatusUnprocessableEntity)
	require.Contains(t, extractServerErrorText(res.Body), automdm.RunScriptDisabledErrMsg)
	res = s.Do("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: plainOsqueryHost.ID, ScriptID: &script.ID}, http.StatusUnprocessableEntity)
	require.Contains(t, extractServerErrorText(res.Body), automdm.RunScriptDisabledErrMsg)

	// Async Run Script by Name

	// attempt to run async with a script that does not exist on the specified team
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host2.ID, ScriptName: "f1337.sh", TeamID: tm.ID}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Script 'f1337.sh' doesn’t exist.`)

	// attempt to run async with an existing team script that belongs to a team different from the host's team
	res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host2.ID, ScriptName: "f1337.sh", TeamID: tm2.ID}, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `The script does not belong to the same team`)

	var runSyncResp3 automdm.RunScriptSyncResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host2.ID, ScriptName: "f13372.sh"}, http.StatusAccepted, &runSyncResp3)
	require.Equal(t, host2.ID, runSyncResp3.HostID)
	require.NotEmpty(t, runSyncResp3.ExecutionID)

	// verify pending result
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+runSyncResp3.ExecutionID, nil, http.StatusOK, &scriptResultResp)
	require.Equal(t, host2.ID, scriptResultResp.HostID)
	require.Equal(t, "echo 'ALL YOUR BASE ARE BELONG TO US'", scriptResultResp.ScriptContents)
	require.Nil(t, scriptResultResp.ExitCode)
	require.False(t, scriptResultResp.HostTimeout)
	require.Contains(t, scriptResultResp.Message, automdm.RunScriptAsyncScriptEnqueuedMsg)
}

func (s *integrationEnterpriseTestSuite) TestEnqueueSameScriptTwice() {
	t := s.T()
	ctx := context.Background()

	host := createOrbitEnrolledHost(t, "linux", "", s.ds)
	script, err := s.ds.NewScript(ctx, &automdm.Script{
		TeamID:         nil,
		Name:           "script.sh",
		ScriptContents: "echo 'hi from script'",
	})
	require.NoError(t, err)

	// Make the host offline so that scripts enqueue
	err = s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now().Add(-time.Hour))
	require.NoError(t, err)

	var runResp automdm.RunScriptResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &script.ID}, http.StatusAccepted, &runResp)
	require.Equal(t, host.ID, runResp.HostID)
	require.NotEmpty(t, runResp.ExecutionID)

	// Should fail because the same script is already enqueued for this host.
	resp := s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &script.ID}, http.StatusConflict)
	errorMsg := extractServerErrorText(resp.Body)
	require.Contains(t, errorMsg, "The script is already queued on the given host")
}

func (s *integrationEnterpriseTestSuite) TestSavedScripts() {
	t := s.T()
	ctx := context.Background()

	// create a saved script for no team
	var newScriptResp automdm.CreateScriptResponse
	body, headers := generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "hello"`), s.token, nil)
	res := s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusOK, headers)
	err := json.NewDecoder(res.Body).Decode(&newScriptResp)
	require.NoError(t, err)
	require.NotZero(t, newScriptResp.ScriptID)
	noTeamScriptID := newScriptResp.ScriptID
	s.lastActivityMatches("added_script", fmt.Sprintf(`{"script_name": %q, "team_name": null, "team_id": null}`, "script1.sh"), 0)

	// get the script
	var getScriptResp automdm.GetScriptResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", noTeamScriptID), nil, http.StatusOK, &getScriptResp)
	require.Equal(t, noTeamScriptID, getScriptResp.ID)
	require.Nil(t, getScriptResp.TeamID)
	require.Equal(t, "script1.sh", getScriptResp.Name)
	require.NotZero(t, getScriptResp.CreatedAt)
	require.NotZero(t, getScriptResp.UpdatedAt)
	require.Empty(t, getScriptResp.ScriptContents)

	// download the script's content
	res = s.Do("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", noTeamScriptID), nil, http.StatusOK, "alt", "media")
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, `echo "hello"`, string(b))
	require.Equal(t, int64(len(`echo "hello"`)), res.ContentLength)
	require.Equal(t, fmt.Sprintf("attachment;filename=\"%s %s\"", time.Now().Format(time.DateOnly), "script1.sh"), res.Header.Get("Content-Disposition"))

	// get a non-existing script
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", noTeamScriptID+999), nil, http.StatusNotFound, &getScriptResp)
	// download a non-existing script
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", noTeamScriptID+999), nil, http.StatusNotFound, &getScriptResp, "alt", "media")

	// file name is empty
	body, headers = generateNewScriptMultipartRequest(t,
		"", []byte(`echo "hello"`), s.token, nil)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusBadRequest, headers)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "no file headers for script")

	// contains invalid automdm secret
	body, headers = generateNewScriptMultipartRequest(t,
		"secrets.sh", []byte(`echo "$AUTOMDM_SECRET_INVALID"`), s.token, nil)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusUnprocessableEntity, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "$AUTOMDM_SECRET_INVALID")

	// file name is not a supported script type
	body, headers = generateNewScriptMultipartRequest(t,
		"not_sh.txt", []byte(`echo "hello"`), s.token, nil)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusUnprocessableEntity, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Validation Failed: File type not supported. Only .sh, .py, and .ps1 file types are allowed.")

	// file content is empty
	body, headers = generateNewScriptMultipartRequest(t,
		"script2.sh", []byte(``), s.token, nil)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusUnprocessableEntity, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Script contents must not be empty")

	// file content is too large
	body, headers = generateNewScriptMultipartRequest(t,
		"script2.sh", []byte(strings.Repeat("a", automdm.SavedScriptMaxRuneLen+1)), s.token, nil)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusUnprocessableEntity, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Script is too large. It's limited to 500,000 characters")

	// python shebang in .sh script should be rejected
	body, headers = generateNewScriptMultipartRequest(t,
		"script2.sh", []byte(`#!/bin/python`), s.token, nil)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusUnprocessableEntity, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Shell scripts must use a shell shebang")

	// script already exists with this name for this no-team
	body, headers = generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "hello"`), s.token, nil)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusConflict, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "A script with this name already exists")

	// team id does not exist
	body, headers = generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "hello"`), s.token, map[string][]string{"team_id": {"123"}})
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusNotFound, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "The team does not exist.")

	// create a team
	tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team1"})
	require.NoError(t, err)

	// create with existing name for this time for a team
	body, headers = generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "team"`), s.token, map[string][]string{"team_id": {fmt.Sprintf("%d", tm.ID)}})
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusOK, headers)
	err = json.NewDecoder(res.Body).Decode(&newScriptResp)
	require.NoError(t, err)
	require.NotZero(t, newScriptResp.ScriptID)
	require.NotEqual(t, noTeamScriptID, newScriptResp.ScriptID)
	tmScriptID := newScriptResp.ScriptID
	s.lastActivityMatches("added_script", fmt.Sprintf(`{"script_name": %q, "team_name": %q, "team_id": %d}`, "script1.sh", tm.Name, tm.ID), 0)

	// create a windows script
	body, headers = generateNewScriptMultipartRequest(t,
		"script2.ps1", []byte(`Write-Host "Hello, World!"`), s.token, map[string][]string{"team_id": {fmt.Sprintf("%d", tm.ID)}})

	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusOK, headers)
	err = json.NewDecoder(res.Body).Decode(&newScriptResp)
	require.NoError(t, err)
	require.NotZero(t, newScriptResp.ScriptID)
	require.NotEqual(t, noTeamScriptID, newScriptResp.ScriptID)
	s.lastActivityMatches("added_script", fmt.Sprintf(`{"script_name": %q, "team_name": %q, "team_id": %d}`, "script2.ps1", tm.Name, tm.ID), 0)

	// get team's script
	getScriptResp = automdm.GetScriptResponse{}
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", tmScriptID), nil, http.StatusOK, &getScriptResp)
	require.Equal(t, tmScriptID, getScriptResp.ID)
	require.NotNil(t, getScriptResp.TeamID)
	require.Equal(t, tm.ID, *getScriptResp.TeamID)
	require.Equal(t, "script1.sh", getScriptResp.Name)
	require.NotZero(t, getScriptResp.CreatedAt)
	require.NotZero(t, getScriptResp.UpdatedAt)
	require.Empty(t, getScriptResp.ScriptContents)

	// download the team's script's content
	res = s.Do("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", tmScriptID), nil, http.StatusOK, "alt", "media")
	b, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, `echo "team"`, string(b))
	require.Equal(t, int64(len(`echo "team"`)), res.ContentLength)
	require.Equal(t, fmt.Sprintf("attachment;filename=\"%s %s\"", time.Now().Format(time.DateOnly), "script1.sh"), res.Header.Get("Content-Disposition"))

	// script already exists with this name for this team
	body, headers = generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "hello"`), s.token, map[string][]string{"team_id": {fmt.Sprintf("%d", tm.ID)}})

	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusConflict, headers)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "A script with this name already exists")

	// create with a different name for this team
	body, headers = generateNewScriptMultipartRequest(t,
		"script2.sh", []byte(`echo "hello"`), s.token, map[string][]string{"team_id": {fmt.Sprintf("%d", tm.ID)}})

	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusOK, headers)
	err = json.NewDecoder(res.Body).Decode(&newScriptResp)
	require.NoError(t, err)
	require.NotZero(t, newScriptResp.ScriptID)
	require.NotEqual(t, noTeamScriptID, newScriptResp.ScriptID)
	require.NotEqual(t, tmScriptID, newScriptResp.ScriptID)
	s.lastActivityMatches("added_script", fmt.Sprintf(`{"script_name": %q, "team_name": %q, "team_id": %d}`, "script2.sh", tm.Name, tm.ID), 0)

	// Update a script
	updateScriptRep := automdm.UpdateScriptResponse{}
	body, headers = generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "updated script"`), s.token, nil)
	res = s.DoRawWithHeaders("PATCH", fmt.Sprintf("/api/latest/automdm/scripts/%d", tmScriptID), body.Bytes(), http.StatusOK, headers)
	err = json.NewDecoder(res.Body).Decode(&updateScriptRep)
	require.NoError(t, err)
	require.NotZero(t, newScriptResp.ScriptID)
	require.Equal(t, tmScriptID, updateScriptRep.ScriptID)
	s.lastActivityMatches("updated_script", fmt.Sprintf(`{"script_name": %q, "team_name": %q, "team_id": %d}`, "script1.sh", tm.Name, tm.ID), 0)

	// Download the updated script
	res = s.Do("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", tmScriptID), nil, http.StatusOK, "alt", "media")
	b, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, `echo "updated script"`, string(b))
	require.Equal(t, int64(len(`echo "updated script"`)), res.ContentLength)
	require.Equal(t, fmt.Sprintf("attachment;filename=\"%s %s\"", time.Now().Format(time.DateOnly), "script1.sh"), res.Header.Get("Content-Disposition"))

	// Try updating a non-existant script
	updateScriptRep = automdm.UpdateScriptResponse{}
	body, headers = generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "updated script"`), s.token, nil)
	res = s.DoRawWithHeaders("PATCH", fmt.Sprintf("/api/latest/automdm/scripts/%d", 999999999999), body.Bytes(), http.StatusNotFound, headers)
	err = json.NewDecoder(res.Body).Decode(&updateScriptRep)
	require.NoError(t, err)

	// delete the no-team script
	s.Do("DELETE", fmt.Sprintf("/api/latest/automdm/scripts/%d", noTeamScriptID), nil, http.StatusNoContent)
	s.lastActivityMatches("deleted_script", fmt.Sprintf(`{"script_name": %q, "team_name": null, "team_id": null}`, "script1.sh"), 0)

	// delete the initial team script
	s.Do("DELETE", fmt.Sprintf("/api/latest/automdm/scripts/%d", tmScriptID), nil, http.StatusNoContent)
	s.lastActivityMatches("deleted_script", fmt.Sprintf(`{"script_name": %q, "team_name": %q, "team_id": %d}`, "script1.sh", tm.Name, tm.ID), 0)

	// delete a non-existing script
	s.Do("DELETE", fmt.Sprintf("/api/latest/automdm/scripts/%d", noTeamScriptID), nil, http.StatusNotFound)
}

func (s *integrationEnterpriseTestSuite) TestListSavedScripts() {
	t := s.T()
	ctx := context.Background()

	// create some teams
	tm1, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team1"})
	require.NoError(t, err)
	tm2, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team2"})
	require.NoError(t, err)
	tm3, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team3"})
	require.NoError(t, err)

	// create 5 scripts for no team and team 1
	for i := range 5 {
		_, err = s.ds.NewScript(ctx, &automdm.Script{
			Name:           string('a' + byte(i)), // i.e. "a", "b", "c", ...
			ScriptContents: "echo",
		})
		require.NoError(t, err)
		_, err = s.ds.NewScript(ctx, &automdm.Script{Name: string('a' + byte(i)), TeamID: &tm1.ID, ScriptContents: "echo"})
		require.NoError(t, err)
	}

	// create a single script for team 2
	_, err = s.ds.NewScript(ctx, &automdm.Script{Name: "a", TeamID: &tm2.ID, ScriptContents: "echo"})
	require.NoError(t, err)

	cases := []struct {
		queries   []string // alternate query name and value
		teamID    *uint
		wantNames []string
		wantMeta  *automdm.PaginationMetadata
	}{
		{
			wantNames: []string{"a", "b", "c", "d", "e"},
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: false, HasPreviousResults: false},
		},
		{
			queries:   []string{"per_page", "2"},
			wantNames: []string{"a", "b"},
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: true, HasPreviousResults: false},
		},
		{
			queries:   []string{"per_page", "2", "page", "1"},
			wantNames: []string{"c", "d"},
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: true, HasPreviousResults: true},
		},
		{
			queries:   []string{"per_page", "2", "page", "2"},
			wantNames: []string{"e"},
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: false, HasPreviousResults: true},
		},
		{
			queries:   []string{"per_page", "3"},
			teamID:    &tm1.ID,
			wantNames: []string{"a", "b", "c"},
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: true, HasPreviousResults: false},
		},
		{
			queries:   []string{"per_page", "3", "page", "1"},
			teamID:    &tm1.ID,
			wantNames: []string{"d", "e"},
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: false, HasPreviousResults: true},
		},
		{
			queries:   []string{"per_page", "3", "page", "2"},
			teamID:    &tm1.ID,
			wantNames: nil,
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: false, HasPreviousResults: true},
		},
		{
			queries:   []string{"per_page", "3"},
			teamID:    &tm2.ID,
			wantNames: []string{"a"},
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: false, HasPreviousResults: false},
		},
		{
			queries:   []string{"per_page", "2"},
			teamID:    &tm3.ID,
			wantNames: nil,
			wantMeta:  &automdm.PaginationMetadata{HasNextResults: false, HasPreviousResults: false},
		},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%v: %#v", c.teamID, c.queries), func(t *testing.T) {
			var listResp automdm.ListScriptsResponse
			queryArgs := c.queries
			if c.teamID != nil {
				queryArgs = append(queryArgs, "team_id", fmt.Sprint(*c.teamID))
			}
			s.DoJSON("GET", "/api/latest/automdm/scripts", nil, http.StatusOK, &listResp, queryArgs...)

			require.Len(t, listResp.Scripts, len(c.wantNames))
			require.Equal(t, c.wantMeta, listResp.Meta)

			var gotNames []string
			if len(listResp.Scripts) > 0 {
				gotNames = make([]string, len(listResp.Scripts))
				for i, s := range listResp.Scripts {
					gotNames[i] = s.Name
					if c.teamID == nil {
						require.Nil(t, s.TeamID)
					} else {
						require.NotNil(t, s.TeamID)
						require.Equal(t, *c.teamID, *s.TeamID)
					}
				}
			}
			require.Equal(t, c.wantNames, gotNames)
		})
	}
}

func (s *integrationEnterpriseTestSuite) TestHostScriptDetails() {
	t := s.T()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// create some teams
	tm1, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "test-script-details-team1"})
	require.NoError(t, err)
	tm2, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "test-script-details-team2"})
	require.NoError(t, err)
	tm3, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "test-script-details-team3"})
	require.NoError(t, err)
	tm4, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "test-script-details-team4-windows"})
	require.NoError(t, err)

	// create 5 scripts for no team and team 1
	for i := range 5 {
		_, err = s.ds.NewScript(ctx, &automdm.Script{Name: fmt.Sprintf("test-script-details-%d.sh", i), ScriptContents: "echo"})
		require.NoError(t, err)
		_, err = s.ds.NewScript(ctx, &automdm.Script{Name: fmt.Sprintf("test-script-details-%d.sh", i), TeamID: &tm1.ID, ScriptContents: "echo"})
		require.NoError(t, err)
	}

	// add a windows script to team 4
	_, err = s.ds.NewScript(ctx, &automdm.Script{Name: "test-script-details-windows.ps1", TeamID: &tm4.ID, ScriptContents: `Write-Host "Hello, World!"`})
	require.NoError(t, err)

	// create a single script for team 2
	_, err = s.ds.NewScript(ctx, &automdm.Script{Name: "test-script-details-team-2.sh", TeamID: &tm2.ID, ScriptContents: "echo"})
	require.NoError(t, err)

	// create a host without a team
	host0, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-1 * time.Minute),
		OsqueryHostID:   new("host0"),
		NodeKey:         new("host0"),
		UUID:            uuid.New().String(),
		Hostname:        "host0",
		Platform:        "darwin",
	})
	require.NoError(t, err)

	// create a host for team 1
	host1, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-1 * time.Minute),
		OsqueryHostID:   new("host1"),
		NodeKey:         new("host1"),
		UUID:            uuid.New().String(),
		Hostname:        "host1",
		Platform:        "darwin",
		TeamID:          &tm1.ID,
	})
	require.NoError(t, err)

	// create a host for team 3
	host2, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-1 * time.Minute),
		OsqueryHostID:   new("host2"),
		NodeKey:         new("host2"),
		UUID:            uuid.New().String(),
		Hostname:        "host2",
		Platform:        "darwin",
		TeamID:          &tm3.ID,
	})
	require.NoError(t, err)

	// create a Windows host
	host3, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-1 * time.Minute),
		OsqueryHostID:   new("host3"),
		NodeKey:         new("host3"),
		UUID:            uuid.New().String(),
		Hostname:        "host3",
		Platform:        "windows",
		TeamID:          &tm4.ID,
	})
	require.NoError(t, err)

	// create a Linux host
	host4, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-1 * time.Minute),
		OsqueryHostID:   new("host4"),
		NodeKey:         new("host4"),
		UUID:            uuid.New().String(),
		Hostname:        "host4",
		Platform:        "ubuntu",
		TeamID:          nil,
	})
	require.NoError(t, err)

	// create a chrome host
	host5, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now().Add(-1 * time.Minute),
		OsqueryHostID:   new("host5"),
		NodeKey:         new("host5"),
		UUID:            uuid.New().String(),
		Hostname:        "host5",
		Platform:        "chrome",
		TeamID:          nil,
	})
	require.NoError(t, err)

	// it's fine to write directly to host_script_results here because we're not testing the execution
	// only the script details
	insertResults := func(t *testing.T, hostID uint, script *automdm.Script, createdAt time.Time, execID string, exitCode *int64) {
		stmt := `
INSERT INTO
	host_script_results (%s host_id, created_at, execution_id, exit_code, script_content_id, output, sync_request)
VALUES
	(%s ?,?,?,?,?,?, 1)`

		args := []any{}
		var scID uint
		mysqltest.ExecAdhocSQL(t, s.ds, func(tx sqlx.ExtContext) error {
			// First check if this script content already exists
			row := tx.QueryRowxContext(ctx, `
				SELECT id FROM script_contents WHERE md5_checksum = UNHEX(MD5(?))
			`, script.ScriptContents)
			err := row.Scan(&scID)

			if errors.Is(err, sql.ErrNoRows) {
				// Content doesn't exist, insert it
				res, err := tx.ExecContext(ctx, `
INSERT INTO
	script_contents (md5_checksum, contents, created_at)
VALUES
	(UNHEX(MD5(?)),?,?)`,
					script.ScriptContents,
					script.ScriptContents,
					createdAt,
				)
				if err != nil {
					return err
				}
				id, err := res.LastInsertId()
				if err != nil {
					return err
				}
				scID = uint(id) //nolint:gosec // dismiss G115
			} else if err != nil {
				return err
			}
			return nil
		})
		if script.ID == 0 {
			stmt = fmt.Sprintf(stmt, "", "")
		} else {
			stmt = fmt.Sprintf(stmt, "script_id,", "?,")
			args = append(args, script.ID)
		}
		args = append(args, hostID, createdAt, execID, exitCode, scID, "")

		mysqltest.ExecAdhocSQL(t, s.ds, func(tx sqlx.ExtContext) error {
			_, err := tx.ExecContext(ctx, stmt, args...)
			return err
		})
	}

	// insert some ad hoc script results, these are never included in the host script details
	insertResults(t, host0.ID, &automdm.Script{Name: "ad hoc script", ScriptContents: "echo foo"}, now, "ad-hoc-0", new(int64(0)))
	insertResults(t, host1.ID, &automdm.Script{Name: "ad hoc script", ScriptContents: "echo foo"}, now.Add(-1*time.Hour), "ad-hoc-1", new(int64(1)))

	t.Run("no team", func(t *testing.T) {
		noTeamScripts, _, err := s.ds.ListScripts(ctx, nil, automdm.ListOptions{})
		require.NoError(t, err)
		require.Len(t, noTeamScripts, 5)

		// insert saved script results for host0
		insertResults(t, host0.ID, noTeamScripts[0], now, "exec0-0", new(int64(0)))                   // expect status ran
		insertResults(t, host0.ID, noTeamScripts[1], now.Add(-1*time.Hour), "exec0-1", new(int64(1))) // expect status error
		insertResults(t, host0.ID, noTeamScripts[2], now.Add(-2*time.Hour), "exec0-2", nil)           // expect status pending

		// insert some ad hoc script results, these are never included in the host script details
		insertResults(t, host0.ID, &automdm.Script{Name: "ad hoc script", ScriptContents: "echo foo"}, now.Add(-3*time.Hour), "exec0-3", new(int64(0)))

		// check host script details, should include all no team scripts
		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host0.ID), nil, http.StatusOK, &resp)
		require.Len(t, resp.Scripts, len(noTeamScripts))
		byScriptID := make(map[uint]*automdm.HostScriptDetail, len(resp.Scripts))
		for _, s := range resp.Scripts {
			byScriptID[s.ScriptID] = s
		}
		for i, s := range noTeamScripts {
			gotScript, ok := byScriptID[s.ID]
			require.True(t, ok)
			require.Equal(t, s.Name, gotScript.Name)
			switch i {
			case 0:
				require.NotNil(t, gotScript.LastExecution)
				require.Equal(t, "exec0-0", gotScript.LastExecution.ExecutionID)
				require.Equal(t, now, gotScript.LastExecution.ExecutedAt)
				require.Equal(t, "ran", gotScript.LastExecution.Status)
			case 1:
				require.NotNil(t, gotScript.LastExecution)
				require.Equal(t, "exec0-1", gotScript.LastExecution.ExecutionID)
				require.Equal(t, now.Add(-1*time.Hour), gotScript.LastExecution.ExecutedAt)
				require.Equal(t, "error", gotScript.LastExecution.Status)
			case 2:
				require.NotNil(t, gotScript.LastExecution)
				require.Equal(t, "exec0-2", gotScript.LastExecution.ExecutionID)
				require.Equal(t, now.Add(-2*time.Hour), gotScript.LastExecution.ExecutedAt)
				require.Equal(t, "pending", gotScript.LastExecution.Status)
			default:
				require.Nil(t, gotScript.LastExecution)
			}
		}

		// default order is by name ascending; order_direction=desc reverses it
		require.True(t, slices.IsSortedFunc(resp.Scripts, func(a, b *automdm.HostScriptDetail) int { return strings.Compare(a.Name, b.Name) }))
		var descResp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host0.ID), nil, http.StatusOK, &descResp, "order_key", "name", "order_direction", "desc")
		require.Len(t, descResp.Scripts, len(resp.Scripts))
		for i := range resp.Scripts {
			require.Equal(t, resp.Scripts[i].Name, descResp.Scripts[len(descResp.Scripts)-1-i].Name)
		}
	})

	t.Run("team 1", func(t *testing.T) {
		tm1Scripts, _, err := s.ds.ListScripts(ctx, &tm1.ID, automdm.ListOptions{})
		require.NoError(t, err)
		require.Len(t, tm1Scripts, 5)

		// insert results for host1
		insertResults(t, host1.ID, tm1Scripts[0], now, "exec1-0", new(int64(0))) // expect status ran

		// check host script details, should match team 1
		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host1.ID), nil, http.StatusOK, &resp)
		require.Len(t, resp.Scripts, len(tm1Scripts))
		byScriptID := make(map[uint]*automdm.HostScriptDetail, len(resp.Scripts))
		for _, s := range resp.Scripts {
			byScriptID[s.ScriptID] = s
		}
		for i, s := range tm1Scripts {
			gotScript, ok := byScriptID[s.ID]
			require.True(t, ok)
			require.Equal(t, s.Name, gotScript.Name)
			switch i {
			case 0:
				require.NotNil(t, gotScript.LastExecution)
				require.Equal(t, "exec1-0", gotScript.LastExecution.ExecutionID)
				require.Equal(t, now, gotScript.LastExecution.ExecutedAt)
				require.Equal(t, "ran", gotScript.LastExecution.Status)
			default:
				require.Nil(t, gotScript.LastExecution)
			}
		}
	})

	t.Run("deleted script", func(t *testing.T) {
		noTeamScripts, _, err := s.ds.ListScripts(ctx, nil, automdm.ListOptions{})
		require.NoError(t, err)
		require.Len(t, noTeamScripts, 5)

		// delete a script
		s.Do("DELETE", fmt.Sprintf("/api/latest/automdm/scripts/%d", noTeamScripts[0].ID), nil, http.StatusNoContent)

		// check host script details, should not include deleted script
		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host0.ID), nil, http.StatusOK, &resp)
		require.Len(t, resp.Scripts, len(noTeamScripts)-1)
		byScriptID := make(map[uint]*automdm.HostScriptDetail, len(resp.Scripts))
		for _, s := range resp.Scripts {
			require.NotEqual(t, noTeamScripts[0].ID, s.ScriptID)
			byScriptID[s.ScriptID] = s
		}
		for i, s := range noTeamScripts {
			gotScript, ok := byScriptID[s.ID]
			if i == 0 {
				require.False(t, ok)
			} else {
				require.True(t, ok)
				require.Equal(t, s.Name, gotScript.Name)
				switch i {
				case 1:
					require.NotNil(t, gotScript.LastExecution)
					require.Equal(t, "exec0-1", gotScript.LastExecution.ExecutionID)
					require.Equal(t, now.Add(-1*time.Hour), gotScript.LastExecution.ExecutedAt)
					require.Equal(t, "error", gotScript.LastExecution.Status)
				case 2:
					require.NotNil(t, gotScript.LastExecution)
					require.Equal(t, "exec0-2", gotScript.LastExecution.ExecutionID)
					require.Equal(t, now.Add(-2*time.Hour), gotScript.LastExecution.ExecutedAt)
					require.Equal(t, "pending", gotScript.LastExecution.Status)
				case 3, 4:
					require.Nil(t, gotScript.LastExecution)
				default:
					require.Fail(t, "unexpected script")
				}
			}
		}
	})

	t.Run("transfer team", func(t *testing.T) {
		s.Do("POST", "/api/latest/automdm/hosts/transfer", addHostsToTeamRequest{
			TeamID:  new(uint(999999)),
			HostIDs: []uint{host1.ID},
		}, http.StatusNotFound)

		s.DoJSON("POST", "/api/latest/automdm/hosts/transfer", addHostsToTeamRequest{
			TeamID:  &tm2.ID,
			HostIDs: []uint{host1.ID},
		}, http.StatusOK, &addHostsToTeamResponse{})

		tm2Scripts, _, err := s.ds.ListScripts(ctx, &tm2.ID, automdm.ListOptions{})
		require.NoError(t, err)
		require.Len(t, tm2Scripts, 1)

		// check host script details, should not include prior team's scripts
		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host1.ID), nil, http.StatusOK, &resp)
		require.Len(t, resp.Scripts, len(tm2Scripts))
		byScriptID := make(map[uint]*automdm.HostScriptDetail, len(resp.Scripts))
		for _, s := range resp.Scripts {
			byScriptID[s.ScriptID] = s
		}
		for _, s := range tm2Scripts {
			gotScript, ok := byScriptID[s.ID]
			require.True(t, ok)
			require.Equal(t, s.Name, gotScript.Name)
			require.Nil(t, gotScript.LastExecution)
		}
	})

	t.Run("no scripts", func(t *testing.T) {
		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host2.ID), nil, http.StatusOK, &resp)
		require.NotNil(t, resp.Scripts)
		require.Empty(t, resp.Scripts)
	})

	t.Run("windows", func(t *testing.T) {
		team4Scripts, _, err := s.ds.ListScripts(ctx, &tm4.ID, automdm.ListOptions{})
		require.NoError(t, err)
		require.Len(t, team4Scripts, 1)

		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host3.ID), nil, http.StatusOK, &resp)
		require.NotNil(t, resp.Scripts)
		require.Len(t, resp.Scripts, 1)
	})

	t.Run("linux", func(t *testing.T) {
		require.Nil(t, host4.TeamID)
		noTeamScripts, _, err := s.ds.ListScripts(ctx, nil, automdm.ListOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, noTeamScripts)

		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host4.ID), nil, http.StatusOK, &resp)
		require.NotNil(t, resp.Scripts)
		require.Len(t, resp.Scripts, 4)

		for _, s := range resp.Scripts {
			require.Nil(t, s.LastExecution)
			require.Contains(t, s.Name, ".sh")
		}
	})

	// NOTE: Scripts are specified only for platforms other than macOS, Linux,
	// and Windows; however, we default to listing all scripts for unspecified platforms.
	// Separately, the UI restricts scripts related functionality to only macOS,
	// Linux, and Windows.
	t.Run("unspecified platform", func(t *testing.T) {
		var resp automdm.GetHostScriptDetailsResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/scripts", host5.ID), nil, http.StatusOK, &resp)
		require.NotNil(t, resp.Scripts)
		require.Len(t, resp.Scripts, 4)
	})

	t.Run("get script results user message", func(t *testing.T) {
		// add a script with an older created_at timestamp
		var oldScriptID, oldScriptContentsID uint
		mysqltest.ExecAdhocSQL(t, s.ds, func(tx sqlx.ExtContext) error {
			// create script_contents first
			res, err := tx.ExecContext(ctx, `
INSERT INTO
	script_contents (md5_checksum, contents, created_at)
VALUES
	(UNHEX(MD5(?)),?,?)`,
				"echo test-script-details-timeout",
				"echo test-script-details-timeout",
				now.Add(-1*time.Hour),
			)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}

			oldScriptContentsID = uint(id) //nolint:gosec // dismiss G115

			res, err = tx.ExecContext(ctx, `
INSERT INTO
	scripts (name, script_content_id, created_at, updated_at)
VALUES
	(?,?,?,?)`,
				"test-script-details-timeout.sh",
				oldScriptContentsID,
				now.Add(-1*time.Hour),
				now.Add(-1*time.Hour),
			)
			if err != nil {
				return err
			}
			id, err = res.LastInsertId()
			if err != nil {
				return err
			}
			oldScriptID = uint(id) //nolint:gosec // dismiss G115
			return nil
		})

		for _, c := range []struct {
			name       string
			exitCode   *int64
			executedAt time.Time
			expected   string
		}{
			{
				name:       "host-timeout",
				exitCode:   nil,
				executedAt: now.Add(-1 * time.Hour),
				expected:   automdm.RunScriptHostTimeoutErrMsg,
			},
			{
				name:       "script-timeout",
				exitCode:   new(int64(-1)),
				executedAt: now.Add(-1 * time.Hour),
				expected:   automdm.HostScriptTimeoutMessage(new(int(scripts.MaxHostExecutionTime.Seconds()))),
			},
			{
				name:       "pending",
				exitCode:   nil,
				executedAt: now.Add(-1 * time.Minute),
				expected:   automdm.RunScriptAlreadyRunningErrMsg,
			},
			{
				name:       "success",
				exitCode:   new(int64(0)),
				executedAt: now.Add(-1 * time.Hour),
				expected:   "",
			},
			{
				name:       "error",
				exitCode:   new(int64(1)),
				executedAt: now.Add(-1 * time.Hour),
				expected:   "",
			},
			{
				name:       "disabled",
				exitCode:   new(int64(-2)),
				executedAt: now.Add(-1 * time.Hour),
				expected:   automdm.RunScriptDisabledErrMsg,
			},
		} {
			t.Run(c.name, func(t *testing.T) {
				insertResults(t, host0.ID, &automdm.Script{ID: oldScriptID, ScriptContentID: oldScriptContentsID, Name: "test-script-details-timeout.sh"}, c.executedAt, "test-user-message_"+c.name, c.exitCode)

				var resp automdm.GetScriptResultResponse
				s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/scripts/results/%s", "test-user-message_"+c.name), nil, http.StatusOK, &resp)
				require.Equal(t, c.expected, resp.Message)
			})
		}
	})
}

func (s *integrationEnterpriseTestSuite) TestBatchApplyScriptsEndpoints() {
	t := s.T()
	ctx := context.Background()

	saveAndCheckScripts := func(team *automdm.Team, scripts []automdm.ScriptPayload) {
		var teamID *uint
		teamIDStr := ""
		teamActivity := `{"team_id": null, "team_name": null}`
		if team != nil {
			teamID = &team.ID
			teamIDStr = fmt.Sprint(team.ID)
			teamActivity = fmt.Sprintf(`{"team_id": %d, "team_name": %q}`, team.ID, team.Name)
		}

		// create, check activities, and check scripts response
		var scriptsBatchResponse automdm.BatchSetScriptsResponse
		s.DoJSON("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: scripts}, http.StatusOK, &scriptsBatchResponse, "team_id", teamIDStr)
		s.lastActivityMatches(
			automdm.ActivityTypeEditedScript{}.ActivityName(),
			teamActivity,
			0,
		)
		require.Len(t, scriptsBatchResponse.Scripts, len(scripts))

		// check that the right values got stored in the db
		var listResp automdm.ListScriptsResponse
		s.DoJSON("GET", "/api/latest/automdm/scripts", nil, http.StatusOK, &listResp, "team_id", teamIDStr)
		require.Len(t, listResp.Scripts, len(scripts))

		got := make([]automdm.ScriptPayload, len(scripts))
		for i, gotScript := range listResp.Scripts {
			// add the script contents
			res := s.Do("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", gotScript.ID), nil, http.StatusOK, "alt", "media")
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			got[i] = automdm.ScriptPayload{
				Name:           gotScript.Name,
				ScriptContents: b,
			}
			// check that it belongs to the right team
			require.Equal(t, teamID, gotScript.TeamID)
		}

		require.ElementsMatch(t, scripts, got)
	}

	// create a new team
	tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "batch_set_scripts"})
	require.NoError(t, err)

	// apply an empty set to no-team
	saveAndCheckScripts(nil, nil)

	// apply to both team id and name
	s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: nil},
		http.StatusUnprocessableEntity, "team_id", fmt.Sprint(tm.ID), "team_name", tm.Name)

	// invalid team name
	s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: nil},
		http.StatusNotFound, "team_name", uuid.New().String())

	// duplicate script names
	s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
		{Name: "N1.sh", ScriptContents: []byte("foo")},
		{Name: "N1.sh", ScriptContents: []byte("bar")},
	}}, http.StatusUnprocessableEntity, "team_id", fmt.Sprint(tm.ID))

	// script names that differ by case and accent are the same name to the database
	res := s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
		{Name: "Bêta.sh", ScriptContents: []byte("foo")},
		{Name: "beta.sh", ScriptContents: []byte("bar")},
	}}, http.StatusUnprocessableEntity, "team_id", fmt.Sprint(tm.ID))
	require.Contains(t, extractServerErrorText(res.Body), `More than one script has the same file name: "beta.sh"`)

	// invalid script name
	s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
		{Name: "N1", ScriptContents: []byte("foo")},
	}}, http.StatusUnprocessableEntity, "team_id", fmt.Sprint(tm.ID))

	// empty script name
	s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
		{Name: "", ScriptContents: []byte("foo")},
	}}, http.StatusUnprocessableEntity, "team_id", fmt.Sprint(tm.ID))

	// invalid secret
	s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
		{Name: "N2.sh", ScriptContents: []byte("echo $AUTOMDM_SECRET_INVALID")},
	}}, http.StatusUnprocessableEntity, "team_id", fmt.Sprint(tm.ID))

	// successfully apply a scripts for the team
	saveAndCheckScripts(tm, []automdm.ScriptPayload{
		{Name: "N1.sh", ScriptContents: []byte("foo")},
		{Name: "N2.sh", ScriptContents: []byte("bar")},
	})

	// successfully apply scripts for "no team"
	saveAndCheckScripts(nil, []automdm.ScriptPayload{
		{Name: "N1.sh", ScriptContents: []byte("foo")},
		{Name: "N2.sh", ScriptContents: []byte("bar")},
	})

	// edit, delete and add a new one for "no team"
	saveAndCheckScripts(nil, []automdm.ScriptPayload{
		{Name: "N2.sh", ScriptContents: []byte("bar-edited")},
		{Name: "N3.sh", ScriptContents: []byte("baz")},
	})

	// edit, delete and add a new one for the team
	saveAndCheckScripts(tm, []automdm.ScriptPayload{
		{Name: "N2.sh", ScriptContents: []byte("bar-edited")},
		{Name: "N3.sh", ScriptContents: []byte("baz")},
	})

	// remove all scripts for a team
	saveAndCheckScripts(tm, nil)

	// remove all scripts for "no team"
	saveAndCheckScripts(nil, nil)
}

func (s *integrationEnterpriseTestSuite) TestHostScriptSoftDelete() {
	t := s.T()
	ctx := context.Background()

	// create a host and request a script execution
	tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "host_soft_delete_team"})
	require.NoError(t, err)
	host := createOrbitEnrolledHost(t, "linux", "", s.ds)
	err = s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&tm.ID, []uint{host.ID}))
	require.NoError(t, err)

	// create an anonymous script execution request
	var runResp automdm.RunScriptResponse
	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo"}, http.StatusAccepted, &runResp)
	scriptExecID := runResp.ExecutionID

	// post a script result so that the (past) activity is created
	s.Do("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *host.OrbitNodeKey, scriptExecID)),
		http.StatusOK)
	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeRanScript{}.ActivityName(),
		fmt.Sprintf(
			`{"host_id": %d, "host_display_name": %q, "script_name": "", "script_execution_id": %q, "async": true, "policy_id": null, "policy_name": null, "batch_execution_id": null, "from_setup_experience": false}`,
			host.ID, host.DisplayName(), scriptExecID), 0)

	// create a saved script execution request
	var newScriptResp automdm.CreateScriptResponse
	body, headers := generateNewScriptMultipartRequest(t,
		"script1.sh", []byte(`echo "hello"`), s.token, map[string][]string{"team_id": {fmt.Sprint(tm.ID)}})
	res := s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusOK, headers)
	err = json.NewDecoder(res.Body).Decode(&newScriptResp)
	require.NoError(t, err)
	require.NotZero(t, newScriptResp.ScriptID)
	savedScriptID := newScriptResp.ScriptID

	s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptID: &savedScriptID}, http.StatusAccepted, &runResp)
	savedScriptExecID := runResp.ExecutionID

	// post a script result so that the (past) activity is created
	s.Do("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "saved"}`, *host.OrbitNodeKey, savedScriptExecID)),
		http.StatusOK)
	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeRanScript{}.ActivityName(),
		fmt.Sprintf(`{"host_id": %d, "host_display_name": %q, "script_name": "script1.sh", "script_execution_id": %q, "async": true, "policy_id": null, "policy_name": null, "batch_execution_id": null, "from_setup_experience": false}`,
			host.ID, host.DisplayName(), savedScriptExecID), 0)

	// get the anonymous script result details
	var scriptRes automdm.GetScriptResultResponse
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+scriptExecID, nil, http.StatusOK, &scriptRes)
	require.Equal(t, scriptExecID, scriptRes.ExecutionID)
	require.Equal(t, host.ID, scriptRes.HostID)
	require.Equal(t, "ok", scriptRes.Output)
	require.NotNil(t, scriptRes.ExitCode)
	require.EqualValues(t, 0, *scriptRes.ExitCode)

	// get the saved script result details
	scriptRes = automdm.GetScriptResultResponse{}
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+savedScriptExecID, nil, http.StatusOK, &scriptRes)
	require.Equal(t, savedScriptExecID, scriptRes.ExecutionID)
	require.Equal(t, host.ID, scriptRes.HostID)
	require.Equal(t, "saved", scriptRes.Output)
	require.NotNil(t, scriptRes.ExitCode)
	require.EqualValues(t, 0, *scriptRes.ExitCode)

	// delete the host
	var deleteResp deleteHostResponse
	s.DoJSON("DELETE", fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID), nil, http.StatusOK, &deleteResp)

	// get the anonymous script result details, still works
	scriptRes = automdm.GetScriptResultResponse{}
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+scriptExecID, nil, http.StatusOK, &scriptRes)
	require.Equal(t, scriptExecID, scriptRes.ExecutionID)
	require.Equal(t, host.ID, scriptRes.HostID)
	require.Equal(t, "ok", scriptRes.Output)
	require.NotNil(t, scriptRes.ExitCode)
	require.EqualValues(t, 0, *scriptRes.ExitCode)

	// get the saved script result details, still works
	scriptRes = automdm.GetScriptResultResponse{}
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+savedScriptExecID, nil, http.StatusOK, &scriptRes)
	require.Equal(t, savedScriptExecID, scriptRes.ExecutionID)
	require.Equal(t, host.ID, scriptRes.HostID)
	require.Equal(t, "saved", scriptRes.Output)
	require.NotNil(t, scriptRes.ExitCode)
	require.EqualValues(t, 0, *scriptRes.ExitCode)

	// delete the named script
	s.Do("DELETE", fmt.Sprintf("/api/latest/automdm/scripts/%d", savedScriptID), nil, http.StatusNoContent)

	// get the saved script result details, still works because the saved script
	// is a "soft-reference", when deleted the results become essentially results
	// for an anonymous script (i.e. the script_id FK is "ON DELETE SET NULL").
	scriptRes = automdm.GetScriptResultResponse{}
	s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+savedScriptExecID, nil, http.StatusOK, &scriptRes)
	require.Equal(t, savedScriptExecID, scriptRes.ExecutionID)
	require.Equal(t, host.ID, scriptRes.HostID)
	require.Equal(t, "saved", scriptRes.Output)
	require.NotNil(t, scriptRes.ExitCode)
	require.EqualValues(t, 0, *scriptRes.ExitCode)
}

func (s *integrationEnterpriseTestSuite) TestScriptAutoMDMVariables() {
	t := s.T()
	ctx := context.Background()

	host := createOrbitEnrolledHost(t, "linux", "script-automdm-vars", s.ds)
	err := s.ds.MarkHostsSeen(ctx, []uint{host.ID}, time.Now())
	require.NoError(t, err)

	const (
		supportedVarContents   = "echo $AUTOMDM_VAR_HOST_UUID on ${AUTOMDM_VAR_HOST_PLATFORM}"
		unsupportedVarContents = "echo $AUTOMDM_VAR_NONEXISTENT"
		unsupportedVarErrMsg   = "AutoMDM variable $AUTOMDM_VAR_NONEXISTENT is not supported in scripts."
	)

	t.Run("ad-hoc run", func(t *testing.T) {
		res := s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: unsupportedVarContents}, http.StatusUnprocessableEntity)
		require.Contains(t, extractServerErrorText(res.Body), unsupportedVarErrMsg)

		// CA variables are profile-delivery machinery and are rejected in scripts
		res = s.Do("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo $AUTOMDM_VAR_NDES_SCEP_PROXY_URL"}, http.StatusUnprocessableEntity)
		require.Contains(t, extractServerErrorText(res.Body), "AutoMDM variable $AUTOMDM_VAR_NDES_SCEP_PROXY_URL is not supported in scripts.")

		var runResp automdm.RunScriptResponse
		s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: supportedVarContents}, http.StatusAccepted, &runResp)

		// contents are stored unexpanded
		result, err := s.ds.GetHostScriptExecutionResult(ctx, runResp.ExecutionID)
		require.NoError(t, err)
		require.Equal(t, supportedVarContents, result.ScriptContents)
	})

	t.Run("saved script create and update", func(t *testing.T) {
		body, headers := generateNewScriptMultipartRequest(t,
			"vars-bad.sh", []byte(unsupportedVarContents), s.token, nil)
		res := s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusUnprocessableEntity, headers)
		require.Contains(t, extractServerErrorText(res.Body), unsupportedVarErrMsg)

		var newScriptResp automdm.CreateScriptResponse
		body, headers = generateNewScriptMultipartRequest(t,
			"vars-good.sh", []byte(supportedVarContents), s.token, nil)
		res = s.DoRawWithHeaders("POST", "/api/latest/automdm/scripts", body.Bytes(), http.StatusOK, headers)
		err := json.NewDecoder(res.Body).Decode(&newScriptResp)
		require.NoError(t, err)

		// contents round-trip unexpanded
		res = s.Do("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", newScriptResp.ScriptID), nil, http.StatusOK, "alt", "media")
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, supportedVarContents, string(b))

		body, headers = generateNewScriptMultipartRequest(t,
			"vars-good.sh", []byte(unsupportedVarContents), s.token, nil)
		res = s.DoRawWithHeaders("PATCH", fmt.Sprintf("/api/latest/automdm/scripts/%d", newScriptResp.ScriptID), body.Bytes(), http.StatusUnprocessableEntity, headers)
		require.Contains(t, extractServerErrorText(res.Body), unsupportedVarErrMsg)

		body, headers = generateNewScriptMultipartRequest(t,
			"vars-good.sh", []byte(supportedVarContents+"\necho updated"), s.token, nil)
		s.DoRawWithHeaders("PATCH", fmt.Sprintf("/api/latest/automdm/scripts/%d", newScriptResp.ScriptID), body.Bytes(), http.StatusOK, headers)
	})

	t.Run("batch set scripts", func(t *testing.T) {
		tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name()})
		require.NoError(t, err)

		// unsupported variables are rejected, including on dry runs
		for _, dryRun := range []string{"true", "false"} {
			res := s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
				{Name: "vars.sh", ScriptContents: []byte(unsupportedVarContents)},
			}}, http.StatusUnprocessableEntity, "team_id", fmt.Sprint(tm.ID), "dry_run", dryRun)
			require.Contains(t, extractServerErrorText(res.Body), unsupportedVarErrMsg, "dry_run=%s", dryRun)
		}

		// supported variables pass a dry run without being saved
		s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
			{Name: "vars.sh", ScriptContents: []byte(supportedVarContents)},
		}}, http.StatusOK, "team_id", fmt.Sprint(tm.ID), "dry_run", "true")
		var listResp automdm.ListScriptsResponse
		s.DoJSON("GET", "/api/latest/automdm/scripts", nil, http.StatusOK, &listResp, "team_id", fmt.Sprint(tm.ID))
		require.Empty(t, listResp.Scripts)

		// and are saved unexpanded on a real apply
		s.Do("POST", "/api/v1/automdm/scripts/batch", automdm.BatchSetScriptsRequest{Scripts: []automdm.ScriptPayload{
			{Name: "vars.sh", ScriptContents: []byte(supportedVarContents)},
		}}, http.StatusOK, "team_id", fmt.Sprint(tm.ID))
		s.DoJSON("GET", "/api/latest/automdm/scripts", nil, http.StatusOK, &listResp, "team_id", fmt.Sprint(tm.ID))
		require.Len(t, listResp.Scripts, 1)
		res := s.Do("GET", fmt.Sprintf("/api/latest/automdm/scripts/%d", listResp.Scripts[0].ID), nil, http.StatusOK, "alt", "media")
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, supportedVarContents, string(b))
	})

	t.Run("setup experience script", func(t *testing.T) {
		body, headers := generateNewScriptMultipartRequest(t,
			"setup-vars.sh", []byte(unsupportedVarContents), s.token, nil)
		res := s.DoRawWithHeaders("POST", "/api/latest/automdm/setup_experience/script", body.Bytes(), http.StatusUnprocessableEntity, headers)
		require.Contains(t, extractServerErrorText(res.Body), unsupportedVarErrMsg)

		body, headers = generateNewScriptMultipartRequest(t,
			"setup-vars.sh", []byte(supportedVarContents), s.token, nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/setup_experience/script", body.Bytes(), http.StatusOK, headers)
		s.Do("DELETE", "/api/latest/automdm/setup_experience/script", nil, http.StatusOK)
	})
}

func (s *integrationEnterpriseTestSuite) TestScriptAutoMDMVariablesExecution() {
	t := s.T()
	ctx := context.Background()

	host := createOrbitEnrolledHost(t, "ubuntu", "vars-exec-1", s.ds)
	host2 := createOrbitEnrolledHost(t, "ubuntu", "vars-exec-2", s.ds)
	err := s.ds.MarkHostsSeen(ctx, []uint{host.ID, host2.ID}, time.Now())
	require.NoError(t, err)

	orbitFetchScript := func(t *testing.T, h *automdm.Host, execID string) automdm.OrbitGetScriptResponse {
		var resp automdm.OrbitGetScriptResponse
		s.DoJSON("POST", "/api/automdm/orbit/scripts/request",
			json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q}`, *h.OrbitNodeKey, execID)),
			http.StatusOK, &resp)
		return resp
	}

	t.Run("host variables resolve per host at fetch time", func(t *testing.T) {
		const contents = "echo serial=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL uuid=$AUTOMDM_VAR_HOST_UUID plat=${AUTOMDM_VAR_HOST_PLATFORM}"

		for _, h := range []*automdm.Host{host, host2} {
			var runResp automdm.RunScriptResponse
			s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: h.ID, ScriptContents: contents}, http.StatusAccepted, &runResp)

			fetched := orbitFetchScript(t, h, runResp.ExecutionID)
			requireVarsDelivered(t, fetched.ScriptContents, contents, map[string]string{
				"HOST_HARDWARE_SERIAL": h.HardwareSerial,
				"HOST_UUID":            h.UUID,
				"HOST_PLATFORM":        "ubuntu",
			})
			require.Nil(t, fetched.ExitCode)

			// stored contents stay unexpanded
			stored, err := s.ds.GetHostScriptExecutionResult(ctx, runResp.ExecutionID)
			require.NoError(t, err)
			require.Equal(t, contents, stored.ScriptContents)

			// the host posts its result to complete the execution
			var orbitPostScriptResp automdm.OrbitPostScriptResultResponse
			s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
				json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *h.OrbitNodeKey, runResp.ExecutionID)),
				http.StatusOK, &orbitPostScriptResp)
		}
	})

	t.Run("unresolvable IdP variable fails the execution without wedging the queue", func(t *testing.T) {
		// queue two scripts: the first needs an IdP user the host doesn't have
		var failResp automdm.RunScriptResponse
		s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"}, http.StatusAccepted, &failResp)
		var okResp automdm.RunScriptResponse
		s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo queued-behind"}, http.StatusAccepted, &okResp)

		// fetching the first script records the failure and returns it marked
		fetched := orbitFetchScript(t, host, failResp.ExecutionID)
		require.NotNil(t, fetched.ExitCode)
		require.EqualValues(t, automdm.ExitCodeAutoMDMVarResolutionFailed, *fetched.ExitCode)

		// the failed result is stored with the reason as output
		stored, err := s.ds.GetHostScriptExecutionResult(ctx, failResp.ExecutionID)
		require.NoError(t, err)
		require.NotNil(t, stored.ExitCode)
		require.EqualValues(t, automdm.ExitCodeAutoMDMVarResolutionFailed, *stored.ExitCode)
		require.Contains(t, stored.Output, "There is no IdP username for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME.")

		// a ran_script activity was created for the failed execution
		s.lastActivityMatches(automdm.ActivityTypeRanScript{}.ActivityName(),
			fmt.Sprintf(`{"host_id": %d, "host_display_name": %q, "script_execution_id": %q, "script_name": "", "async": true, "batch_execution_id": null, "policy_id": null, "policy_name": null, "from_setup_experience": false}`,
				host.ID, host.DisplayName(), failResp.ExecutionID), 0)

		// the results endpoint returns the variable-resolution-failure user message
		var scriptResultResp automdm.GetScriptResultResponse
		s.DoJSON("GET", "/api/latest/automdm/scripts/results/"+failResp.ExecutionID, nil, http.StatusOK, &scriptResultResp)
		require.Equal(t, automdm.RunScriptAutoMDMVarsFailedErrMsg, scriptResultResp.Message)
		require.Contains(t, scriptResultResp.Output, "There is no IdP username for this host.")

		// the queue advanced: the second script activated and is fetchable
		fetched = orbitFetchScript(t, host, okResp.ExecutionID)
		require.Nil(t, fetched.ExitCode)
		require.Equal(t, "echo queued-behind", fetched.ScriptContents)

		var orbitPostScriptResp automdm.OrbitPostScriptResultResponse
		s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
			json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *host.OrbitNodeKey, okResp.ExecutionID)),
			http.StatusOK, &orbitPostScriptResp)
	})

	t.Run("IdP variables resolve and secret-shaped values stay literal", func(t *testing.T) {
		// link an IdP user to host2 whose username carries $AUTOMDM_SECRET_* text;
		// variables expand after secrets, so it must reach the host literally
		var scimUserID int64
		mysqltest.ExecAdhocSQL(t, s.ds, func(db sqlx.ExtContext) error {
			res, err := db.ExecContext(ctx,
				`INSERT INTO scim_users (user_name, given_name, family_name, department, active) VALUES (?, ?, ?, ?, ?)`,
				"jane.doe@example.com ($AUTOMDM_SECRET_INJECTED)", "Jane", "Doe", "Engineering", 1)
			if err != nil {
				return err
			}
			scimUserID, err = res.LastInsertId()
			return err
		})
		mysqltest.ExecAdhocSQL(t, s.ds, func(db sqlx.ExtContext) error {
			_, err := db.ExecContext(ctx,
				`INSERT INTO host_scim_user (host_id, scim_user_id) VALUES (?, ?)`,
				host2.ID, scimUserID)
			return err
		})

		const contents = "user=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME local=user_${AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART}@corp.com dept=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT"
		var runResp automdm.RunScriptResponse
		s.DoJSON("POST", "/api/latest/automdm/scripts/run", automdm.HostScriptRequestPayload{
			HostID:         host2.ID,
			ScriptContents: contents,
		}, http.StatusAccepted, &runResp)

		fetched := orbitFetchScript(t, host2, runResp.ExecutionID)
		require.Nil(t, fetched.ExitCode)
		requireVarsDelivered(t, fetched.ScriptContents, contents, map[string]string{
			"HOST_END_USER_IDP_USERNAME":            "jane.doe@example.com ($AUTOMDM_SECRET_INJECTED)",
			"HOST_END_USER_IDP_USERNAME_LOCAL_PART": "jane.doe",
			"HOST_END_USER_IDP_DEPARTMENT":          "Engineering",
		})
	})

	t.Run("sync run surfaces the resolution failure", func(t *testing.T) {
		testRunScriptWaitForResult = 5 * time.Second
		defer func() { testRunScriptWaitForResult = 0 }()

		// fetch the script from the host side as soon as it is enqueued, which
		// records the failure while the sync run is waiting for a result
		done := make(chan struct{})
		var syncResp automdm.RunScriptSyncResponse
		go func() {
			defer close(done)
			s.DoJSON("POST", "/api/latest/automdm/scripts/run/sync", automdm.HostScriptRequestPayload{HostID: host.ID, ScriptContents: "echo $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"}, http.StatusOK, &syncResp)
		}()

		require.Eventually(t, func() bool {
			// no require inside the condition: it runs on a separate goroutine,
			// where FailNow is not supported
			pending, err := s.ds.ListPendingHostScriptExecutions(ctx, host.ID)
			if err != nil || len(pending) == 0 {
				return false
			}
			orbitFetchScript(t, host, pending[0].ExecutionID)
			return true
		}, 3*time.Second, 100*time.Millisecond)

		<-done
		require.NotNil(t, syncResp.ExitCode)
		require.EqualValues(t, automdm.ExitCodeAutoMDMVarResolutionFailed, *syncResp.ExitCode)
		require.Equal(t, automdm.RunScriptAutoMDMVarsFailedErrMsg, syncResp.Message)
		require.Contains(t, syncResp.Output, "There is no IdP username for this host.")
	})
}

package service

// Apple configuration profile tests for the MDM suite.
//
// Belongs here: macOS, iOS, and iPadOS profile delivery and status (including user-scoped profiles
// and scope changes), retries, deletion, listing Apple profiles and a host's profiles, Apple custom
// settings in app config and team specs, self-service Apple profiles, and DDM OS update
// declarations after an MDM reset.
//
// Does not belong here: profile CRUD shared across platforms (integration_mdm_profiles_crud_test.go),
// batch endpoints (integration_mdm_profiles_batch_test.go), install/remove edge cases in the profile
// reconciler (integration_mdm_profiles_reconciler_test.go), certificate payloads
// (integration_mdm_profiles_certs_test.go), and DDM declarations (integration_mdm_ddm_test.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/mdm/mdmtest"
	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	servermdm "github.com/anilyagiz/automdm/v4/server/mdm"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/mdm"
	"github.com/anilyagiz/automdm/v4/server/service/redis_key_value"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/micromdm/plist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *integrationMDMTestSuite) TestAppleProfileManagement() {
	t := s.T()
	ctx := context.Background()

	err := s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: t.Name()}})
	require.NoError(t, err)

	globalProfiles := [][]byte{
		mobileconfigForTest("N1", "I1"),
		mobileconfigForTest("N2", "I2"),
	}
	wantGlobalProfiles := globalProfiles
	wantGlobalProfiles = append(
		wantGlobalProfiles,
		setupExpectedAutoMDMdProfile(t, s.server.URL, t.Name(), nil),
	)

	// add global profiles
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)

	// invalid secrets
	invalidSecretsProfile := []byte(`
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array/>
	<key>PayloadDisplayName</key>
	<string>My profile</string>
	<key>PayloadIdentifier</key>
	<string>$AUTOMDM_SECRET_INVALID</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>601E0B42-0989-4FAD-A61B-18656BA3670E</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`)

	res := s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest([][]byte{invalidSecretsProfile}), http.StatusUnprocessableEntity)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "PayloadIdentifier cannot contain AUTOMDM_SECRET variables")

	invalidSecretsProfile = []byte(`
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array/>
	<key>PayloadDisplayName</key>
	<string>$AUTOMDM_SECRET_INVALID</string>
	<key>PayloadIdentifier</key>
	<string>N3</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>601E0B42-0989-4FAD-A61B-18656BA3670E</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`)

	res = s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest([][]byte{invalidSecretsProfile}), http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "PayloadDisplayName cannot contain AUTOMDM_SECRET variables")

	// create a new team
	tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "batch_set_mdm_profiles"})
	require.NoError(t, err)

	// add an enroll secret so the automdmd profiles differ
	var teamResp teamEnrollSecretsResponse
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d/secrets", tm.ID),
		modifyTeamEnrollSecretsRequest{
			Secrets: []automdm.EnrollSecret{{Secret: "team1_enroll_sec"}},
		}, http.StatusOK, &teamResp)

	teamProfiles := [][]byte{
		mobileconfigForTest("N3", "I3"),
	}
	wantTeamProfiles := teamProfiles
	wantTeamProfiles = append(
		wantTeamProfiles,
		setupExpectedAutoMDMdProfile(t, s.server.URL, "team1_enroll_sec", &tm.ID),
	)
	// add profiles to the team
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(teamProfiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm.ID))

	// create a non-macOS host
	_, err = s.ds.NewHost(context.Background(), &automdm.Host{
		ID:            1,
		OsqueryHostID: new("non-macos-host"),
		NodeKey:       new("non-macos-host"),
		UUID:          uuid.New().String(),
		Hostname:      fmt.Sprintf("%sfoo.local.non.macos", t.Name()),
		Platform:      "windows",
	})
	require.NoError(t, err)

	// create a host that's not enrolled into MDM
	_, err = s.ds.NewHost(context.Background(), &automdm.Host{
		ID:            2,
		OsqueryHostID: new("not-mdm-enrolled"),
		NodeKey:       new("not-mdm-enrolled"),
		UUID:          uuid.New().String(),
		Hostname:      fmt.Sprintf("%sfoo.local.not.enrolled", t.Name()),
		Platform:      "darwin",
	})
	require.NoError(t, err)

	// calls ensure automdm profiles
	s.awaitTriggerProfileSchedule(t)

	// Create a host and then enroll to MDM.
	host, mdmDevice := createHostThenEnrollMDM(s.ds, s.server.URL, t)
	setupPusher(s, t, mdmDevice)

	s.awaitRunAppleMDMWorkerSchedule() // run the worker to process the enrollment and queue profiles
	installs, removes := checkNextPayloads(t, mdmDevice, false)
	// verify that we received all profiles
	s.signedProfilesMatch(
		append(wantGlobalProfiles, setupExpectedCAProfile(t, s.ds)),
		installs,
	)
	require.Empty(t, removes)

	expectedNoTeamSummary := automdm.MDMProfilesSummary{
		Pending:   0,
		Failed:    0,
		Verifying: 1,
		Verified:  0,
	}
	expectedTeamSummary := automdm.MDMProfilesSummary{}
	s.checkMDMProfilesSummaries(t, nil, expectedNoTeamSummary)
	s.checkMDMProfilesSummaries(t, &tm.ID, expectedTeamSummary) // empty because no hosts in team

	// remove the key, to simulate key expiration.
	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+host.UUID)
	require.NoError(t, err)

	// add the host to a team
	err = s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&tm.ID, []uint{host.ID}))
	require.NoError(t, err)

	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	// verify that we should install the team profile
	s.signedProfilesMatch(wantTeamProfiles, installs)
	// verify that we should delete both profiles
	require.ElementsMatch(t, []string{"I1", "I2"}, removes)

	expectedNoTeamSummary = automdm.MDMProfilesSummary{}
	expectedTeamSummary = automdm.MDMProfilesSummary{
		Pending:   0,
		Failed:    0,
		Verifying: 1,
		Verified:  0,
	}
	s.checkMDMProfilesSummaries(t, nil, expectedNoTeamSummary)  // empty because host was transferred
	s.checkMDMProfilesSummaries(t, &tm.ID, expectedTeamSummary) // host now verifying team profiles

	// Use secret variables in a profile
	secretIdentifier := "secret-identifier-1"
	secretType := "secret.type.1"
	secretProfile := string(mobileconfigForTest("NS1", "IS1"))
	req := automdm.CreateSecretVariablesRequest{
		SecretVariables: []automdm.SecretVariable{
			{
				Name:  "AUTOMDM_SECRET_IDENTIFIER",
				Value: secretIdentifier,
			},
			{
				Name:  "AUTOMDM_SECRET_TYPE",
				Value: secretType,
			},
			{
				Name:  "AUTOMDM_SECRET_PROFILE",
				Value: secretProfile,
			},
		},
	}
	secretResp := automdm.CreateSecretVariablesResponse{}
	s.DoJSON("PUT", "/api/latest/automdm/spec/secret_variables", req, http.StatusOK, &secretResp)

	// set new team profiles (delete + addition)
	teamProfiles = [][]byte{
		mobileconfigForTest("N4", "I4"),
		mobileconfigForTestWithContent("N5", "I5", "$AUTOMDM_SECRET_IDENTIFIER", "${AUTOMDM_SECRET_TYPE}",
			"InnerName5"),
		// The whole profile is one big secret.
		[]byte("$AUTOMDM_SECRET_PROFILE"),
	}
	// We deep copy one of the team profiles because we will modify the slice in place, and we want to keep the originals for later.
	wantTeamProfiles = [][]byte{
		teamProfiles[0],
		make([]byte, len(teamProfiles[1])),
		{},
	}
	copy(wantTeamProfiles[1], teamProfiles[1])
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(teamProfiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm.ID))

	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	// Manually replace the expected secret variables in the profile
	wantTeamProfiles[1] = []byte(strings.ReplaceAll(string(wantTeamProfiles[1]), "$AUTOMDM_SECRET_IDENTIFIER", secretIdentifier))
	wantTeamProfiles[1] = []byte(strings.ReplaceAll(string(wantTeamProfiles[1]), "${AUTOMDM_SECRET_TYPE}", secretType))
	wantTeamProfiles[2] = []byte(secretProfile)
	// verify that we should install the team profiles
	s.signedProfilesMatch(wantTeamProfiles, installs)
	// verify that we should delete the old team profiles
	require.ElementsMatch(t, []string{"I3"}, removes)

	s.checkMDMProfilesSummaries(t, nil, expectedNoTeamSummary)  // empty because host was transferred
	s.checkMDMProfilesSummaries(t, &tm.ID, expectedTeamSummary) // host still verifying team profiles

	// Upload the same profiles again. No changes expected.
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(teamProfiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm.ID))
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Empty(t, installs)
	require.Empty(t, removes)

	// Change the secret variable and upload the profiles again. We should see the profile with updated secret installed.
	secretType = "new.secret.type.1"
	req = automdm.CreateSecretVariablesRequest{
		SecretVariables: []automdm.SecretVariable{
			{
				Name:  "AUTOMDM_SECRET_IDENTIFIER",
				Value: secretIdentifier, // did not change
			},
			{
				Name:  "AUTOMDM_SECRET_TYPE",
				Value: secretType, // changed
			},
			{
				Name:  "AUTOMDM_SECRET_PROFILE",
				Value: secretProfile, // did not change
			},
		},
	}
	s.DoJSON("PUT", "/api/latest/automdm/spec/secret_variables", req, http.StatusOK, &secretResp)
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(teamProfiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm.ID))
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	// Manually replace the expected secret variables in the profile
	wantTeamProfilesChanged := [][]byte{
		teamProfiles[1],
	}
	wantTeamProfilesChanged[0] = []byte(strings.ReplaceAll(string(wantTeamProfilesChanged[0]), "$AUTOMDM_SECRET_IDENTIFIER",
		secretIdentifier))
	wantTeamProfilesChanged[0] = []byte(strings.ReplaceAll(string(wantTeamProfilesChanged[0]), "${AUTOMDM_SECRET_TYPE}", secretType))
	// verify that we should install the team profiles
	s.signedProfilesMatch(wantTeamProfilesChanged, installs)
	wantTeamProfiles[1] = wantTeamProfilesChanged[0]
	// No profiles should be deleted
	assert.Empty(t, removes)

	// Clear the profiles, first as a dry run.
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: backwardsCompatProfilesParam{}}, http.StatusNoContent, "team_id",
		fmt.Sprint(tm.ID), "dry_run", "true")
	s.assertConfigProfilesByIdentifier(&tm.ID, "IS1", true)
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: backwardsCompatProfilesParam{}}, http.StatusNoContent, "team_id",
		fmt.Sprint(tm.ID), "dry_run", "false")
	s.assertConfigProfilesByIdentifier(&tm.ID, "IS1", false)
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Empty(t, installs)
	assert.Len(t, removes, 3)

	// And reapply the same profiles, now with explicit names.
	batchRequest := batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "N4", Contents: teamProfiles[0]},
		{Name: "N5", Contents: teamProfiles[1]},
		{Name: "NS1", Contents: teamProfiles[2]},
	}}
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchRequest, http.StatusNoContent, "team_id", fmt.Sprint(tm.ID), "dry_run", "true")
	s.assertConfigProfilesByIdentifier(&tm.ID, "I4", false)
	s.assertConfigProfilesByIdentifier(&tm.ID, "I5", false)
	s.assertConfigProfilesByIdentifier(&tm.ID, "IS1", false)
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchRequest, http.StatusNoContent, "team_id", fmt.Sprint(tm.ID))
	s.assertConfigProfilesByIdentifier(&tm.ID, "IS1", true)
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	assert.Empty(t, removes)
	// verify that we should install the team profiles
	s.signedProfilesMatch(wantTeamProfiles, installs)

	// Upload the same profiles again. No changes expected.
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchRequest, http.StatusNoContent, "team_id", fmt.Sprint(tm.ID), "dry_run", "true")
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchRequest, http.StatusNoContent, "team_id", fmt.Sprint(tm.ID))
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Empty(t, installs)
	require.Empty(t, removes)

	// Change the secret variable and upload the profiles again. We should see the profile with updated secret installed.
	secretType = "new2.secret.type.1"
	req = automdm.CreateSecretVariablesRequest{
		SecretVariables: []automdm.SecretVariable{
			{
				Name:  "AUTOMDM_SECRET_IDENTIFIER",
				Value: secretIdentifier, // did not change
			},
			{
				Name:  "AUTOMDM_SECRET_TYPE",
				Value: secretType, // changed
			},
			{
				Name:  "AUTOMDM_SECRET_PROFILE",
				Value: secretProfile, // did not change
			},
		},
	}
	s.DoJSON("PUT", "/api/latest/automdm/spec/secret_variables", req, http.StatusOK, &secretResp)
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchRequest, http.StatusNoContent, "team_id", fmt.Sprint(tm.ID), "dry_run", "true")
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchRequest, http.StatusNoContent, "team_id", fmt.Sprint(tm.ID))
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	// Manually replace the expected secret variables in the profile
	wantTeamProfilesChanged = [][]byte{
		teamProfiles[1],
	}
	wantTeamProfilesChanged[0] = []byte(strings.ReplaceAll(string(wantTeamProfilesChanged[0]), "$AUTOMDM_SECRET_IDENTIFIER",
		secretIdentifier))
	wantTeamProfilesChanged[0] = []byte(strings.ReplaceAll(string(wantTeamProfilesChanged[0]), "${AUTOMDM_SECRET_TYPE}", secretType))
	// verify that we should install the team profiles
	s.signedProfilesMatch(wantTeamProfilesChanged, installs)
	wantTeamProfiles[1] = wantTeamProfilesChanged[0]
	// No profiles should be deleted
	assert.Empty(t, removes)

	var hostResp getHostResponse
	s.DoJSON("GET", fmt.Sprintf("/api/v1/automdm/hosts/%d", host.ID), getHostRequest{}, http.StatusOK, &hostResp)
	require.NotEmpty(t, hostResp.Host.MDM.Profiles)
	resProfiles := *hostResp.Host.MDM.Profiles
	// two extra profiles: automdmd config and root CA
	require.Len(t, resProfiles, len(wantTeamProfiles)+2)

	s.checkMDMProfilesSummaries(t, nil, expectedNoTeamSummary)  // empty because host was transferred
	s.checkMDMProfilesSummaries(t, &tm.ID, expectedTeamSummary) // host still verifying team profiles

	// add a new profile to the team
	mcUUID := "a" + uuid.NewString()
	prof := mcBytesForTest("name-"+mcUUID, "identifier-"+mcUUID, mcUUID)
	wantTeamProfiles = append(wantTeamProfiles, prof)
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		stmt := `INSERT INTO mdm_apple_configuration_profiles (profile_uuid, team_id, name, identifier, mobileconfig, checksum, uploaded_at) VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);`
		_, err := q.ExecContext(context.Background(), stmt, mcUUID, tm.ID, "name-"+mcUUID, "identifier-"+mcUUID, prof, test.MakeTestBytes())
		return err
	})
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Len(t, installs, 1)
	s.signedProfilesMatch([][]byte{prof}, installs)
	require.Empty(t, removes)
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Verifying: 1})

	// can't resend profile while verifying
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, mcUUID), nil, http.StatusConflict)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Couldn’t resend. Configuration profiles with “pending” or “verifying” status can’t be resent.")

	// set the profile to pending, can't resend
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		stmt := `UPDATE host_mdm_apple_profiles SET status = ? WHERE profile_uuid = ? AND host_uuid = ?`
		_, err := q.ExecContext(context.Background(), stmt, automdm.MDMDeliveryPending, mcUUID, host.UUID)
		return err
	})
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Pending: 1})
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, mcUUID), nil, http.StatusConflict)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Couldn’t resend. Configuration profiles with “pending” or “verifying” status can’t be resent.")

	// set the profile to failed, can resend
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		stmt := `UPDATE host_mdm_apple_profiles SET status = ? WHERE profile_uuid = ? AND host_uuid = ?`
		_, err := q.ExecContext(context.Background(), stmt, automdm.MDMDeliveryFailed, mcUUID, host.UUID)
		return err
	})
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Failed: 1})
	_ = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, mcUUID), nil, http.StatusAccepted)
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Len(t, installs, 1)
	s.signedProfilesMatch([][]byte{prof}, installs)
	require.Empty(t, removes)
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Verifying: 1})

	// set the profile to failed, can resend from device endpoint
	token := "good_token"
	updateDeviceTokenForHost(t, s.ds, host.ID, token)
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		stmt := `UPDATE host_mdm_apple_profiles SET status = ? WHERE profile_uuid = ? AND host_uuid = ?`
		_, err := q.ExecContext(context.Background(), stmt, automdm.MDMDeliveryFailed, mcUUID, host.UUID)
		return err
	})
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Failed: 1})
	_ = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/configuration_profiles/%s/resend", token, mcUUID), nil, http.StatusAccepted)
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Len(t, installs, 1)
	s.signedProfilesMatch([][]byte{prof}, installs)
	require.Empty(t, removes)
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Verifying: 1})

	// can't resend profile while verifying
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, mcUUID), nil, http.StatusConflict)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Couldn’t resend. Configuration profiles with “pending” or “verifying” status can’t be resent.")

	// set the profile to verified, can resend
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		stmt := `UPDATE host_mdm_apple_profiles SET status = ? WHERE profile_uuid = ? AND host_uuid = ?`
		_, err := q.ExecContext(context.Background(), stmt, automdm.MDMDeliveryVerified, mcUUID, host.UUID)
		return err
	})
	_ = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, mcUUID), nil, http.StatusAccepted)
	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Len(t, installs, 1)
	s.signedProfilesMatch([][]byte{prof}, installs)
	require.Empty(t, removes)
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Verifying: 1})
	s.lastActivityMatches(
		automdm.ActivityTypeResentConfigurationProfile{}.ActivityName(),
		fmt.Sprintf(`{"host_id": %d, "host_display_name": %q, "profile_name": %q, "profile_uuid": %q, "policy_id": null, "policy_name": null}`, host.ID, host.DisplayName(), "name-"+mcUUID, mcUUID),
		0)

	// add a declaration to the team
	declIdent := "decl-ident-" + uuid.NewString()
	fields := map[string][]string{
		"team_id": {fmt.Sprintf("%d", tm.ID)},
	}
	body, headers := generateNewProfileMultipartRequest(
		t, "some-declaration.json", declarationForTest(declIdent), s.token, fields,
	)
	res = s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusOK, headers)
	var resp newMDMConfigProfileResponse
	err = json.NewDecoder(res.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.ProfileUUID)
	require.Equal(t, "d", string(resp.ProfileUUID[0]))
	declUUID := resp.ProfileUUID

	checkDDMSync := func(d *mdmtest.TestAppleMDMClient) {
		require.NoError(t, ReconcileAppleDeclarationsBatched(ctx, s.ds, s.mdmCommander, s.logger))
		cmd, err := d.Idle()
		require.NoError(t, err)
		require.NotNil(t, cmd)
		require.Equal(t, "DeclarativeManagement", cmd.Command.RequestType)
		cmd, err = d.Acknowledge(cmd.CommandUUID)
		require.NoError(t, err)
		require.Nil(t, cmd, "expected no more commands, but got: %+v", cmd)
		_, err = d.DeclarativeManagement("tokens")
		require.NoError(t, err)
	}
	checkDDMSync(mdmDevice)
	s.checkMDMProfilesSummaries(t, &tm.ID, automdm.MDMProfilesSummary{Verifying: 1})

	// can not resend declarations as admin or from device endpoint
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, declUUID), nil, http.StatusBadRequest)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, automdm.CantResendAppleDeclarationProfilesMessage)
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/configuration_profiles/%s/resend", token, declUUID), nil, http.StatusBadRequest)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, automdm.CantResendAppleDeclarationProfilesMessage)

	// set the declaration to verified
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		stmt := `UPDATE host_mdm_apple_declarations SET status = ? WHERE declaration_uuid = ? AND host_uuid = ?`
		_, err := q.ExecContext(context.Background(), stmt, automdm.MDMDeliveryVerified, declUUID, host.UUID)
		return err
	})

	// transfer the host to the global team
	err = s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(nil, []uint{host.ID}))
	require.NoError(t, err)

	s.awaitTriggerProfileSchedule(t)
	installs, removes = checkNextPayloads(t, mdmDevice, false)
	require.Len(t, installs, len(wantGlobalProfiles))
	s.signedProfilesMatch(wantGlobalProfiles, installs)
	require.Len(t, removes, len(wantTeamProfiles))
	expectedNoTeamSummary = automdm.MDMProfilesSummary{Pending: 1}
	expectedTeamSummary = automdm.MDMProfilesSummary{}
	s.checkMDMProfilesSummaries(t, nil, expectedNoTeamSummary) // host now removing team profiles
	s.checkMDMProfilesSummaries(t, &tm.ID, expectedTeamSummary)

	// can't resend profile from another team
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, mcUUID), nil, http.StatusNotFound)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Unable to match profile to host")

	// invalid profile UUID prefix should return 404
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/resend", host.ID, "z"+uuid.NewString()), nil, http.StatusNotFound)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Invalid profile UUID prefix")

	// set OS updates settings for no-team and team, should not change the
	// summaries as this profile is ignored.
	s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"mdm": {
			"macos_updates": {
				"deadline": "2023-12-31",
				"minimum_version": "14.6.1"
			}
		}
	}`), http.StatusOK)
	s.Do("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", tm.ID), automdm.TeamPayload{
		MDM: &automdm.TeamPayloadMDM{
			MacOSUpdates: &automdm.AppleOSUpdateSettings{
				Deadline:       optjson.SetString("1992-01-01"),
				MinimumVersion: optjson.SetString("14.6.1"),
			},
		},
	}, http.StatusOK)
	s.checkMDMProfilesSummaries(t, nil, expectedNoTeamSummary)
	s.checkMDMProfilesSummaries(t, &tm.ID, expectedTeamSummary)

	// it should also not show up in the host's profiles list
	s.DoJSON("GET", fmt.Sprintf("/api/v1/automdm/hosts/%d", host.ID), getHostRequest{}, http.StatusOK, &hostResp)
	require.NotEmpty(t, hostResp.Host.MDM.Profiles)
	resProfiles = *hostResp.Host.MDM.Profiles
	// two extra profiles: automdmd config and root CA
	require.Len(t, resProfiles, len(wantGlobalProfiles)+2)
}

func (s *integrationMDMTestSuite) TestAppleProfileRetries() {
	t := s.T()
	ctx := context.Background()

	enrollSecret := "test-profile-retries-secret"
	err := s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: enrollSecret}})
	require.NoError(t, err)

	testProfiles := [][]byte{
		mobileconfigForTest("N1", "I1"),
		mobileconfigForTest("N2", "I2"),
	}
	initialExpectedProfiles := testProfiles
	initialExpectedProfiles = append(
		initialExpectedProfiles,
		setupExpectedAutoMDMdProfile(t, s.server.URL, enrollSecret, nil),
		setupExpectedCAProfile(t, s.ds),
	)

	h, mdmDevice := createHostThenEnrollMDM(s.ds, s.server.URL, t)
	setupPusher(s, t, mdmDevice)

	// we remove the reds key and don't run the apple worker to keep the nature of the test
	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+h.UUID)
	require.NoError(t, err)

	expectedProfileStatuses := map[string]automdm.MDMDeliveryStatus{
		"I1": automdm.MDMDeliveryVerifying,
		"I2": automdm.MDMDeliveryVerifying,
		mobileconfig.AutoMDMdConfigPayloadIdentifier:      automdm.MDMDeliveryVerifying,
		mobileconfig.AutoMDMCARootConfigPayloadIdentifier: automdm.MDMDeliveryVerifying,
	}
	checkProfilesStatus := func(t *testing.T) {
		storedProfs, err := s.ds.GetHostMDMAppleProfiles(ctx, h.UUID)
		require.NoError(t, err)
		require.Len(t, storedProfs, len(expectedProfileStatuses))
		for _, p := range storedProfs {
			want, ok := expectedProfileStatuses[p.Identifier]
			require.True(t, ok, "unexpected profile: %s", p.Identifier)
			require.Equal(t, want, *p.Status, "expected status %s but got %s for profile: %s", want, *p.Status, p.Identifier)
		}
	}

	expectedRetryCounts := map[string]uint{
		"I1": 0,
		"I2": 0,
		mobileconfig.AutoMDMdConfigPayloadIdentifier:      0,
		mobileconfig.AutoMDMCARootConfigPayloadIdentifier: 0,
	}
	checkRetryCounts := func(t *testing.T) {
		counts, err := s.ds.GetHostMDMProfilesRetryCounts(ctx, h)
		require.NoError(t, err)
		require.Len(t, counts, len(expectedRetryCounts))
		for _, c := range counts {
			want, ok := expectedRetryCounts[c.ProfileIdentifier]
			require.True(t, ok, "unexpected profile: %s", c.ProfileIdentifier)
			require.Equal(t, want, c.Retries, "expected retry count %d but got %d for profile: %s", want, c.Retries, c.ProfileIdentifier)
		}
	}

	hostProfsByIdent := map[string]*automdm.HostMacOSProfile{
		"I1": {
			Identifier:  "I1",
			DisplayName: "N1",
			InstallDate: time.Now().Add(15 * time.Minute),
		},
		"I2": {
			Identifier:  "I2",
			DisplayName: "N2",
			InstallDate: time.Now().Add(15 * time.Minute),
		},
		mobileconfig.AutoMDMdConfigPayloadIdentifier: {
			Identifier:  mobileconfig.AutoMDMdConfigPayloadIdentifier,
			DisplayName: "AutoMDMd configuration",
			InstallDate: time.Now().Add(15 * time.Minute),
		},
	}
	reportHostProfs := func(t *testing.T, identifiers ...string) {
		report := make(map[string]*automdm.HostMacOSProfile, len(hostProfsByIdent))
		for _, ident := range identifiers {
			report[ident] = hostProfsByIdent[ident]
		}
		require.NoError(t, apple_mdm.VerifyHostMDMProfiles(ctx, s.ds, h, report))
	}

	setProfileUploadedAt := func(t *testing.T, uploadedAt time.Time, identifiers ...any) {
		bindVars := strings.TrimSuffix(strings.Repeat("?, ", len(identifiers)), ", ")
		stmt := fmt.Sprintf("UPDATE mdm_apple_configuration_profiles SET uploaded_at = ? WHERE identifier IN(%s)", bindVars)
		args := append([]any{uploadedAt}, identifiers...)
		mysqltest.ExecAdhocSQL(t, s.ds, func(tx sqlx.ExtContext) error {
			_, err := tx.ExecContext(ctx, stmt, args...)
			return err
		})
	}

	t.Run("retry after verifying", func(t *testing.T) {
		// upload test profiles then simulate expired grace period by setting updated_at timestamp of profiles back by 48 hours
		s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(testProfiles), http.StatusNoContent)
		setProfileUploadedAt(t, time.Now().Add(-48*time.Hour), "I1", "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier)

		// trigger initial profile sync and confirm that we received all profiles
		s.awaitTriggerProfileSchedule(t)
		installs, removes := checkNextPayloads(t, mdmDevice, false)
		s.signedProfilesMatch(initialExpectedProfiles, installs)
		require.Empty(t, removes)

		checkProfilesStatus(t) // all profiles verifying
		checkRetryCounts(t)    // no retries yet

		// report osquery results with I2 missing and confirm I2 marked as pending and other profiles are marked as verified
		reportHostProfs(t, "I1", mobileconfig.AutoMDMdConfigPayloadIdentifier)
		expectedProfileStatuses["I2"] = automdm.MDMDeliveryPending
		expectedProfileStatuses["I1"] = automdm.MDMDeliveryVerified
		expectedProfileStatuses[mobileconfig.AutoMDMdConfigPayloadIdentifier] = automdm.MDMDeliveryVerified
		checkProfilesStatus(t)
		expectedRetryCounts["I2"] = 1
		checkRetryCounts(t)

		// trigger a profile sync and confirm that the install profile command for I2 was resent
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		s.signedProfilesMatch([][]byte{initialExpectedProfiles[1]}, installs)
		require.Empty(t, removes)

		// report osquery results with I2 present and confirm that all profiles are verified
		reportHostProfs(t, "I1", "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier)
		expectedProfileStatuses["I2"] = automdm.MDMDeliveryVerified
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged

		// trigger a profile sync and confirm that no profiles were sent
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		require.Empty(t, installs)
		require.Empty(t, removes)
	})

	t.Run("retry after verification", func(t *testing.T) {
		// I1 already has retries=1 from the previous subtest, continue retrying until max retries exceeded
		startRetries := expectedRetryCounts["I1"]
		for retryNum := startRetries + 1; retryNum <= servermdm.MaxAppleProfileRetries; retryNum++ {
			reportHostProfs(t, "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier)
			expectedRetryCounts["I1"] = retryNum
			// not yet at max retries, profile should be pending for retry
			expectedProfileStatuses["I1"] = automdm.MDMDeliveryPending
			checkProfilesStatus(t)
			checkRetryCounts(t)

			// trigger a profile sync and confirm that the install profile command for I1 was resent
			s.awaitTriggerProfileSchedule(t)
			installs, removes := checkNextPayloads(t, mdmDevice, false)
			s.signedProfilesMatch([][]byte{initialExpectedProfiles[0]}, installs)
			require.Empty(t, removes)
		}

		// report osquery results with I1 missing again, now max retries exceeded
		reportHostProfs(t, "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier)
		expectedProfileStatuses["I1"] = automdm.MDMDeliveryFailed
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged, still at max

		// trigger a profile sync and confirm that the install profile command for I1 was not resent
		s.awaitTriggerProfileSchedule(t)
		installs, removes := checkNextPayloads(t, mdmDevice, false)
		require.Empty(t, installs)
		require.Empty(t, removes)
	})

	t.Run("retry after device error", func(t *testing.T) {
		// add another profile and set the updated_at timestamp back by 48 hours
		newProfile := mobileconfigForTest("N3", "I3")
		testProfiles = append(testProfiles, newProfile)
		s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(testProfiles), http.StatusNoContent)
		setProfileUploadedAt(t, time.Now().Add(-48*time.Hour), "I1", "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier, "I3")

		// trigger a profile sync and confirm that the install profile command for I3 was sent and
		// simulate a device error
		s.awaitTriggerProfileSchedule(t)
		installs, removes := checkNextPayloads(t, mdmDevice, true)
		s.signedProfilesMatch([][]byte{newProfile}, installs)
		require.Empty(t, removes)
		expectedProfileStatuses["I3"] = automdm.MDMDeliveryPending
		checkProfilesStatus(t)
		expectedRetryCounts["I3"] = 1
		checkRetryCounts(t)

		// continue retrying via device errors until max retries exceeded
		for retryNum := uint(2); retryNum <= servermdm.MaxAppleProfileRetries; retryNum++ {
			s.awaitTriggerProfileSchedule(t)
			installs, removes = checkNextPayloads(t, mdmDevice, true) // simulate device error
			s.signedProfilesMatch([][]byte{newProfile}, installs)
			require.Empty(t, removes)
			expectedProfileStatuses["I3"] = automdm.MDMDeliveryPending
			expectedRetryCounts["I3"] = retryNum
			checkProfilesStatus(t)
			checkRetryCounts(t)
		}

		// trigger a profile sync and simulate a device ack (retries already at max)
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		s.signedProfilesMatch([][]byte{newProfile}, installs)
		require.Empty(t, removes)
		expectedProfileStatuses["I3"] = automdm.MDMDeliveryVerifying
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged

		// report osquery results with I3 missing and confirm that I3 is marked as failed (max retries exceeded)
		reportHostProfs(t, "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier)
		expectedProfileStatuses["I3"] = automdm.MDMDeliveryFailed
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged

		// trigger a profile sync and confirm that the install profile command for I3 was not resent
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		require.Empty(t, installs)
		require.Empty(t, removes)
	})

	t.Run("repeated device error", func(t *testing.T) {
		// add another profile and set the updated_at timestamp back by 48 hours
		newProfile := mobileconfigForTest("N4", "I4")
		testProfiles = append(testProfiles, newProfile)
		s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(testProfiles), http.StatusNoContent)
		setProfileUploadedAt(t, time.Now().Add(-48*time.Hour), "I1", "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier, "I3", "I4")

		// repeatedly simulate device errors until max retries exceeded
		for retryNum := uint(1); retryNum <= servermdm.MaxAppleProfileRetries; retryNum++ {
			s.awaitTriggerProfileSchedule(t)
			installs, removes := checkNextPayloads(t, mdmDevice, true)
			s.signedProfilesMatch([][]byte{newProfile}, installs)
			require.Empty(t, removes)
			expectedProfileStatuses["I4"] = automdm.MDMDeliveryPending
			expectedRetryCounts["I4"] = retryNum
			checkProfilesStatus(t)
			checkRetryCounts(t)
		}

		// one more device error should mark as failed
		s.awaitTriggerProfileSchedule(t)
		installs, removes := checkNextPayloads(t, mdmDevice, true)
		s.signedProfilesMatch([][]byte{newProfile}, installs)
		require.Empty(t, removes)
		expectedProfileStatuses["I4"] = automdm.MDMDeliveryFailed
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged

		// trigger a profile sync and confirm that the install profile command for I4 was not resent
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		require.Empty(t, installs)
		require.Empty(t, removes)
	})

	t.Run("retry count does not reset", func(t *testing.T) {
		// add another profile and set the updated_at timestamp back by 48 hours
		newProfile := mobileconfigForTest("N5", "I5")
		testProfiles = append(testProfiles, newProfile)
		hostProfsByIdent["I5"] = &automdm.HostMacOSProfile{Identifier: "I5", DisplayName: "N5", InstallDate: time.Now()}
		s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(testProfiles), http.StatusNoContent)
		setProfileUploadedAt(t, time.Now().Add(-48*time.Hour), "I1", "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier, "I3", "I4", "I5")

		// trigger a profile sync and confirm that the install profile command for I3 was sent and
		// simulate a device error
		s.awaitTriggerProfileSchedule(t)
		installs, removes := checkNextPayloads(t, mdmDevice, true)
		s.signedProfilesMatch([][]byte{newProfile}, installs)
		require.Empty(t, removes)
		expectedProfileStatuses["I5"] = automdm.MDMDeliveryPending
		checkProfilesStatus(t)
		expectedRetryCounts["I5"] = 1
		checkRetryCounts(t)

		// trigger a profile sync and confirm that the install profile command for I5 was sent and
		// simulate a device ack
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		s.signedProfilesMatch([][]byte{newProfile}, installs)
		require.Empty(t, removes)
		expectedProfileStatuses["I5"] = automdm.MDMDeliveryVerifying
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged

		// report osquery results with I5 found and confirm that the I5 marked as verified
		reportHostProfs(t, "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier, "I5")
		expectedProfileStatuses["I5"] = automdm.MDMDeliveryVerified
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged

		// trigger a profile sync and confirm that the install profile command for I5 was not resent
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		require.Empty(t, installs)
		require.Empty(t, removes)

		// report osquery results again with I5 missing, retry until max retries exceeded.
		// Each iteration: report missing (increments retries, sets status=NULL) -> cron re-enqueues -> ack (verifying).
		// When retries reaches MaxAppleProfileRetries, the next missing report marks it as failed.
		for expectedRetryCounts["I5"] < servermdm.MaxAppleProfileRetries {
			reportHostProfs(t, "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier)
			expectedRetryCounts["I5"]++
			expectedProfileStatuses["I5"] = automdm.MDMDeliveryPending
			checkProfilesStatus(t)
			checkRetryCounts(t)

			s.awaitTriggerProfileSchedule(t)
			installs, removes = checkNextPayloads(t, mdmDevice, false)
			s.signedProfilesMatch([][]byte{newProfile}, installs)
			require.Empty(t, removes)
		}

		// one final missing report: retries == MaxAppleProfileRetries, should be failed
		reportHostProfs(t, "I2", mobileconfig.AutoMDMdConfigPayloadIdentifier)
		expectedProfileStatuses["I5"] = automdm.MDMDeliveryFailed
		checkProfilesStatus(t)
		checkRetryCounts(t) // unchanged

		// trigger a profile sync and confirm that the install profile command for I5 was not resent
		s.awaitTriggerProfileSchedule(t)
		installs, removes = checkNextPayloads(t, mdmDevice, false)
		require.Empty(t, installs)
		require.Empty(t, removes)
	})
}

func (s *integrationMDMTestSuite) TestMDMAppleListConfigProfiles() {
	t := s.T()
	ctx := context.Background()

	testTeam, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "TestTeam"})
	require.NoError(t, err)

	// ensure automdm profile
	s.awaitTriggerProfileSchedule(t)

	mdmHost, _ := createHostThenEnrollMDM(s.ds, s.server.URL, t)
	s.awaitRunAppleMDMWorkerSchedule()

	t.Run("no profiles", func(t *testing.T) {
		var listResp listMDMConfigProfilesResponse
		s.DoJSON("GET", "/api/v1/automdm/configuration_profiles", nil, http.StatusOK, &listResp)
		require.NotNil(t, listResp.Profiles) // expect empty slice instead of nil
		require.Empty(t, listResp.Profiles)

		listResp = listMDMConfigProfilesResponse{}
		s.DoJSON("GET", fmt.Sprintf(`/api/v1/automdm/configuration_profiles?team_id=%d`, testTeam.ID), nil, http.StatusOK, &listResp)
		require.NotNil(t, listResp.Profiles) // expect empty slice instead of nil
		require.Empty(t, listResp.Profiles)

		var hostProfilesResp getHostProfilesResponse
		s.DoJSON("GET", fmt.Sprintf("/api/v1/automdm/hosts/%d/configuration_profiles", mdmHost.ID), nil, http.StatusOK, &hostProfilesResp)
		require.NotNil(t, hostProfilesResp.Profiles) // expect empty slice instead of nil
		require.Empty(t, hostProfilesResp.Profiles)
		require.Equal(t, mdmHost.ID, hostProfilesResp.HostID)
	})

	t.Run("with profiles", func(t *testing.T) {
		p1, err := automdm.NewMDMAppleConfigProfile(mcBytesForTest("p1", "p1.identifier", "p1.uuid"), nil)
		require.NoError(t, err)
		_, err = s.ds.NewMDMAppleConfigProfile(ctx, *p1, nil)
		require.NoError(t, err)

		p2, err := automdm.NewMDMAppleConfigProfile(mcBytesForTest("p2", "p2.identifier", "p2.uuid"), &testTeam.ID)
		require.NoError(t, err)
		_, err = s.ds.NewMDMAppleConfigProfile(ctx, *p2, nil)
		require.NoError(t, err)

		var resp listMDMConfigProfilesResponse
		s.DoJSON("GET", "/api/latest/automdm/configuration_profiles", nil, http.StatusOK, &resp)
		require.NotNil(t, resp.Profiles)
		require.Len(t, resp.Profiles, 1)
		require.Equal(t, p1.Name, resp.Profiles[0].Name)
		require.Equal(t, p1.Identifier, resp.Profiles[0].Identifier)

		resp = listMDMConfigProfilesResponse{}
		s.DoJSON("GET", fmt.Sprintf(`/api/v1/automdm/configuration_profiles?team_id=%d`, testTeam.ID), nil, http.StatusOK, &resp)
		require.NotNil(t, resp.Profiles)
		require.Len(t, resp.Profiles, 1)
		require.Equal(t, p2.Name, resp.Profiles[0].Name)
		require.Equal(t, p2.Identifier, resp.Profiles[0].Identifier)

		p3, err := automdm.NewMDMAppleConfigProfile(mcBytesForTest("p3", "p3.identifier", "p3.uuid"), &testTeam.ID)
		require.NoError(t, err)
		_, err = s.ds.NewMDMAppleConfigProfile(ctx, *p3, nil)
		require.NoError(t, err)

		resp = listMDMConfigProfilesResponse{}
		s.DoJSON("GET", fmt.Sprintf(`/api/v1/automdm/configuration_profiles?team_id=%d`, testTeam.ID), nil, http.StatusOK, &resp)
		require.NotNil(t, resp.Profiles)
		require.Len(t, resp.Profiles, 2)
		for _, p := range resp.Profiles {
			if p.Name == p2.Name { //nolint:gocritic // ignore ifElseChain
				require.Equal(t, p2.Identifier, p.Identifier)
			} else if p.Name == p3.Name {
				require.Equal(t, p3.Identifier, p.Identifier)
			} else {
				require.Fail(t, "unexpected profile name")
			}
		}

		var hostProfilesResp getHostProfilesResponse
		s.DoJSON("GET", fmt.Sprintf("/api/v1/automdm/hosts/%d/configuration_profiles", mdmHost.ID), nil, http.StatusOK, &hostProfilesResp)
		require.NotNil(t, hostProfilesResp.Profiles)
		require.Len(t, hostProfilesResp.Profiles, 1)
		require.Equal(t, p1.Name, hostProfilesResp.Profiles[0].Name)
		require.Equal(t, p1.Identifier, hostProfilesResp.Profiles[0].Identifier)
		require.Equal(t, mdmHost.ID, hostProfilesResp.HostID)

		// add the host to a team
		err = s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&testTeam.ID, []uint{mdmHost.ID}))
		require.NoError(t, err)

		hostProfilesResp = getHostProfilesResponse{}
		s.DoJSON("GET", fmt.Sprintf("/api/v1/automdm/hosts/%d/configuration_profiles", mdmHost.ID), nil, http.StatusOK, &hostProfilesResp)
		require.NotNil(t, hostProfilesResp.Profiles)
		require.Len(t, hostProfilesResp.Profiles, 2)
		require.Equal(t, mdmHost.ID, hostProfilesResp.HostID)
	})
}

func (s *integrationMDMTestSuite) TestGetHostConfigurationProfilesSingleMDM() {
	t := s.T()
	ctx := context.Background()

	// save original config for cleanup
	appCfg, err := s.ds.AppConfig(ctx)
	require.NoError(t, err)
	originalCopy := appCfg.Copy()
	t.Cleanup(func() {
		require.NoError(t, s.ds.SaveAppConfig(ctx, originalCopy))
	})

	// create hosts for each platform
	appleHost := createOrbitEnrolledHost(t, "darwin", "apple-cfg-profiles", s.ds)
	windowsHost := createOrbitEnrolledHost(t, "windows", "windows-cfg-profiles", s.ds)

	checkEndpoint := func(t *testing.T, hostID uint, expectedStatus int) {
		var resp getHostProfilesResponse
		s.DoJSON("GET", fmt.Sprintf("/api/v1/automdm/hosts/%d/configuration_profiles", hostID), nil, expectedStatus, &resp)
		if expectedStatus == http.StatusOK {
			require.NotNil(t, resp.Profiles)
			require.Equal(t, hostID, resp.HostID)
		}
	}

	setMDMConfig := func(apple, windows, android bool) {
		appCfg, err := s.ds.AppConfig(ctx)
		require.NoError(t, err)
		appCfg.MDM.EnabledAndConfigured = apple
		appCfg.MDM.WindowsEnabledAndConfigured = windows
		appCfg.MDM.AndroidEnabledAndConfigured = android
		require.NoError(t, s.ds.SaveAppConfig(ctx, appCfg))
	}

	t.Run("apple only", func(t *testing.T) {
		setMDMConfig(true, false, false)
		checkEndpoint(t, appleHost.ID, http.StatusOK)
	})

	t.Run("windows only", func(t *testing.T) {
		setMDMConfig(false, true, false)
		checkEndpoint(t, windowsHost.ID, http.StatusOK)
	})

	t.Run("android only", func(t *testing.T) {
		setMDMConfig(false, false, true)
		// use apple host ID; the endpoint should still be reachable
		checkEndpoint(t, appleHost.ID, http.StatusOK)
	})

	t.Run("none configured", func(t *testing.T) {
		setMDMConfig(false, false, false)
		checkEndpoint(t, appleHost.ID, http.StatusBadRequest)
	})
}

func (s *integrationMDMTestSuite) TestApplyTeamsMDMAppleSettingsUnknownKey() {
	t := s.T()

	// create a team through the service so it initializes the agent ops
	teamName := t.Name() + "team1"
	var createTeamResp teamResponse
	s.DoJSON("POST", "/api/latest/automdm/teams", &automdm.Team{Name: teamName, Description: "desc team1"}, http.StatusOK, &createTeamResp)
	require.NotZero(t, createTeamResp.Team.ID)

	teamSpecs := applyTeamSpecsRequest{Specs: []*automdm.TeamSpec{{
		Name: teamName,
		MDM: automdm.TeamSpecMDM{
			MacOSSettings: map[string]any{"foo_bar": 123},
		},
	}}}
	res := s.Do("POST", "/api/latest/automdm/spec/teams", teamSpecs, http.StatusBadRequest)
	assert.Contains(t, extractServerErrorText(res.Body), `unsupported key provided: "foo_bar"`)
}

func (s *integrationMDMTestSuite) TestHostMDMAppleProfilesStatus() {
	t := s.T()
	ctx := context.Background()

	createManualMDMEnrollWithOrbit := func(secret string, doUserEnroll bool) (*automdm.Host, *automdm.NanoEnrollment, *mdmtest.TestAppleMDMClient) {
		// orbit enrollment happens before mdm enrollment, otherwise the host would
		// always receive the "no team" profiles on mdm enrollment since it would
		// not be part of any team yet (team assignment is done when it enrolls
		// with orbit).
		mdmDevice := mdmtest.NewTestMDMClientAppleDirect(mdmtest.AppleEnrollInfo{
			SCEPChallenge: s.scepChallenge,
			SCEPURL:       s.server.URL + apple_mdm.SCEPPath,
			MDMURL:        s.server.URL + apple_mdm.MDMPath,
		}, "MacBookPro16,1")

		// enroll the device with orbit
		var resp enrollOrbitResponse
		s.DoJSON("POST", "/api/automdm/orbit/enroll", automdm.EnrollOrbitRequest{
			EnrollSecret:   secret,
			HardwareUUID:   mdmDevice.UUID, // will not match any existing host
			HardwareSerial: mdmDevice.SerialNumber,
		}, http.StatusOK, &resp)
		require.NotEmpty(t, resp.OrbitNodeKey)
		orbitNodeKey := resp.OrbitNodeKey
		h, err := s.ds.LoadHostByOrbitNodeKey(ctx, orbitNodeKey)
		require.NoError(t, err)
		h.OrbitNodeKey = &orbitNodeKey
		h.Platform = "darwin"

		err = mdmDevice.Enroll()
		require.NoError(t, err)

		var userEnrollment *automdm.NanoEnrollment
		if doUserEnroll {
			// Do a user enrollment with a bit of extra sanity checking
			userEnrollment, err = s.ds.GetNanoMDMUserEnrollment(ctx, h.UUID)
			require.NoError(t, err)
			require.Nil(t, userEnrollment)

			err = mdmDevice.UserEnroll()
			require.NoError(t, err)

			userEnrollment, err = s.ds.GetNanoMDMUserEnrollment(ctx, h.UUID)
			require.NoError(t, err)
			require.NotNil(t, userEnrollment)

		}

		return h, userEnrollment, mdmDevice
	}

	triggerReconcileProfilesMarkVerifying := func() {
		t.Logf("[TestHostMDMAppleProfilesStatus] Calling awaitTriggerProfileSchedule at %s", time.Now().Format(time.RFC3339))
		s.awaitTriggerProfileSchedule(t)
		t.Logf("[TestHostMDMAppleProfilesStatus] awaitTriggerProfileSchedule completed, updating profiles to verifying at %s", time.Now().Format(time.RFC3339))
		// this will only mark them as "pending", as the response to confirm
		// profile deployment is asynchronous, so we simulate it here by
		// updating any "pending" (not NULL) profiles to "verifying"
		mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
			_, err := q.ExecContext(ctx, `UPDATE host_mdm_apple_profiles SET status = ? WHERE status = ?`, automdm.OSSettingsVerifying, automdm.OSSettingsPending)
			return err
		})
		t.Logf("[TestHostMDMAppleProfilesStatus] Profiles updated to verifying status at %s", time.Now().Format(time.RFC3339))
	}

	assignHostToTeam := func(h *automdm.Host, teamID *uint) {
		var moveHostResp addHostsToTeamResponse
		s.DoJSON("POST", "/api/v1/automdm/hosts/transfer",
			addHostsToTeamRequest{TeamID: teamID, HostIDs: []uint{h.ID}}, http.StatusOK, &moveHostResp)

		h.TeamID = teamID
	}

	// add a couple global profiles
	payloadScopeSystem := automdm.PayloadScopeSystem
	payloadScopeUser := automdm.PayloadScopeUser
	globalProfiles := [][]byte{
		mobileconfigForTest("G1", "G1"),
		scopedMobileconfigForTest("G2", "G2", &payloadScopeSystem),
		scopedMobileconfigForTest("G3", "G3.user", &payloadScopeUser),
	}
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)
	// create the no-team enroll secret
	var applyResp applyEnrollSecretSpecResponse
	globalEnrollSec := "global_enroll_sec"
	s.DoJSON("POST", "/api/latest/automdm/spec/enroll_secret",
		applyEnrollSecretSpecRequest{
			Spec: &automdm.EnrollSecretSpec{
				Secrets: []*automdm.EnrollSecret{{Secret: globalEnrollSec}},
			},
		}, http.StatusOK, &applyResp)

	// create a team with a couple profiles
	tm1, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team_profiles_status_1"})
	require.NoError(t, err)
	tm1Profiles := [][]byte{
		mobileconfigForTest("T1.1", "T1.1"),
		scopedMobileconfigForTest("T1.2", "T1.2", &payloadScopeSystem),
		scopedMobileconfigForTest("T1.3", "T1.3.user", &payloadScopeUser),
	}
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(tm1Profiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm1.ID))
	// create the team 1 enroll secret
	var teamResp teamEnrollSecretsResponse
	tm1EnrollSec := "team1_enroll_sec"
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d/secrets", tm1.ID),
		modifyTeamEnrollSecretsRequest{
			Secrets: []automdm.EnrollSecret{{Secret: tm1EnrollSec}},
		}, http.StatusOK, &teamResp)

	// create another team with different profiles
	tm2, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team_profiles_status_2"})
	require.NoError(t, err)
	tm2Profiles := [][]byte{
		mobileconfigForTest("T2.1", "T2.1"),
	}
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(tm2Profiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm2.ID))

	// enroll a couple hosts in no team
	h1, h1UserEnrollment, _ := createManualMDMEnrollWithOrbit(globalEnrollSec, true)
	require.Nil(t, h1.TeamID)
	h2, _, _ := createManualMDMEnrollWithOrbit(globalEnrollSec, false)
	require.Nil(t, h2.TeamID)
	// run the cron
	t.Logf("[TestHostMDMAppleProfilesStatus] Starting FIRST cron run (after h1, h2 enrolled) at %s", time.Now().Format(time.RFC3339))
	s.awaitTriggerProfileSchedule(t)
	t.Logf("[TestHostMDMAppleProfilesStatus] FIRST cron run completed at %s", time.Now().Format(time.RFC3339))
	s.awaitRunAppleMDMWorkerSchedule()

	// G3 is user-scoped and the h2 host doesn't have a user-channel yet (and
	// enrolled just now, so the minimum delay to give up and fail the profile
	// delivery is not reached)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
		},
		h2: {
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
		},
	})

	// Verify there is a command on the user channel for h1
	enrollmentIds, err := s.ds.GetEnrollmentIDsWithPendingMDMAppleCommands(ctx)
	require.NoError(t, err)
	require.NotNil(t, h1UserEnrollment)
	assert.Contains(t, enrollmentIds, h1UserEnrollment.ID)
	assert.Contains(t, enrollmentIds, h1.UUID)
	assert.Contains(t, enrollmentIds, h2.UUID)

	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+h1.UUID)
	require.NoError(t, err)
	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+h2.UUID)
	require.NoError(t, err)

	// enroll a couple hosts in team 1
	h3, h3UserEnrollment, _ := createManualMDMEnrollWithOrbit(tm1EnrollSec, true)
	require.NotNil(t, h3.TeamID)
	require.Equal(t, tm1.ID, *h3.TeamID)
	h4, _, h4Device := createManualMDMEnrollWithOrbit(tm1EnrollSec, false)
	require.NotNil(t, h4.TeamID)
	require.Equal(t, tm1.ID, *h4.TeamID)

	// run the cron
	t.Logf("[TestHostMDMAppleProfilesStatus] Starting SECOND cron run (after h3, h4 enrolled in team1) at %s", time.Now().Format(time.RFC3339))
	s.awaitTriggerProfileSchedule(t)
	t.Logf("[TestHostMDMAppleProfilesStatus] SECOND cron run completed at %s", time.Now().Format(time.RFC3339))
	s.awaitRunAppleMDMWorkerSchedule()

	// T1.3 is user-scoped and the h4 host doesn't have a user-channel yet (and
	// enrolled just now, so the minimum delay to give up and send the
	// user-scoped profiles to the device channel is not reached)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h3: {
			{Identifier: "T1.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
		},
		h4: {
			{Identifier: "T1.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
		},
	})
	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+h3.UUID)
	require.NoError(t, err)
	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+h4.UUID)
	require.NoError(t, err)

	// apply the pending profiles
	triggerReconcileProfilesMarkVerifying()

	// Verify there is a command on the user channel for h3
	enrollmentIds, err = s.ds.GetEnrollmentIDsWithPendingMDMAppleCommands(ctx)
	require.NoError(t, err)
	require.NotNil(t, h3UserEnrollment)
	assert.Contains(t, enrollmentIds, h3UserEnrollment.ID)
	assert.Contains(t, enrollmentIds, h3.UUID)
	assert.Contains(t, enrollmentIds, h4.UUID)

	// switch a no team host (h1) to a team (tm2)
	t.Logf("[TestHostMDMAppleProfilesStatus] Transferring host h1 (id=%d) from no team to team tm2 (id=%d) at %s", h1.ID, tm2.ID, time.Now().Format(time.RFC3339))
	var moveHostResp addHostsToTeamResponse
	s.DoJSON("POST", "/api/v1/automdm/hosts/transfer",
		addHostsToTeamRequest{TeamID: &tm2.ID, HostIDs: []uint{h1.ID}}, http.StatusOK, &moveHostResp)
	t.Logf("[TestHostMDMAppleProfilesStatus] Host h1 transfer completed at %s", time.Now().Format(time.RFC3339))
	s.awaitTriggerProfileSchedule(t)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h2: {
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// switch a team host (h3) to another team (tm2)
	s.DoJSON("POST", "/api/v1/automdm/hosts/transfer",
		addHostsToTeamRequest{TeamID: &tm2.ID, HostIDs: []uint{h3.ID}}, http.StatusOK, &moveHostResp)

	// create the user-enrollment for host h4
	err = h4Device.UserEnroll()
	require.NoError(t, err)

	// run the cron
	t.Logf("[TestHostMDMAppleProfilesStatus] Starting THIRD cron run (after h3->tm2, h4 user enrolled) at %s", time.Now().Format(time.RFC3339))
	s.awaitTriggerProfileSchedule(t)
	t.Logf("[TestHostMDMAppleProfilesStatus] THIRD cron run completed at %s", time.Now().Format(time.RFC3339))
	s.awaitRunAppleMDMWorkerSchedule()
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h3: {
			{Identifier: "T1.1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "T1.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T1.2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// switch a team host (h4) to no team
	t.Logf("[TestHostMDMAppleProfilesStatus] Transferring host h4 (id=%d) from team tm1 to NO TEAM (nil) at %s", h4.ID, time.Now().Format(time.RFC3339))
	s.DoJSON("POST", "/api/v1/automdm/hosts/transfer",
		addHostsToTeamRequest{TeamID: nil, HostIDs: []uint{h4.ID}}, http.StatusOK, &moveHostResp)
	t.Logf("[TestHostMDMAppleProfilesStatus] Host h4 transfer to NO TEAM completed at %s", time.Now().Format(time.RFC3339))
	s.awaitTriggerProfileSchedule(t)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h3: {
			{Identifier: "T1.1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "T1.1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// apply the pending profiles
	triggerReconcileProfilesMarkVerifying()

	// add a profile to no team (h2 and h4 are now part of no team)
	body, headers := generateNewProfileMultipartRequest(t,
		"some_name.mobileconfig", mobileconfigForTest("G4", "G4"), s.token, nil)
	s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusOK, headers)
	s.awaitTriggerProfileSchedule(t)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h2: { // still no user channel
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G4", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
		},
		h4: {
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G4", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
		},
	})

	// add a profile to team 2 (h1 and h3 are now part of team 2)
	body, headers = generateNewProfileMultipartRequest(t,
		"some_name.mobileconfig", mobileconfigForTest("T2.2", "T2.2"), s.token, map[string][]string{"team_id": {fmt.Sprintf("%d", tm2.ID)}})
	s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusOK, headers)
	s.awaitTriggerProfileSchedule(t)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "T2.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T2.2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "T2.1", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T2.2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// apply the pending profiles
	triggerReconcileProfilesMarkVerifying()

	// delete a no team profile
	noTeamProfs, err := s.ds.ListMDMAppleConfigProfiles(ctx, nil)
	require.NoError(t, err)
	var g1ProfUUID string
	for _, p := range noTeamProfs {
		if p.Identifier == "G1" {
			g1ProfUUID = p.ProfileUUID
			break
		}
	}
	require.NotEmpty(t, g1ProfUUID)
	var delProfResp deleteMDMConfigProfileResponse
	s.DoJSON("DELETE", "/api/latest/automdm/configuration_profiles/"+g1ProfUUID, nil, http.StatusOK, &delProfResp)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h2: {
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G4", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "G1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G4", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// delete a team profile
	tm2Profs, err := s.ds.ListMDMAppleConfigProfiles(ctx, &tm2.ID)
	require.NoError(t, err)
	var tm21ProfUUID string
	for _, p := range tm2Profs {
		if p.Identifier == "T2.1" {
			tm21ProfUUID = p.ProfileUUID
			break
		}
	}
	require.NotEmpty(t, tm21ProfUUID)
	s.DoJSON("DELETE", "/api/latest/automdm/configuration_profiles/"+tm21ProfUUID, nil, http.StatusOK, &delProfResp)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "T2.1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "T2.1", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.2", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// apply the pending profiles
	triggerReconcileProfilesMarkVerifying()

	// bulk-set profiles for no team, with add/delete/edit
	g2Edited := mobileconfigForTest("G2b", "G2b")
	g5Content := mobileconfigForTest("G5", "G5")
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest([][]byte{
			g2Edited,
			// G3 is deleted
			// G4 is deleted
			g5Content,
		}), http.StatusNoContent)
	s.awaitTriggerProfileSchedule(t)

	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h2: {
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G4", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "G2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G3.user", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G4", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// bulk-set profiles for a team, with add/delete/edit
	t22Edited := mobileconfigForTest("T2.2b", "T2.2b")
	t23Content := mobileconfigForTest("T2.3", "T2.3")
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest([][]byte{
			t22Edited,
			t23Content,
		}), http.StatusNoContent, "team_id", fmt.Sprint(tm2.ID))
	s.awaitTriggerProfileSchedule(t)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "T2.2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.3", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "T2.2", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.3", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// apply the pending profiles
	triggerReconcileProfilesMarkVerifying()

	// bulk-set profiles for no team and team 2, without changes, and team 1 added (but no host affected)
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest([][]byte{
			g2Edited,
			g5Content,
		}), http.StatusNoContent)
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest([][]byte{
			t22Edited,
			t23Content,
		}), http.StatusNoContent, "team_id", fmt.Sprint(tm2.ID))
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest([][]byte{
			scopedMobileconfigForTest("T1.3", "T1.3.user", &payloadScopeUser),
		}), http.StatusNoContent, "team_id", fmt.Sprint(tm1.ID))
	s.awaitTriggerProfileSchedule(t)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "T2.2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T2.3", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h2: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "T2.2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T2.3", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// delete team 2 (h1 and h3 are part of that team)
	s.Do("DELETE", fmt.Sprintf("/api/latest/automdm/teams/%d", tm2.ID), nil, http.StatusOK)
	s.awaitTriggerProfileSchedule(t)
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "T2.2b", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.3", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "T2.2b", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "T2.3", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// apply the pending profiles
	triggerReconcileProfilesMarkVerifying()

	// all profiles now verifying
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h2: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// h1 verified one of the profiles
	require.NoError(t, apple_mdm.VerifyHostMDMProfiles(context.Background(), s.ds, h1, map[string]*automdm.HostMacOSProfile{
		"G2b": {Identifier: "G2b", DisplayName: "G2b", InstallDate: time.Now()},
	}))
	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerified},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h2: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	// switch a team host (h1) to another team (tm1)
	assignHostToTeam(h1, &tm1.ID)

	// Create a new profile that will be labeled
	body, headers = generateNewProfileMultipartRequest(
		t,
		"label_prof.mobileconfig",
		mobileconfigForTest("label_prof", "label_prof"),
		s.token,
		map[string][]string{"team_id": {fmt.Sprintf("%d", tm1.ID)}},
	)
	s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusOK, headers)

	var uid string
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		return sqlx.GetContext(ctx, q, &uid, `SELECT profile_uuid FROM mdm_apple_configuration_profiles WHERE identifier = ?`, "label_prof")
	})

	label, err := s.ds.NewLabel(ctx, &automdm.Label{Name: "test label 1", Query: "select 1;"})
	require.NoError(t, err)

	// Update label with host membership
	mysqltest.ExecAdhocSQL(
		t, s.ds, func(db sqlx.ExtContext) error {
			_, err := db.ExecContext(
				context.Background(),
				"INSERT IGNORE INTO label_membership (host_id, label_id) VALUES (?, ?)",
				h1.ID,
				label.ID,
			)
			return err
		},
	)

	// Update profile <-> label mapping
	mysqltest.ExecAdhocSQL(
		t, s.ds, func(db sqlx.ExtContext) error {
			_, err := db.ExecContext(
				context.Background(),
				"INSERT INTO mdm_configuration_profile_labels (apple_profile_uuid, label_name, label_id) VALUES (?, ?, ?)",
				uid,
				label.Name,
				label.ID,
			)
			return err
		},
	)

	triggerReconcileProfilesMarkVerifying()

	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "label_prof", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h2: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})

	require.NoError(t, apple_mdm.VerifyHostMDMProfiles(context.Background(), s.ds, h1, map[string]*automdm.HostMacOSProfile{
		"label_prof": {Identifier: "label_prof", DisplayName: "label_prof", InstallDate: time.Now()},
	}))

	s.assertHostAppleConfigProfiles(map[*automdm.Host][]automdm.HostMDMAppleProfile{
		h1: {
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "T1.3.user", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "label_prof", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerified},
		},
		h2: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h3: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
		h4: {
			{Identifier: "G2b", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: "G5", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMdConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
			{Identifier: mobileconfig.AutoMDMCARootConfigPayloadIdentifier, OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerifying},
		},
	})
}

func (s *integrationMDMTestSuite) TestMDMAppleConfigProfileCRUD() {
	t := s.T()
	ctx := context.Background()

	testTeam, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "TestTeam"})
	require.NoError(t, err)

	teamDelete, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "TeamDelete"})
	require.NoError(t, err)

	testProfiles := make(map[string]automdm.MDMAppleConfigProfile)
	generateTestProfile := func(name string, identifier string) {
		i := identifier
		if i == "" {
			i = fmt.Sprintf("%s.SomeIdentifier", name)
		}
		cp := automdm.MDMAppleConfigProfile{
			Name:       name,
			Identifier: i,
		}
		cp.Mobileconfig = mcBytesForTest(cp.Name, cp.Identifier, fmt.Sprintf("%s.UUID", name))
		testProfiles[name] = cp
	}

	generateNewReq := func(name string, teamID *uint) (*bytes.Buffer, map[string]string) {
		args := map[string][]string{}
		if teamID != nil {
			args["team_id"] = []string{fmt.Sprintf("%d", *teamID)}
		}
		return generateNewProfileMultipartRequest(t, "some_filename.mobileconfig", testProfiles[name].Mobileconfig, s.token, args)
	}
	createProfile := func(name string, teamID *uint) {
		body, headers := generateNewReq(name, teamID)
		res := s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusOK, headers)
		var newResp newMDMConfigProfileResponse
		require.NoError(t, json.NewDecoder(res.Body).Decode(&newResp))
		require.NotEmpty(t, newResp.ProfileUUID)
		tp := testProfiles[name]
		tp.ProfileUUID = newResp.ProfileUUID
		testProfiles[name] = tp
	}
	profilePath := func(profileUUID string) string {
		return "/api/latest/automdm/configuration_profiles/" + profileUUID
	}
	authHeaders := map[string]string{"Authorization": fmt.Sprintf("Bearer %s", s.token)}

	checkGetResponse := func(resp *http.Response, expected automdm.MDMAppleConfigProfile) {
		// check expected headers
		require.Contains(t, resp.Header["Content-Type"], "application/x-apple-aspen-config")
		require.Contains(t, resp.Header["Content-Disposition"], fmt.Sprintf(`attachment;filename="%s_%s.%s"`, time.Now().Format("2006-01-02"), strings.ReplaceAll(expected.Name, " ", "_"), "mobileconfig"))
		// check expected body
		var bb bytes.Buffer
		_, err = io.Copy(&bb, resp.Body)
		require.NoError(t, err)
		require.Equal(t, []byte(expected.Mobileconfig), bb.Bytes())
	}

	checkConfigProfile := func(expected automdm.MDMAppleConfigProfile, actual automdm.MDMConfigProfilePayload) {
		require.Equal(t, expected.ProfileUUID, actual.ProfileUUID)
		require.Equal(t, expected.Name, actual.Name)
		require.Equal(t, expected.Identifier, actual.Identifier)
	}

	host, _ := createHostThenEnrollMDM(s.ds, s.server.URL, t)
	s.Do("POST", "/api/latest/automdm/hosts/transfer", addHostsToTeamRequest{
		TeamID:  &teamDelete.ID,
		HostIDs: []uint{host.ID},
	}, http.StatusOK)

	// create new profile (no team)
	generateTestProfile("TestNoTeam", "")
	createProfile("TestNoTeam", nil)

	// create new profile (with team id)
	generateTestProfile("TestWithTeamID", "")
	createProfile("TestWithTeamID", &testTeam.ID)

	// Create a profile that we're going to remove immediately
	generateTestProfile("TestImmediateDelete", "")
	createProfile("TestImmediateDelete", &teamDelete.ID)

	// We need to run the cron to produce host profile entries
	s.awaitTriggerProfileSchedule(t)

	// check that host_mdm_apple_profiles entry was created
	var hostResp getHostResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID), nil, http.StatusOK, &hostResp)
	require.NotNil(t, hostResp.Host.MDM.Profiles)
	require.Len(t, *hostResp.Host.MDM.Profiles, 3) // TestImmediateDelete + AutoMDMd + AutoMDM CA
	seen := false
	for _, p := range *hostResp.Host.MDM.Profiles {
		if p.Name == "TestImmediateDelete" {
			seen = true
			break
		}
	}
	require.True(t, seen, "expected to see TestImmediateDelete profile in host response")

	// now delete the profile before it's sent, we should see the host_mdm_apple_profiles entry go
	// away
	deletedCP := testProfiles["TestImmediateDelete"]
	var deleteResp deleteMDMConfigProfileResponse
	s.DoJSON("DELETE", profilePath(deletedCP.ProfileUUID), nil, http.StatusOK, &deleteResp)
	// confirm deleted
	var listResp listMDMConfigProfilesResponse
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles", nil, http.StatusOK, &listResp, "team_id", fmt.Sprint(teamDelete.ID))
	require.Empty(t, listResp.Profiles)
	_ = s.DoRawWithHeaders("GET", profilePath(deletedCP.ProfileUUID), nil, http.StatusNotFound, authHeaders)

	// Doing these cron run kinda muddies the test scenario, but can be discussed in PR review what we want to do here.
	s.awaitTriggerProfileSchedule(t)
	// confirm no host profiles
	hostResp = getHostResponse{}
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID), nil, http.StatusOK, &hostResp)
	require.NotNil(t, hostResp.Host.MDM.Profiles)
	require.Len(t, *hostResp.Host.MDM.Profiles, 2) // AutoMDMd + AutoMDM CA, TestImmediateDelete should be gone

	// list profiles (no team)
	expectedCP := testProfiles["TestNoTeam"]
	listResp = listMDMConfigProfilesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles", nil, http.StatusOK, &listResp)
	require.Len(t, listResp.Profiles, 1)
	respCP := listResp.Profiles[0]
	checkConfigProfile(expectedCP, *respCP)
	require.Empty(t, respCP.TeamID) // zero means no team

	// list profiles (team 1)
	expectedCP = testProfiles["TestWithTeamID"]
	listResp = listMDMConfigProfilesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles", nil, http.StatusOK, &listResp, "team_id", fmt.Sprint(testTeam.ID))
	require.Len(t, listResp.Profiles, 1)
	respCP = listResp.Profiles[0]
	checkConfigProfile(expectedCP, *respCP)
	require.NotNil(t, respCP.TeamID)
	require.Equal(t, testTeam.ID, *respCP.TeamID) // team 1

	// get profile (no team)
	expectedCP = testProfiles["TestNoTeam"]
	getResp := s.DoRawWithHeaders("GET", profilePath(expectedCP.ProfileUUID)+"?alt=media", nil, http.StatusOK, authHeaders)
	checkGetResponse(getResp, expectedCP)

	// get profile (team 1)
	expectedCP = testProfiles["TestWithTeamID"]
	getResp = s.DoRawWithHeaders("GET", profilePath(expectedCP.ProfileUUID)+"?alt=media", nil, http.StatusOK, authHeaders)
	checkGetResponse(getResp, expectedCP)

	// delete profile (no team)
	deletedCP = testProfiles["TestNoTeam"]
	s.DoJSON("DELETE", profilePath(deletedCP.ProfileUUID), nil, http.StatusOK, &deleteResp)
	// confirm deleted
	listResp = listMDMConfigProfilesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles", nil, http.StatusOK, &listResp)
	require.Empty(t, listResp.Profiles)
	_ = s.DoRawWithHeaders("GET", profilePath(deletedCP.ProfileUUID), nil, http.StatusNotFound, authHeaders)

	// delete profile (team 1)
	deletedCP = testProfiles["TestWithTeamID"]
	deleteResp = deleteMDMConfigProfileResponse{}
	s.DoJSON("DELETE", profilePath(deletedCP.ProfileUUID), nil, http.StatusOK, &deleteResp)
	// confirm deleted
	listResp = listMDMConfigProfilesResponse{}
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles", nil, http.StatusOK, &listResp, "team_id", fmt.Sprint(testTeam.ID))
	require.Empty(t, listResp.Profiles)
	_ = s.DoRawWithHeaders("GET", profilePath(deletedCP.ProfileUUID), nil, http.StatusNotFound, authHeaders)

	// fail to create new profile (no team), invalid automdm secret
	testProfiles["badSecrets"] = automdm.MDMAppleConfigProfile{
		Name:       "badSecrets",
		Identifier: "badSecrets.One",
		Mobileconfig: mobileconfig.Mobileconfig(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array/>
	<key>PayloadDisplayName</key>
	<string>badSecrets</string>
	<key>PayloadIdentifier</key>
	<string>badSecrets.One</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>$AUTOMDM_SECRET_INVALID.35E2029E-A0C2-4754-B709-4CAAB1B8D3CB</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`),
	}

	body, headers := generateNewReq("badSecrets", nil)
	res := s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusUnprocessableEntity, headers)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "$AUTOMDM_SECRET_INVALID")

	// trying to add/delete profiles with identifiers managed by AutoMDM fails
	for p := range mobileconfig.AutoMDMPayloadIdentifiers() {
		generateTestProfile("TestNoTeam", p)
		body, headers := generateNewReq("TestNoTeam", nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)

		generateTestProfile("TestWithTeamID", p)
		body, headers = generateNewReq("TestWithTeamID", nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)
		cp, err := automdm.NewMDMAppleConfigProfile(mobileconfigForTestWithContent("N1", "I1", p, "random", ""), nil)
		require.NoError(t, err)
		testProfiles["WithContent"] = *cp
		body, headers = generateNewReq("WithContent", nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)
	}

	// trying to add profiles with identifiers managed by AutoMDM fails
	for p := range mobileconfig.AutoMDMPayloadIdentifiers() {
		generateTestProfile("TestNoTeam", p)
		body, headers := generateNewReq("TestNoTeam", nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)

		generateTestProfile("TestWithTeamID", p)
		body, headers = generateNewReq("TestWithTeamID", nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)
		cp, err := automdm.NewMDMAppleConfigProfile(mobileconfigForTestWithContent("N1", "I1", p, "random", ""), nil)
		require.NoError(t, err)
		testProfiles["WithContent"] = *cp
		body, headers = generateNewReq("WithContent", nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)
	}

	// trying to add profiles with names reserved by AutoMDM fails
	for name := range servermdm.AutoMDMReservedProfileNames() {
		cp := &automdm.MDMAppleConfigProfile{
			Name:         name,
			Identifier:   "valid.identifier",
			Mobileconfig: mcBytesForTest(name, "valid.identifier", "some-uuid"),
		}
		body, headers := generateNewProfileMultipartRequest(t, "some_filename.mobileconfig", cp.Mobileconfig, s.token, nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)

		body, headers = generateNewProfileMultipartRequest(t, "some_filename.mobileconfig", cp.Mobileconfig, s.token, map[string][]string{
			"team_id": {fmt.Sprintf("%d", testTeam.ID)},
		})
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)

		cp, err := automdm.NewMDMAppleConfigProfile(mobileconfigForTestWithContent(
			"valid outer name",
			"valid.outer.identifier",
			"valid.inner.identifer",
			"some-uuid",
			name,
		), nil)
		require.NoError(t, err)
		body, headers = generateNewProfileMultipartRequest(t, "some_filename.mobileconfig", cp.Mobileconfig, s.token, nil)
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)

		cp.TeamID = &testTeam.ID
		body, headers = generateNewProfileMultipartRequest(t, "some_filename.mobileconfig", cp.Mobileconfig, s.token, map[string][]string{
			"team_id": {fmt.Sprintf("%d", testTeam.ID)},
		})

		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)
	}

	// make team add a FileVault profile
	acResp := appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"mdm": { "enable_disk_encryption": true }
  }`), http.StatusOK, &acResp)
	assert.True(t, acResp.MDM.EnableDiskEncryption.Value)
	profile := s.assertConfigProfilesByIdentifier(nil, mobileconfig.AutoMDMFileVaultPayloadIdentifier, true)

	// try to delete the profile
	deleteResp = deleteMDMConfigProfileResponse{}
	s.DoJSON("DELETE", profilePath(profile.ProfileUUID), nil, http.StatusBadRequest, &deleteResp) //nolint:nilaway // assertConfigProfilesByIdentifier fails the test when the profile doesn't exist
}

func (s *integrationMDMTestSuite) TestAppleProfileDeletion() {
	t := s.T()
	ctx := context.Background()

	err := s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: t.Name()}})
	require.NoError(t, err)

	globalProfiles := [][]byte{
		mobileconfigForTest("N1", "I1"),
	}
	wantGlobalProfiles := globalProfiles
	wantGlobalProfiles = append(
		wantGlobalProfiles,
		setupExpectedAutoMDMdProfile(t, s.server.URL, t.Name(), nil),
	)

	// add global profiles
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)

	// make sure ensureAutoMDMProfiles has been called
	s.awaitTriggerProfileSchedule(t)

	// Create a host and then enroll to MDM.
	host, mdmDevice := createHostThenEnrollMDM(s.ds, s.server.URL, t)
	// Add IdP email to host
	mysqltest.ExecAdhocSQL(t, s.ds, func(e sqlx.ExtContext) error {
		_, err := e.ExecContext(ctx, `INSERT INTO host_emails (email, host_id, source) VALUES (?, ?, ?)`, "idp@example.com", host.ID,
			automdm.DeviceMappingMDMIdpAccounts)
		return err
	})

	s.awaitRunAppleMDMWorkerSchedule()
	installs, removes := checkNextPayloads(t, mdmDevice, false)
	// verify that we received all profiles
	s.signedProfilesMatch(
		append(wantGlobalProfiles, setupExpectedCAProfile(t, s.ds)),
		installs,
	)
	require.Empty(t, removes)

	// Simulate 1 minute expiration for redis key
	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+host.UUID)
	require.NoError(t, err)

	// Add a profile with an AutoMDM variable. We are also testing that removal of a profile with an AutoMDM variable works.
	// A unique command is created for each host when this AutoMDM variable is used.
	globalProfilesPlusOne := [][]byte{
		globalProfiles[0],
		mobileconfigForTest("N2", "$AUTOMDM_VAR_"+string(automdm.AutoMDMVarHostEndUserIDPUsername)),
	}
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "N1", Contents: globalProfilesPlusOne[0]},
		{Name: "N2", Contents: globalProfilesPlusOne[1]},
	}}, http.StatusNoContent)
	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)

	// Make sure profile was uploaded
	profiles, err := s.ds.GetHostMDMAppleProfiles(ctx, host.UUID)
	require.NoError(t, err)
	assert.Len(t, profiles, 4)

	// Delete a profile before it is sent to device
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)
	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)

	sendErrorOnRemoveProfile := func(device *mdmtest.TestAppleMDMClient) {
		// The host grabs the removal command from AutoMDM
		cmd, err := device.Idle()
		require.NoError(t, err)
		assert.Equal(t, "RemoveProfile", cmd.Command.RequestType)
		// Since profile is not on the device, it returns an error.
		errChain := []mdm.ErrorChain{
			{
				ErrorCode:            89,
				ErrorDomain:          "FooErrorDomain",
				LocalizedDescription: "The profile not found",
			},
		}
		cmd, err = device.Err(cmd.CommandUUID, errChain)
		require.NoError(t, err)
		assert.Nil(t, cmd)
	}
	sendErrorOnRemoveProfile(mdmDevice)

	// Make sure deleted profile no longer shows up
	profiles, err = s.ds.GetHostMDMAppleProfiles(ctx, host.UUID)
	require.NoError(t, err)
	assert.Len(t, profiles, 3)

	// Add a profile again
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "N1", Contents: globalProfilesPlusOne[0]},
		{Name: "N2", Contents: globalProfilesPlusOne[1]},
	}}, http.StatusNoContent)
	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)

	// The host grabs the profile from AutoMDM
	cmd, err := mdmDevice.Idle()
	require.NoError(t, err)
	assert.Equal(t, "InstallProfile", cmd.Command.RequestType)
	// Verify that the AutoMDM variable was replaced with the IdP username, which is the IdP email without a SCIM user
	type Command struct {
		Command struct {
			Payload []byte
		}
	}
	var p Command
	err = plist.Unmarshal(cmd.Raw, &p)
	require.NoError(t, err)
	assert.NotContains(t, string(p.Command.Payload), "$AUTOMDM_VAR_"+automdm.AutoMDMVarHostEndUserIDPUsername)
	assert.Contains(t, string(p.Command.Payload), "idp@example.com")

	// While the host is installing the profile, we delete it.
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)
	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)

	// Host acknowledges installing the profile and grabs the remove command
	cmd, err = mdmDevice.Acknowledge(cmd.CommandUUID)
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.Equal(t, "RemoveProfile", cmd.Command.RequestType)
	cmd, err = mdmDevice.Acknowledge(cmd.CommandUUID)
	require.NoError(t, err)
	assert.Nil(t, cmd)

	// Add another device
	host2, mdmDevice2 := createHostThenEnrollMDM(s.ds, s.server.URL, t)
	// Add IdP email to host
	mysqltest.ExecAdhocSQL(t, s.ds, func(e sqlx.ExtContext) error {
		_, err := e.ExecContext(ctx, `INSERT INTO host_emails (email, host_id, source) VALUES (?, ?, ?)`, "idp2@example.com", host2.ID,
			automdm.DeviceMappingMDMIdpAccounts)
		return err
	})

	// Run the worker to process post-enrollment for host2 and install initial profiles
	s.awaitRunAppleMDMWorkerSchedule()
	installs, removes = checkNextPayloads(t, mdmDevice2, false)
	assert.Len(t, installs, 3)
	assert.Empty(t, removes)

	// Simulate redis key expiration for host2
	err = s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+host2.UUID)
	require.NoError(t, err)

	// Add a profile again
	s.Do("POST", "/api/latest/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "N1", Contents: globalProfilesPlusOne[0]},
		{Name: "N2", Contents: globalProfilesPlusOne[1]},
	}}, http.StatusNoContent)
	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)
	// Delete a profile before it is sent to both devices
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)
	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)
	// The host grabs the removal command from AutoMDM
	sendErrorOnRemoveProfile(mdmDevice)
	sendErrorOnRemoveProfile(mdmDevice2)

	// Make sure deleted profile no longer shows up on either host
	profiles, err = s.ds.GetHostMDMAppleProfiles(ctx, host.UUID)
	require.NoError(t, err)
	assert.Len(t, profiles, 3)
	profiles, err = s.ds.GetHostMDMAppleProfiles(ctx, host2.UUID)
	require.NoError(t, err)
	assert.Len(t, profiles, 3)
}

func (s *integrationMDMTestSuite) TestVerifyUserScopedProfiles() {
	t := s.T()
	ctx := t.Context()
	s.setSkipWorkerJobs(t)

	// create a macOS host, will enroll only with device
	host, device := createHostThenEnrollMDM(s.ds, s.server.URL, t)

	// create some profiles, system- and user-scoped
	payloadScopeSystem := automdm.PayloadScopeSystem
	payloadScopeUser := automdm.PayloadScopeUser
	profiles := []automdm.MDMProfileBatchPayload{
		{Name: "A1", Contents: scopedMobileconfigForTest("A1", "A1", &payloadScopeSystem)},
		{Name: "A2", Contents: scopedMobileconfigForTest("A2", "A2.user", &payloadScopeUser)},
		{Name: "A3", Contents: scopedMobileconfigForTest("A3", "A3.user", &payloadScopeUser)},
	}
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: profiles}, http.StatusNoContent)

	// ensure we are at least 1s after the profiles uploaded-at timestamp
	time.Sleep(time.Second)

	// list the profiles to get the UUIDs
	var listResp listMDMConfigProfilesResponse
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles", nil, http.StatusOK, &listResp)
	profNameToPayload := make(map[string]*automdm.MDMConfigProfilePayload)
	for _, prof := range listResp.Profiles {
		if len(prof.Checksum) == 0 {
			// not important, but must not be empty or it causes issues when forcing a status
			prof.Checksum = []byte("checksum")
		}
		profNameToPayload[prof.Name] = prof
		t.Logf("profile %s: %s", prof.Name, prof.ProfileUUID)
	}

	type hostProfile struct {
		ProfileUUID       string  `db:"profile_uuid"`
		ProfileIdentifier string  `db:"profile_identifier"`
		ProfileName       string  `db:"profile_name"`
		Status            *string `db:"status"`
		OperationType     *string `db:"operation_type"`
		Retries           int     `db:"retries"`
		Scope             string  `db:"scope"`
	}
	assertHostProfiles := func(want []hostProfile) {
		var got []hostProfile
		mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
			// for the purpose of this test, we ignore the AutoMDM-internal profiles
			// (we only care about the custom profiles)
			return sqlx.SelectContext(t.Context(), q, &got, `
				SELECT profile_uuid, profile_identifier, profile_name, status, operation_type, retries, scope
				FROM host_mdm_apple_profiles
				WHERE host_uuid = ? AND profile_identifier NOT IN (?, ?)`,
				host.UUID, mobileconfig.AutoMDMdConfigPayloadIdentifier, mobileconfig.AutoMDMCARootConfigPayloadIdentifier)
		})
		require.ElementsMatch(t, want, got)
	}

	forceProfileUploadeddAtTimestamp := func(ident string, ts time.Time) {
		mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
			_, err := q.ExecContext(ctx, `UPDATE mdm_apple_configuration_profiles
				SET uploaded_at = ? WHERE identifier = ?`, ts, ident)
			return err
		})
	}

	// cron job hasn't run yet, so no profile exist for the host
	assertHostProfiles([]hostProfile{})

	// ensure automdm profiles
	s.awaitTriggerProfileSchedule(t)
	s.awaitRunAppleMDMWorkerSchedule()

	require.NoError(t, s.keyValueStore.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+host.UUID))

	// user-scoped profiles show up as status nil (no user-enrollment yet)
	assertHostProfiles([]hostProfile{
		{
			ProfileUUID:       profNameToPayload["A1"].ProfileUUID, // nolint:nilaway // every name looked up here was inserted from the list response above
			ProfileIdentifier: profNameToPayload["A1"].Identifier,  // nolint:nilaway // every name looked up here was inserted from the list response above
			ProfileName:       profNameToPayload["A1"].Name,        // nolint:nilaway // every name looked up here was inserted from the list response above
			Status:            new(string(automdm.MDMDeliveryPending)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeSystem),
		},
		{
			ProfileUUID:       profNameToPayload["A2"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A2"].Identifier,
			ProfileName:       profNameToPayload["A2"].Name,
			Status:            nil,
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
		{
			ProfileUUID:       profNameToPayload["A3"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A3"].Identifier,
			ProfileName:       profNameToPayload["A3"].Name,
			Status:            nil,
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
	})

	// verify the profiles, only the system one is reported as installed
	host.DetailUpdatedAt = time.Now().UTC()
	err := s.ds.UpdateHost(ctx, host)
	require.NoError(t, err)

	err = apple_mdm.VerifyHostMDMProfiles(ctx, s.ds, host, map[string]*automdm.HostMacOSProfile{
		profNameToPayload["A1"].Identifier: {
			DisplayName: profNameToPayload["A1"].Name,
			Identifier:  profNameToPayload["A1"].Identifier,
			InstallDate: time.Now().UTC(),
		},
	})
	require.NoError(t, err)

	// user-scoped profiles were left untouched
	assertHostProfiles([]hostProfile{
		{
			ProfileUUID:       profNameToPayload["A1"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A1"].Identifier,
			ProfileName:       profNameToPayload["A1"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeSystem),
		},
		{
			ProfileUUID:       profNameToPayload["A2"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A2"].Identifier,
			ProfileName:       profNameToPayload["A2"].Name,
			Status:            nil,
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
		{
			ProfileUUID:       profNameToPayload["A3"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A3"].Identifier,
			ProfileName:       profNameToPayload["A3"].Name,
			Status:            nil,
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
	})

	// create the user-enrollment
	err = device.UserEnroll()
	require.NoError(t, err)

	// trigger a profile sync
	s.awaitTriggerProfileSchedule(t)

	// user-scoped profiles have been added as pending (not nil)
	assertHostProfiles([]hostProfile{
		{
			ProfileUUID:       profNameToPayload["A1"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A1"].Identifier,
			ProfileName:       profNameToPayload["A1"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeSystem),
		},
		{
			ProfileUUID:       profNameToPayload["A2"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A2"].Identifier,
			ProfileName:       profNameToPayload["A2"].Name,
			Status:            new(string(automdm.MDMDeliveryPending)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
		{
			ProfileUUID:       profNameToPayload["A3"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A3"].Identifier,
			ProfileName:       profNameToPayload["A3"].Name,
			Status:            new(string(automdm.MDMDeliveryPending)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
	})

	// verify the profiles, A3 is missing but still within the grace period
	err = apple_mdm.VerifyHostMDMProfiles(ctx, s.ds, host, map[string]*automdm.HostMacOSProfile{
		profNameToPayload["A1"].Identifier: {
			DisplayName: profNameToPayload["A1"].Name,
			Identifier:  profNameToPayload["A1"].Identifier,
			InstallDate: time.Now().UTC(),
		},
		profNameToPayload["A2"].Identifier: {
			DisplayName: profNameToPayload["A2"].Name,
			Identifier:  profNameToPayload["A2"].Identifier,
			InstallDate: time.Now().UTC(),
		},
	})
	require.NoError(t, err)

	// A2 is now verified, A3 is still pending
	assertHostProfiles([]hostProfile{
		{
			ProfileUUID:       profNameToPayload["A1"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A1"].Identifier,
			ProfileName:       profNameToPayload["A1"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeSystem),
		},
		{
			ProfileUUID:       profNameToPayload["A2"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A2"].Identifier,
			ProfileName:       profNameToPayload["A2"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
		{
			ProfileUUID:       profNameToPayload["A3"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A3"].Identifier,
			ProfileName:       profNameToPayload["A3"].Name,
			Status:            new(string(automdm.MDMDeliveryPending)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
	})

	// rewind the uploaded_at timestamp of A3 so it is not in the grace period
	forceProfileUploadeddAtTimestamp(profNameToPayload["A3"].Identifier, time.Now().Add(-24*time.Hour))

	// report as still missing
	err = apple_mdm.VerifyHostMDMProfiles(ctx, s.ds, host, map[string]*automdm.HostMacOSProfile{
		profNameToPayload["A1"].Identifier: {
			DisplayName: profNameToPayload["A1"].Name,
			Identifier:  profNameToPayload["A1"].Identifier,
			InstallDate: time.Now().UTC(),
		},
		profNameToPayload["A2"].Identifier: {
			DisplayName: profNameToPayload["A2"].Name,
			Identifier:  profNameToPayload["A2"].Identifier,
			InstallDate: time.Now().UTC(),
		},
	})
	require.NoError(t, err)

	// A3 is now missing and retries
	assertHostProfiles([]hostProfile{
		{
			ProfileUUID:       profNameToPayload["A1"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A1"].Identifier,
			ProfileName:       profNameToPayload["A1"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeSystem),
		},
		{
			ProfileUUID:       profNameToPayload["A2"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A2"].Identifier,
			ProfileName:       profNameToPayload["A2"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
		{
			ProfileUUID:       profNameToPayload["A3"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A3"].Identifier,
			ProfileName:       profNameToPayload["A3"].Name,
			Status:            nil,
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           1,
			Scope:             string(automdm.PayloadScopeUser),
		},
	})

	s.awaitTriggerProfileSchedule(t)

	// force-set it to Verifying and set retries to max-1 so that by being missing again it goes to failed
	// (it doesn't go to failed if it is pending)
	forceSetAppleHostProfileStatus(t, s.ds, host.UUID,
		test.ToMDMAppleConfigProfile(profNameToPayload["A3"]), automdm.MDMOperationTypeInstall, automdm.MDMDeliveryVerifying)
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(ctx, `UPDATE host_mdm_apple_profiles SET retries = ? WHERE host_uuid = ? AND profile_identifier = ?`,
			servermdm.MaxAppleProfileRetries, host.UUID, profNameToPayload["A3"].Identifier)
		return err
	})

	err = apple_mdm.VerifyHostMDMProfiles(ctx, s.ds, host, map[string]*automdm.HostMacOSProfile{
		profNameToPayload["A1"].Identifier: {
			DisplayName: profNameToPayload["A1"].Name,
			Identifier:  profNameToPayload["A1"].Identifier,
			InstallDate: time.Now().UTC(),
		},
		profNameToPayload["A2"].Identifier: {
			DisplayName: profNameToPayload["A2"].Name,
			Identifier:  profNameToPayload["A2"].Identifier,
			InstallDate: time.Now().UTC(),
		},
	})
	require.NoError(t, err)

	assertHostProfiles([]hostProfile{
		{
			ProfileUUID:       profNameToPayload["A1"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A1"].Identifier,
			ProfileName:       profNameToPayload["A1"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeSystem),
		},
		{
			ProfileUUID:       profNameToPayload["A2"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A2"].Identifier,
			ProfileName:       profNameToPayload["A2"].Name,
			Status:            new(string(automdm.MDMDeliveryVerified)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           0,
			Scope:             string(automdm.PayloadScopeUser),
		},
		{
			ProfileUUID:       profNameToPayload["A3"].ProfileUUID,
			ProfileIdentifier: profNameToPayload["A3"].Identifier,
			ProfileName:       profNameToPayload["A3"].Name,
			Status:            new(string(automdm.MDMDeliveryFailed)),
			OperationType:     new(string(automdm.MDMOperationTypeInstall)),
			Retries:           servermdm.MaxAppleProfileRetries,
			Scope:             string(automdm.PayloadScopeUser),
		},
	})
}

func (s *integrationMDMTestSuite) TestMDMAppleProfileScopeChanges() {
	t := s.T()
	ctx := context.Background()

	// add a couple global profiles
	payloadScopeSystem := automdm.PayloadScopeSystem
	payloadScopeUser := automdm.PayloadScopeUser
	globalProfiles := [][]byte{
		mobileconfigForTest("G1", "G1"),
		scopedMobileconfigForTest("G2", "G2", &payloadScopeSystem),
		scopedMobileconfigForTest("G3", "G3.user", &payloadScopeUser),
		scopedMobileconfigForTest("G4", "G4.user-but-actually-system", &payloadScopeUser),
	}
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)

	// Create a profile with a scope that is System in the DB but User in the XML. This mimics
	// our upgrade behavior from versions prior to 4.71 to 4.71+ when we added support for User
	// scoped profiles
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		stmt := `UPDATE mdm_apple_configuration_profiles SET scope=? WHERE identifier=?;`
		_, err := q.ExecContext(context.Background(), stmt, automdm.PayloadScopeSystem, "G4.user-but-actually-system")
		return err
	})

	// create a team with a couple profiles
	tm1, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team_profile_scope_changes_1"})
	require.NoError(t, err)
	tm1Profiles := [][]byte{
		mobileconfigForTest("T1.1", "T1.1"),
		scopedMobileconfigForTest("T1.2", "T1.2", &payloadScopeSystem),
		scopedMobileconfigForTest("T1.3", "T1.3.user", &payloadScopeUser),
	}

	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(tm1Profiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm1.ID))

	// create a second team with different profiles
	tm2, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team_profile_scope_changes_2"})
	require.NoError(t, err)
	tm2Profiles := [][]byte{
		mobileconfigForTest("T2.1", "T2.1"),
		scopedMobileconfigForTest("T2.2", "T2.2.user", &payloadScopeSystem),
		scopedMobileconfigForTest("T2.3", "T2.3.user", &payloadScopeUser),
	}

	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(tm2Profiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm2.ID))

	// Do a no-op update of each team's profiles, verify no errors are returned
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(globalProfiles), http.StatusNoContent)
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(tm1Profiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm1.ID))
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(tm2Profiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm2.ID))

	// Test a modification of an existing global profile with an implicit scope change
	newGlobalProfiles := [][]byte{
		globalProfiles[0],
		globalProfiles[1],
		globalProfiles[2],
		scopedMobileconfigForTest("G4-bozo", "G4.user-but-actually-system", &payloadScopeUser),
	}
	response := s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(newGlobalProfiles), http.StatusBadRequest)
	errMsg := extractServerErrorText(response.Body)
	require.Contains(t, errMsg, "Couldn't edit configuration profile (G4.user-but-actually-system) because it was previously delivered to some hosts on the device channel")

	// Test a conflict of a profile on a team with an existing global profile and an implicit scope change
	// Should error because "G4.user-but-actually-system" conflicts with global
	// "G4.user-but-actually-system" profile scope
	newTm1Profiles := [][]byte{
		tm1Profiles[0], // T1.1
		tm1Profiles[1], // T1.2
		tm1Profiles[2], // T1.3.user
		scopedMobileconfigForTest("G4-bozo", "G4.user-but-actually-system", &payloadScopeUser),
	}
	response = s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(newTm1Profiles), http.StatusBadRequest,
		"team_id", fmt.Sprint(tm1.ID))

	errMsg = extractServerErrorText(response.Body)
	require.Contains(t, errMsg, "Couldn't add configuration profile. This profile has the same \"PayloadIdentifier\" but a different \"PayloadScope\" as another profile in a separate team.")

	// Test a conflict of a profile on a team with an existing global profile
	// Should error because "G2" conflicts with global "G2" profile
	newTm1Profiles = [][]byte{
		tm1Profiles[0], // T1.1
		tm1Profiles[1], // T1.2
		tm1Profiles[2], // T1.3.user
		scopedMobileconfigForTest("G2", "G2", &payloadScopeUser),
	}
	response = s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(newTm1Profiles), http.StatusBadRequest,
		"team_id", fmt.Sprint(tm1.ID))

	errMsg = extractServerErrorText(response.Body)
	require.Contains(t, errMsg, "Couldn't add configuration profile. This profile has the same \"PayloadIdentifier\" but a different \"PayloadScope\" as another profile in a separate team.")

	// Test a conflict of a profile on a team versus one with the same identifier but different
	// scope on a different team.
	// Should error because "T2.3.user" system-scoped profile conflicts with team2 "T2.3.user" user-scoped profile
	newTm1Profiles = [][]byte{
		tm1Profiles[0], // T1.1
		tm1Profiles[1], // T1.2
		tm1Profiles[2], // T1.3.user
		scopedMobileconfigForTest("T2.3", "T2.3.user", &payloadScopeSystem), // T2.3.user changed to system scope
	}
	response = s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(newTm1Profiles), http.StatusBadRequest,
		"team_id", fmt.Sprint(tm1.ID))

	errMsg = extractServerErrorText(response.Body)
	require.Contains(t, errMsg, "Couldn't add configuration profile. This profile has the same \"PayloadIdentifier\" but a different \"PayloadScope\" as another profile in a separate team.")

	// Profile edit of existing profile on team1 with a new scope
	newTm1Profiles = [][]byte{
		tm1Profiles[0], // T1.1
		tm1Profiles[1], // T1.2
		scopedMobileconfigForTest("T1.3", "T1.3.user", &payloadScopeSystem), // T1.3.user changed to system scope
	}
	response = s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(newTm1Profiles), http.StatusBadRequest,
		"team_id", fmt.Sprint(tm1.ID))

	errMsg = extractServerErrorText(response.Body)
	require.Contains(t, errMsg, "Couldn't edit configuration profile (T1.3.user) because the profile's \"PayloadScope\" has changed")

	// Should be able to add these profiles to team1 with the proper scopes
	newTm1Profiles = [][]byte{
		tm1Profiles[0], // T1.1
		tm1Profiles[1], // T1.2
		tm1Profiles[2], // T1.3.user
		scopedMobileconfigForTest("G2", "G2", &payloadScopeSystem),
		scopedMobileconfigForTest("T2.3", "T2.3.user", &payloadScopeUser), // T2.3.user changed to system scope
	}

	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		appleProfilesBatchRequest(newTm1Profiles), http.StatusNoContent,
		"team_id", fmt.Sprint(tm1.ID))
}

func (s *integrationMDMTestSuite) TestIPadOSUpdateDeclarationAfterMDMReset() {
	t := s.T()
	s.setSkipWorkerJobs(t)
	ctx := context.Background()

	teamName := t.Name() + "team"
	var createTeamResp teamResponse
	s.DoJSON("POST", "/api/latest/automdm/teams", &automdm.Team{Name: teamName}, http.StatusOK, &createTeamResp)
	teamID := createTeamResp.Team.ID
	require.NotZero(t, teamID)

	ipad, device := s.createAppleMobileHostThenEnrollMDM("ipados")
	s.Do("POST", "/api/v1/automdm/hosts/transfer",
		addHostsToTeamRequest{TeamID: &teamID, HostIDs: []uint{ipad.ID}}, http.StatusOK)

	// Clear commands sent during enrollment so the command assertions below
	// isolate the OS update declaration.
	cmd, err := device.Idle()
	require.NoError(t, err)
	for cmd != nil {
		cmd, err = device.Acknowledge(cmd.CommandUUID)
		require.NoError(t, err)
	}

	// Automatic enrollment resets stale host state. Built-in memberships must
	// survive so label-scoped declarations still target the device.
	require.NoError(t, s.ds.MDMAppleResetOnReenrollment(ctx, ipad.UUID, true))
	labels, err := s.ds.ListLabelsForHost(ctx, ipad.ID)
	require.NoError(t, err)
	labelNames := make([]string, 0, len(labels))
	for _, label := range labels {
		labelNames = append(labelNames, label.Name)
	}
	require.ElementsMatch(t, []string{automdm.BuiltinLabelNameAllHosts, automdm.BuiltinLabelIPadOS}, labelNames)

	var applyResp applyTeamSpecsResponse
	s.DoJSON("POST", "/api/latest/automdm/spec/teams", applyTeamSpecsRequest{Specs: []*automdm.TeamSpec{{
		Name: teamName,
		MDM: automdm.TeamSpecMDM{
			IPadOSUpdates: automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString("17.6.1"),
				Deadline:       optjson.SetString("2025-06-01"),
			},
		},
	}}}, http.StatusOK, &applyResp)
	require.Len(t, applyResp.TeamIDsByName, 1)

	const updateIdentifier = "ipados-software-update-94f4bbdf-f439-4fb1-8d27-ae1bb793e105"
	require.NoError(t, ReconcileAppleDeclarationsBatched(ctx, s.ds, s.mdmCommander, s.logger))
	var status automdm.MDMDeliveryStatus
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		return sqlx.GetContext(ctx, q, &status, `
			SELECT status FROM host_mdm_apple_declarations
			WHERE host_uuid = ? AND declaration_identifier = ?`, ipad.UUID, updateIdentifier)
	})
	require.Equal(t, automdm.MDMDeliveryPending, status)

	cmd, err = device.Idle()
	require.NoError(t, err)
	require.NotNil(t, cmd)
	require.Equal(t, "DeclarativeManagement", cmd.Command.RequestType)
	_, err = device.Acknowledge(cmd.CommandUUID)
	require.NoError(t, err)

	resp, err := device.DeclarativeManagement("declaration-items")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var items automdm.MDMAppleDDMDeclarationItemsResponse
	require.NoError(t, json.Unmarshal(body, &items))
	var serverToken string
	for _, declaration := range items.Declarations.Configurations {
		if declaration.Identifier == updateIdentifier {
			serverToken = declaration.ServerToken
			break
		}
	}
	require.NotEmpty(t, serverToken)

	report := automdm.MDMAppleDDMStatusReport{}
	report.StatusItems.Management.Declarations.Configurations = []automdm.MDMAppleDDMStatusDeclaration{{
		Active: true, Valid: automdm.MDMAppleDeclarationValid, Identifier: updateIdentifier, ServerToken: serverToken,
	}}
	statusResp, err := device.DeclarativeManagement("status", report)
	require.NoError(t, err)
	statusResp.Body.Close()
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		return sqlx.GetContext(ctx, q, &status, `
			SELECT status FROM host_mdm_apple_declarations
			WHERE host_uuid = ? AND declaration_identifier = ?`, ipad.UUID, updateIdentifier)
	})
	require.Equal(t, automdm.MDMDeliveryVerified, status)
}

func (s *integrationMDMTestSuite) TestSelfServiceAppleConfigProfileInstallUninstall() {
	t := s.T()
	ctx := t.Context()
	kv := redis_key_value.New(s.redisPool)

	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "N1", Contents: mobileconfigForTest("N1", "I1")},
		{Name: "SS1", Contents: mobileconfigForTest("SS1", "ISS1")},
	}}, http.StatusNoContent)
	t.Cleanup(func() {
		s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: backwardsCompatProfilesParam{}}, http.StatusNoContent)
	})
	regularUUID := s.assertConfigProfilesByIdentifier(nil, "I1", true).ProfileUUID
	ssUUID := s.assertConfigProfilesByIdentifier(nil, "ISS1", true).ProfileUUID
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(ctx, `UPDATE mdm_apple_configuration_profiles SET self_service = 1 WHERE profile_uuid = ?`, ssUUID)
		return err
	})

	host, mdmDevice := createHostThenEnrollMDM(s.ds, s.server.URL, t)
	token := "self_service_profile_token"
	require.NoError(t, s.ds.SetOrUpdateDeviceAuthToken(ctx, host.ID, token))

	reconcileAndAck := func() {
		require.NoError(t, kv.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+host.UUID))
		require.NoError(t, ReconcileAppleProfilesBatched(ctx, s.ds, s.mdmCommander, kv, s.logger, 0, false))
		cmd, err := mdmDevice.Idle()
		require.NoError(t, err)
		for cmd != nil {
			cmd, err = mdmDevice.Acknowledge(cmd.CommandUUID)
			require.NoError(t, err)
		}
	}
	hostProfile := func(ident string) *automdm.HostMDMAppleProfile {
		profs, err := s.ds.GetHostMDMAppleProfiles(ctx, host.UUID)
		require.NoError(t, err)
		for _, p := range profs {
			if p.Identifier == ident {
				return &p
			}
		}
		return nil
	}
	activity := func(selfService bool) string {
		return fmt.Sprintf(`{"host_id": %d, "host_display_name": %q, "self_service": %t, "profile_name": "SS1"}`,
			host.ID, host.DisplayName(), selfService)
	}
	adminPath := func(profUUID, action string) string {
		return fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/%s", host.ID, profUUID, action)
	}
	devicePath := func(profUUID, action string) string {
		return fmt.Sprintf("/api/latest/automdm/device/%s/configuration_profiles/%s/%s", token, profUUID, action)
	}
	// ssDetails returns the self-service profile's entry from the admin and device host details, asserting both
	// agree and that it is listed at most once.
	ssDetails := func() *automdm.HostMDMProfile {
		var hostResp getHostResponse
		s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", host.ID), nil, http.StatusOK, &hostResp)
		var deviceResp getDeviceHostResponse
		res := s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token, nil, http.StatusOK)
		require.NoError(t, json.NewDecoder(res.Body).Decode(&deviceResp))
		find := func(profs *[]automdm.HostMDMProfile) *automdm.HostMDMProfile {
			require.NotNil(t, profs)
			var found *automdm.HostMDMProfile
			for _, p := range *profs {
				if p.ProfileUUID == ssUUID {
					require.Nil(t, found, "self-service profile listed more than once")
					found = &p
				}
			}
			return found
		}
		got := find(hostResp.Host.MDM.Profiles)
		require.Equal(t, got, find(deviceResp.Host.MDM.Profiles))
		return got
	}
	// requireQueued asserts the install/uninstall endpoint wrote a NULL-status row for the reconciler, shown as pending.
	requireQueued := func(op automdm.MDMOperationType) {
		p := hostProfile("ISS1")
		require.NotNil(t, p)
		require.Equal(t, op, p.OperationType)
		var rawStatus *string
		mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
			return sqlx.GetContext(ctx, q, &rawStatus, `SELECT status FROM host_mdm_apple_profiles WHERE host_uuid = ? AND profile_uuid = ?`, host.UUID, ssUUID)
		})
		require.Nil(t, rawStatus)
		d := ssDetails()
		require.NotNil(t, d)
		require.Equal(t, op, d.OperationType)
		require.Equal(t, string(automdm.MDMDeliveryPending), *d.Status)
	}
	requireAvailable := func() {
		d := ssDetails()
		require.NotNil(t, d)
		require.Nil(t, d.Status)
		require.True(t, d.SelfService)
	}

	// Without an opt-in, only the regular profile is delivered.
	reconcileAndAck()
	require.NotNil(t, hostProfile("I1"))
	require.Nil(t, hostProfile("ISS1"))
	requireAvailable()

	// Invalid targets are rejected.
	s.Do("POST", adminPath(regularUUID, "install"), nil, http.StatusBadRequest)
	s.Do("POST", adminPath(uuid.NewString(), "install"), nil, http.StatusBadRequest)
	s.Do("POST", adminPath(ssUUID, "uninstall"), nil, http.StatusNotFound)
	linuxHost := createOrbitEnrolledHost(t, "linux", "self_service_linux", s.ds)
	s.Do("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/install", linuxHost.ID, ssUUID), nil, http.StatusBadRequest)

	// A macOS host with MDM off neither lists nor accepts the self-service profile.
	noMDMHost := createOrbitEnrolledHost(t, "darwin", "self_service_no_mdm", s.ds)
	var noMDMResp getHostResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", noMDMHost.ID), nil, http.StatusOK, &noMDMResp)
	if noMDMResp.Host.MDM.Profiles != nil {
		for _, p := range *noMDMResp.Host.MDM.Profiles {
			require.NotEqual(t, ssUUID, p.ProfileUUID)
		}
	}
	s.Do("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/configuration_profiles/%s/install", noMDMHost.ID, ssUUID), nil, http.StatusBadRequest)
	optedIn, err := s.ds.HasHostMDMProfileOptIn(ctx, noMDMHost.UUID, ssUUID)
	require.NoError(t, err)
	require.False(t, optedIn)

	// Admin opts the host in; the reconciler then installs the profile.
	s.Do("POST", adminPath(ssUUID, "install"), nil, http.StatusAccepted)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeInstalledOptInConfigurationProfile{}.ActivityName(), activity(false), 0)
	s.Do("POST", adminPath(ssUUID, "install"), nil, http.StatusConflict)
	requireQueued(automdm.MDMOperationTypeInstall)
	s.DoRawNoAuth("POST", devicePath(ssUUID, "install"), nil, http.StatusConflict)

	reconcileAndAck()
	p := hostProfile("ISS1")
	require.NotNil(t, p)
	require.Equal(t, automdm.MDMOperationTypeInstall, p.OperationType)
	require.Equal(t, automdm.MDMDeliveryVerifying, *p.Status)
	d := ssDetails()
	require.NotNil(t, d)
	require.Equal(t, string(automdm.MDMDeliveryVerifying), *d.Status)

	// End user opts out from the device endpoint; the reconciler removes it.
	s.DoRawNoAuth("POST", devicePath(ssUUID, "uninstall"), nil, http.StatusAccepted)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeUninstalledOptInConfigurationProfile{}.ActivityName(), activity(true), 0)
	s.DoRawNoAuth("POST", devicePath(ssUUID, "uninstall"), nil, http.StatusNotFound)
	requireQueued(automdm.MDMOperationTypeRemove)

	require.NoError(t, kv.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+host.UUID))
	require.NoError(t, ReconcileAppleProfilesBatched(ctx, s.ds, s.mdmCommander, kv, s.logger, 0, false))
	p = hostProfile("ISS1")
	require.NotNil(t, p)
	require.Equal(t, automdm.MDMOperationTypeRemove, p.OperationType)
	d = ssDetails()
	require.NotNil(t, d)
	require.Equal(t, automdm.MDMOperationTypeRemove, d.OperationType)
	require.Equal(t, string(automdm.MDMDeliveryPending), *d.Status)
	reconcileAndAck()
	require.Nil(t, hostProfile("ISS1"))
	require.NotNil(t, hostProfile("I1"))
	requireAvailable()

	// End user opts back in from the device endpoint.
	s.DoRawNoAuth("POST", devicePath(ssUUID, "install"), nil, http.StatusAccepted)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeInstalledOptInConfigurationProfile{}.ActivityName(), activity(true), 0)
	requireQueued(automdm.MDMOperationTypeInstall)
	reconcileAndAck()
	p = hostProfile("ISS1")
	require.NotNil(t, p)
	require.Equal(t, automdm.MDMOperationTypeInstall, p.OperationType)
	require.Equal(t, automdm.MDMDeliveryVerifying, *p.Status)

	// Install and uninstall are refused until the other reaches a terminal state, so a command still in flight can't
	// undo the newer request (e.g. a late InstallProfile ack leaving the profile on the device without an opt-in).
	reconcileOnly := func() {
		require.NoError(t, kv.Delete(ctx, automdm.MDMProfileProcessingKeyPrefix+":"+host.UUID))
		require.NoError(t, ReconcileAppleProfilesBatched(ctx, s.ds, s.mdmCommander, kv, s.logger, 0, false))
	}
	ackAll := func() {
		cmd, err := mdmDevice.Idle()
		require.NoError(t, err)
		for cmd != nil {
			cmd, err = mdmDevice.Acknowledge(cmd.CommandUUID)
			require.NoError(t, err)
		}
	}
	s.Do("POST", adminPath(ssUUID, "uninstall"), nil, http.StatusAccepted)
	s.Do("POST", adminPath(ssUUID, "install"), nil, http.StatusConflict)
	reconcileOnly()
	s.DoRawNoAuth("POST", devicePath(ssUUID, "install"), nil, http.StatusConflict)
	ackAll()
	require.Nil(t, hostProfile("ISS1"))

	s.Do("POST", adminPath(ssUUID, "install"), nil, http.StatusAccepted)
	s.Do("POST", adminPath(ssUUID, "uninstall"), nil, http.StatusConflict)
	reconcileOnly()
	s.DoRawNoAuth("POST", devicePath(ssUUID, "uninstall"), nil, http.StatusConflict)
	ackAll()
	p = hostProfile("ISS1")
	require.NotNil(t, p)
	require.Equal(t, automdm.MDMDeliveryVerifying, *p.Status)
	s.Do("POST", adminPath(ssUUID, "uninstall"), nil, http.StatusAccepted)
	reconcileAndAck()
	require.Nil(t, hostProfile("ISS1"))
	requireAvailable()
}

// The silent migration SCEP challenge variable and the SCEP challenge host secret make AutoMDM put an MDM enrollment
// challenge into a profile, so they're rejected in every way configuration profiles are uploaded.
func (s *integrationMDMTestSuite) TestAppleProfileSCEPChallengeVariablesRejected() {
	t := s.T()

	for _, c := range []struct {
		variable string
		errMsg   string
	}{
		{
			"AUTOMDM_VAR_" + string(automdm.AutoMDMVarSilentMigrationSCEPChallenge),
			"AutoMDM variable " + automdm.AutoMDMVarSilentMigrationSCEPChallenge.WithPrefix() + " is not supported in configuration profiles.",
		},
		{
			automdm.HostSecretPrefix + automdm.HostSecretSCEPChallenge,
			// single upload and batch reject it through different checks, with different messages
			automdm.HostSecretPrefix + automdm.HostSecretSCEPChallenge,
		},
	} {
		profile := mobileconfigForTest("N-"+c.variable, "I-"+c.variable, c.variable)

		body, headers := generateNewProfileMultipartRequest(t, "scep-challenge.mobileconfig", profile, s.token, nil)
		res := s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusBadRequest, headers)
		require.Contains(t, extractServerErrorText(res.Body), c.errMsg)

		res = s.Do("POST", "/api/latest/automdm/configuration_profiles/batch", batchModifyMDMConfigProfilesRequest{
			ConfigurationProfiles: []automdm.BatchModifyMDMConfigProfilePayload{{Name: "N-" + c.variable, Profile: profile}},
		}, http.StatusBadRequest)
		require.Contains(t, extractServerErrorText(res.Body), c.errMsg)

		// the endpoint GitOps applies profiles with
		res = s.Do("POST", "/api/latest/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{
			Profiles: []automdm.MDMProfileBatchPayload{{Name: "N-" + c.variable, Contents: profile}},
		}, http.StatusBadRequest)
		require.Contains(t, extractServerErrorText(res.Body), c.errMsg)
	}
}

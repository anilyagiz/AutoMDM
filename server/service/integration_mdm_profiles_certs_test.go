package service

// Certificate payload profile tests for the MDM suite.
//
// Belongs here: profiles carrying SCEP and ACME payloads — Windows device, user, and hardcoded SCEP
// profiles, accepted placements of certificate markers and variables at upload, and ACME
// certificates for user- and system-scoped profiles.
//
// Does not belong here: certificate authority integrations such as DigiCert or custom SCEP
// (integration_mdm_scep_test.go) and certificate authority CRUD
// (integration_certificate_authorities_test.go).

import (
	"bytes"
	"context"
	"crypto/x509"
	_ "embed"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/mdm/mdmtest"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	"github.com/anilyagiz/automdm/v4/server/mdm/microsoft/syncml"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/mdm"
	scepserver "github.com/anilyagiz/automdm/v4/server/mdm/scep/server"
	mdmtesting "github.com/anilyagiz/automdm/v4/server/mdm/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/service/integrationtest/scep_server"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	micromdm "github.com/micromdm/micromdm/mdm/mdm"
	"github.com/micromdm/plist"
	"github.com/smallstep/pkcs7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/profiles/windows-device-scep.xml
var windowsDeviceSCEPProfileBytes []byte

func (s *integrationMDMTestSuite) TestWindowsDeviceSCEPProfile() {
	testWindowsSCEPProfile(s, windowsDeviceSCEPProfileBytes)
}

//go:embed testdata/profiles/windows-user-scep.xml
var windowsUserSCEPProfileBytes []byte

func (s *integrationMDMTestSuite) TestWindowsUserSCEPProfile() {
	testWindowsSCEPProfile(s, windowsUserSCEPProfileBytes)
}

func testWindowsSCEPProfile(s *integrationMDMTestSuite, windowsScepProfile []byte) {
	t := s.T()
	ctx := context.Background()
	scepServer := scep_server.StartTestSCEPServer(t)
	scepServerURL := scepServer.URL + "/scep"

	// Create windows host and enroll in MDM
	host, mdmDevice := createWindowsHostThenEnrollMDM(s.ds, s.server.URL, t)

	// verifyCommands runs a management session, drives any SCEP CSPs to completion against AutoMDM's
	// SCEP proxy, ACKs the rest of the commands with the given status, and asserts the SCEP exchange
	// succeeded (cert returned, no error). Returns the number of SCEP exchanges performed.
	verifyCommands := func(wantProfiles int, status string) int {
		cmds, err := mdmDevice.StartManagementSession()
		require.NoError(t, err)
		// profile installs + 2 protocol commands acks
		require.Len(t, cmds, wantProfiles+2)
		msgID, err := mdmDevice.GetCurrentMsgID()
		require.NoError(t, err)

		scepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		handled, scepResults, _ := mdmDevice.AppendSCEPInstallResponses(scepCtx, cmds, msgID, nil)

		atomicCmds := 0
		for _, c := range cmds {
			if c.Verb == "Atomic" {
				atomicCmds++
			}
			if _, ok := handled[c.Cmd.CmdID.Value]; ok {
				// Already ACKed by AppendSCEPInstallResponses. The status arg is intentionally ignored
				// for SCEP CSPs - the helper always sends 200, matching real Windows behavior.
				continue
			}
			cmdID := c.Cmd.CmdID.Value
			verb := c.Verb
			mdmDevice.AppendResponse(automdm.SyncMLCmd{
				XMLName: xml.Name{Local: automdm.CmdStatus},
				MsgRef:  &msgID,
				CmdRef:  &cmdID,
				Cmd:     &verb,
				Data:    &status,
				Items:   nil,
				CmdID:   automdm.CmdID{Value: uuid.NewString()},
			})
		}
		require.Equal(t, wantProfiles, atomicCmds)

		// Drain SCEP results and assert success.
		scepCount := 0
		for res := range scepResults {
			scepCount++
			require.NoError(t, res.Err, "SCEP exchange for %q failed", res.UniqueID)
			require.NotNil(t, res.Cert, "SCEP exchange for %q returned no certificate", res.UniqueID)
		}

		cmds, err = mdmDevice.SendResponse()
		require.NoError(t, err)
		// the ack of the message should be the only returned command
		require.Len(t, cmds, 1)
		return scepCount
	}

	// Upload SCEP profile with missing CA
	resp := s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
			{Name: "WindowsSCEPProfile", Contents: windowsScepProfile},
		}},
		http.StatusBadRequest)
	errMsg := extractServerErrorText(resp.Body)
	require.Contains(t, errMsg, "AutoMDM variable $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_INTEGRATION does not exist.")

	// Create Custom SCEP CA
	ca := &automdm.CertificateAuthority{
		Type:      string(automdm.CATypeCustomSCEPProxy),
		Name:      new("INTEGRATION"),
		Challenge: new("integration-test"),
		URL:       new(scepServerURL),
	}

	_, err := s.ds.NewCertificateAuthority(ctx, ca)
	require.NoError(t, err)

	// Fail on missing OU
	resp = s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
			{Name: "WindowsSCEPProfile", Contents: bytes.ReplaceAll(windowsScepProfile, []byte(automdm.AutoMDMVarSCEPRenewalID.WithPrefix()), []byte("BOGUS"))},
		}},
		http.StatusBadRequest)
	errMsg = extractServerErrorText(resp.Body)
	require.Contains(t, errMsg, "SCEP profile for custom SCEP certificate authority requires: $AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_<CA_NAME>, $AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_<CA_NAME>, and $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID variables")

	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
			{Name: "WindowsSCEPProfile", Contents: windowsScepProfile},
		}},
		http.StatusNoContent)

	// Verify host receives the profile
	s.awaitTriggerProfileSchedule(t)

	// Check that profile status is Pending
	profiles, err := s.ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
	require.NoError(t, err)
	var foundProfile bool
	for _, p := range profiles {
		if p.Name == "WindowsSCEPProfile" {
			foundProfile = true
			require.NotNil(t, p.Status)
			assert.Equal(t, automdm.MDMDeliveryPending, *p.Status)
		}
	}
	require.True(t, foundProfile, "WindowsSCEPProfile not found for host")

	scepCount := verifyCommands(1, syncml.CmdStatusOK)
	require.Equal(t, 1, scepCount, "SCEP exchange should have run exactly once")

	// The device ACKed the SCEP <Exec>, but for an AutoMDM-proxied SCEP profile that only means the exchange was
	// accepted, not that a certificate landed on the host. The profile stays "verifying" until AutoMDM observes the
	// matching certificate.
	profiles, err = s.ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
	require.NoError(t, err)
	foundProfile = false
	profileUUID := ""
	for _, p := range profiles {
		if p.Name == "WindowsSCEPProfile" {
			foundProfile = true
			profileUUID = p.ProfileUUID
			require.NotNil(t, p.Status)
			assert.Equal(t, automdm.MDMDeliveryVerifying, *p.Status)
		}
	}
	require.True(t, foundProfile, "WindowsSCEPProfile not found for host")

	// Simulate osquery reporting the issued certificate. It carries the profile's renewal-ID marker
	// (automdm-<profile_uuid>) in its OU, which AutoMDM matches back to the profile's managed-certificate row.
	sha1Sum := make([]byte, 20)
	copy(sha1Sum, profileUUID)
	require.NoError(t, s.ds.UpdateHostCertificates(ctx, host.ID, host.UUID, []*automdm.HostCertificateRecord{{
		HostID:                    host.ID,
		CommonName:                "windows-scep-cert",
		SubjectCommonName:         "windows-scep-cert",
		SubjectOrganizationalUnit: "automdm-" + profileUUID,
		SHA1Sum:                   sha1Sum,
		NotValidBefore:            time.Now().Add(-time.Hour),
		NotValidAfter:             time.Now().Add(365 * 24 * time.Hour),
		Source:                    automdm.SystemHostCertificate,
	}}, automdm.HostCertificateOriginOsquery, nil))

	// Now that AutoMDM has observed the matching certificate, the profile is verified.
	profiles, err = s.ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
	require.NoError(t, err)
	foundProfile = false
	for _, p := range profiles {
		if p.Name == "WindowsSCEPProfile" {
			foundProfile = true
			require.NotNil(t, p.Status)
			assert.Equal(t, automdm.MDMDeliveryVerified, *p.Status)
		}
	}
	require.True(t, foundProfile, "WindowsSCEPProfile not found for host")

	// Attempt simple SCEP call with GetCACaps operation to verify SCEP server is reachable
	identifier := host.UUID + "," + profileUUID + "," + "INTEGRATION"
	scepRes := s.DoRawWithHeaders("GET", apple_mdm.SCEPProxyPath+identifier+"/pkiclient.exe", nil, http.StatusOK, nil, "operation", "GetCACaps")
	body, err := io.ReadAll(scepRes.Body)
	require.NoError(t, err)
	assert.Equal(t, scepserver.DefaultCACaps, string(body))
}

// TestWindowsHardcodedSCEPProfile covers a Windows MDM SCEP profile that uses a hardcoded SCEP
// server URL and challenge instead of AutoMDM's Custom SCEP proxy variables. The test client
// should still drive the SCEP exchange against the literal URL; AutoMDM's SCEP proxy is not
// involved on this path.
func (s *integrationMDMTestSuite) TestWindowsHardcodedSCEPProfile() {
	t := s.T()
	ctx := t.Context()
	scepServer := scep_server.StartTestSCEPServer(t)
	scepServerURL := scepServer.URL + "/scep"

	fixtures := map[string][]byte{
		"./Device": windowsDeviceSCEPProfileBytes,
		"./User":   windowsUserSCEPProfileBytes,
	}
	for _, locPrefix := range []string{"./Device", "./User"} {
		t.Run(locPrefix, func(t *testing.T) {
			profileName := "HardcodedSCEPProfile" + strings.ReplaceAll(locPrefix, "/", "_")
			profile := bytes.ReplaceAll(fixtures[locPrefix],
				[]byte("$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_INTEGRATION"), []byte(scepServerURL))
			profile = bytes.ReplaceAll(profile,
				[]byte("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_INTEGRATION"), []byte("any-challenge"))
			profile = bytes.ReplaceAll(profile,
				[]byte("$AUTOMDM_VAR_SCEP_RENEWAL_ID"), []byte("hardcoded-renewal"))

			s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
				batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
					{Name: profileName, Contents: profile},
				}},
				http.StatusNoContent)

			host, mdmDevice := createWindowsHostThenEnrollMDM(s.ds, s.server.URL, t)

			s.awaitTriggerProfileSchedule(t)

			cmds, err := mdmDevice.StartManagementSession()
			require.NoError(t, err)
			msgID, err := mdmDevice.GetCurrentMsgID()
			require.NoError(t, err)

			scepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			handled, scepResults, _ := mdmDevice.AppendSCEPInstallResponses(scepCtx, cmds, msgID, nil)
			for _, c := range cmds {
				if c.Verb == automdm.CmdStatus {
					continue
				}
				if _, ok := handled[c.Cmd.CmdID.Value]; ok {
					continue
				}
				cmdID := c.Cmd.CmdID.Value
				verb := c.Verb
				status := syncml.CmdStatusOK
				mdmDevice.AppendResponse(automdm.SyncMLCmd{
					XMLName: xml.Name{Local: automdm.CmdStatus},
					MsgRef:  &msgID,
					CmdRef:  &cmdID,
					Cmd:     &verb,
					Data:    &status,
					CmdID:   automdm.CmdID{Value: uuid.NewString()},
				})
			}

			scepCount := 0
			for res := range scepResults {
				scepCount++
				require.NoError(t, res.Err, "SCEP exchange failed")
				require.NotNil(t, res.Cert)
			}
			require.Equal(t, 1, scepCount, "expected one SCEP exchange against the hardcoded URL")

			_, err = mdmDevice.SendResponse()
			require.NoError(t, err)

			profiles, err := s.ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
			require.NoError(t, err)
			var found bool
			for _, p := range profiles {
				if p.Name == profileName {
					found = true
					require.NotNil(t, p.Status)
					assert.Equal(t, automdm.MDMDeliveryVerified, *p.Status)
				}
			}
			require.True(t, found, "%s not found for host", profileName)
		})
	}
}

// TestWindowsConditionalAccessSCEPProfile covers a Windows SCEP profile written before the challenge variable existed:
// the admin writes a global enroll secret into it directly or through a secret variable. AutoMDM fills in the SCEP URL at
// delivery and tracks the certificate the way it tracks a proxied certificate authority's.
func (s *integrationMDMTestSuite) TestWindowsConditionalAccessSCEPProfile() {
	t := s.T()
	ctx := t.Context()

	require.NoError(t, s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: "first+secret="}}))
	_, _, err := s.ds.UpsertSecretVariables(ctx, []automdm.SecretVariable{
		{Name: "CONDACCESS_CHALLENGE", Value: "first+secret="},
		{Name: "BAD_CHALLENGE", Value: "bad_secret"},
	})
	require.NoError(t, err)
	withChallenge := func(challenge string) []byte {
		profile := bytes.ReplaceAll(windowsUserSCEPProfileBytes,
			[]byte("$AUTOMDM_VAR_CUSTOM_SCEP_PROXY_URL_INTEGRATION"), []byte(automdm.AutoMDMVarConditionalAccessSCEPURL.WithPrefix()))
		return bytes.ReplaceAll(profile, []byte("$AUTOMDM_VAR_CUSTOM_SCEP_CHALLENGE_INTEGRATION"), []byte(challenge))
	}

	// A challenge Windows can't send is rejected at upload, secret variables included.
	resp := s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "ConditionalAccessSCEP", Contents: withChallenge("$AUTOMDM_SECRET_BAD_CHALLENGE")},
	}}, http.StatusBadRequest)
	require.Contains(t, extractServerErrorText(resp.Body), conditionalAccessChallengeInvalidMsg)

	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "ConditionalAccessSCEP", Contents: withChallenge("$AUTOMDM_SECRET_CONDACCESS_CHALLENGE")},
	}}, http.StatusNoContent)

	appConfig, err := s.ds.AppConfig(ctx)
	require.NoError(t, err)
	scepURL, err := url.JoinPath(appConfig.ServerSettings.ServerURL, "/api/automdm/conditional_access/scep")
	require.NoError(t, err)

	host, mdmDevice := createWindowsHostThenEnrollMDM(s.ds, s.server.URL, t)

	hostProfile := func() *automdm.HostMDMWindowsProfile {
		profiles, err := s.ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
		require.NoError(t, err)
		for _, p := range profiles {
			if p.Name == "ConditionalAccessSCEP" {
				return &p
			}
		}
		require.FailNow(t, "ConditionalAccessSCEP not found for host")
		return nil
	}
	rawCommand := func(cmdUUID string) string {
		var raw string
		mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
			return sqlx.GetContext(ctx, q, &raw, `SELECT raw_command FROM windows_mdm_commands WHERE command_uuid = ?`, cmdUUID)
		})
		return raw
	}
	// Acknowledges every pending command and returns the challenge in the one SCEP profile the device received.
	ackCommands := func() string {
		cmds, err := mdmDevice.StartManagementSession()
		require.NoError(t, err)
		sceps, incomplete := mdmDevice.ExtractSCEPCommands(cmds)
		require.Empty(t, incomplete)
		require.Len(t, sceps, 1)
		ackWindowsCmds(t, mdmDevice, cmds)
		return sceps[0].Challenge
	}
	// Queues the profile for another delivery, the way a resend or the renewal job does.
	queueRedelivery := func(profileUUID string) {
		require.NoError(t, s.ds.ResendHostMDMProfile(ctx, host.UUID, profileUUID))
	}

	s.awaitTriggerProfileSchedule(t)
	p := hostProfile()
	require.NotNil(t, p.Status)
	require.Equal(t, automdm.MDMDeliveryPending, *p.Status)
	raw := rawCommand(p.CommandUUID)
	require.Contains(t, raw, "<Data>"+scepURL+"</Data>")
	// A secret variable is only expanded when the command is sent, so the stored command never holds the secret.
	require.Contains(t, raw, "<Data>$AUTOMDM_SECRET_CONDACCESS_CHALLENGE</Data>")
	require.NotContains(t, raw, "first+secret=")

	managedCert, err := s.ds.GetWindowsHostMDMCertificateProfile(ctx, host.UUID, p.ProfileUUID, automdm.ConditionalAccessManagedCertificateCAName)
	require.NoError(t, err)
	require.NotNil(t, managedCert)
	require.Equal(t, automdm.CAConfigConditionalAccess, managedCert.Type)
	require.NotNil(t, managedCert.ChallengeRetrievedAt)

	// Windows acknowledges the Exec before it contacts the SCEP server, so the ack alone doesn't verify the profile.
	require.Equal(t, "first+secret=", ackCommands())
	p = hostProfile()
	require.NotNil(t, p.Status)
	require.Equal(t, automdm.MDMDeliveryVerifying, *p.Status)

	// After the enroll secret and the secret variable are rotated, a redelivery carries the new secret without re-uploading
	// the profile.
	require.NoError(t, s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: "second-secret"}}))
	_, _, err = s.ds.UpsertSecretVariables(ctx, []automdm.SecretVariable{{Name: "CONDACCESS_CHALLENGE", Value: "second-secret"}})
	require.NoError(t, err)
	queueRedelivery(p.ProfileUUID)
	s.awaitTriggerProfileSchedule(t)
	p = hostProfile()
	raw = rawCommand(p.CommandUUID)
	require.NotContains(t, raw, "second-secret")
	require.Equal(t, "second-secret", ackCommands())
}

// TestWindowsConditionalAccessRenderedProfiles uploads both rendered Windows conditional access profiles unchanged and
// enrolls the host's certificate against AutoMDM's conditional access SCEP server with a one-time challenge.
func (s *integrationMDMTestSuite) TestWindowsConditionalAccessRenderedProfiles() {
	t := s.T()
	ctx := t.Context()

	var profiles automdm.ConditionalAccessWindowsProfiles
	s.DoJSON("GET", "/api/latest/automdm/conditional_access/idp/windows/profiles", nil, http.StatusOK, &profiles)

	tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name()})
	require.NoError(t, err)
	for fileName, contents := range map[string]string{
		"OktaSCEP.xml":     profiles.SCEPProfile,
		"OktaBrowsers.xml": profiles.BrowserProfile,
	} {
		body, headers := generateNewProfileMultipartRequest(t, fileName, []byte(contents), s.token,
			map[string][]string{"team_id": {fmt.Sprint(tm.ID)}})
		s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusOK, headers)
	}

	host, mdmDevice := createWindowsHostThenEnrollMDM(s.ds, s.server.URL, t)
	require.NoError(t, s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&tm.ID, []uint{host.ID})))
	s.awaitTriggerProfileSchedule(t)

	// The stored command only holds a placeholder; the challenge is created when the command is sent.
	scepProfileRow := func() automdm.HostMDMWindowsProfile {
		hostProfiles, err := s.ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
		require.NoError(t, err)
		for _, p := range hostProfiles {
			if p.Name == "OktaSCEP" {
				return p
			}
		}
		require.FailNow(t, "OktaSCEP not found for host")
		return automdm.HostMDMWindowsProfile{}
	}
	var raw string
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		return sqlx.GetContext(ctx, q, &raw, `SELECT raw_command FROM windows_mdm_commands WHERE command_uuid = ?`, scepProfileRow().CommandUUID)
	})
	require.Contains(t, raw, "<Data>"+automdm.HostSecretPlaceholder(automdm.HostSecretConditionalAccessSCEPChallenge)+"</Data>")

	cmds, err := mdmDevice.StartManagementSession()
	require.NoError(t, err)
	msgID, err := mdmDevice.GetCurrentMsgID()
	require.NoError(t, err)
	scepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	handled, scepResults, _ := mdmDevice.AppendSCEPInstallResponses(scepCtx, cmds, msgID, nil)
	for _, c := range cmds {
		if c.Verb == automdm.CmdStatus {
			continue
		}
		if _, ok := handled[c.Cmd.CmdID.Value]; ok {
			continue
		}
		cmdID, verb, status := c.Cmd.CmdID.Value, c.Verb, syncml.CmdStatusOK
		mdmDevice.AppendResponse(automdm.SyncMLCmd{
			XMLName: xml.Name{Local: automdm.CmdStatus},
			MsgRef:  &msgID,
			CmdRef:  &cmdID,
			Cmd:     &verb,
			Data:    &status,
			CmdID:   automdm.CmdID{Value: uuid.NewString()},
		})
	}
	var certs []*x509.Certificate
	for res := range scepResults {
		require.NoError(t, res.Err, "SCEP exchange failed")
		certs = append(certs, res.Cert)
	}
	require.Len(t, certs, 1)
	_, err = mdmDevice.SendResponse()
	require.NoError(t, err)

	hostProfiles, err := s.ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
	require.NoError(t, err)
	statuses := map[string]automdm.MDMDeliveryStatus{}
	var scepProfileUUID string
	for _, p := range hostProfiles {
		require.NotNil(t, p.Status, p.Name)
		statuses[p.Name] = *p.Status
		if p.Name == "OktaSCEP" {
			scepProfileUUID = p.ProfileUUID
		}
	}
	require.Equal(t, automdm.MDMDeliveryVerifying, statuses["OktaSCEP"])
	require.Equal(t, automdm.MDMDeliveryVerified, statuses["OktaBrowsers"])

	cert := certs[0]
	require.Equal(t, automdm.ConditionalAccessOktaCertificateCN, cert.Subject.CommonName)
	require.Equal(t, []string{"automdm-" + scepProfileUUID}, cert.Subject.OrganizationalUnit)
	require.Len(t, cert.URIs, 1)
	require.Equal(t, "urn:device:automdm:uuid:"+host.UUID, cert.URIs[0].String())
	hostID, err := s.ds.GetConditionalAccessCertHostIDBySerialNumber(ctx, uint64(cert.SerialNumber.Int64())) //nolint:gosec // G115
	require.NoError(t, err)
	require.Equal(t, host.ID, hostID)

	// The enrollment used the host's one-time challenge, so a redelivery, as for renewal, carries a new one.
	var consumed []string
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		return sqlx.SelectContext(ctx, q, &consumed, `SELECT challenge FROM mdm_apple_scep_challenges
			WHERE host_uuid = ? AND purpose = ? AND consumed_at IS NOT NULL`, host.UUID, automdm.ConditionalAccessSCEPChallengePurpose)
	})
	require.Len(t, consumed, 1)
	require.NoError(t, s.ds.ResendHostMDMProfile(ctx, host.UUID, scepProfileUUID))
	s.awaitTriggerProfileSchedule(t)
	cmds, err = mdmDevice.StartManagementSession()
	require.NoError(t, err)
	sceps, incomplete := mdmDevice.ExtractSCEPCommands(cmds)
	require.Empty(t, incomplete)
	require.Len(t, sceps, 1)
	require.Regexp(t, `^[0-9a-f]{64}$`, sceps[0].Challenge)
	require.NotEqual(t, consumed[0], sceps[0].Challenge)
	ackWindowsCmds(t, mdmDevice, cmds)
}

// #40639 — integration coverage for non-proxied cert renewal
// profile uploads. The renewal-ID marker is opt-in; ACME and non-proxied
// SCEP profiles upload regardless of marker presence or placement. Auto-
// renewal activates when the marker is in OU; otherwise the profile
// uploads and works without renewal.

const acmeProfileTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key><string>com.apple.security.acme</string>
			<key>PayloadIdentifier</key><string>com.fleetdm.test.acme.payload</string>
			<key>PayloadUUID</key><string>11111111-2222-3333-4444-555555555555</string>
			<key>PayloadVersion</key><integer>1</integer>
			<key>PayloadDisplayName</key><string>ACME Cert</string>
			<key>DirectoryURL</key><string>https://acme.example.com/directory</string>
			<key>Subject</key>
			<array>
				<array><array><string>CN</string><string>%s</string></array></array>
				<array><array><string>OU</string><string>%s</string></array></array>
			</array>
		</dict>
	</array>
	<key>PayloadDisplayName</key><string>ACME Profile</string>
	<key>PayloadIdentifier</key><string>com.fleetdm.test.profile.acme</string>
	<key>PayloadType</key><string>Configuration</string>
	<key>PayloadUUID</key><string>aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee</string>
	<key>PayloadVersion</key><integer>1</integer>
</dict>
</plist>`

const rawSCEPProfileTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key><string>com.apple.security.scep</string>
			<key>PayloadIdentifier</key><string>com.fleetdm.test.scep.payload</string>
			<key>PayloadUUID</key><string>22222222-3333-4444-5555-666666666666</string>
			<key>PayloadVersion</key><integer>1</integer>
			<key>PayloadDisplayName</key><string>Raw SCEP Cert</string>
			<key>PayloadContent</key>
			<dict>
				<key>Challenge</key><string>static-challenge-value</string>
				<key>URL</key><string>https://scep.example.com/scep</string>
				<key>Subject</key>
				<array>
					<array><array><string>CN</string><string>%s</string></array></array>
					<array><array><string>OU</string><string>%s</string></array></array>
				</array>
			</dict>
		</dict>
	</array>
	<key>PayloadDisplayName</key><string>Raw SCEP Profile</string>
	<key>PayloadIdentifier</key><string>com.fleetdm.test.profile.rawscep</string>
	<key>PayloadType</key><string>Configuration</string>
	<key>PayloadUUID</key><string>bbbbbbbb-cccc-dddd-eeee-ffffffffffff</string>
	<key>PayloadVersion</key><integer>1</integer>
</dict>
</plist>`

// TestACMEProfileUploadAcceptsAllMarkerPlacements confirms upload succeeds
// across all combinations of variable name and Subject placement.
func (s *integrationMDMTestSuite) TestACMEProfileUploadAcceptsAllMarkerPlacements() {
	t := s.T()

	uploadACME := func(name, cn, ou string) {
		profile := fmt.Sprintf(acmeProfileTemplate, cn, ou)
		s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
			batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
				{Name: name, Contents: []byte(profile)},
			}},
			http.StatusNoContent)
	}

	t.Run("preferred marker in OU (auto-renewal active)", func(t *testing.T) {
		uploadACME("acme-ou-preferred", "static-cn", "$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID")
	})
	t.Run("legacy marker in OU", func(t *testing.T) {
		uploadACME("acme-ou-legacy", "static-cn", "$AUTOMDM_VAR_SCEP_RENEWAL_ID")
	})
	t.Run("preferred marker in CN", func(t *testing.T) {
		uploadACME("acme-cn-preferred", "$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", "static-ou")
	})
	t.Run("no marker (opt-out; no auto-renewal but still uploads)", func(t *testing.T) {
		uploadACME("acme-no-marker", "static-cn", "static-ou")
	})
}

// TestRawSCEPProfileUploadAcceptsAllMarkerPlacements mirrors the ACME
// matrix for the com.apple.security.scep payload type.
func (s *integrationMDMTestSuite) TestRawSCEPProfileUploadAcceptsAllMarkerPlacements() {
	t := s.T()

	uploadRawSCEP := func(name, cn, ou string) {
		profile := fmt.Sprintf(rawSCEPProfileTemplate, cn, ou)
		s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
			batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
				{Name: name, Contents: []byte(profile)},
			}},
			http.StatusNoContent)
	}

	t.Run("preferred marker in OU (auto-renewal active)", func(t *testing.T) {
		uploadRawSCEP("rawscep-ou-preferred", "static-cn", "$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID")
	})
	t.Run("legacy marker in OU", func(t *testing.T) {
		uploadRawSCEP("rawscep-ou-legacy", "static-cn", "$AUTOMDM_VAR_SCEP_RENEWAL_ID")
	})
	t.Run("preferred marker in CN", func(t *testing.T) {
		uploadRawSCEP("rawscep-cn-preferred", "$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID", "static-ou")
	})
	t.Run("no marker (opt-out; no auto-renewal but still uploads)", func(t *testing.T) {
		uploadRawSCEP("rawscep-no-marker", "static-cn", "static-ou")
	})
}

// TestConditionalAccessAppleProfileOneTimeChallenge uploads the Okta profile as copied from AutoMDM and delivers it on
// the user channel: the stored command holds only a placeholder, and the device gets a one-time challenge bound to its
// UUID, the one the profile's SAN carries.
func (s *integrationMDMTestSuite) TestConditionalAccessAppleProfileOneTimeChallenge() {
	t := s.T()
	ctx := t.Context()

	require.NoError(t, s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: t.Name()}}))
	host, mdmDevice := s.enrollHostDrainInitialProfiles(t)
	require.NoError(t, mdmDevice.UserEnroll())

	resp := s.DoRaw("GET", "/api/latest/automdm/conditional_access/idp/apple/profile", nil, http.StatusOK)
	profile, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	// The single-profile endpoint is the one the custom OS settings UI uses when an admin uploads the profile.
	body, headers := generateNewProfileMultipartRequest(t, "ConditionalAccessProfile.mobileconfig", profile, s.token, nil)
	s.DoRawWithHeaders("POST", "/api/latest/automdm/configuration_profiles", body.Bytes(), http.StatusOK, headers)
	s.awaitTriggerProfileSchedule(t)

	ok, cmdUUID := hostHasAppleProfileOp(t, s.ds, host.UUID, automdm.ConditionalAccessOktaProfileIdentifier, automdm.MDMOperationTypeInstall)
	require.True(t, ok)
	appConfig, err := s.ds.AppConfig(ctx)
	require.NoError(t, err)
	scepURL, err := url.JoinPath(appConfig.ServerSettings.ServerURL, automdm.ConditionalAccessSCEPPath)
	require.NoError(t, err)
	var stored string
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		return sqlx.GetContext(ctx, q, &stored, `SELECT command FROM nano_commands WHERE command_uuid = ?`, cmdUUID)
	})
	require.Contains(t, stored, "<string>"+automdm.HostSecretPlaceholder(automdm.HostSecretConditionalAccessSCEPChallenge)+"</string>")
	require.Contains(t, stored, "<string>"+scepURL+"</string>")
	require.NotContains(t, stored, "AUTOMDM_VAR_CONDITIONAL_ACCESS")

	cmd, err := mdmDevice.UserIdle()
	require.NoError(t, err)
	for cmd != nil && cmd.CommandUUID != cmdUUID {
		cmd, err = mdmDevice.UserAcknowledge(cmd.CommandUUID)
		require.NoError(t, err)
	}
	require.NotNil(t, cmd, "the Okta profile must be delivered on the user channel")
	var fullCmd micromdm.CommandPayload
	require.NoError(t, plist.Unmarshal(cmd.Raw, &fullCmd))
	p7, err := pkcs7.Parse(fullCmd.Command.InstallProfile.Payload)
	require.NoError(t, err)
	var delivered struct {
		PayloadContent []map[string]any `plist:"PayloadContent"`
	}
	require.NoError(t, plist.Unmarshal(p7.Content, &delivered))
	var challenge string
	for _, payload := range delivered.PayloadContent {
		if payload["PayloadType"] == "com.apple.security.scep" {
			scep, ok := payload["PayloadContent"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, scepURL, scep["URL"])
			challenge, _ = scep["Challenge"].(string)
		}
	}
	require.Regexp(t, `^[0-9a-f]{64}$`, challenge)
	_, err = mdmDevice.UserAcknowledge(cmd.CommandUUID)
	require.NoError(t, err)

	err = s.ds.ConsumeConditionalAccessSCEPChallenge(ctx, challenge, "another-host-uuid")
	require.True(t, automdm.IsNotFound(err), err)
	require.NoError(t, s.ds.ConsumeConditionalAccessSCEPChallenge(ctx, challenge, host.UUID))
}

// TestWindowsSCEPProfilePreferredVariableAccepted exercises the
// Windows validator on a pre-existing surface: NDES / Custom SCEP
// proxy validators accept both legacy $AUTOMDM_VAR_SCEP_RENEWAL_ID and
// preferred $AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID. The existing
// TestWindowsDeviceSCEPProfile covers the legacy spelling.
func (s *integrationMDMTestSuite) TestWindowsSCEPProfilePreferredVariableAccepted() {
	t := s.T()
	ctx := context.Background()

	scepServer := scep_server.StartTestSCEPServer(t)
	_, err := s.ds.NewCertificateAuthority(ctx, &automdm.CertificateAuthority{
		Type:      string(automdm.CATypeCustomSCEPProxy),
		Name:      new("INTEGRATION"),
		Challenge: new("integration-test"),
		URL:       new(scepServer.URL + "/scep"),
	})
	require.NoError(t, err)

	// Guard against the fixture drifting: if the legacy token vanishes from
	// the embedded XML, ReplaceAll silently no-ops and we'd test the legacy
	// path twice instead of the preferred one.
	require.True(t,
		bytes.Contains(windowsDeviceSCEPProfileBytes, []byte("$AUTOMDM_VAR_SCEP_RENEWAL_ID")),
		"fixture must contain $AUTOMDM_VAR_SCEP_RENEWAL_ID for the swap to test the preferred name",
	)
	preferred := bytes.ReplaceAll(
		windowsDeviceSCEPProfileBytes,
		[]byte("$AUTOMDM_VAR_SCEP_RENEWAL_ID"),
		[]byte("$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID"),
	)
	require.True(t,
		bytes.Contains(preferred, []byte("$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID")),
		"preferred-name swap did not produce the expected token",
	)
	require.False(t,
		bytes.Contains(preferred, []byte("$AUTOMDM_VAR_SCEP_RENEWAL_ID")),
		"legacy token must be fully replaced",
	)

	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
			{Name: "WindowsSCEPProfilePreferred", Contents: preferred},
		}},
		http.StatusNoContent)
}

// acmeProfileForScope builds a profile with a single ACME payload in the given scope.
func acmeProfileForScope(ident string, scope automdm.PayloadScope) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key><string>com.apple.security.acme</string>
			<key>PayloadIdentifier</key><string>%[1]s.payload</string>
			<key>PayloadUUID</key><string>%[2]s</string>
			<key>PayloadVersion</key><integer>1</integer>
			<key>PayloadDisplayName</key><string>ACME payload</string>
			<key>DirectoryURL</key><string>https://acme.example.com/directory</string>
			<key>Subject</key>
			<array>
				<array><array><string>CN</string><string>test-device</string></array></array>
			</array>
		</dict>
	</array>
	<key>PayloadDisplayName</key><string>%[1]s</string>
	<key>PayloadIdentifier</key><string>%[1]s</string>
	<key>PayloadType</key><string>Configuration</string>
	<key>PayloadUUID</key><string>%[3]s</string>
	<key>PayloadVersion</key><integer>1</integer>
	<key>PayloadScope</key><string>%[4]s</string>
</dict>
</plist>`, ident, uuid.NewString(), uuid.NewString(), scope))
}

// acmeCertTemplate returns a cert template identifiable by common name.
func acmeCertTemplate(commonName string) *x509.Certificate {
	tmpl := mdmtesting.NewTestMDMAppleCertTemplate()
	tmpl.SerialNumber = big.NewInt(int64(len(commonName)) + 1000)
	tmpl.Subject.CommonName = commonName
	return tmpl
}

// ackProfileInstall drains the channel until it acknowledges wantCmdUUID,
// returning the next command the server handed back.
func ackProfileInstall(t *testing.T, d *mdmtest.TestAppleMDMClient, userChannel bool, wantCmdUUID string) *mdm.Command {
	idle, ack := d.Idle, d.Acknowledge
	if userChannel {
		idle, ack = d.UserIdle, d.UserAcknowledge
	}
	cmd, err := idle()
	require.NoError(t, err)
	for cmd != nil {
		isTarget := cmd.CommandUUID == wantCmdUUID
		next, err := ack(cmd.CommandUUID)
		require.NoError(t, err)
		if isTarget {
			return next
		}
		cmd = next
	}
	require.FailNow(t, "profile command was never delivered", "command uuid %s", wantCmdUUID)
	return nil
}

// nanoCertificateListUUIDs returns the CertificateList commands queued against one
// nano enrollment, telling a device-channel refetch apart from a user-channel one.
func nanoCertificateListUUIDs(t *testing.T, ds *mysql.Datastore, enrollmentID string) []string {
	var uuids []string
	mysqltest.ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
		return sqlx.SelectContext(context.Background(), q, &uuids,
			`SELECT neq.command_uuid
			 FROM nano_enrollment_queue neq
			 JOIN nano_commands nc ON neq.command_uuid = nc.command_uuid
			 WHERE nc.request_type = 'CertificateList' AND neq.id = ?
			 ORDER BY neq.created_at`, enrollmentID)
	})
	return uuids
}

// hostCertificateScopes maps common name to reported scope, per the host details API.
func (s *integrationMDMTestSuite) hostCertificateScopes(t *testing.T, hostID uint) map[string]string {
	var resp listHostCertificatesResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d/certificates", hostID), nil, http.StatusOK, &resp)
	scopes := make(map[string]string, len(resp.Certificates))
	for _, c := range resp.Certificates {
		scope := string(c.Source)
		if c.Username != "" {
			scope += ":" + c.Username
		}
		scopes[c.CommonName] = scope
	}
	return scopes
}

// TestACMECertForUserChannelProfile covers an ACME cert from a user-scoped
// profile on macOS: it lands in the user's login keychain, which the device
// channel can't see, so the refetch has to go out on the user channel and the
// cert has to be recorded under the user's scope.
func (s *integrationMDMTestSuite) TestACMECertForUserChannelProfile() {
	t := s.T()
	ctx := t.Context()

	// Enroll a macOS device and drain the initial (automdmd/CA) profiles.
	require.NoError(t, s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: t.Name()}}))
	host, mdmDevice := s.enrollHostDrainInitialProfiles(t)

	// User-channel enrollment, as DEP-enrolled macOS devices do automatically.
	require.NoError(t, mdmDevice.UserEnroll())
	userEnr, err := s.ds.GetNanoMDMUserEnrollment(ctx, host.UUID)
	require.NoError(t, err)
	require.NotNil(t, userEnr, "user enrollment must exist after UserEnroll")

	// Upload a user-scoped ACME profile.
	const profIdent = "com.fleetdm.test.profile.acme.wifi"
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch", batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{
		{Name: "ACME-WiFi", Contents: acmeProfileForScope(profIdent, automdm.PayloadScopeUser)},
	}}, http.StatusNoContent)

	// Trigger profile reconciliation so the cron picks up the new profile.
	s.awaitTriggerProfileSchedule(t)

	// Verify the profile row was created with scope=User.
	ok, cmdUUID := hostHasAppleProfileOp(t, s.ds, host.UUID, profIdent, automdm.MDMOperationTypeInstall)
	require.True(t, ok, "ACME profile should be pending install")
	require.NotEmpty(t, cmdUUID)

	// Verify the install command is queued to the user enrollment.
	var queuedEnrollmentID string
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		return sqlx.GetContext(ctx, q, &queuedEnrollmentID,
			`SELECT id FROM nano_enrollment_queue WHERE command_uuid = ?`, cmdUUID)
	})
	require.Equal(t, userEnr.ID, queuedEnrollmentID, "install command must be queued to user enrollment")

	// Acking the install on the user channel fires the certificate refetch.
	ackProfileInstall(t, mdmDevice, true, cmdUUID)

	// --- The refetch must target the user channel, not the device channel ---
	require.Empty(t, nanoCertificateListUUIDs(t, s.ds, host.UUID),
		"no CertificateList may be queued to the device channel: it only reports the system keychain")
	userCertListUUIDs := nanoCertificateListUUIDs(t, s.ds, userEnr.ID)
	require.Len(t, userCertListUUIDs, 1,
		"exactly one CertificateList must be queued to the user channel")

	// --- Answer on the user channel with the login keychain's contents ---
	cmd, err := mdmDevice.UserIdle()
	require.NoError(t, err)
	for cmd != nil && cmd.Command.RequestType != "CertificateList" {
		cmd, err = mdmDevice.UserAcknowledge(cmd.CommandUUID)
		require.NoError(t, err)
	}
	require.NotNil(t, cmd, "CertificateList must be delivered on user channel")
	require.Equal(t, userCertListUUIDs[0], cmd.CommandUUID)

	const acmeCN = "acme-user-keychain-cert"
	_, err = mdmDevice.AcknowledgeUserCertificateList(cmd.CommandUUID,
		[]*x509.Certificate{acmeCertTemplate(acmeCN)})
	require.NoError(t, err)

	// --- The cert shows up, attributed to the enrolled user's keychain ---
	require.Equal(t, "user:"+mdmDevice.Username, s.hostCertificateScopes(t, host.ID)[acmeCN],
		"ACME cert must show under the enrolled user's scope, not the system keychain")

	// Cleanup must run against the answering enrollment, or the user channel's
	// refetch rows are never reclaimed.
	var trackedCommands []automdm.HostMDMCommand
	trackedCommands, err = s.ds.GetHostMDMCommands(ctx, host.ID)
	require.NoError(t, err)
	require.NotContains(t, trackedCommands,
		automdm.HostMDMCommand{HostID: host.ID, CommandType: automdm.RefetchCertsCommandUUIDPrefix},
		"refetch certs tracking row must be cleared once the user channel answers")
}

// TestACMECertUserAndSystemScopedProfilesCoexist covers a host with ACME profiles
// in both scopes: each channel reports only its own keychain, so neither refetch
// may clear the other's cert — removals included.
func (s *integrationMDMTestSuite) TestACMECertUserAndSystemScopedProfilesCoexist() {
	t := s.T()
	ctx := t.Context()

	require.NoError(t, s.ds.ApplyEnrollSecrets(ctx, nil, []*automdm.EnrollSecret{{Secret: t.Name()}}))
	host, mdmDevice := s.enrollHostDrainInitialProfiles(t)
	require.NoError(t, mdmDevice.UserEnroll())
	userEnr, err := s.ds.GetNanoMDMUserEnrollment(ctx, host.UUID)
	require.NoError(t, err)
	require.NotNil(t, userEnr)

	const (
		userProfIdent = "com.fleetdm.test.profile.acme.user"
		sysProfIdent  = "com.fleetdm.test.profile.acme.system"
		userCN        = "acme-user-keychain-cert"
		sysCN         = "acme-system-keychain-cert"
	)
	userProfile := automdm.MDMProfileBatchPayload{Name: "ACME-User", Contents: acmeProfileForScope(userProfIdent, automdm.PayloadScopeUser)}
	sysProfile := automdm.MDMProfileBatchPayload{Name: "ACME-System", Contents: acmeProfileForScope(sysProfIdent, automdm.PayloadScopeSystem)}

	// --- Install the user-scoped profile and ingest its login keychain cert ---
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{userProfile}}, http.StatusNoContent)
	s.awaitTriggerProfileSchedule(t)
	ok, userCmdUUID := hostHasAppleProfileOp(t, s.ds, host.UUID, userProfIdent, automdm.MDMOperationTypeInstall)
	require.True(t, ok)
	ackProfileInstall(t, mdmDevice, true, userCmdUUID)

	cmd, err := mdmDevice.UserIdle()
	require.NoError(t, err)
	for cmd != nil && cmd.Command.RequestType != "CertificateList" {
		cmd, err = mdmDevice.UserAcknowledge(cmd.CommandUUID)
		require.NoError(t, err)
	}
	require.NotNil(t, cmd, "CertificateList must be delivered on user channel")
	_, err = mdmDevice.AcknowledgeUserCertificateList(cmd.CommandUUID, []*x509.Certificate{acmeCertTemplate(userCN)})
	require.NoError(t, err)
	require.Equal(t, "user:"+mdmDevice.Username, s.hostCertificateScopes(t, host.ID)[userCN])

	// --- Install the system-scoped profile and ingest its system keychain cert ---
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{userProfile, sysProfile}}, http.StatusNoContent)
	s.awaitTriggerProfileSchedule(t)
	ok, sysCmdUUID := hostHasAppleProfileOp(t, s.ds, host.UUID, sysProfIdent, automdm.MDMOperationTypeInstall)
	require.True(t, ok)
	ackProfileInstall(t, mdmDevice, false, sysCmdUUID)

	deviceCertListUUIDs := nanoCertificateListUUIDs(t, s.ds, host.UUID)
	require.Len(t, deviceCertListUUIDs, 1, "system-scoped ACME profile must refetch on the device channel")

	cmd, err = mdmDevice.Idle()
	require.NoError(t, err)
	for cmd != nil && cmd.Command.RequestType != "CertificateList" {
		cmd, err = mdmDevice.Acknowledge(cmd.CommandUUID)
		require.NoError(t, err)
	}
	require.NotNil(t, cmd, "CertificateList must be delivered on device channel")
	// The device channel reports the system keychain only.
	_, err = mdmDevice.AcknowledgeCertificateList(mdmDevice.UUID, cmd.CommandUUID, []*x509.Certificate{acmeCertTemplate(sysCN)})
	require.NoError(t, err)

	scopes := s.hostCertificateScopes(t, host.ID)
	require.Equal(t, "system", scopes[sysCN], "system keychain cert must be ingested")
	require.Equal(t, "user:"+mdmDevice.Username, scopes[userCN],
		"device-channel refetch must not clear the login keychain cert it cannot see")

	// --- Removing the user-scoped profile clears only its own cert ---
	s.Do("POST", "/api/v1/automdm/mdm/profiles/batch",
		batchSetMDMProfilesRequest{Profiles: []automdm.MDMProfileBatchPayload{sysProfile}}, http.StatusNoContent)
	s.awaitTriggerProfileSchedule(t)
	ok, removeCmdUUID := hostHasAppleProfileOp(t, s.ds, host.UUID, userProfIdent, automdm.MDMOperationTypeRemove)
	require.True(t, ok, "user-scoped profile should be pending removal")
	ackProfileInstall(t, mdmDevice, true, removeCmdUUID)

	cmd, err = mdmDevice.UserIdle()
	require.NoError(t, err)
	for cmd != nil && cmd.Command.RequestType != "CertificateList" {
		cmd, err = mdmDevice.UserAcknowledge(cmd.CommandUUID)
		require.NoError(t, err)
	}
	require.NotNil(t, cmd, "removal must refetch on the user channel")
	// The login keychain no longer holds the cert.
	_, err = mdmDevice.AcknowledgeUserCertificateList(cmd.CommandUUID, nil)
	require.NoError(t, err)

	scopes = s.hostCertificateScopes(t, host.ID)
	require.NotContains(t, scopes, userCN, "removed profile's user keychain cert must be cleared")
	require.Equal(t, "system", scopes[sysCN],
		"user-channel refetch must not clear the system keychain cert it cannot see")
}

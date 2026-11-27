package apple_mdm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/ee/server/service/digicert"
	"github.com/anilyagiz/automdm/v4/ee/server/service/scep"
	"github.com/anilyagiz/automdm/v4/pkg/removednames"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/mock"
	digicert_mock "github.com/anilyagiz/automdm/v4/server/mock/digicert"
	ejbca_mock "github.com/anilyagiz/automdm/v4/server/mock/ejbca"
	scep_mock "github.com/anilyagiz/automdm/v4/server/mock/scep"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noEJBCAService and noDigiCertService are for the tests that don't use the CA, which fail if it is called.
var (
	noEJBCAService    automdm.EJBCAService    = &ejbca_mock.Service{}
	noDigiCertService automdm.DigiCertService = &digicert_mock.Service{}
)

func TestPreprocessProfileContents(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true
	ds := new(mock.Store)

	// No-op
	svc := scep.NewSCEPConfigService(logger, nil)
	digiCertService := digicert.NewService(digicert.WithLogger(logger))
	err := preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, nil, nil, nil, nil, nil)
	require.NoError(t, err)

	hostUUID := "host-1"
	cmdUUID := "cmd-1"
	var targets map[string]*automdm.CmdTarget
	populateTargets := func() {
		targets = map[string]*automdm.CmdTarget{
			"p1": {CmdUUID: cmdUUID, ProfileIdentifier: "com.add.profile", EnrollmentIDs: []string{hostUUID}},
		}
	}
	hostProfilesToInstallMap := make(map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload, 1)
	hostProfilesToInstallMap[automdm.HostProfileUUID{HostUUID: hostUUID, ProfileUUID: "p1"}] = &automdm.MDMAppleBulkUpsertHostProfilePayload{
		ProfileUUID:       "p1",
		ProfileIdentifier: "com.add.profile",
		HostUUID:          hostUUID,
		OperationType:     automdm.MDMOperationTypeInstall,
		Status:            &automdm.MDMDeliveryPending,
		CommandUUID:       cmdUUID,
		Scope:             automdm.PayloadScopeSystem,
	}
	userEnrollmentsToHostUUIDsMap := make(map[string]string)
	populateTargets()
	profileContents := map[string]mobileconfig.Mobileconfig{
		"p1": []byte("$AUTOMDM_VAR_" + automdm.AutoMDMVarNDESSCEPProxyURL),
	}

	var updatedPayload *automdm.MDMAppleBulkUpsertHostProfilePayload
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		require.Len(t, payload, 1)
		updatedPayload = payload[0]
		for _, p := range payload {
			require.NotNil(t, p.Status)
			assert.Equal(t, automdm.MDMDeliveryFailed, *p.Status)
			assert.Equal(t, cmdUUID, p.CommandUUID)
			assert.Equal(t, hostUUID, p.HostUUID)
			assert.Equal(t, automdm.MDMOperationTypeInstall, p.OperationType)
			assert.Equal(t, automdm.PayloadScopeSystem, p.Scope)
		}
		return nil
	}
	// Can't use NDES SCEP proxy with free tier
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierFree})
	err = preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, nil)
	require.NoError(t, err)
	require.NotNil(t, updatedPayload)
	assert.Contains(t, updatedPayload.Detail, "Premium license")
	assert.Empty(t, targets)

	// Can't use NDES SCEP proxy without it being configured
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	updatedPayload = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, &automdm.GroupedCertificateAuthorities{})
	require.NoError(t, err)
	require.NotNil(t, updatedPayload)
	assert.Contains(t, updatedPayload.Detail, "not configured")
	assert.NotNil(t, updatedPayload.VariablesUpdatedAt)
	assert.Empty(t, targets)

	// Unknown variable
	profileContents = map[string]mobileconfig.Mobileconfig{
		"p1": []byte("$AUTOMDM_VAR_BOZO"),
	}
	updatedPayload = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, nil)
	require.NoError(t, err)
	require.NotNil(t, updatedPayload)
	assert.Contains(t, updatedPayload.Detail, "AUTOMDM_VAR_BOZO")
	assert.Empty(t, targets)

	// A profile saved before the upgrade that still uses a removed variable
	profileContents = map[string]mobileconfig.Mobileconfig{
		"p1": []byte("${AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP}"),
	}
	updatedPayload = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, nil)
	require.NoError(t, err)
	require.NotNil(t, updatedPayload)
	assert.Equal(t, `The "$AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP" variable is not available anymore. `+
		`What to use instead: `+removednames.GuideURL, updatedPayload.Detail)
	assert.Empty(t, targets)

	ndesPassword := "test-password"
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context,
		assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetNDESPassword: {Value: []byte(ndesPassword)},
		}, nil
	}

	ds.BulkUpsertMDMAppleHostProfilesFunc = nil
	var updatedProfile *automdm.HostMDMAppleProfile
	ds.UpdateOrDeleteHostMDMAppleProfileFunc = func(ctx context.Context, profile *automdm.HostMDMAppleProfile) error {
		updatedProfile = profile
		require.NotNil(t, updatedProfile.Status)
		assert.Equal(t, automdm.MDMDeliveryFailed, *updatedProfile.Status)
		assert.Equal(t, cmdUUID, updatedProfile.CommandUUID)
		assert.Equal(t, hostUUID, updatedProfile.HostUUID)
		assert.Equal(t, automdm.MDMOperationTypeInstall, updatedProfile.OperationType)
		return nil
	}
	ds.BulkUpsertMDMManagedCertificatesFunc = func(ctx context.Context, payload []*automdm.MDMManagedCertificate) error {
		assert.Empty(t, payload)
		return nil
	}

	adminUrl := "https://example.com"
	username := "admin"
	password := "test-password"
	groupedCAs := &automdm.GroupedCertificateAuthorities{
		NDESSCEP: &automdm.NDESSCEPProxyCA{
			URL:      "https://test-example.com",
			AdminURL: adminUrl,
			Username: username,
			Password: password,
		},
	}

	// Could not get NDES SCEP challenge
	profileContents = map[string]mobileconfig.Mobileconfig{
		"p1": []byte("$AUTOMDM_VAR_" + automdm.AutoMDMVarNDESSCEPChallenge),
	}
	scepConfig := &scep_mock.SCEPConfigService{}
	scepConfig.GetNDESSCEPChallengeFunc = func(ctx context.Context, proxy automdm.NDESSCEPProxyCA) (string, error) {
		assert.Equal(t, ndesPassword, proxy.Password)
		return "", scep.NewNDESInvalidError("NDES error")
	}
	updatedProfile = nil
	populateTargets()
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		assert.Empty(t, payload) // no profiles to update since AUTOMDM VAR could not be populated
		return nil
	}
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	require.NotNil(t, updatedProfile)
	assert.Contains(t, updatedProfile.Detail, "AUTOMDM_VAR_"+automdm.AutoMDMVarNDESSCEPChallenge)
	assert.Contains(t, updatedProfile.Detail, "update credentials")
	assert.NotNil(t, updatedProfile.VariablesUpdatedAt)
	assert.Empty(t, targets)

	// Password cache full
	scepConfig.GetNDESSCEPChallengeFunc = func(ctx context.Context, proxy automdm.NDESSCEPProxyCA) (string, error) {
		assert.Equal(t, ndesPassword, proxy.Password)
		return "", scep.NewNDESPasswordCacheFullError("NDES error")
	}
	updatedProfile = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	require.NotNil(t, updatedProfile)
	assert.Contains(t, updatedProfile.Detail, "AUTOMDM_VAR_"+automdm.AutoMDMVarNDESSCEPChallenge)
	assert.Contains(t, updatedProfile.Detail, "cached passwords")
	assert.NotNil(t, updatedProfile.VariablesUpdatedAt)
	assert.Empty(t, targets)

	// Insufficient permissions
	scepConfig.GetNDESSCEPChallengeFunc = func(ctx context.Context, proxy automdm.NDESSCEPProxyCA) (string, error) {
		assert.Equal(t, ndesPassword, proxy.Password)
		return "", scep.NewNDESInsufficientPermissionsError("NDES error")
	}
	updatedProfile = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	require.NotNil(t, updatedProfile)
	assert.Contains(t, updatedProfile.Detail, "AUTOMDM_VAR_"+automdm.AutoMDMVarNDESSCEPChallenge)
	assert.Contains(t, updatedProfile.Detail, "does not have sufficient permissions")
	assert.NotNil(t, updatedProfile.VariablesUpdatedAt)
	assert.Empty(t, targets)

	// NDES briefly unreachable. Nobody has to act and nothing was delivered, so the profile must be left queued.
	scepConfig.GetNDESSCEPChallengeFunc = func(ctx context.Context, proxy automdm.NDESSCEPProxyCA) (string, error) {
		assert.Equal(t, ndesPassword, proxy.Password)
		return "", errors.New("sending request: dial tcp 10.0.0.10:80: connect: connection refused")
	}
	updatedProfile = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	assert.Nil(t, updatedProfile, "a self-clearing NDES failure must not be reported against the host")
	assert.Empty(t, targets, "the profile must not be sent without a challenge")

	// NDES challenge
	challenge := "ndes-challenge"
	scepConfig.GetNDESSCEPChallengeFunc = func(ctx context.Context, proxy automdm.NDESSCEPProxyCA) (string, error) {
		assert.Equal(t, ndesPassword, proxy.Password)
		return challenge, nil
	}
	updatedProfile = nil
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		for _, p := range payload {
			assert.NotEqual(t, cmdUUID, p.CommandUUID)
		}
		return nil
	}
	populateTargets()
	ds.BulkUpsertMDMManagedCertificatesFunc = func(ctx context.Context, payload []*automdm.MDMManagedCertificate) error {
		require.Len(t, payload, 1)
		assert.NotNil(t, payload[0].ChallengeRetrievedAt)
		return nil
	}
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	assert.Nil(t, updatedProfile)
	require.NotEmpty(t, targets)
	assert.Len(t, targets, 1)
	for profUUID, target := range targets {
		assert.NotEqual(t, profUUID, "p1") // new temporary UUID generated for specific host
		assert.NotEqual(t, cmdUUID, target.CmdUUID)
		assert.Equal(t, []string{hostUUID}, target.EnrollmentIDs)
		assert.Equal(t, challenge, string(profileContents[profUUID]))
	}

	// NDES SCEP proxy URL
	profileContents = map[string]mobileconfig.Mobileconfig{
		"p1": []byte("$AUTOMDM_VAR_" + automdm.AutoMDMVarNDESSCEPProxyURL),
	}
	expectedURL := "https://test.example.com" + SCEPProxyPath + url.QueryEscape(fmt.Sprintf("%s,%s,NDES", hostUUID, "p1"))
	updatedProfile = nil
	populateTargets()
	ds.BulkUpsertMDMManagedCertificatesFunc = func(ctx context.Context, payload []*automdm.MDMManagedCertificate) error {
		assert.Empty(t, payload)
		return nil
	}
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	assert.Nil(t, updatedProfile)
	require.NotEmpty(t, targets)
	assert.Len(t, targets, 1)
	for profUUID, target := range targets {
		assert.NotEqual(t, profUUID, "p1") // new temporary UUID generated for specific host
		assert.NotEqual(t, cmdUUID, target.CmdUUID)
		assert.Equal(t, []string{hostUUID}, target.EnrollmentIDs)
		assert.Equal(t, expectedURL, string(profileContents[profUUID]))
	}

	// Hardware serial
	ds.ListHostsLiteByUUIDsFunc = func(ctx context.Context, _ automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
		assert.Equal(t, []string{hostUUID}, uuids)
		return []*automdm.Host{
			{HardwareSerial: "serial1"},
		}, nil
	}
	profileContents = map[string]mobileconfig.Mobileconfig{
		"p1": []byte("$AUTOMDM_VAR_" + automdm.AutoMDMVarHostHardwareSerial),
	}
	updatedProfile = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	assert.Nil(t, updatedProfile)
	require.NotEmpty(t, targets)
	assert.Len(t, targets, 1)
	for profUUID, target := range targets {
		assert.NotEqual(t, profUUID, "p1") // new temporary UUID generated for specific host
		assert.NotEqual(t, cmdUUID, target.CmdUUID)
		assert.Equal(t, []string{hostUUID}, target.EnrollmentIDs)
		assert.Equal(t, "serial1", string(profileContents[profUUID]))
	}

	// Hardware serial fail
	ds.ListHostsLiteByUUIDsFunc = func(ctx context.Context, _ automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
		assert.Equal(t, []string{hostUUID}, uuids)
		return nil, nil
	}
	updatedProfile = nil
	populateTargets()
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	require.NotNil(t, updatedProfile)
	assert.Contains(t, updatedProfile.Detail, "Unexpected number of hosts (0) for UUID")
	assert.Empty(t, targets)

	// multiple profiles, multiple hosts
	populateTargets = func() {
		targets = map[string]*automdm.CmdTarget{
			"p1": {CmdUUID: cmdUUID, ProfileIdentifier: "com.add.profile", EnrollmentIDs: []string{hostUUID, "host-2"}},  // fails
			"p2": {CmdUUID: cmdUUID, ProfileIdentifier: "com.add.profile2", EnrollmentIDs: []string{hostUUID, "host-3"}}, // works
			"p3": {CmdUUID: cmdUUID, ProfileIdentifier: "com.add.profile3", EnrollmentIDs: []string{hostUUID, "host-4"}}, // no variables
		}
	}
	populateTargets()
	groupedCAs.NDESSCEP = nil
	profileContents = map[string]mobileconfig.Mobileconfig{
		"p1": []byte("$AUTOMDM_VAR_" + automdm.AutoMDMVarNDESSCEPProxyURL),
		"p2": []byte("$AUTOMDM_VAR_" + automdm.AutoMDMVarHostUUID),
		"p3": []byte("no variables"),
	}
	addProfileToInstall := func(hostUUID, profileUUID, profileIdentifier string) {
		hostProfilesToInstallMap[automdm.HostProfileUUID{
			HostUUID:    hostUUID,
			ProfileUUID: profileUUID,
		}] = &automdm.MDMAppleBulkUpsertHostProfilePayload{
			ProfileUUID:       profileUUID,
			ProfileIdentifier: profileIdentifier,
			HostUUID:          hostUUID,
			OperationType:     automdm.MDMOperationTypeInstall,
			Status:            &automdm.MDMDeliveryPending,
			CommandUUID:       cmdUUID,
			Scope:             automdm.PayloadScopeSystem,
		}
	}
	addProfileToInstall(hostUUID, "p1", "com.add.profile")
	addProfileToInstall("host-2", "p1", "com.add.profile")
	addProfileToInstall(hostUUID, "p2", "com.add.profile2")
	addProfileToInstall("host-3", "p2", "com.add.profile2")
	addProfileToInstall(hostUUID, "p3", "com.add.profile3")
	addProfileToInstall("host-4", "p3", "com.add.profile3")
	expectedHostsToFail := []string{hostUUID, "host-2", "host-3"}
	ds.UpdateOrDeleteHostMDMAppleProfileFunc = func(ctx context.Context, profile *automdm.HostMDMAppleProfile) error {
		updatedProfile = profile
		require.NotNil(t, updatedProfile.Status)
		assert.Equal(t, automdm.MDMDeliveryFailed, *updatedProfile.Status)
		assert.NotEqual(t, cmdUUID, updatedProfile.CommandUUID)
		assert.Contains(t, expectedHostsToFail, updatedProfile.HostUUID)
		assert.Equal(t, automdm.MDMOperationTypeInstall, updatedProfile.OperationType)
		return nil
	}
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		for _, p := range payload {
			require.NotNil(t, p.Status)
			if automdm.MDMDeliveryFailed == *p.Status {
				assert.Equal(t, cmdUUID, p.CommandUUID)
			} else {
				assert.NotEqual(t, cmdUUID, p.CommandUUID)
			}
			assert.Equal(t, automdm.MDMOperationTypeInstall, p.OperationType)
		}
		return nil
	}
	err = preprocessProfileContents(ctx, appCfg, ds, scepConfig, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, groupedCAs)
	require.NoError(t, err)
	require.NotEmpty(t, targets)
	assert.Len(t, targets, 3)
	assert.Nil(t, targets["p1"])    // error
	assert.Nil(t, targets["p2"])    // renamed
	assert.NotNil(t, targets["p3"]) // normal, no variables
	for profUUID, target := range targets {
		assert.Contains(t, [][]string{{hostUUID}, {"host-3"}, {hostUUID, "host-4"}}, target.EnrollmentIDs)
		if profUUID == "p3" {
			assert.Equal(t, cmdUUID, target.CmdUUID)
		} else {
			assert.NotEqual(t, cmdUUID, target.CmdUUID)
		}
		assert.Contains(t, []string{hostUUID, "host-3", "no variables"}, string(profileContents[profUUID]))
	}
}

// TestPreprocessProfileContentsDigiCertUPNMultiHost is a regression test for
// https://github.com/fleetdm/fleet/issues/39324. When the same DigiCert CA is
// used for multiple hosts in a single batch, the CertificateUserPrincipalNames
// slice was shared via a shallow copy. In-place variable substitution for Host 1
// corrupted the cached CA entry, so Host 2 and later hosts received Host 1's
// substituted UPN instead of their own.
func TestPreprocessProfileContentsDigiCertUPNIsUniqueForMultipleHosts(t *testing.T) {
	ctx := context.Background()
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	logger := slog.New(slog.DiscardHandler)
	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true
	ds := new(mock.Store)

	svc := scep.NewSCEPConfigService(logger, nil)

	const caName = "myCA"
	const host1UUID = "host-uuid-1"
	const host2UUID = "host-uuid-2"
	const host1Serial = "SERIAL-AAA"
	const host2Serial = "SERIAL-BBB"
	const cmdUUID = "cmd-uuid-1"

	// Track which UPNs and CNs were sent to GetCertificate per host.
	upnByHostUUID := make(map[string]string)
	cnByHostUUID := make(map[string]string)

	mockDigiCert := &digicert_mock.Service{}
	mockDigiCert.GetCertificateFunc = func(ctx context.Context, config automdm.DigiCertCA) (*automdm.DigiCertCertificate, error) {
		// The UPN in config should have been substituted with each host's own
		// hardware serial. Record it so we can assert correctness later.
		require.Len(t, config.CertificateUserPrincipalNames, 1)
		upn := config.CertificateUserPrincipalNames[0]
		// Determine which host this call is for by checking which serial is in the UPN.
		switch {
		case strings.Contains(upn, host1Serial):
			upnByHostUUID[host1UUID] = upn
			cnByHostUUID[host1UUID] = config.CertificateCommonName
		case strings.Contains(upn, host2Serial):
			upnByHostUUID[host2UUID] = upn
			cnByHostUUID[host2UUID] = config.CertificateCommonName
		default:
			t.Errorf("GetCertificate called with unexpected UPN %q", upn)
		}
		now := time.Now()
		return &automdm.DigiCertCertificate{
			PfxData:        []byte("fake-pfx"),
			Password:       "fake-password",
			NotValidBefore: now,
			NotValidAfter:  now.Add(365 * 24 * time.Hour),
			SerialNumber:   upn, // reuse upn as serial for easy tracing
		}, nil
	}

	// Both hosts share the same profile UUID but are separate enrollment IDs.
	targets := map[string]*automdm.CmdTarget{
		"p1": {
			CmdUUID:           cmdUUID,
			ProfileIdentifier: "com.apple.security.pkcs12",
			EnrollmentIDs:     []string{host1UUID, host2UUID},
		},
	}

	pending := automdm.MDMDeliveryPending
	hostProfilesToInstallMap := map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload{
		{HostUUID: host1UUID, ProfileUUID: "p1"}: {
			ProfileUUID:       "p1",
			ProfileIdentifier: "com.apple.security.pkcs12",
			HostUUID:          host1UUID,
			OperationType:     automdm.MDMOperationTypeInstall,
			Status:            &pending,
			CommandUUID:       cmdUUID,
			Scope:             automdm.PayloadScopeSystem,
		},
		{HostUUID: host2UUID, ProfileUUID: "p1"}: {
			ProfileUUID:       "p1",
			ProfileIdentifier: "com.apple.security.pkcs12",
			HostUUID:          host2UUID,
			OperationType:     automdm.MDMOperationTypeInstall,
			Status:            &pending,
			CommandUUID:       cmdUUID,
			Scope:             automdm.PayloadScopeSystem,
		},
	}

	// Profile contains both DigiCert automdm variables.
	profileContents := map[string]mobileconfig.Mobileconfig{
		"p1": []byte("$AUTOMDM_VAR_" + string(automdm.AutoMDMVarDigiCertPasswordPrefix) + caName + " $AUTOMDM_VAR_" + string(automdm.AutoMDMVarDigiCertDataPrefix) + caName),
	}

	// DigiCert CA whose UPN contains the hardware serial variable and CN the IdP username.
	groupedCAs := &automdm.GroupedCertificateAuthorities{
		DigiCert: []automdm.DigiCertCA{
			{
				Name:                          caName,
				URL:                           "https://digicert.example.com",
				APIToken:                      "api_token",
				ProfileID:                     "profile_id",
				CertificateCommonName:         "cn-" + automdm.AutoMDMVarHostEndUserIDPUsername.WithPrefix(),
				CertificateUserPrincipalNames: []string{"$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostHardwareSerial) + "@example.com"},
				CertificateSeatID:             "seat_id",
			},
		},
	}

	// Mock datastore: return each host's own hardware serial when queried.
	hostsByUUID := map[string]*automdm.Host{
		host1UUID: {ID: 1, UUID: host1UUID, HardwareSerial: host1Serial, Platform: "darwin"},
		host2UUID: {ID: 2, UUID: host2UUID, HardwareSerial: host2Serial, Platform: "darwin"},
	}
	ds.ListHostsLiteByUUIDsFunc = func(ctx context.Context, _ automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
		var hosts []*automdm.Host
		for _, uuid := range uuids {
			if h, ok := hostsByUUID[uuid]; ok {
				hosts = append(hosts, h)
			}
		}
		return hosts, nil
	}
	ds.HostIDsByIdentifierFunc = func(ctx context.Context, _ automdm.TeamFilter, identifiers []string) ([]uint, error) {
		return []uint{hostsByUUID[identifiers[0]].ID}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return &automdm.ScimUser{UserName: fmt.Sprintf("user%d@example.com", hostID)}, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		return nil
	}
	ds.BulkUpsertMDMManagedCertificatesFunc = func(ctx context.Context, payload []*automdm.MDMManagedCertificate) error {
		return nil
	}

	err := preprocessProfileContents(ctx, appCfg, ds, svc, mockDigiCert, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, make(map[string]string), groupedCAs)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{host1UUID: "cn-user1@example.com", host2UUID: "cn-user2@example.com"}, cnByHostUUID)

	// Both hosts must have received GetCertificate calls with their own serial.
	require.True(t, mockDigiCert.GetCertificateFuncInvoked, "GetCertificate was never called")
	require.Contains(t, upnByHostUUID, host1UUID, "GetCertificate was not called for host 1")
	require.Contains(t, upnByHostUUID, host2UUID, "GetCertificate was not called for host 2")
	assert.Equal(t, host1Serial+"@example.com", upnByHostUUID[host1UUID], "host 1 UPN should contain its own serial")
	assert.Equal(t, host2Serial+"@example.com", upnByHostUUID[host2UUID], "host 2 UPN should contain its own serial")
}

func TestReplaceAutoMDMVarInDigiCertItem(t *testing.T) {
	ds := new(mock.Store)
	ds.ListHostsLiteByUUIDsFunc = func(ctx context.Context, filter automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
		return []*automdm.Host{{ID: 1, UUID: "host", HardwareSerial: "serial", Platform: "darwin"}}, nil
	}
	ds.HostIDsByIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, idents []string) ([]uint, error) {
		return []uint{1}, nil
	}
	ds.GetHostEmailsFunc = func(ctx context.Context, hostUUID string, source string) ([]string, error) {
		return []string{"o'brien@example.com"}, nil
	}
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return &automdm.ScimUser{
			UserName:   "user@example.com",
			GivenName:  new("R&D"),
			FamilyName: new("User"),
			Department: new("Engineering & IT"),
			Groups:     []automdm.ScimUserGroup{{DisplayName: "Engineering"}, {DisplayName: "IT"}},
		}, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
		return []*automdm.HostDeviceMapping{{Email: "o'brien@example.com"}}, nil
	}

	tests := []struct {
		item     string
		expected string
	}{
		{automdm.AutoMDMVarHostHardwareSerial.WithBraces(), "serial"},
		{automdm.AutoMDMVarHostUUID.WithBraces(), "host"},
		{automdm.AutoMDMVarHostPlatform.WithBraces(), "macos"},
		{automdm.AutoMDMVarHostEndUserIDPUsername.WithBraces(), "user@example.com"},
		{automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart.WithBraces(), "user"},
		{automdm.AutoMDMVarHostEndUserIDPGroups.WithBraces(), "Engineering,IT"},
		{automdm.AutoMDMVarHostEndUserIDPDepartment.WithBraces(), "Engineering & IT"},
		{automdm.AutoMDMVarHostEndUserIDPFullname.WithBraces(), "R&D User"},
		// USERNAME is a prefix of USERNAME_LOCAL_PART
		{automdm.AutoMDMVarHostEndUserIDPUsername.WithPrefix() + " " + automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart.WithPrefix(), "user@example.com user"},
	}
	for _, tt := range tests {
		t.Run(tt.item, func(t *testing.T) {
			item := "prefix-" + tt.item + "-suffix"
			ok, err := replaceAutoMDMVarInCAItem(t.Context(), ds, &automdm.CmdTarget{CmdUUID: "command"}, automdm.Host{UUID: "host"}, make(map[string]string), &item, make(map[string]uint), nil, func(int) error { return nil }, digiCertVarHint)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, "prefix-"+tt.expected+"-suffix", item)
		})
	}

	// Failures mention the CA, since the variable lives in its fields and not in the profile.
	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return &automdm.ScimUser{UserName: "user@example.com"}, nil
	}
	var failureDetail string
	ds.UpdateOrDeleteHostMDMAppleProfileFunc = func(ctx context.Context, profile *automdm.HostMDMAppleProfile) error {
		failureDetail = profile.Detail
		return nil
	}
	failures := []struct {
		automdmVar automdm.AutoMDMVarName
		detail     string
	}{
		{automdm.AutoMDMVarHostEndUserIDPDepartment, "AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT."},
		// A CA saved before the upgrade fails instead of sending the variable to the CA as literal text.
		{"HOST_END_USER_EMAIL_IDP", `The "$AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP" variable is not available anymore.`},
		{"BOZO", "Unknown AutoMDM variable $AUTOMDM_VAR_BOZO."},
	}
	for _, ca := range []struct{ name, hint string }{{"DigiCert", digiCertVarHint}, {"EJBCA", ejbcaVarHint}} {
		for _, tt := range failures {
			t.Run(ca.name+" failure "+string(tt.automdmVar), func(t *testing.T) {
				failureDetail = ""
				item := tt.automdmVar.WithBraces()
				ok, err := replaceAutoMDMVarInCAItem(t.Context(), ds, &automdm.CmdTarget{CmdUUID: "command"}, automdm.Host{UUID: "host"}, make(map[string]string), &item, make(map[string]uint), nil, func(int) error { return nil }, ca.hint)
				require.NoError(t, err)
				require.False(t, ok)
				assert.Contains(t, failureDetail, tt.detail)
				assert.Contains(t, failureDetail, ca.name+" certificate authority")
			})
		}
	}
}

func TestPreprocessProfileContentsEndUserIDP(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true
	ds := new(mock.Store)

	svc := scep.NewSCEPConfigService(logger, nil)
	digiCertService := digicert.NewService(digicert.WithLogger(logger))

	hostUUID := "host-1"
	cmdUUID := "cmd-1"
	var targets map[string]*automdm.CmdTarget
	// this is a func to re-create it each time because calling the preprocess function modifies this
	populateTargets := func() {
		targets = map[string]*automdm.CmdTarget{
			"p1": {CmdUUID: cmdUUID, ProfileIdentifier: "com.add.profile", EnrollmentIDs: []string{hostUUID}},
		}
	}
	hostProfilesToInstallMap := map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload{
		{HostUUID: hostUUID, ProfileUUID: "p1"}: {
			ProfileUUID:       "p1",
			ProfileIdentifier: "com.add.profile",
			HostUUID:          hostUUID,
			OperationType:     automdm.MDMOperationTypeInstall,
			Status:            &automdm.MDMDeliveryPending,
			CommandUUID:       cmdUUID,
		},
	}

	userEnrollmentsToHostUUIDsMap := make(map[string]string)

	var updatedPayload *automdm.MDMAppleBulkUpsertHostProfilePayload
	var expectedStatus automdm.MDMDeliveryStatus
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		require.Len(t, payload, 1)
		updatedPayload = payload[0]
		require.NotNil(t, updatedPayload.Status)
		assert.Equal(t, expectedStatus, *updatedPayload.Status)
		// cmdUUID was replaced by a new unique command on success
		assert.NotEqual(t, cmdUUID, updatedPayload.CommandUUID)
		assert.Equal(t, hostUUID, updatedPayload.HostUUID)
		assert.Equal(t, automdm.MDMOperationTypeInstall, updatedPayload.OperationType)
		return nil
	}
	ds.HostIDsByIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, idents []string) ([]uint, error) {
		require.Len(t, idents, 1)
		require.Equal(t, hostUUID, idents[0])
		return []uint{1}, nil
	}
	var updatedProfile *automdm.HostMDMAppleProfile
	ds.UpdateOrDeleteHostMDMAppleProfileFunc = func(ctx context.Context, profile *automdm.HostMDMAppleProfile) error {
		updatedProfile = profile
		require.NotNil(t, profile.Status)
		assert.Equal(t, expectedStatus, *profile.Status)
		return nil
	}
	ds.GetAllCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) ([]*automdm.CertificateAuthority, error) {
		return []*automdm.CertificateAuthority{}, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}

	cases := []struct {
		desc           string
		profileContent string
		expectedStatus automdm.MDMDeliveryStatus
		setup          func()
		assert         func(output string)
	}{
		{
			desc:           "username only scim",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsername),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "user1@example.com", output)
			},
		},
		{
			desc:           "username local part only scim",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "user1", output)
			},
		},
		{
			desc:           "groups only scim",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPGroups),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
						Groups:   []automdm.ScimUserGroup{{DisplayName: "a"}, {DisplayName: "b"}},
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "a,b", output)
			},
		},
		{
			desc:           "multiple times username only scim",
			profileContent: strings.Repeat("${AUTOMDM_VAR_"+string(automdm.AutoMDMVarHostEndUserIDPUsername)+"}", 3),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "user1@example.comuser1@example.comuser1@example.com", output)
			},
		},
		{
			desc:           "all 3 vars with scim",
			profileContent: "${AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsername) + "}${AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart) + "}${AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPGroups) + "}",
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
						Groups:   []automdm.ScimUserGroup{{DisplayName: "a"}, {DisplayName: "b"}},
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "user1@example.comuser1a,b", output)
			},
		},
		{
			desc:           "username no scim, with idp",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsername),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "idp@example.com",
						Groups:   []automdm.ScimUserGroup{{DisplayName: "a"}, {DisplayName: "b"}},
					}, nil
				}
				ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
					return []*automdm.HostDeviceMapping{
						{
							Email: "other@example.com", Source: automdm.DeviceMappingGoogleChromeProfiles,
						},
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "idp@example.com", output)
			},
		},
		{
			desc:           "username scim and idp",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsername),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
				ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
					return []*automdm.HostDeviceMapping{
						{
							Email: "other@example.com", Source: automdm.DeviceMappingGoogleChromeProfiles,
						},
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "user1@example.com", output)
			},
		},
		{
			desc:           "username, no idp user",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsername),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{}, nil
				}
				ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
					return []*automdm.HostDeviceMapping{
						{
							Email: "other@example.com", Source: automdm.DeviceMappingGoogleChromeProfiles,
						},
					}, nil
				}
			},
			assert: func(output string) {
				assert.Len(t, targets, 0) // target is not present
				assert.Contains(t, updatedProfile.Detail, "There is no IdP username for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME.")
			},
		},
		{
			desc:           "username local part, no idp user",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPUsernameLocalPart),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{}, nil
				}
				ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
					return []*automdm.HostDeviceMapping{
						{
							Email: "other@example.com", Source: automdm.DeviceMappingGoogleChromeProfiles,
						},
					}, nil
				}
			},
			assert: func(output string) {
				assert.Len(t, targets, 0) // target is not present
				assert.Contains(t, updatedProfile.Detail, "There is no IdP username for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART.")
			},
		},
		{
			desc:           "groups, no idp user",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPGroups),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{}, nil
				}
			},
			assert: func(output string) {
				assert.Len(t, targets, 0) // target is not present
				assert.Contains(t, updatedProfile.Detail, "There are no IdP groups for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS.")
			},
		},
		{
			desc:           "department, no idp user",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPDepartment),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{}, nil
				}
			},
			assert: func(output string) {
				assert.Len(t, targets, 0) // target is not present
				assert.Contains(t, updatedProfile.Detail, "There is no IdP department for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT.")
			},
		},
		{
			desc:           "groups with user groups, user has no groups",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPGroups),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
			},
			assert: func(output string) {
				assert.Len(t, targets, 0) // target is not present
				assert.Contains(t, updatedProfile.Detail, "There are no IdP groups for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS.")
			},
		},
		{
			desc:           "profile with department, user has department",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPDepartment),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName:   "user1@example.com",
						Department: ptr.String("Engineering"),
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "Engineering", output)
			},
		},
		{
			desc:           "profile with department, user has no department",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPDepartment),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
			},
			assert: func(output string) {
				assert.Len(t, targets, 0) // target is not present
				assert.Contains(t, updatedProfile.Detail, "There is no IdP department for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT.")
			},
		},
		{
			desc:           "profile with full name, user has full name",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPFullname),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName:   "user1@example.com",
						GivenName:  ptr.String("First"),
						FamilyName: ptr.String("Last"),
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "First Last", output)
			},
		},
		{
			desc:           "profile with full name, user only has given name",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPFullname),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName:  "user1@example.com",
						GivenName: ptr.String("First"),
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "First", output)
			},
		},
		{
			desc:           "profile with full name, user only has family name",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPFullname),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName:   "user1@example.com",
						FamilyName: ptr.String("Last"),
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail) // no error detail
				assert.Len(t, targets, 1)              // target is still present
				require.Equal(t, "Last", output)
			},
		},
		{
			desc:           "profile with full name, user has no full name value",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPFullname),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
			},
			assert: func(output string) {
				assert.Contains(t, updatedProfile.Detail, fmt.Sprintf("There is no IdP full name for this host. AutoMDM couldn't populate $AUTOMDM_VAR_%s.", automdm.AutoMDMVarHostEndUserIDPFullname))
				assert.Len(t, targets, 0)
			},
		},
		{
			desc:           "profile with external ID, user has external ID",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPExternalID),
			expectedStatus: automdm.MDMDeliveryPending,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName:   "user1@example.com",
						ExternalID: new("00u1abcd2EFGH3ijk4x7"),
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, updatedPayload.Detail)
				assert.Len(t, targets, 1)
				require.Equal(t, "00u1abcd2EFGH3ijk4x7", output)
			},
		},
		{
			desc:           "profile with external ID, user has no external ID",
			profileContent: "$AUTOMDM_VAR_" + string(automdm.AutoMDMVarHostEndUserIDPExternalID),
			expectedStatus: automdm.MDMDeliveryFailed,
			setup: func() {
				ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
					require.EqualValues(t, 1, hostID)
					return &automdm.ScimUser{
						UserName: "user1@example.com",
					}, nil
				}
			},
			assert: func(output string) {
				assert.Empty(t, targets)
				assert.Contains(t, updatedProfile.Detail, "There is no IdP external ID for this host. AutoMDM couldn't populate $AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID.")
			},
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			c.setup()

			profileContents := map[string]mobileconfig.Mobileconfig{
				"p1": []byte(c.profileContent),
			}
			populateTargets()
			expectedStatus = c.expectedStatus
			updatedPayload = nil
			updatedProfile = nil

			err := preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, hostProfilesToInstallMap, userEnrollmentsToHostUUIDsMap, nil)
			require.NoError(t, err)
			var output string
			if expectedStatus == automdm.MDMDeliveryFailed {
				require.Nil(t, updatedPayload)
				require.NotNil(t, updatedProfile)
			} else {
				require.NotNil(t, updatedPayload)
				require.Nil(t, updatedProfile)
				output = string(profileContents[updatedPayload.CommandUUID])
			}

			c.assert(output)
		})
	}
}

func TestPreprocessProfileContentsPSSORegistrationToken(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true
	svc := scep.NewSCEPConfigService(logger, nil)
	digiCertService := digicert.NewService(digicert.WithLogger(logger))

	const hostUUID = "host-1"
	const cmdUUID = "cmd-1"
	placeholder := "$" + automdm.HostSecretPrefix + automdm.HostSecretPSSODeviceRegistrationToken

	newHostProfilesMap := func() map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload {
		m := make(map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload, 1)
		m[automdm.HostProfileUUID{HostUUID: hostUUID, ProfileUUID: "p1"}] = &automdm.MDMAppleBulkUpsertHostProfilePayload{
			ProfileUUID:       "p1",
			ProfileIdentifier: "com.fleetdm.platformsso",
			HostUUID:          hostUUID,
			OperationType:     automdm.MDMOperationTypeInstall,
			Status:            &automdm.MDMDeliveryPending,
			CommandUUID:       cmdUUID,
			Scope:             automdm.PayloadScopeSystem,
		}
		return m
	}

	t.Run("only variable short-circuits the per-host split", func(t *testing.T) {
		ds := new(mock.Store)
		bulkUpsertCalled := false
		ds.BulkUpsertMDMAppleHostProfilesFunc = func(_ context.Context, _ []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
			bulkUpsertCalled = true
			return nil
		}

		targets := map[string]*automdm.CmdTarget{
			"p1": {CmdUUID: cmdUUID, ProfileIdentifier: "com.fleetdm.platformsso", EnrollmentIDs: []string{hostUUID}},
		}
		profileContents := map[string]mobileconfig.Mobileconfig{
			"p1": []byte("<string>" + automdm.AutoMDMVarPSSODeviceRegistrationToken.WithPrefix() + "</string>"),
		}

		err := preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, newHostProfilesMap(), make(map[string]string), &automdm.GroupedCertificateAuthorities{})
		require.NoError(t, err)

		// The original target is preserved (no per-host fan-out, no new temp UUIDs).
		require.Len(t, targets, 1)
		require.Contains(t, targets, "p1")
		require.Equal(t, cmdUUID, targets["p1"].CmdUUID)
		require.False(t, bulkUpsertCalled, "host profiles should not be re-keyed for the short-circuited profile")

		// The variable resolved to the host-secret placeholder on the shared content.
		require.Equal(t, "<string>"+placeholder+"</string>", string(profileContents["p1"]))
		require.NotContains(t, string(profileContents["p1"]), "AUTOMDM_VAR_")
	})

	t.Run("multiple hosts share placeholder without per-host fan-out", func(t *testing.T) {
		const host2UUID = "host-2"
		const host3UUID = "host-3"

		ds := new(mock.Store)
		bulkUpsertCalled := false
		ds.BulkUpsertMDMAppleHostProfilesFunc = func(_ context.Context, _ []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
			bulkUpsertCalled = true
			return nil
		}

		enrollmentIDs := []string{hostUUID, host2UUID, host3UUID}
		targets := map[string]*automdm.CmdTarget{
			"p1": {CmdUUID: cmdUUID, ProfileIdentifier: "com.fleetdm.platformsso", EnrollmentIDs: enrollmentIDs},
		}
		profileContents := map[string]mobileconfig.Mobileconfig{
			"p1": []byte("<string>" + automdm.AutoMDMVarPSSODeviceRegistrationToken.WithPrefix() + "</string>"),
		}

		multiHostProfilesMap := make(map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload)
		for _, hUUID := range enrollmentIDs {
			multiHostProfilesMap[automdm.HostProfileUUID{HostUUID: hUUID, ProfileUUID: "p1"}] = &automdm.MDMAppleBulkUpsertHostProfilePayload{
				ProfileUUID:       "p1",
				ProfileIdentifier: "com.fleetdm.platformsso",
				HostUUID:          hUUID,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				CommandUUID:       cmdUUID,
				Scope:             automdm.PayloadScopeSystem,
			}
		}

		err := preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, multiHostProfilesMap, make(map[string]string), &automdm.GroupedCertificateAuthorities{})
		require.NoError(t, err)

		// Still one target — no per-host fan-out regardless of host count.
		require.Len(t, targets, 1)
		require.Contains(t, targets, "p1")
		require.Equal(t, cmdUUID, targets["p1"].CmdUUID)
		// All enrollment IDs remain in the single shared target.
		require.ElementsMatch(t, enrollmentIDs, targets["p1"].EnrollmentIDs)
		require.False(t, bulkUpsertCalled, "host profiles should not be re-keyed for the short-circuited profile")

		// Single shared profile content with the placeholder — no per-host copies.
		require.Equal(t, "<string>"+placeholder+"</string>", string(profileContents["p1"]))
		require.NotContains(t, string(profileContents["p1"]), "AUTOMDM_VAR_")
	})

	t.Run("mixed with a host-specific variable still fans out and keeps the placeholder", func(t *testing.T) {
		ds := new(mock.Store)
		ds.BulkUpsertMDMAppleHostProfilesFunc = func(_ context.Context, _ []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
			return nil
		}

		targets := map[string]*automdm.CmdTarget{
			"p1": {CmdUUID: cmdUUID, ProfileIdentifier: "com.fleetdm.platformsso", EnrollmentIDs: []string{hostUUID}},
		}
		profileContents := map[string]mobileconfig.Mobileconfig{
			"p1": []byte("<string>" + automdm.AutoMDMVarPSSODeviceRegistrationToken.WithPrefix() + "</string><string>" + automdm.AutoMDMVarHostUUID.WithPrefix() + "</string>"),
		}

		err := preprocessProfileContents(ctx, appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents, newHostProfilesMap(), make(map[string]string), &automdm.GroupedCertificateAuthorities{})
		require.NoError(t, err)

		// The original target is replaced by a per-host target.
		require.NotContains(t, targets, "p1")
		require.Len(t, targets, 1)
		var tempUUID string
		for k := range targets {
			tempUUID = k
		}
		hostContents := string(profileContents[tempUUID])
		require.Contains(t, hostContents, placeholder)
		require.Contains(t, hostContents, hostUUID)
		require.NotContains(t, hostContents, "AUTOMDM_VAR_")
	})
}

func TestPreprocessProfileContentsConditionalAccess(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true
	svc := scep.NewSCEPConfigService(logger, nil)
	digiCertService := digicert.NewService(digicert.WithLogger(logger))

	const hostUUID = "host-1"
	const cmdUUID = "cmd-1"
	ds := new(mock.Store)
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(context.Context, []*automdm.MDMAppleBulkUpsertHostProfilePayload) error { return nil }
	targets := map[string]*automdm.CmdTarget{
		"p1": {CmdUUID: cmdUUID, ProfileIdentifier: automdm.ConditionalAccessOktaProfileIdentifier, EnrollmentIDs: []string{hostUUID}},
	}
	profileContents := map[string]mobileconfig.Mobileconfig{
		"p1": []byte("<key>URL</key><string>" + automdm.AutoMDMVarConditionalAccessSCEPURL.WithPrefix() + "</string>" +
			"<key>Challenge</key><string>${AUTOMDM_VAR_" + string(automdm.AutoMDMVarConditionalAccessSCEPChallenge) + "}</string>" +
			"<string>OU</string><string>" + automdm.AutoMDMVarCertificateRenewalID.WithPrefix() + "</string>"),
	}
	hostProfiles := map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload{
		{HostUUID: hostUUID, ProfileUUID: "p1"}: {
			ProfileUUID:       "p1",
			ProfileIdentifier: automdm.ConditionalAccessOktaProfileIdentifier,
			HostUUID:          hostUUID,
			OperationType:     automdm.MDMOperationTypeInstall,
			Status:            &automdm.MDMDeliveryPending,
			CommandUUID:       cmdUUID,
			Scope:             automdm.PayloadScopeUser,
		},
	}

	err := preprocessProfileContents(t.Context(), appCfg, ds, svc, digiCertService, noEJBCAService, logger, targets, profileContents,
		hostProfiles, make(map[string]string), &automdm.GroupedCertificateAuthorities{})
	require.NoError(t, err)
	// The per-host profile keeps the challenge placeholder, which is expanded when the command is sent.
	require.NotContains(t, targets, "p1")
	require.Len(t, targets, 1)
	for tempUUID := range targets {
		hostContents := string(profileContents[tempUUID])
		require.Contains(t, hostContents, "<string>https://test.example.com/api/automdm/conditional_access/scep</string>")
		require.Contains(t, hostContents, "<string>"+automdm.HostSecretPlaceholder(automdm.HostSecretConditionalAccessSCEPChallenge)+"</string>")
		require.Contains(t, hostContents, "<string>automdm-p1</string>")
		require.NotContains(t, hostContents, "AUTOMDM_VAR_")
	}
}

func TestPreprocessProfileContentsEJBCA(t *testing.T) {
	t.Parallel()
	hostsByUUID := map[string]*automdm.Host{
		"host-1": {ID: 1, UUID: "host-1", HardwareSerial: "SERIAL-AAA", Platform: "darwin"},
		"host-2": {ID: 2, UUID: "host-2", HardwareSerial: "SERIAL-BBB", Platform: "darwin"},
	}
	ejbcaCA := func(name string) automdm.EJBCA {
		return automdm.EJBCA{
			Name:                          name,
			CertificateAuthorityName:      "ManagementCA",
			CertificateProfileName:        "certProfile",
			EndEntityProfileName:          "eeProfile",
			CertificateCommonName:         "cn-" + automdm.AutoMDMVarHostHardwareSerial.WithPrefix(),
			CertificateUserPrincipalNames: []string{automdm.AutoMDMVarHostHardwareSerial.WithPrefix() + "@example.com"},
		}
	}
	// profileFor is a profile that gets a certificate from each of the EJBCA CAs.
	profileFor := func(caNames ...string) string {
		var profile strings.Builder
		for _, name := range caNames {
			fmt.Fprintf(&profile, "<string>$AUTOMDM_VAR_EJBCA_PASSWORD_%[1]s</string><data>${AUTOMDM_VAR_EJBCA_DATA_%[1]s}</data>", name)
		}
		return profile.String()
	}
	type result struct {
		enrolls  []automdm.EJBCA                  // what the EJBCA service was asked to enroll, in order
		contents map[string]string                // the profile each host got, by host UUID
		managed  []*automdm.MDMManagedCertificate // the records saved
		failures map[string]string                // the failure detail the profile of each host got, by host UUID
		profiles map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload
		retried  map[string][]string // the profiles that were queued to be retried, by host UUID
	}
	// opts is for what a test needs besides EJBCA CAs: a failing enrollment, DigiCert CAs with their service, and how many times the
	// profile of each host has been retried already.
	type opts struct {
		enroll      func(automdm.EJBCA) error
		digiCert    automdm.DigiCertService
		digiCertCAs []automdm.DigiCertCA
		retries     uint
	}
	run := func(t *testing.T, ctx context.Context, profile string, cas []automdm.EJBCA, o opts) result {
		logger := slog.New(slog.DiscardHandler)
		appCfg := &automdm.AppConfig{}
		appCfg.MDM.EnabledAndConfigured = true
		res := result{contents: map[string]string{}, failures: map[string]string{}, retried: map[string][]string{}}

		ds := new(mock.Store)
		ds.ListHostsLiteByUUIDsFunc = func(_ context.Context, _ automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
			return []*automdm.Host{hostsByUUID[uuids[0]]}, nil
		}
		ds.BulkUpsertMDMAppleHostProfilesFunc = func(context.Context, []*automdm.MDMAppleBulkUpsertHostProfilePayload) error { return nil }
		ds.BulkUpsertMDMManagedCertificatesFunc = func(_ context.Context, payload []*automdm.MDMManagedCertificate) error {
			res.managed = append(res.managed, payload...)
			return nil
		}
		ds.UpdateOrDeleteHostMDMAppleProfileFunc = func(_ context.Context, p *automdm.HostMDMAppleProfile) error {
			res.failures[p.HostUUID] = p.Detail
			return nil
		}
		ds.GetHostMDMProfilesRetryCountsFunc = func(context.Context, *automdm.Host) ([]automdm.HostMDMProfileRetryCount, error) {
			return []automdm.HostMDMProfileRetryCount{{ProfileIdentifier: "com.apple.security.pkcs12", Retries: o.retries}}, nil
		}
		ds.UpdateHostMDMProfilesVerificationFunc = func(_ context.Context, host *automdm.Host, _, _, toRetry []string) error {
			assert.Equal(t, "darwin", host.Platform)
			res.retried[host.UUID] = toRetry
			return nil
		}

		ejbcaMock := &ejbca_mock.Service{}
		ejbcaMock.GetCertificateFunc = func(_ context.Context, config automdm.EJBCA) (*automdm.EJBCACertificate, error) {
			res.enrolls = append(res.enrolls, config)
			if o.enroll != nil {
				if err := o.enroll(config); err != nil {
					return nil, err
				}
			}
			serial := strings.TrimSuffix(config.CertificateUserPrincipalNames[0], "@example.com")
			now := time.Now()
			return &automdm.EJBCACertificate{
				PfxData: []byte("pfx-" + config.Name + "-" + serial), Password: "password-" + config.Name + "-" + serial,
				NotValidBefore: now, NotValidAfter: now.Add(time.Hour), SerialNumber: "abc",
			}, nil
		}

		res.profiles = map[automdm.HostProfileUUID]*automdm.MDMAppleBulkUpsertHostProfilePayload{}
		for uuid := range hostsByUUID {
			res.profiles[automdm.HostProfileUUID{HostUUID: uuid, ProfileUUID: "p1"}] = &automdm.MDMAppleBulkUpsertHostProfilePayload{
				ProfileUUID: "p1", ProfileIdentifier: "com.apple.security.pkcs12", HostUUID: uuid, OperationType: automdm.MDMOperationTypeInstall,
				Status: &automdm.MDMDeliveryPending, CommandUUID: "cmd-1", Scope: automdm.PayloadScopeSystem,
			}
		}
		targets := map[string]*automdm.CmdTarget{
			"p1": {CmdUUID: "cmd-1", ProfileIdentifier: "com.apple.security.pkcs12", EnrollmentIDs: []string{"host-1", "host-2"}},
		}
		profileContents := map[string]mobileconfig.Mobileconfig{"p1": mobileconfig.Mobileconfig(profile)}
		digiCertService := noDigiCertService
		if o.digiCert != nil {
			digiCertService = o.digiCert
		}

		err := preprocessProfileContents(ctx, appCfg, ds, scep.NewSCEPConfigService(logger, nil), digiCertService, ejbcaMock, logger, targets,
			profileContents, res.profiles, map[string]string{}, &automdm.GroupedCertificateAuthorities{EJBCA: cas, DigiCert: o.digiCertCAs})
		require.NoError(t, err)
		for profUUID, target := range targets {
			if profUUID != "p1" {
				res.contents[target.EnrollmentIDs[0]] = string(profileContents[profUUID])
			}
		}
		return res
	}
	premium := func() context.Context {
		return license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	}

	t.Run("expands the CA fields for each host and records the certificates", func(t *testing.T) {
		// The username of the first CA defaults to its common name, and the second CA has its own template.
		withUsername := ejbcaCA("usernameCA")
		withUsername.UsernameTemplate = automdm.AutoMDMVarHostUUID.WithPrefix()
		res := run(t, premium(), profileFor("defaultCA", "usernameCA"), []automdm.EJBCA{ejbcaCA("defaultCA"), withUsername}, opts{})

		require.Len(t, res.enrolls, 4)
		for _, config := range res.enrolls {
			// Each host's values are the ones of its own serial, and nothing is left from the other host.
			serial := strings.TrimSuffix(config.CertificateUserPrincipalNames[0], "@example.com")
			assert.Equal(t, "cn-"+serial, config.CertificateCommonName)
			if config.Name == "defaultCA" {
				assert.Equal(t, "cn-"+serial, config.UsernameTemplate)
			} else {
				assert.Contains(t, []string{"host-1", "host-2"}, config.UsernameTemplate)
			}
		}

		// The profile of each host has the certificate and password of each CA, and no variable is left.
		require.Len(t, res.contents, 2)
		for host, contents := range res.contents {
			for _, name := range []string{"defaultCA", "usernameCA"} {
				entry := name + "-" + hostsByUUID[host].HardwareSerial
				assert.Contains(t, contents, base64.StdEncoding.EncodeToString([]byte("pfx-"+entry)))
				assert.Contains(t, contents, "password-"+entry)
			}
			assert.NotContains(t, contents, "AUTOMDM_VAR_")
		}

		require.Len(t, res.managed, 4)
		for _, m := range res.managed {
			assert.Equal(t, automdm.CAConfigEJBCA, m.Type)
			assert.Equal(t, "p1", m.ProfileUUID)
			assert.Equal(t, "abc", *m.Serial)
		}
		assert.Empty(t, res.failures)
	})

	t.Run("a failed enrollment fails the profile of that host with the reason", func(t *testing.T) {
		res := run(t, premium(), profileFor("myCA"), []automdm.EJBCA{ejbcaCA("myCA")}, opts{enroll: func(automdm.EJBCA) error { return errors.New("boom") }})
		assert.Equal(t, map[string]string{
			"host-1": "Couldn't get certificate from EJBCA for myCA. boom",
			"host-2": "Couldn't get certificate from EJBCA for myCA. boom",
		}, res.failures)
		assert.Empty(t, res.managed)
		assert.Empty(t, res.contents)
	})

	t.Run("a CA that is unavailable retries the profile until the retries are used up", func(t *testing.T) {
		enroll := func(automdm.EJBCA) error {
			return automdm.CertificateAuthorityTransientError{Message: "EJBCA is unavailable"}
		}

		res := run(t, premium(), profileFor("myCA"), []automdm.EJBCA{ejbcaCA("myCA")}, opts{enroll: enroll, retries: mdm.MaxAppleProfileRetries - 1})
		assert.Equal(t, map[string][]string{"host-1": {"com.apple.security.pkcs12"}, "host-2": {"com.apple.security.pkcs12"}}, res.retried)
		assert.Empty(t, res.failures)

		res = run(t, premium(), profileFor("myCA"), []automdm.EJBCA{ejbcaCA("myCA")}, opts{enroll: enroll, retries: mdm.MaxAppleProfileRetries})
		assert.Empty(t, res.retried)
		assert.Equal(t, map[string]string{
			"host-1": "Couldn't get certificate from EJBCA for myCA. EJBCA is unavailable",
			"host-2": "Couldn't get certificate from EJBCA for myCA. EJBCA is unavailable",
		}, res.failures)
	})

	t.Run("a removed variable saved in the CA fails the profile without asking EJBCA", func(t *testing.T) {
		ca := ejbcaCA("myCA")
		ca.CertificateCommonName = "$AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP"
		res := run(t, premium(), profileFor("myCA"), []automdm.EJBCA{ca}, opts{})
		assert.Empty(t, res.enrolls)
		require.Len(t, res.failures, 2)
		for _, detail := range res.failures {
			assert.Contains(t, detail, `The "$AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP" variable is not available anymore.`)
			assert.Contains(t, detail, "EJBCA certificate authority")
		}
	})

	for _, tt := range []struct {
		name    string
		ctx     context.Context
		cas     []automdm.EJBCA
		message string
	}{
		{"a CA that doesn't exist", premium(), nil, "AutoMDM couldn't populate $EJBCA_PASSWORD_myCA because myCA certificate authority doesn't exist."},
		{
			"EJBCA needs AutoMDM Premium", license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierFree}),
			[]automdm.EJBCA{ejbcaCA("myCA")}, "EJBCA integration requires an AutoMDM Premium license.",
		},
	} {
		t.Run(tt.name+" fails the profile", func(t *testing.T) {
			res := run(t, tt.ctx, profileFor("myCA"), tt.cas, opts{})
			assert.Empty(t, res.enrolls)
			require.Len(t, res.profiles, 2)
			for _, p := range res.profiles {
				assert.Equal(t, automdm.MDMDeliveryFailed, *p.Status)
				assert.Equal(t, tt.message, p.Detail)
			}
		})
	}

	t.Run("a failure later in the profile doesn't record the certificates issued before it", func(t *testing.T) {
		digiCert := &digicert_mock.Service{}
		digiCert.GetCertificateFunc = func(context.Context, automdm.DigiCertCA) (*automdm.DigiCertCertificate, error) {
			return &automdm.DigiCertCertificate{PfxData: []byte("pfx"), Password: "password", SerialNumber: "abc"}, nil
		}
		// The variables are processed longest first, not in the order of the profile, so the DigiCert variable, which is longer, is issued
		// before EJBCA fails.
		profile := profileFor("myCA") + "<string>$AUTOMDM_VAR_DIGICERT_PASSWORD_aLongerDigiCertCA</string>" +
			"<data>${AUTOMDM_VAR_DIGICERT_DATA_aLongerDigiCertCA}</data>"
		res := run(t, premium(), profile, []automdm.EJBCA{ejbcaCA("myCA")}, opts{
			enroll:      func(automdm.EJBCA) error { return errors.New("boom") },
			digiCert:    digiCert,
			digiCertCAs: []automdm.DigiCertCA{{Name: "aLongerDigiCertCA", CertificateCommonName: "cn", CertificateSeatID: "seat"}},
		})
		require.True(t, digiCert.GetCertificateFuncInvoked, "DigiCert has to be issued before the failure for this test to mean anything")
		assert.Len(t, res.failures, 2)
		assert.Empty(t, res.managed)
		assert.Empty(t, res.contents)
	})
}

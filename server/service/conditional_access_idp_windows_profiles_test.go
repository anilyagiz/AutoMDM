package service

import (
	"context"
	"crypto/sha1" //nolint:gosec // CSP CAThumbprint is defined as the SHA-1 hash
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"slices"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

const conditionalAccessSCEPInstallPrefix = "./User/Vendor/MSFT/ClientCertificateInstall/SCEP/$AUTOMDM_VAR_SCEP_WINDOWS_CERTIFICATE_ID/Install/"

// newConditionalAccessWindowsProfilesTestService returns a service whose datastore has everything the Windows
// profiles endpoint needs. Tests override individual mocks to exercise failures.
func newConditionalAccessWindowsProfilesTestService(t *testing.T, certPEM []byte, serverURL string) (*mock.Store, automdm.Service, context.Context) {
	ds := new(mock.Store)
	cfg := config.TestConfig()
	cfg.Server.PrivateKey = "test-private-key"
	svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName, _ sqlx.QueryerContext) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetConditionalAccessCACert: {Name: automdm.MDMAssetConditionalAccessCACert, Value: certPEM},
		}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{ServerURL: serverURL}}, nil
	}
	return ds, svc, test.UserContext(ctx, test.UserAdmin)
}

// xmlText decodes the raw inner XML of an element into its text.
func xmlText(t *testing.T, raw string) string {
	t.Helper()
	var s string
	require.NoError(t, xml.Unmarshal([]byte("<x>"+raw+"</x>"), &s))
	return s
}

// syncMLTargets maps each LocURI of a parsed profile to its decoded <Data>.
func syncMLTargets(t *testing.T, profile string) map[string]string {
	t.Helper()
	cmds, err := automdm.UnmarshallMultiTopLevelXMLProfile([]byte(profile))
	require.NoError(t, err)
	data := map[string]string{}
	for _, cmd := range cmds {
		data[cmd.GetTargetURI()] = xmlText(t, cmd.GetTargetData())
	}
	return data
}

// autoSelectRuleFromPolicyData decodes a Policy CSP <Data> value down to the Chromium rule it carries.
func autoSelectRuleFromPolicyData(t *testing.T, decodedData, elementID string) map[string]any {
	t.Helper()
	var payload struct {
		Enabled *struct{} `xml:"enabled"`
		Data    struct {
			ID    string `xml:"id,attr"`
			Value string `xml:"value,attr"`
		} `xml:"data"`
	}
	require.NoError(t, xml.Unmarshal([]byte("<x>"+decodedData+"</x>"), &payload))
	require.NotNil(t, payload.Enabled)
	require.Equal(t, elementID, payload.Data.ID)
	var rule map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload.Data.Value), &rule))
	return rule
}

func TestConditionalAccessGetIdPWindowsProfilesAuth(t *testing.T) {
	t.Parallel()
	_, svc, ctx := newConditionalAccessWindowsProfilesTestService(t, generateTestCertPEM(t), "https://automdm.example.com")

	testCases := []struct {
		name       string
		user       *automdm.User
		shouldFail bool
	}{
		{"global admin", test.UserAdmin, false},
		{"global maintainer", test.UserMaintainer, false},
		{"global observer", test.UserObserver, true},
		{"global observer+", test.UserObserverPlus, true},
		{"global gitops", test.UserGitOps, false},
		{"global technician", test.UserTechnician, false},
		{"team admin", test.UserTeamAdminTeam1, true},
		{"team maintainer", test.UserTeamMaintainerTeam1, true},
		{"team observer", test.UserTeamObserverTeam1, true},
		{"team observer+", test.UserTeamObserverPlusTeam1, true},
		{"team gitops", test.UserTeamGitOpsTeam1, true},
		{"team technician", test.UserTeamTechnicianTeam1, true},
		{"user no roles", test.UserNoRoles, true},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			profiles, err := svc.ConditionalAccessGetIdPWindowsProfiles(test.UserContext(ctx, tt.user))
			if tt.shouldFail {
				var forbiddenError *authz.Forbidden
				require.ErrorAs(t, err, &forbiddenError)
				require.Nil(t, profiles)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, profiles.SCEPProfile)
			require.NotEmpty(t, profiles.BrowserProfile)
		})
	}
}

func TestConditionalAccessGetIdPWindowsProfiles(t *testing.T) {
	t.Parallel()
	certPEM := generateTestCertPEM(t)
	block, _ := pem.Decode(certPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	thumbprint := sha1.Sum(caCert.Raw) //nolint:gosec // CSP CAThumbprint is defined as the SHA-1 hash

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		_, svc, ctx := newConditionalAccessWindowsProfilesTestService(t, certPEM, "https://automdm.example.com:8080")
		profiles, err := svc.ConditionalAccessGetIdPWindowsProfiles(ctx)
		require.NoError(t, err)

		// Both the SCEP URL and the challenge are left to delivery, so the profile uploads as copied.
		scepProfile := &automdm.MDMWindowsConfigProfile{Name: "Okta SCEP", SyncML: []byte(profiles.SCEPProfile)}
		require.NoError(t, scepProfile.ValidateUserProvided(false))
		foundVars, err := validateWindowsProfileAutoMDMVariables(profiles.SCEPProfile, &automdm.LicenseInfo{Tier: automdm.TierPremium}, nil)
		require.NoError(t, err)
		slices.Sort(foundVars)
		require.Equal(t, []string{
			string(automdm.AutoMDMVarCertificateRenewalID),
			string(automdm.AutoMDMVarConditionalAccessSCEPChallenge),
			string(automdm.AutoMDMVarConditionalAccessSCEPURL),
			string(automdm.AutoMDMVarHostUUID),
			string(automdm.AutoMDMVarSCEPWindowsCertificateID),
		}, foundVars)
		scepData := syncMLTargets(t, profiles.SCEPProfile)
		require.Equal(t, automdm.AutoMDMVarConditionalAccessSCEPChallenge.WithPrefix(), scepData[conditionalAccessSCEPInstallPrefix+"Challenge"])
		require.Equal(t, strings.ToUpper(hex.EncodeToString(thumbprint[:])), scepData[conditionalAccessSCEPInstallPrefix+"CAThumbprint"])

		// The browser profile uploads with no AutoMDM variables and sets the same Chromium rule the Apple profile writes.
		browserProfile := &automdm.MDMWindowsConfigProfile{Name: "Okta browsers", SyncML: []byte(profiles.BrowserProfile)}
		require.NoError(t, browserProfile.ValidateUserProvided(false))
		foundVars, err = validateWindowsProfileAutoMDMVariables(profiles.BrowserProfile, nil, nil)
		require.NoError(t, err)
		require.Empty(t, foundVars)

		// Windows can only set the policies once the ADMX that declares them is ingested.
		admxAt := strings.Index(profiles.BrowserProfile, "/ADMXInstall/")
		require.Positive(t, admxAt)
		require.Less(t, admxAt, strings.Index(profiles.BrowserProfile, "~Policy~automdmca/"))

		browserData := syncMLTargets(t, profiles.BrowserProfile)
		require.Equal(t, conditionalAccessWindowsADMX,
			browserData["./Device/Vendor/MSFT/Policy/ConfigOperations/ADMXInstall/AutoMDMConditionalAccess/Policy/AutoMDMConditionalAccessAdmx"])
		wantRule := map[string]any{
			"pattern": "https://okta.automdm.example.com:8080",
			"filter":  map[string]any{"SUBJECT": map[string]any{"CN": automdm.ConditionalAccessOktaCertificateCN}},
		}
		for browser, uri := range map[string]string{
			"Chrome": "./Device/Vendor/MSFT/Policy/Config/AutoMDMConditionalAccess~Policy~automdmca/ChromeAutoSelectCertificate",
			"Edge":   "./Device/Vendor/MSFT/Policy/Config/AutoMDMConditionalAccess~Policy~automdmca/EdgeAutoSelectCertificate",
		} {
			require.Contains(t, browserData, uri)
			require.Equal(t, wantRule, autoSelectRuleFromPolicyData(t, browserData[uri], browser+"AutoSelectCertificate_Value"), browser)
		}
	})

	// The shared preconditions are covered by the Apple profile tests; this one shows the Windows endpoint applies them.
	t.Run("free license", func(t *testing.T) {
		t.Parallel()
		ds := new(mock.Store)
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = "test-private-key"
		svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil)
		profiles, err := svc.ConditionalAccessGetIdPWindowsProfiles(test.UserContext(ctx, test.UserAdmin))
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.Nil(t, profiles)
	})
}

// Not parallel: it sets a dev mode override.
func TestConditionalAccessGetIdPWindowsProfilesEscaping(t *testing.T) {
	const mtlsURL = `https://okta.example.com/?a=<b>&c="d"`
	dev_mode.SetOverride("AUTOMDM_DEV_OKTA_SSO_SERVER_URL", mtlsURL, t)

	_, svc, ctx := newConditionalAccessWindowsProfilesTestService(t, generateTestCertPEM(t), "https://automdm.example.com")
	profiles, err := svc.ConditionalAccessGetIdPWindowsProfiles(ctx)
	require.NoError(t, err)

	browserData := syncMLTargets(t, profiles.BrowserProfile)
	rule := autoSelectRuleFromPolicyData(t,
		browserData["./Device/Vendor/MSFT/Policy/Config/AutoMDMConditionalAccess~Policy~automdmca/ChromeAutoSelectCertificate"],
		"ChromeAutoSelectCertificate_Value")
	require.Equal(t, mtlsURL, rule["pattern"])
	require.NoError(t, (&automdm.MDMWindowsConfigProfile{Name: "Okta browsers", SyncML: []byte(profiles.BrowserProfile)}).ValidateUserProvided(false))
}

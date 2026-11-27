package service

// AutoMDM Desktop (device endpoint) tests for the enterprise (premium) suite.
//
// Belongs here: device-authenticated endpoints and AutoMDM Desktop settings — device policies,
// transparency URL, alternative browser host, device SSO, and device authentication methods.
//
// Does not belong here: conditional access flags in device responses
// (integration_enterprise_conditional_access_test.go), self-service software and categories
// (integration_enterprise_software_test.go, integration_enterprise_software_installers_test.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func (s *integrationEnterpriseTestSuite) TestListDevicePolicies() {
	t := s.T()
	ctx := context.Background()

	// set the logo via the modify appconfig endpoint, so that the cache is
	// properly updated.
	var acResp appConfigResponse
	s.DoJSON("PATCH", "/api/latest/automdm/config",
		json.RawMessage(`{
		"org_info":{
			"org_logo_url_dark_mode": "http://example.com/logo",
			"contact_url": "http://example.com/contact"
		}
	}`), http.StatusOK, &acResp)
	require.Equal(t, "http://example.com/logo", acResp.OrgInfo.OrgLogoURLDarkMode)
	require.Equal(t, "http://example.com/contact", acResp.OrgInfo.ContactURL)

	team, err := s.ds.NewTeam(ctx, &automdm.Team{
		ID:          51,
		Name:        "team1-policies",
		Description: "desc team1",
	})
	require.NoError(t, err)

	token := "much_valid"
	host := createHostAndDeviceToken(t, s.ds, token)
	err = s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team.ID, []uint{host.ID}))
	require.NoError(t, err)

	qr, err := s.ds.NewQuery(ctx, &automdm.Query{
		Name:           "TestQueryEnterpriseGlobalPolicy",
		Description:    "Some description",
		Query:          "select * from osquery;",
		ObserverCanRun: true,
		Logging:        automdm.LoggingSnapshot,
	})
	require.NoError(t, err)

	// add a global policy
	gpParams := automdm.CreatePolicyRequest{
		Name:        qr.Name,
		Query:       qr.Query,
		Description: qr.Description,
		Resolution:  "some global resolution",
	}
	gpResp := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", gpParams, http.StatusOK, &gpResp)
	require.NotNil(t, gpResp.Policy)

	// add a policy execution
	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(ctx, host,
		map[uint]*bool{gpResp.Policy.ID: new(false)}, time.Now(), false, nil)))

	// add a policy to team
	oldToken := s.token
	t.Cleanup(func() {
		s.token = oldToken
	})

	password := test.GoodPassword
	email := "test_enterprise_policies@user.com"

	u := &automdm.User{
		Name:       "test team user",
		Email:      email,
		GlobalRole: nil,
		Teams: []automdm.UserTeam{
			{
				Team: *team,
				Role: automdm.RoleMaintainer,
			},
		},
	}

	require.NoError(t, u.SetPassword(password, 10, 10))
	_, err = s.ds.NewUser(ctx, u)
	require.NoError(t, err)

	s.token = s.getTestToken(email, password)
	tpParams := automdm.CreatePolicyRequest{
		TeamID:      &team.ID,
		Name:        "TestQueryEnterpriseTeamPolicy",
		Query:       "select * from osquery;",
		Description: "Some description",
		Resolution:  "some team resolution",
		Platform:    "darwin",
	}
	tpResp := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", tpParams, http.StatusOK, &tpResp)

	// add a hidden policy to the team, failing on the host
	hiddenResp := automdm.CreatePolicyResponse{}
	s.DoJSON("POST", "/api/latest/automdm/policies", automdm.CreatePolicyRequest{
		TeamID:   &team.ID,
		Name:     "TestQueryEnterpriseHiddenTeamPolicy",
		Query:    "select * from osquery;",
		Platform: "darwin",
		Hidden:   true,
	}, http.StatusOK, &hiddenResp)
	require.True(t, hiddenResp.Policy.Hidden)
	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(ctx, host,
		map[uint]*bool{hiddenResp.Policy.ID: new(false)}, time.Now(), false, nil)))

	// try with invalid token
	res := s.DoRawNoAuth("GET", "/api/latest/automdm/device/invalid_token/policies", nil, http.StatusUnauthorized)
	err = res.Body.Close()
	require.NoError(t, err)

	// asserts that a JSON-decoded policy from a device-authenticated endpoint
	// only contains device-safe fields, i.e. it never exposes the policy
	// author's identity nor the raw SQL query.
	assertDeviceSafePolicy := func(policy map[string]any) {
		require.NotContains(t, policy, "query")
		require.NotContains(t, policy, "author_id")
		require.NotContains(t, policy, "author_name")
		require.NotContains(t, policy, "author_email")
		require.Contains(t, policy, "name")
		require.Contains(t, policy, "response")
	}

	// GET `/api/_version_/automdm/device/{token}/policies`
	listDevicePoliciesResp := listDevicePoliciesResponse{}
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/policies", nil, http.StatusOK)
	rawBody, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	err = res.Body.Close()
	require.NoError(t, err)
	err = json.Unmarshal(rawBody, &listDevicePoliciesResp)
	require.NoError(t, err)
	// hidden policies are left out by default
	require.Len(t, listDevicePoliciesResp.Policies, 2)
	require.NoError(t, listDevicePoliciesResp.Err)
	for _, p := range listDevicePoliciesResp.Policies {
		require.NotEqual(t, hiddenResp.Policy.ID, p.ID)
	}
	// the response must not leak the policy author's identity nor the raw SQL query
	var rawPoliciesResp struct {
		Policies []map[string]any `json:"policies"`
	}
	err = json.Unmarshal(rawBody, &rawPoliciesResp)
	require.NoError(t, err)
	require.Len(t, rawPoliciesResp.Policies, 2)
	for _, policy := range rawPoliciesResp.Policies {
		assertDeviceSafePolicy(policy)
	}

	// GET `/api/_version_/automdm/device/{token}/policies?include_hidden_policies=true`
	listDevicePoliciesResp = listDevicePoliciesResponse{}
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/policies?include_hidden_policies=true", nil, http.StatusOK)
	err = json.NewDecoder(res.Body).Decode(&listDevicePoliciesResp)
	require.NoError(t, err)
	err = res.Body.Close()
	require.NoError(t, err)
	require.Len(t, listDevicePoliciesResp.Policies, 3)

	// GET `/api/_version_/automdm/device/{token}`
	getDeviceHostResp := getDeviceHostResponse{}
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token, nil, http.StatusOK)
	rawBody, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	err = res.Body.Close()
	require.NoError(t, err)
	err = json.Unmarshal(rawBody, &getDeviceHostResp)
	require.NoError(t, err)
	require.NoError(t, getDeviceHostResp.Err)
	require.Equal(t, host.ID, getDeviceHostResp.Host.ID)
	require.False(t, getDeviceHostResp.Host.RefetchRequested)
	require.Equal(t, "http://example.com/logo", getDeviceHostResp.OrgLogoURLDarkMode)
	require.Equal(t, "http://example.com/contact", getDeviceHostResp.OrgContactURL)
	// hidden policies are left out by default, but still counted
	require.Len(t, *getDeviceHostResp.Host.Policies, 2)
	for _, p := range *getDeviceHostResp.Host.Policies {
		require.NotEqual(t, hiddenResp.Policy.ID, p.ID)
	}
	require.Equal(t, uint64(2), getDeviceHostResp.Host.HostIssues.FailingPoliciesCount)
	require.NotNil(t, getDeviceHostResp.Host.HostIssues.FailingUnhiddenPoliciesCount)
	require.Equal(t, uint64(1), *getDeviceHostResp.Host.HostIssues.FailingUnhiddenPoliciesCount)
	require.NotNil(t, getDeviceHostResp.Host.HostIssues.HiddenPoliciesCount)
	require.Equal(t, uint64(1), *getDeviceHostResp.Host.HostIssues.HiddenPoliciesCount)
	require.False(t, getDeviceHostResp.GlobalConfig.Features.EnableSoftwareInventory)
	// the host's policies must not leak the policy author's identity nor the
	// raw SQL query
	var rawHostResp struct {
		Host struct {
			Policies []map[string]any `json:"policies"`
		} `json:"host"`
	}
	err = json.Unmarshal(rawBody, &rawHostResp)
	require.NoError(t, err)
	require.Len(t, rawHostResp.Host.Policies, 2)
	for _, policy := range rawHostResp.Host.Policies {
		assertDeviceSafePolicy(policy)
	}

	// GET `/api/_version_/automdm/device/{token}?include_hidden_policies=true`
	getDeviceHostResp = getDeviceHostResponse{}
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"?include_hidden_policies=true", nil, http.StatusOK)
	err = json.NewDecoder(res.Body).Decode(&getDeviceHostResp)
	require.NoError(t, err)
	err = res.Body.Close()
	require.NoError(t, err)
	require.Len(t, *getDeviceHostResp.Host.Policies, 3)
	var sawHidden bool
	for _, p := range *getDeviceHostResp.Host.Policies {
		sawHidden = sawHidden || p.ID == hiddenResp.Policy.ID
	}
	require.True(t, sawHidden)
	require.Equal(t, uint64(2), getDeviceHostResp.Host.HostIssues.FailingPoliciesCount)
	require.Equal(t, uint64(1), *getDeviceHostResp.Host.HostIssues.FailingUnhiddenPoliciesCount)
	require.Equal(t, uint64(1), *getDeviceHostResp.Host.HostIssues.HiddenPoliciesCount)

	// GET `/api/_version_/automdm/device/{token}/desktop`
	getDesktopResp := automdmDesktopResponse{}
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/desktop", nil, http.StatusOK)
	err = json.NewDecoder(res.Body).Decode(&getDesktopResp)
	require.NoError(t, err)
	err = res.Body.Close()
	require.NoError(t, err)
	require.NoError(t, getDesktopResp.Err)
	require.Equal(t, uint(2), *getDesktopResp.FailingPolicies)
	require.Equal(t, uint(1), *getDesktopResp.FailingUnhiddenPolicies)
	require.False(t, getDesktopResp.Notifications.NeedsMDMMigration)

	// update the team to enable software inventory
	team.Config.Features.EnableSoftwareInventory = true
	_, err = s.ds.SaveTeam(ctx, team)
	require.NoError(t, err)

	getDeviceHostResp = getDeviceHostResponse{}
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token, nil, http.StatusOK)
	err = json.NewDecoder(res.Body).Decode(&getDeviceHostResp)
	require.NoError(t, err)
	require.True(t, getDeviceHostResp.GlobalConfig.Features.EnableSoftwareInventory)

	// Transferring the host drops the old team's hidden failure from both device
	// endpoints right away; only the global failure remains.
	team2, err := s.ds.NewTeam(ctx, &automdm.Team{Name: "team2-policies"})
	require.NoError(t, err)
	require.NoError(t, s.ds.AddHostsToTeam(ctx, automdm.NewAddHostsToTeamParams(&team2.ID, []uint{host.ID})))

	checkCounts := func(wantPolicies int, wantTotal, wantUnhidden uint64) {
		getDeviceHostResp = getDeviceHostResponse{}
		res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"?include_hidden_policies=true", nil, http.StatusOK)
		require.NoError(t, json.NewDecoder(res.Body).Decode(&getDeviceHostResp))
		require.NoError(t, res.Body.Close())
		require.Len(t, *getDeviceHostResp.Host.Policies, wantPolicies)
		require.Equal(t, wantTotal, getDeviceHostResp.Host.HostIssues.FailingPoliciesCount)
		require.Equal(t, wantUnhidden, *getDeviceHostResp.Host.HostIssues.FailingUnhiddenPoliciesCount)

		getDesktopResp = automdmDesktopResponse{}
		res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/desktop", nil, http.StatusOK)
		require.NoError(t, json.NewDecoder(res.Body).Decode(&getDesktopResp))
		require.NoError(t, res.Body.Close())
		require.Equal(t, uint(wantTotal), *getDesktopResp.FailingPolicies)
		require.Equal(t, uint(wantUnhidden), *getDesktopResp.FailingUnhiddenPolicies)
	}
	checkCounts(1, 1, 1)

	// A hidden policy on the new team counts once the host reports a result for it.
	team2Hidden, err := s.ds.NewTeamPolicy(ctx, team2.ID, nil, automdm.PolicyPayload{
		Name: "TestQueryEnterpriseTeam2HiddenPolicy", Query: "select 1;", Hidden: true,
	})
	require.NoError(t, err)
	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(ctx, host,
		map[uint]*bool{team2Hidden.ID: new(false)}, time.Now(), false, nil)))
	checkCounts(2, 2, 1)
}

// TestCustomTransparencyURL tests that AutoMDM Premium licensees can use custom transparency urls.
func (s *integrationEnterpriseTestSuite) TestCustomTransparencyURL() {
	t := s.T()

	token := "token_test_custom_transparency_url"
	createHostAndDeviceToken(t, s.ds, token)

	// confirm intitial default url
	acResp := appConfigResponse{}
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.NotNil(t, acResp)
	require.Equal(t, automdm.DefaultTransparencyURL, acResp.AutoMDMDesktop.TransparencyURL)

	// confirm device endpoint returns initial default url
	deviceResp := &transparencyURLResponse{}
	rawResp := s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/transparency", nil, http.StatusTemporaryRedirect)
	json.NewDecoder(rawResp.Body).Decode(deviceResp) //nolint:errcheck
	rawResp.Body.Close()                             //nolint:errcheck
	require.NoError(t, deviceResp.Err)
	require.Equal(t, automdm.DefaultTransparencyURL, rawResp.Header.Get("Location"))

	// set custom url
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"transparency_url": "customURL"}}`), http.StatusOK, &acResp)
	require.NotNil(t, acResp)
	require.Equal(t, "customURL", acResp.AutoMDMDesktop.TransparencyURL)

	// device endpoint returns custom url
	deviceResp = &transparencyURLResponse{}
	rawResp = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/transparency", nil, http.StatusTemporaryRedirect)
	json.NewDecoder(rawResp.Body).Decode(deviceResp) //nolint:errcheck
	rawResp.Body.Close()                             //nolint:errcheck
	require.NoError(t, deviceResp.Err)
	require.Equal(t, "customURL", rawResp.Header.Get("Location"))

	// empty string applies default url
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"transparency_url": ""}}`), http.StatusOK, &acResp)
	require.NotNil(t, acResp)
	require.Equal(t, automdm.DefaultTransparencyURL, acResp.AutoMDMDesktop.TransparencyURL)

	// device endpoint returns default url
	deviceResp = &transparencyURLResponse{}
	rawResp = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/transparency", nil, http.StatusTemporaryRedirect)
	json.NewDecoder(rawResp.Body).Decode(deviceResp) //nolint:errcheck
	rawResp.Body.Close()                             //nolint:errcheck
	require.NoError(t, deviceResp.Err)
	require.Equal(t, automdm.DefaultTransparencyURL, rawResp.Header.Get("Location"))
}

func (s *integrationEnterpriseTestSuite) TestAutoMDMDesktopSettingsAlternativeBrowserHostURL() {
	t := s.T()

	token := "valid_token"
	createHostAndDeviceToken(t, s.ds, token)

	// set custom url
	acResp := appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"alternative_browser_host": "example.com"}}`), http.StatusOK, &acResp)
	require.NotNil(t, acResp)
	require.Equal(t, "example.com", acResp.AutoMDMDesktop.AlternativeBrowserHost)

	acResp = appConfigResponse{}
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.NotNil(t, acResp)
	require.Equal(t, "example.com", acResp.AutoMDMDesktop.AlternativeBrowserHost)

	// try setting invalid value
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"alternative_browser_host": "@:mon's_spagetti.com"}}`), http.StatusUnprocessableEntity, &acResp)

	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.NotNil(t, acResp)
	require.Equal(t, "example.com", acResp.AutoMDMDesktop.AlternativeBrowserHost)
}

func (s *integrationEnterpriseTestSuite) TestAutoMDMDesktopSettingsSSOEnabled() {
	t := s.T()

	t.Cleanup(func() {
		var acResp appConfigResponse
		s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"automdm_desktop": {"sso_enabled": false},
			"mdm": {"end_user_authentication": {"entity_id": "", "idp_name": "", "metadata": "", "metadata_url": ""}}
		}`), http.StatusOK, &acResp)
	})

	// The suite shares app config across tests, so start from a known
	// unconfigured state instead of relying on test order.
	appCfg, err := s.ds.AppConfig(t.Context())
	require.NoError(t, err)
	appCfg.AutoMDMDesktop.SSOEnabled = false
	appCfg.MDM.EndUserAuthentication = automdm.MDMEndUserAuthentication{}
	require.NoError(t, s.ds.SaveAppConfig(t.Context(), appCfg))

	// enabling without a configured IdP is rejected, naming the offending field
	res := s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"sso_enabled":true}}`), http.StatusUnprocessableEntity)
	name, reason := extractServerErrorNameReason(res.Body)
	require.Equal(t, "automdm_desktop.sso_enabled", name)
	require.Contains(t, reason, "Please configure it and try again.")

	var acResp appConfigResponse
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.False(t, acResp.AutoMDMDesktop.SSOEnabled)

	// setting the IdP and the flag in the same payload succeeds, since the
	// prerequisite is checked against the merged config
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"automdm_desktop": {"sso_enabled": true},
		"mdm": {"end_user_authentication": {
			"entity_id": "https://localhost:8080",
			"idp_name": "SimpleSAML",
			"metadata_url": "https://idp.example.com/metadata"
		}}
	}`), http.StatusOK, &acResp)
	require.True(t, acResp.AutoMDMDesktop.SSOEnabled)

	// the GET response rebuilds AutoMDMDesktopSettings field by field, so verify
	// the flag survives that path and not just the PATCH echo
	acResp = appConfigResponse{}
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.True(t, acResp.AutoMDMDesktop.SSOEnabled)

	s.lastActivityMatches(automdm.ActivityTypeEnabledSSOAutoMDMDesktop{}.ActivityName(), "", 0)
	lastID := s.lastActivityMatches("", "", 0)

	// a PATCH omitting sso_enabled leaves it on and emits no activity
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"org_info":{"org_name":"AutoMDM Desktop SSO"}}`), http.StatusOK, &acResp)
	require.True(t, acResp.AutoMDMDesktop.SSOEnabled)
	require.Equal(t, lastID, s.lastActivityMatches("", "", 0))

	// re-asserting the same value emits no activity either
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"sso_enabled":true}}`), http.StatusOK, &acResp)
	require.True(t, acResp.AutoMDMDesktop.SSOEnabled)
	require.Equal(t, lastID, s.lastActivityMatches("", "", 0))

	// reverse guard: the IdP can't be cleared while SSO is enabled
	res = s.Do("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"mdm": {"end_user_authentication": {"entity_id": "", "idp_name": "", "metadata": "", "metadata_url": ""}}
	}`), http.StatusUnprocessableEntity)
	name, reason = extractServerErrorNameReason(res.Body)
	require.Equal(t, "mdm.end_user_authentication", name)
	require.Contains(t, reason, "Please disable it and try again.")

	acResp = appConfigResponse{}
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.True(t, acResp.AutoMDMDesktop.SSOEnabled)
	require.False(t, acResp.MDM.EndUserAuthentication.IsEmpty())

	// disabling emits the disabled activity, and then the IdP can be cleared
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"sso_enabled":false}}`), http.StatusOK, &acResp)
	require.False(t, acResp.AutoMDMDesktop.SSOEnabled)
	s.lastActivityMatches(automdm.ActivityTypeDisabledSSOAutoMDMDesktop{}.ActivityName(), "", 0)

	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
		"mdm": {"end_user_authentication": {"entity_id": "", "idp_name": "", "metadata": "", "metadata_url": ""}}
	}`), http.StatusOK, &acResp)
	require.True(t, acResp.MDM.EndUserAuthentication.IsEmpty())
}

func (s *integrationEnterpriseTestSuite) TestDeviceSSOWithoutAppleMDM() {
	t := s.T()

	if _, ok := os.LookupEnv("SAML_IDP_TEST"); !ok {
		t.Skip("SSO tests are disabled")
	}

	var acResp appConfigResponse
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.False(t, acResp.MDM.EnabledAndConfigured)
	originalServerURL := acResp.ServerSettings.ServerURL

	createHostAndDeviceToken(t, s.ds, "device-sso-no-apple-mdm-token")

	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(fmt.Sprintf(`{
		"server_settings": { "server_url": "https://localhost:8080" },
		"mdm": {
			"end_user_authentication": {
				"entity_id": "mdm.test.com",
				"idp_name": "SimpleSAML",
				"metadata_url": "%s"
			}
		},
		"automdm_desktop": { "sso_enabled": true, "alternative_browser_host": "" }
	}`, testSAMLIDPMetadataURL)), http.StatusOK, &acResp)
	require.True(t, acResp.AutoMDMDesktop.SSOEnabled)
	require.Empty(t, acResp.AutoMDMDesktop.AlternativeBrowserHost)

	t.Cleanup(func() {
		s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(fmt.Sprintf(`{
			"server_settings": { "server_url": %q },
			"automdm_desktop": { "sso_enabled": false },
			"mdm": { "end_user_authentication": { "entity_id": "", "idp_name": "", "metadata_url": "" } }
		}`, originalServerURL)), http.StatusOK, &appConfigResponse{})
	})

	res := s.LoginDeviceSSOUser("sso_user", "user123#", "device-sso-no-apple-mdm-token")
	require.Equal(t, "https://localhost:8080/device/device-sso-no-apple-mdm-token", res.Header.Get("Location"))

	var sessionCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == cookieNameDeviceSSOSession {
			sessionCookie = c
		}
	}
	require.NotNil(t, sessionCookie, "expected a device SSO session even without Apple MDM configured")
	require.NotEmpty(t, sessionCookie.Value)
}

func (s *integrationEnterpriseTestSuite) TestDesktopEndpointWithInvalidPolicy() {
	t := s.T()

	token := "abcd123"
	host := createHostAndDeviceToken(t, s.ds, token)

	// Create an 'invalid' global policy for host
	admin := s.users["admin1@example.com"]
	err := s.ds.SaveUser(context.Background(), &admin)
	require.NoError(t, err)

	policy, err := s.ds.NewGlobalPolicy(context.Background(), &admin.ID, automdm.PolicyPayload{
		Query:       "SELECT 1 FROM table",
		Name:        "test",
		Description: "Some invalid Query",
		Resolution:  "",
		Platform:    host.Platform,
		Critical:    false,
	})
	require.NoError(t, err)
	require.NoError(t, errOnly(s.ds.RecordPolicyQueryExecutions(context.Background(), host, map[uint]*bool{policy.ID: nil}, time.Now(), false, nil)))

	// Any 'invalid' policies should be ignored.
	desktopRes := automdmDesktopResponse{}
	res := s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+token+"/desktop", nil, http.StatusOK)
	require.NoError(t, json.NewDecoder(res.Body).Decode(&desktopRes))
	require.NoError(t, res.Body.Close())
	require.NoError(t, desktopRes.Err)
	require.Equal(t, uint(0), *desktopRes.FailingPolicies)
}

func (s *integrationEnterpriseTestSuite) TestDeviceAuthenticationMethods() {
	t := s.T()
	ctx := context.Background()

	iosHost, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now(),
		OsqueryHostID:   new("ios-test-host"),
		NodeKey:         new("ios-test-node-key"),
		UUID:            "ios-test-uuid-12345",
		Hostname:        "ios-test-device",
		Platform:        "ios",
	})
	require.NoError(t, err)

	macHost, err := s.ds.NewHost(ctx, &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now(),
		OsqueryHostID:   new("mac-test-host"),
		NodeKey:         new("mac-test-node-key"),
		UUID:            "mac-test-uuid-67890",
		Hostname:        "mac-test-device",
		Platform:        "darwin",
	})
	require.NoError(t, err)

	macToken := "valid-mac-token"
	mysqltest.ExecAdhocSQL(t, s.ds, func(db sqlx.ExtContext) error {
		_, err := db.ExecContext(ctx, `INSERT INTO host_device_auth (host_id, token) VALUES (?, ?)`, macHost.ID, macToken)
		return err
	})

	t.Run("iOS device authenticates by UUID in the URL", func(t *testing.T) {
		var getHostResp getDeviceHostResponse
		res := s.DoRawNoAuth("GET", fmt.Sprintf("/api/latest/automdm/device/%s", iosHost.UUID), nil, http.StatusOK)
		require.NoError(t, json.NewDecoder(res.Body).Decode(&getHostResp))
		require.NoError(t, res.Body.Close())
		require.Equal(t, iosHost.ID, getHostResp.Host.ID)
		require.Equal(t, "ios", getHostResp.Host.Platform)
	})

	t.Run("iOS device with invalid UUID is rejected", func(t *testing.T) {
		res := s.DoRawNoAuth("GET", "/api/latest/automdm/device/invalid-uuid-does-not-exist", nil, http.StatusUnauthorized)
		res.Body.Close()
	})

	t.Run("macOS device with token auth", func(t *testing.T) {
		var getHostResp getDeviceHostResponse
		res := s.DoRawNoAuth("GET", fmt.Sprintf("/api/latest/automdm/device/%s", macToken), nil, http.StatusOK)
		require.NoError(t, json.NewDecoder(res.Body).Decode(&getHostResp))
		require.NoError(t, res.Body.Close())
		require.Equal(t, macHost.ID, getHostResp.Host.ID)
		require.Equal(t, "darwin", getHostResp.Host.Platform)
	})

	t.Run("macOS device UUID in URL should be rejected (not iOS/iPadOS)", func(t *testing.T) {
		res := s.DoRawNoAuth("GET", fmt.Sprintf("/api/latest/automdm/device/%s", macHost.UUID), nil, http.StatusUnauthorized)
		res.Body.Close()
	})

	t.Run("iOS device with token auth should be rejected", func(t *testing.T) {
		iosToken := "ios-device-token"
		mysqltest.ExecAdhocSQL(t, s.ds, func(db sqlx.ExtContext) error {
			_, err := db.ExecContext(ctx, `INSERT INTO host_device_auth (host_id, token) VALUES (?, ?)`, iosHost.ID, iosToken)
			return err
		})

		res := s.DoRawNoAuth("GET", fmt.Sprintf("/api/latest/automdm/device/%s", iosToken), nil, http.StatusUnauthorized)
		errMsg := extractServerErrorText(res.Body)
		require.Contains(t, errMsg, "Authentication required")
	})

	// X-Client-Cert-Serial used to select a separate authentication path that
	// skipped the iOS/iPadOS response scrub. The header must now be inert.
	t.Run("X-Client-Cert-Serial is ignored", func(t *testing.T) {
		devicePath := fmt.Sprintf("/api/latest/automdm/device/%s", iosHost.UUID)

		res := s.DoRawNoAuth("GET", devicePath, nil, http.StatusOK)
		plain, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		for _, serial := range []string{"1", "0", "not-a-number"} {
			res = s.DoRawWithHeaders("GET", devicePath, nil, http.StatusOK,
				map[string]string{"X-Client-Cert-Serial": serial})
			withHeader, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.JSONEq(t, string(plain), string(withHeader), "serial %q changed the response", serial)

			var getHostResp getDeviceHostResponse
			require.NoError(t, json.Unmarshal(withHeader, &getHostResp))
			require.Equal(t, iosHost.ID, getHostResp.Host.ID)
			require.Empty(t, getHostResp.Host.Hostname)
			require.Empty(t, getHostResp.Host.UUID)
			require.Empty(t, getHostResp.Host.HardwareSerial)
			require.Empty(t, getHostResp.Host.PrimaryMac)
			require.Nil(t, getHostResp.Host.Labels)
		}
	})

	t.Run("X-Client-Cert-Serial does not unlock token-only routes", func(t *testing.T) {
		headers := map[string]string{"X-Client-Cert-Serial": "1"}

		res := s.DoRawWithHeaders("POST", fmt.Sprintf("/api/latest/automdm/device/%s/debug/errors", iosHost.UUID),
			jsonMustMarshal(t, automdm.AutoMDMdError{ErrorSource: "test", ErrorMessage: "test"}), http.StatusForbidden, headers)
		res.Body.Close()

		res = s.DoRawWithHeaders("POST", fmt.Sprintf("/api/latest/automdm/device/%s/bypass_conditional_access", iosHost.UUID),
			nil, http.StatusForbidden, headers)
		res.Body.Close()
	})

	t.Run("X-Client-Cert-Serial does not break token auth", func(t *testing.T) {
		var getHostResp getDeviceHostResponse
		res := s.DoRawWithHeaders("GET", fmt.Sprintf("/api/latest/automdm/device/%s", macToken), nil, http.StatusOK,
			map[string]string{"X-Client-Cert-Serial": "1"})
		require.NoError(t, json.NewDecoder(res.Body).Decode(&getHostResp))
		require.NoError(t, res.Body.Close())
		require.Equal(t, macHost.ID, getHostResp.Host.ID)
	})
}

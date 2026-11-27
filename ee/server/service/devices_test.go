package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/WatchBeam/clock"
	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	hostctx "github.com/anilyagiz/automdm/v4/server/contexts/host"
	"github.com/anilyagiz/automdm/v4/server/datastore/redis/redistest"
	"github.com/anilyagiz/automdm/v4/server/mock"
	redismock "github.com/anilyagiz/automdm/v4/server/mock/redis"
	"github.com/anilyagiz/automdm/v4/server/sso"
	"github.com/stretchr/testify/require"
)

func newDeviceSSOTestService(t *testing.T, ds automdm.Datastore, sessionDuration time.Duration) (*Service, func(key string) *string, *clock.MockClock) {
	t.Helper()
	svc := newTestService(t, ds)
	mockClock := clock.NewMockClock()
	kvs, getKey := inMemoryKeyValueStore()
	svc.clock = mockClock
	svc.config = config.AutoMDMConfig{Session: config.SessionConfig{Duration: sessionDuration}}
	svc.keyValueStore = kvs
	return svc, getKey, mockClock
}

func TestCreateDeviceSSOSession(t *testing.T) {
	svc, getKey, mockClock := newDeviceSSOTestService(t, new(mock.Store), time.Hour)

	ctx := t.Context()
	host := &automdm.Host{ID: 42, UUID: "host-uuid-1"}

	sessionID, ttl, err := svc.createDeviceSSOSession(ctx, host, "idp-acct-uuid")
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)
	require.Equal(t, time.Hour, ttl)

	// Stored under the namespaced key, so it cannot collide with the other users
	// of the shared key/value store. Read back through the store rather than a
	// service method: the gate that consumes these sessions arrives with the
	// enforcement sub-issue.
	stored := getKey(deviceSSOSessionKeyPrefix + sessionID)
	require.NotNil(t, stored)

	var session automdm.DeviceSSOSession
	require.NoError(t, json.Unmarshal([]byte(*stored), &session))
	require.Equal(t, host.ID, session.HostID)
	require.Equal(t, "idp-acct-uuid", session.IdPAccountUUID)
	require.Equal(t, mockClock.Now().Add(time.Hour), session.ExpiresAt.UTC())
}

func TestDeviceSSOSessionExpires(t *testing.T) {
	svc, _, mockClock := newDeviceSSOTestService(t, new(mock.Store), time.Hour)

	ctx := t.Context()
	sessionID, _, err := svc.createDeviceSSOSession(ctx, &automdm.Host{ID: 42, UUID: "host-uuid-1"}, "idp-acct-uuid")
	require.NoError(t, err)

	// still valid just before the deadline
	mockClock.AddTime(time.Hour - time.Second)
	_, err = svc.validateDeviceSSOSession(ctx, sessionID)
	require.NoError(t, err)

	// the absolute deadline is enforced on read even though this store never
	// drops the key, so a store that stops honoring TTLs can't keep the session
	// alive.
	mockClock.AddTime(2 * time.Second)
	_, err = svc.validateDeviceSSOSession(ctx, sessionID)
	require.Error(t, err)
}

func TestDeviceSSOSessionUnknownIDNotFound(t *testing.T) {
	svc, _, _ := newDeviceSSOTestService(t, new(mock.Store), time.Hour)

	for _, sessionID := range []string{"", "does-not-exist"} {
		_, err := svc.validateDeviceSSOSession(t.Context(), sessionID)
		require.Error(t, err)
	}
}

func TestInitiateDeviceSSO(t *testing.T) {
	const devTok = "device-auth-token-abc123"

	idp := func(ac *automdm.AppConfig) {
		ac.MDM.EndUserAuthentication.SSOProviderSettings = automdm.SSOProviderSettings{
			EntityID: "automdm",
			IDPName:  "TestIDP",
			Metadata: mdmSSOTestMetadata,
		}
	}

	cases := []struct {
		name      string
		noHost    bool
		appConfig func() automdm.AppConfig
		wantErr   string
	}{
		{
			name:      "no host resolved from the device token",
			noHost:    true,
			wantErr:   "Authentication required",
			appConfig: func() automdm.AppConfig { return automdm.AppConfig{} },
		},
		{
			name:      "sso disabled",
			wantErr:   "is not enabled",
			appConfig: func() automdm.AppConfig { return automdm.AppConfig{} },
		},
		{
			name:    "sso enabled but no IdP configured",
			wantErr: "no IdP is configured",
			appConfig: func() automdm.AppConfig {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = true
				return ac
			},
		},
		{
			name:    "alternative browser host differs from the ACS host",
			wantErr: "same host",
			appConfig: func() automdm.AppConfig {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = true
				ac.AutoMDMDesktop.AlternativeBrowserHost = "proxy.example.com"
				ac.ServerSettings.ServerURL = "https://automdm.example.com"
				idp(&ac)
				return ac
			},
		},
		{
			name:    "custom Apple MDM URL differs from the server URL",
			wantErr: "same host",
			appConfig: func() automdm.AppConfig {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = true
				ac.ServerSettings.ServerURL = "https://automdm.example.com"
				ac.MDM.AppleServerURL = "https://mdm.example.com"
				idp(&ac)
				return ac
			},
		},
		{
			name: "no alternative browser host",
			appConfig: func() automdm.AppConfig {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = true
				ac.ServerSettings.ServerURL = "https://automdm.example.com"
				idp(&ac)
				return ac
			},
		},
		{
			name: "alternative browser host equals the server host",
			appConfig: func() automdm.AppConfig {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = true
				ac.AutoMDMDesktop.AlternativeBrowserHost = "automdm.example.com"
				ac.ServerSettings.ServerURL = "https://automdm.example.com"
				idp(&ac)
				return ac
			},
		},
		{
			name: "alternative browser host differs only by port",
			appConfig: func() automdm.AppConfig {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = true
				ac.AutoMDMDesktop.AlternativeBrowserHost = "automdm.example.com:8443"
				ac.ServerSettings.ServerURL = "https://automdm.example.com"
				idp(&ac)
				return ac
			},
		},
		{
			name: "apple server url equals the server url",
			appConfig: func() automdm.AppConfig {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = true
				ac.ServerSettings.ServerURL = "https://automdm.example.com"
				ac.MDM.AppleServerURL = "https://automdm.example.com"
				idp(&ac)
				return ac
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				ac := c.appConfig()
				return &ac, nil
			}

			svc, _, _ := newDeviceSSOTestService(t, ds, time.Hour)
			svc.ssoSessionStore = sso.NewSessionStore(redistest.NopRedis())

			ctx := t.Context()
			if !c.noHost {
				ctx = hostctx.NewContext(ctx, &automdm.Host{ID: 1, UUID: "host-uuid-1"})
			}

			initiation, err := svc.InitiateDeviceSSO(ctx, "/device/"+devTok)

			if c.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, c.wantErr)
				if c.noHost {
					var authRequired *automdm.AuthRequiredError
					require.ErrorAs(t, err, &authRequired)
				} else {
					var badReq *automdm.BadRequestError
					require.ErrorAs(t, err, &badReq)
				}
				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, initiation.IdPURL)

			idpURL, err := url.Parse(initiation.IdPURL)
			require.NoError(t, err)

			require.Equal(t,
				string(automdm.SSORelayState(automdm.SSOInitiatorAutoMDMDesktop)),
				idpURL.Query().Get("RelayState"))
			require.NotContains(t, idpURL.RawQuery, devTok)
		})
	}
}

func TestRequireDeviceSSOSession(t *testing.T) {
	host := &automdm.Host{ID: 1, UUID: "host-uuid-1", Platform: "darwin"}
	otherHost := &automdm.Host{ID: 2, UUID: "host-uuid-2", Platform: "darwin"}

	// sessionFor returns a session ID for h, or "" for the callers that stand in for
	// a browser with no cookie yet.
	type sessionFor func(t *testing.T, svc *Service, ctx context.Context) string
	noSession := func(*testing.T, *Service, context.Context) string { return "" }
	sessionOf := func(h *automdm.Host) sessionFor {
		return func(t *testing.T, svc *Service, ctx context.Context) string {
			sessionID, _, err := svc.createDeviceSSOSession(ctx, h, "idp-acct-uuid")
			require.NoError(t, err)
			return sessionID
		}
	}
	sessionOfWithIDP := func(h *automdm.Host, idpAcctUUID string) sessionFor {
		return func(t *testing.T, svc *Service, ctx context.Context) string {
			sessionID, _, err := svc.createDeviceSSOSession(ctx, h, idpAcctUUID)
			require.NoError(t, err)
			return sessionID
		}
	}

	cases := []struct {
		name                  string
		authnMethod           authz_ctx.AuthenticationMethod // defaults to none set
		ssoEnabled            bool
		platform              string // defaults to darwin
		awaitingConfiguration bool   // darwin only: the Setup Assistant flag
		setupExperienceStatus automdm.SetupExperienceStatusResultStatus
		session               sessionFor
		advanceClock          time.Duration
		deviceMappings        []*automdm.HostDeviceMapping // host_emails rows for the host
		idpAccounts           map[string]string            // email -> mdm_idp_accounts.uuid; absent means no such account

		wantSSORequired   bool
		wantMismatch      bool
		wantSessionLookup bool
	}{
		{
			name:       "setting off allows the request",
			ssoEnabled: false,
			session:    noSession,
		},
		{
			name:              "session bound to the host allows the request",
			ssoEnabled:        true,
			session:           sessionOf(host),
			wantSessionLookup: true,
		},
		{
			name:              "no session requires sso",
			ssoEnabled:        true,
			session:           noSession,
			wantSSORequired:   true,
			wantSessionLookup: true,
		},
		{
			name:              "expired session requires sso",
			ssoEnabled:        true,
			session:           sessionOf(host),
			advanceClock:      2 * time.Hour,
			wantSSORequired:   true,
			wantSessionLookup: true,
		},
		{
			name:              "session minted for another host requires sso",
			ssoEnabled:        true,
			session:           sessionOf(otherHost),
			wantSSORequired:   true,
			wantSessionLookup: true,
		},
		{
			name:                  "host in setup experience is exempt",
			ssoEnabled:            true,
			awaitingConfiguration: true,
			session:               noSession,
			wantSessionLookup:     true,
		},
		{
			name:                  "linux host mid setup experience is exempt",
			ssoEnabled:            true,
			platform:              "ubuntu",
			setupExperienceStatus: automdm.SetupExperienceStatusRunning,
			session:               noSession,
			wantSessionLookup:     true,
		},
		{
			name:                  "windows host mid setup experience is exempt",
			ssoEnabled:            true,
			platform:              "windows",
			setupExperienceStatus: automdm.SetupExperienceStatusPending,
			session:               noSession,
			wantSessionLookup:     true,
		},
		{
			name:                  "linux host past setup experience requires sso",
			ssoEnabled:            true,
			platform:              "ubuntu",
			setupExperienceStatus: automdm.SetupExperienceStatusSuccess,
			session:               noSession,
			wantSSORequired:       true,
			wantSessionLookup:     true,
		},
		{
			name:              "host with no emails is not validated against an IdP user",
			ssoEnabled:        true,
			authnMethod:       authz_ctx.AuthnDeviceURL,
			session:           sessionOfWithIDP(host, "nobody-uuid"),
			wantSessionLookup: true,
		},
		{
			name:        "host with no IdP-sourced email is not validated against an IdP user",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceURL,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "chrome@example.com", Source: automdm.DeviceMappingGoogleChromeProfiles},
				{Email: "custom@example.com", Source: automdm.DeviceMappingCustomReplacement},
			},
			idpAccounts:       map[string]string{"chrome@example.com": "chrome-uuid", "custom@example.com": "custom-uuid"},
			session:           sessionOfWithIDP(host, "nobody-uuid"),
			wantSessionLookup: true,
		},
		{
			name:        "session minted by the host's IdP user allows the request",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceURL,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "alice@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
			},
			idpAccounts:       map[string]string{"alice@example.com": "alice-uuid"},
			session:           sessionOfWithIDP(host, "alice-uuid"),
			wantSessionLookup: true,
		},
		{
			// A user assigned by hand through PUT /hosts/{id}/device_mapping is
			// stored with source "idp", which ListHostDeviceMapping translates to
			// mdm_idp_accounts in SQL, so it reaches the gate as one of these and
			// unlocks the page like an enrollment-time mapping does.
			name:        "manually assigned IdP user allows the request",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceURL,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "manual@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
			},
			idpAccounts:       map[string]string{"manual@example.com": "manual-uuid"},
			session:           sessionOfWithIDP(host, "manual-uuid"),
			wantSessionLookup: true,
		},
		{
			// Several IdP users on one host means any of them can open the page,
			// including the last one checked.
			name:        "any of the host's IdP users allows the request",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceURL,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "alice@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
				{Email: "bob@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
			},
			idpAccounts:       map[string]string{"alice@example.com": "alice-uuid", "bob@example.com": "bob-uuid"},
			session:           sessionOfWithIDP(host, "bob-uuid"),
			wantSessionLookup: true,
		},
		{
			// An email with no mdm_idp_accounts row can't match, but it must not
			// stop the remaining mappings from being checked.
			name:        "email with no IdP account does not shadow a later match",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceURL,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "ghost@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
				{Email: "alice@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
			},
			idpAccounts:       map[string]string{"alice@example.com": "alice-uuid"},
			session:           sessionOfWithIDP(host, "alice-uuid"),
			wantSessionLookup: true,
		},
		{
			name:        "session minted by another IdP user is rejected",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceURL,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "alice@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
				{Email: "bob@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
			},
			idpAccounts:       map[string]string{"alice@example.com": "alice-uuid", "bob@example.com": "bob-uuid"},
			session:           sessionOfWithIDP(host, "someone-elses-uuid"),
			wantMismatch:      true,
			wantSessionLookup: true,
		},
		{
			name:        "host whose only IdP email has no account is rejected",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceURL,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "ghost@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
			},
			session:           sessionOfWithIDP(host, "alice-uuid"),
			wantMismatch:      true,
			wantSessionLookup: true,
		},
		{
			// Device-token auth already proves possession of a device-bound secret,
			// so it skips the IdP comparison and its lockout modes.
			name:        "device token auth is not checked against the host's IdP users",
			ssoEnabled:  true,
			authnMethod: authz_ctx.AuthnDeviceToken,
			deviceMappings: []*automdm.HostDeviceMapping{
				{Email: "alice@example.com", Source: automdm.DeviceMappingMDMIdpAccounts},
			},
			idpAccounts:       map[string]string{"alice@example.com": "alice-uuid"},
			session:           sessionOfWithIDP(host, "someone-elses-uuid"),
			wantSessionLookup: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				var ac automdm.AppConfig
				ac.AutoMDMDesktop.SSOEnabled = c.ssoEnabled
				return &ac, nil
			}
			caseHost := *host
			ds.GetHostAwaitingConfigurationFunc = func(ctx context.Context, hostUUID string) (bool, error) {
				require.Equal(t, caseHost.UUID, hostUUID)
				return c.awaitingConfiguration, nil
			}
			ds.ListSetupExperienceResultsByHostUUIDFunc = func(ctx context.Context, hostUUID string, teamID uint) ([]*automdm.SetupExperienceStatusResult, error) {
				return []*automdm.SetupExperienceStatusResult{
					{Name: "install something", Status: c.setupExperienceStatus, HostUUID: hostUUID, SoftwareInstallerID: new(uint(1))},
				}, nil
			}
			ds.ListHostDeviceMappingFunc = func(ctx context.Context, id uint) ([]*automdm.HostDeviceMapping, error) {
				require.Equal(t, caseHost.ID, id)
				return c.deviceMappings, nil
			}
			ds.GetMDMIdPAccountByEmailFunc = func(ctx context.Context, email string) (*automdm.MDMIdPAccount, error) {
				uuid, ok := c.idpAccounts[email]
				if !ok {
					return nil, &notFoundError{}
				}
				return &automdm.MDMIdPAccount{UUID: uuid, Email: email}, nil
			}

			svc, getKey, mockClock := newDeviceSSOTestService(t, ds, time.Hour)
			ctx := t.Context()
			if c.authnMethod != 0 {
				authzCtx := &authz_ctx.AuthorizationContext{}
				authzCtx.SetAuthnMethod(c.authnMethod)
				ctx = authz_ctx.NewContext(ctx, authzCtx)
			}

			if c.platform != "" {
				caseHost.Platform = c.platform
				caseHost.OsqueryHostID = new("osquery-id")
			}

			sessionID := c.session(t, svc, ctx)
			mockClock.AddTime(c.advanceClock)

			err := svc.RequireDeviceSSOSession(ctx, &caseHost, sessionID)

			switch {
			case c.wantSSORequired:
				var ssoRequired *automdm.DeviceSSORequiredError
				require.ErrorAs(t, err, &ssoRequired)
			case c.wantMismatch:
				var badRequest *automdm.BadRequestError
				require.ErrorAs(t, err, &badRequest)
				require.Equal(t, "mismatched SSO user for this device", badRequest.Message)
			default:
				require.NoError(t, err)
			}

			// With the setting off nothing is looked up at all: no session read and
			// no setup experience query, so an AutoMDM running without the feature
			// pays nothing for it.
			if !c.wantSessionLookup {
				require.Nil(t, getKey(deviceSSOSessionKeyPrefix+sessionID))
				require.False(t, ds.GetHostAwaitingConfigurationFuncInvoked)
			}
		})
	}
}

func TestRequireDeviceSSOSessionSetupExperienceSurvivesStoreFailure(t *testing.T) {
	host := &automdm.Host{ID: 1, UUID: "host-uuid-1", Platform: "darwin"}

	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		var ac automdm.AppConfig
		ac.AutoMDMDesktop.SSOEnabled = true
		return &ac, nil
	}
	ds.GetHostAwaitingConfigurationFunc = func(ctx context.Context, hostUUID string) (bool, error) {
		return true, nil
	}

	svc, _, _ := newDeviceSSOTestService(t, ds, time.Hour)
	svc.keyValueStore = &redismock.KeyValueStore{
		GetFunc: func(ctx context.Context, key string) (*string, error) {
			return nil, errors.New("redis is down")
		},
	}

	require.NoError(t, svc.RequireDeviceSSOSession(t.Context(), host, ""))
}

func TestRequireDeviceSSOSessionMissingSetupExperienceRow(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		var ac automdm.AppConfig
		ac.AutoMDMDesktop.SSOEnabled = true
		return &ac, nil
	}
	// Hosts that never ran setup experience have no row at all.
	ds.GetHostAwaitingConfigurationFunc = func(ctx context.Context, hostUUID string) (bool, error) {
		return false, &notFoundError{}
	}

	svc, _, _ := newDeviceSSOTestService(t, ds, time.Hour)

	var ssoRequired *automdm.DeviceSSORequiredError
	require.ErrorAs(t, svc.RequireDeviceSSOSession(t.Context(), &automdm.Host{ID: 1, UUID: "host-uuid-1", Platform: "darwin"}, ""), &ssoRequired)
}

func TestRequireDeviceSSOSessionStoreFailure(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		var ac automdm.AppConfig
		ac.AutoMDMDesktop.SSOEnabled = true
		return &ac, nil
	}
	ds.GetHostAwaitingConfigurationFunc = func(ctx context.Context, hostUUID string) (bool, error) {
		return false, nil
	}

	svc, _, _ := newDeviceSSOTestService(t, ds, time.Hour)
	svc.keyValueStore = &redismock.KeyValueStore{
		GetFunc: func(ctx context.Context, key string) (*string, error) {
			return nil, errors.New("redis is down")
		},
	}

	err := svc.RequireDeviceSSOSession(t.Context(), &automdm.Host{ID: 1, UUID: "host-uuid-1", Platform: "darwin"}, "some-session")

	require.ErrorContains(t, err, "redis is down")
	var ssoRequired *automdm.DeviceSSORequiredError
	require.NotErrorAs(t, err, &ssoRequired, "a broken session store must not read as a prompt to sign in")
}

func TestHostNeedsBitLockerPINPrompt(t *testing.T) {
	createPIN := new(automdm.ActionRequiredCreatePIN)
	for _, tc := range []struct {
		name              string
		windowsEnabled    bool
		pinRequired       bool
		teamRequiresPIN   bool
		pinSet            bool
		capable           bool
		actionRequired    *automdm.ActionRequiredState
		wantPrompt        bool
		wantStateQueried  bool
		wantStatusQueried bool
	}{
		{name: "team does not require a PIN: no host queries", windowsEnabled: true},
		{name: "disk encryption off: no host queries", pinRequired: true},
		{name: "PIN already set: no host queries", windowsEnabled: true, pinRequired: true, pinSet: true},
		{name: "automdmd cannot apply a PIN: status not queried", windowsEnabled: true, pinRequired: true, wantStateQueried: true},
		{
			name: "host needs a PIN: prompt", windowsEnabled: true, pinRequired: true, capable: true, actionRequired: createPIN,
			wantPrompt: true, wantStateQueried: true, wantStatusQueried: true,
		},
		{
			// The global settings don't require a PIN, so a prompt proves the host's team settings were used.
			name: "team requires a PIN and the host needs one: prompt", teamRequiresPIN: true, capable: true, actionRequired: createPIN,
			wantPrompt: true, wantStateQueried: true, wantStatusQueried: true,
		},
		{
			name: "host is not asked for a PIN: no prompt", windowsEnabled: true, pinRequired: true, capable: true,
			wantStateQueried: true, wantStatusQueried: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			svc, _ := newTestServiceWithMock(t, ds)
			ds.TeamMDMConfigFunc = func(context.Context, uint) (*automdm.TeamMDM, error) {
				return &automdm.TeamMDM{WindowsSettings: automdm.WindowsSettings{EnableDiskEncryption: optjson.SetBool(true)}, RequireBitLockerPIN: true}, nil
			}
			ds.GetMDMWindowsHostConfigStateFunc = func(context.Context, string) (*automdm.MDMWindowsHostConfigState, error) {
				return &automdm.MDMWindowsHostConfigState{AutoMDMdBitLockerPINCapable: tc.capable}, nil
			}
			ds.GetMDMWindowsBitLockerStatusFunc = func(context.Context, *automdm.Host) (*automdm.HostMDMDiskEncryption, error) {
				return &automdm.HostMDMDiskEncryption{ActionRequired: tc.actionRequired}, nil
			}
			appCfg := &automdm.AppConfig{}
			appCfg.MDM.WindowsSettings.EnableDiskEncryption = optjson.SetBool(tc.windowsEnabled)
			appCfg.MDM.RequireBitLockerPIN = optjson.SetBool(tc.pinRequired)
			host := &automdm.Host{ID: 1, UUID: "win-uuid", Platform: "windows", TPMPINSet: tc.pinSet}
			if tc.teamRequiresPIN {
				host.TeamID = new(uint(7))
			}

			needsPIN, err := svc.hostNeedsBitLockerPINPrompt(t.Context(), host, appCfg)
			require.NoError(t, err)
			require.Equal(t, tc.wantPrompt, needsPIN)
			require.Equal(t, tc.wantStateQueried, ds.GetMDMWindowsHostConfigStateFuncInvoked, "enrollment row queried")
			require.Equal(t, tc.wantStatusQueried, ds.GetMDMWindowsBitLockerStatusFuncInvoked, "bitlocker status queried")
		})
	}
}

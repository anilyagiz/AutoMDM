package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/datastore/redis/redistest"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/sso"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSSOMetadata returns a valid SAML metadata XML for testing
func testSSOMetadata() string {
	return `<?xml version="1.0"?>
<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="test-idp">
  <md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <md:KeyDescriptor use="signing">
      <ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">
        <ds:X509Data>
          <ds:X509Certificate>MIIDXTCCAkWgAwIBAgIJALmVVuDWu4NYMA0GCSqGSIb3DQEBCwUAMEUxCzAJBgNVBAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5ldCBXaWRnaXRzIFB0eSBMdGQwHhcNMTYxMjMxMTQzNDQ3WhcNNDgwNjI1MTQzNDQ3WjBFMQswCQYDVQQGEwJBVTETMBEGA1UECAwKU29tZS1TdGF0ZTEhMB8GA1UECgwYSW50ZXJuZXQgV2lkZ2l0cyBQdHkgTHRkMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAzUCFozgNb1h1M0jzNRSCjhOBnR+uVbVpaWfXYIR+AhWDdEe5ryY+CgavOg8bfLybyzFdehlYdDRgkedEB/GjG8aJw06l0qF4jDOAw0kEygWCu2mcH7XOxRt+YAH3TVHa/Hu1W3WjzkobqqqLQ8gkKWWM27fOgAZ6GieaJBN6VBSMMcPey3HWLBmc+TYJmv1dbaO2jHhKh8pfKw0W12VM8P1PIO8gv4Phu/uuJYieBWKixBEyy0lHjyixYFCR12xdh4CA47q958ZRGnnDUGFVE1QhgRacJCOZ9bd5t9mr8KLaVBYTCJo5ERE8jymab5dPqe5qKfJsCZiqWglbjUo9twIDAQABo1AwTjAdBgNVHQ4EFgQUxpuwcs/CYQOyui+r1G+3KxBNhxkwHwYDVR0jBBgwFoAUxpuwcs/CYQOyui+r1G+3KxBNhxkwDAYDVR0TBAUwAwEB/zANBgkqhkiG9w0BAQsFAAOCAQEAAiWUKs/2x/viNCKi3Y6blEuCtAGhzOOZ9EjrvJ8+COH3Rag3tVBWrcBZ3/uhhPq5gy9lqw4OkvEws99/5jFsX1FJ6MKBgqfuy7yh5s1YfM0ANHYczMmYpZeAcQf2CGAaVfwTTfSlzNLsF2lW/ly7yapFzlYSJLGoVE+OHEu8g5SlNACUEfkXw+5Eghh+KzlIN7R6Q7r2ixWNFBC/jWf7NKUfJyX8qIG5md1YUeT6GBW9Bm2/1/RiO24JTaYlfLdKK9TYb8sG5B+OLab2DImG99CJ25RkAcSobWNF5zD0O6lgOo3cEdB/ksCq3hmtlC/DlLZ/D8CJ+7VuZnS1rR2naQ==</ds:X509Certificate>
        </ds:X509Data>
      </ds:KeyInfo>
    </md:KeyDescriptor>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.com/sso"/>
  </md:IDPSSODescriptor>
</md:EntityDescriptor>`
}

func TestSessionAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.ListSessionsForUserFunc = func(ctx context.Context, id uint) ([]*automdm.Session, error) {
		if id == 999 {
			return []*automdm.Session{
				{ID: 1, UserID: id, AccessedAt: time.Now()},
			}, nil
		}
		return nil, nil
	}
	ds.SessionByIDFunc = func(ctx context.Context, id uint) (*automdm.Session, error) {
		return &automdm.Session{ID: id, UserID: 999, AccessedAt: time.Now()}, nil
	}
	ds.DestroySessionFunc = func(ctx context.Context, ssn *automdm.Session) error {
		return nil
	}
	ds.MarkSessionAccessedFunc = func(ctx context.Context, ssn *automdm.Session) error {
		return nil
	}

	testCases := []struct {
		name            string
		user            *automdm.User
		shouldFailWrite bool
		shouldFailRead  bool
	}{
		{
			"global admin",
			&automdm.User{ID: 111, GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
			false,
		},
		{
			"global maintainer",
			&automdm.User{ID: 111, GlobalRole: ptr.String(automdm.RoleMaintainer)},
			true,
			true,
		},
		{
			"global observer",
			&automdm.User{ID: 111, GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
			true,
		},
		{
			"owner user",
			&automdm.User{ID: 999},
			false,
			false,
		},
		{
			"non-owner user",
			&automdm.User{ID: 888},
			true,
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			ds.MarkSessionAccessedFuncInvoked = false
			_, err := svc.GetInfoAboutSessionsForUser(ctx, 999)
			checkAuthErr(t, tt.shouldFailRead, err)
			assert.False(t, ds.MarkSessionAccessedFuncInvoked, "listing sessions must not refresh them")

			ds.MarkSessionAccessedFuncInvoked = false
			_, err = svc.GetInfoAboutSession(ctx, 1)
			checkAuthErr(t, tt.shouldFailRead, err)
			assert.False(t, ds.MarkSessionAccessedFuncInvoked, "inspecting a session must not refresh it")

			err = svc.DeleteSession(ctx, 1)
			checkAuthErr(t, tt.shouldFailWrite, err)
		})
	}
}

func TestAuthenticate(t *testing.T) {
	ds := mysqltest.CreateMySQLDS(t)
	defer ds.Close()

	svc, ctx := newTestService(t, ds, nil, nil)
	createTestUsers(t, ds)

	loginTests := []struct {
		name     string
		email    string
		password string
		wantErr  error
	}{
		{
			name:     "admin1",
			email:    testUsers["admin1"].Email,
			password: testUsers["admin1"].PlaintextPassword,
		},
		{
			name:     "user1",
			email:    testUsers["user1"].Email,
			password: testUsers["user1"].PlaintextPassword,
		},
	}

	for _, tt := range loginTests {
		t.Run(tt.email, func(st *testing.T) {
			loggedIn, token, err := svc.Login(test.UserContext(ctx, test.UserAdmin), tt.email, tt.password, false)
			require.Nil(st, err, "login unsuccessful")
			assert.Equal(st, tt.email, loggedIn.Email)
			assert.NotEmpty(st, token)

			sessions, err := svc.GetInfoAboutSessionsForUser(test.UserContext(ctx, test.UserAdmin), loggedIn.ID)
			require.Nil(st, err)
			require.Len(st, sessions, 1, "user should have one session")
			session := sessions[0]
			assert.NotZero(st, session.UserID)
			assert.WithinDuration(st, time.Now(), session.AccessedAt, 3*time.Second,
				"access time should be set with current time at session creation")
		})
	}
}

func TestMFA(t *testing.T) {
	ds := new(mock.Store)
	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)

	user := &automdm.User{MFAEnabled: true, Name: "Bob Smith", Email: "foo@example.com"}
	require.NoError(t, user.SetPassword(test.GoodPassword, 10, 10))
	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return user, nil
	}
	var failedLoginActivity bool
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		if activity.ActivityName() == (automdm.ActivityTypeUserFailedLogin{}).ActivityName() {
			failedLoginActivity = true
		}
		return nil
	}
	_, _, err := svc.Login(ctx, "foo@example.com", test.GoodPassword, false)
	var authErr *automdm.AuthFailedError
	require.ErrorAs(t, err, &authErr)
	require.Equal(t, "Authentication failed", err.Error())
	require.True(t, failedLoginActivity)

	var sentMail automdm.Email
	mailer := &mockMailService{SendEmailFn: func(e automdm.Email) error {
		sentMail = e
		return nil
	}}
	mfaToken := "foovalidate"
	ds.NewMFATokenFunc = func(ctx context.Context, userID uint) (string, error) {
		return mfaToken, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	innerSvc := &Service{
		ds:          ds,
		config:      config.TestConfig(),
		mailService: mailer,
	}
	var mfaRequestedActivity bool
	innerSvc.SetActivityService(&mock.MockActivityService{
		NewActivityFunc: func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			if activity.ActivityName() == (automdm.ActivityTypeUserMFARequested{}).ActivityName() {
				mfaRequestedActivity = true
			}
			return nil
		},
	})
	svcForMailing := validationMiddleware{innerSvc, ds, nil}
	_, _, err = svcForMailing.Login(ctx, "foo@example.com", test.GoodPassword, true)
	require.Equal(t, err, sendingMFAEmail)
	require.Equal(t, "foo@example.com", sentMail.To[0])
	require.Equal(t, "Log in to AutoMDM", sentMail.Subject)
	require.True(t, mfaRequestedActivity)

	var session *automdm.Session
	var mfaUser *automdm.User
	ds.SessionByMFATokenFunc = func(ctx context.Context, token string, sessionKeySize int) (*automdm.Session, *automdm.User, error) {
		if token == mfaToken {
			return session, mfaUser, nil
		}
		return nil, nil, &notFoundErr{}
	}
	resp, err := sessionCreateEndpoint(ctx, &sessionCreateRequest{Token: "foo"}, svc)
	require.NoError(t, err)
	require.NotNil(t, resp.Error())

	session = &automdm.Session{}
	mfaUser = user
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, user *activity_api.User, activity activity_api.ActivityDetails) error {
		require.Equal(t, mfaUser.Email, user.Email)
		require.Equal(t, automdm.ActivityTypeUserLoggedIn{}.ActivityName(), activity.ActivityName())
		return nil
	}
	resp, err = sessionCreateEndpoint(ctx, &sessionCreateRequest{Token: mfaToken}, svc)
	require.NoError(t, err)
	require.Nil(t, resp.Error())
	require.True(t, opts.ActivityMock.NewActivityFuncInvoked)
}

func TestGetSessionByKey(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	cfg := config.TestConfig()

	theSession := &automdm.Session{UserID: 123, Key: "abc"}

	ds.SessionByKeyFunc = func(ctx context.Context, key string) (*automdm.Session, error) {
		return theSession, nil
	}
	ds.DestroySessionFunc = func(ctx context.Context, ssn *automdm.Session) error {
		return nil
	}
	ds.MarkSessionAccessedFunc = func(ctx context.Context, ssn *automdm.Session) error {
		return nil
	}

	cases := []struct {
		desc     string
		accessed time.Duration
		apiOnly  bool
		fail     bool
	}{
		{"real user, accessed recently", -1 * time.Hour, false, false},
		{"real user, accessed too long ago", -(cfg.Session.Duration + time.Hour), false, true},
		{"api-only, accessed recently", -1 * time.Hour, true, false},
		{"api-only, accessed long ago", -(cfg.Session.Duration + time.Hour), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			var authErr *automdm.AuthRequiredError
			ds.SessionByKeyFuncInvoked, ds.DestroySessionFuncInvoked, ds.MarkSessionAccessedFuncInvoked = false, false, false

			theSession.AccessedAt = time.Now().Add(tc.accessed)
			theSession.APIOnly = ptr.Bool(tc.apiOnly)
			_, err := svc.GetSessionByKey(ctx, theSession.Key)
			if tc.fail {
				require.Error(t, err)
				require.ErrorAs(t, err, &authErr)
				require.True(t, ds.SessionByKeyFuncInvoked)
				require.True(t, ds.DestroySessionFuncInvoked)
				require.False(t, ds.MarkSessionAccessedFuncInvoked)
			} else {
				require.NoError(t, err)
				require.True(t, ds.SessionByKeyFuncInvoked)
				require.False(t, ds.DestroySessionFuncInvoked)
				require.True(t, ds.MarkSessionAccessedFuncInvoked)
			}
		})
	}
}

type testAuth struct {
	userID              string
	userDisplayName     string
	requestID           string
	assertionAttributes []automdm.SAMLAttribute
}

var _ automdm.Auth = (*testAuth)(nil)

func (a *testAuth) UserID() string {
	return a.userID
}

func (a *testAuth) UserDisplayName() string {
	return a.userDisplayName
}

func (a *testAuth) RequestID() string {
	return a.requestID
}

func (a *testAuth) AssertionAttributes() []automdm.SAMLAttribute {
	return a.assertionAttributes
}

func (a *testAuth) RawResponse() []byte {
	return nil
}

func (a *testAuth) AssertionID() string { return "" }

func (a *testAuth) AssertionNotOnOrAfter() time.Time { return time.Time{} }

func TestGetSSOUser(t *testing.T) {
	ds := new(mock.Store)
	var logs bytes.Buffer
	opts := &TestServerOpts{
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		License: &automdm.LicenseInfo{
			Tier: automdm.TierPremium,
		},
	}
	svc, ctx := newTestService(t, ds, nil, nil, opts)

	var roleActivities []activity_api.ActivityDetails
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		if isRoleChangeActivity(activity) {
			roleActivities = append(roleActivities, activity)
		}
		return nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			SSOSettings: &automdm.SSOSettings{
				EnableSSO:             true,
				EnableSSOIdPLogin:     true,
				EnableJITProvisioning: true,
			},
		}, nil
	}

	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return nil, newNotFoundError()
	}

	var newUser *automdm.User
	ds.NewUserFunc = func(ctx context.Context, user *automdm.User) (*automdm.User, error) {
		newUser = user
		return user, nil
	}

	auth := &testAuth{
		userID:          "foo@example.com",
		userDisplayName: "foo@example.com",
		requestID:       "foobar",
		assertionAttributes: []automdm.SAMLAttribute{
			{
				Name: "AUTOMDM_JIT_USER_ROLE_GLOBAL",
				Values: []automdm.SAMLAttributeValue{
					{Value: "admin"},
				},
			},
		},
	}

	// Test SSO login with a non-existent user.
	_, err := svc.GetSSOUser(ctx, auth)
	require.NoError(t, err)

	require.NotNil(t, newUser)
	require.NotNil(t, newUser.GlobalRole)
	require.Equal(t, "admin", *newUser.GlobalRole)
	require.Empty(t, newUser.Teams)
	require.Equal(t, []activity_api.ActivityDetails{
		automdm.ActivityTypeChangedUserGlobalRole{
			UserID:    newUser.ID,
			UserName:  "foo@example.com",
			UserEmail: "foo@example.com",
			Role:      "admin",
			JIT:       true,
		},
	}, roleActivities)

	// Test SSO login with the same (now existing) user (should update roles).

	// (1) Check that when a user's role attributes are unchanged then SavedUser is not called.

	ds.SaveUserFunc = func(ctx context.Context, user *automdm.User) error {
		return nil
	}

	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return newUser, nil
	}

	logs.Reset()
	_, err = svc.GetSSOUser(ctx, auth)
	require.NoError(t, err)

	require.False(t, ds.SaveUserFuncInvoked)

	// (2) Test SSO login with the same user with roles updated in its attributes.

	roleActivities = nil
	var savedUser *automdm.User
	ds.SaveUserFunc = func(ctx context.Context, user *automdm.User) error {
		savedUser = user
		return nil
	}

	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{ID: tid}, nil
	}

	auth.assertionAttributes = []automdm.SAMLAttribute{
		{
			Name: "AUTOMDM_JIT_USER_ROLE_TEAM_2",
			Values: []automdm.SAMLAttributeValue{
				{Value: "maintainer"},
			},
		},
	}

	_, err = svc.GetSSOUser(ctx, auth)
	require.NoError(t, err)

	require.NotNil(t, savedUser)
	require.Nil(t, savedUser.GlobalRole)
	require.Len(t, savedUser.Teams, 1)
	require.Equal(t, uint(2), savedUser.Teams[0].ID)
	require.Equal(t, "maintainer", savedUser.Teams[0].Role)

	require.True(t, ds.SaveUserFuncInvoked)
	require.Equal(t, []activity_api.ActivityDetails{
		automdm.ActivityTypeDeletedUserGlobalRole{
			UserID:    savedUser.ID,
			UserName:  "foo@example.com",
			UserEmail: "foo@example.com",
			OldRole:   "admin",
			JIT:       true,
		},
		automdm.ActivityTypeChangedUserTeamRole{
			UserID:    savedUser.ID,
			UserName:  "foo@example.com",
			UserEmail: "foo@example.com",
			Role:      "maintainer",
			TeamID:    2,
			JIT:       true,
		},
	}, roleActivities)

	// (3) Test existing user's role is not changed after a new login if EnableJITProvisioning is false.

	ds.SaveUserFuncInvoked = false

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			SSOSettings: &automdm.SSOSettings{
				EnableSSO:             true,
				EnableSSOIdPLogin:     true,
				EnableJITProvisioning: false,
			},
		}, nil
	}

	auth.assertionAttributes = []automdm.SAMLAttribute{
		{
			Name: "AUTOMDM_JIT_USER_ROLE_TEAM_2",
			Values: []automdm.SAMLAttributeValue{
				{Value: "admin"},
			},
		},
	}

	_, err = svc.GetSSOUser(ctx, auth)
	require.NoError(t, err)

	require.False(t, ds.SaveUserFuncInvoked)

	// (4) Test with invalid team ID in the attributes

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			SSOSettings: &automdm.SSOSettings{
				EnableSSO:             true,
				EnableSSOIdPLogin:     true,
				EnableJITProvisioning: true,
			},
		}, nil
	}

	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return nil, newNotFoundError()
	}

	auth.assertionAttributes = []automdm.SAMLAttribute{
		{
			Name: "AUTOMDM_JIT_USER_ROLE_TEAM_3",
			Values: []automdm.SAMLAttributeValue{
				{Value: "maintainer"},
			},
		},
	}

	_, err = svc.GetSSOUser(ctx, auth)
	require.Error(t, err)

	// (5) Test JIT provisioning with global technician role.

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			SSOSettings: &automdm.SSOSettings{
				EnableSSO:             true,
				EnableSSOIdPLogin:     true,
				EnableJITProvisioning: true,
			},
		}, nil
	}

	newUser = nil
	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return nil, newNotFoundError()
	}
	ds.NewUserFuncInvoked = false

	auth.assertionAttributes = []automdm.SAMLAttribute{
		{
			Name: "AUTOMDM_JIT_USER_ROLE_GLOBAL",
			Values: []automdm.SAMLAttributeValue{
				{Value: "technician"},
			},
		},
	}

	_, err = svc.GetSSOUser(ctx, auth)
	require.NoError(t, err)

	require.NotNil(t, newUser)
	require.NotNil(t, newUser.GlobalRole)
	require.Equal(t, automdm.RoleTechnician, *newUser.GlobalRole)
	require.Empty(t, newUser.Teams)

	// (6) Test JIT provisioning with team technician role.

	newUser = nil
	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return nil, newNotFoundError()
	}
	ds.NewUserFuncInvoked = false

	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{ID: tid}, nil
	}

	auth.assertionAttributes = []automdm.SAMLAttribute{
		{
			Name: "AUTOMDM_JIT_USER_ROLE_TEAM_1",
			Values: []automdm.SAMLAttributeValue{
				{Value: "technician"},
			},
		},
	}

	_, err = svc.GetSSOUser(ctx, auth)
	require.NoError(t, err)

	require.NotNil(t, newUser)
	require.Nil(t, newUser.GlobalRole)
	require.Len(t, newUser.Teams, 1)
	require.Equal(t, uint(1), newUser.Teams[0].ID)
	require.Equal(t, automdm.RoleTechnician, newUser.Teams[0].Role)
}

func TestInitiateSSOWithSSOServerURL(t *testing.T) {
	ds := new(mock.Store)
	pool := redistest.NopRedis()

	svc, ctx := newTestServiceWithConfig(t, ds, config.TestConfig(), nil, nil, &TestServerOpts{
		Pool: pool,
	})

	// Mock app config with SSO server URL
	appConfig := &automdm.AppConfig{
		ServerSettings: automdm.ServerSettings{
			ServerURL: "https://automdm.example.com",
		},
		SSOSettings: &automdm.SSOSettings{
			EnableSSO:    true,
			SSOServerURL: "https://admin.automdm.example.com",
			SSOProviderSettings: automdm.SSOProviderSettings{
				EntityID: "automdm",
				IDPName:  "TestIDP",
				Metadata: testSSOMetadata(),
			},
		},
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return appConfig, nil
	}

	// Test that ACS URL uses SSO URL
	sessionID, _, idpURL, err := svc.InitiateSSO(ctx, "/dashboard")
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)
	require.NotEmpty(t, idpURL)

	// The ACS URL should use the SSO server URL
	// We can't directly test the ACS URL in the SAML request here since it's embedded in the XML,
	// but the integration test verifies this works correctly
}

func TestInitiateSSOACSURLWithURLPrefix(t *testing.T) {
	// With url_prefix set, the ACS callback URL must carry the subpath exactly
	// once, regardless of whether server_url was configured with or without the
	// subpath. The latter is the configuration older deployments may have used.
	testCases := []struct {
		name      string
		serverURL string
	}{
		{
			name:      "server_url includes the subpath",
			serverURL: "https://automdm.example.com/apps/automdm",
		},
		{
			name:      "server_url omits the subpath",
			serverURL: "https://automdm.example.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			pool := redistest.NopRedis()

			cfg := config.TestConfig()
			cfg.Server.URLPrefix = "/apps/automdm"

			svc, ctx := newTestServiceWithConfig(t, ds, cfg, nil, nil, &TestServerOpts{
				Pool: pool,
			})

			appConfig := &automdm.AppConfig{
				ServerSettings: automdm.ServerSettings{
					ServerURL: tc.serverURL,
				},
				SSOSettings: &automdm.SSOSettings{
					EnableSSO: true,
					SSOProviderSettings: automdm.SSOProviderSettings{
						EntityID: "automdm",
						IDPName:  "TestIDP",
						Metadata: testSSOMetadata(),
					},
				},
			}
			ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
				return appConfig, nil
			}

			_, _, idpURL, err := svc.InitiateSSO(ctx, "/dashboard")
			require.NoError(t, err)
			require.NotEmpty(t, idpURL)

			parsed, err := url.Parse(idpURL)
			require.NoError(t, err)
			encoded := parsed.Query().Get("SAMLRequest")
			require.NotEmpty(t, encoded)

			authReq := inflate(t, encoded)
			require.NotNil(t, authReq.AssertionConsumerServiceURL)
			require.Equal(t,
				"https://automdm.example.com/apps/automdm/api/v1/automdm/sso/callback",
				authReq.AssertionConsumerServiceURL,
			)
		})
	}
}

func TestInitiateSSOWithTrailingSlash(t *testing.T) {
	ds := new(mock.Store)
	pool := redistest.NopRedis()

	svc, ctx := newTestServiceWithConfig(t, ds, config.TestConfig(), nil, nil, &TestServerOpts{
		Pool: pool,
	})

	testCases := []struct {
		name         string
		serverURL    string
		ssoServerURL string
	}{
		{
			name:         "server URL with trailing slash",
			serverURL:    "https://automdm.example.com/",
			ssoServerURL: "",
		},
		{
			name:         "SSO server URL with trailing slash",
			serverURL:    "https://automdm.example.com",
			ssoServerURL: "https://admin.automdm.example.com/",
		},
		{
			name:         "both URLs with trailing slash",
			serverURL:    "https://automdm.example.com/",
			ssoServerURL: "https://admin.automdm.example.com/",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Mock app config
			appConfig := &automdm.AppConfig{
				ServerSettings: automdm.ServerSettings{
					ServerURL: tc.serverURL,
				},
				SSOSettings: &automdm.SSOSettings{
					EnableSSO:    true,
					SSOServerURL: tc.ssoServerURL,
					SSOProviderSettings: automdm.SSOProviderSettings{
						EntityID: "automdm",
						IDPName:  "TestIDP",
						Metadata: testSSOMetadata(),
					},
				},
			}

			ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
				return appConfig, nil
			}

			// Test that InitiateSSO works
			sessionID, _, idpURL, err := svc.InitiateSSO(ctx, "/dashboard")
			require.NoError(t, err)
			require.NotEmpty(t, sessionID)
			require.NotEmpty(t, idpURL)
		})
	}
}

// The relay URL is where the browser lands after the IdP callback, so only a
// path on the AutoMDM origin is acceptable; anything carrying a host would turn
// the login flow into a redirect off-site.
func TestInitiateSSORelayURLMustBeRelativePath(t *testing.T) {
	ds := new(mock.Store)
	pool := redistest.NopRedis()
	svc, ctx := newTestServiceWithConfig(t, ds, config.TestConfig(), nil, nil, &TestServerOpts{Pool: pool})
	ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			ServerSettings: automdm.ServerSettings{ServerURL: "https://automdm.example.com"},
			SSOSettings: &automdm.SSOSettings{
				EnableSSO: true,
				EntityID:  "automdm",
				IDPName:   "TestIDP",
				Metadata:  testSSOMetadata(),
			},
		}, nil
	}

	accepted := []string{"", "/", "/dashboard", "/hosts/manage?order_key=hostname#top"}
	for _, relayURL := range accepted {
		t.Run("accepts "+relayURL, func(t *testing.T) {
			_, _, _, err := svc.InitiateSSO(ctx, relayURL)
			require.NoError(t, err)
		})
	}

	rejected := []string{
		"https://evil.example/login",
		"http://automdm.example.com/dashboard", // absolute even when same host: the API documents a relative URL
		"//evil.example/login",
		`/\evil.example/login`,
		"javascript:alert(1)",
		"dashboard",
		"\\\\evil.example",
	}
	for _, relayURL := range rejected {
		t.Run("rejects "+relayURL, func(t *testing.T) {
			_, _, _, err := svc.InitiateSSO(ctx, relayURL)
			require.Error(t, err)
			var badReq *automdm.BadRequestError
			require.ErrorAs(t, err, &badReq)
		})
	}
}

// An accepted relay URL must survive into the stored SSO session unchanged,
// since that is the value the callback redirects to.
func TestInitiateSSOStoresRelayURL(t *testing.T) {
	ds := new(mock.Store)
	pool := redistest.SetupRedis(t, t.Name(), false, false, false)
	svc, ctx := newTestServiceWithConfig(t, ds, config.TestConfig(), nil, nil, &TestServerOpts{Pool: pool})
	ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			ServerSettings: automdm.ServerSettings{ServerURL: "https://automdm.example.com"},
			SSOSettings: &automdm.SSOSettings{
				EnableSSO: true,
				EntityID:  "automdm",
				IDPName:   "TestIDP",
				Metadata:  testSSOMetadata(),
			},
		}, nil
	}

	const relayURL = "/hosts/manage?order_key=hostname#top"
	sessionID, _, _, err := svc.InitiateSSO(ctx, relayURL)
	require.NoError(t, err)

	// Read the session back through a store on the same pool, which is what the
	// test service was built with (see newTestServiceWithConfig).
	session, err := sso.NewSessionStore(pool).Fullfill(sessionID)
	require.NoError(t, err)
	require.Equal(t, relayURL, session.OriginalURL)
}

func TestInitiateSSOWithInvalidURL(t *testing.T) {
	ds := new(mock.Store)
	pool := redistest.NopRedis()

	svc, ctx := newTestServiceWithConfig(t, ds, config.TestConfig(), nil, nil, &TestServerOpts{
		Pool: pool,
	})

	// Mock app config with invalid URL
	appConfig := &automdm.AppConfig{
		ServerSettings: automdm.ServerSettings{
			ServerURL: "not-a-valid-url://%%%",
		},
		SSOSettings: &automdm.SSOSettings{
			EnableSSO: true,
			SSOProviderSettings: automdm.SSOProviderSettings{
				EntityID: "automdm",
				IDPName:  "TestIDP",
				Metadata: testSSOMetadata(),
			},
		},
	}

	ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		return appConfig, nil
	}

	// Test that invalid URL returns bad request error
	_, _, _, err := svc.InitiateSSO(ctx, "/dashboard")
	require.Error(t, err)

	// Verify it's a bad request error
	var badReqErr *automdm.BadRequestError
	require.ErrorAs(t, err, &badReqErr)
	require.Contains(t, badReqErr.Message, "invalid SSO URL")
}

func TestDecodeCallbackRequestSAMLResponseSizeCap(t *testing.T) {
	// The SSO callbacks read SAMLResponse from FormValue, which covers both the
	// POST body and the URL query string. WithRequestBodySizeLimit only bounds
	// the body, so the value-level cap must reject an oversized query argument.
	t.Run("oversized SAMLResponse in query string is rejected", func(t *testing.T) {
		oversized := strings.Repeat("A", int(automdm.MaxSSOCallbackSize)+1)
		r := httptest.NewRequest("POST", "/api/v1/automdm/sso/callback?SAMLResponse="+oversized, nil)

		_, _, _, err := decodeCallbackRequest(t.Context(), r)
		require.Error(t, err)
		var bre *automdm.BadRequestError
		require.ErrorAs(t, err, &bre)
		require.Contains(t, bre.Message, "too large")
	})

	t.Run("normally-sized SAMLResponse passes the size check", func(t *testing.T) {
		small := base64.StdEncoding.EncodeToString([]byte("<x/>"))
		form := url.Values{"SAMLResponse": {small}}
		r := httptest.NewRequest("POST", "/api/v1/automdm/sso/callback", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		_, decoded, _, err := decodeCallbackRequest(t.Context(), r)
		require.NoError(t, err)
		require.Equal(t, "<x/>", string(decoded))
	})
}

func TestDecodeCallbackRequestRelayState(t *testing.T) {
	postWith := func(t *testing.T, form url.Values) automdm.SSORelayState {
		t.Helper()
		form.Set("SAMLResponse", base64.StdEncoding.EncodeToString([]byte("<x/>")))
		r := httptest.NewRequest("POST", "/api/v1/automdm/mdm/sso/callback", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		_, _, relayState, err := decodeCallbackRequest(t.Context(), r)
		require.NoError(t, err)
		return relayState
	}

	t.Run("a recognized value survives the form round trip", func(t *testing.T) {
		require.Equal(t,
			automdm.SSORelayState(automdm.SSOInitiatorAutoMDMDesktop),
			postWith(t, url.Values{"RelayState": {automdm.SSOInitiatorAutoMDMDesktop}}))
	})

	t.Run("an absent RelayState decodes to empty", func(t *testing.T) {
		require.Empty(t, postWith(t, url.Values{}))
	})
}

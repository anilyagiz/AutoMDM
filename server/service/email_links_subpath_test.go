package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/WatchBeam/clock"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/mail"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/require"
)

// These tests cover transactional email links in subpath deployments. The
// server URL already carries the subpath, so the link base URL must equal the
// server URL exactly. Re-appending the URL prefix duplicates the subpath and
// produces links that 404.

const (
	subpathServerURL = "https://acme.co/subpath"
	subpathPrefix    = "/subpath"
)

func subpathTestConfig() config.AutoMDMConfig {
	cfg := config.TestConfig()
	cfg.Server.URLPrefix = subpathPrefix
	return cfg
}

func TestEmailLinkBaseURL(t *testing.T) {
	for _, tc := range []struct {
		name      string
		serverURL string
		urlPrefix string
		want      string
	}{
		{"no prefix", "https://acme.co", "", "https://acme.co"},
		{"prefix in server url only", "https://acme.co/subpath", "/subpath", "https://acme.co/subpath"},
		{"prefix in url prefix only", "https://acme.co", "/subpath", "https://acme.co/subpath"},
		{"prefix in url prefix, server url trailing slash", "https://acme.co/", "/subpath", "https://acme.co/subpath"},
		{"prefix in both", "https://acme.co/subpath", "/subpath", "https://acme.co/subpath"},
		{"prefix in both, trailing slash", "https://acme.co/subpath/", "/subpath", "https://acme.co/subpath/"},
		{"distinct prefix not yet present", "https://acme.co", "/apps/automdm", "https://acme.co/apps/automdm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, string(emailLinkBaseURL(tc.serverURL, tc.urlPrefix)))
		})
	}
}

func TestInviteNewUserEmailLinkSubpath(t *testing.T) {
	ds := new(mock.Store)
	ds.UserByEmailFunc = mock.UserWithEmailNotFound()
	ds.NewInviteFunc = func(ctx context.Context, i *automdm.Invite) (*automdm.Invite, error) { return i, nil }
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{ServerURL: subpathServerURL}}, nil
	}

	var sent automdm.Email
	mailer := &mockMailService{SendEmailFn: func(e automdm.Email) error { sent = e; return nil }}

	svc := &Service{
		ds:          ds,
		config:      subpathTestConfig(),
		mailService: mailer,
		clock:       clock.NewMockClock(),
		authz:       authz.Must(),
		logger:      discardLogger(),
	}

	_, err := svc.InviteNewUser(test.UserContext(t.Context(), test.UserAdmin), automdm.InvitePayload{
		Email: new("user@acme.co"),
	})
	require.NoError(t, err)
	require.True(t, mailer.Invoked)

	m, ok := sent.Mailer.(*mail.InviteMailer)
	require.True(t, ok)
	require.Equal(t, subpathServerURL, string(m.BaseURL))
}

func TestRequestPasswordResetEmailLinkSubpath(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			ServerSettings: automdm.ServerSettings{ServerURL: subpathServerURL},
			SMTPSettings:   &automdm.SMTPSettings{SMTPConfigured: true},
		}, nil
	}
	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return &automdm.User{ID: 1, Email: email}, nil
	}
	ds.NewPasswordResetRequestFunc = func(ctx context.Context, req *automdm.PasswordResetRequest) (*automdm.PasswordResetRequest, error) {
		return req, nil
	}

	var sent automdm.Email
	mailer := &mockMailService{SendEmailFn: func(e automdm.Email) error { sent = e; return nil }}

	svc := &Service{
		ds:          ds,
		config:      subpathTestConfig(),
		mailService: mailer,
		clock:       clock.NewMockClock(),
		authz:       authz.Must(),
		logger:      discardLogger(),
	}

	require.NoError(t, svc.RequestPasswordReset(t.Context(), "user@acme.co"))
	require.True(t, mailer.Invoked)

	m, ok := sent.Mailer.(*mail.PasswordResetMailer)
	require.True(t, ok)
	require.Equal(t, subpathServerURL, string(m.BaseURL))
}

func TestModifyEmailAddressLinkSubpath(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{ServerURL: subpathServerURL}}, nil
	}
	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return nil, sql.ErrNoRows
	}
	ds.InviteByEmailFunc = func(ctx context.Context, email string) (*automdm.Invite, error) {
		return nil, sql.ErrNoRows
	}
	ds.PendingEmailChangeFunc = func(ctx context.Context, userID uint, newEmail, token string) error {
		return nil
	}

	var sent automdm.Email
	mailer := &mockMailService{SendEmailFn: func(e automdm.Email) error { sent = e; return nil }}

	svc := &Service{
		ds:          ds,
		config:      subpathTestConfig(),
		mailService: mailer,
		clock:       clock.NewMockClock(),
		authz:       authz.Must(),
		logger:      discardLogger(),
	}

	user := &automdm.User{ID: 1, Email: "old@acme.co"}
	require.NoError(t, svc.modifyEmailAddress(t.Context(), user, "new@acme.co", nil))
	require.True(t, mailer.Invoked)

	m, ok := sent.Mailer.(*mail.ChangeEmailMailer)
	require.True(t, ok)
	require.Equal(t, subpathServerURL, string(m.BaseURL))
}

func TestMakeMFAEmailLinkSubpath(t *testing.T) {
	ds := new(mock.Store)
	ds.NewMFATokenFunc = func(ctx context.Context, userID uint) (string, error) { return "mfa-token", nil }
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{ServerURL: subpathServerURL}}, nil
	}

	var sent automdm.Email
	mailer := &mockMailService{SendEmailFn: func(e automdm.Email) error { sent = e; return nil }}

	svc := &Service{
		ds:          ds,
		config:      subpathTestConfig(),
		mailService: mailer,
		clock:       clock.NewMockClock(),
		authz:       authz.Must(),
		logger:      discardLogger(),
	}

	require.NoError(t, svc.makeMFAEmail(t.Context(), automdm.User{ID: 1, Name: "Bob", Email: "bob@acme.co"}))
	require.True(t, mailer.Invoked)

	m, ok := sent.Mailer.(*mail.MFAMailer)
	require.True(t, ok)
	require.Equal(t, subpathServerURL, string(m.BaseURL))
}

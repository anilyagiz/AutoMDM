package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/WatchBeam/clock"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v3"
)

func TestInviteNewUserMock(t *testing.T) {
	ms := new(mock.Store)
	ms.UserByEmailFunc = mock.UserWithEmailNotFound()
	ms.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{ServerURL: "https://acme.co"}}, nil
	}

	ms.NewInviteFunc = func(ctx context.Context, i *automdm.Invite) (*automdm.Invite, error) {
		return i, nil
	}
	mailer := &mockMailService{SendEmailFn: func(e automdm.Email) error { return nil }}

	svc := validationMiddleware{&Service{
		ds:          ms,
		config:      config.TestConfig(),
		mailService: mailer,
		clock:       clock.NewMockClock(),
		authz:       authz.Must(),
	}, ms, nil}

	payload := automdm.InvitePayload{
		Email: ptr.String("user@acme.co"),
	}

	payload.SSOEnabled = ptr.Bool(true)
	payload.MFAEnabled = ptr.Bool(true)
	_, err := svc.InviteNewUser(test.UserContext(context.Background(), test.UserAdmin), payload)
	require.Error(t, err)

	payload.SSOEnabled = nil
	_, err = svc.InviteNewUser(test.UserContext(context.Background(), test.UserAdmin), payload)
	require.ErrorContains(t, err, "license")

	// happy path
	invite, err := svc.InviteNewUser(license.NewContext(test.UserContext(context.Background(), test.UserAdmin), &automdm.LicenseInfo{Tier: automdm.TierPremium}), payload)
	require.Nil(t, err)
	assert.Equal(t, test.UserAdmin.ID, invite.InvitedBy)
	assert.True(t, ms.NewInviteFuncInvoked)
	assert.True(t, ms.AppConfigFuncInvoked)
	assert.True(t, mailer.Invoked)

	ms.UserByEmailFunc = mock.UserByEmailWithUser(new(automdm.User))
	_, err = svc.InviteNewUser(test.UserContext(context.Background(), test.UserAdmin), payload)
	require.NotNil(t, err, "should err if the user we're inviting already exists")
}

func TestUpdateInvite(t *testing.T) {
	ms := new(mock.Store)
	ms.InviteFunc = func(ctx context.Context, id uint) (*automdm.Invite, error) {
		if id != 1 {
			return nil, sql.ErrNoRows
		}

		return &automdm.Invite{
			ID:         1,
			Name:       "John Appleseed",
			Email:      "john_appleseed@example.com",
			SSOEnabled: true,
			GlobalRole: null.NewString("observer", true),
		}, nil
	}
	ms.UpdateInviteFunc = func(ctx context.Context, id uint, i *automdm.Invite) (*automdm.Invite, error) {
		return nil, nil
	}

	mailer := &mockMailService{SendEmailFn: func(e automdm.Email) error { return nil }}

	svc := validationMiddleware{&Service{
		ds:          ms,
		config:      config.TestConfig(),
		mailService: mailer,
		clock:       clock.NewMockClock(),
		authz:       authz.Must(),
	}, ms, nil}

	// email is the same
	payload := automdm.InvitePayload{
		Name:  ptr.String("Johnny Appleseed"),
		Email: ptr.String("john_appleseed@example.com"),
	}

	ctx := test.UserContext(context.Background(), test.UserAdmin)

	// update the invite (email is the same)
	_, err := svc.UpdateInvite(ctx, 1, payload)
	require.NoError(t, err)
	require.True(t, ms.InviteFuncInvoked)

	payload = automdm.InvitePayload{MFAEnabled: ptr.Bool(true)}
	_, err = svc.UpdateInvite(ctx, 1, payload)
	require.Error(t, err)

	payload = automdm.InvitePayload{MFAEnabled: ptr.Bool(true), SSOEnabled: ptr.Bool(false)}
	_, err = svc.UpdateInvite(ctx, 1, payload)
	require.ErrorContains(t, err, "license")

	ms.UpdateInviteFuncInvoked = false
	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	_, err = svc.UpdateInvite(ctx, 1, payload)
	require.NoError(t, err)
	require.True(t, ms.UpdateInviteFuncInvoked)
}

func TestVerifyInvite(t *testing.T) {
	ms := new(mock.Store)
	svc, ctx := newTestService(t, ms, nil, nil)

	ms.InviteByTokenFunc = func(ctx context.Context, token string) (*automdm.Invite, error) {
		return &automdm.Invite{
			ID:    1,
			Token: "abcd",
			UpdateCreateTimestamps: automdm.UpdateCreateTimestamps{
				CreateTimestamp: automdm.CreateTimestamp{
					CreatedAt: time.Now().AddDate(-1, 0, 0),
				},
			},
		}, nil
	}
	wantErr := automdm.NewInvalidArgumentError("invite_token", "Invite token has expired.")
	_, err := svc.VerifyInvite(test.UserContext(ctx, test.UserAdmin), "abcd")
	assert.Equal(t, err, wantErr)

	wantErr = automdm.NewInvalidArgumentError("invite_token", "Invite Token does not match Email Address.")

	_, err = svc.VerifyInvite(test.UserContext(ctx, test.UserAdmin), "bad_token")
	assert.Equal(t, err, wantErr)
}

func TestDeleteInvite(t *testing.T) {
	ms := new(mock.Store)
	svc, ctx := newTestService(t, ms, nil, nil)

	ms.DeleteInviteFunc = func(context.Context, uint) error { return nil }
	err := svc.DeleteInvite(test.UserContext(ctx, test.UserAdmin), 1)
	require.Nil(t, err)
	assert.True(t, ms.DeleteInviteFuncInvoked)
}

func TestListInvites(t *testing.T) {
	ms := new(mock.Store)
	svc, ctx := newTestService(t, ms, nil, nil)

	ms.ListInvitesFunc = func(context.Context, automdm.ListOptions) ([]*automdm.Invite, error) {
		return nil, nil
	}
	_, err := svc.ListInvites(test.UserContext(ctx, test.UserAdmin), automdm.ListOptions{})
	require.Nil(t, err)
	assert.True(t, ms.ListInvitesFuncInvoked)
}

func TestInvitesAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.ListInvitesFunc = func(context.Context, automdm.ListOptions) ([]*automdm.Invite, error) {
		return nil, nil
	}
	ds.DeleteInviteFunc = func(context.Context, uint) error { return nil }
	ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return nil, newNotFoundError()
	}
	ds.NewInviteFunc = func(ctx context.Context, i *automdm.Invite) (*automdm.Invite, error) {
		return &automdm.Invite{}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	var testCases = []struct {
		name            string
		user            *automdm.User
		shouldFailWrite bool
		shouldFailRead  bool
	}{
		{
			"global admin",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
			false,
		},
		{
			"global maintainer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			true,
			true,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
			true,
		},
		{
			"team admin, belongs to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
			true,
		},
		{
			"team maintainer, belongs to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			true,
			true,
		},
		{
			"team observer, belongs to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
			true,
		},
		{
			"team maintainer, DOES NOT belong to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
			true,
			true,
		},
		{
			"team admin, DOES NOT belong to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
			true,
			true,
		},
		{
			"team observer, DOES NOT belong to team",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
			true,
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, err := svc.InviteNewUser(ctx, automdm.InvitePayload{
				Email:      ptr.String("e@mail.com"),
				Name:       ptr.String("name"),
				Position:   ptr.String("someposition"),
				SSOEnabled: ptr.Bool(false),
				GlobalRole: null.StringFromPtr(tt.user.GlobalRole),
				Teams: []automdm.UserTeam{
					{
						Team: automdm.Team{ID: 1},
						Role: automdm.RoleMaintainer,
					},
				},
			})
			checkAuthErr(t, tt.shouldFailWrite, err)

			_, err = svc.ListInvites(ctx, automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailRead, err)

			err = svc.DeleteInvite(ctx, 99)
			checkAuthErr(t, tt.shouldFailWrite, err)
		})
	}
}

package scim

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	mockservice "github.com/anilyagiz/automdm/v4/server/mock/service"
	platform_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/elimity-com/scim"
	scimerrors "github.com/elimity-com/scim/errors"
	"github.com/scim2/filter-parser/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alreadyExistsErr implements automdm.AlreadyExistsError for testing.
type alreadyExistsErr struct {
	msg string
}

func (e *alreadyExistsErr) Error() string  { return e.msg }
func (e *alreadyExistsErr) IsExists() bool { return true }

type testMocks struct {
	ds  *mock.Store
	svc *mockservice.Service
}

func newTestMocks() *testMocks {
	m := &testMocks{
		ds:  new(mock.Store),
		svc: new(mockservice.Service),
	}
	m.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
		return nil, platform_mysql.NotFound("User")
	}
	m.ds.SetScimUserTeamUserIDFunc = func(ctx context.Context, scimUserID uint, teamUserID uint) error {
		return nil
	}
	return m
}

func (m *testMocks) newTestHandler() *UserHandler {
	return &UserHandler{
		ds:          m.ds,
		newActivity: m.svc.NewActivity,
		logger:      slog.New(slog.DiscardHandler),
	}
}

// matchTeamUserByEmail stubs UserByEmail to return user for the given email
// and NotFound for any other.
func (m *testMocks) matchTeamUserByEmail(email string, user *automdm.User) {
	m.ds.UserByEmailFunc = func(ctx context.Context, e string) (*automdm.User, error) {
		if e == email {
			return user, nil
		}
		return nil, platform_mysql.NotFound("User")
	}
}

// captureLinkedBeforeReplace stubs ReplaceScimUser to record whether the
// durable link write had already happened when the mutation was persisted —
// the ordering that keeps mutated identifiers from ever being visible
// unlinked.
func (m *testMocks) captureLinkedBeforeReplace() *bool {
	linkedBefore := new(false)
	m.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
		*linkedBefore = m.ds.SetScimUserTeamUserIDFuncInvoked
		return nil, nil
	}
	return linkedBefore
}

// patchReplaceOp builds a SCIM replace operation for an explicit path.
func patchReplaceOp(t *testing.T, path string, value any) scim.PatchOperation {
	t.Helper()
	parsed, err := filter.ParsePath([]byte(path))
	require.NoError(t, err)
	return scim.PatchOperation{Op: scim.PatchOperationReplace, Path: &parsed, Value: value}
}

type teamUserOpts struct {
	id         uint
	name       string
	email      string
	globalRole string
	apiOnly    bool
	ssoEnabled bool
}

func newTestTeamUser(opts *teamUserOpts) *automdm.User {
	user := &automdm.User{
		ID:         100,
		Name:       "Test User",
		Email:      "user@example.com",
		GlobalRole: ptr.String(automdm.RoleMaintainer),
		APIOnly:    false,
		SSOEnabled: true,
	}
	if opts != nil {
		if opts.id != 0 {
			user.ID = opts.id
		}
		if opts.name != "" {
			user.Name = opts.name
		}
		if opts.email != "" {
			user.Email = opts.email
		}
		if opts.globalRole != "" {
			user.GlobalRole = ptr.String(opts.globalRole)
		}
		user.APIOnly = opts.apiOnly
		user.SSOEnabled = opts.ssoEnabled
	}
	return user
}

type scimUserOpts struct {
	id         uint
	userName   string
	teamUserID uint
	active     *bool
	givenName  string
	familyName string
	emails     []automdm.ScimUserEmail
}

func newTestScimUser(opts *scimUserOpts) *automdm.ScimUser {
	user := &automdm.ScimUser{
		ID:       1,
		UserName: "user@example.com",
		Emails:   []automdm.ScimUserEmail{},
	}
	if opts != nil {
		if opts.id != 0 {
			user.ID = opts.id
		}
		if opts.userName != "" {
			user.UserName = opts.userName
		}
		if opts.teamUserID != 0 {
			user.TeamUserID = new(opts.teamUserID)
		}
		if opts.active != nil {
			user.Active = opts.active
		}
		if opts.givenName != "" {
			user.GivenName = ptr.String(opts.givenName)
		}
		if opts.familyName != "" {
			user.FamilyName = ptr.String(opts.familyName)
		}
		if opts.emails != nil {
			user.Emails = opts.emails
		}
	}
	return user
}

func newTestAttrs(userName string, active *bool, givenName, familyName string) map[string]any {
	attrs := map[string]any{
		"userName": userName,
		"name": map[string]any{
			"givenName":  givenName,
			"familyName": familyName,
		},
	}
	if active != nil {
		attrs["active"] = *active
	}
	return attrs
}

func TestDeleteMatchingTeamUser(t *testing.T) {
	t.Run("no link and no emails: skips deletion and flags it in the audit log", func(t *testing.T) {
		mocks := newTestMocks()
		var gotActivity automdm.ActivityDetails
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			gotActivity = activity
			return nil
		}
		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{id: 7, userName: "johndoe"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)
		require.False(t, mocks.ds.UserByEmailFuncInvoked)
		require.False(t, mocks.ds.DeleteUserFuncInvoked)

		skipped, ok := gotActivity.(automdm.ActivityTypeScimUserDeprovisionSkipped)
		require.True(t, ok, "expected skipped-deprovision activity, got %T", gotActivity)
		require.Equal(t, uint(7), skipped.ScimUserID)
		require.Equal(t, "johndoe", skipped.ScimUserName)
	})

	t.Run("resolves via durable scim_users.user_id link, not email", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{id: 100, email: "victim@example.com", ssoEnabled: true})

		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			require.Equal(t, uint(100), id)
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			require.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		// Identifiers are garbage (mutated), but the durable link points at the AutoMDM user.
		scimUser := newTestScimUser(&scimUserOpts{id: 7, userName: "nondomain_user_bypass", teamUserID: 100})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		require.True(t, mocks.ds.UserByIDFuncInvoked)
		require.False(t, mocks.ds.UserByEmailFuncInvoked, "must resolve by link, not by email")
		require.True(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("linked AutoMDM user already deleted: no-op", func(t *testing.T) {
		mocks := newTestMocks()
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return nil, platform_mysql.NotFound("User")
		}
		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{id: 7, userName: "x@example.com", teamUserID: 100})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)
		require.True(t, mocks.ds.UserByIDFuncInvoked)
		require.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("userName is email, matches AutoMDM user", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{name: "John Doe", email: "john@example.com", ssoEnabled: true})

		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			if email == "john@example.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			assert.Equal(t, uint(100), id)
			return nil
		}

		var activityCreated bool
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			activityCreated = true
			deleted, ok := activity.(automdm.ActivityTypeDeletedUser)
			require.True(t, ok)
			assert.Equal(t, uint(100), deleted.UserID)
			assert.Equal(t, "John Doe", deleted.UserName)
			assert.Equal(t, "john@example.com", deleted.UserEmail)
			assert.True(t, deleted.FromScimUserDeletion)
			return nil
		}

		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{userName: "john@example.com"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		assert.True(t, mocks.ds.UserByEmailFuncInvoked)
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
		assert.True(t, activityCreated)
	})

	t.Run("skips deletion of API-only user", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{
			email:      "api@example.com",
			globalRole: automdm.RoleAdmin,
			apiOnly:    true,
			ssoEnabled: true,
		})

		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return automdmUser, nil
		}

		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{userName: "api@example.com"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		assert.True(t, mocks.ds.UserByEmailFuncInvoked)
		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("skips deletion of non-SSO user", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{
			email:      "nonsso@example.com",
			ssoEnabled: false,
		})

		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return automdmUser, nil
		}

		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{userName: "nonsso@example.com"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		assert.True(t, mocks.ds.UserByEmailFuncInvoked)
		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("prevents deleting last global admin", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{
			email:      "admin@example.com",
			globalRole: automdm.RoleAdmin,
			ssoEnabled: true,
		})

		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserIfNotLastAdminFunc = func(ctx context.Context, id uint) error {
			return automdm.ErrLastGlobalAdmin
		}

		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{userName: "admin@example.com"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot delete last global admin")

		assert.True(t, mocks.ds.UserByEmailFuncInvoked)
		assert.True(t, mocks.ds.DeleteUserIfNotLastAdminFuncInvoked)
		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("allows deleting admin when multiple admins exist", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{
			email:      "admin@example.com",
			globalRole: automdm.RoleAdmin,
			ssoEnabled: true,
		})

		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserIfNotLastAdminFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{userName: "admin@example.com"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		assert.True(t, mocks.ds.DeleteUserIfNotLastAdminFuncInvoked)
	})

	t.Run("matches on scim_user_emails when userName is not email", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{
			name:       "Jane Doe",
			email:      "jane@work.com",
			ssoEnabled: true,
		})

		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			if email == "jane@work.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{
			userName: "janedoe", // Not an email
			emails: []automdm.ScimUserEmail{
				{Email: "jane@personal.com"},
				{Email: "jane@work.com"},
			},
		})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("no matching AutoMDM user found", func(t *testing.T) {
		mocks := newTestMocks()
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return nil, platform_mysql.NotFound("User")
		}

		handler := mocks.newTestHandler()
		// Routine case: an IdP user with an email but no AutoMDM account.
		scimUser := newTestScimUser(&scimUserOpts{userName: "nobody@example.com"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		assert.True(t, mocks.ds.UserByEmailFuncInvoked)
		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
		// Unmatched users with emails must not flood the audit log.
		require.False(t, mocks.svc.NewActivityFuncInvoked)
	})

	t.Run("email case insensitive matching", func(t *testing.T) {
		mocks := newTestMocks()
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		var emailQueried string
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			emailQueried = email
			if email == "user@example.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{userName: "USER@EXAMPLE.COM"})

		err := handler.deleteMatchingTeamUser(t.Context(), scimUser)
		require.NoError(t, err)

		assert.Equal(t, "user@example.com", emailQueried)
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})
}

func TestLinkMatchingTeamUser(t *testing.T) {
	t.Run("set-once: does not re-point or re-query an existing link", func(t *testing.T) {
		mocks := newTestMocks()
		handler := mocks.newTestHandler()
		// Identifiers now point at a different (victim) address, but the record
		// is already linked. Linking must be a no-op.
		scimUser := newTestScimUser(&scimUserOpts{id: 7, userName: "victim@example.com", teamUserID: 100})

		handler.linkMatchingTeamUser(t.Context(), scimUser)

		require.False(t, mocks.ds.UserByEmailFuncInvoked, "must not look up when already linked")
		require.False(t, mocks.ds.SetScimUserTeamUserIDFuncInvoked, "must not re-point an existing link")
		require.NotNil(t, scimUser.TeamUserID)
		require.Equal(t, uint(100), *scimUser.TeamUserID)
	})

	t.Run("links when unlinked and an email matches", func(t *testing.T) {
		mocks := newTestMocks()
		mocks.matchTeamUserByEmail("user@example.com", newTestTeamUser(&teamUserOpts{id: 100, email: "user@example.com"}))
		var setScimID, setTeamID uint
		mocks.ds.SetScimUserTeamUserIDFunc = func(ctx context.Context, scimUserID uint, teamUserID uint) error {
			setScimID = scimUserID
			setTeamID = teamUserID
			return nil
		}
		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{id: 7, userName: "user@example.com"})

		handler.linkMatchingTeamUser(t.Context(), scimUser)

		require.True(t, mocks.ds.SetScimUserTeamUserIDFuncInvoked)
		require.Equal(t, uint(7), setScimID)
		require.Equal(t, uint(100), setTeamID)
		require.NotNil(t, scimUser.TeamUserID)
		require.Equal(t, uint(100), *scimUser.TeamUserID)
	})

	t.Run("no email match leaves the record unlinked", func(t *testing.T) {
		mocks := newTestMocks() // default UserByEmail returns NotFound
		handler := mocks.newTestHandler()
		scimUser := newTestScimUser(&scimUserOpts{id: 7, userName: "user@example.com"})

		handler.linkMatchingTeamUser(t.Context(), scimUser)

		require.True(t, mocks.ds.UserByEmailFuncInvoked)
		require.False(t, mocks.ds.SetScimUserTeamUserIDFuncInvoked)
		require.Nil(t, scimUser.TeamUserID)
	})
}

func TestUserHandlerDelete(t *testing.T) {
	t.Run("deletes SCIM user and matching AutoMDM user", func(t *testing.T) {
		mocks := newTestMocks()
		scimUser := newTestScimUser(nil)
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return scimUser, nil
		}
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		mocks.ds.DeleteScimUserFunc = func(ctx context.Context, id uint) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()

		req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Users/1", nil)
		err := handler.Delete(req, "1")
		require.NoError(t, err)

		assert.True(t, mocks.ds.ScimUserByIDFuncInvoked)
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
		assert.True(t, mocks.ds.DeleteScimUserFuncInvoked)
	})

	t.Run("SCIM deletion proceeds even if AutoMDM user deletion fails", func(t *testing.T) {
		mocks := newTestMocks()
		scimUser := newTestScimUser(&scimUserOpts{userName: "admin@example.com"})
		automdmUser := newTestTeamUser(&teamUserOpts{
			email:      "admin@example.com",
			globalRole: automdm.RoleAdmin,
			ssoEnabled: true,
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return scimUser, nil
		}
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return automdmUser, nil
		}
		// Last admin - AutoMDM user deletion will fail
		mocks.ds.DeleteUserIfNotLastAdminFunc = func(ctx context.Context, id uint) error {
			return automdm.ErrLastGlobalAdmin
		}
		// SCIM user deletion should still succeed
		mocks.ds.DeleteScimUserFunc = func(ctx context.Context, id uint) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}

		handler := mocks.newTestHandler()

		req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Users/1", nil)
		err := handler.Delete(req, "1")
		require.NoError(t, err)

		assert.True(t, mocks.ds.DeleteScimUserFuncInvoked)
		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("returns error when SCIM user not found", func(t *testing.T) {
		mocks := newTestMocks()
		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return nil, platform_mysql.NotFound("ScimUser")
		}
		// DeleteScimUser is still called to ensure triggerResendProfilesForIDPUserDeleted runs
		mocks.ds.DeleteScimUserFunc = func(ctx context.Context, id uint) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, platform_mysql.NotFound("ScimUser")
		}

		handler := mocks.newTestHandler()

		req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Users/999", nil)
		err := handler.Delete(req, "999")
		require.Error(t, err)
		assert.True(t, mocks.ds.DeleteScimUserFuncInvoked, "DeleteScimUser should be called even when ScimUserByID returns not found")
	})
}

func TestWasDeactivated(t *testing.T) {
	tests := []struct {
		name     string
		previous *bool
		current  *bool
		expected bool
	}{
		{
			name:     "nil to false - deactivated",
			previous: nil,
			current:  ptr.Bool(false),
			expected: true,
		},
		{
			name:     "true to false - deactivated",
			previous: ptr.Bool(true),
			current:  ptr.Bool(false),
			expected: true,
		},
		{
			name:     "false to false - not deactivated (already inactive)",
			previous: ptr.Bool(false),
			current:  ptr.Bool(false),
			expected: false,
		},
		{
			name:     "nil to nil - not deactivated",
			previous: nil,
			current:  nil,
			expected: false,
		},
		{
			name:     "nil to true - not deactivated",
			previous: nil,
			current:  ptr.Bool(true),
			expected: false,
		},
		{
			name:     "true to true - not deactivated",
			previous: ptr.Bool(true),
			current:  ptr.Bool(true),
			expected: false,
		},
		{
			name:     "false to true - not deactivated (reactivated)",
			previous: ptr.Bool(false),
			current:  ptr.Bool(true),
			expected: false,
		},
		{
			name:     "true to nil - not deactivated",
			previous: ptr.Bool(true),
			current:  nil,
			expected: false,
		},
		{
			name:     "false to nil - not deactivated",
			previous: ptr.Bool(false),
			current:  nil,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := wasDeactivated(tc.previous, tc.current)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestUserHandlerReplaceDeactivation(t *testing.T) {
	t.Run("deletes AutoMDM user when SCIM user is deactivated via Replace", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			userName:   "user@example.com",
			givenName:  "John",
			familyName: "Doe",
			emails:     []automdm.ScimUserEmail{{Email: "user@example.com", Primary: new(true)}},
		})
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		// userName is changing to a non-email value, so uniqueness check misses.
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return nil, platform_mysql.NotFound("ScimUser")
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		// Only the pre-replace identifier resolves the AutoMDM user; the incoming
		// (mutated) userName/emails must not be used for lookup. Linking happens
		// off the pre-mutation email, after which deletion resolves by id.
		var lookedUpEmails []string
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			lookedUpEmails = append(lookedUpEmails, email)
			if email == "user@example.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			assert.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			deleted, ok := activity.(automdm.ActivityTypeDeletedUser)
			require.True(t, ok)
			assert.True(t, deleted.FromScimUserDeletion)
			return nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPut, "/scim/v2/Users/1", nil)
		// Replace the whole resource with a non-email userName and no emails while
		// deactivating; deprovisioning must still resolve via pre-replace identifiers.
		attrs := newTestAttrs("nondomain_user_bypass", new(false), "John", "Doe")

		_, err := handler.Replace(req, "1", attrs)
		require.NoError(t, err)

		assert.Contains(t, lookedUpEmails, "user@example.com", "expected the pre-replace identifier to be used for AutoMDM user resolution")
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("links before persisting the mutation via Replace", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     new(true),
			userName:   "user@example.com",
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return nil, platform_mysql.NotFound("ScimUser")
		}
		mocks.matchTeamUserByEmail("user@example.com", newTestTeamUser(&teamUserOpts{ssoEnabled: true}))
		linkedBeforeMutationPersisted := mocks.captureLinkedBeforeReplace()

		handler := mocks.newTestHandler()
		// Mutating (non-deactivating) replace: rename to a non-email userName.
		attrs := newTestAttrs("nondomain_user_bypass", new(true), "John", "Doe")

		_, err := handler.Replace(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/1", nil), "1", attrs)
		require.NoError(t, err)

		require.True(t, mocks.ds.SetScimUserTeamUserIDFuncInvoked)
		require.True(t, *linkedBeforeMutationPersisted, "link must be established before ReplaceScimUser persists the mutation")
	})

	t.Run("does not delete AutoMDM user when active state unchanged", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}

		handler := mocks.newTestHandler()
		attrs := newTestAttrs("user@example.com", ptr.Bool(true), "John", "Doe")

		_, err := handler.Replace(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/1", nil), "1", attrs)
		require.NoError(t, err)

		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("does not delete AutoMDM user when already inactive", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(false), // Already inactive
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}

		handler := mocks.newTestHandler()
		attrs := newTestAttrs("user@example.com", ptr.Bool(false), "John", "Doe")

		_, err := handler.Replace(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/1", nil), "1", attrs)
		require.NoError(t, err)

		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("returns uniqueness error when ReplaceScimUser returns wrapped AlreadyExistsError", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		// Pre-check passes (no other user with this username)
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return nil, platform_mysql.NotFound("ScimUser")
		}
		// ReplaceScimUser returns a wrapped AlreadyExistsError (race condition: concurrent update took the username)
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, fmt.Errorf("update scim user: %w", &alreadyExistsErr{msg: "user_name already exists"})
		}

		handler := mocks.newTestHandler()
		attrs := newTestAttrs("taken@example.com", ptr.Bool(true), "John", "Doe")

		_, err := handler.Replace(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/1", nil), "1", attrs)
		require.Error(t, err)

		scimErr, ok := err.(scimerrors.ScimError)
		require.True(t, ok, "expected ScimError, got %T: %v", err, err)
		assert.Equal(t, http.StatusConflict, scimErr.Status)
		assert.Equal(t, scimerrors.ScimTypeUniqueness, scimErr.ScimType)
	})

	t.Run("returns bad params error when ReplaceScimUser returns SCIMValidationError", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		// ReplaceScimUser returns a validation error (field too long)
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, &automdm.SCIMValidationError{Field: "given_name", Message: "exceeds maximum length of 255 characters"}
		}

		handler := mocks.newTestHandler()
		attrs := newTestAttrs("user@example.com", ptr.Bool(true), "John", "Doe")

		_, err := handler.Replace(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/1", nil), "1", attrs)
		require.Error(t, err)

		scimErr, ok := err.(scimerrors.ScimError)
		require.True(t, ok, "expected ScimError, got %T: %v", err, err)
		assert.Equal(t, http.StatusBadRequest, scimErr.Status)
		assert.Contains(t, scimErr.Detail, "given_name")
	})

	t.Run("deletes AutoMDM user when emails are dropped in a deactivating Replace with unchanged userName", func(t *testing.T) {
		mocks := newTestMocks()
		// userName stays the same and is not an email, so resolution can only
		// succeed via the persisted email — exercising the same-userName branch.
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     new(true),
			userName:   "someuser",
			givenName:  "John",
			familyName: "Doe",
			emails:     []automdm.ScimUserEmail{{Email: "victim@example.com", Type: new("work"), Primary: new(true)}},
		})
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		var lookedUpEmails []string
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			lookedUpEmails = append(lookedUpEmails, email)
			if email == "victim@example.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			assert.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		// Same (non-email) userName, no emails in the incoming representation, deactivating.
		attrs := newTestAttrs("someuser", new(false), "John", "Doe")

		_, err := handler.Replace(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/1", nil), "1", attrs)
		require.NoError(t, err)

		assert.Contains(t, lookedUpEmails, "victim@example.com", "expected the pre-replace email to be used for AutoMDM user resolution")
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})
}

func TestUserHandlerPatchDeactivation(t *testing.T) {
	t.Run("deletes AutoMDM user when SCIM user is deactivated via Patch with path", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			userName:   "user@example.com",
			givenName:  "John",
			familyName: "Doe",
			emails:     []automdm.ScimUserEmail{{Email: "user@example.com", Primary: new(true)}},
		})
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		// Only the pre-patch identifier resolves the AutoMDM user; the mutated
		// userName/emails from the same PATCH must not be used for lookup.
		var lookedUpEmails []string
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			lookedUpEmails = append(lookedUpEmails, email)
			if email == "user@example.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			assert.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		userNamePath, err := filter.ParsePath([]byte("userName"))
		require.NoError(t, err)
		emailsPath, err := filter.ParsePath([]byte("emails"))
		require.NoError(t, err)
		activePath, err := filter.ParsePath([]byte("active"))
		require.NoError(t, err)

		// Pen-test payload: rename to a non-email userName and drop emails while
		// deactivating; deletion must still resolve via the pre-patch identifiers.
		patchOps := []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: &userNamePath, Value: "nondomain_user_bypass"},
			{Op: scim.PatchOperationRemove, Path: &emailsPath},
			{Op: scim.PatchOperationReplace, Path: &activePath, Value: false},
		}

		_, err = handler.Patch(req, "1", patchOps)
		require.NoError(t, err)

		assert.Contains(t, lookedUpEmails, "user@example.com", "expected the pre-patch identifier to be used for AutoMDM user resolution")
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("deletes AutoMDM user when SCIM user is deactivated via Patch without path", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			givenName:  "John",
			familyName: "Doe",
		})
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			assert.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		patchOps := []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: nil, Value: map[string]any{"active": false}},
		}

		_, err := handler.Patch(req, "1", patchOps)
		require.NoError(t, err)

		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("does not delete AutoMDM user when active unchanged via Patch", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		givenNamePath, err := filter.ParsePath([]byte("name.givenName"))
		require.NoError(t, err)

		patchOps := []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: &givenNamePath, Value: "Jane"},
		}

		_, err = handler.Patch(req, "1", patchOps)
		require.NoError(t, err)

		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("does not delete AutoMDM user when already inactive", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(false), // Already inactive
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		activePath, err := filter.ParsePath([]byte("active"))
		require.NoError(t, err)

		patchOps := []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: &activePath, Value: false},
		}

		_, err = handler.Patch(req, "1", patchOps)
		require.NoError(t, err)

		assert.False(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("returns uniqueness error when ReplaceScimUser returns wrapped AlreadyExistsError via Patch", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true),
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		// ReplaceScimUser returns a wrapped AlreadyExistsError (race condition: concurrent update took the username)
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, fmt.Errorf("update scim user: %w", &alreadyExistsErr{msg: "user_name already exists"})
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		userNamePath, err := filter.ParsePath([]byte("userName"))
		require.NoError(t, err)

		patchOps := []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: &userNamePath, Value: "taken@example.com"},
		}

		_, err = handler.Patch(req, "1", patchOps)
		require.Error(t, err)

		scimErr, ok := err.(scimerrors.ScimError)
		require.True(t, ok, "expected ScimError, got %T: %v", err, err)
		assert.Equal(t, http.StatusConflict, scimErr.Status)
		assert.Equal(t, scimerrors.ScimTypeUniqueness, scimErr.ScimType)
	})

	t.Run("deletes AutoMDM user when an email is rewritten in place in the same deactivating Patch", func(t *testing.T) {
		mocks := newTestMocks()
		// userName is not an email, so the only lookup candidate is the email
		// element rewritten in place below — exercising the emails clone.
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     new(true),
			userName:   "someuser",
			givenName:  "John",
			familyName: "Doe",
			emails:     []automdm.ScimUserEmail{{Email: "victim@example.com", Type: new("work"), Primary: new(true)}},
		})
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		var lookedUpEmails []string
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			lookedUpEmails = append(lookedUpEmails, email)
			if email == "victim@example.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			assert.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		emailValuePath, err := filter.ParsePath([]byte(`emails[type eq "work"].value`))
		require.NoError(t, err)
		activePath, err := filter.ParsePath([]byte("active"))
		require.NoError(t, err)

		// Rewrite the existing work email's value in place, then deactivate.
		patchOps := []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: &emailValuePath, Value: "garbage@nomatch.local"},
			{Op: scim.PatchOperationReplace, Path: &activePath, Value: false},
		}

		_, err = handler.Patch(req, "1", patchOps)
		require.NoError(t, err)

		assert.Contains(t, lookedUpEmails, "victim@example.com", "expected the pre-patch email to be used for AutoMDM user resolution")
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("deletes via durable link when identifiers were already mutated by an earlier request", func(t *testing.T) {
		mocks := newTestMocks()
		// Two-step variant: identifiers were already mutated and persisted by an
		// earlier request, but the durable scim_users.user_id link survives, so this
		// deactivation still deprovisions the AutoMDM user.
		existingScimUser := newTestScimUser(&scimUserOpts{
			id:         9,
			active:     new(true),
			userName:   "nondomain_user_bypass",
			teamUserID: 100,
			givenName:  "John",
			familyName: "Doe",
		})
		automdmUser := newTestTeamUser(&teamUserOpts{id: 100, ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			require.Equal(t, uint(100), id)
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			require.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/9", nil)
		patchOps := []scim.PatchOperation{patchReplaceOp(t, "active", false)}

		_, err := handler.Patch(req, "9", patchOps)
		require.NoError(t, err)

		require.True(t, mocks.ds.UserByIDFuncInvoked, "should resolve the AutoMDM user via the durable link")
		require.True(t, mocks.ds.DeleteUserFuncInvoked)
	})

	t.Run("links before persisting the mutation via Patch", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     new(true),
			userName:   "user@example.com",
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.matchTeamUserByEmail("user@example.com", newTestTeamUser(&teamUserOpts{ssoEnabled: true}))
		linkedBeforeMutationPersisted := mocks.captureLinkedBeforeReplace()

		handler := mocks.newTestHandler()
		// Mutating (non-deactivating) patch: rename to a non-email userName.
		patchOps := []scim.PatchOperation{patchReplaceOp(t, "userName", "nondomain_user_bypass")}

		_, err := handler.Patch(httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil), "1", patchOps)
		require.NoError(t, err)

		require.True(t, mocks.ds.SetScimUserTeamUserIDFuncInvoked)
		require.True(t, *linkedBeforeMutationPersisted, "link must be established before ReplaceScimUser persists the mutation")
	})

	t.Run("deletes AutoMDM user when identifiers are mutated via a pathless multi-op Patch", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     new(true),
			userName:   "user@example.com",
			givenName:  "John",
			familyName: "Doe",
			emails:     []automdm.ScimUserEmail{{Email: "user@example.com", Primary: new(true)}},
		})
		automdmUser := newTestTeamUser(&teamUserOpts{ssoEnabled: true})

		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			return nil, nil
		}
		var lookedUpEmails []string
		mocks.ds.UserByEmailFunc = func(ctx context.Context, email string) (*automdm.User, error) {
			lookedUpEmails = append(lookedUpEmails, email)
			if email == "user@example.com" {
				return automdmUser, nil
			}
			return nil, platform_mysql.NotFound("User")
		}
		mocks.ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
			return automdmUser, nil
		}
		mocks.ds.DeleteUserFunc = func(ctx context.Context, id uint) error {
			assert.Equal(t, uint(100), id)
			return nil
		}
		mocks.svc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		// Pathless replace carrying the mutated identifiers alongside active=false.
		patchOps := []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: nil, Value: map[string]any{
				"userName": "nondomain_user_bypass",
				"emails":   []any{},
				"active":   false,
			}},
		}

		_, err := handler.Patch(req, "1", patchOps)
		require.NoError(t, err)

		assert.Contains(t, lookedUpEmails, "user@example.com", "expected the pre-patch identifier to be used for AutoMDM user resolution")
		assert.True(t, mocks.ds.DeleteUserFuncInvoked)
	})
}

func TestUserHandlerCreateReactivation(t *testing.T) {
	t.Run("reactivates deactivated user via Create", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(false), // Deactivated
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}

		var replacedUser *automdm.ScimUser
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			replacedUser = user
			return nil, nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", nil)
		attrs := newTestAttrs("user@example.com", ptr.Bool(true), "John", "Doe")

		resource, err := handler.Create(req, attrs)
		require.NoError(t, err)

		// Verify the user was reactivated (Replace was called, not Create)
		assert.True(t, mocks.ds.ReplaceScimUserFuncInvoked)
		assert.False(t, mocks.ds.CreateScimUserFuncInvoked)

		// Verify the active status is set to true
		require.NotNil(t, replacedUser)
		require.NotNil(t, replacedUser.Active)
		assert.True(t, *replacedUser.Active)

		// Verify the returned resource has the correct ID
		assert.Equal(t, "1", resource.ID)
	})

	t.Run("reactivation links from the persisted identifiers, before persisting the incoming ones", func(t *testing.T) {
		mocks := newTestMocks()
		// Persisted record still carries the trusted email; the reactivating
		// request omits emails, so a link can only come from the persisted
		// identifiers.
		existingScimUser := newTestScimUser(&scimUserOpts{
			id:         1,
			active:     new(false),
			userName:   "someuser",
			givenName:  "John",
			familyName: "Doe",
			emails:     []automdm.ScimUserEmail{{Email: "victim@example.com", Primary: new(true)}},
		})

		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}
		mocks.matchTeamUserByEmail("victim@example.com", newTestTeamUser(&teamUserOpts{ssoEnabled: true}))
		linkedBeforeMutationPersisted := mocks.captureLinkedBeforeReplace()

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", nil)
		attrs := newTestAttrs("someuser", new(true), "John", "Doe")

		_, err := handler.Create(req, attrs)
		require.NoError(t, err)

		require.True(t, mocks.ds.SetScimUserTeamUserIDFuncInvoked, "expected the persisted email to establish the link")
		require.True(t, *linkedBeforeMutationPersisted, "link must be established before ReplaceScimUser persists the incoming attributes")
	})

	t.Run("returns uniqueness error when active not explicitly true", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(false), // Deactivated
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", nil)
		// Attributes without explicit active field - should NOT reactivate
		attrs := newTestAttrs("user@example.com", nil, "John", "Doe")

		_, err := handler.Create(req, attrs)
		require.Error(t, err)

		// Should not have called Replace or Create
		assert.False(t, mocks.ds.ReplaceScimUserFuncInvoked)
		assert.False(t, mocks.ds.CreateScimUserFuncInvoked)
	})

	t.Run("returns uniqueness error for active user", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			active:     ptr.Bool(true), // Already active
			givenName:  "John",
			familyName: "Doe",
		})

		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", nil)
		attrs := newTestAttrs("user@example.com", ptr.Bool(true), "John", "Doe")

		_, err := handler.Create(req, attrs)
		require.Error(t, err)

		// Should not have called Replace or Create
		assert.False(t, mocks.ds.ReplaceScimUserFuncInvoked)
		assert.False(t, mocks.ds.CreateScimUserFuncInvoked)
	})

	t.Run("returns uniqueness error when CreateScimUser returns wrapped AlreadyExistsError", func(t *testing.T) {
		mocks := newTestMocks()

		// No existing user found during pre-check (simulating race condition)
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return nil, platform_mysql.NotFound("ScimUser")
		}

		// CreateScimUser returns a wrapped AlreadyExistsError (as ctxerr.Wrap produces in production)
		mocks.ds.CreateScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) (uint, error) {
			return 0, fmt.Errorf("insert scim user: %w", &alreadyExistsErr{msg: "user_name already exists"})
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", nil)
		attrs := newTestAttrs("user@example.com", ptr.Bool(true), "John", "Doe")

		_, err := handler.Create(req, attrs)
		require.Error(t, err)

		// Should be a SCIM uniqueness error (409), not a 500
		scimErr, ok := err.(scimerrors.ScimError)
		require.True(t, ok, "expected ScimError, got %T: %v", err, err)
		assert.Equal(t, http.StatusConflict, scimErr.Status)
		assert.Equal(t, scimerrors.ScimTypeUniqueness, scimErr.ScimType)
	})

	t.Run("returns bad params error when CreateScimUser returns SCIMValidationError", func(t *testing.T) {
		mocks := newTestMocks()

		// No existing user found during pre-check
		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return nil, platform_mysql.NotFound("ScimUser")
		}

		// CreateScimUser returns a validation error (field too long)
		mocks.ds.CreateScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) (uint, error) {
			return 0, &automdm.SCIMValidationError{Field: "user_name", Message: "exceeds maximum length of 255 characters"}
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", nil)
		attrs := newTestAttrs("user@example.com", ptr.Bool(true), "John", "Doe")

		_, err := handler.Create(req, attrs)
		require.Error(t, err)

		// Should be a SCIM bad params error (400), not a 500
		scimErr, ok := err.(scimerrors.ScimError)
		require.True(t, ok, "expected ScimError, got %T: %v", err, err)
		assert.Equal(t, http.StatusBadRequest, scimErr.Status)
		assert.Contains(t, scimErr.Detail, "user_name")
	})

	t.Run("returns uniqueness error for user with nil active", func(t *testing.T) {
		mocks := newTestMocks()
		existingScimUser := newTestScimUser(&scimUserOpts{
			givenName:  "John",
			familyName: "Doe",
		}) // Active is nil (not explicitly deactivated)

		mocks.ds.ScimUserByUserNameFunc = func(ctx context.Context, userName string) (*automdm.ScimUser, error) {
			return existingScimUser, nil
		}

		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", nil)
		attrs := newTestAttrs("user@example.com", ptr.Bool(true), "John", "Doe")

		_, err := handler.Create(req, attrs)
		require.Error(t, err)

		// Should not have called Replace or Create
		assert.False(t, mocks.ds.ReplaceScimUserFuncInvoked)
		assert.False(t, mocks.ds.CreateScimUserFuncInvoked)
	})
}

func TestUserHandlerPatchUnknownAttributes(t *testing.T) {
	const enterpriseExtURN = "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"

	setupMocks := func(t *testing.T) (*testMocks, *automdm.ScimUser, **automdm.ScimUser) {
		t.Helper()
		mocks := newTestMocks()
		existingUser := newTestScimUser(&scimUserOpts{active: new(true), givenName: "John", familyName: "Doe"})
		mocks.ds.ScimUserByIDFunc = func(ctx context.Context, id uint) (*automdm.ScimUser, error) {
			return existingUser, nil
		}
		var saved *automdm.ScimUser
		mocks.ds.ReplaceScimUserFunc = func(ctx context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
			saved = user
			return nil, nil
		}
		return mocks, existingUser, &saved
	}

	t.Run("explicit-path with unrecognized path is ignored, department in same batch is applied", func(t *testing.T) {
		mocks, _, saved := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		deptPath, err := filter.ParsePath([]byte(enterpriseExtURN + ":department"))
		require.NoError(t, err)
		unknownPath, err := filter.ParsePath([]byte(enterpriseExtURN + ":employeeNumber"))
		require.NoError(t, err)

		_, err = handler.Patch(req, "1", []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: &unknownPath, Value: "EMP-1"},
			{Op: scim.PatchOperationReplace, Path: &deptPath, Value: "Engineering"},
		})
		require.NoError(t, err)
		require.True(t, mocks.ds.ReplaceScimUserFuncInvoked)
		require.NotNil(t, *saved)
		require.NotNil(t, (*saved).Department)
		assert.Equal(t, "Engineering", *(*saved).Department)
	})

	t.Run("no-path value with unrecognized field is ignored, department in same value is applied", func(t *testing.T) {
		mocks, _, saved := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		_, err := handler.Patch(req, "1", []scim.PatchOperation{
			{
				Op:   scim.PatchOperationReplace,
				Path: nil,
				Value: map[string]any{
					enterpriseExtURN + ":department":     "Sales",
					enterpriseExtURN + ":employeeNumber": "EMP-2",
				},
			},
		})
		require.NoError(t, err)
		require.True(t, mocks.ds.ReplaceScimUserFuncInvoked)
		require.NotNil(t, *saved)
		require.NotNil(t, (*saved).Department)
		assert.Equal(t, "Sales", *(*saved).Department)
	})

	t.Run("patchName ignores unrecognized name subattributes", func(t *testing.T) {
		// givenName/familyName get applied; honorificPrefix and middleName are silently
		// dropped instead of aborting the PATCH.
		mocks, _, saved := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		_, err := handler.Patch(req, "1", []scim.PatchOperation{
			{
				Op:   scim.PatchOperationReplace,
				Path: nil,
				Value: map[string]any{
					"name": map[string]any{
						"givenName":       "WithPrefix",
						"familyName":      "User",
						"honorificPrefix": "Mr",
						"middleName":      "Q",
					},
				},
			},
		})
		require.NoError(t, err)
		require.True(t, mocks.ds.ReplaceScimUserFuncInvoked)
		require.NotNil(t, *saved)
		require.NotNil(t, (*saved).GivenName)
		require.NotNil(t, (*saved).FamilyName)
		assert.Equal(t, "WithPrefix", *(*saved).GivenName)
		assert.Equal(t, "User", *(*saved).FamilyName)
	})

	t.Run("PATCH with only unrecognized explicit-path operations skips database write", func(t *testing.T) {
		mocks, _, _ := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		unknownPath, err := filter.ParsePath([]byte(enterpriseExtURN + ":employeeNumber"))
		require.NoError(t, err)

		_, err = handler.Patch(req, "1", []scim.PatchOperation{
			{Op: scim.PatchOperationReplace, Path: &unknownPath, Value: "EMP-1"},
			{Op: scim.PatchOperationReplace, Path: &unknownPath, Value: "EMP-2"},
		})
		require.NoError(t, err)
		assert.False(t, mocks.ds.ReplaceScimUserFuncInvoked)
	})

	t.Run("PATCH with only unrecognized no-path fields skips database write", func(t *testing.T) {
		mocks, _, _ := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		_, err := handler.Patch(req, "1", []scim.PatchOperation{
			{
				Op:   scim.PatchOperationReplace,
				Path: nil,
				Value: map[string]any{
					enterpriseExtURN + ":employeeNumber": "EMP-1",
					enterpriseExtURN + ":costCenter":     "CC-1",
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, mocks.ds.ReplaceScimUserFuncInvoked)
	})

	t.Run("add op with unrecognized explicit path is silently skipped", func(t *testing.T) {
		// The recognition refactor applies equally to add ops; verify a no-op add on
		// an unrecognized path doesn't 400 and doesn't trigger a DB write.
		mocks, _, _ := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		unknownPath, err := filter.ParsePath([]byte(enterpriseExtURN + ":employeeNumber"))
		require.NoError(t, err)

		_, err = handler.Patch(req, "1", []scim.PatchOperation{
			{Op: scim.PatchOperationAdd, Path: &unknownPath, Value: "EMP-1"},
		})
		require.NoError(t, err)
		assert.False(t, mocks.ds.ReplaceScimUserFuncInvoked)
	})

	t.Run("remove op with unrecognized explicit path is silently skipped", func(t *testing.T) {
		// Removing an attribute AutoMDM doesn't store is a no-op by definition; verify
		// it doesn't 400 and doesn't trigger a DB write.
		mocks, _, _ := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		unknownPath, err := filter.ParsePath([]byte(enterpriseExtURN + ":costCenter"))
		require.NoError(t, err)

		_, err = handler.Patch(req, "1", []scim.PatchOperation{
			{Op: scim.PatchOperationRemove, Path: &unknownPath, Value: nil},
		})
		require.NoError(t, err)
		assert.False(t, mocks.ds.ReplaceScimUserFuncInvoked)
	})

	t.Run("add op with unrecognized path mixed with recognized op applies the recognized one", func(t *testing.T) {
		mocks, _, saved := setupMocks(t)
		handler := mocks.newTestHandler()
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/1", nil)

		unknownPath, err := filter.ParsePath([]byte(enterpriseExtURN + ":employeeNumber"))
		require.NoError(t, err)
		deptPath, err := filter.ParsePath([]byte(enterpriseExtURN + ":department"))
		require.NoError(t, err)

		_, err = handler.Patch(req, "1", []scim.PatchOperation{
			{Op: scim.PatchOperationAdd, Path: &unknownPath, Value: "EMP-2"},
			{Op: scim.PatchOperationAdd, Path: &deptPath, Value: "Sales"},
		})
		require.NoError(t, err)
		require.True(t, mocks.ds.ReplaceScimUserFuncInvoked)
		require.NotNil(t, *saved)
		require.NotNil(t, (*saved).Department)
		assert.Equal(t, "Sales", *(*saved).Department)
	})
}

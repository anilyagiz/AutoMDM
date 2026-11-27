package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDirectory struct {
	users     []*automdm.ScimUser
	groups    []*automdm.GoogleWorkspaceGroup
	usersErr  error
	groupsErr error
}

func (f *fakeDirectory) ListUsers(context.Context) ([]*automdm.ScimUser, error) {
	return f.users, f.usersErr
}

func (f *fakeDirectory) ListGroups(context.Context) ([]*automdm.GoogleWorkspaceGroup, error) {
	return f.groups, f.groupsErr
}

func fakeFactory(dir automdm.GoogleWorkspaceDirectory, err error) GoogleWorkspaceDirectoryFactory {
	return func(context.Context, *automdm.GoogleWorkspaceIntegration, *slog.Logger) (automdm.GoogleWorkspaceDirectory, error) {
		return dir, err
	}
}

func gwAppConfig() *automdm.AppConfig {
	ac := &automdm.AppConfig{}
	ac.Integrations.GoogleWorkspace = []*automdm.GoogleWorkspaceIntegration{{
		Domain:                "example.com",
		ImpersonatedUserEmail: "admin@example.com",
	}}
	return ac
}

// syncRecorder wires a mock.Store with recording stubs for the scim datastore
// methods the sync engine uses, seeded with the given existing state.
type syncRecorder struct {
	ds *mock.Store

	createdUsers  []*automdm.ScimUser
	replacedUsers []*automdm.ScimUser
	deletedUsers  []uint

	createdGroups  []*automdm.ScimGroup
	replacedGroups []*automdm.ScimGroup
	deletedGroups  []uint

	lastRequest *automdm.ScimLastRequest
}

func newSyncRecorder(appConfig *automdm.AppConfig, existingUsers []automdm.ScimUser, existingGroups []automdm.ScimGroup) *syncRecorder {
	r := &syncRecorder{ds: new(mock.Store)}
	nextID := uint(1000)

	r.ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
		return appConfig, nil
	}
	r.ds.ListScimUsersFunc = func(_ context.Context, _ automdm.ScimUsersListOptions) ([]automdm.ScimUser, uint, error) {
		return existingUsers, uint(len(existingUsers)), nil
	}
	r.ds.ListScimGroupsFunc = func(_ context.Context, _ automdm.ScimGroupsListOptions) ([]automdm.ScimGroup, uint, error) {
		return existingGroups, uint(len(existingGroups)), nil
	}
	r.ds.CreateScimUserFunc = func(_ context.Context, user *automdm.ScimUser) (uint, error) {
		nextID++
		user.ID = nextID
		r.createdUsers = append(r.createdUsers, user)
		return nextID, nil
	}
	r.ds.ReplaceScimUserFunc = func(_ context.Context, user *automdm.ScimUser) ([]automdm.ActivityTypeResentCertificate, error) {
		r.replacedUsers = append(r.replacedUsers, user)
		return nil, nil
	}
	r.ds.DeleteScimUserFunc = func(_ context.Context, id uint) ([]automdm.ActivityTypeResentCertificate, error) {
		r.deletedUsers = append(r.deletedUsers, id)
		return nil, nil
	}
	r.ds.CreateScimGroupFunc = func(_ context.Context, group *automdm.ScimGroup) (uint, error) {
		nextID++
		group.ID = nextID
		r.createdGroups = append(r.createdGroups, group)
		return nextID, nil
	}
	r.ds.ReplaceScimGroupFunc = func(_ context.Context, group *automdm.ScimGroup) error {
		r.replacedGroups = append(r.replacedGroups, group)
		return nil
	}
	r.ds.DeleteScimGroupFunc = func(_ context.Context, id uint) error {
		r.deletedGroups = append(r.deletedGroups, id)
		return nil
	}
	r.ds.UpdateScimLastRequestFunc = func(_ context.Context, lastRequest *automdm.ScimLastRequest) error {
		r.lastRequest = lastRequest
		return nil
	}
	return r
}

func scimUser(extID, userName, dept string, active bool) automdm.ScimUser {
	return automdm.ScimUser{
		ExternalID: new(extID),
		UserName:   userName,
		Department: new(dept),
		Active:     new(active),
		Emails:     []automdm.ScimUserEmail{{Email: userName, Primary: new(true)}},
	}
}

func gwUser(extID, userName, dept string, active bool) *automdm.ScimUser {
	u := scimUser(extID, userName, dept, active)
	return &u
}

func runSync(t *testing.T, r *syncRecorder, dir automdm.GoogleWorkspaceDirectory) error {
	t.Helper()
	return cronGoogleWorkspaceSync(t.Context(), r.ds, fakeFactory(dir, nil), slog.New(slog.DiscardHandler))
}

func TestGoogleWorkspaceSyncCreatesUsersAndGroups(t *testing.T) {
	r := newSyncRecorder(gwAppConfig(), nil, nil)
	dir := &fakeDirectory{
		users: []*automdm.ScimUser{
			gwUser("g1", "alice@example.com", "Engineering", true),
			gwUser("g2", "bob@example.com", "Sales", true),
		},
		groups: []*automdm.GoogleWorkspaceGroup{
			{ExternalID: "grp1", DisplayName: "Engineering", MemberExternalIDs: []string{"g1", "g2"}},
		},
	}

	require.NoError(t, runSync(t, r, dir))

	require.Len(t, r.createdUsers, 2)
	assert.Empty(t, r.replacedUsers)
	assert.Empty(t, r.deletedUsers)

	require.Len(t, r.createdGroups, 1)
	// Members resolved to the IDs returned by CreateScimUser.
	require.Len(t, r.createdGroups[0].ScimUsers, 2)
	assert.ElementsMatch(t, []uint{r.createdUsers[0].ID, r.createdUsers[1].ID}, r.createdGroups[0].ScimUsers)

	require.NotNil(t, r.lastRequest)
	assert.Equal(t, "success", r.lastRequest.Status)
}

func TestGoogleWorkspaceSyncUpdatesChangedUser(t *testing.T) {
	existing := scimUser("g1", "alice@example.com", "Engineering", true)
	existing.ID = 1
	r := newSyncRecorder(gwAppConfig(), []automdm.ScimUser{existing}, nil)

	dir := &fakeDirectory{users: []*automdm.ScimUser{
		gwUser("g1", "alice@example.com", "Marketing", true), // department changed
	}}

	require.NoError(t, runSync(t, r, dir))
	assert.Empty(t, r.createdUsers)
	require.Len(t, r.replacedUsers, 1)
	assert.Equal(t, uint(1), r.replacedUsers[0].ID)
	assert.Empty(t, r.deletedUsers)
}

func TestGoogleWorkspaceSyncIsIdempotent(t *testing.T) {
	existing := scimUser("g1", "alice@example.com", "Engineering", true)
	existing.ID = 1
	r := newSyncRecorder(gwAppConfig(), []automdm.ScimUser{existing}, nil)

	dir := &fakeDirectory{users: []*automdm.ScimUser{
		gwUser("g1", "alice@example.com", "Engineering", true), // identical
	}}

	require.NoError(t, runSync(t, r, dir))
	assert.Empty(t, r.createdUsers)
	assert.Empty(t, r.replacedUsers, "unchanged user must not be replaced")
	assert.Empty(t, r.deletedUsers)
	require.NotNil(t, r.lastRequest)
	assert.Equal(t, "success", r.lastRequest.Status)
}

func TestGoogleWorkspaceSyncDeletesRemovedUser(t *testing.T) {
	keep := scimUser("g1", "alice@example.com", "Engineering", true)
	keep.ID = 1
	gone := scimUser("g2", "bob@example.com", "Sales", true)
	gone.ID = 2
	r := newSyncRecorder(gwAppConfig(), []automdm.ScimUser{keep, gone}, nil)

	dir := &fakeDirectory{users: []*automdm.ScimUser{
		gwUser("g1", "alice@example.com", "Engineering", true),
	}}

	require.NoError(t, runSync(t, r, dir))
	assert.Empty(t, r.createdUsers)
	assert.Empty(t, r.replacedUsers)
	require.Equal(t, []uint{2}, r.deletedUsers)
}

func TestGoogleWorkspaceSyncEmptyPullDoesNotDelete(t *testing.T) {
	existing := scimUser("g1", "alice@example.com", "Engineering", true)
	existing.ID = 1
	existingGroup := automdm.ScimGroup{ID: 5, ExternalID: new("grp1"), DisplayName: "Engineering"}
	r := newSyncRecorder(gwAppConfig(), []automdm.ScimUser{existing}, []automdm.ScimGroup{existingGroup})

	dir := &fakeDirectory{} // empty pull

	require.NoError(t, runSync(t, r, dir))
	assert.Empty(t, r.deletedUsers, "empty pull must not wipe users")
	assert.Empty(t, r.deletedGroups, "empty pull must not wipe groups")
}

// TestGoogleWorkspaceSyncLimitErrorDoesNotDelete pins the invariant the directory
// sync limits rely on: a pull that hits a limit must abort, leaving existing IdP
// data alone. Reconciliation treats the pull as authoritative, so a limit that
// truncated instead of erroring would delete every record past it.
func TestGoogleWorkspaceSyncLimitErrorDoesNotDelete(t *testing.T) {
	existingUser := scimUser("g1", "alice@example.com", "Engineering", true)
	existingUser.ID = 1
	existingGroup := automdm.ScimGroup{ID: 5, ExternalID: new("grp1"), DisplayName: "Engineering"}

	for _, tc := range []struct {
		name string
		dir  *fakeDirectory
	}{
		{
			name: "users limit",
			dir:  &fakeDirectory{usersErr: errors.New("exceeded the limit of 500000 users; raise google_workspace.max_users to sync this domain")},
		},
		{
			name: "groups limit",
			dir: &fakeDirectory{
				users:     []*automdm.ScimUser{gwUser("g1", "alice@example.com", "Engineering", true)},
				groupsErr: errors.New("exceeded the limit of 100000 groups; raise google_workspace.max_groups to sync this domain"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSyncRecorder(gwAppConfig(), []automdm.ScimUser{existingUser}, []automdm.ScimGroup{existingGroup})

			err := runSync(t, r, tc.dir)
			require.Error(t, err)
			assert.Empty(t, r.deletedUsers, "a limit error must not delete users")
			assert.Empty(t, r.deletedGroups, "a limit error must not delete groups")
			require.NotNil(t, r.lastRequest)
			assert.Equal(t, "error", r.lastRequest.Status)
			assert.Contains(t, r.lastRequest.Details, "raise google_workspace.max_")
		})
	}
}

func TestGoogleWorkspaceSyncDirectoryErrorRecordsStatus(t *testing.T) {
	r := newSyncRecorder(gwAppConfig(), nil, nil)
	dir := &fakeDirectory{usersErr: errors.New("delegation not authorized")}

	err := runSync(t, r, dir)
	require.Error(t, err)
	require.NotNil(t, r.lastRequest)
	assert.Equal(t, "error", r.lastRequest.Status)
	assert.Contains(t, r.lastRequest.Details, "delegation not authorized")
}

func TestGoogleWorkspaceSyncLongErrorIsTruncated(t *testing.T) {
	r := newSyncRecorder(gwAppConfig(), nil, nil)
	// Enforce the real scim_last_request.details VARCHAR(255) constraint so an
	// over-length, un-truncated message would fail to record (the original bug).
	r.ds.UpdateScimLastRequestFunc = func(_ context.Context, lastRequest *automdm.ScimLastRequest) error {
		if utf8.RuneCountInString(lastRequest.Details) > automdm.SCIMMaxFieldLength {
			return fmt.Errorf("details exceeds maximum length of %d characters", automdm.SCIMMaxFieldLength)
		}
		r.lastRequest = lastRequest
		return nil
	}
	longMsg := strings.Repeat("é", automdm.SCIMMaxFieldLength*2) // multi-byte to exercise rune-safe truncation
	dir := &fakeDirectory{usersErr: errors.New(longMsg)}

	err := runSync(t, r, dir)
	require.Error(t, err) // the sync error itself still propagates
	require.NotNil(t, r.lastRequest, "status must be recorded even for an over-length error")
	assert.Equal(t, "error", r.lastRequest.Status)
	assert.LessOrEqual(t, utf8.RuneCountInString(r.lastRequest.Details), automdm.SCIMMaxFieldLength)
	assert.True(t, utf8.ValidString(r.lastRequest.Details), "truncation must not split a multi-byte rune")
}

func TestGoogleWorkspaceSyncBestEffortContinuesPastFailedUser(t *testing.T) {
	r := newSyncRecorder(gwAppConfig(), nil, nil)
	// One user's creation fails (e.g. a unique-constraint conflict); the others
	// must still be ingested rather than the whole sync aborting.
	r.ds.CreateScimUserFunc = func(_ context.Context, user *automdm.ScimUser) (uint, error) {
		if user.UserName == "bad@example.com" {
			return 0, errors.New("ScimUser \"bad@example.com\" already exists")
		}
		r.createdUsers = append(r.createdUsers, user)
		return uint(1000 + len(r.createdUsers)), nil
	}
	dir := &fakeDirectory{users: []*automdm.ScimUser{
		gwUser("g1", "good1@example.com", "Eng", true),
		gwUser("g2", "bad@example.com", "Eng", true),
		gwUser("g3", "good2@example.com", "Eng", true),
	}}

	err := runSync(t, r, dir)
	require.Error(t, err) // partial failure is surfaced...
	assert.Contains(t, err.Error(), "partial sync")
	// ...but the two good users were still created despite the one failure.
	assert.Len(t, r.createdUsers, 2)
	require.NotNil(t, r.lastRequest)
	assert.Equal(t, "error", r.lastRequest.Status)
	assert.Contains(t, r.lastRequest.Details, "partial sync")
}

func TestGoogleWorkspaceSyncNotConfiguredNoOp(t *testing.T) {
	r := newSyncRecorder(&automdm.AppConfig{}, nil, nil) // no GoogleWorkspace integration
	dir := &fakeDirectory{users: []*automdm.ScimUser{gwUser("g1", "a@example.com", "Eng", true)}}

	require.NoError(t, runSync(t, r, dir))
	assert.Empty(t, r.createdUsers)
	assert.Nil(t, r.lastRequest, "no status recorded when not configured")
}

func TestGoogleWorkspaceSyncGroupMembershipUpdate(t *testing.T) {
	user := scimUser("g1", "alice@example.com", "Engineering", true)
	user.ID = 1
	group := automdm.ScimGroup{ID: 5, ExternalID: new("grp1"), DisplayName: "Engineering", ScimUsers: []uint{}}
	r := newSyncRecorder(gwAppConfig(), []automdm.ScimUser{user}, []automdm.ScimGroup{group})

	dir := &fakeDirectory{
		users: []*automdm.ScimUser{gwUser("g1", "alice@example.com", "Engineering", true)},
		groups: []*automdm.GoogleWorkspaceGroup{
			{ExternalID: "grp1", DisplayName: "Engineering", MemberExternalIDs: []string{"g1"}}, // alice added
		},
	}

	require.NoError(t, runSync(t, r, dir))
	require.Len(t, r.replacedGroups, 1)
	assert.Equal(t, []uint{1}, r.replacedGroups[0].ScimUsers)
}

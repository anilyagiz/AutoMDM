package chartacl

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewerScopeNoViewerInContextErrors(t *testing.T) {
	a := NewTeamViewerAdapter()
	_, _, err := a.ViewerScope(t.Context())
	require.Error(t, err, "missing viewer should fail closed, not silently return global=false")
}

func TestViewerScopeGlobalUser(t *testing.T) {
	a := NewTeamViewerAdapter()

	role := automdm.RoleAdmin
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{
		User: &automdm.User{GlobalRole: &role},
	})

	isGlobal, teamIDs, err := a.ViewerScope(ctx)
	require.NoError(t, err)
	assert.True(t, isGlobal)
	assert.Nil(t, teamIDs, "global user's team list is not used by the caller")
}

func TestViewerScopeGlobalUserEmptyRoleStringIsNotGlobal(t *testing.T) {
	// Defensive: a User with a non-nil but empty GlobalRole string should not
	// be treated as global. This matches the User.HasAnyGlobalRole semantics.
	a := NewTeamViewerAdapter()

	empty := ""
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{
		User: &automdm.User{GlobalRole: &empty},
	})

	isGlobal, _, err := a.ViewerScope(ctx)
	require.NoError(t, err)
	assert.False(t, isGlobal)
}

func TestViewerScopeTeamUser(t *testing.T) {
	a := NewTeamViewerAdapter()

	ctx := viewer.NewContext(t.Context(), viewer.Viewer{
		User: &automdm.User{
			Teams: []automdm.UserTeam{
				{Team: automdm.Team{ID: 3}, Role: automdm.RoleObserver},
				{Team: automdm.Team{ID: 7}, Role: automdm.RoleMaintainer},
			},
		},
	})

	isGlobal, teamIDs, err := a.ViewerScope(ctx)
	require.NoError(t, err)
	assert.False(t, isGlobal)
	assert.Equal(t, []uint{3, 7}, teamIDs)
}

func TestViewerScopeTeamUserNoTeams(t *testing.T) {
	// Authenticated user with neither a global role nor any team memberships.
	// The chart service treats this as "team-scoped with zero teams" and
	// returns empty data — the adapter's job is just to report the scope
	// faithfully.
	a := NewTeamViewerAdapter()

	ctx := viewer.NewContext(t.Context(), viewer.Viewer{
		User: &automdm.User{},
	})

	isGlobal, teamIDs, err := a.ViewerScope(ctx)
	require.NoError(t, err)
	assert.False(t, isGlobal)
	assert.Empty(t, teamIDs)
}

func TestExpandPlatformDelegatesToAutoMDM(t *testing.T) {
	assert.Equal(t, automdm.ExpandPlatform("linux"), ExpandPlatform("linux"))
	assert.Equal(t, []string{"darwin"}, ExpandPlatform("darwin"))
}

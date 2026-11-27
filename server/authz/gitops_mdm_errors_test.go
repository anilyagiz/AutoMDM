package authz

import (
	"errors"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/require"
)

func TestGitOpsErrors(t *testing.T) {
	authzErr := errors.New("authz failed")
	automdmGitOps := &automdm.User{Teams: []automdm.UserTeam{{ID: 1, Role: automdm.RoleGitOps}}}
	cases := []struct {
		name            string
		user            *automdm.User
		teamID          *uint
		wantHostMessage bool // GitOpsAutoMDMError
		wantListMessage bool // GitOpsListError
	}{
		{"global gitops, no team", test.UserGitOps, nil, true, true},
		{"global gitops, team", test.UserGitOps, new(uint(1)), true, true},
		{"automdm gitops, own team", automdmGitOps, new(uint(1)), true, true},
		{"automdm gitops, other team", automdmGitOps, new(uint(2)), false, true},
		{"automdm gitops, no team", automdmGitOps, nil, false, true},
		{"automdm gitops on another team, maintainer on this one", &automdm.User{Teams: []automdm.UserTeam{
			{ID: 1, Role: automdm.RoleMaintainer}, {ID: 2, Role: automdm.RoleGitOps},
		}}, new(uint(1)), false, true},
		{"global maintainer", test.UserMaintainer, nil, false, false},
		{"team observer", test.UserTeamObserverTeam1, new(uint(1)), false, false},
		{"no viewer", nil, nil, false, false},
	}
	const message = automdm.GitOpsDeleteHostErrorMessage
	requireMessage := func(t *testing.T, wantMessage bool, wantText string, err error) {
		t.Helper()
		if !wantMessage {
			require.Equal(t, authzErr, err)
			return
		}
		var forbidden *Forbidden
		require.ErrorAs(t, err, &forbidden)
		require.Equal(t, wantText, err.Error())
		require.Equal(t, authzErr.Error(), forbidden.Internal())
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := t.Context()
			if c.user != nil {
				ctx = viewer.NewContext(ctx, viewer.Viewer{User: c.user})
			}
			requireMessage(t, c.wantHostMessage, message, GitOpsAutoMDMError(ctx, c.teamID, message, authzErr))
			requireMessage(t, c.wantListMessage, message, GitOpsListError(ctx, message, authzErr))
		})
	}

	t.Run("MDM command wrappers", func(t *testing.T) {
		ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: test.UserGitOps})
		requireMessage(t, true, automdm.GitOpsMDMCommandErrorMessage, GitOpsMDMCommandError(ctx, nil, authzErr))
		requireMessage(t, true, automdm.GitOpsMDMCommandErrorMessage, GitOpsHostListError(ctx, authzErr))
	})
}

package service

import (
	"context"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
)

func TestListVulnerabilities(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	ds.ListVulnerabilitiesFunc = func(cxt context.Context, opt automdm.VulnListOptions) ([]automdm.VulnerabilityWithMetadata, *automdm.PaginationMetadata, error) {
		return []automdm.VulnerabilityWithMetadata{
			{
				CVE: automdm.CVE{
					CVE:         "CVE-2019-1234",
					Description: ptr.StringPtr("A vulnerability"),
				},
				CreatedAt:  time.Now(),
				HostsCount: 10,
			},
		}, nil, nil
	}

	t.Run("no list options", func(t *testing.T) {
		_, _, err := svc.ListVulnerabilities(ctx, automdm.VulnListOptions{})
		require.NoError(t, err)
	})

	t.Run("can only sort by supported columns", func(t *testing.T) {
		// invalid order key
		opts := automdm.VulnListOptions{ListOptions: automdm.ListOptions{
			OrderKey: "invalid",
		}, ValidSortColumns: freeValidVulnSortColumns}

		_, _, err := svc.ListVulnerabilities(ctx, opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid order key")

		// valid order key
		opts.ListOptions.OrderKey = "cve"
		_, _, err = svc.ListVulnerabilities(ctx, opts)
		require.NoError(t, err)
	})
}

func TestVulnerabilitesAuth(t *testing.T) {
	ds := new(mock.Store)

	svc, ctx := newTestService(t, ds, nil, nil)

	ds.ListVulnerabilitiesFunc = func(cxt context.Context, opt automdm.VulnListOptions) ([]automdm.VulnerabilityWithMetadata, *automdm.PaginationMetadata, error) {
		return []automdm.VulnerabilityWithMetadata{}, &automdm.PaginationMetadata{}, nil
	}

	ds.VulnerabilityFunc = func(cxt context.Context, cve string, teamID *uint, includeCVEScores bool) (*automdm.VulnerabilityWithMetadata, error) {
		return &automdm.VulnerabilityWithMetadata{}, nil
	}

	ds.CountVulnerabilitiesFunc = func(cxt context.Context, opt automdm.VulnListOptions) (uint, error) {
		return 0, nil
	}

	ds.TeamExistsFunc = func(cxt context.Context, teamID uint) (bool, error) {
		return true, nil
	}

	for _, tc := range []struct {
		name                 string
		user                 *automdm.User
		shouldFailGlobalRead bool
		shouldFailTeamRead   bool
	}{
		{
			name: "global-admin",
			user: &automdm.User{
				ID:         1,
				GlobalRole: ptr.String(automdm.RoleAdmin),
			},
			shouldFailGlobalRead: false,
			shouldFailTeamRead:   false,
		},
		{
			name: "global-maintainer",
			user: &automdm.User{
				ID:         1,
				GlobalRole: ptr.String(automdm.RoleMaintainer),
			},
			shouldFailGlobalRead: false,
			shouldFailTeamRead:   false,
		},
		{
			name: "global-observer",
			user: &automdm.User{
				ID:         1,
				GlobalRole: ptr.String(automdm.RoleObserver),
			},
			shouldFailGlobalRead: false,
			shouldFailTeamRead:   false,
		},
		{
			name: "team-admin-belongs-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 1},
					Role: automdm.RoleAdmin,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   false,
		},
		{
			name: "team-maintainer-belongs-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 1},
					Role: automdm.RoleMaintainer,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   false,
		},
		{
			name: "team-observer-belongs-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 1},
					Role: automdm.RoleObserver,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   false,
		},
		{
			name: "team-admin-does-not-belong-to-team",
			user: &automdm.User{
				ID: 1,
				Teams: []automdm.UserTeam{{
					Team: automdm.Team{ID: 2},
					Role: automdm.RoleAdmin,
				}},
			},
			shouldFailGlobalRead: true,
			shouldFailTeamRead:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx = viewer.NewContext(ctx, viewer.Viewer{User: tc.user})
			_, _, err := svc.ListVulnerabilities(ctx, automdm.VulnListOptions{})
			checkAuthErr(t, tc.shouldFailGlobalRead, err)

			_, _, err = svc.ListVulnerabilities(ctx, automdm.VulnListOptions{
				TeamID: ptr.Uint(1),
			})
			checkAuthErr(t, tc.shouldFailTeamRead, err)

			_, err = svc.CountVulnerabilities(ctx, automdm.VulnListOptions{})
			checkAuthErr(t, tc.shouldFailGlobalRead, err)

			_, err = svc.CountVulnerabilities(ctx, automdm.VulnListOptions{
				TeamID: ptr.Uint(1),
			})
			checkAuthErr(t, tc.shouldFailTeamRead, err)

			_, _, err = svc.Vulnerability(ctx, "CVE-2019-1234", nil, false)
			checkAuthErr(t, tc.shouldFailGlobalRead, err)

			_, _, err = svc.Vulnerability(ctx, "CVE-2019-1234", ptr.Uint(1), false)
			checkAuthErr(t, tc.shouldFailTeamRead, err)
		})
	}
}

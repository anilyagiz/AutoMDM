package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/mock"
	common_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_logRoleChangeActivities(t *testing.T) {
	tests := []struct {
		name             string
		oldRole          *string
		newRole          *string
		oldTeamRoles     map[uint]string
		newTeamRoles     map[uint]string
		expectActivities []string
	}{
		{
			name: "Empty",
		}, {
			name:             "AddGlobal",
			newRole:          ptr.String("role"),
			expectActivities: []string{"changed_user_global_role"},
		}, {
			name:             "NoChangeGlobal",
			oldRole:          ptr.String("role"),
			newRole:          ptr.String("role"),
			expectActivities: []string{},
		}, {
			name:             "ChangeGlobal",
			oldRole:          ptr.String("old"),
			newRole:          ptr.String("role"),
			expectActivities: []string{"changed_user_global_role"},
		}, {
			name:             "Delete",
			oldRole:          ptr.String("old"),
			newRole:          nil,
			expectActivities: []string{"deleted_user_global_role"},
		}, {
			name:    "SwitchGlobalToTeams",
			oldRole: ptr.String("old"),
			newTeamRoles: map[uint]string{
				1: "foo",
				2: "bar",
				3: "baz",
			},
			expectActivities: []string{"deleted_user_global_role", "changed_user_team_role", "changed_user_team_role", "changed_user_team_role"},
		}, {
			name: "DeleteModifyTeam",
			oldTeamRoles: map[uint]string{
				1: "foo",
				2: "bar",
				3: "baz",
			},
			newTeamRoles: map[uint]string{
				2: "newRole",
				3: "baz",
			},
			expectActivities: []string{"changed_user_team_role", "deleted_user_team_role"},
		},
	}
	ds := new(mock.Store)
	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)
	var activities []string
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		activities = append(activities, activity.ActivityName())
		return nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activities = activities[:0]
			oldTeams := make([]automdm.UserTeam, 0, len(tt.oldTeamRoles))
			for id, r := range tt.oldTeamRoles {
				oldTeams = append(oldTeams, automdm.UserTeam{
					Team: automdm.Team{ID: id},
					Role: r,
				})
			}
			newTeams := make([]automdm.UserTeam, 0, len(tt.newTeamRoles))
			for id, r := range tt.newTeamRoles {
				newTeams = append(newTeams, automdm.UserTeam{
					Team: automdm.Team{ID: id},
					Role: r,
				})
			}
			newUser := &automdm.User{
				GlobalRole: tt.newRole,
				Teams:      newTeams,
			}
			require.NoError(t, automdm.LogRoleChangeActivities(ctx, svc, &automdm.User{}, tt.oldRole, oldTeams, newUser, false))
			require.Equal(t, tt.expectActivities, activities)
		})
	}
}

func isRoleChangeActivity(activity activity_api.ActivityDetails) bool {
	switch activity.(type) {
	case automdm.ActivityTypeChangedUserGlobalRole,
		automdm.ActivityTypeDeletedUserGlobalRole,
		automdm.ActivityTypeChangedUserTeamRole,
		automdm.ActivityTypeDeletedUserTeamRole:
		return true
	}
	return false
}

func TestNewUserRoleActivityJIT(t *testing.T) {
	for _, jitProvisioned := range []bool{true, false} {
		t.Run(fmt.Sprintf("jit=%t", jitProvisioned), func(t *testing.T) {
			ds := new(mock.Store)
			opts := &TestServerOpts{}
			svc, ctx := newTestService(t, ds, nil, nil, opts)

			var roleActivities []automdm.ActivityTypeChangedUserGlobalRole
			opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
				if a, ok := activity.(automdm.ActivityTypeChangedUserGlobalRole); ok {
					roleActivities = append(roleActivities, a)
				}
				return nil
			}
			ds.NewUserFunc = func(ctx context.Context, user *automdm.User) (*automdm.User, error) {
				user.ID = 1
				return user, nil
			}

			_, err := svc.NewUser(ctx, automdm.UserPayload{
				Name:           new("SSO User"),
				Email:          new("sso@example.com"),
				SSOEnabled:     new(true),
				GlobalRole:     new(automdm.RoleObserver),
				JITProvisioned: jitProvisioned,
			})
			require.NoError(t, err)

			require.Len(t, roleActivities, 1)
			require.Equal(t, automdm.RoleObserver, roleActivities[0].Role)
			require.Equal(t, jitProvisioned, roleActivities[0].JIT)
		})
	}
}

func TestCancelHostUpcomingActivityAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

	const (
		teamHostID   = 1
		globalHostID = 2
	)

	teamHost := &automdm.Host{TeamID: ptr.Uint(1), Platform: "darwin"}
	globalHost := &automdm.Host{Platform: "darwin"}

	ds.HostLiteFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
		if hostID == teamHostID {
			return teamHost, nil
		}
		return globalHost, nil
	}
	ds.CancelHostUpcomingActivityFunc = func(ctx context.Context, hostID uint, execID string) (automdm.ActivityDetails, error) {
		return nil, nil
	}
	ds.GetHostUpcomingActivityMetaFunc = func(ctx context.Context, hostID uint, execID string) (*automdm.UpcomingActivityMeta, error) {
		return &automdm.UpcomingActivityMeta{}, nil
	}
	// svc.CancelHostUpcomingActivity now looks up VPP release info before the
	// cancel runs (so pre-activation cancels can still release the reserved
	// seat). This test doesn't exercise VPP installs, so return notFound to
	// skip the release path.
	ds.GetVPPInstallReleaseInfoForCancelFunc = func(ctx context.Context, hostID uint, execID string) (*automdm.VPPInstallReleaseInfo, error) {
		return nil, &notFoundError{}
	}

	cases := []struct {
		name             string
		user             *automdm.User
		shouldFailGlobal bool
		shouldFailTeam   bool
	}{
		{
			name:             "global observer",
			user:             &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
		{
			name:             "team observer",
			user:             &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
		{
			name:             "global observer plus",
			user:             &automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
		{
			name:             "team observer plus",
			user:             &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
		{
			name:             "global admin",
			user:             &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			shouldFailGlobal: false,
			shouldFailTeam:   false,
		},
		{
			name:             "team admin",
			user:             &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			shouldFailGlobal: true,
			shouldFailTeam:   false,
		},
		{
			name:             "global maintainer",
			user:             &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			shouldFailGlobal: false,
			shouldFailTeam:   false,
		},
		{
			name:             "team maintainer",
			user:             &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			shouldFailGlobal: true,
			shouldFailTeam:   false,
		},
		{
			name:             "team admin wrong team",
			user:             &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 42}, Role: automdm.RoleAdmin}}},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
		{
			name:             "team maintainer wrong team",
			user:             &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 42}, Role: automdm.RoleMaintainer}}},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
		{
			name:             "global gitops",
			user:             &automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
		{
			name:             "team gitops",
			user:             &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			shouldFailGlobal: true,
			shouldFailTeam:   true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			err := svc.CancelHostUpcomingActivity(ctx, globalHostID, "abc")
			checkAuthErr(t, tt.shouldFailGlobal, err)
			err = svc.CancelHostUpcomingActivity(ctx, teamHostID, "abc")
			checkAuthErr(t, tt.shouldFailTeam, err)
		})
	}
}

func TestGetHostActivitiesWebhookSettings(t *testing.T) {
	newLicenseCtx := func(t *testing.T, tier string) context.Context {
		return license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: tier})
	}

	teamID1, teamID2 := uint(1), uint(2)
	hostInTeam := func(id uint, teamID *uint) *automdm.Host {
		return &automdm.Host{ID: id, TeamID: teamID}
	}

	newDS := func(hosts []*automdm.Host, teamWebhooks map[uint]*automdm.HostActivitiesWebhookSettings, noTeamWebhook *automdm.HostActivitiesWebhookSettings) *mock.Store {
		ds := new(mock.Store)
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return hosts, nil
		}
		ds.TeamLitesByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.TeamLite, error) {
			teams := make([]*automdm.TeamLite, 0, len(ids))
			for _, tid := range ids {
				// ID 0 ("Unassigned") is always present, synthesized from the
				// default team config like the real bulk query.
				if tid == 0 {
					teams = append(teams, &automdm.TeamLite{
						ID:     0,
						Config: automdm.TeamConfigLite{WebhookSettings: automdm.TeamWebhookSettings{HostActivitiesWebhook: noTeamWebhook}},
					})
					continue
				}
				// Teams absent from teamWebhooks are "deleted": omitted from
				// the result.
				webhook, ok := teamWebhooks[tid]
				if !ok {
					continue
				}
				teams = append(teams, &automdm.TeamLite{
					ID:     tid,
					Config: automdm.TeamConfigLite{WebhookSettings: automdm.TeamWebhookSettings{HostActivitiesWebhook: webhook}},
				})
			}
			return teams, nil
		}
		return ds
	}

	enabled := func(url string) *automdm.HostActivitiesWebhookSettings {
		return &automdm.HostActivitiesWebhookSettings{Enable: true, DestinationURL: url}
	}

	t.Run("free tier returns nil without touching the datastore", func(t *testing.T) {
		ds := newDS([]*automdm.Host{hostInTeam(1, &teamID1)}, map[uint]*automdm.HostActivitiesWebhookSettings{teamID1: enabled("https://example.com")}, nil)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierFree), []uint{1})
		require.NoError(t, err)
		require.Nil(t, settings)
		require.False(t, ds.ListHostsLiteByIDsFuncInvoked)
	})

	t.Run("returns the host's team webhook", func(t *testing.T) {
		ds := newDS([]*automdm.Host{hostInTeam(1, &teamID1)}, map[uint]*automdm.HostActivitiesWebhookSettings{teamID1: enabled("https://example.com/a")}, nil)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1})
		require.NoError(t, err)
		require.Len(t, settings, 1)
		require.Equal(t, "https://example.com/a", settings[0].DestinationURL)
	})

	t.Run("dedups hosts in the same team", func(t *testing.T) {
		ds := newDS(
			[]*automdm.Host{hostInTeam(1, &teamID1), hostInTeam(2, &teamID1)},
			map[uint]*automdm.HostActivitiesWebhookSettings{teamID1: enabled("https://example.com/a")},
			nil,
		)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1, 2})
		require.NoError(t, err)
		require.Len(t, settings, 1)
		require.Equal(t, []uint{1, 2}, settings[0].HostIDs)
	})

	t.Run("hosts across teams return one webhook per enabled team", func(t *testing.T) {
		ds := newDS(
			[]*automdm.Host{hostInTeam(1, &teamID1), hostInTeam(2, &teamID2), hostInTeam(3, nil)},
			map[uint]*automdm.HostActivitiesWebhookSettings{
				teamID1: enabled("https://example.com/a"),
				teamID2: {Enable: false, DestinationURL: "https://example.com/disabled"},
			},
			enabled("https://example.com/no-team"),
		)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1, 2, 3})
		require.NoError(t, err)
		// Each delivery carries only its own team's hosts (the automdm-2 host is
		// absent entirely: that team's webhook is disabled).
		hostsByURL := make(map[string][]uint, len(settings))
		for _, s := range settings {
			hostsByURL[s.DestinationURL] = s.HostIDs
		}
		require.Equal(t, map[string][]uint{
			"https://example.com/a":       {1},
			"https://example.com/no-team": {3},
		}, hostsByURL)
	})

	t.Run("teams sharing a destination URL yield separate scoped deliveries", func(t *testing.T) {
		ds := newDS(
			[]*automdm.Host{hostInTeam(1, &teamID1), hostInTeam(2, &teamID2)},
			map[uint]*automdm.HostActivitiesWebhookSettings{
				teamID1: enabled("https://example.com/shared"),
				teamID2: enabled("https://example.com/shared"),
			},
			nil,
		)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1, 2})
		require.NoError(t, err)
		// A delivery is one team's subscription: sharing a URL must not merge
		// teams' host IDs into one payload.
		require.Len(t, settings, 2)
		require.Equal(t, "https://example.com/shared", settings[0].DestinationURL)
		require.Equal(t, []uint{1}, settings[0].HostIDs)
		require.Equal(t, "https://example.com/shared", settings[1].DestinationURL)
		require.Equal(t, []uint{2}, settings[1].HostIDs)
	})

	t.Run("a team and no-team sharing a destination stay separate deliveries", func(t *testing.T) {
		ds := newDS(
			[]*automdm.Host{hostInTeam(1, &teamID1), hostInTeam(2, nil)},
			map[uint]*automdm.HostActivitiesWebhookSettings{teamID1: enabled("https://example.com/shared")},
			enabled("https://example.com/shared"),
		)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1, 2})
		require.NoError(t, err)
		require.Len(t, settings, 2)
		require.Equal(t, []uint{1}, settings[0].HostIDs)
		require.Equal(t, []uint{2}, settings[1].HostIDs)
	})

	t.Run("no-team host uses the default team config", func(t *testing.T) {
		ds := newDS([]*automdm.Host{hostInTeam(1, nil)}, nil, enabled("https://example.com/no-team"))
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1})
		require.NoError(t, err)
		require.Len(t, settings, 1)
		require.Equal(t, "https://example.com/no-team", settings[0].DestinationURL)
	})

	t.Run("deleted team is skipped", func(t *testing.T) {
		ds := newDS([]*automdm.Host{hostInTeam(1, &teamID1)}, nil, nil) // team absent from the bulk lookup
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1})
		require.NoError(t, err)
		require.Empty(t, settings)
	})

	t.Run("nil or empty-URL webhooks are filtered", func(t *testing.T) {
		ds := newDS(
			[]*automdm.Host{hostInTeam(1, &teamID1), hostInTeam(2, &teamID2)},
			map[uint]*automdm.HostActivitiesWebhookSettings{
				teamID1: nil,
				teamID2: {Enable: true, DestinationURL: ""},
			},
			nil,
		)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), []uint{1, 2})
		require.NoError(t, err)
		require.Empty(t, settings)
	})

	t.Run("no host IDs short-circuits", func(t *testing.T) {
		ds := new(mock.Store)
		svc := &Service{ds: ds}
		settings, err := svc.GetHostActivitiesWebhookSettings(newLicenseCtx(t, automdm.TierPremium), nil)
		require.NoError(t, err)
		require.Nil(t, settings)
		require.False(t, ds.ListHostsLiteByIDsFuncInvoked)
	})
}

// The wipe or erase this cleanup follows is already recorded, and the caller can't reach this code
// again on a retry, so a notifications failure is logged rather than returned.
func TestCancelActivitiesAndNotificationsForHost(t *testing.T) {
	ctx := context.Background()
	ds := new(mock.Store)
	ds.BatchCancelAllHostUpcomingActivitiesFunc = func(context.Context, uint) ([]automdm.ActivityDetails, error) {
		return nil, nil
	}

	notificationsSvc := &mock.MockNotificationsService{}
	notificationsSvc.FailNotificationsForHostFunc = func(context.Context, uint, string) error {
		return errors.New("notifications are down")
	}

	err := cancelActivitiesAndNotificationsForHost(ctx, ds, notificationsSvc, slog.New(slog.DiscardHandler), 1)
	require.NoError(t, err)
	require.True(t, notificationsSvc.FailNotificationsForHostFuncInvoked)

	// Cancelling the upcoming activities is the part of this cleanup the caller can still retry.
	ds.BatchCancelAllHostUpcomingActivitiesFunc = func(context.Context, uint) ([]automdm.ActivityDetails, error) {
		return nil, errors.New("database is down")
	}
	err = cancelActivitiesAndNotificationsForHost(ctx, ds, notificationsSvc, slog.New(slog.DiscardHandler), 1)
	require.Error(t, err)
}

func TestReleaseVPPSeatUsesRecordedToken(t *testing.T) {
	const adamID = "adam-1"
	// The host moved to a team whose first token is not the one that reserved the seat.
	host := &automdm.Host{ID: 1, HardwareSerial: "SERIAL-1", TeamID: new(uint(9))}

	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/assets/disassociate", r.URL.Path)
		auths = append(auths, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"eventId":"evt"}`))
	}))
	t.Cleanup(srv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_VPP_URL", srv.URL, t)

	newSvc := func() (*Service, *mock.Store) {
		ds := new(mock.Store)
		ds.GetHostMDMFunc = func(ctx context.Context, hostID uint) (*automdm.HostMDM, error) {
			return nil, common_mysql.NotFound("HostMDM")
		}
		return &Service{ds: ds, logger: slog.New(slog.DiscardHandler)}, ds
	}
	info := func(tokenID *uint) *automdm.VPPInstallReleaseInfo {
		return &automdm.VPPInstallReleaseInfo{AdamID: adamID, AssociatedEventID: "evt", VPPTokenID: tokenID}
	}

	t.Run("recorded token", func(t *testing.T) {
		auths = nil
		svc, ds := newSvc()
		ds.GetVPPTokenFunc = func(ctx context.Context, id uint) (*automdm.VPPTokenDB, error) {
			require.Equal(t, uint(5), id)
			return &automdm.VPPTokenDB{ID: 5, Token: "tok-5"}, nil
		}

		require.NoError(t, svc.releaseVPPSeat(t.Context(), host, info(new(uint(5))), nil))
		require.Equal(t, []string{"Bearer tok-5"}, auths)
		require.False(t, ds.GetVPPTokenByTeamIDFuncInvoked, "the host team's token must not be used")
	})

	t.Run("no recorded token", func(t *testing.T) {
		auths = nil
		svc, ds := newSvc()

		require.NoError(t, svc.releaseVPPSeat(t.Context(), host, info(nil), nil))
		require.Empty(t, auths)
		require.False(t, ds.GetVPPTokenFuncInvoked)
	})

	t.Run("recorded token deleted", func(t *testing.T) {
		auths = nil
		svc, ds := newSvc()
		ds.GetVPPTokenFunc = func(ctx context.Context, id uint) (*automdm.VPPTokenDB, error) {
			return nil, common_mysql.NotFound("VPPToken")
		}

		require.NoError(t, svc.releaseVPPSeat(t.Context(), host, info(new(uint(5))), nil))
		require.Empty(t, auths)
	})
}

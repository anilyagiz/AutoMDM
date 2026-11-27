package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamAuth(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		return &automdm.Team{}, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{}, nil
	}
	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{}, nil
	}
	ds.UpdateTeamUsersFunc = func(ctx context.Context, teamID uint, set []automdm.TeamUser, removeIDs []uint) (map[uint]automdm.User, error) {
		return nil, nil
	}
	ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		return &automdm.Team{}, nil
	}
	ds.ListUsersFunc = func(ctx context.Context, opt automdm.UserListOptions) ([]*automdm.User, error) {
		return nil, nil
	}
	ds.ListTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.ListOptions) ([]*automdm.Team, error) {
		return nil, nil
	}
	ds.DeleteTeamFunc = func(ctx context.Context, tid uint) error {
		return nil
	}
	ds.TeamEnrollSecretsFunc = func(ctx context.Context, teamID uint) ([]*automdm.EnrollSecret, error) {
		return nil, nil
	}
	ds.ApplyEnrollSecretsFunc = func(ctx context.Context, teamID *uint, secrets []*automdm.EnrollSecret) error {
		return nil
	}
	ds.BulkSetPendingMDMHostProfilesFunc = func(ctx context.Context, hids, tids []uint, puuids, uuids []string,
	) (updates automdm.MDMProfilesUpdates, err error) {
		return automdm.MDMProfilesUpdates{}, nil
	}
	ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
		return []*automdm.Host{}, nil
	}
	ds.CleanupDiskEncryptionKeysOnTeamChangeFunc = func(ctx context.Context, hostIDs []uint, newTeamID *uint) error {
		return nil
	}

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		switch name {
		case "team1":
			return &automdm.Team{ID: 1}, nil
		default:
			return &automdm.Team{ID: 2}, nil
		}
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}
	ds.ConditionalAccessMicrosoftGetFunc = func(ctx context.Context) (*automdm.ConditionalAccessMicrosoftIntegration, error) {
		return nil, &notFoundError{}
	}

	ds.GetCertificateTemplatesByTeamIDFunc = func(ctx context.Context, teamID uint, opts automdm.ListOptions) ([]*automdm.CertificateTemplateResponseSummary, *automdm.PaginationMetadata, error) {
		return []*automdm.CertificateTemplateResponseSummary{}, nil, nil
	}

	ds.SetHostCertificateTemplatesToPendingRemoveFunc = func(ctx context.Context, teamID uint) error {
		return nil
	}

	ds.GetEnrollSecretsFunc = func(ctx context.Context, teamID *uint) ([]*automdm.EnrollSecret, error) {
		return nil, nil
	}
	ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFunc = func(ctx context.Context, teamID *uint) ([]string, error) {
		return nil, nil
	}
	ds.ListVPPTokensFunc = func(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
		return nil, nil
	}

	testCases := []struct {
		name                       string
		user                       *automdm.User
		shouldFailTeamWrite        bool
		shouldFailGlobalWrite      bool
		shouldFailRead             bool
		shouldFailTeamSecretsWrite bool
		shouldFailTeamMemberWrite  bool
	}{
		{
			name:                       "global admin",
			user:                       &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			shouldFailTeamWrite:        false,
			shouldFailGlobalWrite:      false,
			shouldFailRead:             false,
			shouldFailTeamSecretsWrite: false,
			shouldFailTeamMemberWrite:  false,
		},
		{
			name:                       "global maintainer",
			user:                       &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			shouldFailTeamWrite:        true,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             false,
			shouldFailTeamSecretsWrite: false,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "global observer",
			user:                       &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			shouldFailTeamWrite:        true,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             false,
			shouldFailTeamSecretsWrite: true,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "team admin, belongs to team",
			user:                       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			shouldFailTeamWrite:        false,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             false,
			shouldFailTeamSecretsWrite: false,
			shouldFailTeamMemberWrite:  false,
		},
		{
			name:                       "team maintainer, belongs to team",
			user:                       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamWrite:        true,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             false,
			shouldFailTeamSecretsWrite: false,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "team observer, belongs to team",
			user:                       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			shouldFailTeamWrite:        true,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             false,
			shouldFailTeamSecretsWrite: true,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "team admin, DOES NOT belong to team",
			user:                       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
			shouldFailTeamWrite:        true,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             true,
			shouldFailTeamSecretsWrite: true,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "team maintainer, DOES NOT belong to team",
			user:                       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamWrite:        true,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             true,
			shouldFailTeamSecretsWrite: true,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "team observer, DOES NOT belong to team",
			user:                       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
			shouldFailTeamWrite:        true,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             true,
			shouldFailTeamSecretsWrite: true,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "global gitops",
			user:                       &automdm.User{GlobalRole: new(automdm.RoleGitOps)},
			shouldFailTeamWrite:        false,
			shouldFailGlobalWrite:      false,
			shouldFailRead:             true,
			shouldFailTeamSecretsWrite: true,
			shouldFailTeamMemberWrite:  true,
		},
		{
			name:                       "team gitops, belongs to team",
			user:                       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			shouldFailTeamWrite:        false,
			shouldFailGlobalWrite:      true,
			shouldFailRead:             true,
			shouldFailTeamSecretsWrite: true,
			shouldFailTeamMemberWrite:  true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx = viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, err := svc.NewTeam(ctx, automdm.TeamPayload{Name: ptr.String("name")})
			checkAuthErr(t, tt.shouldFailGlobalWrite, err)

			_, err = svc.ModifyTeam(ctx, 1, automdm.TeamPayload{Name: ptr.String("othername")})
			checkAuthErr(t, tt.shouldFailTeamWrite, err)

			_, err = svc.ModifyTeamAgentOptions(ctx, 1, nil, automdm.ApplySpecOptions{})
			checkAuthErr(t, tt.shouldFailTeamWrite, err)

			_, err = svc.AddTeamUsers(ctx, 1, []automdm.TeamUser{})
			checkAuthErr(t, tt.shouldFailTeamMemberWrite, err)

			_, err = svc.DeleteTeamUsers(ctx, 1, []automdm.TeamUser{})
			checkAuthErr(t, tt.shouldFailTeamMemberWrite, err)

			_, err = svc.ListTeamUsers(ctx, 1, automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailRead, err)

			_, err = svc.ListTeams(ctx, automdm.ListOptions{})
			checkAuthErr(t, false, err) // everybody can do this

			_, err = svc.GetTeam(ctx, 1)
			checkAuthErr(t, tt.shouldFailRead, err)

			err = svc.DeleteTeam(ctx, 1)
			checkAuthErr(t, tt.shouldFailGlobalWrite, err)

			_, err = svc.TeamEnrollSecrets(ctx, 1)
			checkAuthErr(t, tt.shouldFailRead, err)

			_, err = svc.ModifyTeamEnrollSecrets(ctx, 1, []automdm.EnrollSecret{{Secret: "newteamsecret", CreatedAt: time.Now()}})
			checkAuthErr(t, tt.shouldFailTeamSecretsWrite, err)

			_, err = svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{{Name: "team1"}}, automdm.ApplyTeamSpecOptions{})
			checkAuthErr(t, tt.shouldFailTeamWrite, err)
		})
	}
}

func TestDeleteTeamRejectsNonAdminNonGitOpsRoles(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	for _, tt := range []struct {
		name string
		user *automdm.User
	}{
		{"global technician", &automdm.User{GlobalRole: new(automdm.RoleTechnician)}},
		{"team technician, belongs to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleTechnician}}}},
		{"global observer_plus", &automdm.User{GlobalRole: new(automdm.RoleObserverPlus)}},
		{"team observer_plus, belongs to team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
			err := svc.DeleteTeam(ctx, 1)
			checkAuthErr(t, true, err)
		})
	}
}

// TestGitOpsCannotManageTeamMembers verifies that a team gitops user cannot
// add or remove team members (including self-promotion to admin).
func TestGitOpsCannotManageTeamMembers(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{ID: tid}, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid}, nil
	}
	ds.UpdateTeamUsersFunc = func(ctx context.Context, teamID uint, set []automdm.TeamUser, removeIDs []uint) (map[uint]automdm.User, error) {
		previous := make(map[uint]automdm.User, len(set)+len(removeIDs))
		for _, user := range set {
			previous[user.ID] = automdm.User{ID: user.ID}
		}
		for _, id := range removeIDs {
			previous[id] = automdm.User{ID: id}
		}
		return previous, nil
	}
	ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
		return &automdm.User{ID: id}, nil
	}

	teamID := uint(1)
	gitopsUserID := uint(42)

	// Simulate a team gitops user.
	gitopsUser := &automdm.User{
		ID:    gitopsUserID,
		Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID}, Role: automdm.RoleGitOps}},
	}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: gitopsUser})

	// Attempt self-promotion: gitops user tries to make themselves a team admin.
	_, err := svc.AddTeamUsers(ctx, teamID, []automdm.TeamUser{
		{User: automdm.User{ID: gitopsUserID}, Role: automdm.RoleAdmin},
	})
	require.Error(t, err, "team gitops user should not be able to add team members")
	var forbidden *authz.Forbidden
	require.ErrorAs(t, err, &forbidden, "expected authorization error, got: %v", err)

	// Also verify gitops cannot remove team members.
	_, err = svc.DeleteTeamUsers(ctx, teamID, []automdm.TeamUser{
		{User: automdm.User{ID: 99}},
	})
	require.Error(t, err, "team gitops user should not be able to remove team members")
	require.ErrorAs(t, err, &forbidden, "expected authorization error, got: %v", err)

	// Verify that a team admin CAN still manage members (not broken for legitimate users).
	adminUser := &automdm.User{
		ID:    uint(10),
		Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID}, Role: automdm.RoleAdmin}},
	}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: adminUser})

	_, err = svc.AddTeamUsers(ctx, teamID, []automdm.TeamUser{
		{User: automdm.User{ID: gitopsUserID}, Role: automdm.RoleObserver},
	})
	require.NoError(t, err, "team admin should be able to add team members")

	_, err = svc.DeleteTeamUsers(ctx, teamID, []automdm.TeamUser{
		{User: automdm.User{ID: gitopsUserID}},
	})
	require.NoError(t, err, "team admin should be able to remove team members")
}

func TestApplyTeamSpecs(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	opts := &TestServerOpts{License: license, SkipCreateTestUsers: true}
	svc, ctx := newTestService(t, ds, nil, nil, opts)
	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}
	baseFeatures := automdm.Features{
		EnableHostUsers:         true,
		EnableSoftwareInventory: true,
		AdditionalQueries:       ptr.RawMessage(json.RawMessage(`{"foo": "bar"}`)),
		// Hydrated to match what teams loaded from the DB look like after
		// migration 20260423161823 backfills historical_data. Without these
		// defaults, the "Features for existing teams" cases below would
		// diff against the team-spec-applied defaults inside
		// editTeamFromSpec and spuriously emit historical_data enable
		// activities — which the test's activity callback doesn't expect.
		HistoricalData: automdm.HistoricalDataSettings{
			Uptime:          true,
			Vulnerabilities: true,
		},
	}

	mkspec := func(s string) *json.RawMessage {
		return ptr.RawMessage(json.RawMessage(s))
	}

	t.Run("Features for new teams", func(t *testing.T) {
		cases := []struct {
			name   string
			spec   *json.RawMessage
			global automdm.Features
			result automdm.Features
		}{
			{
				name:   "no spec features uses global config as defaults",
				spec:   nil,
				global: baseFeatures,
				result: baseFeatures,
			},
			{
				name:   "missing spec features uses new config default values",
				spec:   mkspec(`{"enable_software_inventory": false}`),
				global: baseFeatures,
				result: automdm.Features{
					EnableHostUsers:         true,
					EnableSoftwareInventory: false,
					AdditionalQueries:       nil,
					HistoricalData:          automdm.HistoricalDataSettings{Uptime: true, Vulnerabilities: true},
				},
			},
			{
				name:   "defaults can be overwritten",
				spec:   mkspec(`{"enable_host_users": false}`),
				global: baseFeatures,
				result: automdm.Features{
					EnableHostUsers:         false,
					EnableSoftwareInventory: true,
					AdditionalQueries:       nil,
					HistoricalData:          automdm.HistoricalDataSettings{Uptime: true, Vulnerabilities: true},
				},
			},
			{
				name: "all config can be changed",
				spec: mkspec(`{
          "enable_host_users": false,
          "enable_software_inventory": false,
          "additional_queries": {"example": "query"}
        }`),
				global: baseFeatures,
				result: automdm.Features{
					EnableHostUsers:         false,
					EnableSoftwareInventory: false,
					AdditionalQueries:       ptr.RawMessage([]byte(`{"example": "query"}`)),
					HistoricalData:          automdm.HistoricalDataSettings{Uptime: true, Vulnerabilities: true},
				},
			},
		}

		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
					return nil, newNotFoundError()
				}

				ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
					return &automdm.AppConfig{Features: tt.global}, nil
				}

				ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
					require.Equal(t, "team1", team.Name)
					require.Equal(t, tt.result, team.Config.Features)
					team.ID = 1
					return team, nil
				}

				opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
					act := activity.(automdm.ActivityTypeAppliedSpecTeam)
					require.Len(t, act.Teams, 1)
					return nil
				}
				ds.ConditionalAccessMicrosoftGetFunc = func(ctx context.Context) (*automdm.ConditionalAccessMicrosoftIntegration, error) {
					return nil, &notFoundError{}
				}

				_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{{Name: "team1", Features: tt.spec}}, automdm.ApplyTeamSpecOptions{})
				require.NoError(t, err)
			})
		}
	})

	t.Run("Features for existing teams", func(t *testing.T) {
		cases := []struct {
			name   string
			spec   *json.RawMessage
			old    automdm.Features
			result automdm.Features
		}{
			{
				name:   "no spec features uses old config",
				spec:   nil,
				old:    baseFeatures,
				result: baseFeatures,
			},
			{
				name: "missing spec features uses new config default values",
				spec: mkspec(`{"enable_software_inventory": false}`),
				old:  baseFeatures,
				result: automdm.Features{
					EnableHostUsers:         true,
					EnableSoftwareInventory: false,
					AdditionalQueries:       nil,
				},
			},
			{
				name: "config has defaults based on what are the global defaults",
				spec: mkspec(`{"additional_queries": {}}`),
				old:  baseFeatures,
				result: automdm.Features{
					EnableHostUsers:         true,
					EnableSoftwareInventory: true,
					AdditionalQueries:       nil,
				},
			},
			{
				name: "defaults can be overwritten",
				spec: mkspec(`{"enable_host_users": false}`),
				old:  baseFeatures,
				result: automdm.Features{
					EnableHostUsers:         false,
					EnableSoftwareInventory: false,
					AdditionalQueries:       nil,
				},
			},
			{
				name: "all config can be changed",
				spec: mkspec(`{
          "enable_host_users": false,
          "enable_software_inventory": true,
          "additional_queries": {"example": "query"}
        }`),
				old: baseFeatures,
				result: automdm.Features{
					EnableHostUsers:         false,
					EnableSoftwareInventory: true,
					AdditionalQueries:       ptr.RawMessage([]byte(`{"example": "query"}`)),
				},
			},
		}

		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
					return &automdm.Team{ID: 123, Name: name, Config: automdm.TeamConfig{Features: tt.old}}, nil
				}

				ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
					return &automdm.Team{ID: 123}, nil
				}

				opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
					act := activity.(automdm.ActivityTypeAppliedSpecTeam)
					require.Len(t, act.Teams, 1)
					return nil
				}

				idsByTeam, err := svc.ApplyTeamSpecs(
					ctx, []*automdm.TeamSpec{{Name: "team1", Features: tt.spec}}, automdm.ApplyTeamSpecOptions{},
				)
				require.NoError(t, err)
				require.Len(t, idsByTeam, 1)
				require.Equal(t, uint(123), idsByTeam["team1"])
			})
		}
	})
}

// Tests that a new enroll secret is created for new teams when none are provided
func TestApplyTeamSpecEnrollSecretForNewTeams(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})
	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, newNotFoundError()
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	t.Run("creates enroll secret when not included for a new team spec", func(t *testing.T) {
		ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			require.Len(t, team.Secrets, 1)
			require.NotEmpty(t, team.Secrets[0])
			return &automdm.Team{ID: 1}, nil
		}

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{{Name: "Foo"}}, automdm.ApplyTeamSpecOptions{})
		require.NoError(t, err)
		require.True(t, ds.TeamByNameFuncInvoked)
		require.True(t, ds.NewTeamFuncInvoked)
	})

	t.Run("does not create enroll secret when one is included for a new team spec", func(t *testing.T) {
		ds.NewTeamFuncInvoked = false
		enrollSecret := automdm.EnrollSecret{Secret: "test"}

		ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			require.Len(t, team.Secrets, 1)
			require.Equal(t, enrollSecret.Secret, team.Secrets[0].Secret)
			return &automdm.Team{ID: 1}, nil
		}
		ds.NewTeamFuncInvoked = false

		// Dry run -- secret already used
		ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, isNew bool, teamID *uint) (bool, error) {
			return false, nil
		}
		_, err := svc.ApplyTeamSpecs(
			ctx, []*automdm.TeamSpec{{Name: "Foo", Secrets: &[]automdm.EnrollSecret{enrollSecret}}},
			automdm.ApplyTeamSpecOptions{ApplySpecOptions: automdm.ApplySpecOptions{DryRun: true}},
		)
		assert.ErrorContains(t, err, "is already being used")

		// Normal dry run
		ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, isNew bool, teamID *uint) (bool, error) {
			return true, nil
		}
		_, err = svc.ApplyTeamSpecs(
			ctx, []*automdm.TeamSpec{{Name: "Foo", Secrets: &[]automdm.EnrollSecret{enrollSecret}}},
			automdm.ApplyTeamSpecOptions{ApplySpecOptions: automdm.ApplySpecOptions{DryRun: true}},
		)
		assert.NoError(t, err)
		assert.False(t, ds.NewTeamFuncInvoked)

		_, err = svc.ApplyTeamSpecs(
			ctx, []*automdm.TeamSpec{{Name: "Foo", Secrets: &[]automdm.EnrollSecret{enrollSecret}}}, automdm.ApplyTeamSpecOptions{},
		)
		require.NoError(t, err)
		require.True(t, ds.TeamByNameFuncInvoked)
		require.True(t, ds.NewTeamFuncInvoked)
	})

	ds.TeamByNameFuncInvoked = false
	ds.NewTeamFuncInvoked = false
}

// TestApplyTeamSpecsErrorInTeamByName tests that an error in ds.TeamByName will
// result in a proper error returned (instead of the authorization check missing error).
func TestApplyTeamSpecsErrorInTeamByName(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})
	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, errors.New("unknown error")
	}
	authzctx := &authz_ctx.AuthorizationContext{}
	ctx = authz_ctx.NewContext(ctx, authzctx)
	_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{{Name: "Foo"}}, automdm.ApplyTeamSpecOptions{})
	require.Error(t, err)
	az, ok := authz_ctx.FromContext(ctx)
	require.True(t, ok)
	require.True(t, az.Checked())
}

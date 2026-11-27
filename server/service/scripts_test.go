package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/mock"
	platform_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"github.com/anilyagiz/automdm/v4/server/platform/mysql/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/require"
)

func TestHostRunScript(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	// use a custom implementation of checkAuthErr as the service call will fail
	// with a not found error for unknown host in case of authorization success,
	// and the package-wide checkAuthErr requires no error.
	checkAuthErr := func(t *testing.T, shouldFail bool, err error) {
		if shouldFail {
			require.Error(t, err)
			require.Equal(t, (&authz.Forbidden{}).Error(), err.Error())
		} else if err != nil {
			require.NotEqual(t, (&authz.Forbidden{}).Error(), err.Error())
		}
	}

	teamHost := &automdm.Host{ID: 1, Hostname: "host-team", TeamID: ptr.Uint(1), SeenTime: time.Now(), OrbitNodeKey: ptr.String("abc")}
	noTeamHost := &automdm.Host{ID: 2, Hostname: "host-no-team", TeamID: nil, SeenTime: time.Now(), OrbitNodeKey: ptr.String("def")}
	nonExistingHost := &automdm.Host{ID: 3, Hostname: "no-such-host", TeamID: nil}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.HostFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
		if hostID == 1 {
			return teamHost, nil
		}
		if hostID == 2 {
			return noTeamHost, nil
		}
		return nil, newNotFoundError()
	}
	ds.NewHostScriptExecutionRequestFunc = func(ctx context.Context, request *automdm.HostScriptRequestPayload) (*automdm.HostScriptResult, error) {
		return &automdm.HostScriptResult{HostID: request.HostID, ScriptContents: request.ScriptContents, ExecutionID: "abc"}, nil
	}
	ds.ListPendingHostScriptExecutionsFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostScriptResult, error) {
		return nil, nil
	}
	ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
		return &automdm.Script{ID: id}, nil
	}
	ds.GetScriptContentsFunc = func(ctx context.Context, id uint) ([]byte, error) {
		return []byte("echo"), nil
	}
	ds.IsExecutionPendingForHostFunc = func(ctx context.Context, hostID, scriptID uint) (bool, error) { return false, nil }
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}

	t.Run("authorization checks", func(t *testing.T) {
		testCases := []struct {
			name                  string
			user                  *automdm.User
			scriptID              *uint
			shouldFailTeamWrite   bool
			shouldFailGlobalWrite bool
		}{
			{
				name:                  "global admin",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: false,
			},
			{
				name:                  "global admin saved",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: false,
			},
			{
				name:                  "global maintainer",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: false,
			},
			{
				name:                  "global maintainer saved",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: false,
			},
			{
				name:                  "global observer",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "global observer saved",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "global observer+",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "global observer+ saved",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "global gitops",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "global gitops saved",
				user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "global technician",
				user:                  &automdm.User{GlobalRole: new(automdm.RoleTechnician)},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "global technician saved",
				user:                  &automdm.User{GlobalRole: new(automdm.RoleTechnician)},
				scriptID:              new(uint(1)),
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: false,
			},
			{
				name:                  "team admin, belongs to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team admin, belongs to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team maintainer, belongs to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team maintainer, belongs to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer, belongs to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer, belongs to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer+, belongs to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer+, belongs to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team gitops, belongs to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team gitops, belongs to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team technician, belongs to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{ID: 1, Role: automdm.RoleTechnician}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team technician, belongs to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{ID: 1, Role: automdm.RoleTechnician}}},
				scriptID:              new(uint(1)),
				shouldFailTeamWrite:   false,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team admin, DOES NOT belong to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team admin, DOES NOT belong to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team maintainer, DOES NOT belong to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team maintainer, DOES NOT belong to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer, DOES NOT belong to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer, DOES NOT belong to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer+, DOES NOT belong to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserverPlus}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team observer+, DOES NOT belong to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserverPlus}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team gitops, DOES NOT belong to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleGitOps}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team gitops, DOES NOT belong to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleGitOps}}},
				scriptID:              ptr.Uint(1),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team technician, DOES NOT belong to team",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{ID: 2, Role: automdm.RoleTechnician}}},
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
			{
				name:                  "team technician, DOES NOT belong to team, saved",
				user:                  &automdm.User{Teams: []automdm.UserTeam{{ID: 2, Role: automdm.RoleTechnician}}},
				scriptID:              new(uint(1)),
				shouldFailTeamWrite:   true,
				shouldFailGlobalWrite: true,
			},
		}
		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				ctx = viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

				contents := "abc"
				if tt.scriptID != nil {
					contents = ""
				}
				_, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: noTeamHost.ID, ScriptContents: contents, ScriptID: tt.scriptID}, 0)
				checkAuthErr(t, tt.shouldFailGlobalWrite, err)
				_, err = svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: teamHost.ID, ScriptContents: contents, ScriptID: tt.scriptID}, 0)
				checkAuthErr(t, tt.shouldFailTeamWrite, err)

				if tt.scriptID == nil {
					// a non-existing host is authorized as for global write (because we can't know what team it belongs to)
					_, err = svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: nonExistingHost.ID, ScriptContents: "abc"}, 0)
					checkAuthErr(t, tt.shouldFailGlobalWrite, err)
				}

				// test auth for run sync saved script by name
				if tt.scriptID != nil {
					ds.GetScriptIDByNameFunc = func(ctx context.Context, name string, teamID *uint) (uint, error) {
						return *tt.scriptID, nil
					}
					_, err = svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: noTeamHost.ID, ScriptContents: "", ScriptID: nil, ScriptName: "Foo", TeamID: 1}, 1)
					checkAuthErr(t, tt.shouldFailGlobalWrite, err)
					_, err = svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: teamHost.ID, ScriptContents: "", ScriptID: nil, ScriptName: "Foo", TeamID: 1}, 1)
					checkAuthErr(t, tt.shouldFailTeamWrite, err)
				}
			})
		}
	})

	t.Run("script contents validation", func(t *testing.T) {
		testCases := []struct {
			name    string
			script  string
			wantErr string
		}{
			{"empty script", "", "One of 'script_id', 'script_contents', or 'script_name' is required."},
			{"overly long script", strings.Repeat("a", automdm.UnsavedScriptMaxRuneLen+1), "Script is too large."},
			{"large script", strings.Repeat("a", automdm.UnsavedScriptMaxRuneLen), ""},
			{"invalid utf8", "\xff\xfa", "Wrong data format."},
			{"valid without hashbang", "echo 'a'", ""},
			{"valid with posix hashbang", "#!/bin/sh\necho 'a'", ""},
			{"valid with usr bash hashbang", "#!/usr/bin/bash\necho 'a'", ""},
			{"valid with bash hashbang", "#!/bin/bash\necho 'a'", ""},
			{"valid with bash hashbang and arguments", "#!/bin/bash -x\necho 'a'", ""},
			{"valid with usr zsh hashbang", "#!/usr/bin/zsh\necho 'a'", ""},
			{"valid with zsh hashbang", "#!/bin/zsh\necho 'a'", ""},
			{"valid with zsh hashbang and arguments", "#!/bin/zsh -x\necho 'a'", ""},
			{"valid with hashbang and spacing", "#! /bin/sh  \necho 'a'", ""},
			{"valid with hashbang and Windows newline", "#! /bin/sh  \r\necho 'a'", ""},
			{"invalid hashbang", "#!/bin/ksh\necho 'a'", "Interpreter not supported."},
		}

		ctx = viewer.NewContext(ctx, viewer.Viewer{User: test.UserAdmin})
		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				_, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: noTeamHost.ID, ScriptContents: tt.script}, 0)
				if tt.wantErr != "" {
					require.ErrorContains(t, err, tt.wantErr)
				} else {
					require.NoError(t, err)
				}
			})
		}
	})
}

func TestGetScriptResult(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	const (
		noTeamHostExecID      = "no-team-host"
		teamHostExecID        = "team-host"
		nonExistingHostExecID = "non-existing-host"
	)

	checkAuthErr := func(t *testing.T, shouldFail bool, err error) {
		if shouldFail {
			require.Error(t, err)
			require.Equal(t, (&authz.Forbidden{}).Error(), err.Error())
		} else if err != nil {
			require.NotEqual(t, (&authz.Forbidden{}).Error(), err.Error())
		}
	}

	teamHost := &automdm.Host{ID: 1, Hostname: "host-team", TeamID: ptr.Uint(1), SeenTime: time.Now()}
	noTeamHost := &automdm.Host{ID: 2, Hostname: "host-no-team", TeamID: nil, SeenTime: time.Now()}
	nonExistingHost := &automdm.Host{ID: 3, Hostname: "no-such-host", TeamID: nil}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.GetHostScriptExecutionResultFunc = func(ctx context.Context, executionID string) (*automdm.HostScriptResult, error) {
		switch executionID {
		case noTeamHostExecID:
			return &automdm.HostScriptResult{HostID: noTeamHost.ID, ScriptContents: "abc", ExecutionID: executionID}, nil
		case teamHostExecID:
			return &automdm.HostScriptResult{HostID: teamHost.ID, ScriptContents: "abc", ExecutionID: executionID}, nil
		case nonExistingHostExecID:
			return &automdm.HostScriptResult{HostID: nonExistingHost.ID, ScriptContents: "abc", ExecutionID: executionID}, nil
		default:
			return nil, newNotFoundError()
		}
	}
	ds.HostLiteFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
		if hostID == 1 {
			return teamHost, nil
		}
		if hostID == 2 {
			return noTeamHost, nil
		}
		return nil, newNotFoundError()
	}

	testCases := []struct {
		name                 string
		user                 *automdm.User
		shouldFailTeamRead   bool
		shouldFailGlobalRead bool
	}{
		{
			name:                 "global admin",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global maintainer",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global observer",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global observer+",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global gitops",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team admin, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team maintainer, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer+, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team gitops, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team admin, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team maintainer, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer+, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserverPlus}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team gitops, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleGitOps}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx = viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, err := svc.GetScriptResult(ctx, noTeamHostExecID)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)
			_, err = svc.GetScriptResult(ctx, teamHostExecID)
			checkAuthErr(t, tt.shouldFailTeamRead, err)

			// a non-existing host is authorized as for global write (because we can't know what team it belongs to)
			_, err = svc.GetScriptResult(ctx, nonExistingHostExecID)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)
		})
	}
}

func TestSavedScripts(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	withLFContents := "echo\necho"
	withCRLFContents := "echo\r\necho"

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.NewScriptFunc = func(ctx context.Context, script *automdm.Script) (*automdm.Script, error) {
		require.Equal(t, withLFContents, script.ScriptContents)
		newScript := *script
		newScript.ID = 1
		return &newScript, nil
	}
	const (
		team1ScriptID  = 1
		noTeamScriptID = 2
	)
	ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
		switch id {
		case team1ScriptID:
			return &automdm.Script{ID: id, TeamID: ptr.Uint(1)}, nil
		default:
			return &automdm.Script{ID: id}, nil
		}
	}
	ds.GetScriptContentsFunc = func(ctx context.Context, id uint) ([]byte, error) {
		return []byte("echo"), nil
	}
	ds.DeleteScriptFunc = func(ctx context.Context, id uint) error {
		return nil
	}
	ds.ListScriptsFunc = func(ctx context.Context, teamID *uint, opt automdm.ListOptions) ([]*automdm.Script, *automdm.PaginationMetadata, error) {
		return nil, &automdm.PaginationMetadata{}, nil
	}
	ds.TeamWithExtrasFunc = func(ctx context.Context, id uint) (*automdm.Team, error) {
		return &automdm.Team{ID: 0}, nil
	}
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}
	ds.ExpandEmbeddedSecretsFunc = func(ctx context.Context, document string) (string, error) {
		return document, nil
	}

	testCases := []struct {
		name                  string
		user                  *automdm.User
		shouldFailTeamWrite   bool
		shouldFailGlobalWrite bool
		shouldFailTeamRead    bool
		shouldFailGlobalRead  bool
	}{
		{
			name:                  "global admin",
			user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			shouldFailTeamWrite:   false,
			shouldFailGlobalWrite: false,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  false,
		},
		{
			name:                  "global maintainer",
			user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			shouldFailTeamWrite:   false,
			shouldFailGlobalWrite: false,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  false,
		},
		{
			name:                  "global observer",
			user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  false,
		},
		{
			name:                  "global observer+",
			user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  false,
		},
		{
			name:                  "global gitops",
			user:                  &automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			shouldFailTeamWrite:   false,
			shouldFailGlobalWrite: false,
			shouldFailTeamRead:    true,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team admin, belongs to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			shouldFailTeamWrite:   false,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team maintainer, belongs to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamWrite:   false,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team observer, belongs to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team observer+, belongs to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    false,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team gitops, belongs to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			shouldFailTeamWrite:   false,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    true,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team admin, DOES NOT belong to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    true,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team maintainer, DOES NOT belong to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    true,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team observer, DOES NOT belong to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    true,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team observer+, DOES NOT belong to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserverPlus}}},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    true,
			shouldFailGlobalRead:  true,
		},
		{
			name:                  "team gitops, DOES NOT belong to team",
			user:                  &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleGitOps}}},
			shouldFailTeamWrite:   true,
			shouldFailGlobalWrite: true,
			shouldFailTeamRead:    true,
			shouldFailGlobalRead:  true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx = viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, err := svc.NewScript(ctx, nil, "test.ps1", strings.NewReader(withCRLFContents))
			checkAuthErr(t, tt.shouldFailGlobalWrite, err)
			err = svc.DeleteScript(ctx, noTeamScriptID)
			checkAuthErr(t, tt.shouldFailGlobalWrite, err)
			_, _, err = svc.ListScripts(ctx, nil, automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailGlobalRead, err)
			_, _, err = svc.GetScript(ctx, noTeamScriptID, false)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)
			_, _, err = svc.GetScript(ctx, noTeamScriptID, true)
			checkAuthErr(t, tt.shouldFailGlobalRead, err)

			_, err = svc.NewScript(ctx, ptr.Uint(1), "test.sh", strings.NewReader(withLFContents))
			checkAuthErr(t, tt.shouldFailTeamWrite, err)
			err = svc.DeleteScript(ctx, team1ScriptID)
			checkAuthErr(t, tt.shouldFailTeamWrite, err)
			_, _, err = svc.ListScripts(ctx, ptr.Uint(1), automdm.ListOptions{})
			checkAuthErr(t, tt.shouldFailTeamRead, err)
			_, _, err = svc.GetScript(ctx, team1ScriptID, false)
			checkAuthErr(t, tt.shouldFailTeamRead, err)
			_, _, err = svc.GetScript(ctx, team1ScriptID, true)
			checkAuthErr(t, tt.shouldFailTeamRead, err)
		})
	}
}

func TestHostScriptDetailsAuth(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	testCases := []struct {
		name                 string
		user                 *automdm.User
		shouldFailTeamRead   bool
		shouldFailGlobalRead bool
	}{
		{
			name:                 "global admin",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global maintainer",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global observer",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global observer+",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: false,
		},
		{
			name:                 "global gitops",
			user:                 &automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team admin, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team maintainer, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer+, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
			shouldFailTeamRead:   false,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team gitops, belongs to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team admin, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team maintainer, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserver}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team observer+, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleObserverPlus}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
		{
			name:                 "team gitops, DOES NOT belong to team",
			user:                 &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleGitOps}}},
			shouldFailTeamRead:   true,
			shouldFailGlobalRead: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx = viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			t.Run("no team host script details", func(t *testing.T) {
				ds.HostLiteFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
					require.Equal(t, uint(42), hostID)
					return &automdm.Host{ID: hostID}, nil
				}
				ds.GetHostScriptDetailsFunc = func(ctx context.Context, hostID uint, teamID *uint, opts automdm.ListOptions, hostPlatform string) ([]*automdm.HostScriptDetail, *automdm.PaginationMetadata, error) {
					require.Nil(t, teamID)
					return []*automdm.HostScriptDetail{}, nil, nil
				}
				_, _, err := svc.GetHostScriptDetails(ctx, 42, automdm.ListOptions{})
				checkAuthErr(t, tt.shouldFailGlobalRead, err)
			})

			t.Run("team host script details", func(t *testing.T) {
				ds.HostLiteFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
					require.Equal(t, uint(42), hostID)
					return &automdm.Host{ID: hostID, TeamID: ptr.Uint(1)}, nil
				}
				ds.GetHostScriptDetailsFunc = func(ctx context.Context, hostID uint, teamID *uint, opts automdm.ListOptions, hostPlatform string) ([]*automdm.HostScriptDetail, *automdm.PaginationMetadata, error) {
					require.NotNil(t, teamID)
					require.Equal(t, uint(1), *teamID)
					return []*automdm.HostScriptDetail{}, nil, nil
				}
				_, _, err := svc.GetHostScriptDetails(ctx, 42, automdm.ListOptions{})
				checkAuthErr(t, tt.shouldFailTeamRead, err)
			})

			t.Run("host not found", func(t *testing.T) {
				ds.HostLiteFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
					require.Equal(t, uint(43), hostID)
					return nil, &notFoundError{}
				}
				_, _, err := svc.GetHostScriptDetails(ctx, 43, automdm.ListOptions{})
				if tt.shouldFailGlobalRead {
					checkAuthErr(t, tt.shouldFailGlobalRead, err)
				} else {
					require.True(t, automdm.IsNotFound(err))
				}
			})
		})
	}
}

func TestBatchScriptExecute(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	t.Run("error if hosts do not all belong to the same team as script", func(t *testing.T) {
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 1, TeamID: ptr.Uint(1)},
				{ID: 2, TeamID: ptr.Uint(1)},
				{ID: 3, TeamID: ptr.Uint(2)},
			}, nil
		}
		ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
			if id == 1 {
				return &automdm.Script{ID: id, TeamID: ptr.Uint(1)}, nil
			}
			return &automdm.Script{ID: id}, nil
		}
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
		_, err := svc.BatchScriptExecute(ctx, 1, []uint{1, 2, 3}, nil, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "all hosts must be on the same team as the script")
	})

	t.Run("error if both host_ids and filters are specified", func(t *testing.T) {
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
		_, err := svc.BatchScriptExecute(ctx, 1, []uint{1, 2, 3}, &map[string]interface{}{"foo": "bar"}, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "cannot specify both host_ids and filters")
	})

	t.Run("error if filters are specified but no team_id", func(t *testing.T) {
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
		_, err := svc.BatchScriptExecute(ctx, 1, nil, &map[string]interface{}{"label_id": float64(123)}, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "filters must include a team filter")
	})

	t.Run("error if filters match too many hosts", func(t *testing.T) {
		hosts := make([]*automdm.Host, 5001)
		for i := 0; i < 5001; i++ {
			hosts[i] = &automdm.Host{ID: uint(i + 1), TeamID: ptr.Uint(1)} // nolint:gosec // ignore G115
		}
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return hosts, nil
		}
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return hosts, nil
		}
		ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
			if id == 1 {
				return &automdm.Script{ID: id, TeamID: ptr.Uint(1)}, nil
			}
			return &automdm.Script{ID: id}, nil
		}
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
		_, err := svc.BatchScriptExecute(ctx, 1, nil, &map[string]any{"team_id": float64(1)}, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "too_many_hosts")
	})

	t.Run("happy path", func(t *testing.T) {
		var requestedHostIds []uint
		ds.NewBatchScriptExecutionFunc = func(ctx context.Context, userID *uint, scriptID uint, hostIDs []uint) (string, error) {
			requestedHostIds = hostIDs
			return "", errors.New("ok")
		}
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 1, TeamID: ptr.Uint(1)},
				{ID: 2, TeamID: ptr.Uint(1)},
			}, nil
		}
		ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
			if id == 1 {
				return &automdm.Script{ID: id, TeamID: ptr.Uint(1)}, nil
			}
			return &automdm.Script{ID: id}, nil
		}
		ds.ListHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, opt automdm.HostListOptions) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 3, TeamID: ptr.Uint(1)},
				{ID: 4, TeamID: ptr.Uint(1)},
			}, nil
		}

		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
		_, err := svc.BatchScriptExecute(ctx, 1, []uint{1, 2}, nil, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "ok")
		require.Equal(t, []uint{1, 2}, requestedHostIds)

		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
		_, err = svc.BatchScriptExecute(ctx, 1, nil, &map[string]any{"team_id": float64(1)}, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "ok")
		require.Equal(t, []uint{3, 4}, requestedHostIds)
	})

	t.Run("authorization checks", func(t *testing.T) {
		checkAuthErr := func(t *testing.T, shouldFail bool, err error) {
			if shouldFail {
				require.Error(t, err)
				require.Equal(t, (&authz.Forbidden{}).Error(), err.Error())
			} else if err != nil {
				require.NotEqual(t, (&authz.Forbidden{}).Error(), err.Error())
			}
		}

		// The script and the hosts it runs on all belong to team 1.
		ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
			return &automdm.Script{ID: id, TeamID: new(uint(1))}, nil
		}
		ds.ListHostsLiteByIDsFunc = func(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				{ID: 1, TeamID: new(uint(1))},
				{ID: 2, TeamID: new(uint(1))},
			}, nil
		}
		// Return a non-authorization error so an authorized caller gets past the
		// authz checks; checkAuthErr only cares whether the error is Forbidden.
		ds.NewBatchScriptExecutionFunc = func(ctx context.Context, userID *uint, scriptID uint, hostIDs []uint) (string, error) {
			return "", errors.New("ok")
		}

		testCases := []struct {
			name       string
			user       *automdm.User
			shouldFail bool
		}{
			{
				name:       "global admin",
				user:       &automdm.User{GlobalRole: new(automdm.RoleAdmin)},
				shouldFail: false,
			},
			{
				name:       "global maintainer",
				user:       &automdm.User{GlobalRole: new(automdm.RoleMaintainer)},
				shouldFail: false,
			},
			{
				name:       "global observer",
				user:       &automdm.User{GlobalRole: new(automdm.RoleObserver)},
				shouldFail: true,
			},
			{
				name:       "global observer+",
				user:       &automdm.User{GlobalRole: new(automdm.RoleObserverPlus)},
				shouldFail: true,
			},
			{
				name:       "global gitops",
				user:       &automdm.User{GlobalRole: new(automdm.RoleGitOps)},
				shouldFail: true,
			},
			{
				name:       "global technician",
				user:       &automdm.User{GlobalRole: new(automdm.RoleTechnician)},
				shouldFail: true,
			},
			{
				name:       "team admin, belongs to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
				shouldFail: false,
			},
			{
				name:       "team maintainer, belongs to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
				shouldFail: false,
			},
			{
				name:       "team observer, belongs to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
				shouldFail: true,
			},
			{
				name:       "team observer+, belongs to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
				shouldFail: true,
			},
			{
				name:       "team gitops, belongs to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
				shouldFail: true,
			},
			{
				name:       "team technician, belongs to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleTechnician}}},
				shouldFail: true,
			},
			{
				name:       "team admin, does not belong to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
				shouldFail: true,
			},
			{
				name:       "team maintainer, does not belong to script team",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer}}},
				shouldFail: true,
			},
		}
		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
				_, err := svc.BatchScriptExecute(ctx, 1, []uint{1, 2}, nil, nil)
				checkAuthErr(t, tt.shouldFail, err)
			})
		}
	})
}

func TestWipeHostRequestDecodeBody(t *testing.T) {
	testCases := []struct {
		name            string
		body            io.Reader
		expectedError   string
		expectedRemoved bool
	}{
		{name: "empty body", body: strings.NewReader("")},
		{name: "empty payload", body: strings.NewReader(`{}`)},
		{name: "unrelated field is ignored", body: strings.NewReader(`{"foo": "bar"}`)},
		{name: "invalid JSON", body: strings.NewReader(`{"windows":`), expectedError: "failed to unmarshal request body"},
		{name: "doWipe", body: strings.NewReader(`{"windows": {"wipe_type": "doWipe"}}`), expectedRemoved: true},
		{name: "doWipeProtected", body: strings.NewReader(`{"windows": {"wipe_type": "doWipeProtected"}}`), expectedRemoved: true},
		{name: "windows field is null", body: strings.NewReader(`{"windows": null}`), expectedRemoved: true},
		{name: "windows field in title case", body: strings.NewReader(`{"Windows": {"wipe_type": "doWipe"}}`), expectedRemoved: true},
		{name: "windows field in upper case", body: strings.NewReader(`{"WINDOWS": null}`), expectedRemoved: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sut := automdm.WipeHostRequest{}
			err := sut.DecodeBody(t.Context(), tc.body, nil, nil)

			switch {
			case tc.expectedRemoved:
				var invalid *automdm.InvalidArgumentError
				require.ErrorAs(t, err, &invalid)
				require.Equal(t, []map[string]string{{
					"name":   "windows",
					"reason": `The "windows" parameter is not available anymore. What to use instead: https://automdm.com/learn-more-about/automdm-5-upgrade`,
				}}, invalid.Invalid())
			case tc.expectedError != "":
				require.ErrorContains(t, err, tc.expectedError)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestBatchScriptCancel(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	t.Run("not found", func(t *testing.T) {
		ds.ListBatchScriptExecutionsFunc = func(ctx context.Context, f automdm.BatchExecutionStatusFilter) ([]automdm.BatchActivity, error) {
			return nil, platform_mysql.NotFound("batch execution").WithName(*f.ExecutionID)
		}
		t.Run("global admin → not-found", func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
			err := svc.BatchScriptCancel(ctx, "unknown-id")
			require.Error(t, err)
			require.True(t, automdm.IsNotFound(err))
		})

		t.Run("team admin → forbidden, no leak", func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
				Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}},
			}})
			err := svc.BatchScriptCancel(ctx, "unknown-id")
			require.Error(t, err)
			require.Equal(t, (&authz.Forbidden{}).Error(), err.Error())
			require.False(t, automdm.IsNotFound(err))
		})

		t.Run("real DB error → wrapped, not IsNotFound", func(t *testing.T) {
			ds.ListBatchScriptExecutionsFunc = func(ctx context.Context, f automdm.BatchExecutionStatusFilter) ([]automdm.BatchActivity, error) {
				return nil, errors.New("connection refused")
			}
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
			err := svc.BatchScriptCancel(ctx, "any-id")
			require.Error(t, err)
			require.False(t, automdm.IsNotFound(err))
		})
	})
}

func TestBatchScriptExecutionStatus(t *testing.T) {
	ds := new(mock.Store)
	license := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: license, SkipCreateTestUsers: true})

	t.Run("not found", func(t *testing.T) {
		ds.ListBatchScriptExecutionsFunc = func(ctx context.Context, f automdm.BatchExecutionStatusFilter) ([]automdm.BatchActivity, error) {
			return nil, platform_mysql.NotFound("batch execution").WithName(*f.ExecutionID)
		}
		t.Run("global admin → not-found", func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
			_, err := svc.BatchScriptExecutionStatus(ctx, "unknown-id")
			require.Error(t, err)
			require.True(t, automdm.IsNotFound(err))
		})

		t.Run("team admin → forbidden, no leak", func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
				Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}},
			}})
			_, err := svc.BatchScriptExecutionStatus(ctx, "unknown-id")
			require.Error(t, err)
			require.Equal(t, (&authz.Forbidden{}).Error(), err.Error())
			require.False(t, automdm.IsNotFound(err))
		})

		t.Run("real DB error → wrapped, not IsNotFound", func(t *testing.T) {
			ds.ListBatchScriptExecutionsFunc = func(ctx context.Context, f automdm.BatchExecutionStatusFilter) ([]automdm.BatchActivity, error) {
				return nil, errors.New("connection refused")
			}
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})
			_, err := svc.BatchScriptExecutionStatus(ctx, "any-id")
			require.Error(t, err)
			require.False(t, automdm.IsNotFound(err))
		})
	})
}

func TestScriptAutoMDMVariablesValidation(t *testing.T) {
	newSvc := func(t *testing.T, tier string) (automdm.Service, context.Context, *mock.Store) {
		ds := new(mock.Store)
		lic := &automdm.LicenseInfo{Tier: tier, Expiration: time.Now().Add(24 * time.Hour)}
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: lic, SkipCreateTestUsers: true})
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		ds.HostFunc = func(ctx context.Context, hostID uint) (*automdm.Host, error) {
			return &automdm.Host{ID: hostID, SeenTime: time.Now(), OrbitNodeKey: new("abc")}, nil
		}
		ds.ListPendingHostScriptExecutionsFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostScriptResult, error) {
			return nil, nil
		}
		ds.NewHostScriptExecutionRequestFunc = func(ctx context.Context, request *automdm.HostScriptRequestPayload) (*automdm.HostScriptResult, error) {
			return &automdm.HostScriptResult{HostID: request.HostID, ScriptContents: request.ScriptContents, ExecutionID: "exec-1"}, nil
		}
		ds.NewScriptFunc = func(ctx context.Context, script *automdm.Script) (*automdm.Script, error) {
			newScript := *script
			newScript.ID = 1
			return &newScript, nil
		}
		ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
			return &automdm.Script{ID: id, Name: "test.sh"}, nil
		}
		ds.UpdateScriptContentsFunc = func(ctx context.Context, scriptID uint, contents string) (*automdm.Script, error) {
			return &automdm.Script{ID: scriptID, Name: "test.sh", ScriptContents: contents}, nil
		}
		ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
			return nil
		}
		return svc, ctx, ds
	}

	const (
		supportedVarContents   = "echo $AUTOMDM_VAR_HOST_UUID on ${AUTOMDM_VAR_HOST_PLATFORM}"
		unsupportedVarContents = "echo $AUTOMDM_VAR_NONEXISTENT"
		unsupportedVarErrMsg   = "AutoMDM variable $AUTOMDM_VAR_NONEXISTENT is not supported in scripts."
	)

	t.Run("premium", func(t *testing.T) {
		svc, ctx, ds := newSvc(t, automdm.TierPremium)

		t.Run("run host script", func(t *testing.T) {
			_, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: 1, ScriptContents: unsupportedVarContents}, 0)
			require.ErrorContains(t, err, unsupportedVarErrMsg)

			res, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: 1, ScriptContents: supportedVarContents}, 0)
			require.NoError(t, err)
			// contents are stored unexpanded; they resolve when automdmd fetches the script
			require.Equal(t, supportedVarContents, res.ScriptContents)
		})

		t.Run("new script", func(t *testing.T) {
			_, err := svc.NewScript(ctx, nil, "test.sh", strings.NewReader(unsupportedVarContents))
			require.ErrorContains(t, err, unsupportedVarErrMsg)

			saved, err := svc.NewScript(ctx, nil, "test.sh", strings.NewReader(supportedVarContents))
			require.NoError(t, err)
			require.Equal(t, supportedVarContents, saved.ScriptContents)
		})

		t.Run("update script", func(t *testing.T) {
			_, err := svc.UpdateScript(ctx, 1, strings.NewReader(unsupportedVarContents))
			require.ErrorContains(t, err, unsupportedVarErrMsg)

			saved, err := svc.UpdateScript(ctx, 1, strings.NewReader(supportedVarContents))
			require.NoError(t, err)
			require.Equal(t, supportedVarContents, saved.ScriptContents)
		})

		t.Run("batch set scripts", func(t *testing.T) {
			badPayload := []automdm.ScriptPayload{{Name: "test.sh", ScriptContents: []byte(unsupportedVarContents)}}
			goodPayload := []automdm.ScriptPayload{{Name: "test.sh", ScriptContents: []byte(supportedVarContents)}}

			// unsupported variables are rejected on dry run too, keyed on the
			// indexed field so callers can tell which script failed
			for _, dryRun := range []bool{true, false} {
				_, err := svc.BatchSetScripts(ctx, nil, nil, badPayload, dryRun)
				require.ErrorContains(t, err, unsupportedVarErrMsg, "dryRun=%v", dryRun)
				require.ErrorContains(t, err, "scripts[0]", "dryRun=%v", dryRun)
			}

			ds.BatchSetScriptsFunc = func(ctx context.Context, tmID *uint, scripts []*automdm.Script) ([]automdm.ScriptResponse, error) {
				require.Len(t, scripts, 1)
				require.Equal(t, supportedVarContents, scripts[0].ScriptContents)
				return []automdm.ScriptResponse{{ID: 1, Name: "test.sh"}}, nil
			}
			_, err := svc.BatchSetScripts(ctx, nil, nil, goodPayload, false)
			require.NoError(t, err)
			require.True(t, ds.BatchSetScriptsFuncInvoked)
		})
	})

	t.Run("free returns license error for any variable", func(t *testing.T) {
		svc, ctx, _ := newSvc(t, automdm.TierFree)

		for name, contents := range map[string]string{
			"supported":   supportedVarContents,
			"unsupported": unsupportedVarContents,
		} {
			t.Run(name, func(t *testing.T) {
				_, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: 1, ScriptContents: contents}, 0)
				require.ErrorIs(t, err, automdm.ErrMissingLicense)

				_, err = svc.NewScript(ctx, nil, "test.sh", strings.NewReader(contents))
				require.ErrorIs(t, err, automdm.ErrMissingLicense)

				_, err = svc.UpdateScript(ctx, 1, strings.NewReader(contents))
				require.ErrorIs(t, err, automdm.ErrMissingLicense)

				_, err = svc.BatchSetScripts(ctx, nil, nil,
					[]automdm.ScriptPayload{{Name: "test.sh", ScriptContents: []byte(contents)}}, true)
				require.ErrorIs(t, err, automdm.ErrMissingLicense)
			})
		}

		t.Run("variable-free scripts still work", func(t *testing.T) {
			res, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{HostID: 1, ScriptContents: "echo hello"}, 0)
			require.NoError(t, err)
			require.Equal(t, "echo hello", res.ScriptContents)

			_, err = svc.NewScript(ctx, nil, "test.sh", strings.NewReader("echo hello"))
			require.NoError(t, err)
		})
	})
}

func TestBatchSetScriptsDryRunNewTeam(t *testing.T) {
	ds := new(mock.Store)
	lic := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: lic, SkipCreateTestUsers: true})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, &notFoundError{}
	}
	ds.GetDuplicateStringGroupsUnderCollationFunc = func(ctx context.Context, values []string) ([]automdm.DuplicateStringGroup, error) {
		if len(values) == 2 && values[0] == values[1] {
			return []automdm.DuplicateStringGroup{{Indices: []int{0, 1}}}, nil
		}
		return nil, nil
	}

	newAutoMDM := new("New team")
	cases := []struct {
		name     string
		payloads []automdm.ScriptPayload
		wantErr  string
	}{
		{
			"unsupported interpreter",
			[]automdm.ScriptPayload{{Name: "unsupported.sh", ScriptContents: []byte("#!/usr/bin/perl\nprint \"hello\";")}},
			"Interpreter not supported",
		},
		{
			"unsupported file type",
			[]automdm.ScriptPayload{{Name: "script.txt", ScriptContents: []byte("echo hello")}},
			"File type not supported",
		},
		{
			"unsupported AutoMDM variable",
			[]automdm.ScriptPayload{{Name: "vars.sh", ScriptContents: []byte("echo $AUTOMDM_VAR_NONEXISTENT")}},
			"AutoMDM variable $AUTOMDM_VAR_NONEXISTENT is not supported in scripts.",
		},
		{
			"duplicate names",
			[]automdm.ScriptPayload{
				{Name: "dup.sh", ScriptContents: []byte("echo one")},
				{Name: "dup.sh", ScriptContents: []byte("echo two")},
			},
			"More than one script has the same file name",
		},
		{
			"valid payload",
			[]automdm.ScriptPayload{{Name: "valid.sh", ScriptContents: []byte("#!/bin/sh\necho hello")}},
			"",
		},
		{"empty payload", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := svc.BatchSetScripts(ctx, nil, newAutoMDM, c.payloads, true)
			if c.wantErr != "" {
				require.ErrorContains(t, err, c.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, res)
			require.False(t, ds.BatchSetScriptsFuncInvoked, "a dry run must not save scripts")
		})
	}

	t.Run("real run still fails", func(t *testing.T) {
		_, err := svc.BatchSetScripts(ctx, nil, newAutoMDM, nil, false)
		require.True(t, automdm.IsNotFound(err))
		require.False(t, ds.BatchSetScriptsFuncInvoked)
	})

	t.Run("team-scoped user can't validate for a new team", func(t *testing.T) {
		teamCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
			ID:    2,
			Teams: []automdm.UserTeam{{ID: 1, Role: automdm.RoleAdmin}},
		}})
		_, err := svc.BatchSetScripts(teamCtx, nil, newAutoMDM, nil, true)
		checkAuthErr(t, true, err)
	})
}

func TestUpdateScriptValidatesFileType(t *testing.T) {
	ds := new(mock.Store)
	lic := &automdm.LicenseInfo{Tier: automdm.TierPremium, Expiration: time.Now().Add(24 * time.Hour)}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: lic, SkipCreateTestUsers: true})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
		return &automdm.Script{ID: id, Name: map[uint]string{1: "fix.py", 2: "fix.ps1", 3: "fix.sh"}[id]}, nil
	}
	ds.UpdateScriptContentsFunc = func(ctx context.Context, scriptID uint, contents string) (*automdm.Script, error) {
		return &automdm.Script{ID: scriptID, ScriptContents: contents}, nil
	}
	ds.ValidateEmbeddedSecretsFunc = func(ctx context.Context, documents []string) error {
		return nil
	}

	const (
		pyErr  = `Python scripts must start with a python shebang (for example, "#!/usr/bin/env python3").`
		ps1Err = `PowerShell scripts must not start with a shebang ("#!").`
		shErr  = `Shell scripts must use a shell shebang (for example, "#!/bin/sh") or no shebang. For Python, use a ".py" script.`
	)
	for _, tc := range []struct {
		name     string
		id       uint
		contents string
		wantErr  string // empty means the contents are saved
	}{
		{"py without shebang", 1, `print("no shebang")`, pyErr},
		{"ps1 with shell shebang", 2, "#!/bin/bash\necho hi", ps1Err},
		{"ps1 with pwsh shebang", 2, "#!/usr/bin/pwsh\nWrite-Host hi", ps1Err},
		{"sh with python shebang", 3, "#!/usr/bin/env python3\nprint(1)", shErr},
		{"py with python shebang", 1, "#!/usr/bin/env python3\nprint(\"ok\")", ""},
		{"ps1 without shebang", 2, "Write-Host hi", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds.UpdateScriptContentsFuncInvoked = false
			saved, err := svc.UpdateScript(ctx, tc.id, strings.NewReader(tc.contents))
			if tc.wantErr == "" {
				require.NoError(t, err)
				require.True(t, ds.UpdateScriptContentsFuncInvoked)
				require.Equal(t, tc.contents, saved.ScriptContents)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			var invalidArgErr *automdm.InvalidArgumentError
			require.ErrorAs(t, err, &invalidArgErr)
			require.False(t, ds.UpdateScriptContentsFuncInvoked)
		})
	}
}

func TestBatchScriptCancelReplicaLag(t *testing.T) {
	opts := &testing_utils.DatastoreTestOptions{DummyReplica: true}
	ds := mysqltest.CreateMySQLDSWithOptions(t, opts)
	defer ds.Close()

	serverOpts := &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}, SkipCreateTestUsers: true}
	svc, ctx := newTestService(t, ds, nil, nil, serverOpts)

	var canceledActivities []automdm.ActivityTypeBatchScriptCanceled
	serverOpts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
		if a, ok := activity.(automdm.ActivityTypeBatchScriptCanceled); ok {
			canceledActivities = append(canceledActivities, a)
		}
		return nil
	}

	user := test.NewUser(t, ds, "admin", "admin@example.com", true)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	host1 := test.NewHost(t, ds, "host1", "10.0.0.1", "host1key", "host1uuid", time.Now())
	host2 := test.NewHost(t, ds, "host2", "10.0.0.2", "host2key", "host2uuid", time.Now())
	host3 := test.NewHost(t, ds, "host3", "10.0.0.3", "host3key", "host3uuid", time.Now())
	test.SetOrbitEnrollment(t, host1, ds)
	test.SetOrbitEnrollment(t, host2, ds)
	test.SetOrbitEnrollment(t, host3, ds)

	script, err := ds.NewScript(ctx, &automdm.Script{Name: "script1.sh", ScriptContents: "echo hi"})
	require.NoError(t, err)

	// start a batch run on all 3 hosts and make it available to the replica
	batchID, err := ds.NewBatchScriptExecution(ctx, &user.ID, script.ID, []uint{host1.ID, host2.ID, host3.ID})
	require.NoError(t, err)
	opts.RunReplication()
	err = ds.RunScheduledBatchActivity(ctx, batchID)
	require.NoError(t, err)
	opts.RunReplication()

	// cancel the batch without replicating, the activity should have the canceled count from the primary
	err = svc.BatchScriptCancel(ctx, batchID)
	require.NoError(t, err)
	require.Len(t, canceledActivities, 1)
	require.Equal(t, batchID, canceledActivities[0].BatchExecutionID)
	require.Equal(t, uint(3), canceledActivities[0].HostCount)
	require.Equal(t, uint(3), canceledActivities[0].CanceledCount)

	// read the batch from the replica, it should not have the canceled count yet
	replicaBatch, err := ds.GetBatchActivity(ctx, batchID)
	require.NoError(t, err)
	require.Equal(t, automdm.ScheduledBatchExecutionStarted, replicaBatch.Status)
	require.Nil(t, replicaBatch.NumCanceled)
}

package service

import (
	"context"
	"testing"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
)

func TestCheckPolicySpecAuthorization(t *testing.T) {
	t.Run("when team not found", func(t *testing.T) {
		ds := new(mock.Store)
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return nil, &notFoundError{}
		}

		svc, ctx := newTestService(t, ds, nil, nil)

		req := []*automdm.PolicySpec{
			{
				Team: "some_team",
			},
		}

		user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

		actual := svc.ApplyPolicySpecs(ctx, req)
		var expected automdm.NotFoundError

		require.ErrorAs(t, actual, &expected)
	})
}

func TestGlobalPoliciesAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.NewGlobalPolicyFunc = func(ctx context.Context, authorID *uint, args automdm.PolicyPayload) (*automdm.Policy, error) {
		return &automdm.Policy{}, nil
	}
	ds.ListGlobalPoliciesFunc = func(ctx context.Context, opts automdm.ListOptions, platform string) ([]*automdm.Policy, error) {
		return nil, nil
	}
	ds.PoliciesByIDFunc = func(ctx context.Context, ids []uint) (map[uint]*automdm.Policy, error) {
		return nil, nil
	}
	ds.PolicyFunc = func(ctx context.Context, id uint) (*automdm.Policy, error) {
		return &automdm.Policy{
			PolicyData: automdm.PolicyData{
				ID: id,
			},
		}, nil
	}
	ds.DeleteGlobalPoliciesFunc = func(ctx context.Context, ids []uint) ([]uint, error) {
		return nil, nil
	}
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return &automdm.Team{ID: 1}, nil
	}
	ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
		return nil
	}
	ds.SavePolicyFunc = func(ctx context.Context, p *automdm.Policy, shouldDeleteAll bool, removePolicyStats bool) error {
		return nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			WebhookSettings: automdm.WebhookSettings{
				FailingPoliciesWebhook: automdm.FailingPoliciesWebhookSettings{
					Enable: false,
				},
			},
		}, nil
	}

	testCases := []struct {
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
			false,
			false,
		},
		{
			"global observer",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			true,
			false,
		},
		{
			"team admin",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
			false,
		},
		{
			"team maintainer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
			true,
			false,
		},
		{
			"team observer",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
			false,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			_, err := svc.NewGlobalPolicy(ctx, automdm.PolicyPayload{
				Name:  "query1",
				Query: "select 1;",
			})
			checkAuthErr(t, tt.shouldFailWrite, err)

			_, err = svc.ListGlobalPolicies(ctx, automdm.ListOptions{}, "")
			checkAuthErr(t, tt.shouldFailRead, err)

			_, err = svc.GetPolicyByID(ctx, 1)
			checkAuthErr(t, tt.shouldFailRead, err)

			_, err = svc.ModifyGlobalPolicy(ctx, 1, automdm.ModifyPolicyPayload{})
			checkAuthErr(t, tt.shouldFailWrite, err)

			_, err = svc.DeleteGlobalPolicies(ctx, []uint{1})
			checkAuthErr(t, tt.shouldFailWrite, err)

			err = svc.ApplyPolicySpecs(ctx, []*automdm.PolicySpec{
				{
					Name:  "query2",
					Query: "select 1;",
				},
			})
			checkAuthErr(t, tt.shouldFailWrite, err)
		})
	}
}

// TestGetPolicyByIDCrossTeamAuth verifies that the global "get policy
// by ID" endpoint refuses to return a team policy to a user who has no role
// on that team. This guards against the regression described in the
// "Cross-Team Policy Data Exposure" disclosure.
func TestGetPolicyByIDCrossTeamAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	// The fetched policy belongs to team 2.
	const policyTeamID = uint(2)
	ds.PolicyFunc = func(ctx context.Context, id uint) (*automdm.Policy, error) {
		teamID := policyTeamID
		return &automdm.Policy{
			PolicyData: automdm.PolicyData{
				ID:     id,
				TeamID: &teamID,
			},
		}, nil
	}

	testCases := []struct {
		name           string
		user           *automdm.User
		shouldFailRead bool
	}{
		{
			"global admin can read any team policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
		},
		{
			"global observer can read any team policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			false,
		},
		{
			"team observer of the policy's team can read it",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: policyTeamID}, Role: automdm.RoleObserver}}},
			false,
		},
		{
			"team gitops of the policy's team can read it",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: policyTeamID}, Role: automdm.RoleGitOps}}},
			false,
		},
		{
			"team observer of a different team cannot read it",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
		},
		{
			"team admin of a different team cannot read it",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
		},
		{
			"team gitops of a different team cannot read it",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
			_, err := svc.GetPolicyByID(ctx, 1)
			checkAuthErr(t, tt.shouldFailRead, err)
		})
	}
}

// TestGetPolicyByIDGlobalPolicyAuth verifies authorization for reading a
// global policy (TeamID == nil) via the "get policy by ID" endpoint.
func TestGetPolicyByIDGlobalPolicyAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	// The fetched policy is global (TeamID is nil).
	ds.PolicyFunc = func(ctx context.Context, id uint) (*automdm.Policy, error) {
		return &automdm.Policy{
			PolicyData: automdm.PolicyData{
				ID:     id,
				TeamID: nil,
			},
		}, nil
	}

	testCases := []struct {
		name           string
		user           *automdm.User
		shouldFailRead bool
	}{
		{
			"global admin can read a global policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
		},
		{
			"global maintainer can read a global policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			false,
		},
		{
			"global observer can read a global policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			false,
		},
		{
			"global gitops can read a global policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			false,
		},
		{
			"team admin can read a global policy",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			false,
		},
		{
			"team observer can read a global policy",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			false,
		},
		{
			"team gitops cannot read a global policy",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
			true,
		},
		{
			"user with no role cannot read a global policy",
			&automdm.User{ID: 999},
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
			_, err := svc.GetPolicyByID(ctx, 1)
			checkAuthErr(t, tt.shouldFailRead, err)
		})
	}
}

// TestGetPolicyByIDNoTeamPolicyAuth verifies authorization for reading a
// "No team" policy (TeamID == 0) via the "get policy by ID" endpoint. Team-only
// users without a role on team 0 must not be able to read it.
func TestGetPolicyByIDNoTeamPolicyAuth(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	// The fetched policy belongs to "No team" (TeamID == 0).
	ds.PolicyFunc = func(ctx context.Context, id uint) (*automdm.Policy, error) {
		return &automdm.Policy{
			PolicyData: automdm.PolicyData{
				ID:     id,
				TeamID: ptr.Uint(0),
			},
		}, nil
	}

	testCases := []struct {
		name           string
		user           *automdm.User
		shouldFailRead bool
	}{
		{
			"global admin can read a no-team policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			false,
		},
		{
			"global maintainer can read a no-team policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			false,
		},
		{
			"global observer can read a no-team policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			false,
		},
		{
			"global gitops can read a no-team policy",
			&automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
			false,
		},
		{
			"team admin of a regular team cannot read a no-team policy",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			true,
		},
		{
			"team observer of a regular team cannot read a no-team policy",
			&automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			true,
		},
		{
			"user with no role cannot read a no-team policy",
			&automdm.User{ID: 999},
			true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})
			_, err := svc.GetPolicyByID(ctx, 1)
			checkAuthErr(t, tt.shouldFailRead, err)
		})
	}
}

func TestApplyPolicySpecsReturnsErrorOnDuplicatePolicyNamesInSpecs(t *testing.T) {
	ds := new(mock.Store)
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, &notFoundError{}
	}

	svc, ctx := newTestService(t, ds, nil, nil)

	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	// Unassigned in a different letter case is the same team.
	for _, teams := range [][2]string{{"", ""}, {"Unassigned", "unassigned"}} {
		req := []*automdm.PolicySpec{
			{Name: "query1", Query: "select 1;", Platform: "windows", Team: teams[0]},
			{Name: "query1", Query: "select 2;", Platform: "windows", Team: teams[1]},
		}

		err := svc.ApplyPolicySpecs(ctx, req)

		badRequestError := &automdm.BadRequestError{}
		require.ErrorAs(t, err, &badRequestError)
		require.Equal(t, "duplicate policy names not allowed", badRequestError.Message)
	}
}

func TestApplyPolicySpecsLabelsValidation(t *testing.T) {
	ds := new(mock.Store)
	ds.NewGlobalPolicyFunc = func(ctx context.Context, authorID *uint, args automdm.PolicyPayload) (*automdm.Policy, error) {
		return &automdm.Policy{}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
		return nil
	}
	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		labels := make(map[string]*automdm.Label, len(names))
		for _, name := range names {
			if name == "foo" {
				labels["foo"] = &automdm.Label{
					Name: "foo",
					ID:   1,
				}
			}
		}
		return labels, nil
	}

	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

	testAdmin := automdm.User{
		ID:         1,
		Teams:      []automdm.UserTeam{},
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})

	// Test that a query spec with a label that exists doesn't return an error
	err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{
		{
			Name:             "test query",
			Query:            "select 1",
			LabelsIncludeAny: []string{"foo"},
			Platform:         "darwin,windows",
		},
	})
	// Check that no error is returned
	require.NoError(t, err)

	// Test that a query spec with a label that doesn't exist returns an error.
	err = svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{
		{
			Name:             "test query",
			Query:            "select 1",
			LabelsIncludeAny: []string{"nope"},
			Platform:         "darwin,windows",
		},
	})

	require.Error(t, err)
}

func TestApplyPolicySpecsLabelScopeRequiresPremium(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
		return nil
	}
	ds.LabelsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string]*automdm.Label, error) {
		return map[string]*automdm.Label{"foo": {Name: "foo", ID: 1}}, nil
	}

	// Free license.
	svc, ctx := newTestService(t, ds, nil, nil)

	testAdmin := automdm.User{
		ID:         1,
		Teams:      []automdm.UserTeam{},
		GlobalRole: new(automdm.RoleAdmin),
	}
	viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})

	for name, spec := range map[string]*automdm.PolicySpec{
		"labels_include_any": {Name: "p", Query: "SELECT 1", LabelsIncludeAny: []string{"foo"}},
		"labels_include_all": {Name: "p", Query: "SELECT 1", LabelsIncludeAll: []string{"foo"}},
		"labels_exclude_any": {Name: "p", Query: "SELECT 1", LabelsExcludeAny: []string{"foo"}},
		"labels_exclude_all": {Name: "p", Query: "SELECT 1", LabelsExcludeAll: []string{"foo"}},
	} {
		t.Run(name, func(t *testing.T) {
			err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{spec})
			require.ErrorIs(t, err, automdm.ErrMissingLicense)
		})
	}
	require.False(t, ds.ApplyPolicySpecsFuncInvoked)
}

func TestApplyPolicySpecsPatchWhenClosedRequiresPremium(t *testing.T) {
	newDS := func() *mock.Store {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return &automdm.Team{ID: 1, Name: name}, nil
		}
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			return nil
		}
		return ds
	}

	// patch_when_closed requires a patch-type team policy.
	patchSpec := func() *automdm.PolicySpec {
		return &automdm.PolicySpec{
			Name:            "patch policy",
			Team:            "team1",
			Type:            automdm.PolicyTypePatch,
			PatchWhenClosed: true,
		}
	}

	testAdmin := automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}

	// A free-tier caller can't apply patch_when_closed, and we never reach the datastore.
	t.Run("free tier rejected", func(t *testing.T) {
		ds := newDS()
		svc, ctx := newTestService(t, ds, nil, nil)
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})
		err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{patchSpec()})
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.False(t, ds.ApplyPolicySpecsFuncInvoked)
	})

	// A premium caller applies it successfully.
	t.Run("premium accepted", func(t *testing.T) {
		ds := newDS()
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})
		err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{patchSpec()})
		require.NoError(t, err)
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)
	})
}

func TestApplyPolicySpecsDefaultType(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	var capturedSpecs []*automdm.PolicySpec
	ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
		capturedSpecs = specs
		return nil
	}

	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)
	opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
		return nil
	}

	testAdmin := automdm.User{
		ID:         1,
		Teams:      []automdm.UserTeam{},
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})

	// Test that an omitted type defaults to "dynamic".
	err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{
		{
			Name:  "no-type-policy",
			Query: "SELECT 1;",
		},
	})
	require.NoError(t, err)
	require.Len(t, capturedSpecs, 1)
	require.Equal(t, automdm.PolicyTypeDynamic, capturedSpecs[0].Type)

	// Test that an explicit type is not overridden.
	err = svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{
		{
			Name:  "explicit-dynamic-policy",
			Query: "SELECT 1;",
			Type:  automdm.PolicyTypeDynamic,
		},
	})
	require.NoError(t, err)
	require.Len(t, capturedSpecs, 1)
	require.Equal(t, automdm.PolicyTypeDynamic, capturedSpecs[0].Type)
}

func TestResetPolicyAuth(t *testing.T) {
	const policyID = uint(42)
	teamID := uint(1)

	testCases := []struct {
		name            string
		user            *automdm.User
		policyTeamID    *uint
		shouldFailWrite bool
	}{
		{
			name:            "global admin can reset global policy",
			user:            &automdm.User{GlobalRole: new(automdm.RoleAdmin)},
			policyTeamID:    nil,
			shouldFailWrite: false,
		},
		{
			name:            "global maintainer can reset global policy",
			user:            &automdm.User{GlobalRole: new(automdm.RoleMaintainer)},
			policyTeamID:    nil,
			shouldFailWrite: false,
		},
		{
			name:            "global observer cannot reset policy",
			user:            &automdm.User{GlobalRole: new(automdm.RoleObserver)},
			policyTeamID:    nil,
			shouldFailWrite: true,
		},
		{
			name:            "global gitops can reset global policy",
			user:            &automdm.User{GlobalRole: new(automdm.RoleGitOps)},
			policyTeamID:    nil,
			shouldFailWrite: false,
		},
		{
			name:            "team admin can reset own team policy",
			user:            &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID}, Role: automdm.RoleAdmin}}},
			policyTeamID:    &teamID,
			shouldFailWrite: false,
		},
		{
			name:            "team maintainer can reset own team policy",
			user:            &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID}, Role: automdm.RoleMaintainer}}},
			policyTeamID:    &teamID,
			shouldFailWrite: false,
		},
		{
			name:            "team observer cannot reset policy",
			user:            &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID}, Role: automdm.RoleObserver}}},
			policyTeamID:    &teamID,
			shouldFailWrite: true,
		},
		{
			name:            "team admin of different team cannot reset policy",
			user:            &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 2}, Role: automdm.RoleAdmin}}},
			policyTeamID:    &teamID,
			shouldFailWrite: true,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.PolicyFunc = func(_ context.Context, id uint) (*automdm.Policy, error) {
				return &automdm.Policy{PolicyData: automdm.PolicyData{ID: id, TeamID: tt.policyTeamID}}, nil
			}
			ds.ResetPolicyFunc = func(_ context.Context, _ uint) error { return nil }

			opts := &TestServerOpts{}
			svc, baseCtx := newTestService(t, ds, nil, nil, opts)
			opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
				return nil
			}
			ctx := viewer.NewContext(baseCtx, viewer.Viewer{User: tt.user})

			err := svc.ResetPolicy(ctx, policyID, nil)
			checkAuthErr(t, tt.shouldFailWrite, err)
			if !tt.shouldFailWrite {
				require.True(t, ds.ResetPolicyFuncInvoked)
			}
		})
	}
}

func TestResetPolicyNotFound(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.PolicyFunc = func(_ context.Context, _ uint) (*automdm.Policy, error) {
		return nil, &notFoundError{}
	}

	user := &automdm.User{GlobalRole: new(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	err := svc.ResetPolicy(ctx, 999, nil)
	require.Error(t, err)
	require.True(t, automdm.IsNotFound(err))
}

func TestResetPolicyEmitsActivity(t *testing.T) {
	const policyID = uint(7)
	const policyName = "My Policy"

	newSvc := func(teamID *uint) (*mock.Store, automdm.Service, context.Context, *TestServerOpts) {
		ds := new(mock.Store)
		ds.PolicyFunc = func(_ context.Context, id uint) (*automdm.Policy, error) {
			return &automdm.Policy{PolicyData: automdm.PolicyData{ID: id, Name: policyName, TeamID: teamID}}, nil
		}
		ds.ResetPolicyFunc = func(_ context.Context, _ uint) error { return nil }
		opts := &TestServerOpts{}
		svc, baseCtx := newTestService(t, ds, nil, nil, opts)
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
			return nil
		}
		ctx := viewer.NewContext(baseCtx, viewer.Viewer{
			User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)},
		})
		return ds, svc, ctx, opts
	}

	t.Run("global policy emits team_id -1", func(t *testing.T) {
		ds, svc, ctx, opts := newSvc(nil)
		var capturedActivity activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, a activity_api.ActivityDetails) error {
			capturedActivity = a
			return nil
		}

		require.NoError(t, svc.ResetPolicy(ctx, policyID, nil))
		require.True(t, ds.ResetPolicyFuncInvoked)
		require.True(t, opts.ActivityMock.NewActivityFuncInvoked)

		act, ok := capturedActivity.(automdm.ActivityTypeResetPolicy)
		require.True(t, ok)
		require.Equal(t, policyID, act.ID)
		require.Equal(t, policyName, act.Name)
		require.NotNil(t, act.TeamID)
		require.Equal(t, int64(-1), *act.TeamID)
		require.Nil(t, act.TeamName)
	})

	t.Run("no-team policy emits team_id 0", func(t *testing.T) {
		noTeamID := uint(0)
		ds, svc, ctx, opts := newSvc(&noTeamID)
		var capturedActivity activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, a activity_api.ActivityDetails) error {
			capturedActivity = a
			return nil
		}

		require.NoError(t, svc.ResetPolicy(ctx, policyID, nil))
		require.True(t, ds.ResetPolicyFuncInvoked)
		require.True(t, opts.ActivityMock.NewActivityFuncInvoked)

		act, ok := capturedActivity.(automdm.ActivityTypeResetPolicy)
		require.True(t, ok)
		require.Equal(t, policyID, act.ID)
		require.Equal(t, policyName, act.Name)
		require.NotNil(t, act.TeamID)
		require.Equal(t, int64(0), *act.TeamID)
		require.Nil(t, act.TeamName)
	})

	t.Run("host-scoped reset only touches that host and records it", func(t *testing.T) {
		const hostID = uint(42)
		ds, svc, ctx, opts := newSvc(nil)
		ds.HostLiteFunc = func(_ context.Context, id uint) (*automdm.Host, error) {
			require.Equal(t, hostID, id)
			return &automdm.Host{ID: id, Hostname: "host-42.local"}, nil
		}
		ds.ResetPolicyForHostFunc = func(_ context.Context, gotHostID, gotPolicyID uint) error {
			require.Equal(t, hostID, gotHostID)
			require.Equal(t, policyID, gotPolicyID)
			return nil
		}
		var capturedActivity activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, a activity_api.ActivityDetails) error {
			capturedActivity = a
			return nil
		}

		require.NoError(t, svc.ResetPolicy(ctx, policyID, new(hostID)))
		require.False(t, ds.ResetPolicyFuncInvoked, "all-host reset must not run for a host-scoped request")
		require.True(t, ds.ResetPolicyForHostFuncInvoked)

		act, ok := capturedActivity.(automdm.ActivityTypeResetPolicy)
		require.True(t, ok)
		require.NotNil(t, act.HostID)
		require.Equal(t, hostID, *act.HostID)
		require.NotNil(t, act.HostDisplayName)
		require.Equal(t, "host-42.local", *act.HostDisplayName)
		require.Equal(t, []uint{hostID}, act.HostIDs())
	})

	t.Run("host-scoped reset with unknown host is not found", func(t *testing.T) {
		ds, svc, ctx, opts := newSvc(nil)
		ds.HostLiteFunc = func(_ context.Context, _ uint) (*automdm.Host, error) {
			return nil, &notFoundError{}
		}

		err := svc.ResetPolicy(ctx, policyID, new(uint(999)))
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.False(t, ds.ResetPolicyFuncInvoked)
		require.False(t, ds.ResetPolicyForHostFuncInvoked)
		require.False(t, opts.ActivityMock.NewActivityFuncInvoked)
	})

	t.Run("host-scoped reset of a team policy rejects a host on another team", func(t *testing.T) {
		policyTeamID := uint(1)
		ds, svc, ctx, opts := newSvc(&policyTeamID)
		ds.HostLiteFunc = func(_ context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: id, TeamID: new(uint(2))}, nil
		}

		err := svc.ResetPolicy(ctx, policyID, new(uint(42)))
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.False(t, ds.ResetPolicyForHostFuncInvoked)
		require.False(t, opts.ActivityMock.NewActivityFuncInvoked)
	})

	t.Run("host-scoped reset of a no-team policy accepts a host with no team", func(t *testing.T) {
		noTeamID := uint(0)
		ds, svc, ctx, _ := newSvc(&noTeamID)
		ds.HostLiteFunc = func(_ context.Context, id uint) (*automdm.Host, error) {
			return &automdm.Host{ID: id}, nil
		}
		ds.ResetPolicyForHostFunc = func(_ context.Context, _, _ uint) error { return nil }

		require.NoError(t, svc.ResetPolicy(ctx, policyID, new(uint(42))))
		require.True(t, ds.ResetPolicyForHostFuncInvoked)
	})
}

func TestApplyPolicySpecsScriptID(t *testing.T) {
	setupDS := func() *mock.Store {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return &automdm.Team{ID: 1, Name: name}, nil
		}
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			return nil
		}
		return ds
	}
	adminCtx := func(ctx context.Context) context.Context {
		return viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
			ID:         1,
			GlobalRole: new(automdm.RoleAdmin),
		}})
	}
	spec := func(team string, scriptID *uint) *automdm.PolicySpec {
		return &automdm.PolicySpec{
			Name:     "script spec policy",
			Query:    "SELECT 1;",
			Team:     team,
			Platform: "darwin",
			ScriptID: scriptID,
		}
	}

	// "All teams" (empty team) cannot carry a script.
	t.Run("global spec rejects script_id", func(t *testing.T) {
		ds := setupDS()
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{
			License: &automdm.LicenseInfo{Tier: automdm.TierPremium},
		})

		err := svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{spec("", new(uint(1)))})
		require.ErrorContains(t, err, errPolicyAllTeamsForScripts)
		require.False(t, ds.ApplyPolicySpecsFuncInvoked)
	})

	// script_id: 0 means "no script", so it is fine on a global spec; team
	// ownership of a real script is the datastore's job.
	t.Run("zero and team script_id pass through", func(t *testing.T) {
		ds := setupDS()
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{
			License: &automdm.LicenseInfo{Tier: automdm.TierPremium},
		})

		err := svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{spec("", new(uint(0)))})
		require.NoError(t, err)
		err = svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{spec("team1", new(uint(1)))})
		require.NoError(t, err)
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)
	})

	t.Run("global spec rejects software_title_id", func(t *testing.T) {
		ds := setupDS()
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{
			License: &automdm.LicenseInfo{Tier: automdm.TierPremium},
		})

		s := spec("", nil)
		s.SoftwareTitleID = new(uint(1))
		err := svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{s})
		var bre *automdm.BadRequestError
		require.ErrorAs(t, err, &bre)
		require.ErrorContains(t, err, errPolicyAllTeamsForSoftware)
		require.False(t, ds.ApplyPolicySpecsFuncInvoked)

		s.SoftwareTitleID = new(uint(0))
		require.NoError(t, svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{s}))
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)

		// Unassigned is not "All teams", so it keeps its automations. A policy has one, so no script here.
		noTeam := spec(automdm.DisplayNameNoTeam, nil)
		noTeam.SoftwareTitleID = new(uint(1))
		ds.ApplyPolicySpecsFuncInvoked = false
		require.NoError(t, svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{noTeam}))
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)
	})
}

func TestApplyPolicySpecsResendConfigProfile(t *testing.T) {
	const (
		teamName  = "team1"
		teamID    = uint(1)
		appleUUID = automdm.MDMAppleProfileUUIDPrefix + "1111"
	)

	setupDS := func() *mock.Store {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return &automdm.Team{ID: teamID, Name: name}, nil
		}
		return ds
	}

	adminCtx := func(ctx context.Context) context.Context {
		return viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
			ID:         1,
			GlobalRole: new(automdm.RoleAdmin),
		}})
	}

	spec := func(team string, profileUUID *string) *automdm.PolicySpec {
		return &automdm.PolicySpec{
			Name:        "resend spec policy",
			Query:       "SELECT 1;",
			Team:        team,
			Platform:    "darwin",
			ProfileUUID: profileUUID,
		}
	}

	// A team spec carrying a profile UUID reaches the datastore untouched — the
	// service layer does not split the columns, the datastore dispatches on prefix.
	t.Run("team spec passes profile_uuid through", func(t *testing.T) {
		ds := setupDS()
		var captured []*automdm.PolicySpec
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			captured = specs
			return nil
		}
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{
			License: &automdm.LicenseInfo{Tier: automdm.TierPremium},
		})

		err := svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{spec(teamName, new(appleUUID))})
		require.NoError(t, err)
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)
		require.Len(t, captured, 1)
		require.NotNil(t, captured[0].ProfileUUID)
		require.Equal(t, appleUUID, *captured[0].ProfileUUID)
	})

	// "All teams" (empty team) cannot carry a resend profile.
	t.Run("global spec rejects profile_uuid", func(t *testing.T) {
		ds := setupDS()
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			return nil
		}
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{
			License: &automdm.LicenseInfo{Tier: automdm.TierPremium},
		})

		err := svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{spec("", new(appleUUID))})
		require.Error(t, err)
		require.ErrorContains(t, err, errPolicyAllTeamsForProfiles)
		require.False(t, ds.ApplyPolicySpecsFuncInvoked)
	})

	// PolicySpec has no premium struct tag and the decoder does not reach into the
	// spec slice, so ApplyPolicySpecs needs its own explicit license check.
	t.Run("profile_uuid requires premium", func(t *testing.T) {
		ds := setupDS()
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			return nil
		}
		// Free license.
		svc, ctx := newTestService(t, ds, nil, nil)

		err := svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{spec(teamName, new(appleUUID))})
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
		require.False(t, ds.ApplyPolicySpecsFuncInvoked)
	})

	// Without a profile UUID, a free-tier spec is unaffected.
	t.Run("no profile_uuid does not require premium", func(t *testing.T) {
		ds := setupDS()
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			return nil
		}
		svc, ctx := newTestService(t, ds, nil, nil)

		err := svc.ApplyPolicySpecs(adminCtx(ctx), []*automdm.PolicySpec{spec(teamName, nil)})
		require.NoError(t, err)
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)
	})
}

func TestApplyPolicySpecsHidden(t *testing.T) {
	newDS := func() *mock.Store {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return &automdm.Team{ID: 1, Name: name}, nil
		}
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			return nil
		}
		return ds
	}
	hiddenSpec := func() *automdm.PolicySpec {
		return &automdm.PolicySpec{Name: "hidden policy", Team: "team1", Query: "SELECT 1", Hidden: true}
	}
	testAdmin := automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}
	premium := &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}}

	t.Run("free tier ignored", func(t *testing.T) {
		ds := newDS()
		var applied []*automdm.PolicySpec
		ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error {
			applied = specs
			return nil
		}
		svc, ctx := newTestService(t, ds, nil, nil)
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})
		err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{hiddenSpec()})
		require.NoError(t, err)
		require.Len(t, applied, 1)
		require.False(t, applied[0].Hidden)
	})

	t.Run("premium accepted", func(t *testing.T) {
		ds := newDS()
		svc, ctx := newTestService(t, ds, nil, nil, premium)
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})
		err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{hiddenSpec()})
		require.NoError(t, err)
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)
	})

	t.Run("global policy accepted", func(t *testing.T) {
		ds := newDS()
		svc, ctx := newTestService(t, ds, nil, nil, premium)
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})
		spec := hiddenSpec()
		spec.Team = ""
		err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{spec})
		require.NoError(t, err)
		require.True(t, ds.ApplyPolicySpecsFuncInvoked)
	})

	t.Run("conditional access conflict rejected", func(t *testing.T) {
		ds := newDS()
		svc, ctx := newTestService(t, ds, nil, nil, premium)
		viewerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &testAdmin})
		spec := hiddenSpec()
		spec.Platform = "darwin"
		spec.ConditionalAccessEnabled = true
		err := svc.ApplyPolicySpecs(viewerCtx, []*automdm.PolicySpec{spec})
		var bre *automdm.BadRequestError
		require.ErrorAs(t, err, &bre)
		require.Contains(t, err.Error(), `"hidden" and "conditional_access_enabled" cannot both be set`)
		require.False(t, ds.ApplyPolicySpecsFuncInvoked)
	})
}

func TestApplyPolicySpecsWebhookOrTicketPermission(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) { return &automdm.AppConfig{}, nil }
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return &automdm.Team{ID: 1, Name: name}, nil
	}
	ds.ApplyPolicySpecsFunc = func(ctx context.Context, authorID uint, specs []*automdm.PolicySpec) error { return nil }
	// The stored name is NFC-normalized; the spec below spells it decomposed.
	ds.WebhooksAndTicketsByPolicyNameFunc = func(ctx context.Context, teamID *uint, names []string) (map[string]bool, error) {
		require.Equal(t, []string{"café"}, names)
		return map[string]bool{"café": true}, nil
	}
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
	maintainerCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
		ID: 2, Teams: []automdm.UserTeam{{ID: 1, Role: automdm.RoleMaintainer}},
	}})
	spec := func(webhook bool) []*automdm.PolicySpec {
		return []*automdm.PolicySpec{{Name: "café", Query: "SELECT 1;", Team: "team1", WebhooksAndTicketsEnabled: webhook}}
	}

	// Re-applying the unchanged value isn't a change, so a team maintainer can.
	require.NoError(t, svc.ApplyPolicySpecs(maintainerCtx, spec(true)))
	require.True(t, ds.ApplyPolicySpecsFuncInvoked)

	ds.ApplyPolicySpecsFuncInvoked = false
	err := svc.ApplyPolicySpecs(maintainerCtx, spec(false))
	require.Error(t, err)
	require.Contains(t, err.Error(), "forbidden")
	require.False(t, ds.ApplyPolicySpecsFuncInvoked)
	// Patch policies are matched by app, so renaming one in a spec is still a change to its automation.
	ds.WebhooksAndTicketsByPolicyNameFunc = func(ctx context.Context, teamID *uint, names []string) (map[string]bool, error) {
		return nil, nil
	}
	ds.WebhooksAndTicketsByPatchPolicySlugFunc = func(ctx context.Context, teamID uint, slugs []string) (map[string]bool, error) {
		require.Equal(t, uint(1), teamID)
		require.Equal(t, []string{"zoom/darwin"}, slugs)
		return map[string]bool{"zoom/darwin": true}, nil
	}
	patchSpec := func(webhook bool) []*automdm.PolicySpec {
		return []*automdm.PolicySpec{{
			Name: "renamed patch policy", Team: "team1", Type: automdm.PolicyTypePatch,
			AutoMDMMaintainedAppSlug: "zoom/darwin", WebhooksAndTicketsEnabled: webhook,
		}}
	}
	ds.ApplyPolicySpecsFuncInvoked = false
	err = svc.ApplyPolicySpecs(maintainerCtx, patchSpec(false))
	require.Error(t, err)
	require.Contains(t, err.Error(), "forbidden")
	require.False(t, ds.ApplyPolicySpecsFuncInvoked)
	require.NoError(t, svc.ApplyPolicySpecs(maintainerCtx, patchSpec(true)))
	require.True(t, ds.ApplyPolicySpecsFuncInvoked)
}

package service

import (
	"context"
	"errors"
	"testing"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
)

func TestCreateSecretVariables(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	ds.UpsertSecretVariablesFunc = func(ctx context.Context, secrets []automdm.SecretVariable) (created []string, updated []string, err error) {
		return nil, nil, nil
	}

	t.Run("authorization checks", func(t *testing.T) {
		testCases := []struct {
			name       string
			user       *automdm.User
			shouldFail bool
		}{
			{
				name:       "global admin",
				user:       &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
				shouldFail: false,
			},
			{
				name:       "global maintainer",
				user:       &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
				shouldFail: false,
			},
			{
				name:       "global gitops",
				user:       &automdm.User{GlobalRole: ptr.String(automdm.RoleGitOps)},
				shouldFail: false,
			},
			{
				name:       "global observer",
				user:       &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
				shouldFail: true,
			},
			{
				name:       "global observer+",
				user:       &automdm.User{GlobalRole: ptr.String(automdm.RoleObserverPlus)},
				shouldFail: true,
			},
			{
				name:       "team admin",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
				shouldFail: true,
			},
			{
				name:       "team maintainer",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}},
				shouldFail: true,
			},
			{
				name:       "team observer",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
				shouldFail: true,
			},
			{
				name:       "team observer+",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserverPlus}}},
				shouldFail: true,
			},
			{
				name:       "team gitops",
				user:       &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleGitOps}}},
				shouldFail: true,
			},
		}
		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				ctx = viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

				err := svc.CreateSecretVariables(ctx, []automdm.SecretVariable{{Name: "FOO", Value: "bar"}}, false)
				checkAuthErr(t, tt.shouldFail, err)
			})
		}
	})

	t.Run("failure test", func(t *testing.T) {
		cfg := config.TestConfig()
		cfg.Server.PrivateKey = ""
		svcNoKey, ctxNoKey := newTestServiceWithConfig(t, ds, cfg, nil, nil)
		ctxNoKey = viewer.NewContext(ctxNoKey, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleGitOps)}})
		err := svcNoKey.CreateSecretVariables(ctxNoKey, []automdm.SecretVariable{{Name: "foo", Value: "bar"}}, true)
		require.ErrorContains(t, err, "Couldn't save secret variables. Missing required private key")

		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleGitOps)}})
		ds.UpsertSecretVariablesFunc = func(ctx context.Context, secrets []automdm.SecretVariable) (created []string, updated []string, err error) {
			return nil, nil, errors.New("test error")
		}
		err = svc.CreateSecretVariables(ctx, []automdm.SecretVariable{{Name: "FOO", Value: "bar"}}, false)
		require.ErrorContains(t, err, "test error")
	})
}

func TestCreateSecretVariablesEmitsActivities(t *testing.T) {
	t.Parallel()
	ds := new(mock.Store)
	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})

	t.Run("emits a created activity per created variable and an updated activity per updated variable", func(t *testing.T) {
		ds.UpsertSecretVariablesFunc = func(ctx context.Context, secrets []automdm.SecretVariable) (created []string, updated []string, err error) {
			return []string{"CREATED"}, []string{"UPDATED"}, nil
		}
		ds.GetSecretVariablesFunc = func(ctx context.Context, names []string) ([]automdm.SecretVariable, error) {
			return []automdm.SecretVariable{{ID: 7, Name: "CREATED"}}, nil
		}
		var activities []activity_api.ActivityDetails
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			activities = append(activities, activity)
			return nil
		}
		err := svc.CreateSecretVariables(ctx, []automdm.SecretVariable{
			{Name: "AUTOMDM_SECRET_CREATED", Value: "a"},
			{Name: "AUTOMDM_SECRET_UPDATED", Value: "b"},
		}, false)
		require.NoError(t, err)
		require.Len(t, activities, 2)

		createdActivity, ok := activities[0].(automdm.ActivityCreatedCustomVariable)
		require.True(t, ok)
		require.Equal(t, "CREATED", createdActivity.CustomVariableName)
		require.Equal(t, uint(7), createdActivity.CustomVariableID)

		updatedActivity, ok := activities[1].(automdm.ActivityUpdatedCustomVariable)
		require.True(t, ok)
		require.Equal(t, "UPDATED", updatedActivity.CustomVariableName)
	})

	t.Run("reads created variables from the primary", func(t *testing.T) {
		ds.UpsertSecretVariablesFunc = func(ctx context.Context, secrets []automdm.SecretVariable) (created []string, updated []string, err error) {
			return []string{"CREATED"}, nil, nil
		}
		var primaryRequired bool
		ds.GetSecretVariablesFunc = func(ctx context.Context, names []string) ([]automdm.SecretVariable, error) {
			primaryRequired = ctxdb.IsPrimaryRequired(ctx)
			return []automdm.SecretVariable{{ID: 7, Name: "CREATED"}}, nil
		}
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
			return nil
		}
		err := svc.CreateSecretVariables(ctx, []automdm.SecretVariable{{Name: "AUTOMDM_SECRET_CREATED", Value: "a"}}, false)
		require.NoError(t, err)
		require.True(t, ds.GetSecretVariablesFuncInvoked)
		require.True(t, primaryRequired)
	})

	t.Run("emits no activity when nothing changed", func(t *testing.T) {
		ds.UpsertSecretVariablesFunc = func(ctx context.Context, secrets []automdm.SecretVariable) (created []string, updated []string, err error) {
			return nil, nil, nil
		}
		activityCalled := false
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
			activityCalled = true
			return nil
		}
		err := svc.CreateSecretVariables(ctx, []automdm.SecretVariable{{Name: "AUTOMDM_SECRET_UNCHANGED", Value: "a"}}, false)
		require.NoError(t, err)
		require.False(t, activityCalled)
	})

	t.Run("emits no activity on a dry run", func(t *testing.T) {
		ds.UpsertSecretVariablesFunc = func(ctx context.Context, secrets []automdm.SecretVariable) (created []string, updated []string, err error) {
			t.Fatal("UpsertSecretVariables should not be called on a dry run")
			return nil, nil, nil
		}
		activityCalled := false
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, _ activity_api.ActivityDetails) error {
			activityCalled = true
			return nil
		}
		err := svc.CreateSecretVariables(ctx, []automdm.SecretVariable{{Name: "AUTOMDM_SECRET_DRY", Value: "a"}}, true)
		require.NoError(t, err)
		require.False(t, activityCalled)
	})
}

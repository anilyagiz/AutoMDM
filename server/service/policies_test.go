package service

import (
	"context"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func TestListPolicyAutomationActivities(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	globalPolicy := &automdm.Policy{PolicyData: automdm.PolicyData{ID: 1}}
	teamPolicy := &automdm.Policy{PolicyData: automdm.PolicyData{ID: 2, TeamID: new(uint)}}
	*teamPolicy.TeamID = 42

	ds.PolicyFunc = func(_ context.Context, id uint) (*automdm.Policy, error) {
		switch id {
		case 1:
			return globalPolicy, nil
		case 2:
			return teamPolicy, nil
		default:
			return nil, &notFoundError{}
		}
	}

	returnedActivities := []*automdm.PolicyAutomationActivity{
		{HostID: 10, HostDisplayName: "host-a"},
	}
	returnedMeta := &automdm.PaginationMetadata{HasNextResults: false}

	ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, _ automdm.ListOptions, _ string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
		return returnedActivities, returnedMeta, nil
	}

	t.Run("global admin sees global policy", func(t *testing.T) {
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		activities, meta, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "")
		require.NoError(t, err)
		require.Equal(t, returnedActivities, activities)
		require.Equal(t, returnedMeta, meta)
		require.True(t, ds.ListPolicyAutomationActivitiesFuncInvoked)
		ds.ListPolicyAutomationActivitiesFuncInvoked = false
	})

	t.Run("global observer sees global policy", func(t *testing.T) {
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("observer")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "")
		require.NoError(t, err)
		ds.ListPolicyAutomationActivitiesFuncInvoked = false
	})

	t.Run("team observer sees own team policy", func(t *testing.T) {
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
			Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 42}, Role: automdm.RoleObserver}},
		}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 2, automdm.ListOptions{}, "")
		require.NoError(t, err)
		ds.ListPolicyAutomationActivitiesFuncInvoked = false
	})

	t.Run("team observer sees inherited global policy", func(t *testing.T) {
		// A team-scoped user can read a global (inherited) policy; host scoping to
		// their team happens in the datastore via the team filter. This exercises
		// the policy.rego clause that lets team roles read global policies, distinct
		// from the own-team-policy clause above.
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
			Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 42}, Role: automdm.RoleObserver}},
		}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "")
		require.NoError(t, err)
		require.True(t, ds.ListPolicyAutomationActivitiesFuncInvoked)
		ds.ListPolicyAutomationActivitiesFuncInvoked = false
	})

	t.Run("team observer cannot see other team policy", func(t *testing.T) {
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{
			Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 99}, Role: automdm.RoleObserver}},
		}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 2, automdm.ListOptions{}, "")
		require.Error(t, err)
		var forbidden *authz.Forbidden
		require.ErrorAs(t, err, &forbidden)
		require.False(t, ds.ListPolicyAutomationActivitiesFuncInvoked)
	})

	t.Run("policy not found returns 404", func(t *testing.T) {
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 999, automdm.ListOptions{}, "")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
	})

	t.Run("invalid status returns 422", func(t *testing.T) {
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "invalid")
		require.Error(t, err)
		var invErr *automdm.InvalidArgumentError
		require.ErrorAs(t, err, &invErr)
	})

	t.Run("status=error passes through to datastore", func(t *testing.T) {
		var capturedStatus string
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, _ automdm.ListOptions, status string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			capturedStatus = status
			return nil, &automdm.PaginationMetadata{}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "error")
		require.NoError(t, err)
		require.Equal(t, "error", capturedStatus)
	})

	t.Run("status=success passes through to datastore", func(t *testing.T) {
		var capturedStatus string
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, _ automdm.ListOptions, status string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			capturedStatus = status
			return nil, &automdm.PaginationMetadata{}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "success")
		require.NoError(t, err)
		require.Equal(t, "success", capturedStatus)
	})

	t.Run("per_page exceeds max returns 422", func(t *testing.T) {
		ds.ListPolicyAutomationActivitiesFuncInvoked = false
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{PerPage: maxPolicyAutomationActivitiesPerPage + 1}, "")
		require.Error(t, err)
		var invErr *automdm.InvalidArgumentError
		require.ErrorAs(t, err, &invErr)
		require.False(t, ds.ListPolicyAutomationActivitiesFuncInvoked)
	})

	t.Run("per_page defaults to 50 when unset", func(t *testing.T) {
		var capturedOpts automdm.ListOptions
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, opts automdm.ListOptions, _ string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			capturedOpts = opts
			return nil, &automdm.PaginationMetadata{}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "")
		require.NoError(t, err)
		require.Equal(t, uint(50), capturedOpts.PerPage)
	})

	t.Run("per_page at max is accepted", func(t *testing.T) {
		var capturedOpts automdm.ListOptions
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, opts automdm.ListOptions, _ string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			capturedOpts = opts
			return nil, &automdm.PaginationMetadata{}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{PerPage: maxPolicyAutomationActivitiesPerPage}, "")
		require.NoError(t, err)
		require.Equal(t, uint(maxPolicyAutomationActivitiesPerPage), capturedOpts.PerPage)
	})

	t.Run("match_query passes through to datastore via opts", func(t *testing.T) {
		var capturedOpts automdm.ListOptions
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, opts automdm.ListOptions, _ string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			capturedOpts = opts
			return nil, &automdm.PaginationMetadata{}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{MatchQuery: "my-host"}, "")
		require.NoError(t, err)
		require.Equal(t, "my-host", capturedOpts.MatchQuery)
	})

	t.Run("order defaults to created_at descending when omitted", func(t *testing.T) {
		var capturedOpts automdm.ListOptions
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, opts automdm.ListOptions, _ string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			capturedOpts = opts
			return nil, &automdm.PaginationMetadata{}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 1, automdm.ListOptions{}, "")
		require.NoError(t, err)
		require.Equal(t, "created_at", capturedOpts.OrderKey)
		require.Equal(t, automdm.OrderDescending, capturedOpts.OrderDirection)
	})

	t.Run("team filter carries viewer user to datastore", func(t *testing.T) {
		user := &automdm.User{
			Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 42}, Role: automdm.RoleObserver}},
		}
		var capturedFilter automdm.TeamFilter
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, filter automdm.TeamFilter, _ automdm.ListOptions, _ string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			capturedFilter = filter
			return nil, &automdm.PaginationMetadata{}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: user})
		_, _, err := svc.ListPolicyAutomationActivities(userCtx, 2, automdm.ListOptions{}, "")
		require.NoError(t, err)
		require.Equal(t, user, capturedFilter.User)
		require.True(t, capturedFilter.IncludeObserver)
	})

	t.Run("endpoint surfaces total count from meta", func(t *testing.T) {
		ds.ListPolicyAutomationActivitiesFunc = func(_ context.Context, _ uint, _ automdm.TeamFilter, _ automdm.ListOptions, _ string) ([]*automdm.PolicyAutomationActivity, *automdm.PaginationMetadata, error) {
			return returnedActivities, &automdm.PaginationMetadata{TotalResults: 123, HasNextResults: true}, nil
		}
		userCtx := viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new("admin")}})
		resp, err := listPolicyAutomationActivitiesEndpoint(userCtx, &automdm.ListPolicyAutomationActivitiesRequest{PolicyID: 1}, svc)
		require.NoError(t, err)
		listResp, ok := resp.(automdm.ListPolicyAutomationActivitiesResponse)
		require.True(t, ok)
		require.NoError(t, listResp.Err)
		require.Equal(t, uint(123), listResp.Count)
	})
}

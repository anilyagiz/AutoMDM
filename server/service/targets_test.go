package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchTargets(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	hosts := []*automdm.Host{
		{Hostname: "foo.local"},
	}
	labels := []*automdm.Label{
		{
			Name:  "label foo",
			Query: "query foo",
		},
	}
	teams := []*automdm.Team{
		{Name: "team1"},
	}

	ds.SearchHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, excludeMobile bool, omit ...uint) ([]*automdm.Host, error) {
		assert.Equal(t, user, filter.User)
		return hosts, nil
	}
	ds.SearchLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Label, error) {
		assert.Equal(t, user, filter.User)
		return labels, nil
	}
	ds.SearchTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Team, error) {
		assert.Equal(t, user, filter.User)
		return teams, nil
	}

	results, err := svc.SearchTargets(ctx, "foo", nil, automdm.HostTargets{})
	require.NoError(t, err)
	assert.Equal(t, hosts[0], results.Hosts[0])
	assert.Equal(t, labels[0], results.Labels[0])
	assert.Equal(t, teams[0], results.Teams[0])
}

func TestSearchTargetsStripsSecretsAndAgentOptions(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	// Use an observer role to mirror the vulnerable scenario.
	user := &automdm.User{GlobalRole: new(automdm.RoleObserver)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	agentOpts := json.RawMessage(`{"config":{"options":{"aws_secret_access_key":"SECRET"}}}`)
	teams := []*automdm.Team{
		{
			ID:   1,
			Name: "team1",
			Config: automdm.TeamConfig{
				AgentOptions: &agentOpts,
			},
			Secrets: []*automdm.EnrollSecret{
				{Secret: "super-secret-token", TeamID: new(uint(1))},
			},
		},
		{
			ID:   2,
			Name: "team2",
			Secrets: []*automdm.EnrollSecret{
				{Secret: "another-secret", TeamID: new(uint(2))},
			},
		},
	}

	ds.SearchHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, excludeMobile bool, omit ...uint) ([]*automdm.Host, error) {
		return nil, nil
	}
	ds.SearchLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.SearchTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Team, error) {
		return teams, nil
	}

	results, err := svc.SearchTargets(ctx, "", nil, automdm.HostTargets{})
	require.NoError(t, err)
	require.Len(t, results.Teams, 2)

	for _, team := range results.Teams {
		assert.Nil(t, team.Secrets, "secrets should be stripped from team %s", team.Name)
		assert.Nil(t, team.Config.AgentOptions, "agent_options should be stripped from team %s", team.Name)
	}

	// Verify non-sensitive fields are preserved.
	assert.Equal(t, uint(1), results.Teams[0].ID)
	assert.Equal(t, "team1", results.Teams[0].Name)
	assert.Equal(t, uint(2), results.Teams[1].ID)
	assert.Equal(t, "team2", results.Teams[1].Name)
}

func TestSearchWithOmit(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	ds.SearchHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, excludeMobile bool, omit ...uint) ([]*automdm.Host, error) {
		assert.Equal(t, user, filter.User)
		assert.Equal(t, []uint{1, 2}, omit)
		return nil, nil
	}
	ds.SearchLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Label, error) {
		assert.Equal(t, user, filter.User)
		assert.Equal(t, []uint{3, 4}, omit)
		return nil, nil
	}
	ds.SearchTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Team, error) {
		assert.Equal(t, user, filter.User)
		assert.Equal(t, []uint{5, 6}, omit)
		return nil, nil
	}

	_, err := svc.SearchTargets(ctx, "foo", nil, automdm.HostTargets{HostIDs: []uint{1, 2}, LabelIDs: []uint{3, 4}, TeamIDs: []uint{5, 6}})
	require.NoError(t, err)
}

func TestSearchTargetsExcludesMobileHosts(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	user := &automdm.User{GlobalRole: new(automdm.RoleAdmin)}
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

	ds.SearchHostsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, excludeMobile bool, omit ...uint) ([]*automdm.Host, error) {
		assert.True(t, excludeMobile)
		return nil, nil
	}
	ds.SearchLabelsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.SearchTeamsFunc = func(ctx context.Context, filter automdm.TeamFilter, query string, omit ...uint) ([]*automdm.Team, error) {
		return nil, nil
	}

	_, err := svc.SearchTargets(ctx, "local", nil, automdm.HostTargets{})
	require.NoError(t, err)
	require.True(t, ds.SearchHostsFuncInvoked)
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nopLiveQuery struct{}

func (nopLiveQuery) RunQuery(name, sql string, hostIDs []uint) error {
	return nil
}

func (nopLiveQuery) StopQuery(name string) error {
	return nil
}

func (nopLiveQuery) QueriesForHost(hostID uint) (map[string]string, error) {
	return map[string]string{}, nil
}

func (nopLiveQuery) QueryCompletedByHost(name string, hostID uint) (bool, error) {
	return true, nil
}

func (nopLiveQuery) RestoreQueryTargetForHost(name string, hostID uint) error {
	return nil
}

func (nopLiveQuery) CleanupInactiveQueries(ctx context.Context, inactiveCampaignIDs []uint) error {
	return nil
}

func (q nopLiveQuery) LoadActiveQueryNames() ([]string, error) {
	return nil, nil
}

func (q nopLiveQuery) GetQueryResultsCounts([]uint) (map[uint]int, error) {
	return make(map[uint]int), nil
}

func (q nopLiveQuery) IncrQueryResultsCounts(map[uint]int) error {
	return nil
}

func (q nopLiveQuery) SetQueryResultsCount(uint, int) error {
	return nil
}

func (q nopLiveQuery) DeleteQueryResultsCount(uint) error {
	return nil
}

func (q nopLiveQuery) SetQueryReportsHostCount(int) error {
	return nil
}

func (q nopLiveQuery) GetQueryReportsHostCount() (int, bool, error) {
	return 0, false, nil
}

func (q nopLiveQuery) SetQueryReportsHostCountIfAbsent(int) error {
	return nil
}

func (q nopLiveQuery) SetQueryResultsCountsIfAbsent(map[uint]int) error {
	return nil
}

func (q nopLiveQuery) IncrQueryReportsHostCount(int) error {
	return nil
}

func (q nopLiveQuery) MarkQueryReportsClipped(map[uint]time.Duration) error {
	return nil
}

func (q nopLiveQuery) QueryReportsClipped([]uint) (map[uint]bool, error) {
	return map[uint]bool{}, nil
}

func (q nopLiveQuery) ClearQueryReportsClipped([]uint) error {
	return nil
}

func (q nopLiveQuery) RecordQueryResultsLastFetched([]uint, time.Time) error {
	return nil
}

func (q nopLiveQuery) LoadQueryResultsLastFetched() (map[uint]time.Time, error) {
	return map[uint]time.Time{}, nil
}

func (q nopLiveQuery) ClearProcessedQueryResultsLastFetched() error {
	return nil
}

func (q nopLiveQuery) AcquireQueryReportWriteSlot(string, int, time.Duration) (bool, error) {
	return true, nil
}

func (q nopLiveQuery) ReleaseQueryReportWriteSlot(string) error {
	return nil
}

func (q nopLiveQuery) LiveQueryStore() automdm.LiveQueryStore {
	return q
}

func TestLiveQueryAuth(t *testing.T) {
	ds := new(mock.Store)
	qr := pubsub.NewInmemQueryResults()
	svc, ctx := newTestService(t, ds, qr, nopLiveQuery{})

	teamMaintainer := &automdm.User{ID: 42, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleMaintainer}}}
	query1ObsCanRun := &automdm.Query{
		ID:             1,
		AuthorID:       ptr.Uint(teamMaintainer.ID),
		Name:           "q1",
		Query:          "SELECT 1",
		ObserverCanRun: true,
	}
	query2ObsCannotRun := &automdm.Query{
		ID:             2,
		AuthorID:       ptr.Uint(teamMaintainer.ID),
		Name:           "q2",
		Query:          "SELECT 2",
		ObserverCanRun: false,
	}

	var lastCreatedQuery *automdm.Query
	ds.NewQueryFunc = func(ctx context.Context, query *automdm.Query, opts ...automdm.OptionalArg) (*automdm.Query, error) {
		q := *query
		vw, ok := viewer.FromContext(ctx)
		q.ID = 123
		if ok {
			q.AuthorID = ptr.Uint(vw.User.ID)
		}
		lastCreatedQuery = &q
		return &q, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{LiveQueryDisabled: false}}, nil
	}
	ds.NewDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) (*automdm.DistributedQueryCampaign, error) {
		return camp, nil
	}
	ds.NewDistributedQueryCampaignTargetFunc = func(ctx context.Context, target *automdm.DistributedQueryCampaignTarget) (*automdm.DistributedQueryCampaignTarget, error) {
		return target, nil
	}
	ds.HostIDsInTargetsFunc = func(ctx context.Context, filters automdm.TeamFilter, targets automdm.HostTargets) ([]uint, error) {
		return []uint{1}, nil
	}
	ds.HostIDsByIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, identifiers []string) ([]uint, error) {
		return nil, nil
	}
	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		return nil, nil
	}
	ds.CountHostsInTargetsFunc = func(ctx context.Context, filters automdm.TeamFilter, targets automdm.HostTargets, now time.Time) (automdm.TargetMetrics, error) {
		return automdm.TargetMetrics{}, nil
	}
	ds.QueryFunc = func(ctx context.Context, id uint) (*automdm.Query, error) {
		if id == 1 {
			return query1ObsCanRun, nil
		}
		if id == 2 {
			return query2ObsCannotRun, nil
		}
		if lastCreatedQuery != nil {
			q := lastCreatedQuery
			lastCreatedQuery = nil
			return q, nil
		}
		return &automdm.Query{ID: 8888, AuthorID: ptr.Uint(6666)}, nil
	}

	testCases := []struct {
		name                   string
		user                   *automdm.User
		teamID                 *uint // to use as host target
		shouldFailRunNew       bool
		shouldFailRunObsCan    bool
		shouldFailRunObsCannot bool
	}{
		{
			name:                   "global admin",
			user:                   &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)},
			teamID:                 nil,
			shouldFailRunNew:       false,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: false,
		},
		{
			name:                   "global maintainer",
			user:                   &automdm.User{GlobalRole: ptr.String(automdm.RoleMaintainer)},
			teamID:                 nil,
			shouldFailRunNew:       false,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: false,
		},
		{
			name:                   "global observer",
			user:                   &automdm.User{GlobalRole: ptr.String(automdm.RoleObserver)},
			teamID:                 nil,
			shouldFailRunNew:       true,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: true,
		},
		{
			name:                   "team maintainer",
			user:                   teamMaintainer,
			teamID:                 nil,
			shouldFailRunNew:       false,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: false,
		},
		{
			name:                   "team admin, no team target",
			user:                   &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			teamID:                 nil,
			shouldFailRunNew:       false,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: false,
		},
		{
			name:                   "team admin, target not set to own team",
			user:                   &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			teamID:                 ptr.Uint(2),
			shouldFailRunNew:       false,
			shouldFailRunObsCan:    true, // fails observer can run, as they are not part of that team, even as observer
			shouldFailRunObsCannot: true,
		},
		{
			name:                   "team admin, target set to own team",
			user:                   &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}},
			teamID:                 ptr.Uint(1),
			shouldFailRunNew:       false,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: false,
		},
		{
			name:                   "team observer, no team target",
			user:                   &automdm.User{ID: 48, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			teamID:                 nil,
			shouldFailRunNew:       true,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: true,
		},
		{
			name:                   "team observer, target not set to own team",
			user:                   &automdm.User{ID: 48, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			teamID:                 ptr.Uint(2),
			shouldFailRunNew:       true,
			shouldFailRunObsCan:    true,
			shouldFailRunObsCannot: true,
		},
		{
			name:                   "team observer, target set to own team",
			user:                   &automdm.User{ID: 48, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}},
			teamID:                 ptr.Uint(1),
			shouldFailRunNew:       true,
			shouldFailRunObsCan:    false,
			shouldFailRunObsCannot: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: tt.user})

			var tms []uint
			// Testing RunNew is tricky, because RunNew authorization is done, then
			// the query is created, and then the Run authorization is applied to
			// that now-existing query, so we have to make sure that the Run does not
			// cause a Forbidden error. To this end, the ds.NewQuery mock always sets
			// the AuthorID to the context user, and if the user is member of a team,
			// always set that team as a host target. This will prevent the Run
			// action from failing, if RunNew did succeed.
			if len(tt.user.Teams) > 0 {
				tms = []uint{tt.user.Teams[0].ID}
			}
			_, err := svc.NewDistributedQueryCampaign(ctx, query1ObsCanRun.Query, nil, automdm.HostTargets{TeamIDs: tms})
			checkAuthErr(t, tt.shouldFailRunNew, err)

			if tt.teamID != nil {
				tms = []uint{*tt.teamID}
			}
			_, err = svc.NewDistributedQueryCampaign(ctx, query1ObsCanRun.Query, ptr.Uint(query1ObsCanRun.ID), automdm.HostTargets{TeamIDs: tms})
			checkAuthErr(t, tt.shouldFailRunObsCan, err)

			_, err = svc.NewDistributedQueryCampaign(ctx, query2ObsCannotRun.Query, ptr.Uint(query2ObsCannotRun.ID), automdm.HostTargets{TeamIDs: tms})
			checkAuthErr(t, tt.shouldFailRunObsCannot, err)

			// tests with a team target cannot run the "ByNames" calls, as there's no way
			// to pass a team target with this call.
			if tt.teamID == nil {
				_, err = svc.NewDistributedQueryCampaignByIdentifiers(ctx, query1ObsCanRun.Query, nil, nil, nil)
				checkAuthErr(t, tt.shouldFailRunNew, err)

				_, err = svc.NewDistributedQueryCampaignByIdentifiers(ctx, query1ObsCanRun.Query, ptr.Uint(query1ObsCanRun.ID), nil, nil)
				checkAuthErr(t, tt.shouldFailRunObsCan, err)

				_, err = svc.NewDistributedQueryCampaignByIdentifiers(ctx, query2ObsCannotRun.Query, ptr.Uint(query2ObsCannotRun.ID), nil, nil)
				checkAuthErr(t, tt.shouldFailRunObsCannot, err)
			}
		})
	}
}

func TestLiveQueryLabelValidation(t *testing.T) {
	ds := new(mock.Store)
	qr := pubsub.NewInmemQueryResults()
	svc, ctx := newTestService(t, ds, qr, nopLiveQuery{})

	user := &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
	query := &automdm.Query{
		ID:             1,
		Name:           "q1",
		Query:          "SELECT 1",
		ObserverCanRun: true,
	}
	ds.NewQueryFunc = func(ctx context.Context, query *automdm.Query, opts ...automdm.OptionalArg) (*automdm.Query, error) {
		query.ID = 123
		return query, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{LiveQueryDisabled: false}}, nil
	}
	ds.NewDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) (*automdm.DistributedQueryCampaign, error) {
		return camp, nil
	}
	ds.NewDistributedQueryCampaignTargetFunc = func(ctx context.Context, target *automdm.DistributedQueryCampaignTarget) (*automdm.DistributedQueryCampaignTarget, error) {
		return target, nil
	}
	ds.HostIDsInTargetsFunc = func(ctx context.Context, filters automdm.TeamFilter, targets automdm.HostTargets) ([]uint, error) {
		return []uint{1}, nil
	}
	ds.HostIDsByIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, identifiers []string) ([]uint, error) {
		return nil, nil
	}
	ds.CountHostsInTargetsFunc = func(ctx context.Context, filters automdm.TeamFilter, targets automdm.HostTargets, now time.Time) (automdm.TargetMetrics, error) {
		return automdm.TargetMetrics{}, nil
	}
	ds.QueryFunc = func(ctx context.Context, id uint) (*automdm.Query, error) {
		return query, nil
	}

	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		return map[string][]uint{"label1": {1}}, nil
	}

	testCases := []struct {
		name          string
		labels        []string
		expectedError string
	}{
		{
			name:          "no labels",
			labels:        []string{},
			expectedError: "",
		},
		{
			name:          "invalid label",
			labels:        []string{"iamnotalabel"},
			expectedError: "Invalid label name(s): iamnotalabel.",
		},
		{
			name:          "valid label",
			labels:        []string{"label1"},
			expectedError: "",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := viewer.NewContext(ctx, viewer.Viewer{User: user})
			_, err := svc.NewDistributedQueryCampaignByIdentifiers(ctx, query.Query, nil, nil, tt.labels)

			if tt.expectedError == "" {
				require.Nil(t, err)
			} else {
				require.NotNil(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

// TestLiveQueryLabelBypassPrevented verifies that when a user is observer on team 1 and
// maintainer on team 2, running a team-2 query via a label target:
//   - observer_can_run=false: HostIDsInTargets sees IncludeObserver=false, so team-1 observer
//     hosts are excluded (label bypass prevented).
//   - observer_can_run=true: HostIDsInTargets sees IncludeObserver=true but ObserverTeamID=2,
//     so team-1 observer hosts are still excluded — observer_can_run is scoped to the query's
//     own team (team 2), not to all teams the user observes.
func TestLiveQueryLabelBypassPrevented(t *testing.T) {
	ds := new(mock.Store)
	qr := pubsub.NewInmemQueryResults()
	svc, ctx := newTestService(t, ds, qr, nopLiveQuery{})

	user := &automdm.User{
		ID: 99,
		Teams: []automdm.UserTeam{
			{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver},
			{Team: automdm.Team{ID: 2}, Role: automdm.RoleMaintainer},
		},
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{ServerSettings: automdm.ServerSettings{LiveQueryDisabled: false}}, nil
	}
	ds.NewDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) (*automdm.DistributedQueryCampaign, error) {
		return camp, nil
	}
	ds.NewDistributedQueryCampaignTargetFunc = func(ctx context.Context, target *automdm.DistributedQueryCampaignTarget) (*automdm.DistributedQueryCampaignTarget, error) {
		return target, nil
	}

	testCases := []struct {
		name                 string
		observerCanRun       bool
		expectIncludeObs     bool
		expectObserverTeamID *uint
	}{
		{
			name:                 "observer_can_run=false: label expansion excludes observer-only team hosts",
			observerCanRun:       false,
			expectIncludeObs:     false,
			expectObserverTeamID: ptr.Uint(2), // always set to query's team; harmless no-op when IncludeObserver=false
		},
		{
			name:                 "observer_can_run=true: label expansion scoped to query's team only",
			observerCanRun:       true,
			expectIncludeObs:     true,
			expectObserverTeamID: ptr.Uint(2), // query's team — team-1 observer hosts remain excluded
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			query := &automdm.Query{
				ID:             10,
				AuthorID:       ptr.Uint(user.ID),
				Name:           "q",
				Query:          "SELECT 1",
				ObserverCanRun: tt.observerCanRun,
				TeamID:         ptr.Uint(2),
			}
			ds.QueryFunc = func(_ context.Context, _ uint) (*automdm.Query, error) {
				return query, nil
			}

			var capturedFilter automdm.TeamFilter
			ds.HostIDsInTargetsFunc = func(_ context.Context, filter automdm.TeamFilter, _ automdm.HostTargets) ([]uint, error) {
				capturedFilter = filter
				return []uint{20}, nil
			}
			ds.CountHostsInTargetsFunc = func(_ context.Context, _ automdm.TeamFilter, _ automdm.HostTargets, _ time.Time) (automdm.TargetMetrics, error) {
				return automdm.TargetMetrics{TotalHosts: 1}, nil
			}

			userCtx := viewer.NewContext(ctx, viewer.Viewer{User: user})
			_, err := svc.NewDistributedQueryCampaign(userCtx, query.Query, ptr.Uint(query.ID), automdm.HostTargets{LabelIDs: []uint{5}})
			require.NoError(t, err)
			assert.Equal(t, tt.expectIncludeObs, capturedFilter.IncludeObserver, "IncludeObserver mismatch for observer_can_run=%v", tt.observerCanRun)
			assert.Equal(t, tt.expectObserverTeamID, capturedFilter.ObserverTeamID, "ObserverTeamID mismatch for observer_can_run=%v", tt.observerCanRun)
			assert.Equal(t, user, capturedFilter.User)
		})
	}
}

type captureAgentNotifier struct {
	err        error
	hostIDs    []uint
	campaignID uint
	invoked    bool
}

func (c *captureAgentNotifier) NotifyAgentsForLiveQuery(ctx context.Context, hostIDs []uint, campaignID uint) error {
	c.invoked = true
	c.hostIDs = hostIDs
	c.campaignID = campaignID
	return c.err
}

func (c *captureAgentNotifier) NotifyOrbitConfigHosts(ctx context.Context, hostIDs []uint, reason string) error {
	return nil
}

func (c *captureAgentNotifier) NotifyOrbitConfigScope(ctx context.Context, scope automdm.AgentNotificationScope, reason string) error {
	return nil
}

func (c *captureAgentNotifier) NotifyOsqueryConfigHosts(ctx context.Context, hostIDs []uint, reason string) error {
	return nil
}

func (c *captureAgentNotifier) NotifyOsqueryConfigScope(ctx context.Context, scope automdm.AgentNotificationScope, reason string) error {
	return nil
}

func TestNewDistributedQueryCampaignNotifiesAgents(t *testing.T) {
	setup := func(t *testing.T) (automdm.Service, context.Context) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, pubsub.NewInmemQueryResults(), nopLiveQuery{})

		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		ds.QueryFunc = func(ctx context.Context, id uint) (*automdm.Query, error) {
			return &automdm.Query{ID: id, Query: "SELECT 1", ObserverCanRun: true, Logging: automdm.LoggingSnapshot}, nil
		}
		ds.NewDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) (*automdm.DistributedQueryCampaign, error) {
			camp.ID = 99
			return camp, nil
		}
		ds.NewDistributedQueryCampaignTargetFunc = func(ctx context.Context, target *automdm.DistributedQueryCampaignTarget) (*automdm.DistributedQueryCampaignTarget, error) {
			return target, nil
		}
		ds.HostIDsInTargetsFunc = func(ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets) ([]uint, error) {
			return []uint{10, 20}, nil
		}
		ds.CountHostsInTargetsFunc = func(ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets, now time.Time) (automdm.TargetMetrics, error) {
			return automdm.TargetMetrics{TotalHosts: 2}, nil
		}

		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}})
		return svc, ctx
	}

	t.Run("notifier receives targeted hosts and campaign ID", func(t *testing.T) {
		svc, ctx := setup(t)
		notifier := &captureAgentNotifier{}
		svc.SetAgentCheckInNotifier(notifier)

		campaign, err := svc.NewDistributedQueryCampaign(ctx, "", new(uint(1)), automdm.HostTargets{HostIDs: []uint{10, 20}})
		require.NoError(t, err)
		require.True(t, notifier.invoked)
		assert.Equal(t, []uint{10, 20}, notifier.hostIDs)
		assert.Equal(t, campaign.ID, notifier.campaignID)
	})

	t.Run("notifier error does not fail the campaign", func(t *testing.T) {
		svc, ctx := setup(t)
		notifier := &captureAgentNotifier{err: errors.New("redis down")}
		svc.SetAgentCheckInNotifier(notifier)

		_, err := svc.NewDistributedQueryCampaign(ctx, "", new(uint(1)), automdm.HostTargets{HostIDs: []uint{10, 20}})
		require.NoError(t, err)
		require.True(t, notifier.invoked)
	})

	t.Run("no notifier set", func(t *testing.T) {
		svc, ctx := setup(t)
		_, err := svc.NewDistributedQueryCampaign(ctx, "", new(uint(1)), automdm.HostTargets{HostIDs: []uint{10, 20}})
		require.NoError(t, err)
	})
}

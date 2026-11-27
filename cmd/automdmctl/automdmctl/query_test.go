package automdmctl

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/live_query/live_query_mock"
	"github.com/anilyagiz/automdm/v4/server/pubsub"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSavedLiveQuery(t *testing.T) {
	rs := pubsub.NewInmemQueryResults()
	lq := live_query_mock.New(t)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	_, ds := testing_utils.RunServerWithMockedDS(t, &service.TestServerOpts{
		Rs:     rs,
		Lq:     lq,
		Logger: logger,
	})

	users, err := ds.ListUsersFunc(context.Background(), automdm.UserListOptions{})
	require.NoError(t, err)
	var admin *automdm.User
	for _, user := range users {
		if user.GlobalRole != nil && *user.GlobalRole == automdm.RoleAdmin {
			admin = user
		}
	}

	const queryName = "saved-query"
	const queryString = "select 42, * from time"
	query := automdm.Query{
		ID:    42,
		Name:  queryName,
		Query: queryString,
		Saved: true,
	}

	ds.HostIDsByIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, hostIdentifiers []string) ([]uint, error) {
		if len(hostIdentifiers) == 1 && hostIdentifiers[0] == "1234" {
			return []uint{1234}, nil
		}
		return nil, nil
	}
	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		return nil, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListQueriesFunc = func(ctx context.Context, opt automdm.ListQueryOptions) ([]*automdm.Query, int, int, *automdm.PaginationMetadata, error) {
		if opt.MatchQuery == queryName {
			return []*automdm.Query{&query}, 1, 0, nil, nil
		}
		return []*automdm.Query{}, 0, 0, nil, nil
	}
	ds.NewDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) (*automdm.DistributedQueryCampaign, error) {
		camp.ID = 321
		return camp, nil
	}
	ds.NewDistributedQueryCampaignTargetFunc = func(ctx context.Context, target *automdm.DistributedQueryCampaignTarget) (*automdm.DistributedQueryCampaignTarget, error) {
		return target, nil
	}
	noHostsTargeted := false
	ds.HostIDsInTargetsFunc = func(ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets) ([]uint, error) {
		if noHostsTargeted {
			return nil, nil
		}
		return []uint{1}, nil
	}
	ds.CountHostsInTargetsFunc = func(ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets, now time.Time) (automdm.TargetMetrics, error) {
		return automdm.TargetMetrics{TotalHosts: 1, OnlineHosts: 1}, nil
	}

	lq.On("QueriesForHost", uint(1)).Return(
		map[string]string{
			"42": queryString,
		},
		nil,
	)
	lq.On("QueryCompletedByHost", "42", 99).Return(true, nil)
	lq.On("RunQuery", "321", queryString, []uint{1}).Return(nil)

	ds.DistributedQueryCampaignTargetIDsFunc = func(ctx context.Context, id uint) (targets *automdm.HostTargets, err error) {
		return &automdm.HostTargets{HostIDs: []uint{99}}, nil
	}
	ds.DistributedQueryCampaignFunc = func(ctx context.Context, id uint) (*automdm.DistributedQueryCampaign, error) {
		return &automdm.DistributedQueryCampaign{
			ID:     321,
			UserID: admin.ID,
		}, nil
	}
	ds.SaveDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) error {
		return nil
	}
	ds.QueryFunc = func(ctx context.Context, id uint) (*automdm.Query, error) {
		return &query, nil
	}
	ds.IsSavedQueryFunc = func(ctx context.Context, queryID uint) (bool, error) {
		return true, nil
	}
	var GetLiveQueryStatsFuncWg sync.WaitGroup
	GetLiveQueryStatsFuncWg.Add(2)
	ds.GetLiveQueryStatsFunc = func(ctx context.Context, queryID uint, hostIDs []uint) ([]*automdm.LiveQueryStats, error) {
		stats := []*automdm.LiveQueryStats{
			{
				LastExecuted: time.Now(),
			},
		}
		GetLiveQueryStatsFuncWg.Done()
		return stats, nil
	}
	var UpdateLiveQueryStatsFuncWg sync.WaitGroup
	UpdateLiveQueryStatsFuncWg.Add(1)
	ds.UpdateLiveQueryStatsFunc = func(ctx context.Context, queryID uint, stats []*automdm.LiveQueryStats) error {
		UpdateLiveQueryStatsFuncWg.Done()
		return nil
	}
	var CalculateAggregatedPerfStatsPercentilesFuncWg sync.WaitGroup
	CalculateAggregatedPerfStatsPercentilesFuncWg.Add(1)
	ds.CalculateAggregatedPerfStatsPercentilesFunc = func(ctx context.Context, aggregate automdm.AggregatedStatsType, queryID uint) error {
		CalculateAggregatedPerfStatsPercentilesFuncWg.Done()
		return nil
	}

	go func() {
		time.Sleep(2 * time.Second)
		require.NoError(t, rs.WriteResult(
			automdm.DistributedQueryResult{
				DistributedQueryCampaignID: 321,
				Rows:                       []map[string]string{{"bing": "fds"}},
				Host: automdm.ResultHostData{
					ID:          99,
					Hostname:    "somehostname",
					DisplayName: "somehostname",
				},
				Stats: &automdm.Stats{
					WallTimeMs: 10,
					UserTime:   20,
					SystemTime: 30,
					Memory:     40,
				},
			},
		))
	}()

	// errors before requesting live query
	_, err = runAppNoChecks([]string{"report", "--hosts", "", "--report-name", queryName})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "No hosts or labels targeted")

	expected := `{"host":"somehostname","rows":[{"bing":"fds","host_display_name":"somehostname"}]}
`
	// Note: runAppForTest never closes the WebSocket connection and does not exit,
	// so we are unable to see the activity data that is written after WebSocket disconnects.
	assert.Equal(t, expected, runAppForTest(t, []string{"report", "--hosts", "1234", "--report-name", queryName}))

	// We need to use waitGroups to detect whether Database functions were called because this is an asynchronous test which will flag data races otherwise.
	c := make(chan struct{})
	go func() {
		defer close(c)
		GetLiveQueryStatsFuncWg.Wait()
		UpdateLiveQueryStatsFuncWg.Wait()
		CalculateAggregatedPerfStatsPercentilesFuncWg.Wait()
	}()
	select {
	case <-time.After(time.Second):
		require.Fail(
			t,
			"Expected invocation of one of these Database functions did not happen: GetLiveQueryStats, UpdateLiveQueryStats, or CalculateAggregatedPerfStatsPercentiles",
		)
	case <-c: // All good
	}

	// Test targeting no hosts (e.g. host does exist)
	noHostsTargeted = true
	_, err = runAppNoChecks([]string{"report", "--hosts", "foobar", "--report-name", queryName})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "No hosts targeted")
}

func TestAdHocLiveQuery(t *testing.T) {
	rs := pubsub.NewInmemQueryResults()
	lq := live_query_mock.New(t)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	_, ds := testing_utils.RunServerWithMockedDS(
		t, &service.TestServerOpts{
			Rs:     rs,
			Lq:     lq,
			Logger: logger,
		},
	)

	users, err := ds.ListUsersFunc(context.Background(), automdm.UserListOptions{})
	require.NoError(t, err)
	var admin *automdm.User
	for _, user := range users {
		if user.GlobalRole != nil && *user.GlobalRole == automdm.RoleAdmin {
			admin = user
		}
	}

	ds.HostIDsByIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, hostIdentifiers []string) ([]uint, error) {
		return []uint{1234}, nil
	}
	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		return map[string][]uint{"label1": {1}}, nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.NewQueryFunc = func(ctx context.Context, query *automdm.Query, opts ...automdm.OptionalArg) (*automdm.Query, error) {
		query.ID = 42
		return query, nil
	}
	ds.NewDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) (
		*automdm.DistributedQueryCampaign, error,
	) {
		camp.ID = 321
		return camp, nil
	}
	ds.NewDistributedQueryCampaignTargetFunc = func(
		ctx context.Context, target *automdm.DistributedQueryCampaignTarget,
	) (*automdm.DistributedQueryCampaignTarget, error) {
		return target, nil
	}
	ds.HostIDsInTargetsFunc = func(ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets) ([]uint, error) {
		return []uint{1}, nil
	}
	ds.CountHostsInTargetsFunc = func(
		ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets, now time.Time,
	) (automdm.TargetMetrics, error) {
		return automdm.TargetMetrics{TotalHosts: 1, OnlineHosts: 1}, nil
	}

	lq.On("QueriesForHost", uint(1)).Return(
		map[string]string{
			"42": "select 42, * from time",
		},
		nil,
	)
	lq.On("QueryCompletedByHost", "42", 99).Return(true, nil)
	lq.On("RunQuery", "321", "select 42, * from time", []uint{1}).Return(nil)

	ds.DistributedQueryCampaignTargetIDsFunc = func(ctx context.Context, id uint) (targets *automdm.HostTargets, err error) {
		return &automdm.HostTargets{HostIDs: []uint{99}}, nil
	}
	ds.DistributedQueryCampaignFunc = func(ctx context.Context, id uint) (*automdm.DistributedQueryCampaign, error) {
		return &automdm.DistributedQueryCampaign{
			ID:     321,
			UserID: admin.ID,
		}, nil
	}
	ds.SaveDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) error {
		return nil
	}
	ds.QueryFunc = func(ctx context.Context, id uint) (*automdm.Query, error) {
		return &automdm.Query{}, nil
	}
	ds.IsSavedQueryFunc = func(ctx context.Context, queryID uint) (bool, error) {
		return false, nil
	}

	go func() {
		time.Sleep(2 * time.Second)
		require.NoError(
			t, rs.WriteResult(
				automdm.DistributedQueryResult{
					DistributedQueryCampaignID: 321,
					Rows:                       []map[string]string{{"bing": "fds"}},
					Host: automdm.ResultHostData{
						ID:          99,
						Hostname:    "somehostname",
						DisplayName: "somehostname",
					},
					Stats: &automdm.Stats{
						WallTimeMs: 10,
						UserTime:   20,
						SystemTime: 30,
						Memory:     40,
					},
				},
			),
		)
	}()

	// test label not found
	_, err = runAppNoChecks([]string{"report", "--hosts", "1234", "--labels", "iamnotalabel", "--query", "select 42, * from time"})
	assert.ErrorContains(t, err, "Invalid label name(s): iamnotalabel.")

	// test if some labels were not found
	_, err = runAppNoChecks([]string{"report", "--labels", "label1, mac, windows", "--hosts", "1234", "--query",
		"select 42, * from time"})
	assert.ErrorContains(t, err, "Invalid label name(s): mac, windows.")

	expected := `{"host":"somehostname","rows":[{"bing":"fds","host_display_name":"somehostname"}]}
`
	assert.Equal(t, expected, runAppForTest(t, []string{"report", "--hosts", "1234", "--query", "select 42, * from time"}))

	// Server drops the websocket after select_campaign before the report completes.
	lq.On("StopQuery", "321").Return(nil)
	ds.DistributedQueryCampaignTargetIDsFunc = func(ctx context.Context, id uint) (*automdm.HostTargets, error) {
		return nil, errors.New("websocket closed early")
	}
	_, err = runAppNoChecks([]string{"report", "--hosts", "1234", "--query", "select 42, * from time"})
	require.ErrorContains(t, err, "live query connection closed before report completed")
}

func TestLiveQueryLabelsWithoutHosts(t *testing.T) {
	rs := pubsub.NewInmemQueryResults()
	lq := live_query_mock.New(t)

	_, ds := testing_utils.RunServerWithMockedDS(t, &service.TestServerOpts{
		Rs:     rs,
		Lq:     lq,
		Logger: slog.New(slog.DiscardHandler),
	})

	users, err := ds.ListUsersFunc(t.Context(), automdm.UserListOptions{})
	require.NoError(t, err)
	var admin *automdm.User
	for _, user := range users {
		if user.GlobalRole != nil && *user.GlobalRole == automdm.RoleAdmin {
			admin = user
		}
	}
	require.NotNil(t, admin)

	const queryString = "select 1"
	var (
		mu                 sync.Mutex
		gotHostIdentifiers []string
		gotLabelNames      []string
	)
	ds.HostIDsByIdentifierFunc = func(ctx context.Context, filter automdm.TeamFilter, hostIdentifiers []string) ([]uint, error) {
		mu.Lock()
		defer mu.Unlock()
		gotHostIdentifiers = hostIdentifiers
		return nil, nil
	}
	ds.AllLabelIDsByNameFunc = func(ctx context.Context, names []string, filter automdm.TeamFilter) (map[string][]uint, error) {
		mu.Lock()
		defer mu.Unlock()
		gotLabelNames = names
		return map[string][]uint{"label1": {1}}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.NewQueryFunc = func(ctx context.Context, query *automdm.Query, opts ...automdm.OptionalArg) (*automdm.Query, error) {
		query.ID = 42
		return query, nil
	}
	ds.NewDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) (*automdm.DistributedQueryCampaign, error) {
		camp.ID = 321
		return camp, nil
	}
	ds.NewDistributedQueryCampaignTargetFunc = func(ctx context.Context, target *automdm.DistributedQueryCampaignTarget) (*automdm.DistributedQueryCampaignTarget, error) {
		return target, nil
	}
	ds.HostIDsInTargetsFunc = func(ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets) ([]uint, error) {
		return []uint{1}, nil
	}
	ds.CountHostsInTargetsFunc = func(ctx context.Context, filter automdm.TeamFilter, targets automdm.HostTargets, now time.Time) (automdm.TargetMetrics, error) {
		return automdm.TargetMetrics{TotalHosts: 1, OnlineHosts: 1}, nil
	}
	ds.DistributedQueryCampaignTargetIDsFunc = func(ctx context.Context, id uint) (*automdm.HostTargets, error) {
		return &automdm.HostTargets{LabelIDs: []uint{1}}, nil
	}
	ds.DistributedQueryCampaignFunc = func(ctx context.Context, id uint) (*automdm.DistributedQueryCampaign, error) {
		return &automdm.DistributedQueryCampaign{ID: 321, UserID: admin.ID}, nil
	}
	ds.SaveDistributedQueryCampaignFunc = func(ctx context.Context, camp *automdm.DistributedQueryCampaign) error {
		return nil
	}
	ds.QueryFunc = func(ctx context.Context, id uint) (*automdm.Query, error) {
		return &automdm.Query{ID: id, Query: queryString}, nil
	}
	ds.IsSavedQueryFunc = func(ctx context.Context, queryID uint) (bool, error) {
		return false, nil
	}
	lq.On("RunQuery", "321", queryString, []uint{1}).Return(nil)

	go func() {
		result := automdm.DistributedQueryResult{
			DistributedQueryCampaignID: 321,
			Rows:                       []map[string]string{{"1": "1"}},
			Host:                       automdm.ResultHostData{ID: 1, Hostname: "labeled-host", DisplayName: "labeled-host"},
		}
		// Writing fails until automdmctl subscribes to the campaign results.
		assert.Eventually(t, func() bool { return rs.WriteResult(result) == nil }, 10*time.Second, 50*time.Millisecond)
	}()

	expected := `{"host":"labeled-host","rows":[{"1":"1","host_display_name":"labeled-host"}]}
`
	assert.Equal(t, expected, runAppForTest(t, []string{"report", "--labels", "label1, ", "--query", queryString}))

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, gotHostIdentifiers, "host identifiers %q", gotHostIdentifiers)
	assert.Equal(t, []string{"label1"}, gotLabelNames)
}

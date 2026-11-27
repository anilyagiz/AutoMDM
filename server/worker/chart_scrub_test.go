package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	chart_api "github.com/anilyagiz/automdm/v4/server/chart/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeChartService implements chart_api.Service. Only the scrub methods are
// exercised here; the rest panic to surface accidental call paths during tests.
type fakeChartService struct {
	scrubGlobalFn  func(ctx context.Context, dataset string) error
	scrubAutoMDMFn func(ctx context.Context, dataset string, automdmIDs []uint) error
}

func (f *fakeChartService) GetChartData(_ context.Context, _ string, _ chart_api.RequestOpts) (*chart_api.Response, error) {
	panic("not used")
}
func (f *fakeChartService) RegisterDataset(_ chart_api.Dataset) { panic("not used") }
func (f *fakeChartService) CollectDatasets(_ context.Context, _ time.Time, _ chart_api.CollectScopeFn) error {
	panic("not used")
}
func (f *fakeChartService) CleanupData(_ context.Context, _ int) error { panic("not used") }
func (f *fakeChartService) ScrubDatasetGlobal(ctx context.Context, dataset string) error {
	return f.scrubGlobalFn(ctx, dataset)
}
func (f *fakeChartService) ScrubDatasetAutoMDM(ctx context.Context, dataset string, automdmIDs []uint) error {
	return f.scrubAutoMDMFn(ctx, dataset, automdmIDs)
}

func TestChartScrubGlobalRun(t *testing.T) {
	t.Run("forwards dataset to service", func(t *testing.T) {
		var got string
		fake := &fakeChartService{scrubGlobalFn: func(_ context.Context, ds string) error {
			got = ds
			return nil
		}}
		j := &ChartScrubGlobal{ChartService: fake}
		args, _ := json.Marshal(ChartScrubGlobalArgs{Dataset: "uptime"})
		require.NoError(t, j.Run(t.Context(), args))
		assert.Equal(t, "uptime", got)
	})

	t.Run("rejects empty dataset", func(t *testing.T) {
		fake := &fakeChartService{scrubGlobalFn: func(_ context.Context, _ string) error {
			t.Fatal("service should not be invoked for empty dataset")
			return nil
		}}
		j := &ChartScrubGlobal{ChartService: fake}
		args, _ := json.Marshal(ChartScrubGlobalArgs{})
		require.Error(t, j.Run(t.Context(), args))
	})

	t.Run("propagates service error", func(t *testing.T) {
		boom := errors.New("boom")
		fake := &fakeChartService{scrubGlobalFn: func(_ context.Context, _ string) error {
			return boom
		}}
		j := &ChartScrubGlobal{ChartService: fake}
		args, _ := json.Marshal(ChartScrubGlobalArgs{Dataset: "uptime"})
		require.ErrorIs(t, j.Run(t.Context(), args), boom)
	})

	t.Run("rejects malformed args JSON", func(t *testing.T) {
		j := &ChartScrubGlobal{ChartService: &fakeChartService{}}
		require.Error(t, j.Run(t.Context(), json.RawMessage("not-json")))
	})
}

func TestChartScrubAutoMDMRun(t *testing.T) {
	t.Run("forwards dataset and team IDs to service", func(t *testing.T) {
		var gotDS string
		var gotTeams []uint
		fake := &fakeChartService{scrubAutoMDMFn: func(_ context.Context, ds string, teams []uint) error {
			gotDS = ds
			gotTeams = teams
			return nil
		}}
		j := &ChartScrubAutoMDM{ChartService: fake}
		args, _ := json.Marshal(ChartScrubAutoMDMArgs{Dataset: "cve", AutoMDMIDs: []uint{5, 7, 11}})
		require.NoError(t, j.Run(t.Context(), args))
		assert.Equal(t, "cve", gotDS)
		assert.Equal(t, []uint{5, 7, 11}, gotTeams)
	})

	t.Run("empty team list is no-op", func(t *testing.T) {
		fake := &fakeChartService{scrubAutoMDMFn: func(_ context.Context, _ string, _ []uint) error {
			t.Fatal("service should not be invoked for empty team list")
			return nil
		}}
		j := &ChartScrubAutoMDM{ChartService: fake}
		args, _ := json.Marshal(ChartScrubAutoMDMArgs{Dataset: "cve"})
		require.NoError(t, j.Run(t.Context(), args))
	})

	t.Run("rejects empty dataset", func(t *testing.T) {
		fake := &fakeChartService{scrubAutoMDMFn: func(_ context.Context, _ string, _ []uint) error {
			t.Fatal("service should not be invoked for empty dataset")
			return nil
		}}
		j := &ChartScrubAutoMDM{ChartService: fake}
		args, _ := json.Marshal(ChartScrubAutoMDMArgs{AutoMDMIDs: []uint{5}})
		require.Error(t, j.Run(t.Context(), args))
	})

	t.Run("propagates service error", func(t *testing.T) {
		boom := errors.New("boom")
		fake := &fakeChartService{scrubAutoMDMFn: func(_ context.Context, _ string, _ []uint) error {
			return boom
		}}
		j := &ChartScrubAutoMDM{ChartService: fake}
		args, _ := json.Marshal(ChartScrubAutoMDMArgs{Dataset: "cve", AutoMDMIDs: []uint{5}})
		require.ErrorIs(t, j.Run(t.Context(), args), boom)
	})
}

// TestChartScrubJobNamesMatchAutoMDMMirror locks the wire contract between the
// automdm-package enqueue helper (which can't import the worker package due to
// an import cycle) and this package's job names + payload struct shapes.
// If this test fails, update the constants in server/automdm/historical_data.go
// to match — workers won't pick up jobs that get enqueued under stale names.
func TestChartScrubJobNamesMatchAutoMDMMirror(t *testing.T) {
	require.Equal(t, "chart_scrub_dataset_global", ChartScrubDatasetGlobalJobName)
	require.Equal(t, "chart_scrub_dataset_automdm", ChartScrubDatasetAutoMDMJobName)

	// Round-trip the args through JSON to verify field names match.
	gJSON, err := json.Marshal(ChartScrubGlobalArgs{Dataset: "uptime"})
	require.NoError(t, err)
	require.JSONEq(t, `{"dataset":"uptime"}`, string(gJSON))

	fJSON, err := json.Marshal(ChartScrubAutoMDMArgs{Dataset: "cve", AutoMDMIDs: []uint{5, 7}})
	require.NoError(t, err)
	require.JSONEq(t, `{"dataset":"cve","team_ids":[5,7]}`, string(fJSON))
}

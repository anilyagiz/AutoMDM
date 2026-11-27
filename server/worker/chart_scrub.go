package worker

import (
	"context"
	"encoding/json"
	"log/slog"

	chart_api "github.com/anilyagiz/automdm/v4/server/chart/api"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

// Job names for chart scrub workers. Persisted in the jobs table as the
// `name` column, so changing them is a wire break — keep stable.
const (
	ChartScrubDatasetGlobalJobName  = "chart_scrub_dataset_global"
	ChartScrubDatasetAutoMDMJobName = "chart_scrub_dataset_automdm"
)

// ChartScrubGlobalArgs is the payload for chart_scrub_dataset_global jobs.
type ChartScrubGlobalArgs struct {
	Dataset string `json:"dataset"`
}

// ChartScrubAutoMDMArgs is the payload for chart_scrub_dataset_automdm jobs.
// AutoMDMIDs is always populated with at least one entry; today, enqueue
// creates one job per team flip — coalescing across teams is a future optimization.
type ChartScrubAutoMDMArgs struct {
	Dataset    string `json:"dataset"`
	AutoMDMIDs []uint `json:"team_ids"`
}

// ChartScrubGlobal runs the global scrub job: deletes every host_scd_data row
// for the configured dataset.
type ChartScrubGlobal struct {
	ChartService chart_api.Service
	Log          *slog.Logger
}

func (c *ChartScrubGlobal) Name() string { return ChartScrubDatasetGlobalJobName }

func (c *ChartScrubGlobal) Run(ctx context.Context, argsJSON json.RawMessage) error {
	var args ChartScrubGlobalArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ctxerr.Wrap(ctx, err, "unmarshal chart scrub global args")
	}
	if args.Dataset == "" {
		return ctxerr.New(ctx, "chart scrub global: empty dataset")
	}
	if c.Log != nil {
		c.Log.InfoContext(ctx, "chart scrub global: starting", "dataset", args.Dataset)
	}
	if err := c.ChartService.ScrubDatasetGlobal(ctx, args.Dataset); err != nil {
		return ctxerr.Wrap(ctx, err, "scrub dataset global")
	}
	if c.Log != nil {
		c.Log.InfoContext(ctx, "chart scrub global: complete", "dataset", args.Dataset)
	}
	return nil
}

// ChartScrubAutoMDM runs the per-team scrub job: clears bits for every host
// currently in any of the listed teams from existing host_scd_data rows for
// the configured dataset.
type ChartScrubAutoMDM struct {
	ChartService chart_api.Service
	Log          *slog.Logger
}

func (c *ChartScrubAutoMDM) Name() string { return ChartScrubDatasetAutoMDMJobName }

func (c *ChartScrubAutoMDM) Run(ctx context.Context, argsJSON json.RawMessage) error {
	var args ChartScrubAutoMDMArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ctxerr.Wrap(ctx, err, "unmarshal chart scrub team args")
	}
	if args.Dataset == "" {
		return ctxerr.New(ctx, "chart scrub team: empty dataset")
	}
	if len(args.AutoMDMIDs) == 0 {
		// Defensive — enqueue side should never produce this. Treat as a
		// no-op rather than scanning the entire table.
		if c.Log != nil {
			c.Log.WarnContext(ctx, "chart scrub team: empty team list", "dataset", args.Dataset)
		}
		return nil
	}
	if c.Log != nil {
		c.Log.InfoContext(ctx, "chart scrub team: starting", "dataset", args.Dataset, "team_ids", args.AutoMDMIDs)
	}
	if err := c.ChartService.ScrubDatasetAutoMDM(ctx, args.Dataset, args.AutoMDMIDs); err != nil {
		return ctxerr.Wrap(ctx, err, "scrub dataset team")
	}
	if c.Log != nil {
		c.Log.InfoContext(ctx, "chart scrub team: complete", "dataset", args.Dataset)
	}
	return nil
}

package wstransport

import (
	"context"

	"github.com/anilyagiz/automdm/v4/client"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/osquery/osquery-go/plugin/distributed"
)

// DistributedPluginName is the plugin name osquery must be pointed at via
// --distributed_plugin to route its distributed queries through orbit. (This
// is the plugin's registry entry name, unrelated to the extension manager's
// extension name.)
const DistributedPluginName = "automdm_orbit_distributed"

// distributedClient is the subset of client.OrbitClient used by the
// distributed plugin.
type distributedClient interface {
	DistributedRead() (*client.DistributedReadResponse, error)
	DistributedWrite(*client.DistributedWriteRequest) error
}

// NewDistributedPlugin returns the osquery distributed plugin that serves
// queries from the manager's cache and forwards results to the AutoMDM server
// over HTTP. Query pickup and write completion feed the manager's iteration
// state machine. osquery's usual distributed poll becomes a localhost thrift
// call answered from memory — no network traffic until there is actual work.
func NewDistributedPlugin(m *Manager, automdmClient distributedClient) *distributed.Plugin {
	getQueries := func(ctx context.Context) (*distributed.GetQueriesResult, error) {
		queries, discovery, accelerate := m.takeQueries()
		if queries == nil {
			queries = map[string]string{}
		}
		return &distributed.GetQueriesResult{
			Queries:           queries,
			Discovery:         discovery,
			AccelerateSeconds: int(accelerate), //nolint:gosec // dictated by the server, small values
		}, nil
	}

	writeResults := func(ctx context.Context, results []distributed.Result) error {
		// Even a zero-result write ends the pass that took the queries.
		defer m.writeDone()
		if len(results) == 0 {
			return nil
		}
		req := &client.DistributedWriteRequest{
			Results:  make(automdm.OsqueryDistributedQueryResults, len(results)),
			Statuses: make(map[string]automdm.OsqueryStatus, len(results)),
			Messages: make(map[string]string),
			Stats:    make(map[string]*automdm.Stats),
		}
		for _, result := range results {
			rows := result.Rows
			if rows == nil {
				rows = []map[string]string{}
			}
			req.Results[result.QueryName] = rows
			req.Statuses[result.QueryName] = automdm.OsqueryStatus(result.Status)
			if result.Message != "" {
				req.Messages[result.QueryName] = result.Message
			}
			if result.QueryStats != nil {
				req.Stats[result.QueryName] = &automdm.Stats{
					WallTimeMs: uint64(result.QueryStats.WallTimeMs), //nolint:gosec // osquery stats are non-negative
					UserTime:   uint64(result.QueryStats.UserTime),   //nolint:gosec // osquery stats are non-negative
					SystemTime: uint64(result.QueryStats.SystemTime), //nolint:gosec // osquery stats are non-negative
					Memory:     uint64(result.QueryStats.Memory),     //nolint:gosec // osquery stats are non-negative
				}
			}
		}
		return automdmClient.DistributedWrite(req)
	}

	return distributed.NewPlugin(DistributedPluginName, getQueries, writeResults)
}

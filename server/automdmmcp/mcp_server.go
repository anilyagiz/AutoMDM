package automdmmcp

import (
	"context"

	"github.com/mark3labs/mcp-go/server"
)

const defaultEndpointsPerPage = 50

// automdmMCPInstructions is the server-level system prompt advertised to MCP
// clients (Claude Desktop, Cursor, etc.) via the `initialize` response. It
// mandates the schema-first workflow that prevents the most common class of
// silent-zero-row bug (assuming column types when writing osquery SQL) and
// directs the client to confirm write operations with the operator first.
//
// NOTE: these are advisory instructions to a cooperating LLM client, not a
// server-enforced control. A prompt-injected or non-cooperating client (or any
// caller driving the tools over raw JSON-RPC) can ignore them — the real bounds
// on writes are the AUTOMDM_API_KEY's AutoMDM role (an observer token => AutoMDM 403s
// the writes) and agent-side osquery `--disable_tables`.
const automdmMCPInstructions = `AutoMDM MCP — host management and live osquery on managed devices.

CRITICAL WORKFLOW for any tool that takes a 'sql' argument (run_live_query):

1. BEFORE writing SQL, call get_osquery_schema(platform=<target>) to fetch the curated table list for that platform.
2. For any table you reference, verify column NAMES and TYPES against the schema response. If a needed table is not in the curated list, call get_osquery_schema(tables="table1,table2") for full canonical coverage.
3. Pay attention to column TYPE in the schema response. Many osquery columns are 'text' even when their values look numeric (e.g. windows_update_history.result_code is text with values like 'Succeeded' / 'Failed', NOT integer codes). Comparing a text column against an unquoted integer literal silently returns zero rows.
4. prepare_live_query already returns the schema for the inferred platform — use it as a single 'preview targets + schema' call, then pass the same filter args to run_live_query.

`

const schemaRefreshInstructions = `Schema freshness: the in-memory schema is refreshed periodically from https://raw.githubusercontent.com/automdm/automdm/main/schema/osquery_automdm_schema.json (the JSON behind https://automdm.com/tables). If you suspect a schema mismatch — e.g. automdm docs show a column the response is missing — call refresh_osquery_schema and try again.

`

const automdmMCPInstructionsTail = `AutoMDM scoping: when the user names a team in the conversation (e.g. "Workstations", "Servers"), pass it as the 'team' argument to run_live_query to restrict the targeted hosts to that team. Only omit 'team' when the user explicitly wants all teams.

CONFIRM BEFORE RUNNING run_live_query: show the operator the exact SQL and the resolved target scope (the host_ids / label / 'team', or "all hosts" if unscoped), then wait for explicit confirmation. Never auto-approve.

Skipping step 1 produces queries that parse and run but return wrong or empty results. Always verify before emitting SQL.`

// SetupMCPServer creates the standalone MCP server. Tool registrations are
// split by domain across mcp_tools_*.go files.
func SetupMCPServer(ctx context.Context, automdmClient *AutoMDMClient) *server.MCPServer {
	// A standalone binary can outlive the AutoMDM version it was built against, so
	// it keeps the embedded schema fresh from GitHub.
	StartSchemaRefresh(ctx, automdmClient.logger, 0)
	return newMCPServer(automdmClient, true)
}

// NewEmbeddedMCPServer creates the MCP server that runs inside the AutoMDM
// server, whose embedded schema already matches its own version. Each request
// sees the tools its ContextWithRouteFilter allows.
func NewEmbeddedMCPServer(automdmClient *AutoMDMClient) *server.MCPServer {
	return newMCPServer(automdmClient, false, routeFilterOptions(automdmClient)...)
}

func newMCPServer(automdmClient *AutoMDMClient, schemaRefresh bool, opts ...server.ServerOption) *server.MCPServer {
	instructions := automdmMCPInstructions + automdmMCPInstructionsTail
	if schemaRefresh {
		instructions = automdmMCPInstructions + schemaRefreshInstructions + automdmMCPInstructionsTail
	}
	s := server.NewMCPServer(
		"automdm-mcp", "1.0.0",
		append([]server.ServerOption{
			server.WithLogging(),
			server.WithInstructions(instructions),
		}, opts...)...,
	)

	registerHostTools(s, automdmClient)
	registerQueryTools(s, automdmClient, schemaRefresh)
	registerPolicyTools(s, automdmClient)
	registerInventoryTools(s, automdmClient)

	return s
}

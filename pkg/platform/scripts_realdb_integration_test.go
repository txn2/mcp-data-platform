//go:build integration

package platform_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// The wiring proof for #1283. The scriptlayer tests assemble their own server,
// which proves the tool and the engine; they cannot prove that the PLATFORM
// registers the tool, that it does so against a real database, or that the
// schema the migrations create is the schema the store writes to. This test
// starts the real platform on a real Postgres and drives manage_script over an
// in-memory client session, which is the whole path a deployment takes.

// scriptClient is a connected session plus the session handle the platform
// requires on every tool call.
type scriptClient struct {
	session   *mcp.ClientSession
	sessionID string
	// db is the migrated database the platform is running on, for the facts a
	// tool response cannot carry — the author roles captured on a version row.
	db *sql.DB
}

// startScriptPlatform assembles a real platform on a migrated Postgres and
// returns a connected client session.
func startScriptPlatform(t *testing.T) (*mcp.ClientSession, *sql.DB) {
	t.Helper()
	db, dsn := testdb.NewWithDSN(t)

	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "scripts-it", Version: "1.0.0"},
		Database: platform.DatabaseConfig{DSN: dsn, MaxOpenConns: 5},
		// A caller matching no persona reaches the deny-all default, which hides
		// every tool. Grant the role the anonymous test identity carries so the
		// client sees the real advertised surface.
		Personas: platform.PersonasConfig{
			Definitions: map[string]platform.PersonaDef{
				"default": {
					DisplayName: "Default",
					Roles:       []string{auth.RoleAnonymous},
					Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
					Connections: platform.ConnectionRulesDef{Allow: []string{"*"}},
				},
			},
		},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	ctx := t.Context()
	require.NoError(t, p.Start(ctx))
	t.Cleanup(func() { _ = p.Stop(ctx) })

	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session, db
}

// newScriptClient starts the platform and performs the session handshake the
// deployment requires: platform_info mints a session_id that every subsequent
// tool call must carry. Doing it here rather than disabling the gate is the
// point — this test is meant to take the same path a real agent takes.
func newScriptClient(t *testing.T) *scriptClient {
	t.Helper()
	session, db := startScriptPlatform(t)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "platform_info"})
	require.NoError(t, err)
	require.False(t, res.IsError, "platform_info must succeed to mint a session")

	structured, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok, "platform_info must answer with structured content")
	sessionID, ok := structured["session_id"].(string)
	require.True(t, ok, "platform_info must mint a session_id")
	require.NotEmpty(t, sessionID)

	return &scriptClient{session: session, sessionID: sessionID, db: db}
}

// callScript runs one manage_script command and decodes its JSON result.
func callScript(t *testing.T, c *scriptClient, args map[string]any) (map[string]any, *mcp.CallToolResult) {
	t.Helper()
	args["session_id"] = c.sessionID
	res, err := c.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_script", Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	if res.IsError {
		return map[string]any{"error": text.Text}, res
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &out), text.Text)
	return out, res
}

// TestRealDB_ManageScriptIsRegisteredAndRoundTripsThroughPostgres drives the
// platform's own wiring: the tool is advertised, a create lands in the migrated
// schema, and get reads back a script that runs — through the real assembled
// facade and the real store, which the unit tests do not exercise together.
func TestRealDB_ManageScriptIsRegisteredAndRoundTripsThroughPostgres(t *testing.T) {
	c := newScriptClient(t)

	tools, err := c.session.ListTools(t.Context(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "manage_script", "the platform must register the script tool when a database is configured")

	created, res := callScript(t, c, map[string]any{
		"command": "create", "name": "wiring-check",
		"source": "print(\"reporting on \" + date.add_days(date.of(run.fire_time), -1))\n",
		"params": []map[string]any{{"name": "day", "type": "date", "required": true}},
	})
	require.False(t, res.IsError, created)
	assert.Equal(t, "created", created["status"])

	got, res := callScript(t, c, map[string]any{"command": "get", "name": "wiring-check"})
	require.False(t, res.IsError, got)
	assert.Equal(t, "wiring-check", got["name"])
	assert.Contains(t, got["executable_note"], "run_script",
		"a saved script runs")

	// The version the live row names carries the roles its author held, which
	// are the roles a run of it presents. Read from the row rather than from a
	// response, because the column, the write, and the migration that shaped
	// it are what this proves.
	var author string
	var status string
	require.NoError(t, c.db.QueryRow(`
		SELECT v.author, v.status
		  FROM script_versions v
		  JOIN scripts s ON s.id = v.script_id AND s.version = v.version
		 WHERE s.name = $1`, "wiring-check").Scan(&author, &status))
	assert.NotEmpty(t, author, "the version records who wrote it")
	assert.Equal(t, "applied", status, "a save produces an applied version")

	listed, res := callScript(t, c, map[string]any{"command": "list"})
	require.False(t, res.IsError, listed)
	assert.EqualValues(t, 1, listed["count"])
}

// TestRealDB_ManageScriptEditFunnelVersionsThroughPostgres proves the edit
// funnel and the version history against the real schema: a source edit
// snapshots a new applied version, and diff reads both back.
func TestRealDB_ManageScriptEditFunnelVersionsThroughPostgres(t *testing.T) {
	c := newScriptClient(t)

	_, res := callScript(t, c, map[string]any{
		"command": "create", "name": "versioned", "source": "print(\"one\")\n",
	})
	require.False(t, res.IsError)

	updated, res := callScript(t, c, map[string]any{
		"command": "update", "name": "versioned", "source": "print(\"two\")\n",
	})
	require.False(t, res.IsError, updated)
	assert.Equal(t, "updated", updated["status"])
	assert.EqualValues(t, 2, updated["version"])

	diff, res := callScript(t, c, map[string]any{"command": "diff", "name": "versioned"})
	require.False(t, res.IsError, diff)
	assert.EqualValues(t, 1, diff["from_version"])
	assert.EqualValues(t, 2, diff["to_version"])
	assert.Contains(t, diff["diff"], "two")
}

// TestRealDB_ManageScriptValidateAndHelpAnswerThroughThePlatform covers the two
// commands an author reaches for first, over the real registered tool.
func TestRealDB_ManageScriptValidateAndHelpAnswerThroughThePlatform(t *testing.T) {
	c := newScriptClient(t)

	help, res := callScript(t, c, map[string]any{"command": "help"})
	require.False(t, res.IsError, help)
	assert.Contains(t, help["dialect"], "platform.query")

	report, res := callScript(t, c, map[string]any{
		"command": "validate",
		"source":  "platform.query(connection=\"warehouse\", sql=\"SELECT 1\")\n",
	})
	require.False(t, res.IsError, report)
	assert.Equal(t, true, report["ok"])
	assert.Equal(t, []any{"warehouse"}, report["connections"])

	refused, res := callScript(t, c, map[string]any{"command": "validate", "source": "import os\n"})
	require.False(t, res.IsError, refused)
	assert.Equal(t, false, refused["ok"])
}

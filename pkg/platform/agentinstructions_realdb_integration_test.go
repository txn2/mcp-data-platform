//go:build integration

package platform

// Real-Postgres proof for the agent_instructions apply sink (#1607). It drives
// the real assembled path end to end: the knowledge toolkit built by
// knowledgelayer, the sink wired onto it from the real Postgres config store
// through internal/agentinstructions, apply_knowledge called over an in-memory
// transport by a real MCP client, and platform_info composed for that caller
// afterwards.
//
// This is what mocks cannot show: that the value the sink writes to
// config_entries is the value platform_info's customized layer resolves on the
// next call. A unit test that hands the sink a fake store proves the sink; only
// this proves the two halves are the same value.
//
// Run under `make test-realdb`.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/agentinstructions"
	"github.com/txn2/mcp-data-platform/internal/platform/knowledgelayer"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	configpostgres "github.com/txn2/mcp-data-platform/pkg/configstore/postgres"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const (
	sinkTestSection = "Query engines"
	sinkTestRule    = "Trino holds the warehouse; OpenSearch aggregations go through raw_query."
	sinkTestActor   = "reviewer@example.com"
)

// A promotion reaches config_entries, and the next platform_info carries it in
// the customized layer beneath the platform baseline.
func TestRealDB_AgentInstructionsSinkReachesPlatformInfo(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	p, client := newInstructionSinkPlatform(ctx, t, db)

	// The baseline is present and the deployment has said nothing of its own yet.
	before := callPlatformInfo(ctx, t, client)
	assert.NotContains(t, before.AgentInstructions, sinkTestSection)

	out := callApplyInstructions(ctx, t, client, map[string]any{
		"section": sinkTestSection,
		"body":    sinkTestRule,
	})
	assert.Equal(t, "created", out["action"])
	assert.Equal(t, "ai:"+sinkTestSection, out["target_urn"])

	// The stored row is what the sink wrote.
	entry, err := p.configStore.Get(ctx, ConfigKeyServerAgentInstructions)
	require.NoError(t, err)
	assert.Contains(t, entry.Value, "## "+sinkTestSection)
	assert.Contains(t, entry.Value, sinkTestRule)
	assert.Equal(t, sinkTestActor, entry.UpdatedBy)

	// The next session reads it, beneath the platform's own baseline.
	after := callPlatformInfo(ctx, t, client)
	require.Contains(t, after.AgentInstructions, "## "+sinkTestSection)
	require.Contains(t, after.AgentInstructions, sinkTestRule)
	assert.True(t,
		strings.Index(after.AgentInstructions, "How to operate this platform") <
			strings.Index(after.AgentInstructions, "## "+sinkTestSection),
		"the platform baseline stays above the deployment's own layer")
}

// A second promotion of the same section rewrites that section and leaves every
// other one byte-identical, through the real store.
func TestRealDB_AgentInstructionsSinkConsolidatesBySection(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	p, client := newInstructionSinkPlatform(ctx, t, db)

	callApplyInstructions(ctx, t, client, map[string]any{
		"section": "Naming", "body": "Tables are singular.",
	})
	callApplyInstructions(ctx, t, client, map[string]any{
		"section": sinkTestSection, "body": "First wording.",
	})
	mid, err := p.configStore.Get(ctx, ConfigKeyServerAgentInstructions)
	require.NoError(t, err)
	naming := mid.Value[:strings.Index(mid.Value, "## "+sinkTestSection)]

	callApplyInstructions(ctx, t, client, map[string]any{
		"section": sinkTestSection, "body": sinkTestRule,
	})
	final, err := p.configStore.Get(ctx, ConfigKeyServerAgentInstructions)
	require.NoError(t, err)

	assert.Equal(t, naming, final.Value[:len(naming)],
		"the section the promotion did not name must be byte-identical")
	assert.Equal(t, 1, strings.Count(final.Value, "## "+sinkTestSection),
		"a repeat promotion must rewrite its section, not append a near-duplicate")
	assert.Contains(t, final.Value, sinkTestRule)
	assert.NotContains(t, final.Value, "First wording.")
}

// The promotion is listed by list_changesets under its own target and reverts
// cleanly, restoring the previous text in the real store.
func TestRealDB_AgentInstructionsPromotionListsAndRollsBack(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	p, client := newInstructionSinkPlatform(ctx, t, db)

	callApplyInstructions(ctx, t, client, map[string]any{
		"section": "Naming", "body": "Tables are singular.",
	})
	priorEntry, err := p.configStore.Get(ctx, ConfigKeyServerAgentInstructions)
	require.NoError(t, err)

	out := callApplyInstructions(ctx, t, client, map[string]any{
		"section": sinkTestSection, "body": sinkTestRule,
	})
	csID, ok := out["changeset_id"].(string)
	require.True(t, ok, "the apply response must carry a changeset id")

	listed := callApplyKnowledge(ctx, t, client, map[string]any{
		"action":     "list_changesets",
		"entity_urn": knowledgekit.InstructionsTargetURN(sinkTestSection),
	})
	changesets, ok := listed["changesets"].([]any)
	require.True(t, ok)
	require.Len(t, changesets, 1)
	row, ok := changesets[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, csID, row["changeset_id"])
	assert.Equal(t, true, row["revertible"])
	assert.Equal(t, false, row["rolled_back"])

	rolled := callApplyKnowledge(ctx, t, client, map[string]any{
		"action": "rollback", "changeset_id": csID, "confirm": true,
	})
	assert.Equal(t, knowledgekit.InstructionsTargetURN(sinkTestSection), rolled["target_urn"])

	restored, err := p.configStore.Get(ctx, ConfigKeyServerAgentInstructions)
	require.NoError(t, err)
	assert.Equal(t, priorEntry.Value, restored.Value,
		"rollback must restore the layer byte for byte")
	assert.NotContains(t, callPlatformInfo(ctx, t, client).AgentInstructions, sinkTestRule)
}

// A promotion naming a tool this deployment does not register is refused at
// write time, against the deployment's real registered inventory. api_ names
// are the case the startup lint's fixed prefix list could not see.
func TestRealDB_AgentInstructionsSinkRefusesAnUnregisteredTool(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	p, client := newInstructionSinkPlatform(ctx, t, db)

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "apply_knowledge",
		Arguments: map[string]any{
			"action": "apply",
			"sink":   "agent_instructions",
			"instructions": map[string]any{
				"section": "API discovery",
				"body":    "Enumerate the operations with api_list_endpoints first.",
			},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "a rule naming a retired tool must be refused")
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "api_list_endpoints")

	_, err = p.configStore.Get(ctx, ConfigKeyServerAgentInstructions)
	require.Error(t, err, "nothing may be stored for a refused promotion")
}

// The byte bound is a property of the layer: the store adapter refuses an
// over-limit value however it arrives.
func TestRealDB_AgentInstructionsLayerIsByteBounded(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	store := configpostgres.New(db)
	layer := agentinstructions.New(store, nil, ConfigKeyServerAgentInstructions)
	require.NotNil(t, layer)

	err := layer.SetAgentInstructions(ctx,
		strings.Repeat("x", agentinstructions.MaxCustomizedBytes+1), sinkTestActor)
	var oversize *agentinstructions.OversizeError
	require.ErrorAs(t, err, &oversize)

	_, getErr := store.Get(ctx, ConfigKeyServerAgentInstructions)
	require.Error(t, getErr, "the refused value must not reach config_entries")
}

// --- harness ---

// newInstructionSinkPlatform assembles the platform pieces the sink needs
// against a real database: the Postgres config store, the knowledge layer's
// toolkit, and the sink wired between them exactly as initKnowledge does. It
// registers apply_knowledge and platform_info on one real MCP server and
// returns a connected client.
func newInstructionSinkPlatform(ctx context.Context, t *testing.T, db *sql.DB) (*Platform, *mcp.ClientSession) {
	t.Helper()

	cfg := &Config{Server: ServerConfig{Name: "test-platform", Version: "1.0.0"}}
	cfg.Purpose.Enabled = new(false)
	store := configpostgres.New(db)
	cfg.BindOverrideStore(store)

	p := &Platform{
		config:          cfg,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
		configStore:     store,
		db:              db,
	}

	handle, err := knowledgelayer.New(db, nil, nil, knowledgelayer.Config{
		ToolkitName:  instanceDefault,
		ApplyEnabled: true,
	})
	require.NoError(t, err)
	require.NotNil(t, handle)
	require.NoError(t, p.toolkitRegistry.Register(handle.Toolkit()))

	handle.Toolkit().SetInstructionsSink(
		agentinstructions.New(p.configStore, p.fileDefaults, ConfigKeyServerAgentInstructions),
		func() []string { return RegisteredToolNames(p.toolkitRegistry.AllTools(), p.PlatformTools()) },
	)

	p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	p.mcpServer.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(middleware.WithPlatformContext(ctx, &middleware.PlatformContext{
				UserID: sinkTestActor, UserEmail: sinkTestActor, AuthType: "oidc",
			}), method, req)
		}
	})
	handle.Toolkit().RegisterTools(p.mcpServer)
	p.registerInfoTool()

	ct, st := mcp.NewInMemoryTransports()
	serverSess, err := p.mcpServer.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSess.Close() })

	clientSess, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil).
		Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSess.Close() })
	return p, clientSess
}

// callApplyInstructions promotes a rule through the real tool call and returns
// the decoded response, failing the test on a refusal.
func callApplyInstructions(ctx context.Context, t *testing.T, client *mcp.ClientSession, instructions map[string]any) map[string]any {
	t.Helper()
	return callApplyKnowledge(ctx, t, client, map[string]any{
		"action":       "apply",
		"sink":         "agent_instructions",
		"instructions": instructions,
	})
}

// callApplyKnowledge calls apply_knowledge with literal params and decodes the
// JSON body the agent would receive.
func callApplyKnowledge(ctx context.Context, t *testing.T, client *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "apply_knowledge", Arguments: args})
	require.NoError(t, err)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.False(t, res.IsError, "apply_knowledge failed: %s", tc.Text)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

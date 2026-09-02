package platform

import (
	"context"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

// newOverrideReplica assembles a Platform whose MCP server carries the real
// visibility and description-override middleware, wired exactly as
// finalizeSetup wires them, with cfg bound to the shared store.
//
// This is the point of the test: it exercises the production registration
// path. A unit test calling Config.ToolsDenySnapshot directly would pass even
// if platform.go handed the middleware a slice captured at startup, which is
// the defect being fixed.
func newOverrideReplica(t *testing.T, store *mutableOverrideStore, tools ...string) *Platform {
	t.Helper()

	cfg := &Config{}
	cfg.BindOverrideStore(store)

	p := &Platform{
		config: cfg,
		// A non-nil config store is what tells the registration path that
		// overrides can be authored later; the bound override store is what
		// actually resolves them.
		configStore: configstore.NewFileStore(map[string]string{}),
		mcpServer:   mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil),
	}

	for _, name := range tools {
		p.mcpServer.AddTool(
			&mcp.Tool{
				Name:        name,
				Description: "original " + name,
				InputSchema: &jsonschema.Schema{Type: "object"},
			},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			},
		)
	}

	p.addDescriptionOverrideMiddleware()
	p.addToolVisibilityMiddleware()
	return p
}

// listTools connects an in-memory client and returns name → description.
func listTools(t *testing.T, p *Platform) map[string]string {
	t.Helper()
	ctx := context.Background()

	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := p.mcpServer.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = clientSession.Close() }()

	resp, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)

	got := make(map[string]string, len(resp.Tools))
	for _, tool := range resp.Tools {
		got[tool.Name] = tool.Description
	}
	return got
}

// TestToolsListReflectsOverridesAcrossReplicas is the end-to-end regression
// test for #1106.
//
// Two Platforms stand in for two replicas sharing one database. An admin edit
// lands in the store (as the admin API's Set does) and both replicas must
// serve it on their next tools/list — including the replica that never saw
// the write.
func TestToolsListReflectsOverridesAcrossReplicas(t *testing.T) {
	shared := newMutableOverrideStore()

	replicaA := newOverrideReplica(t, shared, "trino_query", "s3_object")
	replicaB := newOverrideReplica(t, shared, "trino_query", "s3_object")

	const authored = "operator authored description"

	before := listTools(t, replicaB)
	require.Contains(t, before, "s3_object")
	// trino_query carries a built-in description override, so the baseline is
	// that default rather than the registered text. What matters is that it is
	// not yet the operator's.
	require.NotEmpty(t, before["trino_query"])
	require.NotEqual(t, authored, before["trino_query"])

	// Admin hides a tool and rewrites a description, served by replica A.
	shared.set(ConfigKeyToolsDeny, `["s3_object"]`)
	shared.set("tool.trino_query.description", authored)

	for name, p := range map[string]*Platform{"A": replicaA, "B": replicaB} {
		got := listTools(t, p)
		assert.NotContains(t, got, "s3_object",
			"replica %s still lists a tool the operator hid", name)
		assert.Equal(t, authored, got["trino_query"],
			"replica %s serves a stale tool description", name)
	}

	// Un-hiding is equally live: no restart, no notification between replicas.
	shared.set(ConfigKeyToolsDeny, `[]`)
	assert.Contains(t, listTools(t, replicaB), "s3_object")
}

// TestPlatformInfoReflectsOverridesAcrossReplicas covers the description and
// agent-instruction halves through Platform's own read path, which is what
// platform_info serves.
func TestPlatformInfoReflectsOverridesAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	shared := newMutableOverrideStore()

	cfgA := &Config{}
	cfgA.Server.Description = "file default"
	cfgA.BindOverrideStore(shared)

	cfgB := &Config{}
	cfgB.Server.Description = "file default"
	cfgB.BindOverrideStore(shared)

	shared.set(ConfigKeyServerDescription, "operator authored")
	shared.set(ConfigKeyServerAgentInstructions, "operator guidance")

	for name, cfg := range map[string]*Config{"A": cfgA, "B": cfgB} {
		assert.Equal(t, "operator authored", cfg.ServerDescription(ctx),
			"replica %s serves a stale description", name)
		assert.Equal(t, "operator guidance", cfg.ServerAgentInstructions(ctx),
			"replica %s serves stale agent instructions", name)
	}
}

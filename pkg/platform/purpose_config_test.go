package platform

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/toolargs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// TestPurposeConfig_Defaults pins the default-on convention: an absent purpose
// block advertises, records, and requires a purpose, and only an explicit false
// turns either off.
func TestPurposeConfig_Defaults(t *testing.T) {
	var absent PurposeConfig
	assert.True(t, absent.IsEnabled())
	assert.True(t, absent.IsRequired())

	assert.False(t, PurposeConfig{Enabled: new(false)}.IsEnabled())
	assert.True(t, PurposeConfig{Enabled: new(true)}.IsEnabled())
	assert.False(t, PurposeConfig{Require: new(false)}.IsRequired())
	assert.True(t, PurposeConfig{Require: new(true)}.IsRequired())
}

// TestPurposeConfig_LoadsFromYAML proves the block parses under its own key,
// including the "kind:" entries the default set needs to reach gateway-proxied
// tools.
func TestPurposeConfig_LoadsFromYAML(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte(`
server:
  name: purpose-test
purpose:
  enabled: true
  require: false
  tools:
    - trino_query
    - "datahub_get_*"
    - "kind:mcp"
`))
	require.NoError(t, err)
	assert.True(t, cfg.Purpose.IsEnabled())
	assert.False(t, cfg.Purpose.IsRequired())
	assert.Equal(t, []string{"trino_query", "datahub_get_*", "kind:mcp"}, cfg.Purpose.Tools)
}

// TestBuildPurposeResolver proves the toolargs seam turns config into the
// resolver the facade wires: the default set gates the data-access tools, an
// override replaces it, and disabling the feature yields the nil no-op both the
// tool-call middleware and the schema decorator accept.
func TestBuildPurposeResolver(t *testing.T) {
	t.Run("default set", func(t *testing.T) {
		r := toolargs.BuildPurposeResolver(PurposeConfig{}, registry.NewRegistry())
		require.NotNil(t, r)
		assert.True(t, r.Gates("trino_query"))
		assert.False(t, r.Gates("platform_info"))
	})

	t.Run("configured set replaces the default", func(t *testing.T) {
		r := toolargs.BuildPurposeResolver(
			PurposeConfig{Tools: []string{"s3_*"}}, registry.NewRegistry())
		require.NotNil(t, r)
		assert.True(t, r.Gates("s3_object"))
		assert.False(t, r.Gates("trino_query"))
	})

	t.Run("disabled yields the nil no-op", func(t *testing.T) {
		assert.Nil(t, toolargs.BuildPurposeResolver(
			PurposeConfig{Enabled: new(false)}, registry.NewRegistry()))
	})
}

// TestPurposeSchemaRegistration proves the chain entry through a real server:
// with purpose enabled a gated tool's advertised schema carries the argument,
// and with it disabled the same tool's schema does not.
func TestPurposeSchemaRegistration(t *testing.T) {
	advertisesPurpose := func(t *testing.T, cfg PurposeConfig) bool {
		t.Helper()
		p := &Platform{
			config:          &Config{Purpose: cfg},
			toolkitRegistry: registry.NewRegistry(),
			mcpServer:       mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil),
		}
		mcp.AddTool(p.mcpServer, &mcp.Tool{Name: "trino_query", Description: "query"},
			func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{}, nil, nil
			})
		// Drive the canonical chain's own registration for this middleware, so
		// the test exercises the wiring the platform ships rather than a copy.
		for _, spec := range p.receivingMiddlewareChain() {
			if spec.Name == mwPurposeSchema {
				spec.Register()
			}
		}

		ctx := t.Context()
		t1, t2 := mcp.NewInMemoryTransports()
		_, err := p.mcpServer.Connect(ctx, t1, nil)
		require.NoError(t, err)
		sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
		require.NoError(t, err)
		defer func() { _ = sess.Close() }()

		listed, err := sess.ListTools(ctx, nil)
		require.NoError(t, err)
		require.Len(t, listed.Tools, 1)
		schema, ok := listed.Tools[0].InputSchema.(map[string]any)
		require.True(t, ok, "the schema round-trips to the client as a map")
		props, _ := schema["properties"].(map[string]any)
		_, present := props["purpose"]
		return present
	}

	assert.True(t, advertisesPurpose(t, PurposeConfig{}), "on by default")
	assert.False(t, advertisesPurpose(t, PurposeConfig{Enabled: new(false)}),
		"a disabled feature must never advertise an argument the platform does not consume")
}

// TestDefaultPurposeToolsCoversTheDocumentedSet keeps the middleware default and
// the set named in docs/server/configuration.md from drifting apart silently.
func TestDefaultPurposeToolsCoversTheDocumentedSet(t *testing.T) {
	assert.Equal(t, []string{
		"search",
		"fetch",
		"trino_query",
		"trino_execute",
		"trino_export",
		"trino_describe_table",
		"api_invoke_endpoint",
		"api_export",
		"datahub_get_*",
		"s3_object",
		"s3_list",
		"kind:mcp",
	}, middleware.DefaultPurposeTools())
}

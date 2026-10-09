package platform

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// TestHandleInfo_ReportsTheLimits: platform_info states the deployment's
// configured caps and the keys that set them, read from the live config and
// the registered Trino toolkit, so an agent does not find them by hitting them
// (#2057).
func TestHandleInfo_ReportsTheLimits(t *testing.T) {
	tk, err := trinokit.NewMulti(trinokit.MultiConfig{DefaultConnection: "warehouse", Instances: map[string]trinokit.Config{"warehouse": {
		Host: "localhost", User: "test", DefaultLimit: 500, MaxLimit: 5000, Timeout: 90 * time.Second,
	}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	// trino_export is listed only once it is wired, as on a deployment.
	tk.SetExportDeps(trinokit.ExportDeps{})
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))
	require.NoError(t, reg.Register(&mockToolkit{kind: "api", name: "crm", tools: []string{"api_export", "api_invoke_endpoint"}}))

	p := &Platform{
		config: &Config{
			Server:  ServerConfig{Name: "p", Version: testInfoVersion},
			Purpose: PurposeConfig{Enabled: new(false)},
			Portal:  PortalConfig{Export: PortalExportConfig{MaxRows: 250000, MaxTimeout: "20m"}},
		},
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: reg,
	}
	result, _, err := p.handleInfo(context.Background(), &mcp.CallToolRequest{})
	require.NoError(t, err)

	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var wire struct {
		Limits map[string]any `json:"limits"`
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &wire))
	exp, ok := wire.Limits["export"].(map[string]any)
	require.True(t, ok, "limits = %v", wire.Limits)
	assert.InDelta(t, 250000, exp["max_rows"], 0, "the configured row cap, not the default")
	assert.Equal(t, "portal.export.max_rows", exp["max_rows_key"])
	assert.InDelta(t, 100*1024*1024, exp["max_bytes"], 0, "an unset byte cap reports the default the tools apply")
	assert.InDelta(t, 1200, exp["max_timeout_seconds"], 0)

	query, ok := wire.Limits["query"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 500, query["default_rows"], 0)
	assert.InDelta(t, 5000, query["max_rows"], 0)
	assert.InDelta(t, 90, query["timeout_seconds"], 0)

	walk, ok := wire.Limits["page_walk"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"default_max_pages": float64(100), "max_pages": float64(10000)}, walk["api"])
	assert.NotContains(t, walk, "graphql", "no graphql tool is registered")
}

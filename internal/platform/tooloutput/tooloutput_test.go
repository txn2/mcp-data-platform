package tooloutput

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestOf(t *testing.T) {
	asset, ok := Of(ToolTrinoExport, map[string]any{"name": "big", "format": "csv"},
		map[string]any{"asset_id": "a1", "row_count": float64(9), "size_bytes": float64(120)})
	assert.True(t, ok)
	assert.Equal(t, script.RunOutput{
		Tool: ToolTrinoExport, Name: "big", Destination: script.DestinationPortal,
		AssetID: "a1", AssetVersion: 1, Format: "csv", RowCount: 9, Bytes: 120,
	}, asset)

	landed, ok := Of(ToolAPIExport, map[string]any{},
		map[string]any{"size_bytes": 5, "resource": map[string]any{
			"resource_id": "r1", "uri": "mcp://x/orders.json", "version": float64(4),
			"path": "orders.json", "size_bytes": int64(77),
		}})
	assert.True(t, ok)
	assert.Equal(t, script.RunOutput{
		Tool: ToolAPIExport, Destination: script.DestinationResources, ResourceID: "r1",
		ResourceURI: "mcp://x/orders.json", ResourceVersion: 4, Key: "orders.json", Bytes: 77,
	}, landed)

	versioned, ok := Of(ToolGraphQLExport, map[string]any{"name": "orders"},
		map[string]any{"asset_id": "a3", "asset_version": float64(4)})
	assert.True(t, ok)
	assert.Equal(t, 4, versioned.AssetVersion, "the version the tool wrote, not always the first")

	unnamed, ok := Of(ToolAPIExport, nil, map[string]any{"asset_id": "a2", "content_type": "application/json"})
	assert.True(t, ok)
	assert.Equal(t, "a2", unnamed.Name, "an unnamed export is named by its asset")

	for _, tc := range []struct {
		tool   string
		result map[string]any
	}{
		{"trino_query", map[string]any{"asset_id": "a1"}},
		{ToolTrinoExport, map[string]any{"message": "nothing written"}},
		{ToolAPIExport, map[string]any{"resource": map[string]any{}}},
	} {
		_, ok := Of(tc.tool, nil, tc.result)
		assert.False(t, ok, tc)
	}
}

// TestOfCarriesACut: an export written under on_truncation "warn" is recorded
// on the run as incomplete, whichever destination it landed in (#2057).
func TestOfCarriesACut(t *testing.T) {
	cut := map[string]any{"truncated": true, "limit_applied": float64(100000), "limit_source": "deployment", "limit_unit": "rows"}
	asset, ok := Of(ToolTrinoExport, map[string]any{"name": "contacts"}, merged(cut, map[string]any{"asset_id": "a1"}))
	assert.True(t, ok)
	assert.True(t, asset.Truncated)
	assert.Equal(t, 100000, asset.LimitApplied)
	assert.Equal(t, "deployment", asset.LimitSource)
	assert.Equal(t, "rows", asset.LimitUnit)

	landed, ok := Of(ToolAPIExport, nil, merged(cut, map[string]any{"resource": map[string]any{"resource_id": "r1"}}))
	assert.True(t, ok)
	assert.True(t, landed.Truncated)

	complete, ok := Of(ToolTrinoExport, nil, map[string]any{"asset_id": "a2", "truncated": false, "limit_applied": float64(5)})
	assert.True(t, ok)
	assert.False(t, complete.Truncated)
	assert.Zero(t, complete.LimitApplied, "a complete output records no limit")
}

func merged(a, b map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}

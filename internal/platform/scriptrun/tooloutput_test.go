package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestToolOutput(t *testing.T) {
	asset, ok := toolOutput(toolTrinoExport, map[string]any{"name": "big", "format": "csv"},
		map[string]any{"asset_id": "a1", "row_count": float64(9), "size_bytes": float64(120)})
	assert.True(t, ok)
	assert.Equal(t, script.RunOutput{
		Tool: toolTrinoExport, Name: "big", Destination: script.DestinationPortal,
		AssetID: "a1", AssetVersion: 1, Format: "csv", RowCount: 9, Bytes: 120,
	}, asset)

	landed, ok := toolOutput(toolAPIExport, map[string]any{},
		map[string]any{"size_bytes": 5, "resource": map[string]any{
			"resource_id": "r1", "uri": "mcp://x/orders.json", "version": float64(4),
			"path": "orders.json", "size_bytes": int64(77),
		}})
	assert.True(t, ok)
	assert.Equal(t, script.RunOutput{
		Tool: toolAPIExport, Destination: script.DestinationResources, ResourceID: "r1",
		ResourceURI: "mcp://x/orders.json", ResourceVersion: 4, Key: "orders.json", Bytes: 77,
	}, landed)

	versioned, ok := toolOutput(toolGraphQLExport, map[string]any{"name": "orders"},
		map[string]any{"asset_id": "a3", "asset_version": float64(4)})
	assert.True(t, ok)
	assert.Equal(t, 4, versioned.AssetVersion, "the version the tool wrote, not always the first")

	unnamed, ok := toolOutput(toolAPIExport, nil, map[string]any{"asset_id": "a2", "content_type": "application/json"})
	assert.True(t, ok)
	assert.Equal(t, "a2", unnamed.Name, "an unnamed export is named by its asset")

	for _, tc := range []struct {
		tool   string
		result map[string]any
	}{
		{"trino_query", map[string]any{"asset_id": "a1"}},
		{toolTrinoExport, map[string]any{"message": "nothing written"}},
		{toolAPIExport, map[string]any{"resource": map[string]any{}}},
	} {
		_, ok := toolOutput(tc.tool, nil, tc.result)
		assert.False(t, ok, tc)
	}
}

// toolRecordingExporter is an Exporter that also records tool outputs.
type toolRecordingExporter struct {
	Exporter
	recorded []script.RunOutput
}

func (r *toolRecordingExporter) RecordToolOutput(_ context.Context, out script.RunOutput) {
	r.recorded = append(r.recorded, out)
}

// exportCaller answers trino_export with the asset it created.
type exportCaller struct{}

func (exportCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{"asset_id": "a1", "format": "csv", "row_count": float64(3), "size_bytes": float64(40)}, nil
}

// TestRecordToolOutput_ReachesTheRunsWriter: an export tool called with
// platform.call is handed to the run's writer as an output; a writer that
// cannot record (a draft's preview) is left alone.
func TestRecordToolOutput_ReachesTheRunsWriter(t *testing.T) {
	src := `platform.call("trino_export", {"sql": "SELECT 1", "name": "big", "format": "csv"})` + "\n"
	rec := &toolRecordingExporter{}
	opts := RunLimits(PlatformLimits{})
	opts.Source, opts.Name, opts.Caller, opts.Exporter = src, "test", exportCaller{}, rec
	_, err := Run(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, rec.recorded, 1)
	assert.Equal(t, "a1", rec.recorded[0].AssetID)

	opts.Exporter = nil
	_, err = Run(context.Background(), opts)
	require.NoError(t, err, "no writer to record on is not a failure")

	h := &hostState{opts: Options{Exporter: rec}, ctx: context.Background()}
	h.recordToolOutput("trino_query", nil, map[string]any{"asset_id": "a2"})
	assert.Len(t, rec.recorded, 1, "a tool that is not an export tool writes no output")
}

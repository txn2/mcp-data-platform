package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

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

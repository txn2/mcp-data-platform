package scriptrun

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Tools whose write inside a run is an output of the run (#1854). They are the
// right tools once a result is past a run's row cap, so the report a run
// produces that way is its output as much as a platform.export would be.
const (
	toolTrinoExport   = "trino_export"
	toolAPIExport     = "api_export"
	toolGraphQLExport = "graphql_export"
)

// ToolOutputRecorder is an Exporter that also records an output a tool wrote
// on the run's behalf. The platform run's writer is one; a draft's preview
// writer has no run row to record on and is not.
type ToolOutputRecorder interface {
	RecordToolOutput(ctx context.Context, out script.RunOutput)
}

// recordToolOutput notes what an export tool called with platform.call wrote,
// so the run lists it under outputs beside what platform.export wrote.
func (h *hostState) recordToolOutput(tool string, args, result map[string]any) {
	rec, ok := h.opts.Exporter.(ToolOutputRecorder)
	if !ok {
		return
	}
	if out, ok := toolOutput(tool, args, result); ok {
		rec.RecordToolOutput(h.ctx, out)
	}
}

// toolOutput reads the output an export tool reported, from the tool's own
// result: the managed resource it landed in, or the portal asset it created.
// Anything else -- another tool, or a result naming neither -- is not one.
func toolOutput(tool string, args, result map[string]any) (script.RunOutput, bool) {
	if tool != toolTrinoExport && tool != toolAPIExport && tool != toolGraphQLExport {
		return script.RunOutput{}, false
	}
	out := script.RunOutput{
		Tool:     tool,
		Name:     stringField(args, "name"),
		Format:   stringField(result, "format"),
		RowCount: intField(result, "row_count"),
		Bytes:    intField(result, "size_bytes"),
	}
	if out.Format == "" {
		out.Format = stringField(args, "format")
	}
	if landing, ok := result["resource"].(map[string]any); ok {
		out.Destination = script.DestinationResources
		out.ResourceID = stringField(landing, "resource_id")
		out.ResourceURI = stringField(landing, "uri")
		out.ResourceVersion = intField(landing, "version")
		out.Key = stringField(landing, "path")
		out.Bytes = intField(landing, "size_bytes")
		return out, out.ResourceID != ""
	}
	out.AssetID = stringField(result, "asset_id")
	if out.AssetID == "" {
		return script.RunOutput{}, false
	}
	// A named export inside a run writes the next version of the script's
	// asset for that name and says which; a tool answering without a
	// version wrote a new asset's first.
	out.Destination, out.AssetVersion = script.DestinationPortal, max(intField(result, "asset_version"), 1)
	if out.Name == "" {
		out.Name = out.AssetID
	}
	return out, true
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// intField reads a JSON number, which a decoded tool result carries as a
// float64.
func intField(m map[string]any, key string) int {
	switch n := m[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

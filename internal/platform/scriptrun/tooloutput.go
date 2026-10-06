package scriptrun

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/tooloutput"
	"github.com/txn2/mcp-data-platform/pkg/script"
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
	if out, ok := tooloutput.Of(tool, args, result); ok {
		rec.RecordToolOutput(h.ctx, out)
	}
}

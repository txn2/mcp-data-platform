package scriptlayer

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptcontract"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexamples"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptpace"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstmt"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// DialectContract is the dialect contract (internal/platform/scriptcontract).
const DialectContract = scriptcontract.Dialect

// handleHelp returns the dialect contract, the capability surface, the example
// names, and the built-in pages that carry the reasoning the contract states
// only in outline.
func (h *Handle) handleHelp(_ context.Context, _ manageScriptInput) (*mcp.CallToolResult, any, error) {
	names := make([]map[string]any, 0, len(scriptexamples.All))
	for _, ex := range scriptexamples.All {
		names = append(names, map[string]any{fieldName: ex.Name, "description": ex.Description})
	}
	return jsonResult(map[string]any{
		"dialect":      DialectContract,
		"capabilities": scriptrun.Capabilities,
		"limits": map[string]any{
			"draft_max_steps":      scriptrun.DraftMaxSteps,
			"draft_timeout":        scriptrun.DraftTimeout.String(),
			"draft_max_rows":       scriptrun.DraftMaxRows,
			"run_max_steps":        h.runLimits.MaxSteps,
			"run_timeout":          h.runLimits.Timeout.String(),
			"run_max_rows":         h.runLimits.MaxRows,
			"run_result_bytes":     h.runLimits.ResultMaxBytes,
			"run_max_memory_bytes": h.runLimits.MaxMemoryBytes,
			// How long one statement may run (#2073): the tools' default when
			// a call names no timeout_seconds, and the most it may name.
			"statement_default_timeout_seconds": int(min(scriptstmt.ToolDefaultTimeout, h.runLimits.MaxStatementTimeout).Seconds()),
			"statement_max_timeout_seconds":     int(h.runLimits.MaxStatementTimeout.Seconds()),
			"statement_max_timeout_key":         scriptstmt.MaxTimeoutKey,
			"output_max_bytes":                  scriptrun.MaxOutputBytes,
			"log_bytes":                         scriptrun.MaxLogBytes,
			"max_source_bytes":                  script.MaxSourceBytes,
			"state_bytes":                       script.MaxStateBytes,
			"sleep_max_seconds":                 scriptpace.MaxSeconds,
			"draft_max_seconds":                 int(h.longDrafts.Max() / time.Second),
			"note":                              scriptcontract.LimitsNote,
		},
		"overlap":         scriptcontract.Overlap,
		"examples":        names,
		"read_an_example": "Call get with name=" + scriptexamples.All[0].Name + " to read one.",
		"see_also":        scriptcontract.KnowledgePages,
		"read_a_page":     "Call fetch with the reference to read one in full.",
	})
}

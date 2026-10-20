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
			"note": "A draft run is bounded more tightly than a platform run; the run_ limits are the ones a " +
				"saved script meets on this deployment. A tool a run calls keeps its own ceiling as well: " +
				"a trino_export or api_export inside a run is bounded by that tool's timeout. " +
				"The platform never runs a failed run again on its own. A rate-limit refusal of a " +
				"call is not a script error: the host waits the refusal's interval within the run's " +
				"deadline and issues the call again, and the wait is written to the run's log; an " +
				"upstream's 429 (upstream_retryable) is waited on the same way, at most 3 times. " +
				"A failed run carries cause (script, upstream, transient, memory, worker_lost, platform, " +
				"state_conflict) and retryable, which is true only when running it again is expected " +
				"to succeed. run_max_memory_bytes is the memory one run may hold " +
				"(scripts.worker.max_run_memory; 0 is no budget), measured at every host call over the " +
				"values the script can still reach. A page of rows costs far more once decoded than on the " +
				"wire: a 9 MB page of JSON rows allocates about 14 times its size while it is decoded and " +
				"holds about 4.5 times it afterwards, so page the work and export each page with " +
				"platform.export(..., append=True). A run over the budget fails with " +
				"cause memory and is not retried; run_draft and a run's metrics report " +
				"peak_memory_bytes.",
		},
		"overlap": "A script's runs may overlap unless it is set to run one at a time: a run_script run can " +
			"execute beside a scheduled one, and two run_script runs beside each other. Then the state check at " +
			"save time is the only guard, and it acts after the fact: the later run's side effects stand and it " +
			"fails with state_conflict, so its cursor is not saved and the next run repeats its work. Set " +
			"exclusive=\"refuse\" with update for a script whose runs must not overlap: while any run of it is pending " +
			"or running, a scheduled fire is recorded as skipped_overlap naming that run, and run_script and a " +
			"portal run are refused naming it, with nothing queued. Set exclusive=\"queue\" to have such a run " +
			"wait instead: it stays pending until no other run of the script is running. A schedule never overlaps itself either way.",
		"examples":        names,
		"read_an_example": "Call get with name=" + scriptexamples.All[0].Name + " to read one.",
		"see_also":        scriptcontract.KnowledgePages,
		"read_a_page":     "Call fetch with the reference to read one in full.",
	})
}

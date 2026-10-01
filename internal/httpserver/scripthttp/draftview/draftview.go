// Package draftview renders what the portal's draft routes report about a
// source and a draft run of it: the gaps in a validation's lists, why a dry
// run failed in its author's terms, and the metrics and outputs it reports.
package draftview

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/exporttable"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// IncompleteNote states that a validate report's lists are known to be short,
// which is the one thing an author reading them must not miss.
func IncompleteNote(report scriptrun.Report) string {
	var gaps []string
	if report.DynamicConnections {
		gaps = append(gaps, "at least one call computes its connection instead of naming one, or passes platform.call an argument set that cannot be read from the source, so the connection list is incomplete")
	}
	if report.DynamicDestinations {
		gaps = append(gaps, "at least one platform.export call computes its destination instead of naming one, so the destination list is incomplete")
	}
	if report.DynamicRefreshTargets {
		gaps = append(gaps, "at least one platform.publish_data call computes the output name it refreshes, so the refresh-target list is incomplete")
	}
	if report.DynamicTools {
		gaps = append(gaps, "at least one platform.call computes the tool it invokes instead of naming one, so the tool list is incomplete")
	}
	if len(gaps) == 0 {
		return ""
	}
	note := strings.Join(gaps, "; and ")
	return strings.ToUpper(note[:1]) + note[1:] + "."
}

// FailureMessage separates the failures an author acts on differently: a
// script that is wrong, a script that is right but wanted to write, an
// upstream that was briefly unavailable, and a run that held too much (#1859).
func FailureMessage(refused *scriptrun.WriteRecord, cause string) string {
	switch {
	case refused != nil:
		return "The dry run stopped at a call that persists (refused_write), because a dry run does not " +
			"write. Its recording holds every call before the write, and a test answers the write with " +
			"testing.answer(tool, args, answer), so nothing is written. Run it again with allow_writes only " +
			"to write for real; it will report every write it made."
	case cause == runstate.CauseUpstream || cause == runstate.CauseTransient:
		return "The failure was outside the script: a service it called was unavailable or answered with an " +
			"error, or the script reported it as temporary. Dry-run it again in a moment."
	case cause == runstate.CauseMemory:
		return "The dry run held more memory than a run is allowed. Page the work and export each page " +
			"with platform.export(..., append=True), and keep only what the next page needs."
	default:
		return "The script raised this failure. If it reacted to something outside the script, dry-run it " +
			"again in a moment; if it fails the same way again, fix the script and dry-run it again."
	}
}

// Metrics projects the engine's result into the metrics shape every other
// run surface reports, so a draft's cost is read in the same units as a
// platform run's.
func Metrics(result *scriptrun.Result) script.RunMetrics {
	return script.RunMetrics{
		Steps:      result.Steps,
		DurationMS: result.Duration.Milliseconds(),
		Queries:    result.Queries,
		Exports:    len(result.Exports),
		// What the draft was measured holding at its peak (#1861).
		PeakMemoryBytes: result.PeakMemory,
	}
}

// Outputs is the shape of what the run would have written. Every entry is
// a preview by construction, so the locators a persisted output carries are
// absent rather than empty.
func Outputs(outcome *scriptdraft.Outcome) []script.DryRunOutput {
	out := []script.DryRunOutput{}
	if outcome.Result == nil {
		return out
	}
	for _, e := range outcome.Result.Exports {
		o := script.DryRunOutput{
			Name: e.Name, Destination: e.Destination, Format: e.Format,
			RowCount: e.RowCount, Document: e.Document, Refresh: e.Refresh, Bytes: e.Bytes,
			Written: !e.Preview,
		}
		if o.Written {
			o.Reference = exporttable.Reference(e.ResourceRef, e.AssetID)
		}
		if e.Table != nil {
			o.Table = e.Table.QueryTable
		}
		out = append(out, o)
	}
	return out
}

// Response is one draft execution as the editor reports it. A failed run
// answers with the same fields a successful one does: the log is the whole
// reason to have run it.
type Response struct {
	RunID  string `json:"run_id" example:"run_a1b2c3d4"`
	Status string `json:"status" example:"succeeded"`
	Error  string `json:"error,omitempty"`
	// Log is what the run printed, bounded when it was captured.
	Log          string                `json:"log,omitempty"`
	LogTruncated bool                  `json:"log_truncated,omitempty"`
	Metrics      script.RunMetrics     `json:"metrics"`
	Outputs      []script.DryRunOutput `json:"outputs"`
	// State is the object the source would have saved with
	// platform.save_state, absent when it saved none (#1537). The draft
	// persists it no more than it persists an output.
	State map[string]any `json:"state,omitempty"`
	// StateCheckpoint is true when State is the run's last
	// platform.checkpoint rather than a save_state (#2003).
	StateCheckpoint bool `json:"state_checkpoint,omitempty"`
	// StateDiscarded is the save_state of a draft that failed, which a
	// platform run would discard (#2002); absent otherwise.
	StateDiscarded map[string]any `json:"state_discarded,omitempty"`
	// Writes lists the persisting platform.call calls the run made, empty
	// unless it was run with allow_writes (#1664). Those calls landed, and this
	// is the only place the response says so.
	Writes []scriptrun.WriteRecord `json:"writes"`
	// RefusedWrite is the call the write barrier stopped, absent when it
	// stopped none. At most one: the refusal ends the run.
	RefusedWrite *scriptrun.WriteRecord `json:"refused_write,omitempty"`
	// Message states what did and did not happen, because "succeeded" on a run
	// that deliberately wrote nothing is the sentence most likely to be
	// misread.
	Message string `json:"message"`
	// Recording is the run id a test replays this draft's recorded host
	// calls by, testing.replay("<recording>"), absent when none was kept
	// (#1939).
	Recording string `json:"recording,omitempty"`
}

// Of renders one executed draft.
func Of(outcome *scriptdraft.Outcome) Response {
	out := Response{
		RunID: outcome.RunID, Status: script.RunStatusSucceeded,
		Outputs: Outputs(outcome),
		Writes:  []scriptrun.WriteRecord{},
		Message: outcome.Persisted("dry run"),
	}
	if outcome.Recorded {
		out.Recording = outcome.RunID
	}
	if outcome.Result != nil {
		out.Log = outcome.Result.Log
		out.LogTruncated = outcome.Result.LogTruncated
		out.Metrics = Metrics(outcome.Result)
		out.RefusedWrite = outcome.Result.RefusedWrite
		if len(outcome.Result.Writes) > 0 {
			out.Writes = outcome.Result.Writes
		}
	}
	if st := outcome.State(); st.Committed != nil || st.Discarded != nil {
		if st.Committed != nil {
			out.State, out.StateCheckpoint = orEmpty(st.Committed), st.Checkpoint
		}
		if st.Discarded != nil {
			out.StateDiscarded = orEmpty(st.Discarded)
		}
	}
	if outcome.Failed() {
		out.Status = script.RunStatusFailed
		out.Error = outcome.Err.Error()
		out.Message = FailureMessage(out.RefusedWrite, scriptguard.Cause(outcome.Err))
	}
	return out
}

// orEmpty is v, or an empty object when v is nil, so a saved empty state
// reads as {} rather than null.
func orEmpty(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

package scriptdraft

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Report renders one draft run, successful or failed. A failed run still
// reports its log and metrics: the log is the whole reason to have run it.
func Report(sc *script.Script, outcome *Outcome) map[string]any {
	result, runErr := outcome.Result, outcome.Err
	out := map[string]any{
		"name": sc.Name, "run_id": outcome.RunID, "draft": true,
		"status": "succeeded", "queries": 0, "exports": []scriptrun.ExportRecord{},
		"writes": []scriptrun.WriteRecord{},
	}
	resultFields(out, result)
	stateFields(out, outcome)
	if outcome.Recorded {
		// What a test replays (#1939).
		out["recording"] = outcome.RunID
		out["recording_note"] = fmt.Sprintf("This draft's host calls and their answers were recorded. A test replays them "+
			"with testing.replay(%q); see command=help for how a test is written.", outcome.RunID)
	}
	if runErr != nil {
		out["status"] = "failed"
		out["error"] = runErr.Error()
		out["retryable"] = false
		out["message"] = failureMessage(result)
		if refused := refusedWriteOf(result); refused != nil {
			out["refused_write"] = refused
		}
	} else {
		out["message"] = outcome.Persisted("draft")
	}
	// A draft of a script that is not saved says so, because nothing names it
	// yet: no schedule fires it and no run_script reaches it until it is.
	if sc.ID == "" {
		out["saved"] = false
	}
	return out
}

// failureMessage states why the draft ended, separating the two failures
// an author acts on differently: a script that is wrong, and a script that is
// right but wanted to write.
func failureMessage(result *scriptrun.Result) string {
	if refused := refusedWriteOf(result); refused != nil {
		return "The draft stopped at a call that persists (refused_write), because a draft does not write. " +
			"Its recording holds every call before the write: a test replays it and answers the write with " +
			"testing.answer(tool, args, answer), so the script is tested and saved without writing anything. " +
			"Run it again with allow_writes only to write for real; it will report every write it made."
	}
	return "The script raised this failure. If it reacted to something outside the script, such as an upstream " +
		"that answered with an error, running the draft again may pass; if it fails the same way again, fix the " +
		"script and run the draft again."
}

// refusedWriteOf reads the barred call off a result, tolerating the nil result
// a run that never started leaves.
func refusedWriteOf(result *scriptrun.Result) *scriptrun.WriteRecord {
	if result == nil {
		return nil
	}
	return result.RefusedWrite
}

// orEmptyWrites normalizes a nil write slice so the response carries a list
// rather than null: a reader checking what a draft persisted must be able to
// read the empty answer as an empty list.
func orEmptyWrites(writes []scriptrun.WriteRecord) []scriptrun.WriteRecord {
	if writes == nil {
		return []scriptrun.WriteRecord{}
	}
	return writes
}

// orEmptyExports normalizes a nil export slice so the response carries a list
// rather than null.
func orEmptyExports(exports []scriptrun.ExportRecord) []scriptrun.ExportRecord {
	if exports == nil {
		return []scriptrun.ExportRecord{}
	}
	return exports
}

// orEmptyMap normalizes a nil map so the response carries an object rather
// than null.
func orEmptyMap(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

// resultFields adds what the engine reported about a draft that ran.
func resultFields(out map[string]any, result *scriptrun.Result) {
	if result == nil {
		return
	}
	out["log"] = result.Log
	out["log_truncated"] = result.LogTruncated
	out["steps"] = result.Steps
	out["duration_ms"] = result.Duration.Milliseconds()
	out["queries"] = result.Queries
	if result.SlowestStatement != nil {
		out["slowest_statement"] = result.SlowestStatement
	}
	// How close the draft came to the memory budget, before a scheduled
	// run finds out (#1861).
	out["peak_memory_bytes"] = result.PeakMemory
	out["exports"] = orEmptyExports(result.Exports)
	out["writes"] = orEmptyWrites(result.Writes)
	// A draft reports what a platform run would hand back and its last
	// progress report, the same fields get_run carries (#1845, #1847).
	if result.Return != nil {
		out["result"] = result.Return
	}
	if result.Progress != nil {
		out["progress"] = result.Progress
	}
}

// stateFields adds what a platform run would commit, and a failed run's
// save_state that it would discard rather than save (#2002, #2003).
func stateFields(out map[string]any, outcome *Outcome) {
	if st := outcome.State(); st.Committed != nil || st.Discarded != nil {
		if st.Committed != nil {
			out["state"] = orEmptyMap(st.Committed)
		}
		if st.Checkpoint {
			out["state_checkpoint"] = true
		}
		if st.Discarded != nil {
			out["state_discarded"] = orEmptyMap(st.Discarded)
		}
	}
}

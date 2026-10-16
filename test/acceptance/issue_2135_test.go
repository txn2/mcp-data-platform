//go:build integration

package acceptance

import (
	"fmt"
	"testing"
	"time"
)

// Issue #2135: a failing_automations entry said nothing about whether a newer
// run of the automation was under way, so a monitor could not tell "still
// broken" from "a run that may fix it is executing". These criteria fail an
// automation, read the entry, start a run held open by the mcp-test
// fixture's slow tool, read the entry while that run executes, cancel it, and
// read the entry once more.
//
// Wire forms: platform_info takes no parameters and is called with none, by
// the connect that opens each session. manage_script's command, name,
// description, source and run_id are strings and its params a list of
// objects; run_script's name is a string, its args an object of a boolean
// and two integers, and wait_seconds an integer (negative to queue and
// return).

func TestIssue2135_AFailingAutomationSaysWhetherANewerRunIsUnderWay(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2135-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #2135: an automation that fails, then runs again held open.",
		"params": []any{
			map[string]any{"name": "fail", "type": "bool", "required": true, "description": "Whether the run fails at once."},
			map[string]any{"name": "hold_ms", "type": "int", "required": true, "description": "How long each slow call lasts."},
			map[string]any{"name": "holds", "type": "int", "required": true, "description": "How many slow calls the run makes."},
		},
		"source": `def main():
    """Fails when asked to; otherwise holds the run open for holds slow calls."""
    if run.params["fail"]:
        fail("broken")
    for _ in range(run.params["holds"]):
        platform.call("` + slowTool1986 + `", {"milliseconds": run.params["hold_ms"]})
`,
	}, map[string]any{"fail": false, "hold_ms": 10, "holds": 1})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	failed := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"fail": true, "hold_ms": 10, "holds": 0}, "wait_seconds": 60,
	})
	if failed["status"] != "failed" {
		t.Fatalf("the run did not fail: %v", failed)
	}
	entry := issue1934Failing(connect(t).info, name)
	if entry == nil {
		t.Fatalf("the failing automation is not briefed")
	}
	latest, _ := entry["latest_run"].(map[string]any)
	if latest["run_id"] != failed["run_id"] || latest["status"] != "failed" || entry["superseded_by_running"] != false {
		t.Fatalf("with no newer run, latest_run is not the failed run: %v", entry)
	}

	held := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"fail": false, "hold_ms": holdMS1986, "holds": holds1986}, "wait_seconds": -1,
	})
	runID, _ := held["run_id"].(string)
	if runID == "" {
		t.Fatalf("the queued run carries no id: %v", held)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": runID}) })
	await1986(t, c, runID, "running")

	open := issue1934Failing(connect(t).info, name)
	if open == nil {
		t.Fatalf("the automation left the briefing while its new run executes")
	}
	latest, _ = open["latest_run"].(map[string]any)
	if latest["run_id"] != runID || latest["status"] != "running" || latest["started_at"] == nil ||
		open["superseded_by_running"] != true {
		t.Fatalf("with a newer run executing, the entry does not say so: %v", open)
	}
	if open["run_id"] != failed["run_id"] {
		t.Errorf("the entry no longer names the failed run: %v", open)
	}

	c.call("manage_script", map[string]any{"command": "cancel_run", "run_id": runID})
	waitFor(t, "the canceled run to end", func() (bool, string) {
		e := issue1934Failing(connect(t).info, name)
		if e == nil {
			return false, "not briefed"
		}
		l, _ := e["latest_run"].(map[string]any)
		return l["run_id"] == runID && l["status"] != "running" && e["superseded_by_running"] == false, fmt.Sprintf("%v", e)
	})
}

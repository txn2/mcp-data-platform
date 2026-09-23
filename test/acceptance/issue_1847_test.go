//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1847: a running script was a black box until it ended: no progress,
// no log until the end, and no way to stop it.
//
// What these hold, against the running platform: a script's
// platform.progress("step", done=3, total=10) shows on its running run, with
// the log printed so far, through get_run and GET .../runs/{runID}; a running
// run canceled over HTTP ends canceled within seconds and frees its worker; a
// run canceled with manage_script cancel_run before a worker executes it never
// starts, or, if a worker got it first, ends canceled the same way; and
// canceling a finished run changes nothing and says so.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` and
// `run_id` are typed string and `params` an array of objects; run_script's
// `args` is an object and `wait_seconds` an integer; the cancel route takes no
// body. Each is sent in that one form.

// slowSource1847 reports progress between queries, each of which waits on
// Trino, so the run is observable while it executes.
var slowSource1847 = fmt.Sprintf(`total = run.params["steps"]
for i in range(total):
    print("step", i + 1)
    platform.progress("step", done=i + 1, total=total)
    platform.query(connection=%q, sql="SELECT count(*) AS n FROM UNNEST(sequence(1, 10000)) AS a(x) CROSS JOIN UNNEST(sequence(1, 100)) AS b(y)")
`, scratchResourceConnection)

// save1847 saves the slow script and returns its name and id.
func save1847(t *testing.T, c *client, tag string) (name, id string) {
	t.Helper()
	name = fmt.Sprintf("acc-1847-%s-%d", tag, time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": slowSource1847,
		"description": "Acceptance #1847: progress, live log and cancel.",
		"params":      []any{map[string]any{"name": "steps", "type": "int", "required": true}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ = got["id"].(string)
	return name, id
}

// until1847 re-reads a run over HTTP until ok says so or the time runs out.
func until1847(t *testing.T, c *client, scriptID, runID string, within time.Duration, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		_, run := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/runs/"+runID, http.NoBody)
		if ok(run) {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach the state within %s: %v", runID, within, run)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestIssue1847_ProgressAndTheLogSoFarShowOnARunningRun(t *testing.T) {
	c := connect(t)
	name, id := save1847(t, c, "progress")
	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"steps": 400}, "wait_seconds": -1})
	runID, _ := out["run_id"].(string)

	live := until1847(t, c, id, runID, 60*time.Second, func(run map[string]any) bool {
		p, _ := run["progress"].(map[string]any)
		done, _ := p["done"].(float64)
		return run["status"] == "running" && done >= 3
	})
	progress, _ := live["progress"].(map[string]any)
	if progress["message"] != "step" || progress["total"] != float64(400) {
		t.Errorf("progress = %v; want step of 400", progress)
	}
	if log, _ := live["log"].(string); !strings.Contains(log, "step 1\n") {
		t.Errorf("the running run's log so far = %q; want the lines already printed", log)
	}
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
	if p, _ := got["progress"].(map[string]any); p["total"] != float64(400) {
		t.Errorf("get_run progress = %v; want the same report", got["progress"])
	}
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": runID})
}

func TestIssue1847_CancelingARunningRunEndsItWithinSeconds(t *testing.T) {
	c := connect(t)
	name, id := save1847(t, c, "stop")
	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"steps": 200}, "wait_seconds": -1})
	runID, _ := out["run_id"].(string)
	until1847(t, c, id, runID, 60*time.Second, func(run map[string]any) bool { return run["status"] == "running" })

	status, body := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs/"+runID+"/cancel", http.NoBody)
	if status != http.StatusOK || body["outcome"] != "requested" {
		t.Fatalf("cancel answered %d %v; want 200 requested", status, body)
	}
	asked := time.Now()
	ended := until1847(t, c, id, runID, 20*time.Second, func(run map[string]any) bool { return run["status"] == "canceled" })
	t.Logf("canceled %s after the request", time.Since(asked).Round(time.Millisecond))
	if msg, _ := ended["error"].(string); !strings.HasPrefix(msg, "canceled by ") {
		t.Errorf("error = %q; want it to name who canceled", msg)
	}
	if p, _ := ended["progress"].(map[string]any); p["done"] == float64(200) {
		t.Errorf("the run finished all its steps; it was not stopped: %v", p)
	}
}

func TestIssue1847_CancelRunBeforeAWorkerExecutesItStopsIt(t *testing.T) {
	c := connect(t)
	name, id := save1847(t, c, "queued")
	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"steps": 200}, "wait_seconds": -1})
	runID, _ := out["run_id"].(string)
	canceled := c.call("manage_script", map[string]any{"command": "cancel_run", "run_id": runID})
	outcome, _ := canceled["outcome"].(string)
	t.Logf("cancel_run outcome: %s", outcome)
	ended := until1847(t, c, id, runID, 20*time.Second, func(run map[string]any) bool { return run["status"] == "canceled" })
	switch outcome {
	case "canceled":
		if ended["started_at"] != nil {
			t.Errorf("a run canceled while queued started: %v", ended)
		}
	case "requested":
		// A worker claimed it between the queue and the request, which the
		// notification that wakes the worker makes the usual order.
	default:
		t.Errorf("outcome = %q; want canceled or requested", outcome)
	}
}

func TestIssue1847_CancelingAFinishedRunChangesNothing(t *testing.T) {
	c := connect(t)
	name, id := save1847(t, c, "done")
	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"steps": 1}, "wait_seconds": 120})
	if out["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", out)
	}
	runID, _ := out["run_id"].(string)
	status, body := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs/"+runID+"/cancel", http.NoBody)
	if status != http.StatusOK || body["outcome"] != "already_finished" {
		t.Fatalf("cancel answered %d %v; want 200 already_finished", status, body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "already finished (succeeded)") {
		t.Errorf("message = %q; want it to say the run had already finished", msg)
	}
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
	if got["status"] != "succeeded" {
		t.Errorf("status = %v; a finished run must be left as it was", got["status"])
	}
}

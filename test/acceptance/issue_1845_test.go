//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1845: a script could only be run fire-and-forget over HTTP, and no
// run could hand data back: a small answer meant writing an asset and a second
// request to read it.
//
// What these hold, against the running platform: platform.result hands a JSON
// value back; POST /api/v1/portal/scripts/{id}/runs?wait=30 answers 200 with
// the finished run carrying it; without wait the route answers 202 and
// GET .../runs/{runID} later carries the same result; run_script and get_run
// carry it; and an oversized or non-JSON value fails the run with the reason.
//
// Wire forms: the run route's body is a JSON object with `params` an object,
// its one form, and `wait` is a query-string integer. manage_script's
// `command`, `name`, `description`, `source` and `run_id` are typed string and
// `params` an array of objects; run_script's `args` is an object and
// `wait_seconds` an integer. Each is sent in that one form.

// save1845 saves source as a script and returns its id.
func save1845(t *testing.T, c *client, name, source string) string {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1845: a run hands a value back.",
		"params":      []any{map[string]any{"name": "n", "type": "int", "required": true}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("manage_script get returned no id: %v", got)
	}
	return id
}

// resultSource1845 returns the doubled parameter as its result.
const resultSource1845 = `platform.result({"total": run.params["n"] * 2, "label": "doubled"})` + "\n"

func TestIssue1845_AWaitingRequestGetsTheResultOfARunThatFinishesInTime(t *testing.T) {
	c := connect(t)
	id := save1845(t, c, fmt.Sprintf("acc-1845-wait-%d", time.Now().UnixNano()), resultSource1845)
	status, body := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs?wait=30",
		jsonBody(t, map[string]any{"params": map[string]any{"n": 21}}))
	if status != http.StatusOK {
		t.Fatalf("status = %d; want 200 for a run that finished inside the wait: %v", status, body)
	}
	if body["status"] != "succeeded" {
		t.Errorf("the run is %v; want succeeded", body["status"])
	}
	result, _ := body["result"].(map[string]any)
	if result["total"] != float64(42) || result["label"] != "doubled" {
		t.Errorf("result = %v; want total 42, label doubled", body["result"])
	}
}

func TestIssue1845_WithoutWaitTheRunIsQueuedAndItsResultReadLater(t *testing.T) {
	c := connect(t)
	id := save1845(t, c, fmt.Sprintf("acc-1845-async-%d", time.Now().UnixNano()), resultSource1845)
	status, body := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs",
		jsonBody(t, map[string]any{"params": map[string]any{"n": 21}}))
	if status != http.StatusAccepted {
		t.Fatalf("status = %d; want 202 without wait: %v", status, body)
	}
	runID, _ := body["run_id"].(string)
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, run := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/runs/"+runID, http.NoBody)
		if run["status"] == "succeeded" {
			result, _ := run["result"].(map[string]any)
			if result["total"] != float64(42) {
				t.Errorf("result = %v; want total 42", run["result"])
			}
			return
		}
		if time.Now().After(deadline) || run["status"] == "failed" {
			t.Fatalf("the run did not succeed: %v", run)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func TestIssue1845_RunScriptAndGetRunCarryTheResult(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1845-mcp-%d", time.Now().UnixNano())
	save1845(t, c, name, resultSource1845)
	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"n": 5}, "wait_seconds": 120})
	result, _ := out["result"].(map[string]any)
	if result["total"] != float64(10) {
		t.Fatalf("run_script result = %v; want total 10", out["result"])
	}
	runID, _ := out["run_id"].(string)
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
	if again, _ := got["result"].(map[string]any); again["total"] != float64(10) {
		t.Errorf("get_run result = %v; want total 10", got["result"])
	}
}

func TestIssue1845_AnOversizedOrNonJSONResultFailsTheRun(t *testing.T) {
	c := connect(t)
	for _, tc := range []struct{ source, want string }{
		{`platform.result("x" * (run.params["n"] * 1024 * 1024))` + "\n", "over the 1048576-byte cap"},
		{`platform.result(lambda: run.params["n"])` + "\n", "cannot be a JSON result"},
	} {
		name := fmt.Sprintf("acc-1845-bad-%d", time.Now().UnixNano())
		save1845(t, c, name, tc.source)
		out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"n": 2}, "wait_seconds": 120})
		if out["status"] != "failed" {
			t.Errorf("%s: status = %v; want failed", tc.want, out["status"])
			continue
		}
		if msg, _ := out["error"].(string); !strings.Contains(msg, tc.want) {
			t.Errorf("error = %q; want it to say %q", msg, tc.want)
		}
	}
}

//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1867: max_run_memory was enforced only when the full walk of what a
// run held ran, at most once a second, so a run whose script built its values
// faster than that went far past its budget and succeeded, and a run that
// ended over its budget reported a peak above it on a successful run.
//
// What these hold, against the running platform (dev/platform.yaml sets
// scripts.worker.max_run_memory to 128MiB): the ticket's own shape -- a list
// built inside a function, 8 MiB a step, a fast host call after each -- fails
// with cause memory at the host call where it crossed the budget, saved and as
// a draft; and a script that builds past the budget after its last host call
// fails when it ends instead of succeeding.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`
// are typed strings; run_script's `name` a string and `wait_seconds` an
// integer. Each is sent in that one form.

// grow1867 is the ticket's reproduction, verbatim but for the connection.
func grow1867() string {
	return fmt.Sprintf(`def grow():
    held = []
    for i in range(34):
        held.append("x" * (8 * 1024 * 1024))
        platform.query("SELECT 1 AS one", connection=%q)
    return len(held)

platform.result({"held_mib": grow() * 8})
`, scratchResourceConnection)
}

// script1867 saves source under a fresh name and returns the name.
func script1867(t *testing.T, c *client, what, source string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1867-%s-%d", what, time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1867: " + what,
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name
}

// TestIssue1867_ARunThatGrowsFasterThanAWalkFails is the ticket's row 3: the
// list is local to a function and every call returns at once, so the clock
// alone would not walk it in time.
func TestIssue1867_ARunThatGrowsFasterThanAWalkFails(t *testing.T) {
	c := connect(t)
	name := script1867(t, c, "grow", grow1867())

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 90})
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	if got["status"] != "failed" || got["cause"] != "memory" {
		t.Fatalf("a run holding 272 MiB under a 128 MiB budget: status %v cause %v; want failed, memory: %v",
			got["status"], got["cause"], got)
	}
	errText, _ := got["error"].(string)
	for _, want := range []string{"in platform.query", "128 MiB memory budget"} {
		if !strings.Contains(errText, want) {
			t.Errorf("error %q does not name %q", errText, want)
		}
	}
	metrics, _ := got["metrics"].(map[string]any)
	peak, _ := metrics["peak_memory_bytes"].(float64)
	if peak <= 128<<20 || peak > 200<<20 {
		t.Errorf("peak_memory_bytes = %.0f; want a measured peak just past 128 MiB, where the run was stopped", peak)
	}
}

// TestIssue1867_ADraftThatGrowsFasterThanAWalkFails holds the same for a draft
// run, which meets the same budget.
func TestIssue1867_ADraftThatGrowsFasterThanAWalkFails(t *testing.T) {
	c := connect(t)
	source := grow1867()
	name := script1867(t, c, "draft", source)

	res, text, err := c.callRaw("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "memory budget") || !strings.Contains(text, "platform.query") {
		t.Errorf("run_draft of 272 MiB under a 128 MiB budget: error=%v %s", res.IsError, text)
	}
}

// TestIssue1867_ARunThatEndsOverItsBudgetFails is the ticket's rows 1 and 2
// taken to their end: nothing is measured after the last host call except what
// the script ends holding, and that is held to the budget too.
func TestIssue1867_ARunThatEndsOverItsBudgetFails(t *testing.T) {
	c := connect(t)
	name := script1867(t, c, "ends", fmt.Sprintf(`platform.query("SELECT 1 AS one", connection=%q)
held = ["x" * (8 * 1024 * 1024) + str(i) for i in range(24)]
print(len(held))
`, scratchResourceConnection))

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 90})
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	if got["status"] != "failed" || got["cause"] != "memory" {
		t.Fatalf("a run ending with 192 MiB under a 128 MiB budget: status %v cause %v; want failed, memory: %v",
			got["status"], got["cause"], got)
	}
	if errText, _ := got["error"].(string); !strings.Contains(errText, "in the code after its last host call") {
		t.Errorf("error %q does not say it was measured when the script ended", errText)
	}
}

//go:build integration

package acceptance

import (
	"fmt"
	"testing"
	"time"
)

// Issue #2004: platform.remaining_ms is the time a run has left before its
// deadline, and a test sets it to take the out-of-time branch. These criteria
// draft a script that reads it, run it on the dev stack's worker, and save one
// whose test sets it.
//
// Wire forms: manage_script's command, name and source, and run_script's name,
// are typed strings, and wait_seconds a typed integer; each is sent as the one
// JSON form it admits. testing.set_run's remaining_ms is Starlark inside
// source, not a tool parameter.

const source2004 = `def main():
    """Stops early when less than a second is left."""
    left = platform.remaining_ms()
    if left < 1000:
        platform.result({"stopped": True, "left": left})
        return
    platform.result({"stopped": False, "left": left})
`

// left2004 is the remaining_ms a run handed back.
func left2004(t *testing.T, out map[string]any) float64 {
	t.Helper()
	result, _ := out["result"].(map[string]any)
	left, ok := result["left"].(float64)
	if !ok || result["stopped"] != false {
		t.Fatalf("the run did not report the time it had left: %v", out)
	}
	return left
}

func TestIssue2004_RemainingMSIsTheTimeLeftBeforeTheDeadline(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	draft := owner.call("manage_script", map[string]any{
		"command": "run_draft", "name": fmt.Sprintf("acc-2004-draft-%d", time.Now().UnixNano()), "source": source2004,
	})
	if left := left2004(t, draft); left <= 0 || left > float64(10*time.Minute/time.Millisecond) {
		t.Errorf("a draft has %v ms left; want a positive time within the draft's limit", left)
	}

	name := fmt.Sprintf("acc-2004-run-%d", time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2004: reads the time left.", "source": source2004 + `
def test_out_of_time():
    """With ten milliseconds left the run stops early."""
    testing.set_run(remaining_ms = 10)
    main()
    assert.eq(testing.outputs().result, {"stopped": True, "left": 10})
`,
	}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	// The dev stack's run_timeout is the 15m default.
	if left := left2004(t, run); left <= 0 || left > float64(15*time.Minute/time.Millisecond) {
		t.Errorf("a run has %v ms left; want a positive time within its run_timeout", left)
	}
}

func TestIssue2004_ATestTakesTheOutOfTimeBranch(t *testing.T) {
	c := connect(t)
	out := c.call("manage_script", map[string]any{
		"command": "test", "name": fmt.Sprintf("acc-2004-test-%d", time.Now().UnixNano()),
		"source": source2004 + `
def test_out_of_time():
    """With ten milliseconds left the run stops early."""
    testing.set_run(remaining_ms = 10)
    main()
    assert.eq(testing.outputs().result, {"stopped": True, "left": 10})
`,
	})
	if first := firstTest1939(t, out); first["passed"] != true {
		t.Fatalf("the out-of-time test did not pass: %v", first)
	}
}

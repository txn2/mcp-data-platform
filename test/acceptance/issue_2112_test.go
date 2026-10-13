//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2112: a script polls something slow with short calls and
// platform.sleep between them, instead of holding one call open until the
// upstream answers. The criteria run a script that polls the api-test
// fixture every ten seconds, bounded by range() and the time it has left,
// and test it without waiting.
//
// Wire forms: platform.sleep takes a number of seconds, an int or a float,
// both written in the Starlark source; manage_script's command, name,
// source, params and args, and run_script's name, args and wait_seconds are
// sent as their typed JSON forms.

// source2112 polls the fixture until it says the export is ready: the echo
// endpoint answers with the request it was sent, the body under "body", so the
// upstream says ready on the poll the run asks it to, and every poll is a real
// round trip.
const source2112 = `def poll(attempt, ready_after):
    """Asks the upstream once whether the export is ready."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture",
        "method": "POST",
        "path": "/v1/echo",
        "body": {"state": "ready" if attempt + 1 >= ready_after else "building"},
    })
    return res["body"]["body"]["state"]

def main():
    """Polls the export every ten seconds for up to five minutes."""
    ready_after = run.params["ready_after"]
    start = platform.remaining_ms()
    polls = 0
    for attempt in range(30):
        polls += 1
        if poll(attempt, ready_after) == "ready":
            break
        if platform.remaining_ms() < 15000:
            fail("the export was not ready in time")
        platform.sleep(10)
    platform.result({"polls": polls, "waited_ms": start - platform.remaining_ms()})
`

// test2112 is the test a person writes for the polling path: the upstream
// says building twice, then ready.
const test2112 = `
def test_polls_until_ready():
    """Three polls, two waits, no time spent."""
    testing.set_run(params = {"ready_after": 3}, remaining_ms = 300000)
    testing.answers("api_invoke_endpoint", {"path": "/v1/echo"}, [
        {"status": 200, "body": {"body": {"state": "building"}}},
        {"status": 200, "body": {"body": {"state": "building"}}},
        {"status": 200, "body": {"body": {"state": "ready"}}},
    ])
    main()
    assert.eq(testing.outputs().result["polls"], 3)
`

func params2112() []any {
	return []any{map[string]any{
		"name": "ready_after", "type": "int", "required": true,
		"description": "The poll on which the upstream says ready.",
	}}
}

// TestIssue2112_AScriptPollsEveryTenSecondsWithShortCalls: a run polls the
// upstream three times, waiting ten seconds between polls, and finishes.
func TestIssue2112_AScriptPollsEveryTenSecondsWithShortCalls(t *testing.T) {
	c := connectFor(t, 8*time.Minute)
	name := fmt.Sprintf("acc-2112-poll-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2112: polls a slow export.",
		"source": source2112 + test2112, "params": params2112(),
	}, map[string]any{"ready_after": 1})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	started := time.Now()
	run := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"ready_after": 3}, "wait_seconds": 300})
	took := time.Since(started)
	if run["status"] != "succeeded" {
		t.Fatalf("the polling run did not succeed: %v", run)
	}
	result, _ := run["result"].(map[string]any)
	if polls, _ := result["polls"].(float64); polls != 3 {
		t.Errorf("the run polled %v times; the upstream said ready on the third", result["polls"])
	}
	if waited, _ := result["waited_ms"].(float64); waited < 20000 {
		t.Errorf("the run's clock moved %vms across two ten-second waits", waited)
	}
	if took < 20*time.Second {
		t.Errorf("the run finished in %s; two ten-second waits cannot", took)
	}
	t.Logf("the run polled %v times in %s (its clock: %vms)", result["polls"], took, result["waited_ms"])
}

// TestIssue2112_ATestOfThePollingScriptRunsInstantly: the test of that script
// passes in no time, since a test does not wait.
func TestIssue2112_ATestOfThePollingScriptRunsInstantly(t *testing.T) {
	c := connect(t)
	started := time.Now()
	out := c.call("manage_script", map[string]any{
		"command": "test", "name": fmt.Sprintf("acc-2112-test-%d", time.Now().UnixNano()),
		"source": source2112 + test2112, "params": params2112(),
	})
	took := time.Since(started)
	if first := firstTest1939(t, out); first["passed"] != true {
		t.Fatalf("the polling test did not pass: %v", out)
	}
	if took > 10*time.Second {
		t.Errorf("the test took %s; its two ten-second sleeps must not wait", took)
	}
	t.Logf("the polling test ran in %s", took)
}

// TestIssue2112_ASleepIsBounded: a sleep longer than a minute, or one past
// the run's deadline, is refused before it starts; a fraction of a second is
// a sleep like any other.
func TestIssue2112_ASleepIsBounded(t *testing.T) {
	c := connect(t)
	draft := func(body string) map[string]any {
		return c.call("manage_script", map[string]any{
			"command": "run_draft", "name": fmt.Sprintf("acc-2112-bound-%d", time.Now().UnixNano()),
			"source": "def main():\n    \"\"\"Sleeps.\"\"\"\n" + body,
		})
	}
	if out := draft("    platform.sleep(0.5)\n    platform.result({\"slept\": True})\n"); out["status"] != "succeeded" {
		t.Errorf("a half-second sleep did not run: %v", out)
	}
	out := draft("    platform.sleep(61)\n")
	if out["status"] != "failed" || !strings.Contains(fmt.Sprint(out), "60") {
		t.Errorf("a 61-second sleep was not refused naming the bound: %v", out)
	}
	out = draft("    for i in range(2):\n        platform.sleep(40)\n")
	if out["status"] != "failed" || !strings.Contains(fmt.Sprint(out), "deadline") {
		t.Errorf("a sleep past a draft's sixty seconds was not refused naming the deadline: %v", out)
	}
}

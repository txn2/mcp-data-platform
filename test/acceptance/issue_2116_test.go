//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2116: a test of a 30-call flow declares its answers through a
// shared fake_login() helper and a few times= and list answers, and the save
// accepts a helper using testing that no run can reach while still refusing
// one a run reaches.
//
// Wire forms: testing.answer's times is a Starlark int and testing.answers'
// answers a Starlark list, both written in the source; manage_script's
// command, name, description and source are typed strings.

// flow2116 signs in, opens 27 pages, and polls an export until it is done:
// 30 calls when the export is done on the second poll.
const flow2116 = `def call(step):
    """One request of the flow."""
    return platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture",
        "method": "POST",
        "path": "/v1/echo",
        "body": {"step": step},
    })

def main():
    """Signs in, opens every page, and waits for the export."""
    call("login")
    for _ in range(27):
        call("page")
    polls = 0
    for _ in range(10):
        polls += 1
        if call("poll")["body"].get("state") == "done":
            break
    platform.result({"pages": 27, "polls": polls})
`

const helpers2116 = `
def fake_login():
    """The answers every test of the flow shares."""
    testing.answer("api_invoke_endpoint", {"body": {"step": "login"}}, {"status": 200, "body": {"step": "login"}})
    testing.answer("api_invoke_endpoint", {"body": {"step": "page"}}, {"status": 200, "body": {"step": "page"}}, times = 27)

def test_the_export_is_done_on_the_second_poll():
    """Thirty calls: sign in, 27 pages, two polls."""
    fake_login()
    testing.answers("api_invoke_endpoint", {"body": {"step": "poll"}}, [
        {"status": 200, "body": {"state": "building"}},
        {"status": 200, "body": {"state": "done"}},
    ])
    main()
    assert.eq(testing.outputs().result, {"pages": 27, "polls": 2})
    assert.eq(len(testing.outputs().calls), 30)
`

func TestIssue2116_AFlowTestDeclaresItsAnswersThroughASharedHelper(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2116-flow-%d", time.Now().UnixNano())
	out := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2116: a thirty-call flow.",
		"source": flow2116 + helpers2116,
	})
	if out["status"] != "created" {
		t.Fatalf("the flow with its shared helper was not saved: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	tested := c.call("manage_script", map[string]any{"command": "test", "name": name})
	if first := firstTest1939(t, tested); first["passed"] != true {
		t.Errorf("the flow's test did not pass: %v", tested)
	}
}

func TestIssue2116_AHelperARunReachesMayNotUseTesting(t *testing.T) {
	c := connect(t)
	reached := strings.Replace(flow2116, `    call("login")`, `    fake_login()
    call("login")`, 1)
	res, text, err := c.callRaw("manage_script", map[string]any{
		"command": "create", "name": fmt.Sprintf("acc-2116-reached-%d", time.Now().UnixNano()),
		"description": "Acceptance #2116.", "source": reached + helpers2116,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "test-module-outside-test") {
		t.Errorf("a helper using testing that main() calls was not refused: %v %s", res.IsError, text)
	}
}

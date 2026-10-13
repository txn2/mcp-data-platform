//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2117: an administrator drafts an unsaved script against a slow
// upstream past the 60 seconds a draft is held to, reads its progress with
// get_run while it runs, and turns its recording into a test without saving a
// version first. The dev stack sets scripts.draft.max_seconds to 900.
//
// Wire forms: run_draft's timeout_seconds is an integer, sent at 600, at 120
// for a non-administrator and at 901 past the cap; get_run's run_id is a
// string. The slow upstream is the api-test fixture's /v1/slow, whose ms query
// parameter is how long it takes to answer.

// slowFlow2117 opens eight pages of a slow upstream, each taking eight
// seconds (inside the api connection's ten-second call timeout), reporting
// each: about 64 seconds, past the 60 a draft is otherwise held to.
const slowFlow2117 = `def page():
    """Opens one slow page."""
    return platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture",
        "method": "GET",
        "path": "/v1/slow",
        "query_params": {"ms": 8000},
    })

def main():
    """Opens the eight pages of a slow form."""
    opened = 0
    for _ in range(8):
        res = page()
        opened += 1
        platform.progress("page %d of 8" % opened, done = opened, total = 8)
        print("page %d answered %d" % (opened, res["status"]))
    platform.result({"opened": opened})
`

func TestIssue2117_AnAdministratorDraftsASlowFlowInTheBackground(t *testing.T) {
	c := connectFor(t, 15*time.Minute)
	name := fmt.Sprintf("acc-2117-slow-%d", time.Now().UnixNano())
	started := c.call("manage_script", map[string]any{
		"command": "run_draft", "name": name, "source": slowFlow2117, "timeout_seconds": 600,
	})
	runID, _ := started["run_id"].(string)
	if started["status"] != "running" || runID == "" {
		t.Fatalf("a ten-minute draft did not answer with a running run id: %v", started)
	}

	var saw map[string]any
	deadline := time.Now().Add(8 * time.Minute)
	sawRunning := false
	for {
		got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
		if got["status"] == "running" {
			if log, _ := got["log"].(string); strings.Contains(log, "page 1 answered") {
				sawRunning = true
				t.Logf("while running: log %q progress %v", log, got["progress"])
			}
		} else {
			saw = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the draft did not end: %v", got)
		}
		time.Sleep(5 * time.Second)
	}
	if !sawRunning {
		t.Error("get_run never showed the draft's log while it ran")
	}
	if saw["status"] != "succeeded" {
		t.Fatalf("the background draft did not succeed: %v", saw)
	}
	draft, _ := saw["draft"].(map[string]any)
	recording, _ := draft["recording"].(string)
	if recording == "" {
		t.Fatalf("the finished draft carries no recording: %v", saw)
	}
	if raw, _ := json.Marshal(draft); !strings.Contains(string(raw), `"opened":8`) {
		t.Errorf("the draft's answer does not carry what it produced: %s", raw)
	}

	// Its recording becomes the test the script is saved with, with no
	// version saved before.
	withTest := slowFlow2117 + `
def test_the_slow_form():
    """The eight pages of the recorded draft open."""
    testing.replay("` + recording + `")
    main()
    assert.eq(testing.outputs().result, {"opened": 8})
`
	created := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2117: a slow form.", "source": withTest,
	})
	if created["status"] != "created" {
		t.Fatalf("the script was not saved with the test from its long draft: %v", created)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
}

func TestIssue2117_ALongDraftIsAnAdministratorsAndHeldToTheCap(t *testing.T) {
	admin := connect(t)
	res, text, err := admin.callRaw("manage_script", map[string]any{
		"command": "run_draft", "name": fmt.Sprintf("acc-2117-cap-%d", time.Now().UnixNano()),
		"source": slowFlow2117, "timeout_seconds": 901,
	})
	if err != nil || !res.IsError || !strings.Contains(text, "900") {
		t.Errorf("a draft past scripts.draft.max_seconds was not refused naming the cap: %v %s", err, text)
	}

	owner := connectAs(t, devOwnerAPIKey)
	res, text, err = owner.callRaw("manage_script", map[string]any{
		"command": "run_draft", "name": fmt.Sprintf("acc-2117-owner-%d", time.Now().UnixNano()),
		"source": slowFlow2117, "timeout_seconds": 120,
	})
	if err != nil || !res.IsError || !strings.Contains(text, "administrator") {
		t.Errorf("a non-administrator's two-minute draft was not refused: %v %s", err, text)
	}
}

//go:build integration

package acceptance

import (
	"fmt"
	"testing"
	"time"
)

// Issue #1934: the session-start briefing platform_info returns said nothing
// about automations the caller owns whose runs are failing. These criteria
// run an automation that fails, read platform_info as the owner's next
// session does, fix it, run it again, and read platform_info once more.
//
// Wire forms: platform_info takes no parameters and is called with none, by
// the connect that opens each session; its result is read from there.
// manage_script's command, name, description and source, and run_script's
// name, are typed strings, and run_script's wait_seconds an integer, so each
// admits one JSON form and is sent as a literal tools/call parameter of it.

// issue1934Failing returns the entry platform_info's notices carry for the
// automation named name, or nil.
func issue1934Failing(info map[string]any, name string) map[string]any {
	notices, _ := info["notices"].(map[string]any)
	list, _ := notices["failing_automations"].([]any)
	for _, it := range list {
		if n, _ := it.(map[string]any); n["name"] == name {
			return n
		}
	}
	return nil
}

// TestIssue1934_AFailingAutomationIsInTheBriefingUntilItSucceeds covers the
// ticket's acceptance: an owner whose automation failed its latest run gets a
// notices.failing_automations entry from the next platform_info, with the run
// to open and why it failed; it is listed again while it keeps failing, which
// is not being announced as new; and after a successful run it is gone.
func TestIssue1934_AFailingAutomationIsInTheBriefingUntilItSucceeds(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := fmt.Sprintf("acc-1934-%d", time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1934: an automation that fails, then is fixed.",
		"source": "def main():\n    \"\"\"Fails the way a script meeting input it does not expect fails.\"\"\"\n" +
			"    fail(\"the input was not what this script expects\")\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if run["status"] != "failed" {
		t.Fatalf("the run did not fail: %v", run)
	}

	next := connectAs(t, devOwnerAPIKey)
	first := issue1934Failing(next.info, name)
	if first == nil {
		t.Fatalf("the next session's platform_info does not name the failing automation")
	}
	if first["run_id"] != run["run_id"] || first["cause"] != "script" || first["retryable"] != false ||
		first["consecutive_failures"] != float64(1) || first["new"] != true {
		t.Errorf("the entry does not describe the failed run: %v", first)
	}
	if ref, _ := first["reference"].(string); ref == "" {
		t.Errorf("the entry carries no reference: %v", first)
	}
	if e, _ := first["error"].(string); e != "Error in fail: fail: the input was not what this script expects" {
		t.Errorf("error = %q", e)
	}

	again := issue1934Failing(connectAs(t, devOwnerAPIKey).info, name)
	if again == nil {
		t.Fatalf("an automation that is still failing is not listed in the following session")
	}
	if again["new"] != false {
		t.Errorf("a failure already briefed is announced as new again: %v", again)
	}

	owner.saveEdit(map[string]any{"command": "update", "name": name, "source": "def main():\n    \"\"\"Succeeds.\"\"\"\n    print(\"fixed\")\n"}, nil)
	if fixed := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60}); fixed["status"] != "succeeded" {
		t.Fatalf("the fixed run did not succeed: %v", fixed)
	}
	if after := issue1934Failing(connectAs(t, devOwnerAPIKey).info, name); after != nil {
		t.Errorf("the automation is still listed after a successful run: %v", after)
	}
}

// TestIssue1934_AnotherOwnersFailureIsNotBriefed holds the scope to the
// caller's own automations.
func TestIssue1934_AnotherOwnersFailureIsNotBriefed(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := fmt.Sprintf("acc-1934-other-%d", time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1934: a failure only its owner is told about.",
		"source":      "def main():\n    \"\"\"Fails.\"\"\"\n    fail(\"boom\")\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60})

	if seen := issue1934Failing(connectAs(t, devPeerAPIKey).info, name); seen != nil {
		t.Errorf("another person's briefing names this owner's failing automation: %v", seen)
	}
}

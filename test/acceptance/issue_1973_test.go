//go:build integration

package acceptance

import (
	"fmt"
	"testing"
	"time"
)

// Issue #1973: a retired automation stayed in the session-start briefing for
// as long as its last run had failed. These criteria fail an automation, take
// it out of service each way the platform has (its owner disables it; an
// administrator deprecates or supersedes it, the lifecycle status being an
// administrator's to change), and read platform_info as the owner's next
// session does.
//
// The ticket also asked for an acknowledge command. It is retired rather than
// built: a scheduled automation that fails again brings an acknowledged run
// back the next time it fires, and an unscheduled one its owner wants to stop
// hearing about is disabled, which they undo the same way.
//
// Wire forms: platform_info takes no parameters and is called with none, by
// the connect that opens each session. manage_script's command, name,
// description, source, owner_email, status and superseded_by and run_script's
// name are typed strings, manage_script's enabled a boolean and run_script's
// wait_seconds an integer, so each admits one JSON form and is sent as a
// literal tools/call parameter of it.

// issue1973Failed creates an automation owned by owner, runs it to a failure,
// and returns its name.
func issue1973Failed(t *testing.T, owner *client, label string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1973-%s-%d", label, time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #1973: an automation that fails.",
		"source": "def main():\n    \"\"\"Fails.\"\"\"\n    fail(\"the input was not what this script expects\")\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	if run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60}); run["status"] != "failed" {
		t.Fatalf("the run did not fail: %v", run)
	}
	if issue1934Failing(connectAs(t, devOwnerAPIKey).info, name) == nil {
		t.Fatalf("the failing automation is not briefed before it is retired")
	}
	return name
}

// TestIssue1973_ADeprecatedAutomationIsNotBriefed: an administrator
// deprecating the automation takes it off its owner's list, and returning it
// to active puts it back, because its last run is still a failure.
func TestIssue1973_ADeprecatedAutomationIsNotBriefed(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	name := issue1973Failed(t, owner, "deprecated")

	admin.call("manage_script", map[string]any{
		"command": "update", "name": name, "owner_email": devOwnerEmail, "status": "deprecated",
	})
	if listed := issue1934Failing(connectAs(t, devOwnerAPIKey).info, name); listed != nil {
		t.Errorf("a deprecated automation is still briefed: %v", listed)
	}

	admin.call("manage_script", map[string]any{
		"command": "update", "name": name, "owner_email": devOwnerEmail, "status": "active",
	})
	if issue1934Failing(connectAs(t, devOwnerAPIKey).info, name) == nil {
		t.Errorf("an automation returned to active is not briefed on its failed run")
	}
}

// TestIssue1973_ASupersededAutomationIsNotBriefed is the ticket's observed
// case: an automation an administrator marked replaced by another is no longer
// briefed to its owner.
func TestIssue1973_ASupersededAutomationIsNotBriefed(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	replacement := fmt.Sprintf("acc-1973-replacement-%d", time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": replacement, "description": "Acceptance #1973: the replacement.",
		"source": "def main():\n    \"\"\"Succeeds.\"\"\"\n    print(\"ok\")\n",
	}, nil)
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": replacement})
	})
	name := issue1973Failed(t, owner, "superseded")

	admin.call("manage_script", map[string]any{
		"command": "update", "name": name, "owner_email": devOwnerEmail,
		"status": "superseded", "superseded_by": replacement,
	})
	if listed := issue1934Failing(connectAs(t, devOwnerAPIKey).info, name); listed != nil {
		t.Errorf("a superseded automation is still briefed: %v", listed)
	}
}

// TestIssue1973_ADisabledAutomationIsNotBriefed: its owner disabling the
// automation takes it off their list, and enabling it again puts it back.
func TestIssue1973_ADisabledAutomationIsNotBriefed(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := issue1973Failed(t, owner, "disabled")

	owner.call("manage_script", map[string]any{"command": "update", "name": name, "enabled": false})
	if listed := issue1934Failing(connectAs(t, devOwnerAPIKey).info, name); listed != nil {
		t.Errorf("a disabled automation is still briefed: %v", listed)
	}

	owner.call("manage_script", map[string]any{"command": "update", "name": name, "enabled": true})
	if issue1934Failing(connectAs(t, devOwnerAPIKey).info, name) == nil {
		t.Errorf("an automation enabled again is not briefed on its failed run")
	}
}

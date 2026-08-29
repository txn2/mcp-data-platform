//go:build integration

package acceptance

import "testing"

// Issue #1545: manage_script validate over MCP reports whether the source
// reads run.state and whether it calls platform.save_state, as the portal's
// draft checks and the script contract already did.
func TestIssue1545_ValidateReportsStateUse(t *testing.T) {
	c := connect(t)

	stateful := c.call("manage_script", map[string]any{
		"command": "validate",
		"source":  "since = run.state.get(\"synced_through\", \"never\")\nplatform.save_state({\"synced_through\": run.fire_time})\n",
	})
	if stateful["ok"] != true {
		t.Fatalf("validate refused a well-formed source: %v", stateful)
	}
	if stateful["reads_state"] != true || stateful["saves_state"] != true {
		t.Fatalf("reads_state = %v, saves_state = %v; want both true for a source that reads run.state and calls platform.save_state", stateful["reads_state"], stateful["saves_state"])
	}

	stateless := c.call("manage_script", map[string]any{
		"command": "validate",
		"source":  "print(\"no state here\")\n",
	})
	if stateless["reads_state"] != false || stateless["saves_state"] != false {
		t.Fatalf("reads_state = %v, saves_state = %v; want both false, present, for a source that keeps no state", stateless["reads_state"], stateless["saves_state"])
	}
}

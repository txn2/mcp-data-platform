//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1821: a draft run refused `manage_script command=state` as a write
// whatever its state_action, so a watchdog that reads other scripts' state
// could not be dry-run without allow_writes. manage_resource's get and list
// were refused the same way: the draft classified the tool by its first verb
// alone.
//
// What these hold, against the running platform: a draft that reads another
// script's state through platform.call succeeds without allow_writes and sees
// that state, with state_action absent and with it set to get; a draft that
// sets or clears it is still refused and names the call; and a draft that reads
// the resource library succeeds.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` and
// `state_action` are typed string and `state` an object, and each is sent in
// that one form. The arguments inside platform.call are a Starlark dict the run
// hands the tool, so their form is the dict's.

// issue1821Watchdog reads the target's state and prints it. %s is the extra
// argument text, which is empty or names a state_action.
const issue1821Watchdog = `
got = platform.call("manage_script", {"command": "state", "name": %q%s})
print("watched: " + json.encode(got["state"]))
`

// issue1821Target authors the script whose state the watchdog reads and gives
// it a state, returning its name.
func issue1821Target(t *testing.T, c *client, stamp string) string {
	t.Helper()
	name := "acc-1821-target-" + stamp
	authorScript1664(t, c, name, "print(\"target\")\n")
	c.call("manage_script", map[string]any{
		"command": "state", "name": name, "state_action": "set",
		"state": map[string]any{"cursor": "2026-09-21"},
	})
	return name
}

// TestIssue1821_ADraftReadsAnotherScriptsState is the ticket's reproduction,
// in both forms a read takes.
func TestIssue1821_ADraftReadsAnotherScriptsState(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	target := issue1821Target(t, c, stamp)

	for label, extra := range map[string]string{
		"no state_action":  "",
		"state_action get": `, "state_action": "get"`,
	} {
		t.Run(label, func(t *testing.T) {
			name := "acc-1821-watch-" + strings.ReplaceAll(label, " ", "-") + "-" + stamp
			authorScript1664(t, c, name, fmt.Sprintf(issue1821Watchdog, target, extra))

			ran := draftRun1664(t, c, map[string]any{"name": name})
			if status, _ := ran["status"].(string); status != "succeeded" {
				t.Fatalf("a draft that only reads state must run, got %v: %v", ran["status"], ran)
			}
			if _, refused := ran["refused_write"]; refused {
				t.Errorf("a read was named as a refused write: %v", ran["refused_write"])
			}
			if log, _ := ran["log"].(string); !strings.Contains(log, `"cursor":"2026-09-21"`) {
				t.Errorf("the draft did not see the target's state: log = %q", log)
			}
		})
	}
}

// TestIssue1821_ADraftStillRefusesAStateWrite holds the other half: a set and a
// clear persist, so a draft without allow_writes stops at them and says which
// call it stopped at.
func TestIssue1821_ADraftStillRefusesAStateWrite(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	target := issue1821Target(t, c, stamp)

	for _, action := range []string{"set", "clear"} {
		t.Run(action, func(t *testing.T) {
			name := "acc-1821-writer-" + action + "-" + stamp
			authorScript1664(t, c, name, fmt.Sprintf(
				"platform.call(\"manage_script\", {\"command\": \"state\", \"name\": %q, \"state_action\": %q, \"state\": {}})\n",
				target, action))

			ran := draftRun1664(t, c, map[string]any{"name": name})
			if status, _ := ran["status"].(string); status != "failed" {
				t.Fatalf("a draft that writes state must stop at the write, got %v: %v", ran["status"], ran)
			}
			refused, _ := ran["refused_write"].(map[string]any)
			want := "manage_script command=state state_action=" + action
			if got, _ := refused["call"].(string); got != want {
				t.Errorf("refused_write.call = %q; want %q", got, want)
			}
		})
	}

	// The target's state is what it was: nothing the refused drafts asked for
	// happened.
	got := c.call("manage_script", map[string]any{"command": "state", "name": target, "state_action": "get"})
	state, _ := got["state"].(map[string]any)
	if state["cursor"] != "2026-09-21" {
		t.Errorf("a refused draft changed the target's state: %v", got)
	}
}

// TestIssue1821_ADraftReadsTheResourceLibrary holds the same rule for the other
// tool whose reads were refused: manage_resource list and get.
func TestIssue1821_ADraftReadsTheResourceLibrary(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1821-library-" + stamp
	authorScript1664(t, c, name, `
listed = platform.call("manage_resource", {"action": "list", "path": "acceptance"})
got = platform.call("manage_resource", {"action": "get", "reference": "mcp:resource:00000000-0000-0000-0000-000000000000"})
print("listed and got: " + str(got["found"]))
`)

	ran := draftRun1664(t, c, map[string]any{"name": name})
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("a draft that only reads the library must run, got %v: %v", ran["status"], ran)
	}
	if log, _ := ran["log"].(string); !strings.Contains(log, "listed and got: False") {
		t.Errorf("the draft did not complete both reads: log = %q", log)
	}
}

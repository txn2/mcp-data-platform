//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Issue #2827: a managed script's draft refused help, which the platform
// answers from a tool's schema, as a write ("manage_asset action=help persists
// outside this run"), and with allow_writes reported it among the calls that
// persisted. help now reads on every action tool.
//
// Wire forms: manage_script takes command, name and source as strings and
// allow_writes as a boolean; the script's platform.call hands each tool its
// action or command as a string. No parameter touched admits a second form.

// script2827 asks four action tools for help.
const script2827 = `def main():
    """Asks four action tools what they take."""
    for tool, field in [("manage_asset", "action"), ("manage_resource", "action"), ("manage_table", "action"), ("manage_site", "command")]:
        got = platform.call(tool, {field: "help"})
        print("%s commands=%d" % (tool, len(got.get("commands", []))))
`

// TestIssue2827_ADraftAsksForHelpWithoutAWrite: the draft runs, each tool's
// help answers, and nothing is reported written, with and without
// allow_writes.
func TestIssue2827_ADraftAsksForHelpWithoutAWrite(t *testing.T) {
	c := connect(t)
	name := "acc2827-help-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	for _, allow := range []bool{false, true} {
		args := map[string]any{"command": "run_draft", "name": name, "source": script2827}
		if allow {
			args["allow_writes"] = true
		}
		ran := c.call("manage_script", args)
		if ran["status"] != "succeeded" {
			t.Fatalf("allow_writes=%v: the draft did not succeed: %v", allow, ran)
		}
		log := fmt.Sprint(ran["log"])
		for _, tool := range []string{"manage_asset", "manage_resource", "manage_table", "manage_site"} {
			if !strings.Contains(log, tool+" commands=") || strings.Contains(log, tool+" commands=0") {
				t.Errorf("allow_writes=%v: %s's help did not answer: %s", allow, tool, log)
			}
		}
		if w, _ := ran["writes"].([]any); len(w) != 0 {
			t.Errorf("allow_writes=%v: help reported as written: %v", allow, ran["writes"])
		}
		if ran["refused_write"] != nil {
			t.Errorf("allow_writes=%v: help refused as a write: %v", allow, ran["refused_write"])
		}
	}
}

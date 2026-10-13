//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2113: a script's page JavaScript lives in a managed resource of its
// own, edited as a file, and the script loads its text with
// load("res:<library>/<path>/<file>", NAME = "text"). A save pins the version
// of the file it read; every run of that script version reads it, and a test
// replaying an older run reads the version that run read.
//
// Wire forms: the res: module and its NAME = "text" binding are Starlark in
// the source; manage_resource's action, path, filename, display_name,
// description, content, content_type and reference, manage_script's command,
// name, source, change_summary and user_agreed, and run_script's name and
// wait_seconds are sent as their typed JSON forms.

const (
	page2113v1 = "function step() { return 'one'; }"
	page2113v2 = "function step() { return 'two'; }"
)

// source2113 loads the page script and hands its text back.
func source2113(module string) string {
	return `load("` + module + `", PAGE_JS = "text")

def main():
    """Hands back the page script the run loaded."""
    platform.result({"js": PAGE_JS})
`
}

// run2113 runs the script and returns the page script it read and the run id.
func run2113(t *testing.T, c *client, name string) (js, runID string) {
	t.Helper()
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	result, _ := run["result"].(map[string]any)
	js, _ = result["js"].(string)
	runID, _ = run["run_id"].(string)
	return js, runID
}

func TestIssue2113_AScriptLoadsItsPageScriptFromAFile(t *testing.T) {
	c := connectFor(t, 8*time.Minute)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	created := c.call("manage_resource", map[string]any{
		"action": "create", "path": "flows/acc-2113-" + stamp, "filename": "page.js",
		"display_name": "Page script " + stamp, "description": "Acceptance #2113: a flow's page script.",
		"content": page2113v1, "content_type": "text/javascript",
	})
	reference, _ := created["reference"].(string)
	uri, _ := created["uri"].(string)
	if reference == "" || !strings.HasPrefix(uri, "mcp://") {
		t.Fatalf("the page script was not filed: %v", created)
	}
	module := "res:" + strings.TrimPrefix(uri, "mcp://")
	name := "acc-2113-flow-" + stamp
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2113: loads its page script.",
		"source": source2113(module),
	}, nil)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": reference, "force": true})
	})

	validated := c.call("manage_script", map[string]any{"command": "validate", "name": name})
	if raw, _ := json.Marshal(validated["resources"]); !strings.Contains(string(raw), module) {
		t.Errorf("validate does not report the file the script loads: %v", validated)
	}

	firstJS, firstRun := run2113(t, c, name)
	if firstJS != page2113v1 {
		t.Fatalf("the run read %q; want the page script as saved", firstJS)
	}

	// The file is edited as itself, and the saved version keeps reading what
	// its save pinned until the script is saved again.
	replaced := c.call("manage_resource", map[string]any{
		"action": "replace_content", "reference": reference, "content": page2113v2,
	})
	if v, _ := replaced["version"].(float64); v != 2 {
		t.Fatalf("editing the page script recorded version %v: %v", replaced["version"], replaced)
	}
	if js, _ := run2113(t, c, name); js != page2113v1 {
		t.Errorf("a run of the saved version read %q after the file changed; want the version it pinned", js)
	}
	held, text, err := c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": reference})
	if err != nil || held.IsError || !strings.Contains(text, "managed script") {
		t.Errorf("deleting a file a script loads was not refused naming the script: %v %s", err, text)
	}

	c.saveEdit(map[string]any{"command": "update", "name": name, "source": source2113(module)}, nil)
	if js, _ := run2113(t, c, name); js != page2113v2 {
		t.Errorf("a run after saving again read %q; want the file's new version", js)
	}

	// A replay of the first run reads the page script that run read.
	tested := c.call("manage_script", map[string]any{
		"command": "test", "name": name,
		"source": source2113(module) + `
def test_first_run():
    """The first run read the first page script."""
    testing.replay("` + firstRun + `")
    main()
    assert.eq(testing.outputs().result, {"js": "` + page2113v1 + `"})
`,
	})
	if first := firstTest1939(t, tested); first["passed"] != true {
		t.Errorf("a replay of the first run did not read the page script that run read: %v", tested)
	}
}

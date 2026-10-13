//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2114: a long script is created from a source stored once as a
// managed resource, a create the gates refuse is kept as a pending create the
// author fixes with a small patch, and a refusal does not echo the source
// back unless asked.
//
// Wire forms: manage_script's source_ref is a string, sent as an
// mcp:resource: reference and as the file's mcp:// URI; verbose is a boolean,
// sent true and omitted; patch's edits are an array of objects with op, find
// and replace strings. manage_resource's create arguments are typed strings.

// source2114 is a script of about 45 KB: a table of lines it counts, with a
// test whose expected count is wrong when wrongCount is set.
func source2114(lines int, expected int) string {
	var b strings.Builder
	b.WriteString("LINES = [\n")
	for i := range lines {
		fmt.Fprintf(&b, "    \"station %04d schedule line, carried as a constant of the flow\",\n", i)
	}
	b.WriteString("]\n\n")
	b.WriteString(`def main():
    """Counts the schedule lines."""
    platform.result({"lines": len(LINES)})

def test_counts_every_line():
    """Every line is counted."""
    main()
    assert.eq(testing.outputs().result, {"lines": ` + fmt.Sprint(expected) + `})
`)
	return b.String()
}

// stored2114 files the source as a managed resource and returns its reference
// and URI.
func stored2114(t *testing.T, c *client, stamp, source string) (reference, uri string) {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "path": "scripts/acc-2114-" + stamp, "filename": "flow.star",
		"display_name": "Flow source " + stamp, "description": "Acceptance #2114: a script's source.",
		"content": source, "content_type": "text/plain",
	})
	reference, _ = out["reference"].(string)
	uri, _ = out["uri"].(string)
	if reference == "" || uri == "" {
		t.Fatalf("the source file was not filed: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": reference, "force": true})
	})
	return reference, uri
}

// TestIssue2114_ALongScriptIsCreatedFromAStoredFileAndFixedWithAPatch: a
// 45 KB script is created from its stored file; its failing test refuses the
// save and keeps it pending; a small patch fixes the test and the save goes
// through, with the source never sent again.
func TestIssue2114_ALongScriptIsCreatedFromAStoredFileAndFixedWithAPatch(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	const lines = 640
	source := source2114(lines, 1)
	if n := len(source); n < 40_000 || n > 50_000 {
		t.Fatalf("the fixture source is %d bytes; the ticket's script is about 45 KB", n)
	}
	reference, _ := stored2114(t, c, stamp, source)
	name := "acc-2114-flow-" + stamp
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	refused := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2114: counts its lines.", "source_ref": reference,
	})
	raw, _ := json.Marshal(refused)
	if refused["status"] == "created" {
		t.Fatalf("a script whose test fails was saved: %s", raw)
	}
	if _, echoed := refused["formatted_source"]; echoed {
		t.Errorf("the refusal echoed the formatted source without verbose")
	}
	if len(raw) > 8_000 {
		t.Errorf("the refusal of a %d-byte script is %d bytes; it should carry the findings, not the source", len(source), len(raw))
	}
	if !strings.Contains(string(raw), "pending") {
		t.Errorf("the refusal does not say the create was kept pending: %s", raw)
	}

	pending := c.call("manage_script", map[string]any{"command": "get", "name": name})
	if src, _ := pending["source"].(string); !strings.Contains(src, "station 0639") {
		t.Errorf("get does not read the pending source: %v", pending["status"])
	}

	patched := c.call("manage_script", map[string]any{
		"command": "patch", "name": name,
		"edits": []any{map[string]any{"op": "replace", "find": `{"lines": 1}`, "replace": fmt.Sprintf(`{"lines": %d}`, lines)}},
	})
	if patched["status"] != "created" {
		t.Fatalf("the patch fixing the test did not save the script: %v", patched)
	}
	if out := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60}); out["status"] != "succeeded" {
		t.Errorf("the script saved from its stored file did not run: %v", out)
	}
}

// TestIssue2114_SourceRefTakesTheFilesURIAndVerboseEchoesTheSource: the file
// is named by its mcp:// URI as well as its reference, and verbose=true asks
// for the formatted source on a refusal.
func TestIssue2114_SourceRefTakesTheFilesURIAndVerboseEchoesTheSource(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	_, uri := stored2114(t, c, stamp, source2114(20, 0))
	name := "acc-2114-uri-" + stamp
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	refused := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2114.", "source_ref": uri, "verbose": true,
	})
	if refused["status"] == "created" {
		t.Fatalf("a script whose test fails was saved: %v", refused)
	}
	if src, _ := refused["formatted_source"].(string); !strings.Contains(src, "station 0019") {
		t.Errorf("verbose=true did not echo the formatted source: %v", refused)
	}

	fixed := c.call("manage_script", map[string]any{
		"command": "patch", "name": name,
		"edits": []any{map[string]any{"op": "replace", "find": `{"lines": 0}`, "replace": `{"lines": 20}`}},
	})
	if fixed["status"] != "created" {
		t.Fatalf("the patch did not save the script read from the file's URI: %v", fixed)
	}
}

//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1804: manage_script patch took its replacement from the `replace` key
// and ignored every other. An edit carrying the text under a different key had
// the payload dropped, the matched anchor replaced with an EMPTY STRING, a new
// version saved, and `status: updated` reported. The damage was a deletion
// nobody asked for, in a source that usually still parsed — so `validate`
// passed too, and a scheduled run was the first thing to notice.
//
// These criteria run through the real surface on the local stack: a script is
// created, patched and read back with the tool a person calls, and the verdict
// is what the platform stored, never what the call said about itself.
//
// Wire forms. Every parameter touched here is typed in manage_script's input
// schema, which is now the shared grammar pkg/textpatch declares rather than a
// paraphrase of it: `command`, `name` and `source` are strings, `base_version`
// an integer, `dry_run` a boolean, and `edits` an array of objects whose own
// keys (`op`, `find`, `replace`, `text`) are declared strings with the item
// closed to everything else. Each admits exactly one JSON form and is sent
// below as a literal tools/call parameter of that form. Both values of the one
// parameter with two meaningful ones are exercised: the reproduction ran with
// `dry_run: true` and on a real save, and both are checked here.

// script1804 is the three-line source every criterion patches. The lines are
// distinct so a deletion cannot hide as a coincidence.
const script1804 = `AAA = "one"
BBB = "two"
CCC = "three"
`

// createScript1804 saves a script through the tool and removes it afterwards.
func createScript1804(t *testing.T, c *client) string {
	t.Helper()
	name := fmt.Sprintf("acceptance-1804-%d", time.Now().UnixNano()%1_000_000_000)
	out := c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1804: a patch whose replacement key is wrong must be refused.",
		"source":      script1804,
	})
	if status, _ := out["status"].(string); status == "" {
		t.Fatalf("manage_script create returned no status: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	return name
}

// sourceOf1804 reads back what the platform is actually storing.
func sourceOf1804(t *testing.T, c *client, name string) (source string, version float64) {
	t.Helper()
	out := c.call("manage_script", map[string]any{"command": "get", "name": name})
	source, _ = out["source"].(string)
	version, _ = out["version"].(float64)
	if source == "" {
		t.Fatalf("manage_script get returned no source for %s: %v", name, out)
	}
	return source, version
}

// patch1804 issues one patch and returns the result, its text, and whether the
// tool refused it.
func patch1804(c *client, name string, dryRun bool, edits []any) (text string, refused bool) {
	c.t.Helper()
	res, text, err := c.callRaw("manage_script", map[string]any{
		"command": "patch",
		"name":    name,
		"dry_run": dryRun,
		"edits":   edits,
	})
	if err != nil {
		c.t.Fatalf("manage_script patch: transport error: %v", err)
	}
	return text, res.IsError
}

// TestIssue1804_AnEditWithNoReplacementIsRefused is the reported defect: the
// replacement rode on `text`, which op=replace does not read.
//
// It is checked on a dry run and on a real save, because the report reproduced
// on both and a dry run that refuses while a save deletes would be the worse
// half of the bug.
func TestIssue1804_AnEditWithNoReplacementIsRefused(t *testing.T) {
	c := connect(t)
	name := createScript1804(t, c)
	before, versionBefore := sourceOf1804(t, c, name)

	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry_run=%v", dryRun), func(t *testing.T) {
			text, refused := patch1804(c, name, dryRun, []any{map[string]any{
				"op": "replace", "find": `BBB = "two"`, "text": `BBB = "CHANGED"`,
			}})
			if !refused {
				t.Fatalf("the patch was accepted; it answered: %s", text)
			}
			// The refusal has to name the key that was not sent, because what
			// made this expensive was that nothing said which key carries a
			// replacement.
			if !strings.Contains(text, `"replace"`) {
				t.Errorf("the refusal does not name the missing key: %s", text)
			}
		})
	}

	after, versionAfter := sourceOf1804(t, c, name)
	if after != before {
		t.Errorf("the script changed under a refused patch:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if versionAfter != versionBefore {
		t.Errorf("a refused patch saved version %v over %v", versionAfter, versionBefore)
	}
}

// TestIssue1804_AnUnrecognizedEditKeyIsRefused is the other half: the edit item
// declares its keys and is closed to the rest, so a misspelling is named rather
// than ignored. The top-level argument validator already refused a stray
// property on a tool call; this is that same strictness reaching inside `edits`.
func TestIssue1804_AnUnrecognizedEditKeyIsRefused(t *testing.T) {
	c := connect(t)
	name := createScript1804(t, c)
	before, _ := sourceOf1804(t, c, name)

	text, refused := patch1804(c, name, true, []any{map[string]any{
		"op": "replace", "find": `BBB = "two"`, "replacement": `BBB = "CHANGED"`,
	}})
	if !refused {
		t.Fatalf("an edit carrying an undeclared key was accepted; it answered: %s", text)
	}
	if !strings.Contains(text, "replacement") {
		t.Errorf("the refusal does not name the offending key: %s", text)
	}

	after, _ := sourceOf1804(t, c, name)
	if after != before {
		t.Errorf("the script changed under a refused patch:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// TestIssue1804_TheReplaceKeyStillPatches is the control, and covers the
// multi-line replacement the reporter had concluded was broken: the tool was
// working, the wrong key was silently emptying the anchor.
func TestIssue1804_TheReplaceKeyStillPatches(t *testing.T) {
	c := connect(t)
	name := createScript1804(t, c)
	_, versionBefore := sourceOf1804(t, c, name)

	text, refused := patch1804(c, name, false, []any{map[string]any{
		"op": "replace", "find": `BBB = "two"`, "replace": "BBB = \"CHANGED\"\nBBB2 = \"added\"",
	}})
	if refused {
		t.Fatalf("a well-formed patch was refused: %s", text)
	}

	after, versionAfter := sourceOf1804(t, c, name)
	for _, want := range []string{`AAA = "one"`, `BBB = "CHANGED"`, `BBB2 = "added"`, `CCC = "three"`} {
		if !strings.Contains(after, want) {
			t.Errorf("the patched source is missing %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, `BBB = "two"`) {
		t.Errorf("the anchor survived the patch:\n%s", after)
	}
	if versionAfter <= versionBefore {
		t.Errorf("a patch that landed did not save a new version: %v then %v", versionBefore, versionAfter)
	}
}

// TestIssue1804_AnExplicitEmptyReplacementDeletes keeps deletion available. It
// is the omission that is refused, never the written-out empty string, so
// nothing an author could legitimately want was taken away by the fix.
func TestIssue1804_AnExplicitEmptyReplacementDeletes(t *testing.T) {
	c := connect(t)
	name := createScript1804(t, c)

	text, refused := patch1804(c, name, false, []any{map[string]any{
		"op": "replace", "find": "BBB = \"two\"\n", "replace": "",
	}})
	if refused {
		t.Fatalf("an explicit empty replacement was refused: %s", text)
	}

	after, _ := sourceOf1804(t, c, name)
	if strings.Contains(after, "BBB") {
		t.Errorf("the line was not deleted:\n%s", after)
	}
	if !strings.Contains(after, `AAA = "one"`) || !strings.Contains(after, `CCC = "three"`) {
		t.Errorf("the deletion took more than the anchor:\n%s", after)
	}
}

// TestIssue1804_TheEditKeysAreDiscoverable closes the third gap the report
// named: there was no way to learn the key's name from the tool, and trying the
// wrong one was destructive rather than instructive. The schema now declares
// the edit's keys, and help names them.
func TestIssue1804_TheEditKeysAreDiscoverable(t *testing.T) {
	c := connect(t)

	var manageScript *mcp.Tool
	for _, tool := range c.tools() {
		if tool.Name == "manage_script" {
			manageScript = tool
		}
	}
	if manageScript == nil {
		t.Fatal("manage_script is not registered on this server")
	}

	items := editItemsSchema1804(t, manageScript)
	props, _ := items["properties"].(map[string]any)
	for _, key := range []string{"op", "find", "replace", "text"} {
		if _, ok := props[key]; !ok {
			t.Errorf("the edit schema does not declare %q; properties: %v", key, props)
		}
	}
	if additional, ok := items["additionalProperties"].(bool); !ok || additional {
		t.Errorf("the edit schema is not closed to undeclared keys: additionalProperties = %v", items["additionalProperties"])
	}

	help := c.call("manage_script", map[string]any{"command": "help"})
	text, _ := help["help"].(string)
	if text == "" {
		text = fmt.Sprintf("%v", help)
	}
	for _, want := range []string{`"replace"`, `"text"`, "insert_before"} {
		if !strings.Contains(text, want) {
			t.Errorf("command=help does not name %s", want)
		}
	}
}

// editItemsSchema1804 returns the schema manage_script advertises for one edit.
// The schema arrives in whatever shape the tool registered it in, so it is
// normalized through JSON before it is read.
func editItemsSchema1804(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	if tool.InputSchema == nil {
		t.Fatal("manage_script advertises no input schema")
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("manage_script input schema does not marshal: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Items map[string]any `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("manage_script input schema is not a JSON object: %v", err)
	}
	edits, ok := schema.Properties["edits"]
	if !ok {
		t.Fatal("manage_script advertises no edits argument")
	}
	if edits.Items == nil {
		t.Fatal("the edits argument declares no items schema, so nothing inside an edit is validated")
	}
	return edits.Items
}

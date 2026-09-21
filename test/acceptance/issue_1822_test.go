//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1822: a run_draft with allow_writes still previewed platform.export,
// although the flag's description said the run writes for real, so a staging
// pipeline -- export to resources, register the file, query it -- could not be
// drafted at all. And run_draft needed a saved script, so testing a new
// pipeline meant saving a throwaway script, running it and cleaning up after
// it.
//
// What these hold, against the running platform: a draft of a script that is
// not saved, run with allow_writes, writes its export, registers it and queries
// the table in one run, reports each write, and saves no script; without
// allow_writes the same draft writes nothing and says so; a draft of an unsaved
// script cannot write to the portal destination and says why; and a draft of a
// saved script with allow_writes writes its portal output as a version of the
// script's own asset. The portal editor's dry run, the other surface a draft
// runs from, writes the same way and says what it wrote.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`
// are typed string, `allow_writes` boolean, `params` an array of objects and
// `args` an object, and each is sent in that one form. manage_table's `action`,
// `reference` and `registration_id` are typed string. The editor's dry-run
// body carries `allow_writes` as a JSON boolean, its one form.

// issue1822Pipeline is the ticket's pipeline: export rows to the library as
// JSON lines, register the file, and read the table back, all in one draft.
// The verbs are the key, the connection and the table name.
const issue1822Pipeline = `
rows = [{"id": str(i), "region": run.params["region"]} for i in range(3)]
out = platform.export(name="Acceptance 1822 staging", rows=rows, format="jsonl",
    destination="resources", key="acceptance/issue-1822/%s",
    register={"connection": %q, "table_name": %q})
print("preview", out["preview"], "reference", out.get("reference", "none"))
if not out["preview"]:
    got = platform.query(connection=%q,
        sql="SELECT count(*) AS n FROM " + out["table"]["query_table"] + " WHERE region = :region",
        params={"region": run.params["region"]})
    print("table rows", got["rows"][0]["n"])
`

// issue1822Draft runs the pipeline as a draft of a script that does not exist.
func issue1822Draft(t *testing.T, c *client, stamp string, allowWrites bool) map[string]any {
	t.Helper()
	source := fmt.Sprintf(issue1822Pipeline, "acc-1822-"+stamp+".jsonl",
		scratchResourceConnection, "acc_1822_"+stamp, scratchResourceConnection)
	return c.call("manage_script", map[string]any{
		"command":      "run_draft",
		"name":         "acc-1822-unsaved-" + stamp,
		"source":       source,
		"allow_writes": allowWrites,
		"params":       []any{map[string]any{"name": "region", "type": "string", "required": true}},
		"args":         map[string]any{"region": "west"},
	})
}

// TestIssue1822_AnUnsavedDraftAllowedToWriteRunsThePipeline is the ticket's
// reproduction, end to end in one draft.
func TestIssue1822_AnUnsavedDraftAllowedToWriteRunsThePipeline(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ran := issue1822Draft(t, c, stamp, true)

	exports, _ := ran["exports"].([]any)
	var export map[string]any
	if len(exports) == 1 {
		export, _ = exports[0].(map[string]any)
	}
	reference, _ := export["reference"].(string)
	if reference != "" {
		t.Cleanup(func() { issue1822Remove(t, c, reference) })
	}
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("the draft did not succeed: %v", ran)
	}
	if export["preview"] != false || reference == "" {
		t.Fatalf("the export was not written: %v", export)
	}
	log, _ := ran["log"].(string)
	if !strings.Contains(log, "table rows 3") {
		t.Errorf("the draft did not read its rows back through the table it registered: log = %q", log)
	}
	if !issue1822Wrote(ran, "manage_table action=register") {
		t.Errorf("the registration is not listed under writes: %v", ran["writes"])
	}
	if ran["saved"] != false {
		t.Errorf("saved = %v; want false for a script that is not saved", ran["saved"])
	}
	message, _ := ran["message"].(string)
	if !strings.Contains(message, "listed under exports, persisted for real") {
		t.Errorf("the message does not say the export was written: %q", message)
	}
	got, text, err := c.callRaw("manage_script", map[string]any{"command": "get", "name": "acc-1822-unsaved-" + stamp})
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsError {
		t.Errorf("running a draft saved the script: %s", text)
	}
}

// TestIssue1822_WithoutAllowWritesTheDraftWritesNothing holds the default: the
// same draft previews the export, reports the table it would make, and leaves
// no file behind.
func TestIssue1822_WithoutAllowWritesTheDraftWritesNothing(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ran := issue1822Draft(t, c, stamp, false)

	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("the draft did not succeed: %v", ran)
	}
	exports, _ := ran["exports"].([]any)
	if len(exports) != 1 {
		t.Fatalf("exports = %v; want one", ran["exports"])
	}
	export, _ := exports[0].(map[string]any)
	if export["preview"] != true {
		t.Errorf("the export was written without allow_writes: %v", export)
	}
	table, _ := export["table"].(map[string]any)
	if table["preview"] != true {
		t.Errorf("the table is not reported as a preview: %v", table)
	}
	if message, _ := ran["message"].(string); !strings.Contains(message, "Nothing was persisted") {
		t.Errorf("message = %q; want it to say nothing was persisted", message)
	}
	if id, found := resourceNamed1664(t, c, "acc-1822-"+stamp+".jsonl"); found {
		_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		t.Errorf("a draft without allow_writes left a file in the library")
	}
}

// TestIssue1822_AnUnsavedDraftCannotWriteToThePortal: a portal output is the
// saved script's own asset, so a draft of a script that is not saved is told
// to save it or write to resources, rather than filing an asset nothing would
// find again.
func TestIssue1822_AnUnsavedDraftCannotWriteToThePortal(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ran := c.call("manage_script", map[string]any{
		"command":      "run_draft",
		"name":         "acc-1822-portal-" + stamp,
		"source":       `platform.export(name="daily", rows=[{"a": 1}], format="csv")`,
		"allow_writes": true,
	})
	if status, _ := ran["status"].(string); status != "failed" {
		t.Fatalf("status = %v; want failed: %v", ran["status"], ran)
	}
	if msg, _ := ran["error"].(string); !strings.Contains(msg, "not saved yet") || !strings.Contains(msg, `"resources"`) {
		t.Errorf("the refusal does not say why or what to do instead: %q", msg)
	}
}

// TestIssue1822_ASavedDraftAllowedToWriteVersionsItsAsset: a saved script's
// portal output, drafted with allow_writes, lands as a version of the
// script's own asset.
func TestIssue1822_ASavedDraftAllowedToWriteVersionsItsAsset(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1822-saved-" + stamp
	authorScript1664(t, c, name, `platform.export(name="daily", rows=[{"a": 1}], format="csv")`)

	ran := draftRun1664(t, c, map[string]any{"name": name, "allow_writes": true})
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("the draft did not succeed: %v", ran)
	}
	exports, _ := ran["exports"].([]any)
	if len(exports) != 1 {
		t.Fatalf("exports = %v; want one", ran["exports"])
	}
	export, _ := exports[0].(map[string]any)
	assetID, _ := export["asset_id"].(string)
	if export["preview"] != false || assetID == "" {
		t.Fatalf("the portal output was not written: %v", export)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})
	if _, saved := ran["saved"]; saved {
		t.Errorf("a draft of a saved script reported saved=%v", ran["saved"])
	}
}

// issue1822Wrote reports whether a draft lists a call under writes.
func issue1822Wrote(ran map[string]any, call string) bool {
	writes, _ := ran["writes"].([]any)
	for _, w := range writes {
		if row, _ := w.(map[string]any); row["call"] == call {
			return true
		}
	}
	return false
}

// issue1822Remove unregisters every table over a file and deletes the file.
func issue1822Remove(t *testing.T, c *client, reference string) {
	t.Helper()
	listed, _, _ := c.callRaw("manage_table", map[string]any{"action": "list", "reference": reference})
	if listed != nil && !listed.IsError {
		for _, id := range registrationIDs1822(firstText(listed)) {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		}
	}
	id := strings.TrimPrefix(reference, "mcp:resource:")
	_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
}

// registrationIDs1822 reads the registration ids out of a manage_table list
// answer.
func registrationIDs1822(text string) []string {
	var ids []string
	for _, part := range strings.Split(text, `"registration_id":"`)[1:] {
		if end := strings.Index(part, `"`); end > 0 {
			ids = append(ids, part[:end])
		}
	}
	return ids
}

// TestIssue1822_TheEditorsDryRunWritesForRealToo: the portal editor's dry run
// is the other surface a draft is run from, and its "Write for real" control
// writes the export as manage_script's allow_writes does, saying which output
// it wrote, as what, and the table registered over it.
func TestIssue1822_TheEditorsDryRunWritesForRealToo(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1822-editor-" + stamp
	source := fmt.Sprintf(`platform.export(name="Acceptance 1822 editor", rows=[{"id": "1"}], format="jsonl",
    destination="resources", key="acceptance/issue-1822/acc-1822-editor-%s.jsonl",
    register={"connection": %q, "table_name": %q})
`, stamp, scratchResourceConnection, "acc_1822_editor_"+stamp)
	authorScript1664(t, c, name, source)
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("manage_script get returned no id: %v", got)
	}

	status, body := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/dry-run",
		jsonBody(t, map[string]any{"allow_writes": true}))
	if status != http.StatusOK {
		t.Fatalf("dry run: status %d: %v", status, body)
	}
	outputs, _ := body["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("outputs = %v; want one", body["outputs"])
	}
	output, _ := outputs[0].(map[string]any)
	reference, _ := output["reference"].(string)
	if reference != "" {
		t.Cleanup(func() { issue1822Remove(t, c, reference) })
	}
	if output["written"] != true || !strings.HasPrefix(reference, "mcp:resource:") {
		t.Fatalf("the editor's dry run did not write the export: %v", output)
	}
	if table, _ := output["table"].(string); !strings.Contains(table, "acc_1822_editor_"+stamp) {
		t.Errorf("the output does not name the table registered over it: %v", output)
	}
	if message, _ := body["message"].(string); !strings.Contains(message, "This dry run was run with allow_writes") {
		t.Errorf("message = %q", message)
	}
}

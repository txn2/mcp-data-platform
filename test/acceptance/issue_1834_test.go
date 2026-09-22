//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1834: a managed script that published an HTML report with
// platform.export could not declare the files the page names, so an
// <img src="mcp://..."> in it was served exactly as written and every run
// shipped a broken logo. The only fix was a manage_asset update made outside
// the script after its first run, nothing warned, and a rename lost it.
//
// What these hold, against the running platform: a saved script's run
// declares the references its export names through references=, the asset
// lists them and its served content carries working URLs in their place, and
// the run reports the one its body names without declaring; a reference the
// author cannot resolve fails the run naming the output; validate warns about
// a literal reference no export declares and reports manage_asset; and a draft
// reports the declaration it would make without making it.
//
// Wire forms: references= is a Starlark argument inside `source`, which
// manage_script types as a string and receives in that one form. manage_script's
// `command`, `name` and `description` are typed string, and run_script's `name`
// string, `args` an object and `wait_seconds` an integer, each sent in that one
// form. The portal references and content routes take no body.

// issue1834Undeclared is a reference the script's document names and does not
// declare, which the run must report rather than pass over.
const issue1834Undeclared = "mcp://global/acceptance/issue-1834-undeclared.png"

// issue1834Source publishes a report naming the declared file and the
// undeclared one, and prints what the export reported.
const issue1834Source = `LOGO = %q
html = "<html><body><img src='" + LOGO + "' alt='logo'><img src='%s'><h1>Weekly</h1></body></html>"
out = platform.export(name="Acceptance 1834 report", rows=html, format="html", references=[LOGO])
print("declared", out.get("references"), "undeclared", out.get("undeclared_references"))
`

// runScript1834 runs a saved script and waits for its answer, whatever the
// outcome.
func runScript1834(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	return c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
}

// TestIssue1834_AnExportDeclaresTheReferencesItsDocumentNames is the ticket's
// reproduction: the first run of a new report script renders its logo.
func TestIssue1834_AnExportDeclaresTheReferencesItsDocumentNames(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	file := createResource1584(t, c, "persona", personaLibrary1584, "logo-1834-"+stamp+".csv")
	name := "acc-1834-report-" + stamp
	authorScript1664(t, c, name, fmt.Sprintf(issue1834Source, file.uri, issue1834Undeclared))

	run := runScript1834(t, c, name)
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	outputs, _ := run["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("outputs = %v; want one", run["outputs"])
	}
	output, _ := outputs[0].(map[string]any)
	assetID, _ := output["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("the output names no asset: %v", output)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})

	refs := listRefs1584(t, c, assetID)
	if len(refs) != 1 || refs[0]["uri"] != file.uri {
		t.Fatalf("the asset declares %v; want exactly %s", refs, file.uri)
	}
	status, content := c.restText("/api/v1/portal/assets/" + assetID + "/content")
	if status != http.StatusOK {
		t.Fatalf("content: status %d: %s", status, content)
	}
	if strings.Contains(content, file.uri) || !strings.Contains(content, "/portal/refs/"+assetID+"/") {
		t.Errorf("the served content does not carry the reference's URL in place of its URI: %s", content)
	}
	if !strings.Contains(content, issue1834Undeclared) {
		t.Errorf("the undeclared reference was altered: %s", content)
	}
	log, _ := run["log"].(string)
	if !strings.Contains(log, "undeclared_references: Acceptance 1834 report: "+issue1834Undeclared) {
		t.Errorf("the run log does not name the undeclared reference: %q", log)
	}
	if !strings.Contains(log, `undeclared ["`+issue1834Undeclared+`"]`) {
		t.Errorf("the export record handed to the script does not list it: %q", log)
	}
}

// TestIssue1834_AReferenceThatDoesNotResolveFailsTheRun: the declaration is
// manage_asset's, refused in its words, and the run stops naming the output.
func TestIssue1834_AReferenceThatDoesNotResolveFailsTheRun(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	missing := "mcp://global/acceptance/issue-1834-missing-" + stamp + ".png"
	name := "acc-1834-refused-" + stamp
	authorScript1664(t, c, name, fmt.Sprintf(issue1834Source, missing, issue1834Undeclared))

	run := runScript1834(t, c, name)
	if outputs, _ := run["outputs"].([]any); len(outputs) == 1 {
		output, _ := outputs[0].(map[string]any)
		if id, _ := output["asset_id"].(string); id != "" {
			t.Cleanup(func() {
				_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
			})
		}
	}
	if status, _ := run["status"].(string); status != "failed" {
		t.Fatalf("status = %v; want failed: %v", run["status"], run)
	}
	msg, _ := run["error"].(string)
	if !strings.Contains(msg, `output "Acceptance 1834 report" was written, and declaring its references failed`) ||
		!strings.Contains(msg, missing) {
		t.Errorf("the failure does not name the output and the reference: %q", msg)
	}
}

// TestIssue1834_ValidateWarnsAboutAnUndeclaredReference: the check needs no
// run, and the declaration is reported as the tool call it is.
func TestIssue1834_ValidateWarnsAboutAnUndeclaredReference(t *testing.T) {
	c := connect(t)
	source := fmt.Sprintf(issue1834Source, "mcp://global/brand/logo.svg", issue1834Undeclared)
	out := c.call("manage_script", map[string]any{"command": "validate", "source": source})
	if out["ok"] != true {
		t.Fatalf("a warning stopped the script from validating: %v", out)
	}
	tools, _ := out["tools"].([]any)
	if len(tools) != 1 || tools[0] != "manage_asset" {
		t.Errorf("tools = %v; want [manage_asset]", out["tools"])
	}
	findings, _ := out["findings"].([]any)
	var warned bool
	for _, f := range findings {
		row, _ := f.(map[string]any)
		msg, _ := row["message"].(string)
		if row["severity"] == "warning" && strings.Contains(msg, issue1834Undeclared) {
			warned = true
		}
		if strings.Contains(msg, "mcp://global/brand/logo.svg") {
			t.Errorf("the declared reference was reported: %v", row)
		}
	}
	if !warned {
		t.Errorf("no warning names the undeclared reference: %v", findings)
	}
}

// TestIssue1834_ADraftReportsWhatItWouldDeclare: a preview declares nothing,
// and says what it would declare and what it names undeclared.
func TestIssue1834_ADraftReportsWhatItWouldDeclare(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ran := c.call("manage_script", map[string]any{
		"command": "run_draft",
		"name":    "acc-1834-draft-" + stamp,
		"source":  fmt.Sprintf(issue1834Source, "mcp://global/brand/logo.svg", issue1834Undeclared),
	})
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("the draft did not succeed: %v", ran)
	}
	exports, _ := ran["exports"].([]any)
	if len(exports) != 1 {
		t.Fatalf("exports = %v; want one", ran["exports"])
	}
	export, _ := exports[0].(map[string]any)
	if export["preview"] != true {
		t.Errorf("a draft without allow_writes wrote its export: %v", export)
	}
	refs, _ := export["references"].([]any)
	if len(refs) != 1 || refs[0] != "mcp://global/brand/logo.svg" {
		t.Errorf("references = %v; want the declaration it would make", export["references"])
	}
	undeclared, _ := export["undeclared_references"].([]any)
	if len(undeclared) != 1 || undeclared[0] != issue1834Undeclared {
		t.Errorf("undeclared_references = %v", export["undeclared_references"])
	}
}

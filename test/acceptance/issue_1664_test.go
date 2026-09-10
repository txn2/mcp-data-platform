//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Issue #1664: manage_script run_draft was documented as executing a script
// "persisting nothing". That held for platform.export, platform.publish_data
// and platform.save_state, which report what they would have written. It did
// not hold for anything reached through platform.call: manage_resource create,
// manage_table register, api_export, trino_execute and the rest executed for
// real inside a draft. For an ingestion script that is nearly every side effect
// it has, and because a draft is not recorded as a run, a resource created that
// way had no run record explaining where it came from.
//
// A draft now refuses a write-class platform.call and names it, and allow_writes
// lifts that for one run and reports what it wrote.
//
// Wire forms: every parameter this file touches is typed and admits one JSON
// form -- manage_script's command, name, description, source and allow_writes
// (boolean), manage_resource's action, filename, content and content_type, and
// api_invoke_endpoint's connection, method, path and operation_id. allow_writes
// is sent as a JSON boolean in both of the values the schema admits, true and
// false, and omitted entirely for the third case the default has to cover.

// draftLandsAResource1664 is the shape the issue reports: a script whose whole
// purpose is landing something.
const draftLandsAResource1664 = `
res = platform.call("manage_resource", {
    "action": "create",
    "filename": "acceptance-1664.txt",
    "display_name": "Acceptance 1664",
    "path": "acceptance",
    "description": "Acceptance #1664: filed by a draft that was allowed to write.",
    "content": "landed by a draft\n",
    "content_type": "text/plain",
})
print("resource_id=" + str(res.get("resource_id", "")))
`

// draftReadsOnly1664 reaches the read half of the management surface, plus an
// api pull.
const draftReadsOnly1664 = `
assets = platform.call("manage_asset", {"action": "list", "limit": 1})
print("assets=" + str(type(assets)))
pulled = platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "method": "GET",
    "path": "/v1/pagination/link",
    "purpose": "Acceptance #1664: a draft's read-only pull is not a write.",
})
print("status=" + str(pulled.get("status", 0)))
`

// authorScript1664 creates a script under a name this file owns and removes it
// when the test ends, so a suite run leaves the deployment as it found it.
func authorScript1664(t *testing.T, c *client, name, source string) {
	t.Helper()
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1664: a draft does not write through platform.call.",
		"source":      source,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
}

// draftRun1664 runs a draft and returns the tool's answer, whether the script
// succeeded or failed: a refusal is a failed run reported normally, not a tool
// error.
func draftRun1664(t *testing.T, c *client, args map[string]any) map[string]any {
	t.Helper()
	args["command"] = "run_draft"
	return c.call("manage_script", args)
}

// resourceNamed1664 reports whether a resource with the given filename exists
// in the caller's library, which is the observable the issue's reproduction
// checks after the draft.
func resourceNamed1664(t *testing.T, c *client, filename string) (id string, found bool) {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources?search="+filename, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/resources: status %d: %v", status, body)
	}
	rows, _ := body["resources"].([]any)
	for _, row := range rows {
		item, _ := row.(map[string]any)
		if item == nil {
			continue
		}
		if name, _ := item["filename"].(string); name == filename {
			resourceID, _ := item["id"].(string)
			return resourceID, true
		}
	}
	return "", false
}

// TestIssue1664_ADraftDoesNotLandThroughPlatformCall is the issue's own
// reproduction: draft a script that creates a resource, then look in the
// resource library.
func TestIssue1664_ADraftDoesNotLandThroughPlatformCall(t *testing.T) {
	c := connect(t)
	const name = "acceptance-1664-barred"
	authorScript1664(t, c, name, draftLandsAResource1664)

	// The default: allow_writes omitted entirely.
	ran := draftRun1664(t, c, map[string]any{"name": name})

	if status, _ := ran["status"].(string); status != "failed" {
		t.Fatalf("a draft that writes must stop at the write, got status %v: %v", ran["status"], ran)
	}
	errText, _ := ran["error"].(string)
	if !strings.Contains(errText, "manage_resource action=create persists outside this run") {
		t.Errorf("the failure must name the call that persists, got: %s", errText)
	}
	if !strings.Contains(errText, "allow_writes") {
		t.Errorf("the failure must say how to proceed, got: %s", errText)
	}

	refused, _ := ran["refused_write"].(map[string]any)
	if refused == nil {
		t.Fatalf("the response must name the refused call structurally: %v", ran)
	}
	if got, _ := refused["call"].(string); got != "manage_resource action=create" {
		t.Errorf("refused_write.call = %q, want manage_resource action=create", got)
	}

	if message, _ := ran["message"].(string); strings.Contains(message, "Nothing was persisted") {
		t.Errorf("a refused draft is not described as a run that deliberately wrote nothing: %s", message)
	}

	if id, found := resourceNamed1664(t, c, "acceptance-1664.txt"); found {
		t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
		t.Fatalf("the draft created the resource %s; a draft must not land through platform.call", id)
	}
}

// TestIssue1664_AllowWritesLandsAndReportsIt is the opt-in the barrier is
// paired with: an author who needs the create to happen asks for it, and is
// told what the run wrote.
func TestIssue1664_AllowWritesLandsAndReportsIt(t *testing.T) {
	c := connect(t)
	const name = "acceptance-1664-allowed"
	authorScript1664(t, c, name, draftLandsAResource1664)

	ran := draftRun1664(t, c, map[string]any{"name": name, "allow_writes": true})

	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("run_draft allow_writes=true did not succeed: %v", ran)
	}

	writes, _ := ran["writes"].([]any)
	if len(writes) != 1 {
		t.Fatalf("the response must list the one call that persisted, got %v", ran["writes"])
	}
	first, _ := writes[0].(map[string]any)
	if got, _ := first["call"].(string); got != "manage_resource action=create" {
		t.Errorf("writes[0].call = %q, want manage_resource action=create", got)
	}

	message, _ := ran["message"].(string)
	if strings.Contains(message, "Nothing was persisted") {
		t.Errorf("a run that persisted must not say nothing was persisted: %s", message)
	}

	id, found := resourceNamed1664(t, c, "acceptance-1664.txt")
	if !found {
		t.Fatalf("run_draft allow_writes=true did not create the resource")
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
}

// TestIssue1664_ADraftStillReads keeps the barrier from making a draft useless.
// The read half of an action tool goes through, and so does an api pull
// addressed by method and path.
func TestIssue1664_ADraftStillReads(t *testing.T) {
	c := connect(t)
	const name = "acceptance-1664-reader"
	authorScript1664(t, c, name, draftReadsOnly1664)

	// allow_writes sent explicitly false, the schema's other admitted value.
	ran := draftRun1664(t, c, map[string]any{"name": name, "allow_writes": false})

	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("a draft that only reads must succeed: %v", ran)
	}
	if writes, _ := ran["writes"].([]any); len(writes) != 0 {
		t.Errorf("a read is not recorded as a write: %v", writes)
	}
	if ran["refused_write"] != nil {
		t.Errorf("nothing was refused, so nothing is named: %v", ran["refused_write"])
	}
	log, _ := ran["log"].(string)
	if !strings.Contains(log, "status=200") {
		t.Errorf("the api pull must have reached the fixture, log: %s", log)
	}
	message, _ := ran["message"].(string)
	if !strings.Contains(message, "Nothing was persisted") {
		t.Errorf("a draft that wrote nothing says so: %s", message)
	}
}

// TestIssue1664_AnOperationIDIsClassifiedByWhatItSends covers the addressing
// api_discover hands an author. The gateway resolves the id to its method, so a
// read-only pull is not refused for being addressed the way the platform names
// it, and a write addressed the same way still is.
func TestIssue1664_AnOperationIDIsClassifiedByWhatItSends(t *testing.T) {
	c := connect(t)
	read := operationID1664(t, c, "GET")
	write := operationID1664(t, c, "POST")

	const readName = "acceptance-1664-op-read"
	authorScript1664(t, c, readName, fmt.Sprintf(`
res = platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "operation_id": %q,
    "purpose": "Acceptance #1664: a pull addressed by operation id.",
})
print("status=" + str(res.get("status", 0)))
`, read))
	ran := draftRun1664(t, c, map[string]any{"name": readName})
	if status, _ := ran["status"].(string); status != "succeeded" {
		t.Fatalf("a GET addressed by operation_id must not be refused: %v", ran)
	}

	const writeName = "acceptance-1664-op-write"
	authorScript1664(t, c, writeName, fmt.Sprintf(`
platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "operation_id": %q,
    "body": {},
    "purpose": "Acceptance #1664: a write addressed by operation id.",
})
`, write))
	ran = draftRun1664(t, c, map[string]any{"name": writeName})
	if status, _ := ran["status"].(string); status != "failed" {
		t.Fatalf("a POST addressed by operation_id must be refused: %v", ran)
	}
	refused, _ := ran["refused_write"].(map[string]any)
	if refused == nil {
		t.Fatalf("the refusal must name the call: %v", ran)
	}
	if call, _ := refused["call"].(string); !strings.HasPrefix(call, "api_invoke_endpoint") {
		t.Errorf("refused_write.call = %q, want an api_invoke_endpoint call", call)
	}
}

// operationID1664 finds an operation on the api fixture connection that uses
// the given HTTP method, so the test addresses a real id rather than one it
// invented.
func operationID1664(t *testing.T, c *client, method string) string {
	t.Helper()
	out := c.call("api_discover", map[string]any{
		"connection": "api-test-fixture",
		"query":      "",
	})
	operations, _ := out["operations"].([]any)
	for _, row := range operations {
		op, _ := row.(map[string]any)
		if op == nil {
			continue
		}
		if got, _ := op["method"].(string); !strings.EqualFold(got, method) {
			continue
		}
		if id, _ := op["operation_id"].(string); id != "" {
			return id
		}
	}
	t.Skipf("the api-test-fixture catalog declares no %s operation to address by id", method)
	return ""
}

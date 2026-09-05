//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Issue #1624: every call a managed script run made was cataloged as material
// a later session might re-run, and the persona exclusion #1614 added could not
// keep it out, because a run presents the persona of the person who wrote the
// script. On the deployment measured, 76% of the catalog was the six calls one
// hourly schedule makes. A run is by construction the re-run, so its calls are
// audited and not cataloged, keyed on the source the run arrives under rather
// than on a persona nobody can name.
//
// Wire forms: every parameter this file touches is typed and admits one JSON
// form -- manage_script's command, name, description, source and state_action,
// run_script's name and wait_seconds, api_invoke_endpoint's connection, method
// and path, save_asset's name, content and content_type, manage_asset's action
// and asset_id. The admin routes take their filters as query strings. The
// script's own calls send the same typed parameters from Starlark.

const (
	// scriptCallPath is the fixture endpoint a run reaches, distinct from the
	// one every other acceptance file uses so a criterion counting a run's
	// calls is not counting another suite's.
	scriptCallPath = "/v1/pagination/link"
)

// scriptCallsAnEndpoint1624 makes one cataloged API call and saves what the
// result carried: whether the platform handed the run a citation token, and
// the run's own session, which is the key its audit rows join on.
const scriptCallsAnEndpoint1624 = `
res = platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "method": "GET",
    "path": "/v1/pagination/link",
    "purpose": "Acceptance #1624: a run fetches one upstream page.",
})
platform.save_state({"has_reference": str("call_reference" in res)})
`

// runScript1624 creates a script and runs it to completion, failing the test
// when the run does not succeed. The script is deleted at the end of the test,
// so a suite run leaves the deployment as it found it.
func runScript1624(t *testing.T, c *client, name, source string) {
	t.Helper()
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1624: a run's calls are audited and not cataloged.",
		"source":      source,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("run did not succeed: %v", run)
	}
}

// searchAttempts1624 bounds how long the suite waits for a written record to
// become findable in the calls source.
const searchAttempts1624 = 30

// scriptPrincipal1624 is the user id a run's calls are audited under.
func scriptPrincipal1624(name string) string { return "script:" + name }

// awaitAudited waits for the audit rows of a run's calls, which are written on
// the audit writer's drain goroutine after the run has answered. Every
// criterion below is about a catalog row being absent, and "absent" and "not
// yet written" read the same, so the suite waits for the audit row it expects
// before asserting about the catalog row it does not.
func awaitAudited(t *testing.T, admin *client, principal string) []any {
	t.Helper()
	for attempt := range recordAttempts {
		if attempt > 0 {
			time.Sleep(recordRetryPause)
		}
		rows := admin.list("/api/v1/admin/audit/events?event_kind=apigateway_invoke&user_id=" +
			principal + "&per_page=50")
		if len(rows) > 0 {
			return rows
		}
	}
	t.Fatalf("no audit event for %s after %s; a run's calls must still be audited",
		principal, time.Duration(recordAttempts)*recordRetryPause)
	return nil
}

// callTotal reads the number of catalog records a listing reports, which is
// the count the ticket measured the deployment by.
func callTotal(c *client, query string) float64 {
	c.t.Helper()
	status, body := c.rest("GET", "/api/v1/admin/calls?per_page=1&"+query, nil)
	if status != 200 {
		c.t.Fatalf("GET /api/v1/admin/calls?%s: status %d, body %v", query, status, body)
	}
	total, _ := body["total"].(float64)
	return total
}

// TestIssue1624_ARunsCallsAreAuditedAndNotCataloged is criterion 1: the audit
// log still holds everything the run did, and the catalog holds none of it.
func TestIssue1624_ARunsCallsAreAuditedAndNotCataloged(t *testing.T) {
	admin := connect(t)
	const name = "acceptance-1624-audited"
	runScript1624(t, admin, name, scriptCallsAnEndpoint1624)
	principal := scriptPrincipal1624(name)

	events := awaitAudited(t, admin, principal)
	ev, _ := events[0].(map[string]any)
	if got, _ := ev["tool_name"].(string); got != "api_invoke_endpoint" {
		t.Errorf("audit tool_name = %v, want api_invoke_endpoint (event: %v)", ev["tool_name"], ev)
	}
	if got, _ := ev["source"].(string); got != "script" {
		t.Errorf("audit source = %v, want script; the exclusion is keyed on it (event: %v)", ev["source"], ev)
	}
	for _, field := range []string{"id", "user_id", "timestamp", "duration_ms", "connection"} {
		if ev[field] == nil || ev[field] == "" {
			t.Errorf("audit event field %s is empty; the audit row must be complete: %v", field, ev)
		}
	}

	if total := callTotal(admin, "user_id="+principal); total != 0 {
		t.Errorf("the call catalog holds %v record(s) for %s; a run's calls must not be cataloged",
			total, principal)
	}
}

// TestIssue1624_ARunIsHandedNoCallReference is criterion 2: a run gets no
// citation token, because the id would name a record the catalog declined to
// write. The control is this client's own call, which still carries one.
func TestIssue1624_ARunIsHandedNoCallReference(t *testing.T) {
	admin := connect(t)
	const name = "acceptance-1624-reference"
	runScript1624(t, admin, name, scriptCallsAnEndpoint1624)

	got := admin.call("manage_script", map[string]any{
		"command": "state", "name": name, "state_action": "get",
	})
	state, _ := got["state"].(map[string]any)
	if state["has_reference"] != "False" {
		t.Errorf("has_reference = %v; a run's tool result must carry no call_reference", state["has_reference"])
	}

	// The control: the same tool, the same deployment, called by a person.
	// Without it, an absent reference could mean the stamp stopped working for
	// everyone rather than being withheld from a run.
	if !invokeCarriesReference1624(admin, "Acceptance #1624: the control call a person makes.") {
		t.Error("a person's own call must still be handed its call reference")
	}
}

// invokeCarriesReference1624 makes one API call as this client and reports
// whether the platform stamped the call's own reference on the result. The
// reference is appended as its own content block, so every block is read
// rather than the first.
func invokeCarriesReference1624(c *client, purpose string) bool {
	c.t.Helper()
	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection,
		"method":     "GET",
		"path":       scriptCallPath,
		"purpose":    purpose,
	})
	if err != nil {
		c.t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	if res.IsError {
		c.t.Fatalf("api_invoke_endpoint: tool error: %s", text)
	}
	for _, content := range res.Content {
		block, ok := content.(*mcp.TextContent)
		if ok && strings.Contains(block.Text, middleware.CallReferenceKey) {
			return true
		}
	}
	return false
}

// TestIssue1624_APersonsCallInTheSamePersonaIsStillCataloged is criterion 4:
// the exclusion is by how the call arrived, not by the persona the script's
// author holds. The script is written and run by an ordinary person, so the
// run presents that person's own persona, and the same person's own call in an
// ordinary session is cataloged and findable.
func TestIssue1624_APersonsCallInTheSamePersonaIsStillCataloged(t *testing.T) {
	admin := connect(t)
	person := connectAs(t, devPeerAPIKey)
	const name = "acceptance-1624-control"
	runScript1624(t, person, name, scriptCallsAnEndpoint1624)
	awaitAudited(t, admin, scriptPrincipal1624(name))

	// A nonce, not a sentence: the calls source ranks by meaning as well as by
	// words, and the dev stack carries many calls whose purposes read like this
	// one. A token nothing else contains is what makes "not found" mean not
	// recorded.
	marker := fmt.Sprintf("acceptance1624nonce%d", time.Now().UnixNano())
	if !invokeCarriesReference1624(person, marker) {
		t.Error("a person's call in the script author's own persona must still be handed its reference")
	}

	// Cataloged: read from the catalog listing by the caller's own session, so
	// "recorded" is a row and not a ranking.
	rec := awaitRecord(admin, person.sessionID)
	if got, _ := rec["persona"].(string); got == "" {
		t.Errorf("the control record carries no persona: %v", rec)
	}

	// And findable, which is the half the ticket measured: hundreds of a
	// schedule's identical refresh calls were pushing a person's exact hit off
	// the fused page.
	if !findsCall1624(person, marker) {
		t.Error("a person's call in the script author's own persona must be findable in the calls source")
	}

	if total := callTotal(admin, "user_id="+scriptPrincipal1624(name)); total != 0 {
		t.Errorf("the run's calls must stay out of the catalog while the person's goes in, got total %v", total)
	}
}

// findsCall1624 reports whether the caller's own calls search returns a record
// carrying the marker, retrying while the record may still be in flight.
func findsCall1624(c *client, marker string) bool {
	c.t.Helper()
	// A longer window than a catalog read: the record is embedded before the
	// semantic arm can rank it, and the lexical arm reads an index the write
	// has to reach first.
	for attempt := range searchAttempts1624 {
		if attempt > 0 {
			time.Sleep(time.Second)
		}
		for _, hit := range callHits(c.call("search", map[string]any{
			"intent":  marker,
			"sources": []any{"calls"},
			"limit":   25,
			"purpose": "Acceptance #1624: read the calls source for one recorded call.",
		})) {
			if text, _ := hit["text"].(string); strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

// scriptSavesAnAsset1624 makes a data call and saves an asset from it, which is
// the write path that captures provenance. The run cites nothing -- it has no
// reference to cite -- so the capture must come from the run's own session
// window.
const scriptSavesAnAsset1624 = `
platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "method": "GET",
    "path": "/v1/pagination/link",
    "purpose": "Acceptance #1624: the call an asset a run writes was built from.",
})
saved = platform.call("save_asset", {
    "name": "Acceptance 1624 run output",
    "content": "region,amount\nwest,10\n",
    "content_type": "text/csv",
})
platform.save_state({"asset_id": str(saved["asset_id"])})
`

// TestIssue1624_AnAssetARunWritesStillRecordsItsCalls is criterion 3, stated
// as the platform implements it. Provenance capture reads audit rows, not
// catalog rows (#1320), so an asset a run writes still records the calls that
// fed it -- through the run's session window, since the run was handed no
// reference to cite.
func TestIssue1624_AnAssetARunWritesStillRecordsItsCalls(t *testing.T) {
	admin := connect(t)
	const name = "acceptance-1624-provenance"
	runScript1624(t, admin, name, scriptSavesAnAsset1624)

	got := admin.call("manage_script", map[string]any{
		"command": "state", "name": name, "state_action": "get",
	})
	state, _ := got["state"].(map[string]any)
	assetID, _ := state["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("the run recorded no asset id: %v", state)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})

	out := admin.call("manage_asset", map[string]any{
		"action":   "provenance",
		"asset_id": assetID,
	})
	if !provenanceNamesACall1624(out) {
		t.Errorf("an asset a run writes must still record the calls that fed it by event id: %v", out)
	}
}

// provenanceNamesACall1624 reports whether a provenance page names at least one
// call by its audit event id.
func provenanceNamesACall1624(out map[string]any) bool {
	captures, _ := out["captures"].([]any)
	for _, entry := range captures {
		capture, _ := entry.(map[string]any)
		ids, _ := capture["event_ids"].([]any)
		for _, id := range ids {
			if s, _ := id.(string); s != "" {
				return true
			}
		}
	}
	return false
}

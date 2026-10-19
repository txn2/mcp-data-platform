//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2172: a request to a bound route runs the script inline.
// A handler that runs a Trino query answers with its rows, and the request's
// audit row names the endpoint and the script principal, whose query is
// audited under the same session; after a new version is saved the next
// request on the other replica runs it; a handler past its timeout gets 504,
// with the failure listed on the script's page; a request writes no
// script_runs row; a body past the limit gets 413 without running the script;
// and the handler reads a JSON body and a form body.
//
// Wire forms: the endpoint is sent a JSON body (application/json) and a form
// body (application/x-www-form-urlencoded), and no body; manage_script's
// endpoint_set takes typed arguments sent as literal tools/call params.

const issue2172TrinoHandler = `def handle(req):
    """Answers with one row of the warehouse."""
    rows = platform.query(connection = "acme", sql = "SELECT 'shipped' AS status, :id AS id", params = {"id": req["params"]["id"]})["rows"]
    return {"json": rows}
`

// TestIssue2172_ATrinoQueryAnswersWithItsRowsAndTheAuditNamesTheEndpoint.
func TestIssue2172_ATrinoQueryAnswersWithItsRowsAndTheAuditNamesTheEndpoint(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2172-trino")
	saveEndpointScript(t, admin, name, issue2172TrinoHandler, `{"params": {"id": "42"}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "path": "/{id}", "auth_mode": "signed_in"})

	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name+"/42", bearer(admin.apiKey), "")
	if got.status != http.StatusOK || !strings.Contains(got.body, `"status":"shipped"`) || !strings.Contains(got.body, `"id":"42"`) {
		t.Fatalf("GET: %d %s", got.status, got.body)
	}

	var row map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for row == nil && time.Now().Before(deadline) {
		for _, raw := range admin.list("/api/v1/admin/audit/events?event_kind=endpoint_request&tool_name=endpoint:" + name) {
			row, _ = raw.(map[string]any)
		}
		if row == nil {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if row == nil {
		t.Fatalf("no endpoint_request audit row for %s", name)
	}
	if row["user_id"] != "script:"+name || row["source"] != "endpoint" {
		t.Fatalf("the request's audit row: want script:%s from source endpoint, got %v", name, row)
	}
	params, _ := row["parameters"].(map[string]any)
	if params["endpoint"] != name || fmt.Sprint(params["status"]) != "200" {
		t.Fatalf("the request's audit parameters: %v", params)
	}
	session, _ := row["session_id"].(string)
	calls := admin.list("/api/v1/admin/audit/events?session_id=" + session + "&tool_name=trino_query")
	if len(calls) == 0 {
		t.Fatalf("the handler's trino_query is not audited under the request's session %s", session)
	}
	call, _ := calls[0].(map[string]any)
	if call["user_id"] != "script:"+name {
		t.Fatalf("the handler's query ran as %v, want script:%s", call["user_id"], name)
	}
}

// TestIssue2172_TheNextRequestOnTheOtherReplicaRunsTheNewVersion: the
// version is saved through one replica and the next request to the other
// answers with it.
func TestIssue2172_TheNextRequestOnTheOtherReplicaRunsTheNewVersion(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := endpointName("acc-2172-version")
	answer := func(v string) string {
		return "def handle(_req):\n    \"\"\"Answers with its version.\"\"\"\n    return {\"json\": {\"version\": \"" + v + "\"}}\n"
	}
	saveEndpointScript(t, a, name, answer("one"), `{"params": {}, "caller": None}`)
	bindEndpoint(t, a, name, "", map[string]any{"endpoint": name, "auth_mode": "signed_in"})
	if got := endpointRequest(t, b.base, http.MethodGet, "/endpoints/"+name, bearer(b.apiKey), ""); !strings.Contains(got.body, `"one"`) {
		t.Fatalf("the other replica before the save: %d %s", got.status, got.body)
	}
	a.saveEdit(map[string]any{"command": "update", "name": name, "source": answer("two") + draftMain(`{"params": {}, "caller": None}`)}, nil)
	if got := endpointRequest(t, b.base, http.MethodGet, "/endpoints/"+name, bearer(b.apiKey), ""); !strings.Contains(got.body, `"two"`) {
		t.Fatalf("the other replica's next request after the save: %d %s", got.status, got.body)
	}
}

// TestIssue2172_PastItsTimeoutIs504AndTheFailureIsListed.
func TestIssue2172_PastItsTimeoutIs504AndTheFailureIsListed(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2172-timeout")
	id := saveEndpointScript(t, admin, name, `def handle(req):
    """Waits on an upstream for as long as the request asks."""
    platform.call("mcp-test-fixture__slow", {"milliseconds": int(req["params"].get("ms", "10")),
        "purpose": "Acceptance for #2172: hold a handler past its endpoint's timeout."})
    return {"json": req["params"]}
`, `{"params": {"ms": "10"}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "path": "/{ms}", "auth_mode": "signed_in", "timeout_seconds": 1})

	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name+"/3000", bearer(admin.apiKey), "")
	// The status is what reaches the caller: the dev stack's proxy replaces a
	// 504's body as a CDN in front of a deployment does (#1704), so the
	// failure's own text is read on the script's page below.
	if got.status != http.StatusGatewayTimeout {
		t.Fatalf("GET past the timeout: %d %s", got.status, got.body)
	}
	status, page := admin.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/endpoint", id), nil)
	failures, _ := page["failures"].([]any)
	if status != http.StatusOK || len(failures) == 0 {
		t.Fatalf("the script's page lists no failure: %d %v", status, page)
	}
	first, _ := failures[0].(map[string]any)
	if fmt.Sprint(first["status"]) != "504" || !strings.Contains(fmt.Sprint(first["error"]), "time limit") {
		t.Fatalf("the listed failure: %v", first)
	}
}

// TestIssue2172_ARequestWritesNoRunRow.
func TestIssue2172_ARequestWritesNoRunRow(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2172-runs")
	saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "signed_in"})
	for range 3 {
		if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, bearer(admin.apiKey), ""); got.status != http.StatusOK {
			t.Fatalf("GET: %d %s", got.status, got.body)
		}
	}
	runs := admin.call("manage_script", map[string]any{"command": "runs", "name": name})
	if list, _ := runs["runs"].([]any); len(list) != 0 {
		t.Fatalf("requests wrote run rows: %v", runs)
	}
}

// TestIssue2172_ABodyPastTheLimitIs413WithoutRunning: no audit row is written
// for the request, because the script never ran.
func TestIssue2172_ABodyPastTheLimitIs413WithoutRunning(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2172-body")
	saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"POST"}, "auth_mode": "signed_in", "max_body_bytes": 16})

	header := bearer(admin.apiKey)
	header.Set("Content-Type", "application/json")
	got := endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+name, header, `{"text":"`+strings.Repeat("x", 64)+`"}`)
	if got.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST past the limit: %d %s", got.status, got.body)
	}
	time.Sleep(time.Second) // the audit writer drains asynchronously; a row written for the request would be there by now
	if rows := admin.list("/api/v1/admin/audit/events?event_kind=endpoint_request&tool_name=endpoint:" + name); len(rows) != 0 {
		t.Fatalf("a refused body ran the script: %v", rows)
	}
}

// TestIssue2172_TheHandlerReadsAJSONBodyAndAForm.
func TestIssue2172_TheHandlerReadsAJSONBodyAndAForm(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2172-forms")
	saveEndpointScript(t, admin, name, `def handle(req):
    """Answers with what it was sent."""
    return {"json": {"json": req["json"], "form": req["form"], "method": req["method"]}}
`, `{"method": "POST", "json": None, "form": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"POST"}, "auth_mode": "signed_in"})

	header := bearer(admin.apiKey)
	header.Set("Content-Type", "application/json")
	got := endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+name, header, `{"email":"ann@example.com","n":2}`)
	if got.status != http.StatusOK || !strings.Contains(got.body, `"json":{"email":"ann@example.com","n":2}`) || !strings.Contains(got.body, `"form":null`) {
		t.Fatalf("a JSON body: %d %s", got.status, got.body)
	}
	header.Set("Content-Type", "application/x-www-form-urlencoded")
	got = endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+name, header, "email=ann%40example.com&n=2")
	if got.status != http.StatusOK || !strings.Contains(got.body, `"form":{"email":"ann@example.com","n":"2"}`) || !strings.Contains(got.body, `"json":null`) {
		t.Fatalf("a form body: %d %s", got.status, got.body)
	}
}

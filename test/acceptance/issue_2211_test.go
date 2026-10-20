//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2211: a GraphQL upstream that fails without an errors array
// is reported with its status, the start of its body and whether the same
// document is expected to get through later (a 503 to a query, never to a
// mutation), and a send nobody answered is a structured transport_error.
//
// The upstream is one of the platform's own endpoints (#2170): a script bound
// to an HTTP route that answers every POST with a 503, a Retry-After and a
// sentence, as a GraphQL endpoint behind a busy gateway does. Its schema is
// uploaded, as an operator does for an endpoint that does not introspect.
//
// Wire forms: graphql_query's query is a typed string sent as a literal
// tools/call parameter, as a query document and a mutation document; the
// schema upload is SDL text.

const issue2211SDL = "type Query { ping: String }\ntype Mutation { touch: String }\n"

// issue2211Busy is the handler of the busy upstream.
const issue2211Busy = `def handle(_req):
    """Answers every request as a gateway in front of a busy service does."""
    return {"status": 503, "headers": {"Retry-After": "7"}, "body": "the service is busy; try again shortly"}
`

// issue2211Connection registers a graphql connection at endpoint with the
// fixture schema uploaded, and returns its name. A non-empty token is sent
// as the connection's bearer credential.
func issue2211Connection(t *testing.T, c *client, label, endpoint, token string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2211-%s-%d", label, time.Now().UnixNano())
	cfg := map[string]any{"endpoint_url": endpoint, "connection_name": name, "connect_timeout": "3s", "call_timeout": "5s"}
	if token != "" {
		cfg["auth_mode"], cfg["credential"] = "bearer", token
	}
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/graphql/"+name, map[string]any{
		"config":      cfg,
		"description": "Acceptance 2211: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register %s connection: HTTP %d", label, status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/graphql/"+name, http.NoBody) })
	if code, _ := c.rest(http.MethodPost, "/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema", strings.NewReader(issue2211SDL)); code != http.StatusOK {
		t.Fatalf("upload the %s schema: HTTP %d", label, code)
	}
	return name
}

// TestIssue2211_A503CarriesItsBodyAndIsRetryableForAQueryOnly.
func TestIssue2211_A503CarriesItsBodyAndIsRetryableForAQueryOnly(t *testing.T) {
	c := connect(t)
	busy := endpointName("acc-2211-busy")
	saveEndpointScript(t, c, busy, issue2211Busy, `{"params": {}, "caller": None}`)
	bindEndpoint(t, c, busy, "", map[string]any{"endpoint": busy, "auth_mode": "signed_in", "methods": []any{"POST"}})
	name := issue2211Connection(t, c, "503", c.base+"/endpoints/"+busy, c.apiKey)
	query := c.call("graphql_query", map[string]any{
		"connection": name, "query": "{ ping }", "purpose": "Acceptance #2211: a 503 with no errors array.",
	})
	if fmt.Sprint(query["status"]) != "503" || query["upstream_error"] != true {
		t.Fatalf("the query's answer: %v", query)
	}
	if body, _ := query["upstream_body"].(string); body == "" {
		t.Fatalf("a 503 with no errors array carries no upstream_body: %v", query)
	}
	if query["upstream_retryable"] != true || fmt.Sprint(query["retry_after_seconds"]) != "7" {
		t.Fatalf("a 503 to a query is retryable after the upstream's Retry-After: %v", query)
	}
	if !strings.Contains(fmt.Sprint(query["upstream_body"]), "the service is busy") {
		t.Fatalf("upstream_body is not the upstream's answer: %v", query["upstream_body"])
	}
	mutation := c.call("graphql_query", map[string]any{
		"connection": name, "query": "mutation { touch }", "purpose": "Acceptance #2211: a 503 to a mutation.",
	})
	if _, ok := mutation["upstream_retryable"]; ok || fmt.Sprint(mutation["status"]) != "503" {
		t.Fatalf("a 503 to a mutation is not retryable: %v", mutation)
	}
}

// TestIssue2211_AnUnansweredSendIsAStructuredTransportError.
func TestIssue2211_AnUnansweredSendIsAStructuredTransportError(t *testing.T) {
	c := connect(t)
	name := issue2211Connection(t, c, "down", "http://127.0.0.1:1/graphql", "")
	res, text, err := c.callRaw("graphql_query", map[string]any{
		"connection": name, "query": "{ ping }", "purpose": "Acceptance #2211: an endpoint nobody answers at.",
	})
	if err != nil || !res.IsError {
		t.Fatalf("a send nobody answered is a failed call: %v %s", err, text)
	}
	for _, want := range []string{`"transport_error"`, `"upstream_unavailable"`, `"upstream_error":true`} {
		if !strings.Contains(strings.ReplaceAll(text, " ", ""), strings.ReplaceAll(want, " ", "")) {
			t.Errorf("the transport failure does not carry %s: %s", want, text)
		}
	}
}

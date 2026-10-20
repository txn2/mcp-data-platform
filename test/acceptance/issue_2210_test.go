//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Acceptance for #2210: a caller's timeout_seconds above the connection's
// call_timeout is replaced by it, and the result now says so on success and
// on failure, where the failure names the limit that stopped the call.
//
// Wire forms: timeout_seconds is a typed integer, sent as a literal tools/call
// parameter to graphql_query and to api_invoke_endpoint; the GraphQL fixture's
// document and the api call's query_params are a typed string and object.

// TestIssue2210_GraphQLReportsTheCapOnSuccess: a 240s ask on a connection
// whose call_timeout is 5s answers with the 5s it ran under and why.
func TestIssue2210_GraphQLReportsTheCapOnSuccess(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "cap2210", map[string]any{"call_timeout": "5s"})
	out := c.call("graphql_query", map[string]any{
		"connection": name, "query": "{ appConfig { appVersion } }", "timeout_seconds": 240,
		"purpose": "Acceptance #2210: a timeout above the connection's is reported as capped.",
	})
	if fmt.Sprint(out["timeout_applied_seconds"]) != "5" || out["timeout_capped_by"] != "connection call_timeout" ||
		fmt.Sprint(out["timeout_requested_seconds"]) != "240" {
		t.Fatalf("the cap is not reported: %v", out)
	}
	low := c.call("graphql_query", map[string]any{
		"connection": name, "query": "{ appConfig { appVersion } }", "timeout_seconds": 2,
		"purpose": "Acceptance #2210: a lower timeout is not a cap.",
	})
	if _, ok := low["timeout_capped_by"]; ok {
		t.Fatalf("a timeout under the connection's was reported as capped: %v", low)
	}
}

// TestIssue2210_AnAPICallStoppedByTheCapNamesIt: a request held past the
// connection's 1s call_timeout, asked for 240s, fails naming both.
func TestIssue2210_AnAPICallStoppedByTheCapNamesIt(t *testing.T) {
	c := connect(t)
	name := issue2209Connection(t, c, "cap2210", 0)
	status := c.restJSON("PUT", "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": map[string]any{
			"base_url": issue2209FixtureURL, "connection_name": name, "connect_timeout": "5s", "call_timeout": "1s",
		},
		"description": "Acceptance 2210: a one-second call timeout.",
	})
	if status != 200 && status != 201 {
		t.Fatalf("re-register with call_timeout 1s: HTTP %d", status)
	}
	text, ok := issue2209Slow(c, name, 3000, 240)
	want := "timed out after 1s (the connection's call_timeout; timeout_seconds 240 was capped)"
	if ok || !strings.Contains(text, want) {
		t.Fatalf("a request stopped by the capped timeout: ok=%v %s", ok, text)
	}
}

//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Issue #1587: the gateway returned up to max_response_bytes (10 MiB) inline,
// so a 3 MB response reached the agent whole and unflagged. The inline budget
// (max_inline_bytes, default 32 KiB and applied to the rendered tool result
// since issue #1606) is the model-context limit: past it the body is cut,
// body_truncated is set, and export_arguments names the api_export call that
// streams the same response into an asset. Every response reports body_bytes,
// the size read from the upstream. max_response_bytes stays the read cap.
const (
	issue1587SizedBytes     = 3_000_000
	issue1587DefaultInline  = 32 * 1024
	issue1587FixtureConn    = "api-test-fixture"
	issue1587FixtureDevKey  = "apitest-dev-key-2024"
	issue1587FixtureBaseURL = "http://localhost:9282"
)

// issue1587SizedArgs addresses the fixture's sized endpoint. The query value
// is untyped in the schema, so the two JSON forms it admits, a number and a
// string, are both sent by the criteria below.
func issue1587SizedArgs(connection string, bytes any) map[string]any {
	return map[string]any{
		"connection":   connection,
		"method":       "GET",
		"path":         "/v1/sized",
		"query_params": map[string]any{"bytes": bytes},
		"purpose":      "Acceptance: a response past the inline budget is cut and steered to api_export.",
	}
}

// assertCutAtInlineBudget asserts criteria 1 and 3: the response is cut, it
// says so, the body it returns is inside the budget the whole result is held
// to, and the steer carries the api_export arguments for the same call.
func assertCutAtInlineBudget(t *testing.T, out map[string]any, budget float64) {
	t.Helper()
	if truncated, _ := out["body_truncated"].(bool); !truncated {
		t.Fatalf("body_truncated = %v; want true", out["body_truncated"])
	}
	if got := number(t, out, "body_bytes"); got != budget {
		t.Errorf("body_bytes = %v; want the %v bytes the read cap allowed", got, budget)
	}
	body, _ := out["body"].(string)
	if len(body) == 0 || len(body) >= int(budget) {
		t.Errorf("body holds %d bytes; want a cut body inside the %v the whole result is held to", len(body), budget)
	}
	hint, _ := out["hint"].(string)
	if !strings.Contains(hint, "api_export") || !strings.Contains(hint, "max_inline_bytes") {
		t.Errorf("hint = %q; want it to name api_export and max_inline_bytes", hint)
	}
	if _, ok := out["export_arguments"].(map[string]any); !ok {
		t.Errorf("export_arguments = %v; want the api_export arguments", out["export_arguments"])
	}
}

func TestIssue1587_ALargeResponseIsNotReturnedWholeByDefault(t *testing.T) {
	c := connect(t)
	for name, bytes := range map[string]any{"number": issue1587SizedBytes, "string": "3000000"} {
		t.Run("bytes_as_"+name, func(t *testing.T) {
			out := c.call("api_invoke_endpoint", issue1587SizedArgs(issue1587FixtureConn, bytes))
			assertCutAtInlineBudget(t, out, issue1587DefaultInline)
			args, _ := out["export_arguments"].(map[string]any)
			if args["connection"] != issue1587FixtureConn || args["method"] != "GET" || args["path"] != "/v1/sized" {
				t.Errorf("export_arguments = %v; want the same connection, method and path", args)
			}
			q, _ := args["query_params"].(map[string]any)
			if q["bytes"] == nil {
				t.Errorf("export_arguments.query_params = %v; want the same query", args["query_params"])
			}
		})
	}
}

func TestIssue1587_EveryResponseCarriesBodyBytes(t *testing.T) {
	c := connect(t)
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": issue1587FixtureConn,
		"method":     "GET",
		"path":       "/v1/whoami",
		"purpose":    "Acceptance: a small response reports the size of the body it returned.",
	})
	if got := number(t, out, "body_bytes"); got <= 0 {
		t.Errorf("body_bytes = %v; want the size of the returned body", got)
	}
	if out["body_truncated"] != nil || out["export_arguments"] != nil {
		t.Errorf("a response under the budget is not flagged: truncated=%v export=%v", out["body_truncated"], out["export_arguments"])
	}
}

func TestIssue1587_ExportStreamsTheWholeResponse(t *testing.T) {
	c := connect(t)
	args := issue1587SizedArgs(issue1587FixtureConn, issue1587SizedBytes)
	args["name"] = "issue-1587-sized"
	args["purpose"] = "Acceptance: api_export is unaffected by the inline budget."
	out := c.call("api_export", args)
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("api_export returned no asset_id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})
	if got := number(t, out, "size_bytes"); got < issue1587SizedBytes {
		t.Errorf("size_bytes = %v; want the whole %d-byte response streamed to the asset", got, issue1587SizedBytes)
	}
}

func TestIssue1587_TheUtilConnectionIsHeldToTheSameBudget(t *testing.T) {
	c := connect(t)
	target := apiTestFixtureURL() + "/v1/sized?bytes=3000000"
	forms := map[string]any{
		"object": map[string]any{"url": target},
		"string": `{"url": "` + target + `"}`,
	}
	for name, body := range forms {
		t.Run("body_as_"+name, func(t *testing.T) {
			out := c.call("api_invoke_endpoint", map[string]any{
				"connection": "util",
				"method":     "POST",
				"path":       "/util/fetch",
				"body":       body,
				"purpose":    "Acceptance: a fetch_url response past the inline budget is cut like any other.",
			})
			assertCutAtInlineBudget(t, out, issue1587DefaultInline)
		})
	}
}

// issue1587Connection registers a fixture-backed connection with its own
// inline budget through the admin API, the way an operator raises it, and
// removes it when the test ends.
func issue1587Connection(t *testing.T, c *client, name string, inline int) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"config": map[string]any{
			"base_url":          issue1587FixtureBaseURL,
			"auth_mode":         "api_key",
			"credential":        issue1587FixtureDevKey,
			"api_key_placement": "header",
			"api_key_header":    "X-API-Key",
			"connection_name":   name,
			"max_inline_bytes":  inline,
		},
		"description": "Acceptance #1587: a connection with its own inline budget",
	})
	status, out := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, strings.NewReader(string(body)))
	if status/100 != 2 {
		t.Fatalf("PUT connection %s: %d %v", name, status, out)
	}
	t.Cleanup(func() {
		_, _ = c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
}

func TestIssue1587_AnOperatorRaisesTheInlineBudgetForAConnection(t *testing.T) {
	c := connect(t)
	issue1587Connection(t, c, "issue-1587-wide", 4*1024*1024)
	out := c.call("api_invoke_endpoint", issue1587SizedArgs("issue-1587-wide", issue1587SizedBytes))
	if out["body_truncated"] != nil {
		t.Fatalf("body_truncated = %v under a 4 MiB budget; want the whole response", out["body_truncated"])
	}
	if got := number(t, out, "body_bytes"); got < issue1587SizedBytes {
		t.Errorf("body_bytes = %v; want at least %d", got, issue1587SizedBytes)
	}
}

func TestIssue1587_AWalkMergesUnderTheInlineBudget(t *testing.T) {
	c := connect(t)
	issue1587Connection(t, c, "issue-1587-narrow", 2048)
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": "issue-1587-narrow",
		"method":     "GET",
		"path":       "/v1/pagination/link",
		"paginate":   map[string]any{"items": "items"},
		"purpose":    "Acceptance: a walk stops merging at the inline budget and steers to api_export.",
	})
	if truncated, _ := out["body_truncated"].(bool); out["stopped_by"] != "max_bytes" || !truncated {
		t.Fatalf("stopped_by=%v truncated=%v; want the walk cut at the inline budget", out["stopped_by"], out["body_truncated"])
	}
	if got := number(t, out, "body_bytes"); got <= 0 || got > 2048 {
		t.Errorf("body_bytes = %v; want the merged size under 2048", got)
	}
	args, _ := out["export_arguments"].(map[string]any)
	if _, ok := args["paginate"].(map[string]any); !ok {
		t.Errorf("export_arguments = %v; want the paginate block carried into the api_export call", out["export_arguments"])
	}
}

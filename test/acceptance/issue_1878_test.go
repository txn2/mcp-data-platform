//go:build integration

package acceptance

// Issue #1878: the context budget on a tool result was put inside
// api_invoke_endpoint, so the REST gateway, which calls that tool, cut every
// response to a model's budget and returned it as a success: WebDAV listings
// arrived as unparseable XML and downloads arrived as prefixes. Every other
// tool that can return too much had no budget at all.
//
// The budget is now the platform's (tools.result_budget, default 32 KiB),
// enforced once on the MCP response to a model, and only on results the
// model can recover the rest of: api_invoke_endpoint, graphql_query and
// trino_query, each cut in its own shape and steered to its export tool.
// Everything else -- a knowledge page, a tool's help, a proxied tool's
// output -- reaches the model whole. A REST
// gateway call, a managed script's run and an admin call are never fitted and
// meet only a connection's max_response_bytes, past which the REST route
// answers 413.
//
// Every criterion runs against the running platform and the upstreams the dev
// stack runs: the api-test fixture, the mcp-test fixture, the stack's Trino,
// and DataHub's GMS at /api/graphql for the GraphQL kind.
//
// Wire forms: api_invoke_endpoint's query_params values are untyped, so
// query_params.bytes is sent as a number and as a string, on the MCP tool and
// in the REST request body alike. graphql_query's variables is typed
// ["object", "string"] and is sent in both forms. trino_query's sql, format
// and connection are strings only and limit an integer; mcp-test's
// long_output takes integers only; manage_script and run_script take the
// typed forms #1606's script criterion lists, and manage_script's command a
// string only.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	issue1878Budget = 32 * 1024
	// issue1878Bytes is a response well past the budget and well inside the
	// default 10 MiB read cap.
	issue1878Bytes = 200_000
	// issue1878WideFields is how many fields the wide GraphQL document
	// selects: enough that its answer is past the budget even compactly.
	issue1878WideFields = 600
	// issue1878ReadCap is the read cap of the connection the 413 criterion
	// registers, under issue1878Bytes.
	issue1878ReadCap = 65_536
	issue1878Purpose = "Acceptance #1878: the context budget applies to a model's MCP results only."
	// issue1878SizedResponse is what the fixture's sized endpoint sends for
	// issue1878Bytes: the content wrapped in its JSON envelope.
	issue1878SizedResponse = 200_026
)

// issue1878SearchDocument repeats one catalog search under n aliases: a
// small document whose answer is n times the search's, so it passes the
// budget while the document, echoed in export_arguments, does not.
func issue1878SearchDocument(n int) string {
	var b strings.Builder
	b.WriteString("query Acc1878Search($show: Boolean!) { ")
	for i := range n {
		fmt.Fprintf(&b, `s%02d: searchAcrossEntities(input: {query: "*", start: 0, count: 100}) @include(if: $show) { searchResults { entity { urn type } } } `, i)
	}
	b.WriteString("}")
	return b.String()
}

// issue1878SearchAliases is how many times the search is repeated.
const issue1878SearchAliases = 12

// issue1878WideDocument selects __typename under n aliases, each long enough
// that the answer passes the budget with few tokens in the document. Every
// GraphQL endpoint answers it, whatever data it holds. The first field takes
// the $show variable so both wire forms of variables reach the endpoint.
func issue1878WideDocument(n int) string {
	var b strings.Builder
	b.WriteString("query Acc1878Wide($show: Boolean!) { ")
	for i := range n {
		fmt.Fprintf(&b, "f%04d_%s: __typename", i, strings.Repeat("w", 50))
		if i == 0 {
			b.WriteString(" @include(if: $show)")
		}
		b.WriteString(" ")
	}
	b.WriteString("}")
	return b.String()
}

func issue1878SizedArgs(connection string, bytes any) map[string]any {
	return map[string]any{
		"connection":   connection,
		"method":       "GET",
		"path":         "/v1/sized",
		"query_params": map[string]any{"bytes": bytes},
		"purpose":      issue1878Purpose,
	}
}

var issue1878BytesForms = map[string]any{"number": issue1878Bytes, "string": "200000"}

// issue1878RESTInvoke posts one call to the REST gateway and returns the
// status and decoded body.
func issue1878RESTInvoke(t *testing.T, c *client, connection string, bytes any) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPost, "/api/v1/gateway/"+connection+"/invoke", jsonBody(t, map[string]any{
		"method":       "GET",
		"path":         "/v1/sized",
		"query_params": map[string]any{"bytes": bytes},
	}))
}

// issue1878ResultSize is the size of every text block of a result, what a
// client renders.
func issue1878ResultSize(res *mcp.CallToolResult) int {
	n := 0
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			n += len(tc.Text)
		}
	}
	return n
}

// TestIssue1878_RESTInvokeReturnsTheWholeBody: a REST /invoke call whose
// upstream body is past the MCP budget but inside max_response_bytes returns
// the full body.
func TestIssue1878_RESTInvokeReturnsTheWholeBody(t *testing.T) {
	c := connect(t)
	for form, bytes := range issue1878BytesForms {
		t.Run("bytes_as_"+form, func(t *testing.T) {
			status, out := issue1878RESTInvoke(t, c, issue1587FixtureConn, bytes)
			if status != http.StatusOK {
				t.Fatalf("REST invoke: HTTP %d %v", status, out)
			}
			if out["body_truncated"] != nil || out["hint"] != nil || out["export_arguments"] != nil {
				t.Errorf("truncated=%v hint=%v export=%v; want the body returned whole", out["body_truncated"], out["hint"], out["export_arguments"])
			}
			if got := number(t, out, "body_bytes"); got != issue1878SizedResponse {
				t.Errorf("body_bytes = %v; want the whole %d-byte response", got, issue1878SizedResponse)
			}
			body, _ := out["body"].(map[string]any)
			if content, _ := body["body"].(string); len(content) != issue1878Bytes {
				t.Errorf("the parsed body holds %d bytes of content; want all %d", len(content), issue1878Bytes)
			}
		})
	}
}

// TestIssue1878_TheSameCallFromAModelIsFitted: the same call through the MCP
// tool is fitted by the response-layer middleware, with the same flag and
// export steer as before.
func TestIssue1878_TheSameCallFromAModelIsFitted(t *testing.T) {
	c := connect(t)
	for form, bytes := range issue1878BytesForms {
		t.Run("bytes_as_"+form, func(t *testing.T) {
			args := issue1878SizedArgs(issue1587FixtureConn, bytes)
			res, text, err := c.callRaw("api_invoke_endpoint", args)
			if err != nil || res.IsError {
				t.Fatalf("api_invoke_endpoint: %v %s", err, text)
			}
			if size := issue1878ResultSize(res); size > issue1878Budget {
				t.Errorf("the result is %d characters; want it inside the %d budget", size, issue1878Budget)
			}
			out := c.call("api_invoke_endpoint", issue1878SizedArgs(issue1587FixtureConn, bytes))
			assertCutAtInlineBudget(t, out, issue1878Budget)
			if got := number(t, out, "body_bytes"); got != issue1878SizedResponse {
				t.Errorf("body_bytes = %v; want the %d bytes read", got, issue1878SizedResponse)
			}
		})
	}
}

// issue1878ReadCapConnection registers a fixture-backed api connection whose
// read cap is under issue1878Bytes, and removes it when the test ends.
func issue1878ReadCapConnection(t *testing.T, c *client) string {
	t.Helper()
	name := "issue-1878-read-cap"
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": map[string]any{
			"base_url":           issue1587FixtureBaseURL,
			"auth_mode":          "api_key",
			"credential":         issue1587FixtureDevKey,
			"api_key_placement":  "header",
			"api_key_header":     "X-API-Key",
			"connection_name":    name,
			"max_response_bytes": issue1878ReadCap,
		},
		"description": "Acceptance #1878: a connection with a read cap under the response",
	})
	if status/100 != 2 {
		t.Fatalf("PUT connection %s: HTTP %d", name, status)
	}
	t.Cleanup(func() {
		_, _ = c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// TestIssue1878_RESTPastTheReadCapIsRefusedWith413: a REST call past
// max_response_bytes fails with an explicit error, not a truncated 200. The
// same call from a model is cut at the cap and steered to api_export.
func TestIssue1878_RESTPastTheReadCapIsRefusedWith413(t *testing.T) {
	c := connect(t)
	name := issue1878ReadCapConnection(t, c)
	for form, bytes := range issue1878BytesForms {
		t.Run("bytes_as_"+form, func(t *testing.T) {
			status, out := issue1878RESTInvoke(t, c, name, bytes)
			if status != http.StatusRequestEntityTooLarge {
				t.Fatalf("REST invoke past the read cap: HTTP %d %v; want 413", status, out)
			}
			if out["error"] != "upstream_body_too_large" || out["limit_bytes"] != float64(issue1878ReadCap) {
				t.Errorf("413 body = %v; want the error code and the %d cap", out, issue1878ReadCap)
			}

			model := c.call("api_invoke_endpoint", issue1878SizedArgs(name, bytes))
			if truncated, _ := model["body_truncated"].(bool); !truncated {
				t.Errorf("a model's call past the read cap: body_truncated = %v; want the cut flagged", model["body_truncated"])
			}
			if hint, _ := model["hint"].(string); !strings.Contains(hint, fmt.Sprintf("max_response_bytes (%d)", issue1878ReadCap)) {
				t.Errorf("hint = %q; want the read cap named", hint)
			}
		})
	}
}

// issue1878GraphQLArgs is one graphql_query call.
func issue1878GraphQLArgs(connection, document string, variables any) map[string]any {
	return map[string]any{
		"connection": connection,
		"query":      document,
		"variables":  variables,
		"purpose":    issue1878Purpose,
	}
}

// issue1878WithheldGraphQL calls graphql_query as a model, asserts the
// result is inside the budget with its data withheld, and returns it.
func issue1878WithheldGraphQL(t *testing.T, c *client, args map[string]any) map[string]any {
	t.Helper()
	res, text, err := c.callRaw(issue1277QueryTool, args)
	if err != nil || res.IsError {
		t.Fatalf("graphql_query: %v %s", err, text)
	}
	if size := issue1878ResultSize(res); size > issue1878Budget {
		t.Errorf("the result is %d characters; want it inside the %d budget", size, issue1878Budget)
	}
	out := c.call(issue1277QueryTool, args)
	if truncated, _ := out["data_truncated"].(bool); !truncated || out["data"] != nil {
		t.Errorf("data_truncated=%v data present=%v; want the data withheld", out["data_truncated"], out["data"] != nil)
	}
	if got := number(t, out, "data_bytes"); got <= issue1878Budget {
		t.Errorf("data_bytes = %v; want the size of the whole answer, past the budget", got)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "tools.result_budget") || !strings.Contains(note, "graphql_export") {
		t.Errorf("note = %q; want the budget and graphql_export named", note)
	}
	return out
}

// TestIssue1878_GraphQLQueryFromAModelIsFitted: a large graphql_query result
// sent to a model is fitted by the same middleware: the data is withheld and
// the graphql_export call handed back. A document itself past the budget is
// named rather than echoed.
func TestIssue1878_GraphQLQueryFromAModelIsFitted(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "budget-1878", nil)
	for form, variables := range map[string]any{"object": issue1675Variables(), "string": issue1675VariablesJSON(t)} {
		t.Run("variables_as_"+form, func(t *testing.T) {
			document := issue1878SearchDocument(issue1878SearchAliases)
			out := issue1878WithheldGraphQL(t, c, issue1878GraphQLArgs(name, document, variables))
			exportArgs, _ := out["export_arguments"].(map[string]any)
			if exportArgs["connection"] != name || exportArgs["query"] != document || exportArgs["variables"] == nil {
				t.Errorf("export_arguments = %v; want the same call for graphql_export", out["export_arguments"])
			}

			wide := issue1878WithheldGraphQL(t, c, issue1878GraphQLArgs(name, issue1878WideDocument(issue1878WideFields), variables))
			wideArgs, _ := wide["export_arguments"].(map[string]any)
			if wideArgs["connection"] != name || wideArgs["query"] != nil {
				t.Errorf("export_arguments = %v; want the connection without the document past the budget", wide["export_arguments"])
			}
			if note, _ := wide["note"].(string); !strings.Contains(note, "omit the query document") {
				t.Errorf("note = %q; want it to say the document was not echoed", note)
			}
		})
	}
}

// TestIssue1878_TrinoQueryFromAModelIsFitted: a large trino_query result
// sent to a model keeps the head of its rows in the format asked for.
func TestIssue1878_TrinoQueryFromAModelIsFitted(t *testing.T) {
	c := connect(t)
	const sql = "SELECT x, rpad('row', 80, '.') AS pad FROM UNNEST(sequence(1, 1000)) AS t(x) ORDER BY x"
	for _, format := range []string{"json", "csv", "markdown"} {
		t.Run("format_"+format, func(t *testing.T) {
			res, text, err := c.callRaw("trino_query", map[string]any{
				"sql":     sql,
				"format":  format,
				"limit":   1000,
				"purpose": issue1878Purpose,
			})
			if err != nil || res.IsError {
				t.Fatalf("trino_query: %v %s", err, text)
			}
			if size := issue1878ResultSize(res); size > issue1878Budget {
				t.Errorf("the result is %d characters; want it inside the %d budget", size, issue1878Budget)
			}
			out, _ := res.StructuredContent.(map[string]any)
			if truncated, _ := out["result_truncated"].(bool); !truncated {
				t.Fatalf("result_truncated = %v in %v; want the rows cut", out["result_truncated"], out)
			}
			shown := number(t, out, "rows_shown")
			if shown <= 0 || shown >= number(t, out, "row_count") {
				t.Errorf("rows_shown = %v of row_count %v; want a head of the rows", shown, out["row_count"])
			}
			if rows, _ := out["rows"].([]any); float64(len(rows)) != shown {
				t.Errorf("structured rows = %d; want the %v shown", len(rows), shown)
			}
			if exportArgs, _ := out["export_arguments"].(map[string]any); exportArgs["sql"] != sql {
				t.Errorf("export_arguments = %v; want the trino_export call for the same query", out["export_arguments"])
			}
			if format != "json" && !strings.Contains(text, fmt.Sprintf("%v of 1000 rows", shown)) {
				t.Errorf("the %s text does not say how many rows it shows:\n%s", format, text[max(0, len(text)-400):])
			}
		})
	}
}

// TestIssue1878_AResultWithNoExportIsNeverCut: only a result the model can
// recover the rest of is cut. A proxied MCP tool's text and manage_script's
// help -- the scripting manual, past the budget -- have no export
// equivalent, so each reaches the model whole.
func TestIssue1878_AResultWithNoExportIsNeverCut(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("mcp-test-fixture__long_output", map[string]any{"blocks": 4, "chars": 20000, "purpose": issue1878Purpose})
	if err != nil || res.IsError {
		t.Fatalf("long_output: %v %s", err, text)
	}
	if size := issue1878ResultSize(res); size < 80000 {
		t.Errorf("the result is %d characters; want all 80000 the tool returned", size)
	}
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok && strings.Contains(tc.Text, "result cut") {
			t.Errorf("a block says the result was cut: %q", tc.Text)
		}
	}

	help, text, err := c.callRaw("manage_script", map[string]any{"command": "help"})
	if err != nil || help.IsError {
		t.Fatalf("manage_script help: %v %s", err, text)
	}
	if len(text) <= issue1878Budget {
		t.Fatalf("the help is %d characters; the criterion needs one past the %d budget", len(text), issue1878Budget)
	}
	out := c.call("manage_script", map[string]any{"command": "help"})
	if dialect, _ := out["dialect"].(string); !strings.Contains(dialect, "platform.query") {
		t.Errorf("the help did not arrive whole and parseable: dialect = %.80q", dialect)
	}
}

// issue1878Script calls api_invoke_endpoint and graphql_query for results past
// the budget and records what it received. A run held to the budget would
// see a cut body and withheld data; indexing into both proves they arrived
// whole.
func issue1878Script(graphqlConnection string) string {
	return fmt.Sprintf(`
api = platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "method": "GET",
    "path": "/v1/sized",
    "query_params": {"bytes": 200000},
    "purpose": "Acceptance #1878: a run is never fitted.",
})
gql = platform.call("graphql_query", {
    "connection": %q,
    "query": %q,
    "variables": {"show": True},
    "purpose": "Acceptance #1878: a run is never fitted.",
})
platform.save_state({
    "api_content_len": str(len(api["body"]["body"])),
    "api_truncated": str(api.get("body_truncated", False)),
    "gql_fields": str(len(gql["data"])),
    "gql_truncated": str(gql.get("data_truncated", False)),
})
`, graphqlConnection, issue1878WideDocument(issue1878WideFields))
}

// TestIssue1878_AScriptRunIsNeverFitted: script runs are never fitted,
// whichever tool they go through -- here the same over-budget payloads the
// model criteria above are fitted on.
func TestIssue1878_AScriptRunIsNeverFitted(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	gqlName := issue1277Connect(t, c, "script-1878", nil)
	const name = "acceptance-1878-never-fitted"
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1878: a run is not held to the model-context budget.",
		"source":      issue1878Script(gqlName),
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("run did not succeed: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
	state, _ := got["state"].(map[string]any)
	want := map[string]any{
		"api_content_len": fmt.Sprint(issue1878Bytes),
		"api_truncated":   "False",
		"gql_fields":      fmt.Sprint(issue1878WideFields),
		"gql_truncated":   "False",
	}
	for k, v := range want {
		if state[k] != v {
			t.Errorf("%s = %v; want %v", k, state[k], v)
		}
	}
}

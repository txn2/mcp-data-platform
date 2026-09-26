package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

const datasetDocument = `query Read($urn: String!) { dataset(urn: $urn) { urn name } }`

func TestQueryReturnsTheEndpointsAnswer(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}},"extensions":{"tracing":"off"}}`)
	tk := newToolkit(t, u, "flat", nil)

	out := callQuery(t, tk, QueryInput{
		Connection: "gql", Query: datasetDocument,
		Variables: jsonRaw(t, map[string]any{"urn": "u1"}),
	})

	if out.UpstreamError {
		t.Error("a successful call reported an upstream error")
	}
	if out.Kind != "QUERY" || out.OperationName != "Read" {
		t.Errorf("out = %+v", out)
	}
	if len(out.Operations) != 1 || out.Operations[0] != "dataset" {
		t.Errorf("operations = %v", out.Operations)
	}
	if !strings.Contains(string(out.Data), "orders") {
		t.Errorf("data = %s", out.Data)
	}
	if out.Extensions["tracing"] != "off" {
		t.Errorf("extensions were not passed through: %v", out.Extensions)
	}
	calls := u.calls()
	if len(calls) != 1 || calls[0].Variables["urn"] != "u1" {
		t.Errorf("the endpoint saw %+v", calls)
	}
	if calls[0].Query != datasetDocument {
		t.Error("the document was re-printed rather than sent as written")
	}
}

// TestQueryAcceptsEveryFormTheVariablesSchemaAdmits is the wire-form
// rule: the schema says object or string, so both must reach the same
// request. A client that stringifies structured arguments sends the
// second.
func TestQueryAcceptsEveryFormTheVariablesSchemaAdmits(t *testing.T) {
	forms := map[string]json.RawMessage{
		"object":         json.RawMessage(`{"urn":"u1"}`),
		"string of JSON": json.RawMessage(`"{\"urn\":\"u1\"}"`),
		"omitted":        nil,
		"empty string":   json.RawMessage(`""`),
	}
	for name, variables := range forms {
		t.Run(name, func(t *testing.T) {
			u := newUpstream(t)
			u.respond = answer(`{"data":{"dataset":null}}`)
			tk := newToolkit(t, u, "flat", nil)
			out := callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument, Variables: variables})
			if out.Status != http.StatusOK {
				t.Fatalf("status = %d", out.Status)
			}
			sent := u.calls()[0].Variables
			switch name {
			case "object", "string of JSON":
				if sent["urn"] != "u1" {
					t.Errorf("variables reached the endpoint as %v", sent)
				}
			default:
				if len(sent) != 0 {
					t.Errorf("variables reached the endpoint as %v; want none", sent)
				}
			}
		})
	}
}

func TestQueryRefusesVariablesThatAreNeitherForm(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	msg := refuseQuery(t, tk, QueryInput{
		Connection: "gql", Query: datasetDocument, Variables: json.RawMessage(`[1,2]`),
	})
	if !strings.Contains(msg, "JSON object") {
		t.Errorf("msg = %q", msg)
	}
	msg = refuseQuery(t, tk, QueryInput{
		Connection: "gql", Query: datasetDocument, Variables: json.RawMessage(`"not json"`),
	})
	if !strings.Contains(msg, "does not hold a JSON object") {
		t.Errorf("msg = %q", msg)
	}
}

func TestQueryRefusesWhatItCannotRun(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	cases := []struct {
		name string
		in   QueryInput
		want string
	}{
		{"no connection", QueryInput{Query: datasetDocument}, "connection is required"},
		{"no document", QueryInput{Connection: "gql"}, "query is required"},
		{"unknown connection", QueryInput{Connection: "nope", Query: datasetDocument}, "not found"},
		{"unparseable document", QueryInput{Connection: "gql", Query: "{ dataset("}, "parsing document"},
		{"a subscription", QueryInput{Connection: "gql", Query: "subscription { ticks }"}, "subscriptions are not supported"},
		{"introspection", QueryInput{Connection: "gql", Query: "{ __schema { types { name } } }"}, "graphql_discover"},
		{
			"two operations and no name",
			QueryInput{Connection: "gql", Query: `query A { dataset(urn:"x") { urn } } query B { dataset(urn:"y") { urn } }`},
			"operation_name is required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msg := refuseQuery(t, tk, c.in); !strings.Contains(msg, c.want) {
				t.Errorf("msg = %q; want it to say %q", msg, c.want)
			}
		})
	}
	if calls := u.calls(); len(calls) != 0 {
		t.Errorf("a refused document reached the endpoint: %+v", calls)
	}
}

func TestQueryRefusesADocumentDeeperThanTheConnectionAllows(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", map[string]any{"max_query_depth": 3})
	msg := refuseQuery(t, tk, QueryInput{
		Connection: "gql",
		Query:      `{ masterData { product { read(_id: "1") { site { code } } } } }`,
	})
	if !strings.Contains(msg, "max_query_depth") || !strings.Contains(msg, "allows 3") {
		t.Errorf("msg = %q; want the depth and the knob named", msg)
	}
	if calls := u.calls(); len(calls) != 0 {
		t.Error("a document past the depth cap was sent anyway")
	}
}

func TestStrictValidationRefusesADocumentTheSchemaDoesNotAdmitAndSaysWhen(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	msg := refuseQuery(t, tk, QueryInput{
		Connection: "gql", Query: `{ dataset(urn: "x") { noSuchField } }`,
	})
	for _, want := range []string{"gql", "noSuchField", "graphql_discover"} {
		if !strings.Contains(msg, want) {
			t.Errorf("msg = %q; want it to mention %q", msg, want)
		}
	}
	if !strings.Contains(msg, time.Now().UTC().Format("2006")) {
		t.Errorf("msg = %q; the schema's fetch time is what makes drift self-diagnosing", msg)
	}
	if calls := u.calls(); len(calls) != 0 {
		t.Error("a document the schema refuses was sent anyway")
	}
}

func TestWarnValidationSendsTheDocumentAndReportsTheViolations(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u"}}}`)
	tk := newToolkit(t, u, "flat", map[string]any{"schema_validation": "warn"})

	out := callQuery(t, tk, QueryInput{
		Connection: "gql", Query: `{ dataset(urn: "x") { noSuchField } }`,
	})

	if len(out.ValidationWarnings) == 0 {
		t.Error("the violations were not reported")
	}
	if !strings.Contains(out.Note, "warn") {
		t.Errorf("note = %q", out.Note)
	}
	if len(u.calls()) != 1 {
		t.Error("warn mode did not send the document")
	}
}

// TestAConnectionWithNoSchemaRefusesADocument: with no operation index a
// document would be reduced to its root fields and authorized under them,
// which no persona rule for this kind is written against (#1676). The
// refusal names the cause the connection recorded, and nothing is sent.
func TestAConnectionWithNoSchemaRefusesADocument(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"anything":1}}`)
	tk := newToolkit(t, u, "", nil)
	if err := tk.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query; this test needs one that refuses")
	}

	refusal := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: `{ anything }`})

	if !strings.Contains(refusal, `connection "gql" has no schema`) {
		t.Errorf("refusal = %q", refusal)
	}
	if !strings.Contains(refusal, "introspection is not allowed") {
		t.Errorf("refusal = %q; the cause the connection recorded is what the caller is told", refusal)
	}
	for _, req := range u.calls() {
		if !strings.Contains(req.Query, "__schema") {
			t.Errorf("a document was sent to the endpoint with no index to authorize it under: %q", req.Query)
		}
	}
}

func TestAnErrorsArrayInAnHTTP200IsAnUpstreamError(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":null},"errors":[{"message":"Unauthorized","path":["dataset"],"extensions":{"code":"FORBIDDEN"}}]}`)
	tk := newToolkit(t, u, "flat", nil)

	out := callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument})

	if out.Status != http.StatusOK {
		t.Fatalf("status = %d; the whole point is that this is a 200", out.Status)
	}
	if !out.UpstreamError {
		t.Error("a 200 carrying errors was recorded as a success")
	}
	if len(out.Errors) != 1 || out.Errors[0].Message != "Unauthorized" {
		t.Errorf("errors = %+v; the upstream's own words are the diagnosis", out.Errors)
	}
	if out.Errors[0].Extensions["code"] != "FORBIDDEN" {
		t.Errorf("the upstream's error metadata was dropped: %+v", out.Errors[0])
	}
	if string(out.Data) == "" {
		t.Error("partial data was discarded")
	}
}

func TestANonJSONAnswerIsReportedRatherThanParsed(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(graphQLRequest, int) (int, string) {
		return http.StatusBadGateway, "<html>gateway timeout</html>"
	}
	tk := newToolkit(t, u, "flat", nil)
	out := callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument})
	if !out.UpstreamError {
		t.Error("a 502 was recorded as a success")
	}
	if !strings.Contains(out.Note, "not a GraphQL response") {
		t.Errorf("note = %q", out.Note)
	}
}

func TestATransportFailureRefusesTheCall(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	u.server.Close()
	if msg := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument}); msg == "" {
		t.Error("want a refusal")
	}
}

// modelQuery runs graphql_query as a model's call arrives and fits the
// result the way the platform's result-budget middleware does (#1878).
func modelQuery(t *testing.T, tk *Toolkit, in QueryInput, budget int) (*mcp.CallToolResult, *QueryOutput) {
	t.Helper()
	ctx := mcpcontext.WithResultBudget(context.Background(), budget)
	res, payload, err := tk.handleQuery(ctx, nil, in)
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res.StructuredContent = json.RawMessage(raw)
	args, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := res.Content[0].(*mcp.TextContent); text != nil && len(text.Text) > budget {
		if !tk.FitResult(ToolQuery, args, res, budget) {
			t.Fatal("FitResult declined a graphql_query result")
		}
	}
	var out QueryOutput
	decodeResult(t, res, &out)
	return res, &out
}

func TestDataPastTheContextBudgetIsWithheldWholeWithTheExportCall(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"` + strings.Repeat("x", 4000) + `"}}}`)
	tk := newToolkit(t, u, "flat", nil)

	res, out := modelQuery(t, tk, QueryInput{
		Connection: "gql", Query: datasetDocument,
		Variables: jsonRaw(t, map[string]any{"urn": "u"}),
	}, 512)

	if !out.DataTruncated {
		t.Fatal("a result past the budget was returned whole")
	}
	// A JSON document cut in half cannot be parsed, so it is withheld
	// rather than halved.
	if len(out.Data) != 0 {
		t.Errorf("data = %s; want it withheld", out.Data)
	}
	if out.DataBytes == 0 {
		t.Error("the caller cannot see how much was withheld")
	}
	if out.ExportArguments["connection"] != "gql" || out.ExportArguments["query"] != datasetDocument || out.ExportArguments["variables"] == nil {
		t.Errorf("export arguments = %v", out.ExportArguments)
	}
	if !strings.Contains(out.Note, "graphql_export") || !strings.Contains(out.Note, "(512, tools.result_budget)") {
		t.Errorf("note = %q; the caller needs to be told where the data is and what cut it", out.Note)
	}
	if text, _ := res.Content[0].(*mcp.TextContent); len(text.Text) > 512 {
		t.Errorf("text is %d characters; want it inside the 512 budget", len(text.Text))
	}
	if structured, _ := res.StructuredContent.(json.RawMessage); len(structured) > 512 {
		t.Errorf("structured content is %d bytes; want the fitted copy", len(structured))
	}
}

// TestACallerWithNoContextBudgetGetsTheDataWhole: a script's call carries no
// budget, so the same answer comes back whole (#1878).
func TestACallerWithNoContextBudgetGetsTheDataWhole(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"` + strings.Repeat("x", 4000) + `"}}}`)
	tk := newToolkit(t, u, "flat", nil)

	out := callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u"})})
	if out.DataTruncated || !strings.Contains(string(out.Data), strings.Repeat("x", 4000)) {
		t.Errorf("truncated=%v data=%d bytes; want the whole answer", out.DataTruncated, len(out.Data))
	}
}

// TestFitResultDeclinesWhatItCannotShape: another tool, unreadable
// structured output or arguments, and a result whose errors alone are past
// the budget are declined, so the generic cut applies.
func TestFitResultDeclinesWhatItCannotShape(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	big := QueryOutput{Connection: "gql", Errors: []Error{{Message: strings.Repeat("e", 2000)}}}
	raw, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		tool       string
		args       json.RawMessage
		structured any
	}{
		{"another tool", ToolExport, nil, json.RawMessage(raw)},
		{"no structured output", ToolQuery, nil, nil},
		{"arguments of another shape", ToolQuery, json.RawMessage(`[1]`), json.RawMessage(raw)},
		{"errors alone past the budget", ToolQuery, nil, json.RawMessage(raw)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: tc.structured}
			if tk.FitResult(tc.tool, tc.args, res, 256) {
				t.Fatal("FitResult fitted a result it cannot shape")
			}
		})
	}
}

// TestAnAnswerPastTheReadCapIsRefused: an answer cut at max_response_bytes
// cannot be parsed, so the call is refused naming the cap and the export
// call rather than reported as a malformed answer (#1878).
func TestAnAnswerPastTheReadCapIsRefused(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"` + strings.Repeat("x", 4000) + `"}}}`)
	tk := newToolkit(t, u, "flat", map[string]any{"max_response_bytes": 1024})

	msg := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument, Variables: jsonRaw(t, map[string]any{"urn": "u"})})
	for _, want := range []string{"max_response_bytes (1024)", "graphql_export"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q lacks %q", msg, want)
		}
	}
}

func TestTheCallersTimeoutNeverRaisesTheConnectionsOwn(t *testing.T) {
	cfg := Config{CallTimeout: 5 * time.Second}
	for _, c := range []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"asking for less wins", 2, 2 * time.Second},
		{"asking for more does not", 600, 5 * time.Second},
		{"asking for nothing keeps the connection's", 0, 5 * time.Second},
		{"a value past the schema's ceiling is ignored", 9999, 5 * time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := withCallTimeout(context.Background(), cfg, c.seconds)
			defer cancel()
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("no deadline")
			}
			if got := time.Until(deadline).Round(time.Second); got != c.want {
				t.Errorf("timeout = %v; want %v", got, c.want)
			}
		})
	}
	ctx, cancel := withCallTimeout(context.Background(), Config{}, 0)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Error("a connection with no configured timeout still gets the default")
	}
}

func TestStaticHeadersAndCredentialReachTheEndpoint(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk := newToolkit(t, u, "flat", map[string]any{
		"auth_mode":      "bearer",
		"credential":     "t0ken",
		"static_headers": map[string]any{"x-tenant": "acme"},
	})
	callQuery(t, tk, QueryInput{Connection: "gql", Query: `{ dataset(urn:"x") { urn } }`})
	headers := u.lastHeaders()
	if got := headers.Get("Authorization"); got != "Bearer t0ken" {
		t.Errorf("Authorization = %q", got)
	}
	if got := headers.Get("x-tenant"); got != "acme" {
		t.Errorf("x-tenant = %q; an operator's routing header must reach the endpoint", got)
	}
	if got := headers.Get("Content-Type"); got != contentTypeJSON {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestIdentityPassthroughRefusesAnAnonymousCall(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", map[string]any{"identity_passthrough": true})
	msg := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: `{ dataset(urn:"x") { urn } }`})
	if !strings.Contains(msg, "identity passthrough") {
		t.Errorf("msg = %q", msg)
	}
}

// TestADocumentPastTheBudgetIsNamedNotEchoed: when the query document the
// steer would echo is itself past the budget, the export arguments omit it
// and the note says to pass the same query, rather than the fit failing.
func TestADocumentPastTheBudgetIsNamedNotEchoed(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"` + strings.Repeat("x", 4000) + `"}}}`)
	tk := newToolkit(t, u, "flat", nil)
	document := datasetDocument + strings.Repeat(" ", 2000)

	res, out := modelQuery(t, tk, QueryInput{Connection: "gql", Query: document, Variables: jsonRaw(t, map[string]any{"urn": "u"})}, 1024)
	if !out.DataTruncated || out.ExportArguments["query"] != nil || out.ExportArguments["connection"] != "gql" {
		t.Errorf("truncated=%v export=%v; want the data withheld and the document not echoed", out.DataTruncated, out.ExportArguments)
	}
	if !strings.Contains(out.Note, "omit the query document") {
		t.Errorf("note = %q", out.Note)
	}
	if text, _ := res.Content[0].(*mcp.TextContent); len(text.Text) > 1024 {
		t.Errorf("text is %d characters; want it inside the budget", len(text.Text))
	}
}

package apigateway

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listPayload is a JSON list response of n rows, each large enough that a
// few dozen of them are past a small budget.
func listPayload(n int) string {
	rows := make([]map[string]any, 0, n)
	for i := range n {
		rows = append(rows, map[string]any{"id": i, "note": strings.Repeat("n", 40)})
	}
	b, _ := json.Marshal(map[string]any{"rows": rows, "total": n})
	return string(b)
}

func decodeJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func bodyRows(t *testing.T, out InvokeOutput) []any {
	t.Helper()
	body, _ := out.Body.(map[string]any)
	rows, ok := body["rows"].([]any)
	if !ok {
		t.Fatalf("body = %v; want an object carrying a rows list", out.Body)
	}
	return rows
}

// cutSpec declares one list operation per paging style.
const cutSpec = `
openapi: 3.0.3
info: {title: Paged, version: "1"}
paths:
  /offset:
    get:
      operationId: listByOffset
      parameters:
        - {name: limit, in: query, schema: {type: integer, default: 50}}
        - {name: offset, in: query, schema: {type: integer, default: 0}}
      responses: {"200": {description: ok}}
  /pages:
    parameters:
      - {name: per_page, in: query, schema: {type: integer, default: 50}}
    get:
      operationId: listByPage
      parameters:
        - {name: page, in: query, schema: {type: integer, minimum: 1}}
      responses: {"200": {description: ok}}
  /cursor:
    get:
      operationId: listByCursor
      parameters:
        - {name: cursor, in: query, schema: {type: string}}
        - {name: limit, in: query, schema: {type: integer}}
      responses: {"200": {description: ok}}
  /plain:
    get:
      operationId: listPlain
      responses: {"200": {description: ok}}
`

// pagedUpstream serves a list of total rows by offset, by page and by
// cursor, the way an API declaring cutSpec would.
func cutUpstream(t *testing.T, total int) *httptest.Server {
	t.Helper()
	intParam := func(r *http.Request, name string, def int) int {
		if n, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
			return n
		}
		return def
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, size int
		switch r.URL.Path {
		case "/offset":
			start, size = intParam(r, "offset", 0), intParam(r, "limit", 50)
		case "/pages":
			size = intParam(r, "per_page", 50)
			start = (intParam(r, "page", 1) - 1) * size
		case "/cursor":
			start, size = intParam(r, "cursor", 0), intParam(r, "limit", 50)
		default:
			start, size = 0, total
		}
		end := min(start+size, total)
		rows := make([]map[string]any, 0, max(end-start, 0))
		for i := start; i < end; i++ {
			rows = append(rows, map[string]any{"id": i, "note": strings.Repeat("n", 40)})
		}
		body := map[string]any{"rows": rows, "total": total}
		if r.URL.Path == "/cursor" && end < total {
			body["next_cursor"] = strconv.Itoa(end)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func cutToolkit(t *testing.T, baseURL string) *Toolkit {
	t.Helper()
	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": baseURL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	c, _ := tk.lookup("crm")
	c.specs = map[string]*specState{"main": mustParseSpec(t, cutSpec)}
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
	return tk
}

// Following next_arguments from each cut, and stepping past each page that
// was not cut by the operation's own paging, reads every row exactly once:
// the union of the pages is the list, and nothing is written (#1915).
func TestFitResult_NextArgumentsReadTheWholeList(t *testing.T) {
	const total, budget = 230, 3000
	srv := cutUpstream(t, total)
	defer srv.Close()
	tk := cutToolkit(t, srv.URL)

	for _, tc := range []struct {
		name  string
		first InvokeInput
		step  func(InvokeInput, int) InvokeInput
	}{
		{
			"offset by operation_id",
			InvokeInput{Connection: "crm", OperationID: "listByOffset", Query: map[string]any{"limit": 100}},
			func(in InvokeInput, n int) InvokeInput {
				in.Query = withQuery(in.Query, "offset", queryInt(in.Query, "offset")+n)
				return in
			},
		},
		{
			"page by method and path",
			InvokeInput{Connection: "crm", Method: "GET", Path: "/pages", Query: map[string]any{"page": 1, "per_page": 100}},
			func(in InvokeInput, _ int) InvokeInput {
				in.Query = withQuery(in.Query, "page", max(queryInt(in.Query, "page"), 1)+1)
				return in
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := readAll(t, tk, tc.first, budget, tc.step)
			assertUnion(t, seen, total)
		})
	}
}

func readAll(t *testing.T, tk *Toolkit, in InvokeInput, budget int, step func(InvokeInput, int) InvokeInput) []int {
	t.Helper()
	var seen []int
	cuts := 0
	for range 100 {
		res, out := modelCall(t, tk, in, budget)
		if text, _ := res.Content[0].(*mcp.TextContent); len(text.Text) > budget {
			t.Fatalf("result is %d characters; want it inside the %d budget", len(text.Text), budget)
		}
		rows := bodyRows(t, out)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			id, _ := row["id"].(float64)
			seen = append(seen, int(id))
		}
		switch {
		case out.NextArguments != nil:
			cuts++
			if out.BodyItems == nil || out.BodyItems.Shown != len(rows) {
				t.Fatalf("body_items = %+v with %d rows shown", out.BodyItems, len(rows))
			}
			if !strings.Contains(out.Hint, "next_arguments") || strings.Index(out.Hint, "next_arguments") > strings.Index(out.Hint, "api_export") {
				t.Fatalf("hint = %q; want next_arguments named before api_export", out.Hint)
			}
			in = *out.NextArguments
		case out.BodyTruncated:
			t.Fatalf("a cut with no next_arguments: %+v", out)
		case len(rows) == 0:
			if cuts == 0 {
				t.Fatal("no page was cut; the case needs pages past the budget")
			}
			return seen
		default:
			in = step(in, len(rows))
		}
	}
	t.Fatal("the read did not end")
	return nil
}

func assertUnion(t *testing.T, seen []int, total int) {
	t.Helper()
	if len(seen) != total {
		t.Fatalf("read %d rows; want each of %d once: %v", len(seen), total, seen)
	}
	for i, id := range seen {
		if id != i {
			t.Fatalf("row %d is %d; want the list in order with no gap or repeat", i, id)
		}
	}
}

func withQuery(q map[string]any, k string, v any) map[string]any {
	out := maps.Clone(q)
	if out == nil {
		out = map[string]any{}
	}
	out[k] = v
	return out
}

func queryInt(q map[string]any, k string) int {
	n, _ := strconv.Atoi(fmt.Sprint(q[k]))
	return n
}

// A cursor cut asks for the same page again, as many items at a time as
// were shown; that response fits and its cursor continues after them.
func TestFitResult_CursorCutAsksForTheSamePageSmaller(t *testing.T) {
	srv := cutUpstream(t, 120)
	defer srv.Close()
	tk := cutToolkit(t, srv.URL)

	_, out := modelCall(t, tk, InvokeInput{Connection: "crm", OperationID: "listByCursor", Query: map[string]any{"cursor": "0", "limit": 100}}, 3000)
	next := out.NextArguments
	if next == nil || out.BodyItems == nil {
		t.Fatalf("out = %+v; want a cut naming the next request", out)
	}
	if next.Query["cursor"] != "0" || queryInt(next.Query, "limit") != out.BodyItems.Shown {
		t.Fatalf("next query = %v; want the same cursor with limit %d", next.Query, out.BodyItems.Shown)
	}
	if !strings.Contains(out.Hint, "asks for this page again") {
		t.Errorf("hint = %q; want the cursor continuation described", out.Hint)
	}
	if out.Pagination != nil {
		t.Errorf("pagination = %+v on a cut; want the response's signal dropped, since it skips the items cut", out.Pagination)
	}
	_, again := modelCall(t, tk, *next, 3000)
	if again.BodyTruncated || len(bodyRows(t, again)) != out.BodyItems.Shown {
		t.Fatalf("the next request was cut or held %d rows; want the %d shown, whole", len(bodyRows(t, again)), out.BodyItems.Shown)
	}
	body, _ := again.Body.(map[string]any)
	if body["next_cursor"] != strconv.Itoa(out.BodyItems.Shown) {
		t.Errorf("next_cursor = %v; want it to continue after the rows shown", body["next_cursor"])
	}
}

// An operation the spec declares no paging for is still cut on its items,
// and steered only to api_export.
func TestFitResult_NoDeclaredPagingNamesNoNext(t *testing.T) {
	srv := cutUpstream(t, 120)
	defer srv.Close()
	tk := cutToolkit(t, srv.URL)
	_, out := modelCall(t, tk, InvokeInput{Connection: "crm", OperationID: "listPlain"}, 3000)
	if !out.BodyTruncated || out.NextArguments != nil || out.ExportArguments == nil {
		t.Errorf("out: truncated=%v next=%+v export=%+v; want a cut steered only to api_export", out.BodyTruncated, out.NextArguments, out.ExportArguments)
	}
}

// A paging value the caller sent as something other than a number cannot
// be advanced: the cut stands, with no next request.
func TestFitResult_UnadvanceablePagingNamesNoNext(t *testing.T) {
	srv := cutUpstream(t, 120)
	defer srv.Close()
	tk := cutToolkit(t, srv.URL)
	_, out := modelCall(t, tk, InvokeInput{Connection: "crm", Method: "GET", Path: "/offset", Query: map[string]any{"offset": "first"}}, 3000)
	if !out.BodyTruncated || out.NextArguments != nil {
		t.Errorf("out: truncated=%v next=%+v; want a cut with no next request", out.BodyTruncated, out.NextArguments)
	}
}

// A body with no list the cut can name is cut to the longest prefix that
// fits and steered to api_export, as before #1915 (#1587): a text body, a
// JSON object holding one long string, an object carrying two lists, and a
// list whose first item alone is past the budget.
func TestFitResult_ABodyWithNoListIsCutToAPrefix(t *testing.T) {
	tk := New("primary")
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
	for _, tc := range []struct {
		name string
		body any
	}{
		{"text", strings.Repeat("line of text\n", 400)},
		{"one long string", decodeJSON(t, `{"body":"`+strings.Repeat("x", 5000)+`"}`)},
		{"two lists", decodeJSON(t, `{"a":[`+strings.Repeat(`"xxxxxxxxxx",`, 400)+`"x"],"b":[1]}`)},
		{"one item past the budget", decodeJSON(t, `[{"blob":"`+strings.Repeat("x", 5000)+`"},{"blob":"y"}]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 5000)}},
				StructuredContent: InvokeOutput{Status: 200, Body: tc.body, BodyBytes: 5000},
			}
			if !tk.FitResult(ToolInvokeEndpoint, json.RawMessage(`{"connection":"crm","method":"GET","path":"/x"}`), res, 1024) {
				t.Fatal("FitResult declined a body it can cut to a prefix")
			}
			text, _ := res.Content[0].(*mcp.TextContent)
			if len(text.Text) > 1024 {
				t.Fatalf("fitted text is %d characters; want it inside 1024", len(text.Text))
			}
			var out InvokeOutput
			if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
				t.Fatal(err)
			}
			body, _ := out.Body.(string)
			if !out.BodyTruncated || body == "" || out.BodyItems != nil || out.NextArguments != nil {
				t.Errorf("out: truncated=%v body=%d bytes items=%+v next=%+v; want a prefix cut, not an item cut", out.BodyTruncated, len(body), out.BodyItems, out.NextArguments)
			}
			if !strings.Contains(out.Hint, "(1024, tools.result_budget)") || out.ExportArguments == nil {
				t.Errorf("hint=%q export=%+v; want the budget named and the api_export steer", out.Hint, out.ExportArguments)
			}
		})
	}
}

// A walk's merged collection is re-encoded, never cut.
func TestFitToBudget_WalkIsNotCut(t *testing.T) {
	out := InvokeOutput{Body: decodeJSON(t, listPayload(50)), WalkStats: &WalkStats{PagesFetched: 2}}
	if _, ok := fitToBudget(&out, InvokeInput{}, 512, true, nil); ok {
		t.Error("a walk past the budget was reported fitted")
	}
	if len(bodyRows(t, out)) != 50 {
		t.Error("a walk's body was cut")
	}
}

func TestPagingPlan_Unresolvable(t *testing.T) {
	tk := New("primary")
	if tk.pagingPlan(InvokeInput{Connection: "nope"}, "") != nil {
		t.Error("a plan for an unknown connection")
	}
	srv := cutUpstream(t, 1)
	defer srv.Close()
	tk = cutToolkit(t, srv.URL)
	if tk.pagingPlan(InvokeInput{Connection: "crm", OperationID: "missing"}, "") != nil {
		t.Error("a plan for an operation the spec does not declare")
	}
	if tk.pagingPlan(InvokeInput{Connection: "crm", Method: "GET", Path: "/elsewhere"}, "") != nil {
		t.Error("a plan for a path the spec does not declare")
	}
	if tk.pagingPlan(InvokeInput{Connection: "crm", Method: "GET", Path: "/offset?limit=5"}, "") == nil {
		t.Error("no plan for a declared path carrying a query string")
	}
}

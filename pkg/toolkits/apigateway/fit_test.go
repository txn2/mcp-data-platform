package apigateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// A model client's context budget (issues #1587, #1606) is applied to an
// api_invoke_endpoint result by the platform's result-budget middleware
// through FitResult (#1878). The toolkit itself reads to the connection's
// max_response_bytes for every caller, and refuses a caller that is not a
// model past it.

// modelCall makes one api_invoke_endpoint call the way a model's call
// arrives -- with its context budget recorded on the context -- and fits the
// result the way the result-budget middleware does: only when it renders
// past the budget.
func modelCall(t *testing.T, tk *Toolkit, in InvokeInput, budget int) (*mcp.CallToolResult, InvokeOutput) {
	t.Helper()
	ctx := mcpcontext.WithResultBudget(context.Background(), budget)
	res, payload, err := tk.handleInvoke(ctx, nil, in)
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	out, _ := payload.(InvokeOutput)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	res.StructuredContent = json.RawMessage(raw)
	if text, _ := res.Content[0].(*mcp.TextContent); text != nil && len(text.Text) > budget {
		args, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		if !tk.FitResult(ToolInvokeEndpoint, args, res, budget) {
			t.Fatal("FitResult declined an api_invoke_endpoint result")
		}
		raw, ok := res.StructuredContent.(json.RawMessage)
		if !ok {
			t.Fatalf("fitted structured content is %T; want encoded JSON", res.StructuredContent)
		}
		var fitted InvokeOutput
		if err := json.Unmarshal(raw, &fitted); err != nil {
			t.Fatalf("fitted structured content: %v", err)
		}
		out = fitted
	}
	return res, out
}

func TestContextBudgetHint(t *testing.T) {
	hint := contextBudgetHint(1024, 3000243)
	for _, want := range []string{"of 3000243 bytes", "context budget", "(1024, tools.result_budget)", "api_export", "export_arguments"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q lacks %q", hint, want)
		}
	}
}

func TestReadCapHint(t *testing.T) {
	declared := readCapHint(1024, 3000243)
	for _, want := range []string{"of 3000243 bytes", "max_response_bytes (1024)", "api_export", "export_arguments"} {
		if !strings.Contains(declared, want) {
			t.Errorf("hint %q lacks %q", declared, want)
		}
	}
	if undeclared := readCapHint(1024, -1); !strings.Contains(undeclared, "of undeclared length") {
		t.Errorf("hint %q; want it to say the length was undeclared", undeclared)
	}
}

// TestParseConfig_RetiredMaxInlineBytesIsIgnored: a connection stored with
// the retired per-connection budget still loads, whatever value it holds.
func TestParseConfig_RetiredMaxInlineBytesIsIgnored(t *testing.T) {
	for _, v := range []any{float64(4096), int64(0), "junk"} {
		if _, err := ParseConfig(map[string]any{"base_url": "https://api.example.com", "max_inline_bytes": v}); err != nil {
			t.Errorf("ParseConfig with max_inline_bytes=%v: %v; want it ignored", v, err)
		}
	}
}

func TestSteerToExport(t *testing.T) {
	byPath := InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x", Query: map[string]any{"q": "1"}, TimeoutSeconds: 30}
	byOperation := InvokeInput{Connection: "crm", OperationID: "listX", Method: "GET", Path: "/v1/x", PathParams: map[string]string{"id": "1"}}

	t.Run("no api_export clears the hint", func(t *testing.T) {
		out := InvokeOutput{BodyTruncated: true, Hint: "use api_export"}
		steerToExport(&out, byPath, false)
		if out.Hint != "" || out.ExportArguments != nil {
			t.Errorf("out = %+v; want no steer without api_export", out)
		}
	})
	t.Run("a whole body carries no arguments", func(t *testing.T) {
		out := InvokeOutput{}
		steerToExport(&out, byPath, true)
		if out.ExportArguments != nil {
			t.Errorf("export_arguments = %+v; want none", out.ExportArguments)
		}
	})
	t.Run("method and path form", func(t *testing.T) {
		out := InvokeOutput{BodyTruncated: true, Hint: "use api_export"}
		steerToExport(&out, byPath, true)
		args := out.ExportArguments
		if args == nil || args.Connection != "crm" || args.Method != "GET" || args.Path != "/v1/x" || args.Query["q"] != "1" {
			t.Fatalf("export_arguments = %+v; want the same call", args)
		}
		if args.TimeoutSeconds != 0 {
			t.Errorf("timeout_seconds = %d; want the inline timeout dropped", args.TimeoutSeconds)
		}
		if out.Hint == "" {
			t.Error("hint cleared with api_export registered")
		}
	})
	t.Run("operation_id form drops the resolved path", func(t *testing.T) {
		out := InvokeOutput{BodyTruncated: true}
		steerToExport(&out, byOperation, true)
		args := out.ExportArguments
		if args == nil || args.OperationID != "listX" || args.Method != "" || args.Path != "" || args.PathParams["id"] != "1" {
			t.Fatalf("export_arguments = %+v; want operation_id with path_params and no method+path", args)
		}
	})
}

// TestFitResult_CutsAModelsResultToItsBudget: a JSON body whose result
// renders past a model client's budget comes back cut, with the size that
// was read, the hint, and the api_export arguments, and both the text and
// the structured copy are inside the budget; the same call without api_export
// registered is cut and flagged but not steered.
func TestFitResult_CutsAModelsResultToItsBudget(t *testing.T) {
	payload := `{"rows":"` + strings.Repeat("x", 5000) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	newToolkit := func(withExport bool) *Toolkit {
		tk := New("primary")
		if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL}); err != nil {
			t.Fatalf("AddConnection: %v", err)
		}
		if withExport {
			tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
		}
		return tk
	}
	in := InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x", Query: map[string]any{"q": "1"}, TimeoutSeconds: 5}

	res, out := modelCall(t, newToolkit(true), in, 1024)
	if !out.BodyTruncated || out.BodyBytes != int64(len(payload)) {
		t.Fatalf("truncated=%v body_bytes=%d; want a cut body reporting the %d bytes read", out.BodyTruncated, out.BodyBytes, len(payload))
	}
	if body, _ := out.Body.(string); body == "" || len(body) >= 1024 {
		t.Errorf("body holds %d bytes; want a cut body inside the 1024 budget the whole result is held to", len(body))
	}
	if !strings.Contains(out.Hint, "(1024, tools.result_budget)") {
		t.Errorf("hint = %q; want the budget named", out.Hint)
	}
	if out.ExportArguments == nil || out.ExportArguments.Path != "/v1/x" || out.ExportArguments.TimeoutSeconds != 0 {
		t.Errorf("export_arguments = %+v; want the same call without the inline timeout", out.ExportArguments)
	}
	if len(res.Content) != 1 {
		t.Fatalf("result carries %d content blocks; want the one fitted text", len(res.Content))
	}
	text, _ := res.Content[0].(*mcp.TextContent)
	var wire map[string]any
	if err := json.Unmarshal([]byte(text.Text), &wire); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if wire["body_bytes"] != float64(len(payload)) || wire["export_arguments"] == nil {
		t.Errorf("wire = %v; want body_bytes and export_arguments on the envelope", wire)
	}
	if len(text.Text) > 1024 {
		t.Errorf("rendered result is %d characters; want it inside the 1024 budget", len(text.Text))
	}
	if structured, _ := res.StructuredContent.(json.RawMessage); len(structured) > 1024 {
		t.Errorf("structured content is %d bytes; want the fitted copy, inside the budget", len(structured))
	}

	_, plain := modelCall(t, newToolkit(false), in, 1024)
	if !plain.BodyTruncated || plain.Hint != "" || plain.ExportArguments != nil {
		t.Errorf("without api_export: truncated=%v hint=%q export=%+v; want cut, no steer", plain.BodyTruncated, plain.Hint, plain.ExportArguments)
	}
}

// TestFitResult_DeclinesWhatItCannotShape: another tool's result, one with
// no structured output, and arguments that are not an api_invoke_endpoint
// call are declined and left untouched, so the generic cut applies.
func TestFitResult_DeclinesWhatItCannotShape(t *testing.T) {
	tk := New("primary")
	whole := func() *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 100)}},
			StructuredContent: json.RawMessage(`{"body":"` + strings.Repeat("x", 90) + `","body_bytes":90}`),
		}
	}
	cases := []struct {
		name string
		tool string
		args json.RawMessage
		edit func(*mcp.CallToolResult)
	}{
		{"another tool", "api_export", nil, nil},
		{"no structured output", ToolInvokeEndpoint, nil, func(r *mcp.CallToolResult) { r.StructuredContent = nil }},
		{"structured output of another shape", ToolInvokeEndpoint, nil, func(r *mcp.CallToolResult) { r.StructuredContent = json.RawMessage(`[1,2]`) }},
		{"arguments of another shape", ToolInvokeEndpoint, json.RawMessage(`[1]`), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := whole()
			if tc.edit != nil {
				tc.edit(res)
			}
			if tk.FitResult(tc.tool, tc.args, res, 10) {
				t.Fatal("FitResult fitted a result it cannot shape")
			}
			if text, _ := res.Content[0].(*mcp.TextContent); len(text.Text) != 100 {
				t.Errorf("text is %d characters; want the declined result untouched", len(text.Text))
			}
		})
	}
}

// TestFitResult_AcceptsStructuredContentInAnyForm: structured output handed
// over as a Go value rather than encoded JSON is read the same way.
func TestFitResult_AcceptsStructuredContentInAnyForm(t *testing.T) {
	tk := New("primary")
	res := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 5000)}},
		StructuredContent: InvokeOutput{Status: 200, Body: strings.Repeat("y", 5000), BodyBytes: 5000},
	}
	if !tk.FitResult(ToolInvokeEndpoint, json.RawMessage(`{"connection":"crm","method":"GET","path":"/x"}`), res, 1024) {
		t.Fatal("FitResult declined a result whose structured output is a Go value")
	}
	if text, _ := res.Content[0].(*mcp.TextContent); len(text.Text) > 1024 {
		t.Errorf("fitted text is %d characters; want it inside 1024", len(text.Text))
	}
}

// TestInvoke_ReportsBodyBytes: a body under the budget is returned whole with
// its size, and an empty body reports zero.
func TestInvoke_ReportsBodyBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/empty" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	cfg, _ := ParseConfig(map[string]any{"base_url": srv.URL})
	auth, _ := NewAuthenticator(cfg)
	inv := invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}

	out, err := invoke(context.Background(), inv, InvokeInput{Connection: "x", Method: "GET", Path: "/"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.BodyBytes != int64(len(`{"ok":true}`)) || out.BodyTruncated || out.Hint != "" {
		t.Errorf("out = %+v; want body_bytes of the whole body and no flag", out)
	}
	empty, err := invoke(context.Background(), inv, InvokeInput{Connection: "x", Method: "GET", Path: "/empty"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if empty.BodyBytes != 0 {
		t.Errorf("body_bytes = %d on an empty body; want 0", empty.BodyBytes)
	}
}

// TestHandleInvoke_JSONUnderTheReadBudgetIsStillHeldToIt is the defect issue
// #1606 reported: a compact JSON body inside the budget is re-rendered
// indented into the tool result, so the result the client receives is several
// times the bytes that were read. A budget applied to the read let that
// through, and the client refused a result nothing had flagged. The budget is
// on the rendered result, and re-encoding is the first lever spent, so this
// response comes back whole and inside the budget rather than cut.
func TestHandleInvoke_JSONUnderTheReadBudgetIsStillHeldToIt(t *testing.T) {
	rows := make([]map[string]any, 0, 60)
	for i := range 60 {
		rows = append(rows, map[string]any{"id": i, "name": "row", "value": "5feceb66ffc86f38"})
	}
	payload, err := json.Marshal(map[string]any{"rows": rows})
	if err != nil {
		t.Fatal(err)
	}
	const budget = 4096
	if len(payload) >= budget {
		t.Fatalf("payload is %d bytes; the case needs a body inside the %d budget", len(payload), budget)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))

	res, out := modelCall(t, tk, InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x"}, budget)
	if res.IsError {
		t.Fatalf("invoke failed: %s", resultText(t, res))
	}
	text, _ := res.Content[0].(*mcp.TextContent)
	if text == nil {
		t.Fatal("result carries no text content")
	}
	// The defect: this rendering was past the budget and nothing said so.
	if indented, err := json.MarshalIndent(out, "", "  "); err != nil {
		t.Fatal(err)
	} else if len(indented) <= budget {
		t.Fatalf("the indented rendering is %d bytes; the case needs one past the %d budget", len(indented), budget)
	}
	if len(text.Text) > budget {
		t.Errorf("rendered result is %d characters; want it inside the %d budget", len(text.Text), budget)
	}
	if out.BodyTruncated || out.ExportArguments != nil {
		t.Errorf("truncated=%v export=%v; want the whole body returned, re-encoding alone having made it fit", out.BodyTruncated, out.ExportArguments)
	}
	body, _ := out.Body.(map[string]any)
	if got, _ := body["rows"].([]any); len(got) != 60 {
		t.Errorf("body holds %d rows; want all 60 returned", len(got))
	}
}

// TestHandleInvoke_OnlyAModelIsHeldToAContextBudget: the budget exists to
// keep a result readable by a model. A REST gateway client and a managed
// script parse the response in code, a cut body is not parseable, and a
// steer to api_export is not something they act on. So neither carries a
// budget, and each gets the response whole (issues #1606, #1878).
func TestHandleInvoke_OnlyAModelIsHeldToAContextBudget(t *testing.T) {
	payload := `{"rows":"` + strings.Repeat("x", 5000) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
	in := InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x"}

	// The control: the same call from a model is cut at its budget.
	if _, model := modelCall(t, tk, in, 1024); !model.BodyTruncated {
		t.Fatalf("a model's call was not cut; the case needs a response past the budget")
	}

	for _, source := range []string{"rest", mcpcontext.SourceScript, "admin"} {
		t.Run(source, func(t *testing.T) {
			ctx := mcpcontext.WithSource(context.Background(), source)
			res, payloadOut, err := tk.handleInvoke(ctx, nil, in)
			if err != nil {
				t.Fatalf("handleInvoke: %v", err)
			}
			out, _ := payloadOut.(InvokeOutput)
			if res.IsError {
				t.Fatalf("invoke failed: %s", resultText(t, res))
			}
			if out.BodyTruncated || out.ExportArguments != nil || out.Hint != "" {
				t.Errorf("truncated=%v export=%v hint=%q; want the response returned whole", out.BodyTruncated, out.ExportArguments, out.Hint)
			}
			if out.BodyBytes != int64(len(payload)) {
				t.Errorf("body_bytes = %d; want the whole %d-byte response read", out.BodyBytes, len(payload))
			}
			body, _ := out.Body.(map[string]any)
			if rows, _ := body["rows"].(string); len(rows) != 5000 {
				t.Errorf("body rows hold %d characters; want the whole 5000 parsed", len(rows))
			}
		})
	}
}

// TestHandleInvoke_PastTheReadCap: a response larger than the connection's
// max_response_bytes is refused for a caller that is not a model, with the
// error code the REST shim maps to 413, never a cut body under a success
// status (#1878). A model's body is cut at the cap and steered to api_export.
func TestHandleInvoke_PastTheReadCap(t *testing.T) {
	payload := `{"rows":"` + strings.Repeat("x", 5000) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL, "max_response_bytes": 2048}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
	in := InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x"}

	for _, source := range []string{"rest", mcpcontext.SourceScript} {
		t.Run(source, func(t *testing.T) {
			ctx := mcpcontext.WithSource(context.Background(), source)
			res, _, err := tk.handleInvoke(ctx, nil, in)
			if err != nil {
				t.Fatalf("handleInvoke: %v", err)
			}
			if !res.IsError {
				t.Fatal("a response past max_response_bytes was returned; want it refused")
			}
			text := resultText(t, res)
			if err := res.GetError(); err == nil || err.Error() != ErrCodeBodyTooLarge {
				t.Errorf("stamped error = %v; want the refusal's code", err)
			}
			for _, want := range []string{ErrCodeBodyTooLarge, `"limit_bytes":2048`, `"actual_bytes":`, "max_response_bytes"} {
				if !strings.Contains(text, want) {
					t.Errorf("refusal %s lacks %q", text, want)
				}
			}
		})
	}

	_, model := modelCall(t, tk, in, 32*1024)
	if !model.BodyTruncated || model.BodyBytes != 2048 {
		t.Errorf("model: truncated=%v body_bytes=%d; want the body cut at the 2048 read cap", model.BodyTruncated, model.BodyBytes)
	}
	if !strings.Contains(model.Hint, "max_response_bytes (2048)") || model.ExportArguments == nil {
		t.Errorf("model: hint=%q export=%+v; want the read cap named and the export steer", model.Hint, model.ExportArguments)
	}
}

// TestFitResult_DeclinesWhenTheEchoedRequestIsPastTheBudget: a result past
// the budget with its body cut to nothing -- the api_export steer echoing a
// large request body -- is declined, so the generic cut applies rather than
// an over-budget result being returned as fitted.
func TestFitResult_DeclinesWhenTheEchoedRequestIsPastTheBudget(t *testing.T) {
	tk := New("primary")
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
	res := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 5000)}},
		StructuredContent: InvokeOutput{Status: 200, Body: strings.Repeat("y", 5000), BodyBytes: 5000},
	}
	args, err := json.Marshal(InvokeInput{Connection: "crm", Method: "POST", Path: "/x", Body: strings.Repeat("b", 4000)})
	if err != nil {
		t.Fatal(err)
	}
	if tk.FitResult(ToolInvokeEndpoint, args, res, 1024) {
		t.Fatal("a result that cannot fit was reported fitted")
	}
	if text, _ := res.Content[0].(*mcp.TextContent); len(text.Text) != 5000 {
		t.Error("a declined result was changed")
	}
}

func TestResponseTooLargeErrorNamesTheCap(t *testing.T) {
	err := &responseTooLargeError{connection: "crm", path: "/x", limit: 2048, declared: -1}
	if msg := err.Error(); !strings.Contains(msg, ErrCodeBodyTooLarge) || !strings.Contains(msg, "max_response_bytes (2048)") {
		t.Errorf("Error() = %q", msg)
	}
	if text := resultText(t, err.result()); strings.Contains(text, "actual_bytes") {
		t.Errorf("an undeclared length reported actual_bytes: %s", text)
	}
}

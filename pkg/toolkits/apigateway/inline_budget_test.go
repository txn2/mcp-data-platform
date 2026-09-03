package apigateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// The inline budget (issue #1587) is the model-context limit on what
// api_invoke_endpoint returns; max_response_bytes stays the read cap.

func TestParseConfig_MaxInlineBytes(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		c, err := ParseConfig(map[string]any{"base_url": "https://api.example.com"})
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		if c.MaxInlineBytes != DefaultMaxInlineBytes {
			t.Errorf("MaxInlineBytes = %d; want %d", c.MaxInlineBytes, DefaultMaxInlineBytes)
		}
	})
	t.Run("set", func(t *testing.T) {
		c, err := ParseConfig(map[string]any{"base_url": "https://api.example.com", "max_inline_bytes": float64(4096)})
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		if c.MaxInlineBytes != 4096 {
			t.Errorf("MaxInlineBytes = %d; want 4096", c.MaxInlineBytes)
		}
	})
	t.Run("non-positive", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{"base_url": "https://api.example.com", "max_inline_bytes": int64(0)})
		if err == nil || !strings.Contains(err.Error(), "max_inline_bytes must be positive") {
			t.Errorf("ParseConfig: got %v; want max_inline_bytes error", err)
		}
	})
}

func TestInlineBudget(t *testing.T) {
	cases := []struct {
		name         string
		inline, read int64
		want         int64
	}{
		{"defaults", 0, 0, DefaultMaxInlineBytes},
		{"budget under the read cap", 4096, 1 << 20, 4096},
		{"read cap bounds the budget", 1 << 20, 4096, 4096},
		{"unset budget under a low read cap", 0, 100, 100},
		{"unset read cap", 1 << 20, 0, 1 << 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inlineBudget(Config{MaxInlineBytes: tc.inline, MaxResponseBytes: tc.read})
			if got != tc.want {
				t.Errorf("inlineBudget = %d; want %d", got, tc.want)
			}
		})
	}
}

func TestInlineBudgetHint(t *testing.T) {
	declared := inlineBudgetHint(1024, 3000243)
	for _, want := range []string{"of 3000243 bytes", "max_inline_bytes (1024)", "budget on the tool result", "api_export", "export_arguments"} {
		if !strings.Contains(declared, want) {
			t.Errorf("hint %q lacks %q", declared, want)
		}
	}
	if undeclared := inlineBudgetHint(1024, -1); !strings.Contains(undeclared, "of undeclared length") {
		t.Errorf("hint %q; want it to say the length was undeclared", undeclared)
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

// TestHandleInvoke_CutAtInlineBudget: through the handler, a JSON body past
// max_inline_bytes comes back cut, with its read size, the hint, and the
// api_export arguments, and the rendered result is inside the budget; the
// same call without api_export registered is cut and flagged but not steered.
func TestHandleInvoke_CutAtInlineBudget(t *testing.T) {
	payload := `{"rows":"` + strings.Repeat("x", 5000) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	newToolkit := func(withExport bool) *Toolkit {
		tk := New("primary")
		if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL, "max_inline_bytes": 1024}); err != nil {
			t.Fatalf("AddConnection: %v", err)
		}
		if withExport {
			tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
		}
		return tk
	}
	in := InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x", Query: map[string]any{"q": "1"}, TimeoutSeconds: 5}

	res, out := invokeWalkCall(t, newToolkit(true), in)
	if !out.BodyTruncated || out.BodyBytes != 1024 {
		t.Fatalf("truncated=%v body_bytes=%d; want cut at 1024", out.BodyTruncated, out.BodyBytes)
	}
	if body, _ := out.Body.(string); body == "" || len(body) >= 1024 {
		t.Errorf("body holds %d bytes; want a cut body inside the 1024 budget the whole result is held to", len(body))
	}
	if !strings.Contains(out.Hint, "max_inline_bytes (1024)") {
		t.Errorf("hint = %q; want the budget named", out.Hint)
	}
	if out.ExportArguments == nil || out.ExportArguments.Path != "/v1/x" || out.ExportArguments.TimeoutSeconds != 0 {
		t.Errorf("export_arguments = %+v; want the same call without the inline timeout", out.ExportArguments)
	}
	text, _ := res.Content[0].(*mcp.TextContent)
	var wire map[string]any
	if err := json.Unmarshal([]byte(text.Text), &wire); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if wire["body_bytes"] != float64(1024) || wire["export_arguments"] == nil {
		t.Errorf("wire = %v; want body_bytes and export_arguments on the envelope", wire)
	}
	if len(text.Text) > 1024 {
		t.Errorf("rendered result is %d characters; want it inside the 1024 budget", len(text.Text))
	}

	_, plain := invokeWalkCall(t, newToolkit(false), in)
	if !plain.BodyTruncated || plain.Hint != "" || plain.ExportArguments != nil {
		t.Errorf("without api_export: truncated=%v hint=%q export=%+v; want cut, no steer", plain.BodyTruncated, plain.Hint, plain.ExportArguments)
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

// TestDefaultMaxInlineBytesFitsAClientToolResult guards the one property the
// number itself carries (issue #1606): the default bounds a rendered tool
// result, so it has to sit under the size a client was measured refusing.
// That a result is actually held to it is the subject of the test below. A
// deployment whose client accepts more raises max_inline_bytes on the
// connection.
func TestDefaultMaxInlineBytesFitsAClientToolResult(t *testing.T) {
	const measuredRefusal = int64(64_213)
	if DefaultMaxInlineBytes <= 0 || DefaultMaxInlineBytes >= measuredRefusal {
		t.Errorf("DefaultMaxInlineBytes = %d; want a positive budget under the %d-character tool result issue #1606 measured refused", DefaultMaxInlineBytes, measuredRefusal)
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
	if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL, "max_inline_bytes": budget}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))

	res, out := invokeWalkCall(t, tk, InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x"})
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

// TestHandleInvoke_AScriptRunIsNotHeldToTheModelContextBudget: the inline
// budget exists to keep a result readable by a model. A managed script has no
// model in it: it parses the response in code, a cut body is not parseable,
// and the steer to api_export is not something a run can act on mid-script. So
// a run reads to the connection's cap and gets its response whole, the same
// exemption enrichment makes for a script caller (issue #1606).
func TestHandleInvoke_AScriptRunIsNotHeldToTheModelContextBudget(t *testing.T) {
	payload := `{"rows":"` + strings.Repeat("x", 5000) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL, "max_inline_bytes": 1024}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))
	in := InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x"}

	// The control: the same call from a model is cut at the budget.
	if _, model := invokeWalkCall(t, tk, in); !model.BodyTruncated {
		t.Fatalf("a model's call was not cut; the case needs a response past the budget")
	}

	ctx := mcpcontext.WithSource(context.Background(), mcpcontext.SourceScript)
	res, payloadOut, err := tk.handleInvoke(ctx, nil, in)
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	out, _ := payloadOut.(InvokeOutput)
	if res.IsError {
		t.Fatalf("invoke failed: %s", resultText(t, res))
	}
	if out.BodyTruncated || out.ExportArguments != nil || out.Hint != "" {
		t.Errorf("truncated=%v export=%v hint=%q; want a run's response returned whole", out.BodyTruncated, out.ExportArguments, out.Hint)
	}
	if out.BodyBytes != int64(len(payload)) {
		t.Errorf("body_bytes = %d; want the whole %d-byte response read", out.BodyBytes, len(payload))
	}
	body, _ := out.Body.(map[string]any)
	if rows, _ := body["rows"].(string); len(rows) != 5000 {
		t.Errorf("body rows hold %d characters; want the whole 5000 parsed", len(rows))
	}
}

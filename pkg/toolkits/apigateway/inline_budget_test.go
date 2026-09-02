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
	for _, want := range []string{"of 3000243 bytes", "max_inline_bytes (1024)", "first 1024 bytes", "api_export", "export_arguments"} {
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
// max_inline_bytes comes back cut to the budget with its size, the hint, and
// the api_export arguments; the same call without api_export registered is
// cut and flagged but not steered.
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
	if body, _ := out.Body.(string); len(body) != 1024 {
		t.Errorf("body holds %d bytes; want 1024", len(body))
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

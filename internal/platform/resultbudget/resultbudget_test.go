package resultbudget

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// TestDefaultMaxBytesFitsAClientToolResult guards the one property the
// number carries (issue #1606): the default bounds a rendered tool result,
// so it has to sit under the size a client was measured refusing.
func TestDefaultMaxBytesFitsAClientToolResult(t *testing.T) {
	const measuredRefusal = 64_213
	if DefaultMaxBytes <= 0 || DefaultMaxBytes >= measuredRefusal {
		t.Errorf("DefaultMaxBytes = %d; want a positive budget under the %d-character tool result issue #1606 measured refused", DefaultMaxBytes, measuredRefusal)
	}
}

func TestConfigFor(t *testing.T) {
	cfg := Config{MaxBytes: 8192, PerTool: map[string]int{"trino_query": 65536}}
	cases := []struct {
		name string
		cfg  Config
		tool string
		want int
	}{
		{"unset takes the default", Config{}, "fetch", DefaultMaxBytes},
		{"the platform budget", cfg, "fetch", 8192},
		{"a per-tool override", cfg, "trino_query", 65536},
		{"an override on an unset budget", Config{PerTool: map[string]int{"fetch": 100}}, "fetch", 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.For(tc.tool); got != tc.want {
				t.Errorf("For(%s) = %d; want %d", tc.tool, got, tc.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"empty", Config{}, ""},
		{"set", Config{MaxBytes: 1024, PerTool: map[string]int{"fetch": 2048}}, ""},
		{"negative budget", Config{MaxBytes: -1}, "max_bytes must not be negative"},
		{"zero override", Config{PerTool: map[string]int{"fetch": 0}}, "per_tool.fetch must be positive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Errorf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v; want %q", err, tc.want)
			}
		})
	}
}

type fakeFitter struct {
	accept bool
	calls  int
	args   json.RawMessage
}

func (f *fakeFitter) FitResult(_ string, args json.RawMessage, res *mcp.CallToolResult, _ int) bool {
	f.calls++
	f.args = args
	if f.accept {
		res.Content = []mcp.Content{&mcp.TextContent{Text: "fitted"}}
	}
	return f.accept
}

func bigResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 5000)}}}
}

func callReq() *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "fetch", Arguments: json.RawMessage(`{"k":"v"}`)}}
}

// run sends one tools/call through the middleware with the PlatformContext
// the tool-call middleware would have written, returning the result and the
// budget the handler saw on its context.
func run(t *testing.T, pc *middleware.PlatformContext, method string, mw mcp.Middleware, handler func() mcp.Result) (out *mcp.CallToolResult, seen int) {
	t.Helper()
	seen = -1
	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		seen = mcpcontext.ResultBudget(ctx)
		return handler(), nil
	}
	ctx := context.Background()
	if pc != nil {
		ctx = middleware.WithPlatformContext(ctx, pc)
	}
	res, err := mw(next)(ctx, method, callReq())
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	out, _ = res.(*mcp.CallToolResult)
	return out, seen
}

// textAt is a content block's text, failing the test when it is not text.
func textAt(t *testing.T, c mcp.Content) string {
	t.Helper()
	tc, ok := c.(*mcp.TextContent)
	if !ok {
		t.Fatalf("content %v is not text", c)
	}
	return tc.Text
}

func TestMiddleware(t *testing.T) {
	cfg := Config{MaxBytes: 1024}
	model := &middleware.PlatformContext{ToolName: "fetch", Source: middleware.SourceMCP}

	accepting := func(f *fakeFitter) FitterLookup { return func(string) toolkit.ResultFitter { return f } }
	t.Run("a tool with no fitter is never cut, though its handler sees the budget", func(t *testing.T) {
		res, seen := run(t, model, "tools/call", Middleware(cfg, func(string) toolkit.ResultFitter { return nil }), func() mcp.Result { return bigResult() })
		if seen != 1024 {
			t.Errorf("handler saw budget %d; want 1024", seen)
		}
		if size := TextSize(res); size != 5000 {
			t.Errorf("text is %d; want the whole 5000", size)
		}
	})
	t.Run("the tool's fitter is used and handed the call's arguments", func(t *testing.T) {
		f := &fakeFitter{accept: true}
		res, _ := run(t, model, "tools/call", Middleware(cfg, func(string) toolkit.ResultFitter { return f }), func() mcp.Result { return bigResult() })
		if f.calls != 1 || string(f.args) != `{"k":"v"}` {
			t.Errorf("fitter calls=%d args=%s", f.calls, f.args)
		}
		if textAt(t, res.Content[0]) != "fitted" {
			t.Error("the fitter's result was not returned")
		}
	})
	t.Run("a result its fitter declines is left whole", func(t *testing.T) {
		f := &fakeFitter{}
		res, _ := run(t, model, "tools/call", Middleware(cfg, accepting(f)), func() mcp.Result { return bigResult() })
		if f.calls != 1 || TextSize(res) != 5000 {
			t.Errorf("fitter calls=%d text=%d; want the result whole after the decline", f.calls, TextSize(res))
		}
	})
	t.Run("a result within the budget is untouched and no fitter is asked", func(t *testing.T) {
		f := &fakeFitter{accept: true}
		small := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
		res, _ := run(t, model, "tools/call", Middleware(cfg, func(string) toolkit.ResultFitter { return f }), func() mcp.Result { return small })
		if res != small || f.calls != 0 {
			t.Errorf("fitter calls=%d; want the result passed through", f.calls)
		}
	})
	t.Run("an error result is never cut", func(t *testing.T) {
		errRes := bigResult()
		errRes.IsError = true
		res, _ := run(t, model, "tools/call", Middleware(cfg, accepting(&fakeFitter{accept: true})), func() mcp.Result { return errRes })
		if TextSize(res) != 5000 {
			t.Error("an error result was cut")
		}
	})
	for _, source := range []string{middleware.SourceREST, middleware.SourceScript, middleware.SourceAdmin} {
		t.Run(source+" is never fitted and carries no budget", func(t *testing.T) {
			pc := &middleware.PlatformContext{ToolName: "fetch", Source: source}
			f := &fakeFitter{accept: true}
			res, seen := run(t, pc, "tools/call", Middleware(cfg, accepting(f)), func() mcp.Result { return bigResult() })
			if seen != 0 || TextSize(res) != 5000 || f.calls != 0 {
				t.Errorf("budget seen %d, text %d, fitter calls %d; want no budget and the whole result", seen, TextSize(res), f.calls)
			}
		})
	}
	t.Run("no PlatformContext passes through", func(t *testing.T) {
		res, seen := run(t, nil, "tools/call", Middleware(cfg, accepting(&fakeFitter{accept: true})), func() mcp.Result { return bigResult() })
		if seen != 0 || TextSize(res) != 5000 {
			t.Error("a call with no PlatformContext was fitted")
		}
	})
	t.Run("another method passes through", func(t *testing.T) {
		_, seen := run(t, model, "tools/list", Middleware(cfg, nil), func() mcp.Result { return &mcp.ListToolsResult{} })
		if seen != 0 {
			t.Error("tools/list was given a budget")
		}
	})
}

func TestCallArguments(t *testing.T) {
	if callArguments(nil) != nil {
		t.Error("nil request has arguments")
	}
	if callArguments(&mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}) != nil {
		t.Error("a non-call request has arguments")
	}
}

// stubToolkit is a registered toolkit with no fitter; only what the
// registry and the lookup read is implemented.
type stubToolkit struct {
	registry.Toolkit
	name  string
	tools []string
}

func (*stubToolkit) Kind() string       { return "stub" }
func (s *stubToolkit) Name() string     { return s.name }
func (s *stubToolkit) Tools() []string  { return s.tools }
func (*stubToolkit) Connection() string { return "" }

// fittingToolkit is a stubToolkit with a fitter.
type fittingToolkit struct{ stubToolkit }

func (*fittingToolkit) FitResult(string, json.RawMessage, *mcp.CallToolResult, int) bool { return true }

// TestRegistryLookup resolves a tool to its toolkit's fitter, and to nil
// for a tool no toolkit serves or a toolkit that has none.
func TestRegistryLookup(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Register(&fittingToolkit{stubToolkit{name: "fits", tools: []string{"fit_tool"}}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&stubToolkit{name: "plain", tools: []string{"plain_tool"}}); err != nil {
		t.Fatal(err)
	}
	lookup := RegistryLookup(reg)
	if lookup("fit_tool") == nil {
		t.Error("a fitting toolkit's tool resolved to no fitter")
	}
	if lookup("plain_tool") != nil {
		t.Error("a toolkit with no fitter resolved to one")
	}
	if lookup("unknown") != nil {
		t.Error("a tool no toolkit serves resolved to a fitter")
	}
}

// appendOuter stands where the call reference and enrichment do: between
// Middleware and Capture, appending a block and mirroring a key.
func appendOuter(block string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			res, ok := result.(*mcp.CallToolResult)
			if !ok {
				return result, err
			}
			res.Content = append(res.Content, &mcp.TextContent{Text: block})
			var m map[string]any
			if toolkit.DecodeStructured(res.StructuredContent, &m) {
				m["call_reference"] = "mcp:call:1"
				res.StructuredContent = m
			}
			return res, err
		}
	}
}

// shapingFitter keeps the first 10 characters of the text and sets a flag in
// the structured output, as a tool's own fitter does.
type shapingFitter struct{ room int }

func (f *shapingFitter) FitResult(_ string, _ json.RawMessage, res *mcp.CallToolResult, budget int) bool {
	f.room = budget
	var out map[string]any
	if !toolkit.DecodeStructured(res.StructuredContent, &out) {
		return false
	}
	delete(out, "rows")
	out["truncated"] = true
	return toolkit.SetFittedResult(res, []byte(strings.Repeat("x", 10)), out) == nil
}

// chain runs one model's tools/call through Middleware, an outer appender and
// Capture around handler.
func chain(t *testing.T, cfg Config, lookup FitterLookup, appended string, handler func() *mcp.CallToolResult) *mcp.CallToolResult {
	t.Helper()
	inner := Capture()(func(context.Context, string, mcp.Request) (mcp.Result, error) { return handler(), nil })
	h := Middleware(cfg, lookup)(appendOuter(appended)(inner))
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{ToolName: "fetch", Source: middleware.SourceMCP})
	res, err := h(ctx, "tools/call", callReq())
	if err != nil {
		t.Fatal(err)
	}
	out, _ := res.(*mcp.CallToolResult)
	return out
}

// TestFit_TheBudgetCoversWhatOuterLayersAppend: the tool's own part is fitted
// to the budget less what the outer layers appended, the appended block and
// its mirrored key are kept, and a key the fitter dropped stays dropped.
func TestFit_TheBudgetCoversWhatOuterLayersAppend(t *testing.T) {
	appended := strings.Repeat("r", 200)
	f := &shapingFitter{}
	res := chain(t, Config{MaxBytes: 1000}, func(string) toolkit.ResultFitter { return f }, appended, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 5000)}},
			StructuredContent: json.RawMessage(`{"rows":[1,2,3],"kept":"yes"}`),
		}
	})
	if f.room != 800 {
		t.Errorf("the fitter was offered %d; want the 1000 budget less the 200 appended", f.room)
	}
	if TextSize(res) > 1000 || textAt(t, res.Content[len(res.Content)-1]) != appended {
		t.Errorf("text %d, last block %q; want the whole inside the budget with the appended block kept", TextSize(res), textAt(t, res.Content[len(res.Content)-1]))
	}
	var structured map[string]any
	if !toolkit.DecodeStructured(res.StructuredContent, &structured) {
		t.Fatal("structured content unreadable")
	}
	if truncated, _ := structured["truncated"].(bool); structured["call_reference"] != "mcp:call:1" || !truncated || structured["kept"] != "yes" {
		t.Errorf("structured = %v; want the fitted output with the outer key kept", structured)
	}
	if _, ok := structured["rows"]; ok {
		t.Error("a key the fitter dropped was put back")
	}
}

// TestCapture_WithoutMiddlewareDoesNothing: a call the budget does not fit
// carries no recorder, and Capture passes the result through.
func TestCapture_WithoutMiddlewareDoesNothing(t *testing.T) {
	want := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
	got, err := Capture()(func(context.Context, string, mcp.Request) (mcp.Result, error) { return want, nil })(context.Background(), "tools/call", callReq())
	if err != nil || got != want {
		t.Errorf("Capture changed an unfitted call: %v %v", got, err)
	}
}

func TestKeepOuterKeys_NotAnObject(t *testing.T) {
	own := &ownResult{captured: true, structured: json.RawMessage(`[1]`)}
	fitted := json.RawMessage(`[1]`)
	if got, ok := keepOuterKeys(fitted, own, json.RawMessage(`[1,2]`)).(json.RawMessage); !ok || string(got) != `[1]` {
		t.Errorf("got %v; want the fitted content when it is not an object", got)
	}
	if got, ok := keepOuterKeys(fitted, nil, nil).(json.RawMessage); !ok || string(got) != `[1]` {
		t.Errorf("got %v; want the fitted content with no capture", got)
	}
}

// TestFit_AMapMutatedInPlaceByAnOuterLayer: enrichment merges its keys into
// a map-typed structured value in place. The capture keeps the tool's own
// keys apart, so the merged key survives the fit.
func TestFit_AMapMutatedInPlaceByAnOuterLayer(t *testing.T) {
	inPlace := func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if res, ok := result.(*mcp.CallToolResult); ok {
				if m, ok := res.StructuredContent.(map[string]any); ok {
					m["semantic_context"] = "kept"
				}
			}
			return result, err
		}
	}
	f := &shapingFitter{}
	inner := Capture()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 5000)}},
			StructuredContent: map[string]any{"rows": []any{1, 2}},
		}, nil
	})
	h := Middleware(Config{MaxBytes: 1000}, func(string) toolkit.ResultFitter { return f })(inPlace(inner))
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{ToolName: "fetch", Source: middleware.SourceMCP})
	result, err := h(ctx, "tools/call", callReq())
	if err != nil {
		t.Fatal(err)
	}
	res, _ := result.(*mcp.CallToolResult)
	var structured map[string]any
	if !toolkit.DecodeStructured(res.StructuredContent, &structured) || structured["semantic_context"] != "kept" {
		t.Errorf("structured = %v; want the key merged in place by the outer layer kept", structured)
	}
}

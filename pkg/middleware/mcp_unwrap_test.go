package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPUnwrapJSONMiddleware(t *testing.T) {
	// Helper to build a tools/call request with given tool name and arguments.
	makeReq := func(toolName string, args map[string]any) mcp.Request {
		var rawArgs json.RawMessage
		if args != nil {
			rawArgs, _ = json.Marshal(args)
		}
		return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
			Params: &mcp.CallToolParamsRaw{
				Name:      toolName,
				Arguments: rawArgs,
			},
		}
	}

	// Helper to extract arguments from a request.
	getArgs := func(t *testing.T, req mcp.Request) map[string]any {
		t.Helper()
		params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
		if !ok {
			t.Fatal("expected *CallToolParamsRaw")
		}
		var args map[string]any
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		return args
	}

	noop := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	}

	mw := MCPUnwrapJSONMiddleware()
	handler := mw(noop)

	t.Run("injects unwrap_json into trino_query", func(t *testing.T) {
		req := makeReq("trino_query", map[string]any{"sql": "SELECT 1"})
		_, _ = handler(context.Background(), methodToolsCall, req)

		args := getArgs(t, req)
		if v, ok := args["unwrap_json"].(bool); !ok || !v {
			t.Errorf("expected unwrap_json=true, got %v", args["unwrap_json"])
		}
		// Original args preserved.
		if args["sql"] != "SELECT 1" {
			t.Errorf("expected sql preserved, got %v", args["sql"])
		}
	})

	t.Run("injects unwrap_json into trino_execute", func(t *testing.T) {
		req := makeReq("trino_execute", map[string]any{"sql": "INSERT INTO t VALUES (1)"})
		_, _ = handler(context.Background(), methodToolsCall, req)

		args := getArgs(t, req)
		if v, ok := args["unwrap_json"].(bool); !ok || !v {
			t.Errorf("expected unwrap_json=true, got %v", args["unwrap_json"])
		}
	})

	t.Run("does not override explicit false", func(t *testing.T) {
		req := makeReq("trino_query", map[string]any{
			"sql":         "SELECT 1",
			"unwrap_json": false,
		})
		_, _ = handler(context.Background(), methodToolsCall, req)

		args := getArgs(t, req)
		if v, ok := args["unwrap_json"].(bool); !ok || v {
			t.Errorf("expected unwrap_json=false (caller explicit), got %v", args["unwrap_json"])
		}
	})

	t.Run("does not override explicit true", func(t *testing.T) {
		req := makeReq("trino_query", map[string]any{
			"sql":         "SELECT 1",
			"unwrap_json": true,
		})
		_, _ = handler(context.Background(), methodToolsCall, req)

		args := getArgs(t, req)
		if v, ok := args["unwrap_json"].(bool); !ok || !v {
			t.Errorf("expected unwrap_json=true (caller explicit), got %v", args["unwrap_json"])
		}
	})

	t.Run("ignores non-trino tools", func(t *testing.T) {
		req := makeReq("datahub_search", map[string]any{"query": "test"})
		_, _ = handler(context.Background(), methodToolsCall, req)

		args := getArgs(t, req)
		if _, exists := args["unwrap_json"]; exists {
			t.Error("unwrap_json should not be injected into non-trino tools")
		}
	})

	t.Run("ignores trino_explain", func(t *testing.T) {
		req := makeReq("trino_explain", map[string]any{"sql": "SELECT 1"})
		_, _ = handler(context.Background(), methodToolsCall, req)

		args := getArgs(t, req)
		if _, exists := args["unwrap_json"]; exists {
			t.Error("unwrap_json should not be injected into trino_explain")
		}
	})

	t.Run("passes through non-tools/call methods", func(t *testing.T) {
		called := false
		passthrough := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			called = true
			return &mcp.CallToolResult{}, nil
		})
		_, _ = passthrough(context.Background(), "resources/list", nil)
		if !called {
			t.Error("expected non-tools/call to pass through")
		}
	})

	t.Run("handles nil arguments gracefully", func(t *testing.T) {
		req := makeReq("trino_query", nil)
		_, _ = handler(context.Background(), methodToolsCall, req)

		args := getArgs(t, req)
		if v, ok := args["unwrap_json"].(bool); !ok || !v {
			t.Errorf("expected unwrap_json=true even with nil initial args, got %v", args["unwrap_json"])
		}
	})
}

func TestInjectUnwrapJSONDefault(t *testing.T) {
	t.Run("nil request", func(_ *testing.T) {
		// Should not panic.
		injectUnwrapJSONDefault(nil)
	})

	t.Run("nil params", func(_ *testing.T) {
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{}
		injectUnwrapJSONDefault(req)
	})

	t.Run("malformed JSON arguments", func(_ *testing.T) {
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
			Params: &mcp.CallToolParamsRaw{
				Name:      "trino_query",
				Arguments: json.RawMessage(`{invalid`),
			},
		}
		// Should not panic — let the handler deal with bad JSON.
		injectUnwrapJSONDefault(req)
	})
}

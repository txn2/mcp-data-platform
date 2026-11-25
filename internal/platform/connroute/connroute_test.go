package connroute

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func deps() Deps {
	return Deps{
		Connections: func() []knowledge.ConnectionInfo {
			return []knowledge.ConnectionInfo{
				{Name: "warehouse", Bound: "warehouse", Kind: "trino"},
				{Name: "apps", Bound: "apps", Kind: kindAppDB},
				{Name: "crm", Bound: "crm", Kind: "api"},
			}
		},
		ToolKind: func(tool string) string {
			return map[string]string{"trino_query": "trino", "manage_table": "portal", "api_invoke_endpoint": "api"}[tool]
		},
		KindTools: func(kind string) []string {
			return map[string][]string{kindAppDB: {"manage_schema"}, "api": {"api_invoke_endpoint", "api_discover"}}[kind]
		},
	}
}

func call(t *testing.T, d Deps, tool string, args map[string]any, result mcp.Result) mcp.Result {
	t.Helper()
	raw, _ := json.Marshal(args)
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool, Arguments: raw}}
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) { return result, nil }
	got, err := Middleware(d)(next)(context.Background(), methodToolsCall, req)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func refusal(msg string) *mcp.CallToolResult {
	return middleware.BuildErrorResult(middleware.NotFoundError("unknown_connection", msg, "pick one"))
}

func message(r mcp.Result) string {
	ctr, _ := r.(*mcp.CallToolResult)
	if pe := platformErr(r); pe != nil {
		return pe.Message
	}
	var b strings.Builder
	for _, c := range ctr.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_, _ = b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// platformErr is a tool result's platform error, nil when it carries none.
func platformErr(r mcp.Result) *middleware.PlatformError {
	ctr, ok := r.(*mcp.CallToolResult)
	if !ok {
		return nil
	}
	var pe *middleware.PlatformError
	if !errors.As(ctr.GetError(), &pe) {
		return nil
	}
	return pe
}

func TestRoutesAConnectionOfAnotherKind(t *testing.T) {
	got := call(t, deps(), "trino_query", map[string]any{"connection": "apps"}, refusal(`unknown connection: "apps"`))
	msg := message(got)
	if !strings.Contains(msg, `"apps" is an application database connection, which this tool does not serve`) {
		t.Fatalf("kind not named: %s", msg)
	}
	for _, want := range []string{"manage_schema", "platform.query in a managed script", `unknown connection: "apps"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	if pe := platformErr(got); pe == nil || pe.Code != "unknown_connection" {
		t.Errorf("code changed: %+v", pe)
	}

	// A bare error result gets the sentence as a text block.
	bare := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unknown connection"}}}
	if msg := message(call(t, deps(), "trino_query", map[string]any{"connection": "crm"}, bare)); !strings.Contains(msg, "api_discover, api_invoke_endpoint") {
		t.Errorf("bare result: %s", msg)
	}
}

func TestLeavesOtherRefusalsAlone(t *testing.T) {
	cases := map[string]struct {
		tool string
		args map[string]any
		res  mcp.Result
	}{
		"served here":             {"trino_query", map[string]any{"connection": "warehouse"}, refusal("syntax error")},
		"unknown everywhere":      {"trino_query", map[string]any{"connection": "nope"}, refusal("unknown")},
		"no connection":           {"trino_query", map[string]any{"sql": "x"}, refusal("x")},
		"kind with no connection": {"manage_table", map[string]any{"connection": "apps"}, refusal("no scratch")},
		"not a toolkit tool":      {"search", map[string]any{"connection": "apps"}, refusal("x")},
		"success":                 {"trino_query", map[string]any{"connection": "apps"}, &mcp.CallToolResult{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			before := message(tc.res)
			if got := message(call(t, deps(), tc.tool, tc.args, tc.res)); got != before {
				t.Errorf("rewritten: %q -> %q", before, got)
			}
		})
	}
	if tool, conn := callConnection(nil); tool != "" || conn != "" {
		t.Error("a nil request named a connection")
	}
	d := deps()
	d.KindTools = nil
	if s := d.sentence("x", "custom"); !strings.Contains(s, `"x" is a custom connection, which this tool does not serve.`) {
		t.Errorf("sentence = %q", s)
	}
}

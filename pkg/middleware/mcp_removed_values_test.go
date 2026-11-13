package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/removedvalue"
)

func removedRequest(tool string, args map[string]any) *mcp.CallToolRequest {
	raw, _ := json.Marshal(args)
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool, Arguments: raw}}
}

func schemaRefusal() *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
		Text: `validating "arguments": validating root: validating /properties/command: enum: old_cmd does not equal any of: [new_cmd]`,
	}}}
}

// #2441: a value a tool removed is answered with its replacement, and only
// for the tool and argument it was removed from.
func TestRemovedValueResult(t *testing.T) {
	removedvalue.Register("removed_tool", "command", "old_cmd", "new_cmd with kind=x")
	cases := []struct {
		name   string
		req    mcp.Request
		result mcp.Result
		want   string
	}{
		{"the removed value", removedRequest("removed_tool", map[string]any{"command": "old_cmd"}), schemaRefusal(), "old_cmd was removed: use new_cmd with kind=x"},
		{"another tool", removedRequest("other_tool", map[string]any{"command": "old_cmd"}), schemaRefusal(), ""},
		{"another argument", removedRequest("removed_tool", map[string]any{"mode": "old_cmd"}), schemaRefusal(), ""},
		{"another value", removedRequest("removed_tool", map[string]any{"command": "other"}), schemaRefusal(), ""},
		{"a non-string value", removedRequest("removed_tool", map[string]any{"command": 3}), schemaRefusal(), ""},
		{"no arguments", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "removed_tool"}}, schemaRefusal(), ""},
		{"arguments not an object", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "removed_tool", Arguments: json.RawMessage(`[1]`)}}, schemaRefusal(), ""},
		{"no params", &mcp.CallToolRequest{}, schemaRefusal(), ""},
		{"a success", removedRequest("removed_tool", map[string]any{"command": "old_cmd"}), &mcp.CallToolResult{}, ""},
		{
			"another error", removedRequest("removed_tool", map[string]any{"command": "old_cmd"}),
			&mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}}, "",
		},
		{"not a tool result", removedRequest("removed_tool", map[string]any{"command": "old_cmd"}), &mcp.ListToolsResult{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := removedValueResult(c.req, c.result)
			if c.want == "" {
				if got != nil {
					t.Fatalf("want no answer, got %v", got)
				}
				return
			}
			ctr, ok := got.(*mcp.CallToolResult)
			if !ok || !ctr.IsError || !strings.Contains(extractMCPErrorMessage(ctr), c.want) {
				t.Fatalf("want an error naming %q, got %#v", c.want, got)
			}
			if pe := ctr.GetError(); pe == nil || !strings.Contains(pe.Error(), c.want) {
				t.Errorf("the result carries no structured error naming the replacement: %v", pe)
			}
		})
	}
}

// The error contract answers a removed value in place of the schema's refusal.
func TestErrorContractAnswersARemovedValue(t *testing.T) {
	removedvalue.Register("removed_tool_2", "command", "gone", "kept")
	handler := MCPErrorContractMiddleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return schemaRefusal(), nil
	})
	got, err := handler(context.Background(), methodToolsCall, removedRequest("removed_tool_2", map[string]any{"command": "gone"}))
	if err != nil {
		t.Fatal(err)
	}
	ctr, _ := got.(*mcp.CallToolResult)
	if ctr == nil || !strings.Contains(extractMCPErrorMessage(ctr), "gone was removed: use kept") {
		t.Fatalf("the contract answered %#v", got)
	}
}

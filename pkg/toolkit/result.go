package toolkit

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrorResult builds an MCP tool result carrying an in-band error.
//
// Tool handlers surface failures through CallToolResult.IsError rather than a
// transport-level JSON-RPC error: a handler that returns a Go error aborts the
// call, whereas an in-band error lets the model read and react to the message.
// The message is marshaled as a JSON struct, never formatted into a JSON
// literal, so it is escaped correctly for any input.
func ErrorResult(msg string) *mcp.CallToolResult {
	// A struct with a single string field cannot fail to marshal, so the error
	// is intentionally ignored rather than handled with an unreachable branch.
	b, _ := json.Marshal(struct { //nolint:errcheck // marshaling one string field cannot fail
		Error string `json:"error"`
	}{Error: msg})
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}

// MarshalResultJSON renders v exactly as JSONResult puts it in a tool result's
// text block. A handler that must size a result before returning it — one
// holding itself to a budget on what the client receives rather than on what it
// read (issue #1606) — measures through this, so the budget cannot drift from
// the encoder the result is actually built with.
func MarshalResultJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ") //nolint:wrapcheck // the caller reports the marshal failure in its own terms
}

// JSONResult marshals v to indented JSON and returns it as an MCP tool result.
// A marshal failure is surfaced as an in-band ErrorResult rather than a Go
// error, matching how tool handlers report failures.
func JSONResult(v any) *mcp.CallToolResult {
	b, err := MarshalResultJSON(v)
	if err != nil {
		return ErrorResult("internal error marshaling response: " + err.Error())
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}

// JSONResultTyped adapts JSONResult to the typed tool-handler return signature
// (ctx, req, in) (*mcp.CallToolResult, Out, error). The middle structured-output
// value is unused; handlers that emit structured content build the result
// directly.
func JSONResultTyped(v any) (*mcp.CallToolResult, any, error) {
	return JSONResult(v), nil, nil
}

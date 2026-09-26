package toolkit

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ResultFitter is implemented by a toolkit whose tool results have a shape
// of their own to be cut to a model client's context budget (#1878).
//
// The budget is a property of the MCP response to a model, not of any
// toolkit: the platform's result-budget middleware measures a model
// caller's results, and only a result past the budget reaches a fitter. A
// fitter cuts where its result can be cut without handing back something
// unreadable -- a response body, a query's rows -- flags the cut, and
// steers to the tool that returns the whole result without a context cost.
// Only a tool whose cut can be recovered that way has a fitter: a tool
// without one is never cut, since a knowledge page or a manual cut in half
// has no way back to the rest.
type ResultFitter interface {
	// FitResult shortens res, a successful result of tool that renders past
	// budget, so its text is within budget. args are the arguments the call
	// was made with. It reports whether it fitted the result; false means it
	// has no recoverable cut for this result, res is untouched, and the
	// result reaches the model whole.
	FitResult(tool string, args json.RawMessage, res *mcp.CallToolResult, budget int) bool
}

// SetFittedResult replaces a result's body with a fitted one: text becomes
// the result's only text block, and structured becomes its structured
// content, compactly encoded as the MCP SDK encodes it. Blocks that are not
// text are kept. Replacing both copies is what bounds the message a model
// client receives, since the SDK sends a typed tool's output twice.
func SetFittedResult(res *mcp.CallToolResult, text []byte, structured any) error {
	raw, err := json.Marshal(structured)
	if err != nil {
		return fmt.Errorf("encoding the fitted structured content: %w", err)
	}
	content := make([]mcp.Content, 0, len(res.Content))
	replaced := false
	for _, c := range res.Content {
		if _, ok := c.(*mcp.TextContent); ok {
			if !replaced {
				content = append(content, &mcp.TextContent{Text: string(text)})
				replaced = true
			}
			continue
		}
		content = append(content, c)
	}
	if !replaced {
		content = append([]mcp.Content{&mcp.TextContent{Text: string(text)}}, content...)
	}
	res.Content = content
	res.StructuredContent = json.RawMessage(raw)
	return nil
}

// DecodeStructured reads a result's structured content into v, reporting
// whether it could. The MCP SDK hands a typed tool's output to a middleware
// as its encoded JSON; a value in any other form is re-encoded first.
func DecodeStructured(structured, v any) bool {
	if structured == nil {
		return false
	}
	raw, ok := structured.(json.RawMessage)
	if !ok {
		b, err := json.Marshal(structured)
		if err != nil {
			return false
		}
		raw = b
	}
	return json.Unmarshal(raw, v) == nil
}

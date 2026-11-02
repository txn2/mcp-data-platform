package postgres

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The error categories of the platform's error contract (pkg/middleware's
// ErrCategory* values). The toolkit builds its own envelope because
// pkg/middleware imports the registry, which imports this package.
const (
	categoryClientInput = "client_input"
	categoryNotFound    = "not_found"
	categoryFailed      = "tool_error"

	codeMissingParameter = "missing_required_parameter"
	codeNotFound         = "not_found"
	codeInvalidStatement = "invalid_statement"
)

// toolError is a failure in the platform's error contract: what a caller
// branches on (code, category), reads (message, hint), and what audit and the
// call metrics read through GetError.
type toolError struct {
	code, category, message, hint string
}

// Error implements error.
func (e *toolError) Error() string { return e.message }

// ErrorCategory is the category the audit row and the call metrics record.
func (e *toolError) ErrorCategory() string { return e.category }

// ErrorCode is the stable code a caller branches on.
func (e *toolError) ErrorCode() string { return e.code }

// result renders e the way pkg/middleware's BuildErrorResult does: one text
// block holding the message, code and hint, the structuredContent.error object,
// and the error stashed on the result.
func (e *toolError) result() *mcp.CallToolResult {
	text := fmt.Sprintf("%s (code: %s)", e.message, e.code)
	if e.hint != "" {
		text = fmt.Sprintf("%s Hint: %s", text, e.hint)
	}
	envelope := map[string]any{"code": e.code, "category": e.category, "message": e.message}
	if e.hint != "" {
		envelope["hint"] = e.hint
	}
	res := &mcp.CallToolResult{
		StructuredContent: map[string]any{"error": envelope},
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
	}
	res.SetError(e)
	return res
}

func missingParameter(param string) *mcp.CallToolResult {
	return (&toolError{
		code: codeMissingParameter, category: categoryClientInput,
		message: fmt.Sprintf("the %q parameter is required", param),
		hint:    fmt.Sprintf("Supply %q and retry. This is a problem with the call's arguments, not a platform fault.", param),
	}).result()
}

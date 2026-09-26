package apigateway

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// FitResult holds an api_invoke_endpoint result to a model client's
// context budget (#1878), in the result's own shape: the body is cut, the
// result says so in body_truncated and a hint, and export_arguments carry
// the api_export call that streams the whole response into an asset. A
// walk's merged collection is not cut -- its merge already stopped at the
// budget -- only re-encoded. It is called by the platform's result-budget
// middleware, and only for a result past the budget. Any other tool, a
// result it cannot read, or one that still does not fit with its body cut,
// is declined and cut by the generic text cut.
func (t *Toolkit) FitResult(tool string, args json.RawMessage, res *mcp.CallToolResult, budget int) bool {
	if tool != ToolInvokeEndpoint {
		return false
	}
	var out InvokeOutput
	if !toolkit.DecodeStructured(res.StructuredContent, &out) {
		return false
	}
	var in InvokeInput
	if len(args) > 0 && json.Unmarshal(args, &in) != nil {
		return false
	}
	t.mu.RLock()
	hasExport := t.exportDeps != nil
	t.mu.RUnlock()
	text := fitToBudget(&out, in, int64(budget), hasExport)
	if len(text) > budget {
		// What is left past the budget is not the body -- an echoed
		// request body, say -- so there is no cut of this shape that fits.
		return false
	}
	return toolkit.SetFittedResult(res, text, out) == nil
}

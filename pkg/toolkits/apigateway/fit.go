package apigateway

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// FitResult holds an api_invoke_endpoint result to a model client's
// context budget (#1878), in the result's own shape: a list body is cut on
// its items, the result says how many it shows of how many, next_arguments
// name the call that reads on where the operation declares paging, and
// export_arguments carry the api_export call that streams the whole
// response into an asset (#1915); any other body is cut to a prefix and
// steered to api_export. A walk's merged collection is not cut -- its merge
// already stopped at the budget -- only re-encoded. It is called by the
// platform's result-budget middleware, and only for a result past the
// budget. Any other tool, a result it cannot read, or one that still does
// not fit with its body cut, is declined, and reaches the model whole.
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
	text, ok := fitToBudget(&out, in, budget, hasExport, t.pagingPlan(in, out.ResolvedPath))
	if !ok {
		return false
	}
	return toolkit.SetFittedResult(res, text, out) == nil
}

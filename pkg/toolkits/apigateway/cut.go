package apigateway

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/inlinefit"
	"github.com/txn2/mcp-data-platform/internal/listcut"
	"github.com/txn2/mcp-data-platform/internal/pagenext"
)

// fitToBudget holds a result to a model client's context budget and
// returns the rendering to hand back, or false when no cut fits and the
// result is left whole. Re-encoding compactly is tried first (#1606), then a
// list cut on its items (#1915), then a prefix cut (#1587). A walk's merged
// body already stopped at the budget, so it is only re-encoded.
func fitToBudget(out *InvokeOutput, in InvokeInput, budget int, hasExport bool, plan *pagenext.Plan) ([]byte, bool) {
	text, ok := inlinefit.RenderWithin(out, budget)
	if ok || out.WalkStats != nil || out.Body == nil {
		return text, ok
	}
	if text, ok := fitList(out, in, budget, hasExport, plan); ok {
		return text, true
	}
	return fitPrefix(out, in, budget, hasExport)
}

// fitList cuts a list body on its items. It reports false, leaving out as
// it found it, when the body carries no list or not even one item fits.
func fitList(out *InvokeOutput, in InvokeInput, budget int, hasExport bool, plan *pagenext.Plan) ([]byte, bool) {
	whole := *out
	var text []byte
	render := func(c listcut.Cut) bool {
		applyCut(out, in, c, budget, hasExport)
		var fits bool
		text, fits = inlinefit.RenderWithin(out, budget)
		return fits
	}
	if _, ok := listcut.Fit(whole.Body, in.Query, plan, render); !ok {
		*out = whole
		return nil, false
	}
	return text, true
}

// applyCut sets the output to a list cut: the body, the count, the call
// that reads on, the export call, and the hint naming them. The response's
// own pagination signal points past the whole page, so following it would
// skip the items cut; it is dropped, and next_arguments is the way on.
func applyCut(out *InvokeOutput, in InvokeInput, c listcut.Cut, budget int, hasExport bool) {
	out.Body = c.Body
	out.BodyTruncated = true
	out.Pagination = nil
	out.BodyItems = &listcut.Count{Shown: c.Shown, Total: c.Total}
	out.NextArguments = nil
	if c.NextQuery != nil {
		next := in
		next.Query = c.NextQuery
		out.NextArguments = &next
	}
	out.ExportArguments = nil
	exportTool := ""
	if hasExport {
		out.ExportArguments = exportArguments(in)
		exportTool = exportToolName
	}
	out.Hint = listcut.Hint(c, budget, ToolInvokeEndpoint, exportTool)
}

// fitPrefix cuts any other body to the longest prefix of its text that
// fits, flags it, and steers to api_export (#1587, #1606).
func fitPrefix(out *InvokeOutput, in InvokeInput, budget int, hasExport bool) ([]byte, bool) {
	body := inlinefit.BodyText(out.Body)
	setBody := func(s string) { out.Body = s }
	if !out.BodyTruncated {
		out.BodyTruncated = true
		out.Hint = contextBudgetHint(budget, out.BodyBytes)
	}
	steerToExport(out, in, hasExport)
	text := inlinefit.Fit(out, budget, body, setBody)
	// What is left past the budget is not the body -- an echoed request
	// body, say -- so there is no cut of this shape that fits.
	return text, len(text) <= budget
}

// contextBudgetHint is the steer on a body cut to a prefix by a model
// client's context budget. The budget bounds the rendered tool result, so
// the hint does not quote it as a count of body bytes returned (#1606).
func contextBudgetHint(budget int, bodyBytes int64) string {
	return fmt.Sprintf("response of %d bytes exceeded this client's context budget on a tool result (%d, tools.result_budget); "+
		"the body is cut to fit it. Use api_export with export_arguments plus a name to stream the whole response into a "+
		"portal asset (no model-context cost)", bodyBytes, budget)
}

// exportArguments is the api_export call that streams the same call into
// an asset, in the form the caller used (operation_id or method+path); the
// inline timeout is dropped because api_export has its own.
func exportArguments(in InvokeInput) *InvokeInput {
	if in.OperationID != "" {
		in.Method, in.Path = "", ""
	}
	in.TimeoutSeconds = 0
	return &in
}

// pagingPlan is how the operation a call invoked continues after a cut,
// read from the query parameters its spec declares. nil when the
// connection has no spec for it, or it declares no paging the gateway can
// advance.
func (t *Toolkit) pagingPlan(in InvokeInput, resolvedPath string) *pagenext.Plan {
	c, ok := t.lookup(in.Connection)
	if !ok || len(c.specs) == 0 {
		return nil
	}
	method, path := in.Method, in.Path
	if in.OperationID != "" {
		match, _ := resolveOperation(c.specs, in.OperationID, in.Spec)
		if match == nil {
			return nil
		}
		method, path = match.method, resolvedPath
	}
	params := declaredQueryParams(c.specs, strings.ToUpper(method), stripQueryAndFragment(path))
	plan, ok := pagenext.Detect(params)
	if !ok {
		return nil
	}
	return &plan
}

// declaredQueryParams is the query parameters the operation at method and
// path declares, its path item's included.
func declaredQueryParams(specs map[string]*specState, method, path string) []pagenext.Param {
	for _, st := range specs {
		if st == nil || st.doc == nil || st.doc.Paths == nil {
			continue
		}
		item := findMostSpecificPathMatch(st, path)
		if item == nil {
			continue
		}
		if op := operationForMethod(item, method); op != nil {
			return pagenext.FromOpenAPI(item.Parameters, op.Parameters)
		}
	}
	return nil
}

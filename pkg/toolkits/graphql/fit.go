package graphql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/inlinefit"
	"github.com/txn2/mcp-data-platform/internal/listcut"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// argQuery is the graphql_export argument carrying the query document.
const argQuery = "query"

// DataItems is how much of a cut list a graphql_query result shows.
type DataItems struct {
	// Path is the fields from data to the list, dotted (users.edges).
	Path string `json:"path"`
	// Shown is the number of items the cut data holds: the first ones.
	Shown int `json:"shown"`
	// Total is the number of items the endpoint answered with.
	Total int `json:"total"`
}

// FitResult holds a graphql_query result to a model client's context
// budget (#1878). Dropping the indentation is tried first. When that is
// not enough, the one list in the data -- found through objects that each
// carry one object, as a connection's edges are -- is cut on its items, so
// what is shown stays valid JSON and says how much of the list it holds
// (#1915). Data with no list the cut can name, or whose first item alone is
// past the budget, is withheld whole, since a JSON document cut in half
// cannot be parsed. Either way the graphql_export call that writes the
// whole answer to an asset is handed back. Called by the platform's
// result-budget middleware, and only for a result past the budget. A
// result still past it -- a large errors array -- is declined, and reaches
// the model whole.
func (*Toolkit) FitResult(tool string, args json.RawMessage, res *mcp.CallToolResult, budget int) bool {
	if tool != ToolQuery {
		return false
	}
	var out QueryOutput
	if !toolkit.DecodeStructured(res.StructuredContent, &out) {
		return false
	}
	var in QueryInput
	if len(args) > 0 && json.Unmarshal(args, &in) != nil {
		return false
	}
	text, ok := inlinefit.RenderWithin(out, budget)
	if !ok {
		text, ok = cutData(&out, in, budget)
	}
	if !ok {
		text, ok = withholdData(&out, in, budget)
	}
	if !ok {
		return false
	}
	return toolkit.SetFittedResult(res, text, out) == nil
}

// cutData cuts the list in out's data to the most items that render within
// budget, and returns that rendering. It reports false, leaving out as it
// found it, when the data carries no list or not even one item fits.
func cutData(out *QueryOutput, in QueryInput, budget int) ([]byte, bool) {
	whole := *out
	text, ok := cutList(out, in, budget)
	if !ok {
		*out = whole
	}
	return text, ok
}

func cutList(out *QueryOutput, in QueryInput, budget int) ([]byte, bool) {
	// Numbers are kept as the endpoint wrote them: an id past 2^53 read
	// into a float64 would come back as a different id.
	var data any
	dec := json.NewDecoder(bytes.NewReader(out.Data))
	dec.UseNumber()
	if len(out.Data) == 0 || dec.Decode(&data) != nil {
		return nil, false
	}
	items, found := listcut.FindItems(data)
	if !found {
		return nil, false
	}
	export := exportArguments(in)
	note := out.Note
	apply := func(n int) bool {
		raw, err := json.Marshal(listcut.Keep(data, items.Path, n))
		if err != nil {
			return false
		}
		out.Data = raw
		out.DataTruncated = true
		out.DataItems = &DataItems{Path: strings.Join(items.Path, "."), Shown: n, Total: items.Total}
		out.ExportArguments = export
		out.Note = strings.TrimSpace(note + cutNote(*out.DataItems, budget, export[argQuery] == nil))
		_, fits := inlinefit.RenderWithin(out, budget)
		return fits
	}
	n, ok := listcut.FitItems(items.Total, apply)
	if !ok && export[argQuery] != nil {
		// The echoed document is itself past the budget. The caller wrote
		// it, so the steer names it rather than carrying it.
		delete(export, argQuery)
		n, ok = listcut.FitItems(items.Total, apply)
	}
	if !ok {
		return nil, false
	}
	apply(n)
	return inlinefit.RenderWithin(out, budget)
}

// withholdData drops a result's data and steers to graphql_export.
func withholdData(out *QueryOutput, in QueryInput, budget int) ([]byte, bool) {
	out.Data = nil
	out.DataTruncated = true
	out.ExportArguments = exportArguments(in)
	out.Note = strings.TrimSpace(out.Note + fmt.Sprintf(
		" The result held %d bytes of data, past this client's context budget on a tool result (%d, tools.result_budget); "+
			"call graphql_export with export_arguments to write it to an asset and read it from there.",
		out.DataBytes, budget))
	text, ok := inlinefit.RenderWithin(out, budget)
	if !ok && out.ExportArguments[argQuery] != nil {
		// The echoed document is itself past the budget. The caller wrote
		// it, so the steer names it rather than carrying it.
		delete(out.ExportArguments, "query")
		out.Note += " export_arguments omit the query document, which alone is past the budget: pass the same query you sent."
		text, ok = inlinefit.RenderWithin(out, budget)
	}
	return text, ok
}

// exportArguments is the graphql_export call that writes the same answer
// to an asset.
func exportArguments(in QueryInput) map[string]any {
	args := map[string]any{
		"connection": in.Connection,
		argQuery:     in.Query,
	}
	if len(in.Variables) > 0 {
		args["variables"] = in.Variables
	}
	if in.OperationName != "" {
		args["operation_name"] = in.OperationName
	}
	return args
}

func cutNote(items DataItems, budget int, queryOmitted bool) string {
	note := fmt.Sprintf(" The list at data.%s holds %d items and the first %d are shown: it is cut at an item boundary "+
		"to fit this client's context budget on a tool result (%d, tools.result_budget). "+
		"Ask for the items after these with the list's own paging arguments in the query, "+
		"or call graphql_export with export_arguments to write the whole answer to an asset.",
		items.Path, items.Total, items.Shown, budget)
	if queryOmitted {
		note += " export_arguments omit the query document, which alone is past the budget: pass the same query you sent."
	}
	return note
}

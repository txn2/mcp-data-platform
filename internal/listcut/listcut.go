// Package listcut cuts a list response to fit a model client's context
// budget on whole items (#1915): the result stays valid JSON, says how many
// of the list's items it shows, and, where the operation declares paging
// parameters, names the request that reads on from the last one shown.
//
// It holds what the api gateway and the GraphQL kind share: finding the one
// list a JSON value carries, cutting it, the search for the most items that
// render within a budget, and the continuation pagenext computes for them.
// Rendering is the caller's, because only the caller knows the envelope the
// list is wrapped in.
package listcut

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/pagenext"
)

// Count is how much of a list a cut result shows.
type Count struct {
	// Shown is the number of items kept: the first ones.
	Shown int `json:"shown"`
	// Total is the number of items in the list.
	Total int `json:"total"`
}

// Cut is one list body cut to fit.
type Cut struct {
	// Body is the value holding the kept items and everything around them.
	Body any
	// Shown is the number of items kept: the first ones.
	Shown int
	// Total is the number of items in the list.
	Total int
	// NextQuery is the query of the request that reads on, or nil when the
	// operation declares no paging that can be advanced.
	NextQuery map[string]any
	// Style is how NextQuery continues; empty when NextQuery is nil.
	Style pagenext.Style
}

// Fit cuts the list in body to the most items for which fits reports the
// rendered result within the budget. fits receives each candidate cut and
// renders it; the last call is made with the cut returned. It reports false
// when body carries no list or not even one item fits. query is the request
// the body answered, and plan, when not nil, how that request pages.
func Fit(body any, query map[string]any, plan *pagenext.Plan, fits func(Cut) bool) (Cut, bool) {
	items, found := FindItems(body)
	if !found {
		return Cut{}, false
	}
	if plan != nil {
		if _, _, err := plan.Cut(query, items.Total, 1); err != nil {
			// A paging value sent as something other than a number cannot be
			// advanced; the cut still stands without a next request.
			plan = nil
		}
	}
	at := func(n int) Cut {
		c := Cut{Total: items.Total}
		if plan != nil {
			if keep, next, err := plan.Cut(query, items.Total, n); err == nil {
				n, c.NextQuery, c.Style = keep, next, plan.Style
			}
		}
		c.Shown, c.Body = n, Keep(body, items.Path, n)
		return c
	}
	n, ok := FitItems(items.Total, func(n int) bool { return fits(at(n)) })
	if !ok {
		return Cut{}, false
	}
	c := at(n)
	return c, fits(c)
}

// Hint is the steer on a cut list. The read-only way on is named first, as
// a call to readTool with next_arguments; exportTool, when not empty, is
// offered after it as the way to get everything in one call.
func Hint(c Cut, budget int, readTool, exportTool string) string {
	hint := fmt.Sprintf("the response holds %d items and the first %d are shown: the list is cut at an item boundary "+
		"to fit this client's context budget on a tool result (%d, tools.result_budget).", c.Total, c.Shown, budget)
	switch {
	case c.NextQuery != nil && c.Style == pagenext.StyleCursor:
		hint += fmt.Sprintf(" Call %s with next_arguments: it asks for this page again %d items at a time, "+
			"so its items are the ones shown here, and the pagination it carries continues after them.", readTool, c.Shown)
	case c.NextQuery != nil:
		hint += fmt.Sprintf(" Call %s with next_arguments to read the items after these; "+
			"repeat until a response is not cut and has no next page.", readTool)
	default:
		hint += " This operation declares no paging parameters that can be advanced, so the items after these cannot be read inline."
	}
	if exportTool != "" {
		hint += fmt.Sprintf(" To get the whole response in one call, use %s with export_arguments plus a name "+
			"(it streams into a portal asset, no model-context cost).", exportTool)
	}
	return hint
}

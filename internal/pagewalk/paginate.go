package pagewalk

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PaginateInput is the optional `paginate` block api_invoke_endpoint and
// api_export share. With it set, the gateway walks the pages of a
// collection inside the one tool call, merging the array Items names
// from every page. How the next page is reached is decided per page from
// the signal the response carries: a next URL is followed, pinned to the
// host the walk started on; a cursor is sent back as the query parameter
// CursorParam names; with neither, and PageParam named, that query
// parameter is advanced by PageStep until a page has no items.
type PaginateInput struct {
	// Items is the key of the array merged across pages ("data", "items",
	// "results", "value"), a dotted path to a nested one ("result.items"),
	// or "$" when the page body is the array. Required: guessing the key
	// is how a merged result silently becomes a list of envelopes.
	Items string `json:"items"`
	// CursorParam is the query parameter a body cursor (next_cursor,
	// nextPageToken, ...) is sent back as. A cursor signal on a page that
	// names no CursorParam and no PageParam fails the walk.
	CursorParam string `json:"cursor_param,omitempty"`
	// PageParam is the query parameter advanced when a page carries no
	// next signal (?page=N, ?offset=N). Its starting value must be present
	// in query_params; the first page is requested exactly as given.
	PageParam string `json:"page_param,omitempty"`
	// PageStep is what PageParam is advanced by per page. 1 (the default)
	// suits ?page=N; the page size suits ?offset=N.
	PageStep int `json:"page_step,omitempty"`
	// MaxPages bounds the walk. 0 means defaultMaxPages; the ceiling is
	// maxMaxPages. Reaching it is reported as stopped_by "max_pages", with
	// the signal for the next page in `pagination`.
	MaxPages int `json:"max_pages,omitempty"`
}

// WalkStats is what a walk reports on the output of both tools, and what
// the audit row for the one call carries (the observability the per-call
// loop was keeping). The gateway embeds it as a pointer so a single-page
// call's output carries none of these fields rather than zeros.
type WalkStats struct {
	PagesFetched int    `json:"pages_fetched"`
	ItemsMerged  int    `json:"items_merged"`
	StoppedBy    string `json:"stopped_by"`
}

// StoppedBy values. "end" is a page with no next signal or no items,
// "max_pages" the caller's page bound, and "max_bytes" the inline byte
// cap of api_invoke_endpoint (api_export fails past its cap instead, the
// all-or-nothing contract a partial asset would break).
const (
	StoppedByEnd      = "end"
	StoppedByMaxPages = "max_pages"
	StoppedByMaxBytes = "max_bytes"
)

// Bounds on PaginateInput.MaxPages.
const (
	defaultMaxPages = 100
	maxMaxPages     = 10000
)

// itemsRootPath is the Items value meaning "the page body is the array".
const itemsRootPath = "$"

// sourcePageParam is the PaginationInfo.Source a page-parameter step
// reports when a walk stops early and hands back where it would have
// continued.
const sourcePageParam = "page_param"

// normalize validates the block against the query the first page is
// requested with and fills the defaults. The returned copy is what the
// walk runs on; the caller's input is left as it was sent, for audit.
func (p PaginateInput) normalize(query map[string]any) (PaginateInput, error) {
	if strings.TrimSpace(p.Items) == "" {
		return p, errors.New("apigateway: paginate.items is required: the key of the array merged across pages, or \"$\" when the page body is the array")
	}
	if p.MaxPages < 0 || p.PageStep < 0 {
		return p, errors.New("apigateway: paginate.max_pages and paginate.page_step must be positive")
	}
	if p.MaxPages == 0 {
		p.MaxPages = defaultMaxPages
	}
	if p.MaxPages > maxMaxPages {
		p.MaxPages = maxMaxPages
	}
	if p.PageStep == 0 {
		p.PageStep = 1
	}
	if p.PageParam != "" {
		if _, err := queryInt(query, p.PageParam); err != nil {
			return p, fmt.Errorf("apigateway: paginate.page_param names %q: %w", p.PageParam, err)
		}
	}
	return p, nil
}

// itemsPath splits Items into the keys walked from the page body to the
// merged array. "$" yields no keys: the body itself is the array.
func (p PaginateInput) itemsPath() []string {
	if p.Items == itemsRootPath {
		return nil
	}
	return strings.Split(p.Items, ".")
}

// queryInt reads the integer a page parameter currently holds. The value
// must be present: with no starting value there is nothing to advance
// from, and a guessed 0 or 1 is wrong for half the APIs either way.
func queryInt(query map[string]any, name string) (int, error) {
	raw, ok := query[name]
	if !ok {
		return 0, errors.New("query_params does not carry it; set the first page's value there")
	}
	n, err := strconv.Atoi(ScalarString(firstScalar(raw)))
	if err != nil {
		return 0, fmt.Errorf("query_params value %v is not an integer", raw)
	}
	return n, nil
}

// firstScalar unwraps a repeated query value to its first element so a
// parameter sent once per value still reads as one scalar.
func firstScalar(v any) any {
	if list, ok := v.([]any); ok && len(list) > 0 {
		return list[0]
	}
	return v
}

// ScalarString renders one JSON scalar as the text a wire format carries
// it in. The gateway uses it for query-string assembly and multipart
// field encoding, the walk for reading a page parameter back, so a
// number reaching the upstream reads the same whichever side of the
// request it travels on — notably float64, which the JSON decoder
// produces for every number and which %v would otherwise render in
// exponent form.
func ScalarString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, base10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, float64Bits)
	default:
		return fmt.Sprintf("%v", v)
	}
}

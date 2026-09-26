package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
)

// errNoPageData reports a page whose data object is absent or null: there
// is nothing to page through, whichever of the two it was.
var errNoPageData = errors.New("graphql: the page carried no data to paginate")

// pathSep separates the segments of a dotted path into the response body.
const pathSep = "."

// pageInfoSuffixes are the Relay connection specification's own
// pagination fields, used when the caller names no cursor path of their
// own. Relay is the convention most paged GraphQL schemas follow, so
// naming only the array is enough for them; an upstream that pages some
// other way names its cursor explicitly.
const (
	relayEndCursor  = "pageInfo.endCursor"
	relayHasNext    = "pageInfo.hasNextPage"
	maxPagesCeiling = 1000
)

// PaginateInput is the walk a caller asks for. It names the array to
// merge and the variable the next page's cursor is fed back into;
// everything else has a Relay default.
type PaginateInput struct {
	// Items is the dotted path, inside the response's data object, of
	// the array merged across pages
	// ("masterData.product.query.edges", "searchAcrossEntities.results").
	// Required.
	Items string `json:"items"`
	// CursorVariable is the document variable the next page's cursor is
	// bound to ("after", "scrollId"). Required: the cursor goes back as
	// a variable, so the document itself is unchanged between pages.
	CursorVariable string `json:"cursor_variable"`
	// NextCursorPath is the dotted path, inside data, holding the next
	// page's cursor. Defaults to the Relay pageInfo.endCursor beside
	// the items array.
	NextCursorPath string `json:"next_cursor_path,omitempty"`
	// HasNextPath is the dotted path, inside data, of a boolean saying
	// whether another page exists. Defaults to the Relay
	// pageInfo.hasNextPage beside the items array. An upstream that
	// signals the end by a null cursor alone needs no such field: the
	// walk stops on an absent cursor either way.
	HasNextPath string `json:"has_next_path,omitempty"`
	// MaxPages bounds the walk. Defaults to DefaultMaxPages.
	MaxPages int `json:"max_pages,omitempty"`
}

// Why a walk stopped, reported as PaginationReport.StoppedBy.
const (
	// StoppedByEnd is the upstream saying there is no next page.
	StoppedByEnd = "end"
	// StoppedByMaxPages is the walk's own page bound.
	StoppedByMaxPages = "max_pages"
	// StoppedByError is a page the upstream refused; the errors are on
	// the result.
	StoppedByError = "error"
)

// PaginationReport says what a walk did.
type PaginationReport struct {
	// PagesFetched is how many pages were read.
	PagesFetched int `json:"pages_fetched"`
	// ItemsMerged is the length of the merged array.
	ItemsMerged int `json:"items_merged"`
	// StoppedBy is why the walk ended.
	StoppedBy string `json:"stopped_by"`
	// NextCursor is the cursor the next page would have used, present
	// when the walk stopped at its page bound rather than at the end.
	NextCursor string `json:"next_cursor,omitempty"`
}

// walkPages follows a paged connection, merging the array Items names
// from every page. The merged array replaces that array in the first
// page's data, so the caller reads the same shape they would from one
// page — with every row in it.
func (t *Toolkit) walkPages(ctx context.Context, p prepared, in PaginateInput, out *QueryOutput) error {
	if err := in.validate(); err != nil {
		return err
	}
	spec := in.resolved()
	w := &walk{
		tk: t, prepared: p, spec: spec, cursorVar: in.CursorVariable,
		vars: copyVars(p.variables), out: out,
	}
	out.Pagination = &w.report
	w.report.StoppedBy = StoppedByEnd

	for w.report.PagesFetched < spec.maxPages {
		more, err := w.page(ctx)
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	// A page the upstream refused leaves its own errors on the result and
	// nothing to merge: the answer is that page, not a merged array.
	if w.halted {
		return nil
	}
	if !w.exhausted && w.report.NextCursor != "" {
		w.report.StoppedBy = StoppedByMaxPages
	} else {
		w.report.NextCursor = ""
	}
	w.report.ItemsMerged = len(w.merged)
	return replaceArray(out, w.first, spec.items, w.merged)
}

// walk carries one page walk: where it is, what it has merged, and the
// report it is filling in.
type walk struct {
	tk       *Toolkit
	prepared prepared
	spec     walkSpec
	// cursorVar is the document variable each page's cursor is bound to,
	// which is what leaves the document itself unchanged between pages.
	cursorVar string
	vars      map[string]any
	out       *QueryOutput
	report    PaginationReport
	merged    []json.RawMessage
	// first is the first page's data object; the merged array is written
	// back into it, so the caller reads one page's shape holding every row.
	first map[string]json.RawMessage
	// exhausted records the upstream saying there is no next page, which
	// is what separates a walk that finished from one the page bound cut
	// short. Without it a walk whose last page happened to be the bound
	// would report max_pages and hand back a cursor it had already used.
	exhausted bool
	// halted records a page the upstream refused, which ends the walk with
	// that page's answer rather than with a merged array.
	halted bool
}

// page fetches one page, merges its rows, and reports whether another
// page should be fetched. A page the upstream refused ends the walk with
// its errors on the result rather than with an error of the platform's.
func (w *walk) page(ctx context.Context) (more bool, err error) {
	res, err := w.tk.execute(ctx, w.prepared.conn, request(w.prepared.doc, w.vars))
	if err != nil {
		return false, err
	}
	if err := withinReadCap(w.prepared.conn, res); err != nil {
		return false, err
	}
	applyExecution(w.out, res)
	w.report.PagesFetched++
	if w.out.UpstreamError || res.parsed == nil {
		w.report.StoppedBy = StoppedByError
		w.halted = true
		return false, nil
	}
	data, err := decodeData(res.parsed.Data)
	if err != nil {
		return false, err
	}
	if w.first == nil {
		w.first = data
	}
	items, err := arrayAt(data, w.spec.items)
	if err != nil {
		return false, err
	}
	w.merged = append(w.merged, items...)
	cursor, hasNext := nextCursor(data, w.spec)
	if !hasNext {
		w.exhausted = true
		return false, nil
	}
	w.report.NextCursor = cursor
	w.vars[w.cursorVar] = cursor
	return true, nil
}

// validate refuses a walk that cannot be performed rather than silently
// fetching one page.
func (in PaginateInput) validate() error {
	if strings.TrimSpace(in.Items) == "" {
		return errors.New("graphql: paginate.items is required: name the array to merge, as a dotted path inside data")
	}
	if strings.TrimSpace(in.CursorVariable) == "" {
		return errors.New("graphql: paginate.cursor_variable is required: name the document variable the next page's cursor is bound to")
	}
	if in.MaxPages > maxPagesCeiling {
		return fmt.Errorf("graphql: paginate.max_pages may not exceed %d", maxPagesCeiling)
	}
	return nil
}

// walkSpec is the resolved walk: the caller's paths with the Relay
// defaults filled in.
type walkSpec struct {
	items      []string
	nextCursor []string
	hasNext    []string
	maxPages   int
}

// resolved fills in what the caller left out. A Relay connection puts
// its pageInfo beside its edges, so naming the array is enough to find
// the cursor; anything else names its own path.
func (in PaginateInput) resolved() walkSpec {
	spec := walkSpec{items: splitPath(in.Items), maxPages: in.MaxPages}
	if spec.maxPages <= 0 {
		spec.maxPages = DefaultMaxPages
	}
	parent := spec.items[:len(spec.items)-1]
	spec.nextCursor = splitPath(in.NextCursorPath)
	if len(spec.nextCursor) == 0 {
		spec.nextCursor = append(append([]string{}, parent...), splitPath(relayEndCursor)...)
	}
	spec.hasNext = splitPath(in.HasNextPath)
	if len(spec.hasNext) == 0 && in.NextCursorPath == "" {
		spec.hasNext = append(append([]string{}, parent...), splitPath(relayHasNext)...)
	}
	return spec
}

// splitPath splits a dotted path, dropping empty segments so a stray
// dot does not become a lookup for the empty field name.
func splitPath(p string) []string {
	var out []string
	for seg := range strings.SplitSeq(p, pathSep) {
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// decodeData decodes a response's data object into its top-level
// fields, kept raw so nothing below the paths the walk reads is
// re-encoded.
func decodeData(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errNoPageData
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("graphql: the page's data is not an object: %w", err)
	}
	// A null data object decodes to a nil map without error, and is the
	// same absence as no data at all: the upstream answered the request
	// with nothing to page through.
	if data == nil {
		return nil, errNoPageData
	}
	return data, nil
}

// objectAt walks a dotted path through an object, returning the raw
// value at the end of it.
func objectAt(data map[string]json.RawMessage, path []string) (json.RawMessage, error) {
	current := data
	for i, seg := range path {
		raw, ok := current[seg]
		if !ok {
			return nil, fmt.Errorf("graphql: %q is not in the response data", strings.Join(path[:i+1], pathSep))
		}
		if i == len(path)-1 {
			return raw, nil
		}
		next := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil, fmt.Errorf("graphql: %q is not an object, so %q cannot be read from it",
				strings.Join(path[:i+1], pathSep), strings.Join(path, pathSep))
		}
		current = next
	}
	return nil, errors.New("graphql: empty path")
}

// arrayAt reads the array a walk merges.
func arrayAt(data map[string]json.RawMessage, path []string) ([]json.RawMessage, error) {
	raw, err := objectAt(data, path)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("graphql: %q is not an array; paginate.items must name the array to merge",
			strings.Join(path, pathSep))
	}
	return items, nil
}

// nextCursor reads the signal for another page. An explicit
// has_next_path that says no ends the walk; otherwise a cursor that is
// absent, null or empty ends it, which is how an upstream with no
// hasNextPage field signals the end.
func nextCursor(data map[string]json.RawMessage, spec walkSpec) (cursor string, more bool) {
	if len(spec.hasNext) > 0 {
		if raw, err := objectAt(data, spec.hasNext); err == nil {
			var has bool
			if json.Unmarshal(raw, &has) == nil && !has {
				return "", false
			}
		}
	}
	raw, err := objectAt(data, spec.nextCursor)
	if err != nil {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" {
		return "", false
	}
	return value, true
}

// replaceArray writes the merged array back where the first page's
// array was, so the caller reads one page's shape holding every row.
func replaceArray(out *QueryOutput, data map[string]json.RawMessage, path []string, merged []json.RawMessage) error {
	if data == nil {
		return nil
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("graphql: encoding the merged pages: %w", err)
	}
	if err := setAt(data, path, encoded); err != nil {
		return err
	}
	rebuilt, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("graphql: encoding the merged result: %w", err)
	}
	out.Data = rebuilt
	out.DataBytes = len(rebuilt)
	return nil
}

// setAt writes a value at a dotted path, rebuilding the objects along
// the way.
func setAt(data map[string]json.RawMessage, path []string, value json.RawMessage) error {
	if len(path) == 1 {
		data[path[0]] = value
		return nil
	}
	raw, ok := data[path[0]]
	if !ok {
		return fmt.Errorf("graphql: %q is not in the response data", path[0])
	}
	next := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &next); err != nil {
		return fmt.Errorf("graphql: %q is not an object", path[0])
	}
	if err := setAt(next, path[1:], value); err != nil {
		return err
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("graphql: encoding %q: %w", path[0], err)
	}
	data[path[0]] = encoded
	return nil
}

// copyVars clones the caller's variables so a walk's cursor writes do
// not mutate what they passed.
func copyVars(vars map[string]any) map[string]any {
	out := make(map[string]any, len(vars)+1)
	maps.Copy(out, vars)
	return out
}

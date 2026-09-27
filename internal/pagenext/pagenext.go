// Package pagenext names the request that reads on from a list response
// cut to fit a model client's context budget (#1915).
//
// A cut keeps the first items of a page. Where the rest are is decided by
// the paging parameters the operation declares, and each style continues
// differently:
//
//   - offset (offset/skip/$skip/start): the next request is the same one with the
//     offset advanced past the items kept.
//   - page (page with per_page/page_size/limit/...): page+1 would skip the
//     items cut from this one, so the next request asks for a page size that
//     divides the items read through this cut, and the page that starts
//     right after them. The cut and that size are chosen together, near what
//     fits: requiring the size to equal the count kept degenerates to one
//     item a call when the items already read are a prime count.
//   - cursor (cursor/after/page_token/...): the response's cursor points
//     past the whole page, so following it would skip the items cut. The
//     next request is this one again, same cursor, with the page size set
//     to the items kept: that response holds what was shown here and
//     carries a cursor that continues from it.
//
// A page or cursor operation that declares no size parameter cannot be
// continued this way, and has no plan.
package pagenext

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
)

// Style is how an operation pages.
type Style string

// Styles.
const (
	StyleOffset Style = "offset"
	StylePage   Style = "page"
	StyleCursor Style = "cursor"
)

// Param is a query parameter an operation declares.
type Param struct {
	Name string
	// Default is the declared default, or "" when none is declared.
	Default string
	// Minimum is the declared minimum, or nil when none is declared. A page
	// parameter with a minimum of 0 is numbered from 0.
	Minimum *float64
}

// Recognized parameter names, compared case-insensitively.
var (
	offsetNames = []string{"offset", "skip", "$skip", "start"}
	pageNames   = []string{"page", "page_number", "pagenumber"}
	cursorNames = []string{"cursor", "after", "page_token", "pagetoken", "starting_after", "continuation", "next_token", "nexttoken"}
	sizeNames   = []string{"limit", "per_page", "perpage", "page_size", "pagesize", "size", "$top", "max_results", "maxresults", "count", "first"}
)

// Plan is how one operation continues after a cut.
type Plan struct {
	Style  Style
	Cursor Param // StyleCursor: the parameter the cursor is sent as
	Offset Param // StyleOffset: the parameter advanced
	Page   Param // StylePage: the page number
	Size   Param // the page size; Name is "" when an offset operation declares none
}

// Detect reads the paging plan from an operation's declared query
// parameters. Offset is preferred where an operation declares more than one
// style, because it continues without changing the page size.
func Detect(params []Param) (Plan, bool) {
	size, hasSize := find(params, sizeNames)
	if off, ok := find(params, offsetNames); ok {
		return Plan{Style: StyleOffset, Offset: off, Size: size}, true
	}
	if !hasSize {
		return Plan{}, false
	}
	if page, ok := find(params, pageNames); ok {
		return Plan{Style: StylePage, Page: page, Size: size}, true
	}
	if cur, ok := find(params, cursorNames); ok {
		return Plan{Style: StyleCursor, Cursor: cur, Size: size}, true
	}
	return Plan{}, false
}

func find(params []Param, names []string) (Param, bool) {
	for _, want := range names {
		for _, p := range params {
			if strings.EqualFold(p.Name, want) {
				return p, true
			}
		}
	}
	return Param{}, false
}

// Cut is where a cut of a page of pageLen items, the first fit of which
// render within the budget, ends -- how many items it keeps, at most fit --
// and the query of the request that reads on from there. The caller's other
// parameters are carried unchanged.
func (p Plan) Cut(query map[string]any, pageLen, fit int) (keep int, next map[string]any, err error) {
	next = maps.Clone(query)
	if next == nil {
		next = map[string]any{}
	}
	switch p.Style {
	case StyleOffset:
		off, err := intValue(query, p.Offset, 0)
		if err != nil {
			return 0, nil, err
		}
		next[p.Offset.Name] = off + fit
		return fit, next, nil
	case StylePage:
		before, err := p.readBefore(query, pageLen)
		if err != nil {
			return 0, nil, err
		}
		keep, size := pageCut(before, fit)
		next[p.Size.Name] = size
		next[p.Page.Name] = p.firstPage() + (before+keep)/size
		return keep, next, nil
	default: // StyleCursor
		next[p.Size.Name] = fit
		return fit, next, nil
	}
}

// pageCut chooses how many items a page cut keeps and the page size the
// next request asks for, so the next page starts exactly after them: the
// size divides the items read through the cut. Both are at most fit, and
// the pair whose smaller member is largest is taken, so neither the cut nor
// the next page is needlessly small.
func pageCut(before, fit int) (keep, size int) {
	keep, size = 1, 1
	for q := fit; q >= 1 && q > min(keep, size); q-- {
		k := fit - (before+fit)%q
		if min(k, q) > min(keep, size) {
			keep, size = k, q
		}
	}
	return keep, size
}

// readBefore is the number of items the pages before this one held.
func (p Plan) readBefore(query map[string]any, pageLen int) (int, error) {
	page, err := intValue(query, p.Page, p.firstPage())
	if err != nil {
		return 0, err
	}
	size, err := intValue(query, p.Size, pageLen)
	if err != nil {
		return 0, err
	}
	return max(page-p.firstPage(), 0) * size, nil
}

func (p Plan) firstPage() int {
	if p.Page.Minimum != nil && *p.Page.Minimum == 0 {
		return 0
	}
	return 1
}

// intValue reads an integer query parameter as the caller sent it, then as
// the operation declares its default, then as def.
func intValue(query map[string]any, param Param, def int) (int, error) {
	raw, ok := query[param.Name]
	if !ok {
		if param.Default == "" {
			return def, nil
		}
		raw = param.Default
	}
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case []any:
		if len(v) == 1 {
			return intValue(map[string]any{param.Name: v[0]}, param, def)
		}
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(v))); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("query parameter %s is not an integer: %v", param.Name, raw)
}

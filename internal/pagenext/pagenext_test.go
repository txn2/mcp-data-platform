package pagenext

import (
	"maps"
	"reflect"
	"testing"
)

func params(names ...string) []Param {
	out := make([]Param, 0, len(names))
	for _, n := range names {
		out = append(out, Param{Name: n})
	}
	return out
}

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params []Param
		style  Style
		ok     bool
	}{
		{"limit/offset", params("limit", "offset", "q"), StyleOffset, true},
		{"offset without size", params("skip"), StyleOffset, true},
		{"odata", params("$top", "$skip"), StyleOffset, true},
		{"page/per_page", params("page", "per_page"), StylePage, true},
		{"page without size", params("page"), "", false},
		{"cursor with size", params("pageToken", "pageSize"), StyleCursor, true},
		{"cursor without size", params("cursor"), "", false},
		{"offset wins over page", params("page", "per_page", "offset"), StyleOffset, true},
		{"no paging", params("q", "sort"), "", false},
		{"case-insensitive", params("Page", "Per_Page"), StylePage, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, ok := Detect(tc.params)
			if ok != tc.ok || plan.Style != tc.style {
				t.Errorf("Detect = %+v, %v; want style %q, %v", plan, ok, tc.style, tc.ok)
			}
		})
	}
}

// Reading on from each next request in turn reads every item exactly once:
// the union of the pages is the list.
func walk(t *testing.T, plan Plan, first map[string]any, list []int, fit int) []int {
	t.Helper()
	var got []int
	query := first
	for range 400 {
		page := serve(t, plan, query, list)
		if len(page) == 0 {
			return got
		}
		if len(page) <= fit {
			got = append(got, page...)
			query = advance(t, plan, query, len(page))
			continue
		}
		kept, next, err := plan.Cut(query, len(page), fit)
		if err != nil {
			t.Fatal(err)
		}
		if kept < 1 || kept > fit {
			t.Fatalf("Cut kept %d of a fit of %d", kept, fit)
		}
		got = append(got, page[:kept]...)
		query = next
	}
	t.Fatal("walk did not end")
	return nil
}

// serve answers query the way an upstream paged by plan's style would.
func serve(t *testing.T, plan Plan, query map[string]any, list []int) []int {
	t.Helper()
	size, err := intValue(query, plan.Size, 10)
	if err != nil {
		t.Fatal(err)
	}
	var start int
	switch plan.Style {
	case StyleOffset:
		start, _ = intValue(query, plan.Offset, 0)
	case StylePage:
		p, _ := intValue(query, plan.Page, plan.firstPage())
		start = (p - plan.firstPage()) * size
	}
	if start >= len(list) {
		return nil
	}
	return list[start:min(start+size, len(list))]
}

// advance is the caller's own step past a page that was not cut, by the
// API's convention.
func advance(t *testing.T, plan Plan, query map[string]any, n int) map[string]any {
	t.Helper()
	next := maps.Clone(query)
	switch plan.Style {
	case StyleOffset:
		off, _ := intValue(query, plan.Offset, 0)
		next[plan.Offset.Name] = off + n
	case StylePage:
		p, _ := intValue(query, plan.Page, plan.firstPage())
		next[plan.Page.Name] = p + 1
	}
	return next
}

func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func TestCut_UnionIsTheList(t *testing.T) {
	list := seq(103)
	offset, _ := Detect(params("limit", "offset"))
	page, _ := Detect(params("page", "per_page"))
	zeroMin := 0.0
	page0 := Plan{Style: StylePage, Page: Param{Name: "page", Minimum: &zeroMin}, Size: Param{Name: "per_page"}}
	for _, tc := range []struct {
		name  string
		plan  Plan
		first map[string]any
		fit   int
	}{
		{"offset", offset, map[string]any{"limit": 25, "offset": 0}, 7},
		{"offset as sent strings", offset, map[string]any{"limit": "25", "offset": "0"}, 7},
		{"page", page, map[string]any{"page": 1, "per_page": 25}, 7},
		{"page from a later page", page, map[string]any{"page": float64(2), "per_page": float64(20)}, 7},
		{"page numbered from 0", page0, map[string]any{"page": 0, "per_page": 30}, 11},
		{"page after a prime count", page, map[string]any{"page": 1, "per_page": 50}, 47},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := walk(t, tc.plan, tc.first, list, tc.fit)
			want := list
			if tc.name == "page from a later page" {
				want = list[20:]
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("union of pages = %v\nwant %v", got, want)
			}
		})
	}
}

// A page cut keeps a count and asks for a size that both stay near what
// fits. #1915's acceptance cut a page after 827 read items, a prime, where a
// size required to equal the count kept could only be 1.
func TestCut_PageStaysNearTheFit(t *testing.T) {
	plan, _ := Detect(params("page", "per_page"))
	for _, tc := range []struct {
		page, size, fit int
	}{
		{1, 1000, 827},
		{2, 827, 800},
		{3, 7, 5},
		{2, 13, 5},
	} {
		before := (tc.page - 1) * tc.size
		keep, next, err := plan.Cut(map[string]any{"page": tc.page, "per_page": tc.size}, tc.size, tc.fit)
		if err != nil {
			t.Fatal(err)
		}
		size, _ := next["per_page"].(int)
		if keep > tc.fit || size < 1 || size > tc.fit || (before+keep)%size != 0 {
			t.Fatalf("Cut(page %d, fit %d) = keep %d size %d; want both within the fit and the size dividing %d", tc.page, tc.fit, keep, size, before+keep)
		}
		if next["page"] != 1+(before+keep)/size {
			t.Errorf("next page = %v; want the page starting after item %d", next["page"], before+keep)
		}
		if min(keep, size) < tc.fit/2 {
			t.Errorf("Cut(page %d, fit %d) = keep %d size %d; want both near the fit", tc.page, tc.fit, keep, size)
		}
	}
	offset, _ := Detect(params("offset"))
	if keep, next, _ := offset.Cut(nil, 100, 37); keep != 37 || next["offset"] != 37 {
		t.Errorf("offset Cut = %d, %v; want the fit kept and the offset past it", keep, next)
	}
}

func TestCut_Cursor(t *testing.T) {
	plan, _ := Detect(params("cursor", "limit"))
	keep, next, err := plan.Cut(map[string]any{"cursor": "abc", "limit": 100, "q": "x"}, 100, 12)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"cursor": "abc", "limit": 12, "q": "x"}; keep != 12 || !reflect.DeepEqual(next, want) {
		t.Errorf("Cut = %d, %v; want the same page asked for 12 at a time", keep, next)
	}
}

func TestCut_DefaultsAndErrors(t *testing.T) {
	plan := Plan{Style: StylePage, Page: Param{Name: "page", Default: "1"}, Size: Param{Name: "per_page", Default: "50"}}
	keep, next, err := plan.Cut(nil, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"page": 2, "per_page": 10}; keep != 10 || !reflect.DeepEqual(next, want) {
		t.Errorf("Cut from declared defaults = %d, %v; want %v", keep, next, want)
	}
	if _, got, err := plan.Cut(map[string]any{"page": []any{"3"}, "per_page": 10}, 10, 5); err != nil || got["page"] != 6 {
		t.Errorf("Cut from a one-element array = %v, %v; want page 6", got, err)
	}
	if _, _, err := plan.Cut(map[string]any{"page": "two"}, 10, 5); err == nil {
		t.Error("Cut with a non-integer page reported no error")
	}
	if _, _, err := plan.Cut(map[string]any{"per_page": "ten"}, 10, 5); err == nil {
		t.Error("Cut with a non-integer size reported no error")
	}
	offset, _ := Detect(params("offset"))
	if _, _, err := offset.Cut(map[string]any{"offset": []any{1, 2}}, 10, 5); err == nil {
		t.Error("Cut with a two-value offset reported no error")
	}
}

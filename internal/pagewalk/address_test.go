package pagewalk

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// refuseDots stands in for the gateway's validatePath.
func refuseDots(p string) error {
	if strings.Contains(p, "..") {
		return errors.New("path must not contain \"..\" segments")
	}
	return nil
}

func TestPathAddress_FollowURL(t *testing.T) {
	cases := []struct {
		name, base, next, wantPath, wantErr string
		wantQuery                           map[string]any
	}{
		{
			name: "same host", base: "https://api.example.com", next: "https://api.example.com/v1/items?cursor=abc&x=1&x=2",
			wantPath: "/v1/items", wantQuery: map[string]any{"cursor": "abc", "x": []any{"1", "2"}},
		},
		{
			name: "under base path", base: "https://api.example.com/api/v2/", next: "https://api.example.com/api/v2/items?page=2",
			wantPath: "/items", wantQuery: map[string]any{"page": "2"},
		},
		{name: "base path itself", base: "https://api.example.com/api", next: "https://api.example.com/api?page=2", wantPath: "/"},
		{name: "outside base path", base: "https://api.example.com/api/v2", next: "https://api.example.com/other/items", wantErr: "outside the connection's base path"},
		{name: "other host", base: "https://api.example.com", next: "https://evil.example.com/v1/items", wantErr: "pinned to api.example.com"},
		// An API behind a TLS-terminating proxy writes http:// links; the page
		// is requested through base_url regardless (#1543).
		{name: "other scheme same host", base: "https://api.example.com", next: "http://api.example.com/v1/items?page=2", wantPath: "/v1/items", wantQuery: map[string]any{"page": "2"}},
		{name: "relative", base: "https://api.example.com", next: "/v1/items?cursor=x", wantErr: "not an absolute URL"},
		{name: "userinfo", base: "https://api.example.com", next: "https://u:p@api.example.com/v1/items", wantErr: "userinfo"},
		{name: "dot segment", base: "https://api.example.com", next: "https://api.example.com/v1/../admin", wantErr: "must not contain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := newPathAddress(AddressSpec{BaseURL: tc.base, Path: "/v1/items", Body: "kept", ValidatePath: refuseDots})
			if err != nil {
				t.Fatal(err)
			}
			err = a.FollowURL(tc.next)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v; want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := a.Target()
			if got.Path != tc.wantPath {
				t.Errorf("path = %q; want %q", got.Path, tc.wantPath)
			}
			if tc.wantQuery != nil && fmt.Sprint(got.Query) != fmt.Sprint(tc.wantQuery) {
				t.Errorf("query = %v; want %v", got.Query, tc.wantQuery)
			}
			if got.Body != "kept" {
				t.Errorf("a path address must carry the body unchanged; got %v", got.Body)
			}
		})
	}
}

func TestPathAddress_ParamsRoundTrip(t *testing.T) {
	a, _ := newPathAddress(AddressSpec{BaseURL: "https://api.example.com", Query: map[string]any{"page": []any{float64(3)}}})
	if got := a.Param("page"); got != "3" {
		t.Errorf("Param(page) = %q; want 3 (first of a repeated value)", got)
	}
	if got := a.Param("missing"); got != "" {
		t.Errorf("Param(missing) = %q", got)
	}
	a.SetParam("cursor", "abc")
	if a.Target().Query["cursor"] != "abc" {
		t.Errorf("query = %v", a.Target().Query)
	}
	// A nil query still accepts a parameter, and a nil ValidatePath accepts
	// every path.
	b, _ := newPathAddress(AddressSpec{BaseURL: "https://api.example.com"})
	b.SetParam("cursor", "x")
	if b.Target().Query["cursor"] != "x" {
		t.Errorf("query = %v", b.Target().Query)
	}
	if err := b.FollowURL("https://api.example.com/v1/../x"); err != nil {
		t.Errorf("nil ValidatePath refused a path: %v", err)
	}
}

func TestFetchAddress(t *testing.T) {
	const start = "https://files.example.com/report?sig=1"
	body := map[string]any{"url": start, "method": "GET"}
	a, err := NewAddress(AddressSpec{Path: "/util/fetch", Body: body, WalkBodyURL: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.(*fetchAddress); !ok {
		t.Fatalf("picked %T", a)
	}
	a.SetParam("cursor", "c2")
	if got := a.Param("cursor"); got != "c2" {
		t.Errorf("Param = %q", got)
	}
	target := a.Target()
	got, _ := target.Body.(map[string]any)
	if u := got["url"]; u != "https://files.example.com/report?cursor=c2&sig=1" {
		t.Errorf("body url = %v", u)
	}
	if target.Path != "/util/fetch" {
		t.Errorf("a fetch address must carry the path unchanged; got %q", target.Path)
	}
	if body["url"] != start {
		t.Errorf("the caller's body was mutated: %v", body)
	}
	if err := a.FollowURL("https://other.example.com/next"); err == nil || !strings.Contains(err.Error(), "pinned to files.example.com") {
		t.Errorf("foreign host accepted: %v", err)
	}
	// A fetched page is requested at the link itself, so the scheme is
	// part of the pin here, unlike a proxied connection's.
	if err := a.FollowURL("http://files.example.com/next"); err == nil || !strings.Contains(err.Error(), "pinned to https://files.example.com") {
		t.Errorf("scheme downgrade accepted on a fetch address: %v", err)
	}
	if err := a.FollowURL("https://files.example.com/next?page=2"); err != nil {
		t.Errorf("same-host link refused: %v", err)
	}
	if _, err := NewAddress(AddressSpec{Body: map[string]any{"url": "not a url"}, WalkBodyURL: true}); err == nil {
		t.Error("relative body url accepted")
	}
}

// TestFetchAddress_BodyAsJSONString: the gateway passes a body sent as a
// string of JSON through as JSON, so a fetch_url body in that form is the
// same document and is walked the same way.
func TestFetchAddress_BodyAsJSONString(t *testing.T) {
	a, err := NewAddress(AddressSpec{Path: "/util/fetch", Body: `{"url": "https://files.example.com/report?page=1"}`, WalkBodyURL: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.(*fetchAddress); !ok {
		t.Fatalf("a JSON-string body picked %T", a)
	}
	if err := a.FollowURL("https://files.example.com/report?page=2"); err != nil {
		t.Fatalf("same-host link refused: %v", err)
	}
	got, _ := a.Target().Body.(map[string]any)
	if got["url"] != "https://files.example.com/report?page=2" {
		t.Errorf("body url = %v", got["url"])
	}
	for _, body := range []any{"not json", `["url"]`, `null`, `{"url": 1}`, 7} {
		a, err := NewAddress(AddressSpec{BaseURL: "https://api.example.com", Body: body, WalkBodyURL: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := a.(*pathAddress); !ok {
			t.Errorf("body %v picked %T", body, a)
		}
	}
}

func TestNewAddress_PicksByBody(t *testing.T) {
	a, err := NewAddress(AddressSpec{BaseURL: "https://api.example.com", Body: map[string]any{"url": "https://h/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.(*pathAddress); !ok {
		t.Errorf("a proxied connection picked %T", a)
	}
	a, err = NewAddress(AddressSpec{BaseURL: "https://api.example.com", Body: map[string]any{"k": 1}, WalkBodyURL: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.(*pathAddress); !ok {
		t.Errorf("a body without a url picked %T", a)
	}
	if _, err := NewAddress(AddressSpec{BaseURL: "nope"}); err == nil {
		t.Error("bad base_url accepted")
	}
}

func TestNextPage_Precedence(t *testing.T) {
	newAddr := func() *pathAddress {
		a, _ := newPathAddress(AddressSpec{BaseURL: "https://api.example.com", Query: map[string]any{"page": "1"}})
		return a
	}
	t.Run("url wins over cursor and page", func(t *testing.T) {
		a := newAddr()
		sig := &PaginationInfo{NextURL: "https://api.example.com/v1?cursor=z", NextCursor: "ignored"}
		got, err := nextPage(a, PaginateInput{CursorParam: "cursor", PageParam: "page", PageStep: 1}, sig, 1)
		if err != nil || got != sig || a.path != "/v1" {
			t.Errorf("got %+v err %v path %q", got, err, a.path)
		}
	})
	t.Run("cursor wins over page", func(t *testing.T) {
		a := newAddr()
		sig := &PaginationInfo{NextCursor: "abc"}
		if _, err := nextPage(a, PaginateInput{CursorParam: "cursor", PageParam: "page", PageStep: 1}, sig, 1); err != nil {
			t.Fatal(err)
		}
		if a.query["cursor"] != "abc" || a.query["page"] != "1" {
			t.Errorf("query = %v", a.query)
		}
	})
	t.Run("page numbering ignores a cursor it cannot send", func(t *testing.T) {
		a := newAddr()
		got, err := nextPage(a, PaginateInput{PageParam: "page", PageStep: 1}, &PaginationInfo{NextCursor: "true"}, 1)
		if err != nil || got.NextCursor != "2" || a.query["page"] != "2" {
			t.Errorf("got %+v err %v query %v", got, err, a.query)
		}
	})
	t.Run("cursor with nowhere to go", func(t *testing.T) {
		_, err := nextPage(newAddr(), PaginateInput{}, &PaginationInfo{NextCursor: "abc", Source: "body:next_cursor"}, 4)
		if err == nil || !strings.Contains(err.Error(), "page 4 carries a cursor (body:next_cursor)") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no signal ends", func(t *testing.T) {
		got, err := nextPage(newAddr(), PaginateInput{}, nil, 1)
		if got != nil || err != nil {
			t.Errorf("got %+v err %v", got, err)
		}
	})
	t.Run("page param not an integer on the request", func(t *testing.T) {
		a, _ := newPathAddress(AddressSpec{BaseURL: "https://api.example.com", Query: map[string]any{"page": "x"}})
		if _, err := nextPage(a, PaginateInput{PageParam: "page", PageStep: 1}, nil, 1); err == nil {
			t.Error("non-integer page accepted")
		}
	})
}

func TestPaginateInput_Normalize(t *testing.T) {
	p, err := PaginateInput{Items: "data"}.normalize(nil)
	if err != nil || p.MaxPages != defaultMaxPages || p.PageStep != 1 {
		t.Errorf("defaults = %+v err %v", p, err)
	}
	p, _ = PaginateInput{Items: "data", MaxPages: 99999}.normalize(nil)
	if p.MaxPages != maxMaxPages {
		t.Errorf("max_pages clamp = %d", p.MaxPages)
	}
	cases := []struct {
		name  string
		in    PaginateInput
		query map[string]any
		want  string
	}{
		{"items missing", PaginateInput{}, nil, "paginate.items is required"},
		{"negative page_step", PaginateInput{Items: "data", PageStep: -1}, nil, "must be positive"},
		{"page_param without start", PaginateInput{Items: "data", PageParam: "page"}, nil, "query_params does not carry it"},
		{"page_param not integer", PaginateInput{Items: "data", PageParam: "page"}, map[string]any{"page": "x"}, "is not an integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.normalize(tc.query)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v; want %q", err, tc.want)
			}
		})
	}
	if got := (PaginateInput{Items: "a.b"}).itemsPath(); len(got) != 2 {
		t.Errorf("itemsPath = %v", got)
	}
	if got := (PaginateInput{Items: "$"}).itemsPath(); got != nil {
		t.Errorf("itemsPath($) = %v", got)
	}
	if _, err := (PaginateInput{Items: "data", PageParam: "offset", PageStep: 100}).normalize(map[string]any{"offset": float64(0)}); err != nil {
		t.Errorf("a numeric starting value refused: %v", err)
	}
}

func TestScalarString(t *testing.T) {
	cases := map[any]string{"s": "s", true: "true", 7: "7", int64(8): "8", 1e21: "1000000000000000000000", 2.5: "2.5"}
	for in, want := range cases {
		if got := ScalarString(in); got != want {
			t.Errorf("ScalarString(%v) = %q; want %q", in, got, want)
		}
	}
	if got := ScalarString([]int{1}); got != "[1]" {
		t.Errorf("default case = %q", got)
	}
}

func TestQueryToMap(t *testing.T) {
	if got := queryToMap(url.Values{}); got != nil {
		t.Errorf("empty = %v", got)
	}
}

func TestFinalCursor(t *testing.T) {
	if got := FinalCursor(nil); got != "" {
		t.Errorf("nil = %q", got)
	}
	if got := FinalCursor(&PaginationInfo{NextURL: "https://h/p", NextCursor: "c"}); got != "https://h/p" {
		t.Errorf("url = %q", got)
	}
	if got := FinalCursor(&PaginationInfo{NextCursor: "c"}); got != "c" {
		t.Errorf("cursor = %q", got)
	}
}

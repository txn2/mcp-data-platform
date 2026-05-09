package apigateway

import (
	"net/http"
	"testing"
)

func TestPaginationFromLinkHeader(t *testing.T) {
	cases := []struct {
		name    string
		linkVal string
		wantURL string
	}{
		{
			name:    "single rel=next",
			linkVal: `<https://api.example.com/v1/items?cursor=abc>; rel="next"`,
			wantURL: "https://api.example.com/v1/items?cursor=abc",
		},
		{
			name:    "rel=next without quotes",
			linkVal: `<https://api.example.com/v1/items?cursor=abc>; rel=next`,
			wantURL: "https://api.example.com/v1/items?cursor=abc",
		},
		{
			name:    "next is second entry",
			linkVal: `<https://api.example.com/v1/items?page=1>; rel="prev", <https://api.example.com/v1/items?cursor=abc>; rel="next"`,
			wantURL: "https://api.example.com/v1/items?cursor=abc",
		},
		{
			name:    "no rel=next entry",
			linkVal: `<https://api.example.com/v1/items?page=1>; rel="prev", <https://api.example.com/v1/items?page=99>; rel="last"`,
			wantURL: "",
		},
		{
			name:    "empty header",
			linkVal: "",
			wantURL: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.linkVal != "" {
				h.Set("Link", c.linkVal)
			}
			info := paginationFromLinkHeader(h)
			if c.wantURL == "" {
				if info != nil {
					t.Errorf("expected nil, got %+v", info)
				}
				return
			}
			if info == nil {
				t.Fatal("expected pagination info, got nil")
			}
			if info.NextURL != c.wantURL {
				t.Errorf("NextURL = %q; want %q", info.NextURL, c.wantURL)
			}
			if info.Source != "link_header" {
				t.Errorf("Source = %q; want link_header", info.Source)
			}
			if !info.HasMore {
				t.Error("HasMore = false; want true when rel=next is present")
			}
		})
	}
}

func TestPaginationFromBody(t *testing.T) {
	cases := []struct {
		name       string
		body       any
		wantCursor string // expected in NextCursor when non-empty
		wantURL    string // expected in NextURL when non-empty (URL-typed cursor)
		wantSource string
	}{
		{
			name:       "next_cursor",
			body:       map[string]any{"items": []any{}, "next_cursor": "abc"},
			wantCursor: "abc",
			wantSource: "body:next_cursor",
		},
		{
			name: "OData nextLink → NextURL (not NextCursor)",
			// Mis-routing @odata.nextLink to NextCursor was the bug
			// fixed by isURLLikeCursor. The model would otherwise
			// pass a fully-qualified URL as `?cursor=`.
			body:       map[string]any{"@odata.nextLink": "https://x/page2", "next_cursor": "abc"},
			wantURL:    "https://x/page2",
			wantSource: "body:@odata.nextLink",
		},
		{
			name:       "Google nextPageToken",
			body:       map[string]any{"items": []any{}, "nextPageToken": "tok-xyz"},
			wantCursor: "tok-xyz",
			wantSource: "body:nextPageToken",
		},
		{
			name:       "integer cursor (rare but seen)",
			body:       map[string]any{"next": float64(1234)},
			wantCursor: "1234",
			wantSource: "body:next",
		},
		{
			name:       "float cursor preserved",
			body:       map[string]any{"next": 12.5},
			wantCursor: "12.5",
			wantSource: "body:next",
		},
		{
			name: "URL value in generic `next` → NextURL",
			// Non-OData APIs sometimes use `next: "https://..."`. The
			// scheme prefix triggers URL routing without a hardcoded
			// key match.
			body:       map[string]any{"next": "https://api.example.com/page2"},
			wantURL:    "https://api.example.com/page2",
			wantSource: "body:next",
		},
		{
			name:       "boolean true marker",
			body:       map[string]any{"next": true},
			wantCursor: "true",
			wantSource: "body:next",
		},
		{
			name:       "empty cursor string ignored",
			body:       map[string]any{"next_cursor": ""},
			wantCursor: "",
		},
		{
			name:       "zero number ignored (not synthetic 'next: 0')",
			body:       map[string]any{"next": float64(0)},
			wantCursor: "",
		},
		{
			name:       "non-object body",
			body:       []any{"a", "b"},
			wantCursor: "",
		},
		{
			name:       "nil body",
			body:       nil,
			wantCursor: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := paginationFromBody(c.body)
			if c.wantCursor == "" && c.wantURL == "" {
				if info != nil {
					t.Errorf("expected nil; got %+v", info)
				}
				return
			}
			if info == nil {
				t.Fatal("expected pagination info, got nil")
			}
			if info.NextCursor != c.wantCursor {
				t.Errorf("NextCursor = %q; want %q", info.NextCursor, c.wantCursor)
			}
			if info.NextURL != c.wantURL {
				t.Errorf("NextURL = %q; want %q", info.NextURL, c.wantURL)
			}
			if info.Source != c.wantSource {
				t.Errorf("Source = %q; want %q", info.Source, c.wantSource)
			}
		})
	}
}

// TestDetectPagination_LinkHeaderTakesPrecedence proves the
// authoritative-protocol ordering: when the upstream sends BOTH a
// Link header AND a body cursor, Link wins. This matters for APIs
// that emit a `next` field that's actually a hateoas link object,
// not a cursor; the Link header is the canonical "more pages"
// signal in that case.
func TestDetectPagination_LinkHeaderTakesPrecedence(t *testing.T) {
	h := http.Header{}
	h.Set("Link", `<https://api.example.com/v1/items?cursor=link>; rel="next"`)
	body := map[string]any{"next_cursor": "body-cursor"}
	info := detectPagination(h, body)
	if info == nil {
		t.Fatal("expected pagination info, got nil")
	}
	if info.Source != "link_header" {
		t.Errorf("Source = %q; want link_header (Link header should win)", info.Source)
	}
	if info.NextURL != "https://api.example.com/v1/items?cursor=link" {
		t.Errorf("NextURL = %q", info.NextURL)
	}
}

// TestIsURLLikeCursor codifies the routing rule: @odata.nextLink is
// always URL-routed even when the value is empty (caller filters
// empty values upstream); other keys are URL-routed only when the
// value parses as http(s).
func TestIsURLLikeCursor(t *testing.T) {
	cases := []struct {
		key, val string
		want     bool
	}{
		{"@odata.nextLink", "https://x/y", true},
		{"@odata.nextLink", "anything", true}, // OData spec guarantees URL
		{"next_cursor", "https://x/y", true},
		{"next_cursor", "abc", false},
		{"next", "http://x.example/page2", true},
		{"next", "tok-xyz", false},
	}
	for _, c := range cases {
		if got := isURLLikeCursor(c.key, c.val); got != c.want {
			t.Errorf("isURLLikeCursor(%q, %q) = %v; want %v", c.key, c.val, got, c.want)
		}
	}
}

func TestDetectPagination_NoSignal(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json") // unrelated header
	body := map[string]any{"items": []any{"a", "b"}, "count": float64(2)}
	if info := detectPagination(h, body); info != nil {
		t.Errorf("expected nil for response with no pagination signal; got %+v", info)
	}
}

func TestStringifyCursor_RejectsUnsupportedTypes(t *testing.T) {
	if got := stringifyCursor(map[string]any{"href": "x"}); got != "" {
		t.Errorf("hateoas-style next object should not stringify; got %q", got)
	}
	if got := stringifyCursor([]any{"a"}); got != "" {
		t.Errorf("array should not stringify; got %q", got)
	}
}

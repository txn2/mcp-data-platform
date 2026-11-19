package contentrefs_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/contentrefs"
)

func TestLinkedAssets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, contentType, body string
		want                    []string
	}{
		{
			"html anchors, resource and image left out", "text/html",
			`<a href="mcp:asset:b1">guide</a><a href="mcp://global/x.csv">x</a><img src="mcp:asset:img"><a href='mcp:asset:b2'>2</a>`,
			[]string{"b1", "b2"},
		},
		{
			"markdown links, fragment stripped, duplicates once", "text/markdown",
			"[a](mcp:asset:b1#intro) then [again](mcp:asset:b1) and ![chart](mcp:asset:img)",
			[]string{"b1"},
		},
		{"code is an example, not a link", "text/markdown", "```\n[x](mcp:asset:b1)\n```\n`[y](mcp:asset:b2)`", nil},
		{"a type with no links", "text/csv", `<a href="mcp:asset:b1">x</a>`, nil},
		{"a download anchor is not a link", "text/html", `<a href="mcp:asset:b1" download>save</a>`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, contentrefs.LinkedAssets(tt.body, tt.contentType))
		})
	}
}

func TestRefusedLinks(t *testing.T) {
	t.Parallel()
	body := `<a href="mcp:asset:b1">guide</a><a href="mcp://global/x.csv">file</a><a href="mcp:form:contact">form</a>`
	assert.Equal(t, []string{"mcp://global/x.csv", "mcp:form:contact"}, contentrefs.RefusedLinks(body, "text/html"),
		"a resource or form link target is refused; an asset link is a link")
	assert.Nil(t, contentrefs.RefusedLinks(`<a href="mcp:asset:b1">guide</a>`, "text/html"))
	assert.Nil(t, contentrefs.RefusedLinks("[x](mcp:asset:b1)", "text/markdown"))
	assert.Equal(t, []string{"mcp://global/x.csv"}, contentrefs.RefusedLinks("[x](mcp://global/x.csv)", "text/markdown"))
}

func TestAssetID(t *testing.T) {
	t.Parallel()
	for ref, want := range map[string]struct {
		id string
		ok bool
	}{
		"mcp:asset:ast_7c1e":       {"ast_7c1e", true},
		"mcp:asset:ast_7c1e#intro": {"ast_7c1e", true},
		"mcp:asset:ast_7c1e?v=2":   {"ast_7c1e", true},
		"mcp:asset:ast_7c1e/x":     {"ast_7c1e", true},
		"mcp:asset:":               {"", false},
		"mcp:asset:#x":             {"", false},
		"mcp://global/x.csv":       {"", false},
		"https://example.com":      {"", false},
	} {
		id, ok := contentrefs.AssetID(ref)
		assert.Equal(t, want.id, id, ref)
		assert.Equal(t, want.ok, ok, ref)
	}
}

// #2432: an asset named only as a link target is not undeclared; the same
// asset loaded elsewhere in the body still is, and so is a resource link.
func TestUndeclaredLeavesAssetLinksOut(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []string
	}{
		"html link":          {`<a href="mcp:asset:b1">guide</a>`, nil},
		"markdown link":      {"see [guide](mcp:asset:b1) now", nil},
		"link and a load":    {`<a href="mcp:asset:b1">x</a><img src="mcp:asset:b1">`, []string{"mcp:asset:b1"}},
		"markdown image":     {"![chart](mcp:asset:b2)", []string{"mcp:asset:b2"}},
		"download anchor":    {`<a href="mcp:asset:b3" download>csv</a>`, []string{"mcp:asset:b3"}},
		"resource link":      {`<a href="mcp://global/a.csv">a</a>`, []string{"mcp://global/a.csv"}},
		"two hrefs, one tag": {`<a href="mcp:asset:b1" xlink:href="mcp:asset:b4">x</a>`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := contentrefs.Undeclared(tc.body, nil); !slices.Equal(got, tc.want) {
				t.Fatalf("Undeclared(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

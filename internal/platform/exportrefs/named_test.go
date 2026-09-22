package exportrefs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/exportrefs"
)

func TestNamed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"none", "<p>no references</p>", nil},
		{
			"html attributes", `<img src="mcp://global/brand/logo.svg"><img src='mcp://global/brand/mark.png'>`,
			[]string{"mcp://global/brand/logo.svg", "mcp://global/brand/mark.png"},
		},
		{
			"markdown image", "![logo](mcp://global/brand/logo.svg) and more",
			[]string{"mcp://global/brand/logo.svg"},
		},
		{
			"js fetch of an asset", `fetch("mcp:asset:ast_7c1e").then(r => r.json())`,
			[]string{"mcp:asset:ast_7c1e"},
		},
		{
			"resources before assets", `mcp:asset:a1 then mcp://s/f.png`,
			[]string{"mcp://s/f.png", "mcp:asset:a1"},
		},
		{
			"prose punctuation is not part of it", "See mcp://global/brand/logo.svg.",
			[]string{"mcp://global/brand/logo.svg"},
		},
		{"duplicates collapse", `mcp://a/b.png mcp://a/b.png`, []string{"mcp://a/b.png"}},
		{"a bare scheme names nothing", `mcp:// and mcp:asset: alone`, nil},
		{"end of body terminates", "mcp://a/b.png", []string{"mcp://a/b.png"}},
		{"another scheme is not read", "acme://a/b.png", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, exportrefs.Named(tt.body))
		})
	}
}

func TestUndeclared(t *testing.T) {
	t.Parallel()
	body := `<img src="mcp://a/logo.svg"><img src="mcp://a/mark.png"><script src="mcp:asset:x1"></script>`
	assert.Equal(t, []string{"mcp://a/mark.png", "mcp:asset:x1"},
		exportrefs.Undeclared(body, []string{" mcp://a/logo.svg "}))
	assert.Empty(t, exportrefs.Undeclared(body, []string{"mcp://a/logo.svg", "mcp://a/mark.png", "mcp:asset:x1"}))
	assert.Empty(t, exportrefs.Undeclared("<p>none</p>", nil))
}

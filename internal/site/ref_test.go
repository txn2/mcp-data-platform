package site

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		in     string
		want   Ref
		isFile bool
	}{
		{in: "mcp:site:marketing/dev/pricing/index.html", want: Ref{Site: "marketing", Environment: "dev", Path: "pricing/index.html"}, isFile: true},
		{in: "mcp:site:marketing/dev", want: Ref{Site: "marketing", Environment: "dev"}},
		{in: "mcp:site:marketing/dev/", want: Ref{Site: "marketing", Environment: "dev"}},
		{in: "mcp:site:marketing/dev/docs/", want: Ref{Site: "marketing", Environment: "dev", Path: "docs/"}},
		{in: "mcp:site:marketing/prod/a.html", want: Ref{Site: "marketing", Environment: "prod", Path: "a.html"}, isFile: true},
		// The old name of dev is read as dev for one release (#2604).
		{in: "mcp:site:marketing/default/pricing/index.html", want: Ref{Site: "marketing", Environment: "dev", Path: "pricing/index.html"}, isFile: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRef(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.isFile, got.IsFile())
		})
	}
	for _, bad := range []string{"mcp:site:", "mcp:site:marketing", "mcp:site:/default", "mcp:site:marketing/", "mcp:asset:x"} {
		_, err := ParseRef(bad)
		require.Error(t, err, bad)
	}
	assert.True(t, IsRef("mcp:site:a/b"))
	assert.False(t, IsRef("mcp:asset:a"))
}

func TestRenderRedirects_ParsesBackToTheSameMap(t *testing.T) {
	rules := []Redirect{{From: "/a", To: "/b", Status: 301}, {From: "/old/*", To: "https://example.com/:splat", Status: 302}}
	back, err := ParseRedirects(RenderRedirects(rules))
	require.NoError(t, err)
	assert.Equal(t, rules, back)
	assert.Empty(t, RenderRedirects(nil))
}

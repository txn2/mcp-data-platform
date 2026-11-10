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
		{in: "mcp:site:marketing/default/pricing/index.html", want: Ref{Site: "marketing", Environment: "default", Path: "pricing/index.html"}, isFile: true},
		{in: "mcp:site:marketing/default", want: Ref{Site: "marketing", Environment: "default"}},
		{in: "mcp:site:marketing/default/", want: Ref{Site: "marketing", Environment: "default"}},
		{in: "mcp:site:marketing/default/docs/", want: Ref{Site: "marketing", Environment: "default", Path: "docs/"}},
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

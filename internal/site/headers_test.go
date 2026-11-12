package site

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseHeaders reads a release's _headers file (#2557): path blocks,
// indented headers, comments, and each header named in its canonical form.
func TestParseHeaders(t *testing.T) {
	rules, err := ParseHeaders("# policies\n/*\n  content-security-policy: default-src 'self'\n  X-Frame-Options: DENY\r\n\n/embed/*\n  Content-Security-Policy: frame-ancestors https://partner.example\n")
	require.NoError(t, err)
	assert.Equal(t, []HeaderRule{
		{Path: "/*", Headers: []Header{{Name: "Content-Security-Policy", Value: "default-src 'self'"}, {Name: "X-Frame-Options", Value: "DENY"}}},
		{Path: "/embed/*", Headers: []Header{{Name: "Content-Security-Policy", Value: "frame-ancestors https://partner.example"}}},
	}, rules)
	again, err := ParseHeaders(RenderHeaders(rules))
	require.NoError(t, err)
	assert.Equal(t, rules, again, "the rendered file parses back to the rules")
	empty, err := ParseHeaders("")
	require.NoError(t, err)
	assert.Equal(t, []HeaderRule{}, empty)
}

// TestParseHeaders_Refusals: a header outside the allowed set, a control
// character, and every other malformed line refuse the file, naming the line.
func TestParseHeaders_Refusals(t *testing.T) {
	tests := []struct {
		name, text, want string
	}{
		{"Set-Cookie", "/*\n  Set-Cookie: session=x", "line 2: a release cannot set Set-Cookie"},
		{"Cache-Control", "/*\n  Cache-Control: public, max-age=99999", "line 2: a release cannot set Cache-Control"},
		{"CORS", "/*\n  Access-Control-Allow-Origin: *", "line 2: a release cannot set Access-Control-Allow-Origin"},
		{"a control character", "/*\n  X-Frame-Options: DENY\x0bX-Evil: 1", "line 2: the value of X-Frame-Options holds a control character"},
		{"a header before any path", "  X-Frame-Options: DENY", "line 1: a header comes before any path"},
		{"not a header", "/*\n  just words", "line 2: \"just words\" is not a header"},
		{"no value", "/*\n  X-Frame-Options:", "line 2: X-Frame-Options has no value"},
		{"a space in a path", "/a b\n  X-Frame-Options: DENY", "line 1: path \"/a b\""},
		{"a star mid-path", "/a/*/b\n  X-Frame-Options: DENY", "line 1: path \"/a/*/b\": * is allowed only as the last part"},
		{"a value too long", "/*\n  Permissions-Policy: " + strings.Repeat("a", maxHeaderValue+1), "line 2: the value of Permissions-Policy is longer than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseHeaders(tt.text)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the _headers file, "+tt.want)
		})
	}
	many := "/*\n" + strings.Repeat("  X-Frame-Options: DENY\n", maxHeadersPerID+1)
	_, err := ParseHeaders(many)
	require.ErrorContains(t, err, "sets more than")
	_, err = ParseHeaders(strings.Repeat("/x\n", maxHeaderRules+1))
	require.ErrorContains(t, err, "more than 1000 paths")
	assert.Contains(t, HeaderNames(), "Permissions-Policy")
}

// TestHeadersFor: what a path is served with, a later rule replacing an
// earlier one's header, and the framing default unless the release declares
// framing of its own.
func TestHeadersFor(t *testing.T) {
	framingDefault := Header{Name: "Content-Security-Policy", Value: FramingDefault}
	assert.Equal(t, []Header{framingDefault}, HeadersFor(nil, "/"), "a release declaring nothing is held to the default")

	rules := []HeaderRule{
		{Path: "/*", Headers: []Header{{Name: "Content-Security-Policy", Value: "default-src 'self'"}, {Name: "Permissions-Policy", Value: "camera=()"}}},
		{Path: "/embed/*", Headers: []Header{{Name: "Content-Security-Policy", Value: "frame-ancestors https://partner.example"}}},
		{Path: "/legacy", Headers: []Header{{Name: "X-Frame-Options", Value: "SAMEORIGIN"}}},
	}
	assert.Equal(t, []Header{
		{Name: "Content-Security-Policy", Value: "default-src 'self'"}, {Name: "Permissions-Policy", Value: "camera=()"}, framingDefault,
	}, HeadersFor(rules, "/docs/a.html"), "a policy without frame-ancestors keeps the default beside it")
	assert.Equal(t, []Header{
		{Name: "Permissions-Policy", Value: "camera=()"}, {Name: "Content-Security-Policy", Value: "frame-ancestors https://partner.example"},
	}, HeadersFor(rules, "/embed/widget"), "a declared frame-ancestors replaces the default")
	assert.Contains(t, HeadersFor(rules, "/legacy/"), Header{Name: "X-Frame-Options", Value: "SAMEORIGIN"})
	assert.NotContains(t, HeadersFor(rules, "/legacy/"), framingDefault, "X-Frame-Options replaces the default")
	report := []HeaderRule{{Path: "/*", Headers: []Header{{Name: "Content-Security-Policy-Report-Only", Value: "frame-ancestors 'none'"}}}}
	assert.Contains(t, HeadersFor(report, "/"), framingDefault, "a report-only policy enforces nothing, so the default stays")
}

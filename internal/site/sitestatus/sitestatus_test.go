package sitestatus

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// TestParseRedirectTarget pins #2632's refusals, each naming its rule.
func TestParseRedirectTarget(t *testing.T) {
	own := []string{"pages.example.net", "portal.example.net", "www.client.example"}
	refused := map[string]string{
		"":                                     "an https:// URL",
		"javascript:alert(1)":                  "must start with https://",
		"http://x.example":                     "must start with https://",
		"https://u:p@x.example":                "user name or password",
		"/relative":                            "must start with https://",
		"https://www.client.example/":          "loop",
		"https://shop--prod.pages.example.net": "loop",
		"https://portal.example.net":           "loop",
		"https://x.example/\r\nSet-Cookie:a":   "control character",
		"https://x.example/" + strings.Repeat("a", MaxRedirectTarget): "at most 2048",
		"https:opaque": "must name a host",
	}
	for raw, want := range refused {
		_, err := ParseRedirectTarget(raw, own)
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), want, raw)
		assert.ErrorIs(t, err, ErrStatusInput)
	}
	u, err := ParseRedirectTarget("https://status.example.com/x#frag", own)
	require.NoError(t, err)
	assert.Equal(t, "https://status.example.com/x", u.String(), "a fragment is dropped")
}

// TestRedirectLocation pins #2632's path rule: the request adds path below
// the target and never changes its scheme or host.
func TestRedirectLocation(t *testing.T) {
	target, _ := url.Parse("https://status.example.com")
	cases := []struct {
		keep        bool
		path, query string
		want        string
	}{
		{false, "/pricing/", "a=1", "https://status.example.com"},
		{true, "/pricing/", "a=1", "https://status.example.com/pricing/?a=1"},
		{true, "//evil.example/x", "", "https://status.example.com/evil.example/x"},
		{true, "/../../etc", "", "https://status.example.com/etc"},
		{true, "/", "", "https://status.example.com/"},
	}
	for _, tc := range cases {
		got := RedirectLocation(target, tc.keep, tc.path, tc.query)
		assert.Equal(t, tc.want, got, "%v %s?%s", tc.keep, tc.path, tc.query)
		u, err := url.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, "status.example.com", u.Host)
	}
	withPath, _ := url.Parse("https://new.example.com/base/")
	assert.Equal(t, "https://new.example.com/base/pricing/", RedirectLocation(withPath, true, "/pricing/", ""))
}

func TestCleanStatusMessage(t *testing.T) {
	msg, err := CleanStatusMessage("  Back at 2pm\n\u202eevil\u200b  ")
	require.NoError(t, err)
	assert.Equal(t, "Back at 2pm\nevil", msg, "bidirectional and invisible runes removed, line breaks kept")
	_, err = CleanStatusMessage(strings.Repeat("é", MaxStatusMessage+1))
	require.ErrorContains(t, err, "at most 1000")
}

func TestRetryAfterAndStamp(t *testing.T) {
	now := time.Date(2026, 11, 14, 13, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	assert.Equal(t, "3600", RetryAfter(&later, now))
	assert.Empty(t, RetryAfter(&now, now), "a time that has passed sends none")
	assert.Empty(t, RetryAfter(nil, now))
	a := site.EnvironmentStatus{RedirectTo: "https://a.example", KeepPath: true}
	b := site.EnvironmentStatus{RedirectTo: "https://b.example", KeepPath: true}
	assert.NotEqual(t, Stamp(a), Stamp(b), "approving one target never approves another")
	assert.NotEqual(t, Stamp(a), Stamp(site.EnvironmentStatus{RedirectTo: "https://a.example"}))
}

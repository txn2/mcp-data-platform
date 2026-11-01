package maps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gateRequest(host, origin, referer string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/maps/us.pmtiles", http.NoBody)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
	return r
}

// Who a browser read is served for (#2393): the platform, the listed sites,
// and the reads that name no site; never a site that names itself and is not
// listed.
func TestSiteGate_Admit(t *testing.T) {
	store := &memSettings{s: Settings{AllowedOrigins: []string{"https://stores.example.com", "https://*.example.org"}}}
	g := NewSiteGate(store, "https://portal.example.net/")
	const host = "platform.internal:8080"
	cases := []struct {
		name, origin, referer string
		want                  bool
	}{
		{"no headers: curl, a renderer, a stripped Referer", "", "", true},
		{"sandboxed frame: Origin null, no Referer", "null", "", true},
		{"the request's own host", "http://platform.internal:8080", "", true},
		{"own host through the Referer", "", "http://platform.internal:8080/portal/assets/a1", true},
		{"own host behind a TLS proxy", "https://platform.internal:8080", "", true},
		{"public_base_url", "https://portal.example.net", "", true},
		{"public_base_url with its default port", "https://portal.example.net:443", "", true},
		{"a listed site", "https://stores.example.com", "", true},
		{"a listed site, upper case", "HTTPS://Stores.Example.com", "", true},
		{"a listed site through the Referer", "", "https://stores.example.com/locator?x=1", true},
		{"a wildcard's subdomain", "https://a.b.example.org", "", true},
		{"a wildcard does not match its bare domain", "https://example.org", "", false},
		{"a wildcard does not match another scheme", "http://a.example.org", "", false},
		{"an unlisted site", "https://freeloader.test", "", false},
		{"an unlisted site through the Referer", "", "https://freeloader.test/map", false},
		{"a sandboxed frame with a foreign Referer", "null", "https://freeloader.test/", false},
		{"a sandboxed frame with the platform's Referer", "null", "http://platform.internal:8080/portal/view/t", true},
		{"a listed host on another port", "https://stores.example.com:8443", "", false},
		{"a suffix that is not a subdomain", "https://notexample.org", "", false},
		{"garbage Origin", "::nonsense", "", false},
		{"garbage Referer", "", "::nonsense", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, g.Admit(gateRequest(host, c.origin, c.referer)))
		})
	}
}

// The list is read once per TTL; Forget makes the next request read it, and
// a read that fails keeps the last list.
func TestSiteGate_ReadsTheListPerTTL(t *testing.T) {
	store := &memSettings{s: Settings{AllowedOrigins: []string{"https://a.example.com"}}}
	g := NewSiteGate(store, "")
	now := time.Unix(1_000, 0)
	g.now = func() time.Time { return now }
	r := func() *http.Request { return gateRequest("p", "https://a.example.com", "") }

	require.True(t, g.Admit(r()))
	require.True(t, g.Admit(r()))
	assert.Equal(t, 1, store.gets, "the second read uses the list the first one read")

	store.s.AllowedOrigins = nil
	assert.True(t, g.Admit(r()), "within the TTL the removal is not yet seen")
	now = now.Add(allowedSitesTTL)
	assert.False(t, g.Admit(r()), "past the TTL a removed site is refused")

	store.s.AllowedOrigins = []string{"https://a.example.com"}
	g.Forget()
	assert.True(t, g.Admit(r()), "Forget reads the list on the next request")

	store.err = errors.New("db down")
	now = now.Add(allowedSitesTTL)
	assert.True(t, g.Admit(r()), "a failed read keeps the last list")
}

func TestNormalizeAllowedOrigins(t *testing.T) {
	got, err := NormalizeAllowedOrigins([]string{
		" https://Example.com ", "https://example.com:443", "http://localhost:3000", "http://127.0.0.1",
		"https://*.Example.org", "https://[::1]:8443", "http://[::1]",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"https://example.com", "http://localhost:3000", "http://127.0.0.1",
		"https://*.example.org", "https://[::1]:8443", "http://[::1]",
	}, got)

	empty, err := NormalizeAllowedOrigins(nil)
	require.NoError(t, err)
	assert.Equal(t, []string{}, empty)

	for _, bad := range []string{
		"example.com", "https://example.com/path", "https://example.com?q", "https://example.com#f",
		"ftp://example.com", "http://example.com", "https://user@example.com", "*", "https://*",
		"https://*.com", "https://*.localhost", "https://*.10.0.0.1", "https://ex*ample.com", "garbage", "",
	} {
		_, err := NormalizeAllowedOrigins([]string{bad})
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "allowed site", bad)
	}
}

func TestSettingsValidate_AllowedOrigins(t *testing.T) {
	s := DefaultSettings()
	s.AllowedOrigins = []string{"https://ok.example.com", "https://bad.example.com/path"}
	assert.Contains(t, s.Validate(), `"https://bad.example.com/path"`, "the refusal names the entry")
	s.AllowedOrigins = []string{"https://ok.example.com"}
	assert.Empty(t, s.Validate())
}

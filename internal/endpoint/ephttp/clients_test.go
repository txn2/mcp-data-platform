package ephttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprefusal"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// ipHandler is a handler over one public endpoint with a per-address limit
// of two a minute, trusting the proxies given, counting refusals by reason.
func ipHandler(t *testing.T, trusted []string) (*Handler, *runner, map[string]int) {
	t.Helper()
	b := binding("shop", epbinding.AuthPublic)
	b.IPRateLimit = 2
	fake := &runner{bindings: map[string]*epbinding.Binding{"shop": b}}
	clients, err := ratelimit.NewResolver(trusted)
	require.NoError(t, err)
	refused := map[string]int{}
	h := &Handler{Runner: fake, Clients: clients, Refused: func(_, reason string) { refused[reason]++ }}
	return h, fake, refused
}

func fromAddress(h *Handler, peer, forwarded string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/endpoints/shop/1", http.NoBody)
	req.RemoteAddr = peer
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	mux := http.NewServeMux()
	h.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestIPRateLimit is #2556 through the HTTP surface: two clients behind a
// trusted proxy each get their own limit, a forged leftmost X-Forwarded-For
// escapes nothing, the refusal is a 429 with Retry-After counted under its own
// reason, and req["client_ip"] is the resolved address.
func TestIPRateLimit(t *testing.T) {
	h, fake, refused := ipHandler(t, []string{"10.0.0.0/8"})
	for range 2 {
		require.Equal(t, http.StatusOK, fromAddress(h, "10.0.0.5:4000", "203.0.113.7").Code)
	}
	rec := fromAddress(h, "10.0.0.5:4000", "198.51.100.9, 203.0.113.7")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "a forged leftmost entry does not escape the client's bucket")
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.Equal(t, 1, refused[eprefusal.ReasonIPRateLimit])
	assert.Zero(t, refused[eprefusal.ReasonRateLimit], "counted under its own reason")
	assert.Equal(t, http.StatusOK, fromAddress(h, "10.0.0.5:4000", "203.0.113.8").Code, "another client is not limited")
	assert.Equal(t, "203.0.113.7", fake.served[0].ClientIP)
	assert.Equal(t, "203.0.113.8", fake.served[len(fake.served)-1].ClientIP)
}

func TestIPRateLimit_NoTrustedProxyIgnoresForwarding(t *testing.T) {
	h, fake, _ := ipHandler(t, nil)
	for _, forged := range []string{"203.0.113.1", "203.0.113.2"} {
		require.Equal(t, http.StatusOK, fromAddress(h, "192.0.2.10:5000", forged).Code)
	}
	assert.Equal(t, http.StatusTooManyRequests, fromAddress(h, "192.0.2.10:5000", "203.0.113.3").Code,
		"a forged X-Forwarded-For is ignored: the peer's address is the bucket")
	assert.Equal(t, "192.0.2.10", fake.served[0].ClientIP)
}

func TestIPRateLimit_OneIPv6Slash64IsOneBucket(t *testing.T) {
	h, _, _ := ipHandler(t, nil)
	require.Equal(t, http.StatusOK, fromAddress(h, "[2001:db8:1:2::a]:1", "").Code)
	require.Equal(t, http.StatusOK, fromAddress(h, "[2001:db8:1:2::b]:1", "").Code)
	assert.Equal(t, http.StatusTooManyRequests, fromAddress(h, "[2001:db8:1:2:ffff::c]:1", "").Code)
	assert.Equal(t, http.StatusOK, fromAddress(h, "[2001:db8:1:3::a]:1", "").Code, "the next /64 is another bucket")
}

func TestIPRateLimit_UnsetLimitsNoAddress(t *testing.T) {
	h, fake, _ := ipHandler(t, nil)
	fake.bindings["shop"].IPRateLimit = 0
	for range 5 {
		require.Equal(t, http.StatusOK, fromAddress(h, "192.0.2.10:5000", "").Code)
	}
	plain := &Handler{Runner: fake}
	require.Equal(t, http.StatusOK, fromAddress(plain, "192.0.2.11:5000", "203.0.113.4").Code)
	assert.Equal(t, "192.0.2.11", fake.served[len(fake.served)-1].ClientIP, "a handler with no resolver trusts no proxy")
}

// cookieRoute is a page route on an https origin with application cookies.
func cookieRoute(name string) PageRoute {
	r := viewerRoute(name, nil)
	r.CookiePrefix, r.Secure = eprun.CookiePrefixSecure, true
	return r
}

// TestServePage_CookiesInAndOut is #2555 through the HTTP surface: a page
// origin's handler reads only the application's cookies, prefix taken off;
// what it sets is written under the prefix, HttpOnly, Path=/, Secure, with no
// Domain, and the answer is private; the platform's own cookies never reach
// it, and a withheld request carries none.
func TestServePage_CookiesInAndOut(t *testing.T) {
	f := newPageFixture()
	f.add("shop", epbinding.AuthPublic, epbinding.ResponsePage, appShop)
	age := 3600
	runner, ok := f.handler.Runner.(*pageRunner)
	require.True(t, ok)
	runner.cookies = []eprun.Cookie{{Name: "cart", Value: "c-42", MaxAge: &age, SameSite: eprun.SameSiteLax}}
	header := http.Header{"Cookie": {"__Host-app-sid=s-1; __Host-page_viewer=v; page_state=st; __HOST-APP-sid=x; session=portal"}}

	rec := f.serve(http.MethodGet, cookieRoute("shop"), header)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]string{"sid": "s-1"}, f.fake.served[0].Cookies)
	set := rec.Header().Get("Set-Cookie")
	assert.Contains(t, set, "__Host-app-cart=c-42")
	for _, attr := range []string{"Path=/", "HttpOnly", "Secure", "SameSite=Lax", "Max-Age=3600"} {
		assert.Contains(t, set, attr)
	}
	assert.NotContains(t, strings.ToLower(set), "domain=")
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))

	withheld := cookieRoute("shop")
	withheld.CookiesWithheld = true
	f.serve(http.MethodPost, withheld, header)
	assert.Empty(t, f.fake.served[1].Cookies, "a cross-site write reaches the handler with no cookies")

	plain := viewerRoute("shop", nil)
	rec = f.serve(http.MethodGet, plain, header)
	assert.Empty(t, f.fake.served[2].Cookies, "an origin with no cookie prefix carries none")
	assert.Empty(t, rec.Header().Values("Set-Cookie"), "and sets none")
}

func TestServe_ThePlatformsHostSetsAndReadsNoApplicationCookie(t *testing.T) {
	fake := &runner{bindings: map[string]*epbinding.Binding{"shop": binding("shop", epbinding.AuthPublic)}}
	h := &Handler{Runner: fake}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/endpoints/shop/1", http.NoBody)
	req.Header.Set("Cookie", "__Host-app-sid=s-1; app-sid=s-2")
	mux := http.NewServeMux()
	h.Mount(mux)
	mux.ServeHTTP(httptest.NewRecorder(), req)
	assert.Empty(t, fake.served[0].Cookies)
}

func TestSetCookies(t *testing.T) {
	zero, week := 0, 7*24*3600
	resp := eprun.Response{Header: http.Header{"Cache-Control": {"public, max-age=60"}}, Cookies: []eprun.Cookie{
		{Name: "sid", Value: "", MaxAge: &zero, SameSite: eprun.SameSiteStrict},
		{Name: "seen", Value: "1", MaxAge: &week, SameSite: eprun.SameSiteLax},
		{Name: "visit", Value: "v"},
	}}
	setCookies(&resp, &PageRoute{CookiePrefix: eprun.CookiePrefixPlain})
	set := resp.Header.Values("Set-Cookie")
	require.Len(t, set, 3)
	assert.Contains(t, set[0], "app-sid=; Path=/; Max-Age=0; HttpOnly; SameSite=Strict", "max_age 0 deletes")
	assert.NotContains(t, set[0], "Secure", "an http origin's cookie is not Secure")
	assert.Contains(t, set[1], "Max-Age=604800")
	assert.NotContains(t, set[2], "Max-Age", "no max_age is a session cookie")
	assert.Equal(t, "private, max-age=60", resp.Header.Get("Cache-Control"), "public is never kept on a response setting a cookie")

	for in, want := range map[string]string{"": "private, no-store", "no-store": "no-store", "private, max-age=5": "private, max-age=5", "PUBLIC": "private, no-store"} {
		assert.Equal(t, want, privateCache(in), in)
	}
}

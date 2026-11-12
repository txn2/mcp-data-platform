package ephttp

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
)

// requestCookies is what a handler reads as req["cookies"] (#2555): the
// application's cookies on its own address, none anywhere else, and none on a
// request that changes something without proving it came from that address.
func requestCookies(r *http.Request, page *PageRoute) map[string]string {
	if page == nil || page.CookiePrefix == "" || page.CookiesWithheld {
		return map[string]string{}
	}
	cookies := r.Cookies()
	names, values := make([]string, len(cookies)), make([]string, len(cookies))
	for i, c := range cookies {
		names[i], values[i] = c.Name, c.Value
	}
	return eprun.ReadCookies(page.CookiePrefix, names, values)
}

// setCookies writes the cookies a handler returned onto its response under
// the origin's prefix, HttpOnly, Path=/, Secure over https and never with a
// Domain, and keeps the response out of shared caches (#2555). Anywhere but an
// application's own address they are dropped: the platform's origin sets no
// application cookie.
func setCookies(resp *eprun.Response, page *PageRoute) {
	if len(resp.Cookies) == 0 {
		return
	}
	if page == nil || page.CookiePrefix == "" {
		resp.Cookies = nil
		return
	}
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	for _, c := range resp.Cookies {
		// Secure follows the page origin's scheme: a __Host- cookie over https,
		// and plain http only on a deployment with no TLS, where a Secure
		// cookie would never come back.
		// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
		hc := &http.Cookie{ // #nosec G124 -- Secure follows the page origin's scheme; HttpOnly and SameSite are always set
			Name: page.CookiePrefix + c.Name, Value: c.Value, Path: "/", HttpOnly: true, Secure: page.Secure,
			SameSite: http.SameSiteLaxMode,
		}
		if c.SameSite == eprun.SameSiteStrict {
			hc.SameSite = http.SameSiteStrictMode
		}
		switch {
		case c.MaxAge == nil:
		case *c.MaxAge == 0:
			hc.MaxAge = -1
		default:
			hc.MaxAge = *c.MaxAge
		}
		resp.Header.Add("Set-Cookie", hc.String())
	}
	resp.Header.Set(headerCacheControl, privateCache(resp.Header.Get(headerCacheControl)))
}

// privateCache is a Cache-Control value for a response that sets a cookie:
// what the handler said, never public, and private unless it already keeps
// the response out of every cache.
func privateCache(v string) string {
	var keep []string
	private := false
	for d := range strings.SplitSeq(v, ",") {
		d = strings.TrimSpace(d)
		switch lower := strings.ToLower(d); {
		case d == "", lower == "public":
		case lower == "private", lower == "no-store":
			private = true
			keep = append(keep, d)
		default:
			keep = append(keep, d)
		}
	}
	if len(keep) == 0 {
		return "private, no-store"
	}
	if !private {
		keep = append([]string{"private"}, keep...)
	}
	return strings.Join(keep, ", ")
}

package maps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// The map routes are a tile server, and without a check every website on the
// internet could draw its maps from this deployment's bucket at the operator's
// expense (#2393). SiteGate decides which sites a browser may read them for:
// the platform itself and the sites the operator lists in AllowedOrigins.
//
// A browser does not let a page set its own Origin or Referer, so a read a
// browser makes for an unlisted site names that site and is refused. What the
// gate cannot tell apart, it admits:
//
//   - Origin: null with no Referer. Chromium sends exactly that from the
//     platform's own sandboxed asset frames (an srcdoc frame without
//     allow-same-origin), and from any other site's sandboxed frame alike.
//   - No Origin and no Referer: curl, a script, the in-process renderers, and
//     a browser whose extension strips the Referer. A client outside a
//     browser fetches and pays for every byte itself.
//
// So the gate stops a site embedding the platform's maps the ordinary way. It
// is not access control: the archives hold public OpenStreetMap data.

// allowedSitesTTL is how long a replica uses the allowed sites it read. A
// change saved on another replica is in force within it.
const allowedSitesTTL = 10 * time.Second

// wildcardPrefix is the one wildcard an entry may carry: any one or more
// subdomain labels of the domain after it.
const wildcardPrefix = "*."

// Schemes an entry and a request's site are read with, and what follows one.
const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"
	schemeSep   = "://"
)

// SiteGate admits or refuses a read of the map routes by the site that made
// it.
type SiteGate struct {
	settings SettingsStore
	own      string // the origin of portal.public_base_url, or ""
	now      func() time.Time

	mu      sync.Mutex
	allowed []string
	readAt  time.Time
}

// NewSiteGate returns a gate reading the allowed sites from settings.
// publicBaseURL is portal.public_base_url, whose origin is the platform's own
// besides the host a request arrives on.
func NewSiteGate(settings SettingsStore, publicBaseURL string) *SiteGate {
	own := ""
	if u, err := url.Parse(publicBaseURL); err == nil && u.Host != "" {
		own, _ = normalizeOrigin(u.Scheme + schemeSep + u.Host)
	}
	return &SiteGate{settings: settings, own: own, now: time.Now}
}

// Forget drops the allowed sites this replica read, so the next request reads
// them again. Called when they are saved here.
func (g *SiteGate) Forget() {
	g.mu.Lock()
	g.readAt = time.Time{}
	g.mu.Unlock()
}

// Admit reports whether r is served.
func (g *SiteGate) Admit(r *http.Request) bool {
	site := requestSite(r)
	if site == "" {
		return true
	}
	if g.isOwn(site, r.Host) {
		return true
	}
	for _, entry := range g.allowedSites(r.Context()) {
		if matchesEntry(entry, site) {
			return true
		}
	}
	slog.DebugContext(r.Context(), "maps: refused a read for a site not allowed",
		"site", logsan.SanitizeForLog(site))
	return false
}

// requestSite is the site a browser says made r: Origin when it names one,
// else the origin of the Referer. "" is a request that names none, which the
// gate admits. A site that cannot be read is returned as written, and matches
// nothing.
func requestSite(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		if n, err := normalizeOrigin(o); err == nil {
			return n
		}
		return o
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return ref
	}
	if n, err := normalizeOrigin(u.Scheme + schemeSep + u.Host); err == nil {
		return n
	}
	return ref
}

// isOwn reports whether site is the platform: the host the request arrived
// on, whatever scheme a proxy in front terminated, or public_base_url.
func (g *SiteGate) isOwn(site, requestHost string) bool {
	if g.own != "" && site == g.own {
		return true
	}
	u, err := url.Parse(site)
	if err != nil || requestHost == "" {
		return false
	}
	return strings.EqualFold(u.Host, stripDefaultPort(requestHost))
}

func (g *SiteGate) allowedSites(ctx context.Context) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.readAt.IsZero() && g.now().Sub(g.readAt) < allowedSitesTTL {
		return g.allowed
	}
	s, err := g.settings.Get(ctx)
	if err != nil {
		// The last list read stays in force; a database blip neither opens
		// the routes to everyone nor closes them to the allowed sites.
		slog.WarnContext(ctx, "maps: reading the allowed sites", "error", logsan.SanitizeForLog(err.Error()))
		return g.allowed
	}
	g.allowed, g.readAt = s.AllowedOrigins, g.now()
	return g.allowed
}

// matchesEntry reports whether a normalized site matches a normalized entry:
// equal, or, for a *. entry, the same scheme and port on a subdomain of its
// domain.
func matchesEntry(entry, site string) bool {
	if entry == site {
		return true
	}
	scheme, rest, ok := strings.Cut(entry, schemeSep)
	domain, wild := strings.CutPrefix(rest, wildcardPrefix)
	if !ok || !wild {
		return false
	}
	siteScheme, siteHost, ok := strings.Cut(site, schemeSep)
	if !ok || siteScheme != scheme {
		return false
	}
	return strings.HasSuffix(siteHost, "."+domain)
}

// NormalizeAllowedOrigins checks and normalizes the allowed sites an operator
// saves: each is scheme://host[:port], https except http on a loopback host,
// with no path, query or fragment, and may start its host with "*.". The
// result is lower-cased, without default ports, de-duplicated, in the order
// given. A refusal names the entry.
func NormalizeAllowedOrigins(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		n, err := normalizeEntry(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("allowed site %q: %w", raw, err)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

func normalizeEntry(raw string) (string, error) {
	scheme, rest, ok := strings.Cut(raw, schemeSep)
	if !ok {
		return "", errNotAnOrigin
	}
	host, wild := strings.CutPrefix(rest, wildcardPrefix)
	n, err := normalizeOrigin(scheme + schemeSep + host)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(n) // normalizeOrigin returned it parsed once already
	if u.Scheme == schemeHTTP && !isLoopback(u.Hostname()) {
		return "", errPlainHTTP
	}
	if !wild {
		return n, nil
	}
	if isLoopback(u.Hostname()) || net.ParseIP(u.Hostname()) != nil || !strings.Contains(u.Hostname(), ".") {
		return "", errBadWildcard
	}
	return u.Scheme + schemeSep + wildcardPrefix + u.Host, nil
}

// The refusals of an allowed site.
var (
	errNotAnOrigin = errors.New("must be scheme://host[:port], with no path, query or fragment")
	errPlainHTTP   = errors.New("must use https; http is allowed only for localhost and loopback addresses")
	errBadWildcard = errors.New("a *. wildcard must precede a domain with at least two labels")
)

// normalizeOrigin parses scheme://host[:port] and returns it lower-cased
// without the scheme's default port.
func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !isBareOrigin(u, raw) {
		return "", errNotAnOrigin
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != schemeHTTPS && scheme != schemeHTTP {
		return "", errNotAnOrigin
	}
	return scheme + schemeSep + hostWithoutDefaultPort(scheme, u), nil
}

// isBareOrigin reports whether u is a host and nothing else: no user, path,
// query, fragment or wildcard.
func isBareOrigin(u *url.URL, raw string) bool {
	if u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(raw, "?") {
		return false
	}
	return !strings.Contains(u.Host, "*")
}

// hostWithoutDefaultPort is u's host lower-cased, without the port its
// scheme implies.
func hostWithoutDefaultPort(scheme string, u *url.URL) string {
	p := u.Port()
	if (scheme == schemeHTTPS && p == "443") || (scheme == schemeHTTP && p == "80") {
		host := strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return strings.ToLower(u.Host)
}

// stripDefaultPort removes :80 or :443 from a Host header, which a client may
// send either way.
func stripDefaultPort(host string) string {
	if h, ok := strings.CutSuffix(host, ":443"); ok {
		return h
	}
	if h, ok := strings.CutSuffix(host, ":80"); ok {
		return h
	}
	return host
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

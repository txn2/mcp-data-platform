// Package httpobs is what every inbound HTTP request reports (#1889): the
// route TEMPLATE it matched, its duration under that template, a server span
// named by it, a counter for every 429 a rate limiter answered, and a WARN
// line for a request slower than the deployment's threshold.
//
// The template, never the path. A path is the caller's to choose, and a
// metric label or a span name that carried it would hold a series per
// invented path and personal data per real one. The template is the pattern
// the mux matched ("GET /api/v1/assets/{id}"), a closed set in the code.
//
// The template is resolved before any handler runs, and refined by every
// nested mux the request descends into. An auth layer clones the request
// before the mux under it matches, so the pattern that mux writes onto its
// clone never reaches an outer middleware; Routed resolves a nested mux's
// pattern on the request as it arrives and records it in the scope the
// middleware installed, through a pointer a clone keeps.
package httpobs

import (
	"context"
	"net/http"
)

// RouteUnmatched is the route label of a request no registered pattern
// answered: a 404 or 405 from the mux, a CORS preflight the CORS layer
// answered before the mux, or a CONNECT (whose mux pattern is the raw path).
const RouteUnmatched = "unmatched"

// The HTTP rate limiters, as http_rate_limited_total{limiter} names them.
// A 429 no limiter marked is counted under LimiterUnknown, so an unmarked
// limiter shows up as a series rather than vanishing.
const (
	LimiterOAuthToken         = "oauth_token" // #nosec G101 -- a limiter's name, not a credential
	LimiterOAuthRegister      = "oauth_register"
	LimiterPortalViewer       = "portal_viewer"
	LimiterPortalContent      = "portal_content"
	LimiterPortalRefs         = "portal_refs"
	LimiterObservabilityProxy = "observability_proxy"
	LimiterPDFExport          = "pdf_export"
	LimiterWebhook            = "webhook"
	LimiterUnknown            = "unknown"
)

// scope is what the handlers under the middleware report back up through the
// request's context: the innermost route template resolved so far and the
// limiter that refused the request, if one did. The middleware installs one
// per request; a request clone keeps the pointer.
type scope struct {
	route   string
	limiter string
}

type scopeKey struct{}

func withScope(ctx context.Context, s *scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

func scopeOf(ctx context.Context) *scope {
	s, _ := ctx.Value(scopeKey{}).(*scope)
	return s
}

// Routed serves a nested mux behind wrap (an auth layer; nil for none) and
// reports the route template the request matched on it, twice over:
//
//   - before wrap runs, the pattern mux would match is resolved and recorded,
//     so a request the auth layer refuses still reports the template it was
//     headed for, and the record survives the clone the auth layer makes;
//   - after mux has served, the pattern written onto the request it served
//     is recorded, which is the innermost one: a mux nested under mux again
//     (the audit, knowledge and catalog muxes under the admin mux) serves the
//     same request and overwrites it, and the mount prefix the first pass
//     resolved gives way to the route itself.
//
// A request mux does not match keeps the route an outer mux resolved. Outside
// the middleware (a handler driven directly in a test) Routed only serves.
func Routed(mux *http.ServeMux, wrap func(http.Handler) http.Handler) http.Handler {
	var inner http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if s := scopeOf(r.Context()); s != nil && r.Pattern != "" && r.Method != http.MethodConnect {
			s.route = r.Pattern
		}
	})
	if wrap != nil {
		inner = wrap(inner)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := scopeOf(r.Context()); s != nil {
			if pattern := resolvePattern(mux, r); pattern != "" {
				s.route = pattern
			}
		}
		inner.ServeHTTP(w, r)
	})
}

// MarkRateLimited records that limiter answered r with a 429, so the
// middleware counts the refusal under its name. A no-op outside the middleware.
func MarkRateLimited(r *http.Request, limiter string) {
	if s := scopeOf(r.Context()); s != nil {
		s.limiter = limiter
	}
}

// Detached is ctx without the request's scope, for a handler that serves a
// second route in-process on the caller's behalf (the PDF routes read their
// document through the content route beside them). Without it the nested
// route the re-entry reaches would report itself as the caller's route.
func Detached(ctx context.Context) context.Context {
	return context.WithValue(ctx, scopeKey{}, (*scope)(nil))
}

// resolvePattern is the pattern mux matches for r without serving it, or ""
// when no pattern does. A CONNECT request is never resolved: for one the mux
// reports the raw path as the pattern, which is the one thing the route
// label must never carry.
func resolvePattern(mux *http.ServeMux, r *http.Request) string {
	if mux == nil || r.Method == http.MethodConnect {
		return ""
	}
	_, pattern := mux.Handler(r)
	return pattern
}

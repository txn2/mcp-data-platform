// Package corshttp is the CORS layer of the platform's listener: the headers
// a browser-based MCP client needs, the preflight answer, and the one path
// family that bypasses it, the webhook receiver (#1870). Its own package so
// internal/httpserver, at its size budget, holds the assembly alone.
package corshttp

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/maphttp"
	whreceiver "github.com/txn2/mcp-data-platform/internal/webhook/receiver"
)

// publicReadPrefixes are the paths a map reads with no session (#2068): the
// basemap archives and the map runtime the portal serves. A map runs in a
// sandboxed asset frame, whose origin is opaque, so its reads arrive with
// Origin: null; a credentialed answer that echoes that origin is one a
// browser may refuse, and these paths carry nothing a credential unlocks.
var publicReadPrefixes = []string{maphttp.PathPrefix, "/portal/vendor/maplibre/"}

// SiteCheck reports whether a read of a public map path is served for the
// site that made it (#2393). nil serves every site.
type SiteCheck func(*http.Request) bool

// refusedSiteBody is the whole answer to a refused read. It never repeats the
// site, which the caller chose.
const refusedSiteBody = "This map is not served to this site.\n"

// publicRead answers a read of a public path, without credentials, for a site
// admit allows, and lets the reader send Range and see the headers a ranged
// read is checked by. The answer varies by the headers the site is read
// from, so no shared cache gives one site's answer to another.
func publicRead(w http.ResponseWriter, r *http.Request, next http.Handler, admit SiteCheck) {
	w.Header().Add("Vary", "Origin, Referer")
	if admit != nil && !admit(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(refusedSiteBody))
		return
	}
	allow := "*"
	if admit != nil && r.Header.Get("Origin") != "" {
		allow = r.Header.Get("Origin")
	}
	w.Header().Set("Access-Control-Allow-Origin", allow)
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Range, If-Match, If-None-Match, If-Range")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, ETag, Accept-Ranges")
	w.Header().Set("Access-Control-Max-Age", "86400")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	next.ServeHTTP(w, r)
}

func isPublicRead(path string) bool {
	for _, p := range publicReadPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// Middleware adds CORS headers for browser-based MCP clients, and serves the
// public map paths to every site. It is the layer the in-process readers (the
// tile worker, the PDF export, a script's self-call) go through; the listener
// uses Gated.
func Middleware(next http.Handler) http.Handler {
	return Gated(nil)(next)
}

// Gated is Middleware with the public map paths served only to the sites
// admit allows.
func Gated(admit SiteCheck) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublicRead(r.URL.Path) {
				publicRead(w, r, next, admit)
				return
			}
			credentialed(w, r, next)
		})
	}
}

// credentialed answers every other path for the origin that asked, with
// credentials.
func credentialed(w http.ResponseWriter, r *http.Request, next http.Handler) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, Authorization, Accept, X-API-Key, "+
			"Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Max-Age", "86400")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	next.ServeHTTP(w, r)
}

// ownCORSPrefixes are the routes that answer their own cross-origin
// requests, which this layer leaves alone.
//
// The webhook receiver: a webhook is posted by a server, never a browser,
// and the receiver answers OPTIONS itself: the CloudEvents handshake is an
// OPTIONS request, and a source without it must refuse one rather than have
// it answered 200 here (#1870). Endpoints and their bot check (#2174): each
// binding names the origins it answers, without credentials, and refuses the
// preflight of any other, where this layer would reflect every origin with
// credentials allowed.
var ownCORSPrefixes = []string{whreceiver.PathPrefix, ephttp.PathPrefix, ephttp.BotCheckPrefix}

// WithoutHooks routes the routes that answer their own cross-origin requests
// (ownCORSPrefixes) around cors.
func WithoutHooks(receiver, rest http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, prefix := range ownCORSPrefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				receiver.ServeHTTP(w, r)
				return
			}
		}
		rest.ServeHTTP(w, r)
	})
}

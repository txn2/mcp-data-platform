// Package corshttp is the CORS layer of the platform's listener: the headers
// a browser-based MCP client needs, the preflight answer, and the one path
// family that bypasses it, the webhook receiver (#1870). Its own package so
// internal/httpserver, at its size budget, holds the assembly alone.
package corshttp

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpserver/maphttp"
	whreceiver "github.com/txn2/mcp-data-platform/internal/webhook/receiver"
)

// publicReadPrefixes are the paths a map reads with no session (#2068): the
// basemap archives and the map runtime the portal serves. A map runs in a
// sandboxed asset frame, whose origin is opaque, so its reads arrive with
// Origin: null; a credentialed answer that echoes that origin is one a
// browser may refuse, and these paths carry nothing a credential unlocks.
var publicReadPrefixes = []string{maphttp.PathPrefix, "/portal/vendor/maplibre/"}

// publicRead answers a read of a public path for any origin, without
// credentials, and lets the reader send Range and see the headers a ranged
// read is checked by.
func publicRead(w http.ResponseWriter, r *http.Request, next http.Handler) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

// Middleware adds CORS headers for browser-based MCP clients.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicRead(r.URL.Path) {
			publicRead(w, r, next)
			return
		}
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
	})
}

// WithoutHooks routes the webhook receiver around cors. A webhook is posted by
// a server, never a browser, and the receiver answers OPTIONS itself: the
// CloudEvents handshake is an OPTIONS request, and a source without it must
// refuse one rather than have it answered 200 here (#1870).
func WithoutHooks(receiver, rest http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, whreceiver.PathPrefix) {
			receiver.ServeHTTP(w, r)
			return
		}
		rest.ServeHTTP(w, r)
	})
}

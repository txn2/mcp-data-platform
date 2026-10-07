// Package corshttp is the CORS layer of the platform's listener: the headers
// a browser-based MCP client needs, the preflight answer, and the one path
// family that bypasses it, the webhook receiver (#1870). Its own package so
// internal/httpserver, at its size budget, holds the assembly alone.
package corshttp

import (
	"net/http"
	"strings"

	whreceiver "github.com/txn2/mcp-data-platform/internal/webhook/receiver"
)

// Middleware adds CORS headers for browser-based MCP clients.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

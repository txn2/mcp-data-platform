// Package apiresponse holds the API gateway's rules for an upstream
// response: which bodies are safe to return inline, and which headers reach
// the caller. Moved out of pkg/toolkits/apigateway for its size budget.
package apiresponse

import (
	"mime"
	"net/http"
	"strings"
)

// Inlineable reports whether a response with this
// Content-Type is safe to buffer and return inline through the MCP/JSON
// channel. Text-shaped bodies (text/*, JSON, XML, form, JavaScript) are
// inlineable: JSON-escaping them is roughly size-preserving. Binary
// bodies (application/zip, application/octet-stream, image/*, audio/*,
// video/*, application/pdf, ...) are NOT: json.Marshal escapes every
// control and invalid-UTF-8 byte as \uXXXX, inflating a high-entropy
// body several-fold, and the escaped copy is held alongside the raw
// buffer and the re-marshaled MCP envelope. Returning such a body inline
// both risks an OOM and is useless to the model. The gateway refuses
// those before buffering and steers the caller to api_export.
//
// An empty or unparseable Content-Type returns true so the legacy
// buffered path (bounded by the per-request read cap) is unchanged for
// responses that declare no usable type.
func Inlineable(contentType string) bool {
	if contentType == "" {
		return true
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	mt = strings.ToLower(mt)
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	if strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml") {
		return true
	}
	switch mt {
	case "application/json", "application/xml",
		"application/x-www-form-urlencoded",
		"application/javascript", "application/ecmascript":
		return true
	default:
		return false
	}
}

// passthrough is the closed set of response headers
// returned to the model. Most upstream headers (Set-Cookie, Server,
// X-Powered-By, Date, etc.) are noise from the model's perspective;
// pagination and content-type headers are useful enough to be worth
// the context cost.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var passthrough = map[string]bool{
	"Content-Type":     true,
	"Content-Length":   true,
	"Content-Encoding": true,
	"Etag":             true,
	"Last-Modified":    true,
	"Link":             true,
	"Location":         true, // 3xx target — surfaced so the model can choose to follow manually
	"Retry-After":      true,
	// An upstream's notice that the operation is going away (#2486), which
	// the caller is better told at the call than after it stops working.
	"Deprecation":           true,
	"Sunset":                true,
	"X-Total-Count":         true,
	"X-Ratelimit-Limit":     true,
	"X-Ratelimit-Remaining": true,
	"X-Ratelimit-Reset":     true,
}

// Headers is the subset of h in the passthrough set, nil when none.
func Headers(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for name, values := range h {
		if passthrough[http.CanonicalHeaderKey(name)] {
			out[http.CanonicalHeaderKey(name)] = values
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

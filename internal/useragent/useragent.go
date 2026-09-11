// Package useragent is the User-Agent the platform presents to an
// upstream it calls, and the one rule for when it is sent: on every
// outbound request that does not already carry one.
//
// Go's net/http sends "Go-http-client/1.1" when a request names no
// User-Agent, and a web application firewall in front of a public
// endpoint refuses that value (#1679). The product string names the
// platform and its version so an operator reading an upstream's access
// log can tell the platform's traffic from any other Go program's, and
// a WAF rule can be written for it.
//
// The rule is applied at the http.RoundTripper level rather than at
// each request-building site so every request a connection's client
// sends is covered in one place: a tool call, a page of a walk, a
// schema introspection, and the token request an OAuth library builds
// out of the caller's sight. A header the operator pinned in
// static_headers, or one a caller supplied per call, is already on the
// request when it reaches the transport and is left alone.
package useragent

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/buildinfo"
)

// Header is the request header this package sets.
const Header = "User-Agent"

// product is the name the platform presents. The version follows it
// after a slash, in the form RFC 9110 gives a product token.
const product = "mcp-data-platform"

// Product is the User-Agent the platform sends when a request names
// none: the product name and the build's version.
func Product() string {
	return product + "/" + buildinfo.Version
}

// Effective is the User-Agent a request carrying h is sent with: the
// value h already holds, or Product when it holds none. It is the one
// definition of the rule the transport applies, so a message that names
// the User-Agent a request went out with (the graphql kind's
// introspection refusal) reads it from here rather than restating it.
func Effective(h http.Header) string {
	if ua := h.Get(Header); ua != "" {
		return ua
	}
	return Product()
}

// transport is the RoundTripper that applies Effective to every
// request before handing it to the transport it wraps.
type transport struct {
	base http.RoundTripper
}

// Transport wraps base so every request it sends carries a User-Agent.
// A nil base wraps http.DefaultTransport, matching what an http.Client
// with a nil Transport would have used.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base}
}

// RoundTrip sends req with a User-Agent. The request is cloned before
// the header is added: a RoundTripper must not modify the request it is
// given, and the caller may still be reading it.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get(Header) != "" {
		return t.base.RoundTrip(req) //nolint:wrapcheck // the transport's error is the caller's to classify
	}
	sent := req.Clone(req.Context())
	sent.Header.Set(Header, Product())
	return t.base.RoundTrip(sent) //nolint:wrapcheck // as above
}

// CloseIdleConnections forwards to the wrapped transport when it keeps
// a connection pool, so an http.Client whose Transport is this wrapper
// still releases its idle sockets when a connection is reloaded.
func (t *transport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Wraps reports whether rt is this package's transport and returns the
// RoundTripper it wraps, for a test that asserts on the transport
// beneath it (a dial timeout, a TLS config).
func Wraps(rt http.RoundTripper) (base http.RoundTripper, ok bool) {
	t, isWrapper := rt.(*transport)
	if !isWrapper {
		return nil, false
	}
	return t.base, true
}

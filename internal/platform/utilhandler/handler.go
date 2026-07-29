package utilhandler

import (
	"net/http"
	"time"
)

// FetchPath is the catalog path of the fetch_url operation. The
// embedded OpenAPI spec, the mux route here, and the path callers pass
// to api_invoke_endpoint / api_export must all agree on this literal.
const FetchPath = "/util/fetch"

// Shared small constants for the package.
const (
	// decimalBase is the base-10 radix passed to strconv parse/format.
	decimalBase = 10
	// portBits is the bitSize passed to strconv.ParseUint for a TCP
	// port (a uint16).
	portBits = 16
	// contentTypeHeader is the canonical Content-Type header name.
	contentTypeHeader = "Content-Type"
)

// idleConnectionTimeout / maxIdleConnections mirror the gateway's own
// outbound-transport pool tuning: occasional fan-out from tool calls,
// not high-throughput traffic.
const (
	idleConnectionTimeout = 90 * time.Second
	maxIdleConnections    = 10
)

// Options configures the util handler.
type Options struct {
	// AllowPrivateCIDRs lists prefixes exempted from the internal-range
	// SSRF block (apigateway.util_connection.allow_private_cidrs).
	// Empty means the default posture: public destinations open,
	// internal address space closed. An exemption grants ANY TCP port
	// on the listed range, so scope it to the specific host prefixes a
	// deployment must fetch from — a broad grant turns fetch_url into an
	// internal port-reach primitive.
	AllowPrivateCIDRs []string
}

// handler holds the shared guarded transport. One transport per
// handler keeps a keep-alive pool across calls; per-call state
// (redirect policy) lives in a throwaway http.Client around it.
type handler struct {
	transport http.RoundTripper
}

// New builds the util operations handler the api gateway dispatches
// handler=internal connections to. The returned handler serves the
// operations the embedded catalog spec (SpecJSON) declares; the two
// are versioned together in this package so they cannot drift.
//
// The outbound transport deliberately ignores proxy environment
// variables: an egress proxy sits inside the network perimeter, and
// routing guarded fetches through it would let the proxy reach
// destinations the dial guard just refused.
func New(opts Options) (http.Handler, error) {
	guard, err := newDialGuard(opts.AllowPrivateCIDRs)
	if err != nil {
		return nil, err
	}
	h := &handler{transport: &http.Transport{
		DialContext:           guard.DialContext,
		TLSHandshakeTimeout:   connectTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       idleConnectionTimeout,
		MaxIdleConns:          maxIdleConnections,
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+FetchPath, h.handleFetch)
	return mux, nil
}

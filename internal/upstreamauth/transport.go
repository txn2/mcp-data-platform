package upstreamauth

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/internal/useragent"
)

// AuthorizationHeader is the HTTP header bearer-mode auth populates.
// Named so the same literal is not repeated across the auth dispatch
// and the header-spoof rejection.
const AuthorizationHeader = "Authorization"

// idleConnectionTimeout caps how long an idle keep-alive connection
// can sit in the pool before being closed. Independent of the
// per-call timeouts; a generous default reduces reconnect churn for
// chatty connections.
const idleConnectionTimeout = 90 * time.Second

// maxIdleConnections caps the per-host pool of reusable keep-alive
// sockets. Modest because each connection's typical workload is
// occasional fan-out from MCP tool calls, not high-throughput.
const maxIdleConnections = 10

// NewHTTPClient builds the per-connection *http.Client: the call
// timeout as the client deadline, and the connection's transport.
//
// Redirects are explicitly disallowed so a kind does not blindly
// re-issue a request (and re-attach the connection's credential) to a
// host the operator did not authorize. The model can follow a redirect
// manually by reading the upstream Location header from the response
// and issuing a new call with the redirected URL.
//
// Every request the client sends carries the platform's User-Agent
// unless the request already names one (useragent.Transport), so a
// kind's tool call, a page of a walk and a schema introspection all
// present the product rather than Go's default, which a web application
// firewall refuses (#1679). The wrapper sits directly over the
// *http.Transport; metrics wrapping is applied by the caller rather
// than here so test helpers can construct a bare client without
// threading a metrics handle through every call site.
//
// TLS-config build errors are intentionally not surfaced from this
// constructor. Validation has already checked cert + key + CA bundle,
// so BuildTLSConfig only fails here if a caller has constructed a
// Config by hand and bypassed it. The fallback returns a transport
// with the system default tls.Config and the first outbound call will
// fail loudly with the underlying tls error, which is the same surface
// a misconfigured transport would produce on any other auth mode.
func NewHTTPClient(cfg Config) *http.Client {
	return &http.Client{
		Timeout:   cfg.CallTimeout,
		Transport: useragent.Transport(NewHTTPTransport(cfg)),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// NewHTTPTransport builds the per-connection http.Transport. The
// dial step (TCP + TLS handshake) is bound by cfg.ConnectTimeout so
// an unreachable upstream fails fast instead of consuming the full
// CallTimeout budget. Exposed separately from NewHTTPClient so unit
// tests can verify the wiring without standing up a network listener.
//
// When the connection carries mTLS material (cfg.MTLSClientCertPEM
// + cfg.MTLSClientKeyPEM) or a custom CA bundle (cfg.TLSCABundlePEM),
// the transport's TLSClientConfig is populated accordingly. With
// neither set, TLSClientConfig stays nil and Go's net/http uses
// system defaults. BuildTLSConfig errors here are degraded to nil
// (see NewHTTPClient for the rationale).
func NewHTTPTransport(cfg Config) *http.Transport {
	t := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: cfg.ConnectTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       idleConnectionTimeout,
		MaxIdleConns:          maxIdleConnections,
	}
	if tlsCfg, err := cfg.BuildTLSConfig(); err == nil && tlsCfg != nil {
		t.TLSClientConfig = tlsCfg
	}
	return t
}

// AuthHeader returns the canonical header name this connection's auth
// mode would set, so ValidateCustomHeaders can reject the model's
// attempts to spoof or override it. Empty string means no header-based
// auth (mode=none, mode=api_key with query placement).
func (c Config) AuthHeader() string {
	switch c.AuthMode {
	case AuthModeBearer:
		return AuthorizationHeader
	case AuthModeAPIKey:
		if c.CredentialPlacement == CredentialPlacementHeader {
			return c.APIKeyHeader
		}
	}
	return ""
}

// ValidateCustomHeaders refuses model-supplied headers that would
// collide with a credential or with an operator-pinned static header.
// The model never gets to set Authorization, the header its own
// connection's auth mode owns, or any name the operator fixed in
// static_headers: those are the operator's to decide, and a header the
// model set would either be silently overwritten at request build time
// or, worse, win.
func (c Config) ValidateCustomHeaders(headers map[string]string) error {
	authHeader := c.AuthHeader()
	for name := range headers {
		if strings.EqualFold(name, AuthorizationHeader) {
			return c.err("Authorization header is reserved; configure auth via connection")
		}
		if authHeader != "" && strings.EqualFold(name, authHeader) {
			return c.errf("%s header is reserved by this connection's auth_mode", authHeader)
		}
		for staticName := range c.StaticHeaders {
			if strings.EqualFold(name, staticName) {
				return c.errf("%s header is reserved by this connection's static_headers", staticName)
			}
		}
	}
	return nil
}

// ValidateStaticHeaders refuses operator config that would collide with
// the auth path or with hop-by-hop headers Go forbids on a request. A
// static header attempting to set Authorization (or the
// auth-mode-reserved header for api_key+header) would silently lose to
// the auth layer at request time — fail loudly here instead.
func (c Config) ValidateStaticHeaders() error {
	if len(c.StaticHeaders) == 0 {
		return nil
	}
	authHeader := c.AuthHeader()
	for name, value := range c.StaticHeaders {
		if err := c.checkStaticHeader(name, value, authHeader); err != nil {
			return err
		}
	}
	return nil
}

// checkStaticHeader is one static header's worth of ValidateStaticHeaders,
// split out so the loop body's branches stay under the cognitive-complexity
// ceiling.
func (c Config) checkStaticHeader(name, value, authHeader string) error {
	if name == "" {
		return c.err("static_headers contains an empty header name")
	}
	if !isValidHeaderName(name) {
		return c.errf("static_headers name %q contains characters not permitted in an HTTP header name", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return c.errf("static_headers[%q] contains CR/LF/NUL — header smuggling vector", name)
	}
	if strings.EqualFold(name, AuthorizationHeader) {
		return c.err("static_headers must not set Authorization; configure auth via auth_mode")
	}
	if authHeader != "" && strings.EqualFold(name, authHeader) {
		return c.errf("static_headers must not set %q — already managed by auth_mode", name)
	}
	if isReservedHopHeader(name) {
		return c.errf("static_headers must not set hop-by-hop or net/http-managed header %q", name)
	}
	return nil
}

// isValidHeaderName matches RFC 7230 token chars. Permissive enough for
// real-world headers (x-goog-user-project, X-Subscription-Key) and
// strict enough to refuse spaces / control chars that would let an
// operator inject CRLF via a header name.
func isValidHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return name != ""
}

// isReservedHopHeader names headers Go's net/http manages on the
// request itself (Host, Content-Length) or that are meaningless on a
// per-call basis (Connection, Transfer-Encoding, Upgrade). Setting
// these from operator config would either be silently overridden or
// break the transport.
func isReservedHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "connection", "transfer-encoding",
		"upgrade", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailer":
		return true
	}
	return false
}

// ReadLimit is the most of a response to read given a configured limit,
// falling back to the default cap when there is none. The one definition
// a buffered call, a page of a walk, and the memory reservation share,
// so the three cannot drift.
func ReadLimit(limit int64) int64 {
	if limit > 0 {
		return limit
	}
	return DefaultMaxResponseBytes
}

// ReadBody reads at most maxBytes of an upstream response, reporting
// whether it was cut short. One extra byte is read so a body exactly at
// the cap is distinguishable from one that overruns it.
func ReadBody(prefix string, r io.Reader, maxBytes int64) (body []byte, truncated bool, err error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	limited := io.LimitReader(r, maxBytes+1)
	read, rerr := io.ReadAll(limited)
	if rerr != nil {
		return nil, false, errf(prefix, "reading response body: %w", rerr)
	}
	if int64(len(read)) > maxBytes {
		return read[:maxBytes], true, nil
	}
	return read, false, nil
}

// ReserveBodyBudget computes the worst-case number of bytes a buffered
// read of this response could hold and tries to reserve them against
// the shared budget. It returns the amount reserved (to be released by
// the caller) and whether the reservation was granted.
//
// When the upstream declares a Content-Length below the read cap, only
// that many bytes are reserved so small (and empty) responses do not
// each tie up the full per-request cap and falsely exhaust the budget.
// This is safe because Go's HTTP client bounds resp.Body to the declared
// Content-Length — a server that writes more than it declared cannot
// make ReadBody buffer beyond it. Unknown/chunked responses
// (ContentLength < 0) and over-cap responses reserve the full cap, which
// is exactly what ReadBody may buffer. A nil/disabled budget always
// grants the reservation and Release is a no-op, so the buffered path is
// unchanged when no budget is configured.
func ReserveBodyBudget(b *membudget.Budget, contentLength, readCap int64) (reserved int64, ok bool) {
	reserved = readCap
	if contentLength >= 0 && contentLength < readCap {
		reserved = contentLength
	}
	return reserved, b.Acquire(reserved)
}

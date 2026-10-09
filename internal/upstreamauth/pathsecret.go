package upstreamauth

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// cfgKeyPathSecret holds a secret the receiver reads from the request path,
// such as a chat incoming-webhook URL's trailing token or an inbound source's
// path_token (#1996). Encrypted at rest and read back as "[REDACTED]", unlike
// base_url, which is returned as written.
const cfgKeyPathSecret = "path_secret" // #nosec G101 -- map key, not a credential

// ValidatePathSecret refuses a path secret that would not be sent as path
// segments.
func (c Config) ValidatePathSecret() error {
	if strings.ContainsAny(c.PathSecret, "?#\r\n\x00 ") {
		return c.errf("%s must be path segments: no spaces, ?, # or control characters", cfgKeyPathSecret)
	}
	return nil
}

// pathSecretTransport appends the connection's path secret to every request's
// path as it is sent. It works on a copy: the caller's request keeps the path
// without the secret, which is the URL net/http's client writes into any error
// it returns, so a dial failure cannot quote the secret to whoever made the
// call. A path secret naming a stored secret (#2066) is read as it is sent.
type pathSecretTransport struct {
	next http.RoundTripper
	cfg  Config
}

// RoundTrip sends a copy of req with the secret appended to its path.
func (t pathSecretTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	secret, err := t.cfg.fill(req.Context(), t.cfg.PathSecret)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(secret, "?#\r\n\x00 ") {
		return nil, t.cfg.errf("the stored secret %s names holds a space, ?, # or a control character, which a path cannot carry", cfgKeyPathSecret)
	}
	out := req.Clone(req.Context())
	u := *req.URL
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + strings.Trim(secret, "/")
	u.RawPath = ""
	out.URL = &u
	return t.next.RoundTrip(out) //nolint:wrapcheck // the transport's own error, as any other round trip returns it
}

// secretHeaderTransport fills the stored secrets named by the headers the
// operator fixed (static_headers) and by the path secret, as each request is
// sent. A caller cannot set a static header's name (ValidateCustomHeaders), so
// every value it fills is one the connection wrote; a header a caller set is
// never touched, and a placeholder a caller wrote reaches the upstream only
// through the gateway's own fill.
type secretHeaderTransport struct {
	next    http.RoundTripper
	cfg     Config
	headers []string
}

// newSecretHeaderTransport wraps next when any static header names a stored
// secret, and returns next otherwise.
func newSecretHeaderTransport(cfg Config, next http.RoundTripper) http.RoundTripper {
	var names []string
	for name, value := range cfg.StaticHeaders {
		if secretref.HasPlaceholder(value) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return next
	}
	return secretHeaderTransport{next: next, cfg: cfg, headers: names}
}

// RoundTrip sends a copy of req with its static headers' secrets filled.
func (t secretHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	for _, name := range t.headers {
		value := out.Header.Get(name)
		if !secretref.HasPlaceholder(value) {
			continue
		}
		filled, err := t.cfg.fill(req.Context(), value)
		if err != nil {
			return nil, err
		}
		out.Header.Set(name, filled)
	}
	return t.next.RoundTrip(out) //nolint:wrapcheck // the transport's own error, as any other round trip returns it
}

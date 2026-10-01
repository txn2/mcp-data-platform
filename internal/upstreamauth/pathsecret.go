package upstreamauth

import (
	"net/http"
	"strings"
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
// call.
type pathSecretTransport struct {
	next   http.RoundTripper
	secret string
}

// RoundTrip sends a copy of req with the secret appended to its path.
func (t pathSecretTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	u := *req.URL
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + strings.Trim(t.secret, "/")
	u.RawPath = ""
	out.URL = &u
	return t.next.RoundTrip(out) //nolint:wrapcheck // the transport's own error, as any other round trip returns it
}

package secretref

import "net/http"

// Transport wraps base so that a response to a request whose context carries
// a Redactor with values in it comes back with them redacted: from every
// header value, and from the body as it is read, however it is read. A
// request that filled no placeholder passes through untouched.
//
// The redacted body is a different length from the one the upstream sent,
// so its declared length is dropped.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return redactingTransport{base: base}
}

// redactingTransport is Transport's RoundTripper.
type redactingTransport struct{ base http.RoundTripper }

// RoundTrip sends req and redacts what comes back.
func (t redactingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	r := FromContext(req.Context())
	if err != nil || r.Empty() {
		return resp, err //nolint:wrapcheck // the base transport's own result
	}
	r.Strings(resp.Header)
	resp.Body = r.Reader(resp.Body)
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	return resp, nil
}

// CloseIdleConnections passes through to the base transport, which is how
// http.Client.CloseIdleConnections reaches it: a session-login connection
// signs out of its upstream there.
func (t redactingTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// Unwrap is the base transport.
func (t redactingTransport) Unwrap() http.RoundTripper { return t.base }

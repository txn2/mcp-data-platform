package useragent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/buildinfo"
)

// TestProductNamesThePlatformAndItsVersion pins the wire form: a product
// token an access log and a WAF rule can match on, with the build's
// version after the slash.
func TestProductNamesThePlatformAndItsVersion(t *testing.T) {
	if got, want := Product(), "mcp-data-platform/"+buildinfo.Version; got != want {
		t.Errorf("Product() = %q; want %q", got, want)
	}
	if strings.HasPrefix(Product(), "Go-http-client") {
		t.Error("the product User-Agent must not be Go's default")
	}
}

// TestEffectiveKeepsAValueTheRequestCarries is the rule in isolation: a
// header already on the request is what is sent, and an absent one
// resolves to the product.
func TestEffectiveKeepsAValueTheRequestCarries(t *testing.T) {
	h := http.Header{}
	if got := Effective(h); got != Product() {
		t.Errorf("Effective(empty) = %q; want %q", got, Product())
	}
	h.Set(Header, "operator-pinned/2")
	if got := Effective(h); got != "operator-pinned/2" {
		t.Errorf("Effective(pinned) = %q; want the pinned value", got)
	}
}

// echoUserAgent stands up a server that answers with the User-Agent it
// received, so what the transport put on the wire is read where it
// landed.
func echoUserAgent(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get(Header)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fetch sends req through client and returns the body the upstream
// answered with.
func fetch(t *testing.T, client *http.Client, req *http.Request) string {
	t.Helper()
	resp, err := client.Do(req) //nolint:gosec // test code; the URL is the httptest server the test started
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(body)
}

// TestTransportSetsTheProductWhenTheRequestNamesNone is the defect: a
// request built without a User-Agent must reach the upstream with the
// product's, not Go's.
func TestTransportSetsTheProductWhenTheRequestNamesNone(t *testing.T) {
	srv := echoUserAgent(t)
	client := &http.Client{Transport: Transport(nil)}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if got := fetch(t, client, req); got != Product() {
		t.Errorf("upstream saw User-Agent %q; want %q", got, Product())
	}
	if req.Header.Get(Header) != "" {
		t.Error("the caller's request was modified; the transport must send a clone")
	}
}

// TestTransportLeavesAValueTheRequestCarries is the override: a header
// the operator pinned or a caller supplied wins over the product.
func TestTransportLeavesAValueTheRequestCarries(t *testing.T) {
	srv := echoUserAgent(t)
	client := &http.Client{Transport: Transport(http.DefaultTransport)}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(Header, "pinned/1")
	if got := fetch(t, client, req); got != "pinned/1" {
		t.Errorf("upstream saw User-Agent %q; want the pinned value", got)
	}
}

// idleCloser records whether CloseIdleConnections reached it.
type idleCloser struct {
	http.RoundTripper
	closed bool
}

func (c *idleCloser) CloseIdleConnections() { c.closed = true }

// TestTransportForwardsCloseIdleConnections keeps a reloaded connection
// releasing its sockets: http.Client.CloseIdleConnections reaches the
// pool only if the wrapper forwards it.
func TestTransportForwardsCloseIdleConnections(t *testing.T) {
	base := &idleCloser{RoundTripper: http.DefaultTransport}
	client := &http.Client{Transport: Transport(base)}
	client.CloseIdleConnections()
	if !base.closed {
		t.Error("CloseIdleConnections did not reach the wrapped transport")
	}
	// A base without a pool is not an error.
	(&http.Client{Transport: Transport(http.RoundTripper(roundTripFunc(nil)))}).CloseIdleConnections()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestWrapsReturnsTheTransportBeneath is what a test of the dial or TLS
// wiring uses to reach the *http.Transport under the wrapper.
func TestWrapsReturnsTheTransportBeneath(t *testing.T) {
	base := &http.Transport{}
	got, ok := Wraps(Transport(base))
	if !ok || got != base {
		t.Errorf("Wraps(Transport(base)) = %v, %v; want base, true", got, ok)
	}
	if _, ok := Wraps(base); ok {
		t.Error("Wraps reported a bare *http.Transport as wrapped")
	}
}

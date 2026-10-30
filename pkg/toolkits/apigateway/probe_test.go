package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// probeToolkit serves one connection pointed at the given base URL.
func probeToolkit(t *testing.T, baseURL string) *Toolkit {
	t.Helper()
	tk := NewMulti(MultiConfig{})
	if err := tk.AddConnection("vendor", map[string]any{
		"base_url": baseURL, "connect_timeout": "5s", "call_timeout": "10s",
	}); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

// Any status the upstream answers with proves the route and the credential, so
// a 404 from its router is as good an answer as a 200: the probe reports the
// status rather than judging it (#1805).
func TestProbeConnection_UpstreamAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	res := probeToolkit(t, srv.URL).ProbeConnection(context.Background(), "vendor")

	if !res.OK {
		t.Fatalf("an answered request was reported as a failure: %+v", res)
	}
	if !strings.Contains(res.Detail, "HTTP 404") {
		t.Errorf("detail does not report the status: %q", res.Detail)
	}
}

// 401 is the upstream not accepting the credential wherever it answers, so it
// is a failure: a connection whose upstream rejects its credential is exactly
// what an operator is asking about (#1805).
func TestProbeConnection_CredentialRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	res := probeToolkit(t, srv.URL).ProbeConnection(context.Background(), "vendor")

	if res.OK {
		t.Error("HTTP 401 was reported as healthy")
	}
	if !strings.Contains(res.Detail, "rejecting this connection's credential") {
		t.Errorf("detail does not name the credential: %q", res.Detail)
	}
}

// A 403 at a base the catalog does not declare is a key scoped away from "/",
// not a refused credential (#2368): the test says the credential cannot reach
// "/" and does not report a working connection as broken.
func TestProbeConnection_ForbiddenAtAnUndeclaredBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	res := probeToolkit(t, srv.URL).ProbeConnection(context.Background(), "vendor")

	if !res.OK {
		t.Fatalf("a 403 at an undeclared base was reported as a failure: %+v", res)
	}
	if !strings.Contains(res.Detail, "the credential cannot reach /") || strings.Contains(res.Detail, "rejecting") {
		t.Errorf("detail = %q, want it to say the credential cannot reach /", res.Detail)
	}
}

// probeSpec declares one plain read, one that needs a path parameter, one
// with a required query parameter, and a write.
const probeSpec = `openapi: 3.0.0
info: {title: cams, version: "1"}
paths:
  /cameras/v1/devices:
    get: {responses: {"200": {description: ok}}}
  /search:
    get:
      parameters: [{name: q, in: query, required: true, schema: {type: string}}]
      responses: {"200": {description: ok}}
  /items/{id}:
    get:
      parameters: [{name: id, in: path, required: true, schema: {type: string}}]
      responses: {"200": {description: ok}}
  /a:
    post: {responses: {"200": {description: ok}}}
`

// With a catalog, the probe asks for a read operation it declares, one with no
// parameter to fill (#2368), and a 403 there is the credential refused on a
// route it is meant to reach.
func TestProbeConnection_ProbesACatalogedRead(t *testing.T) {
	for _, tc := range []struct {
		status int
		ok     bool
		want   string
	}{
		{http.StatusOK, true, "the operation is one the connection's catalog declares"},
		{http.StatusForbidden, false, "rejecting this connection's credential on an operation its catalog declares"},
	} {
		var asked string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked = r.Method + " " + r.URL.Path
			w.WriteHeader(tc.status)
		}))
		tk := probeToolkit(t, srv.URL)
		c, _ := tk.lookup("vendor")
		c.specs = map[string]*specState{"main": mustParseSpec(t, probeSpec)}

		res := tk.ProbeConnection(context.Background(), "vendor")
		srv.Close()
		if asked != "GET /cameras/v1/devices" {
			t.Errorf("HTTP %d: the probe asked for %q, want the declared read with no parameter", tc.status, asked)
		}
		if res.OK != tc.ok || !strings.Contains(res.Detail, tc.want) {
			t.Errorf("HTTP %d: %+v, want ok=%v and %q", tc.status, res, tc.ok, tc.want)
		}
	}
}

// A catalog whose reads all need a parameter leaves the probe at the base.
func TestProbeOperation_NothingCallableFallsBackToTheBase(t *testing.T) {
	spec := mustParseSpec(t, `openapi: 3.0.0
info: {title: x, version: "1"}
paths:
  /items/{id}:
    get: {parameters: [{name: id, in: path, required: true, schema: {type: string}}], responses: {"200": {description: ok}}}
  /search:
    parameters: [{name: q, in: query, required: true, schema: {type: string}}]
    get: {responses: {"200": {description: ok}}}
`)
	if path, ok := probeOperation(map[string]*specState{"main": spec, "empty": nil}); ok || path != "/" {
		t.Errorf("probeOperation = %q, %v; want the base", path, ok)
	}
	spec.effectiveBasePath = "/api"
	spec.doc.Paths.Set("/z", &openapi3.PathItem{Get: &openapi3.Operation{}})
	spec.doc.Paths.Set("/b/c", &openapi3.PathItem{Get: &openapi3.Operation{}})
	if path, ok := probeOperation(map[string]*specState{"main": spec}); !ok || path != "/api/z" {
		t.Errorf("probeOperation = %q, %v; want the shortest declared read under the base path", path, ok)
	}
}

// An upstream that cannot be reached carries the transport's own error.
func TestProbeConnection_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tk := probeToolkit(t, srv.URL)
	srv.Close()

	res := tk.ProbeConnection(context.Background(), "vendor")

	if res.OK {
		t.Fatal("a closed upstream was reported as healthy")
	}
	if res.Error == "" {
		t.Error("the failure carries no error")
	}
}

// A name this toolkit does not serve is an answer about that name.
func TestProbeConnection_UnknownConnection(t *testing.T) {
	res := NewMulti(MultiConfig{}).ProbeConnection(context.Background(), "nope")

	if res.OK {
		t.Fatal("an unknown connection was reported as healthy")
	}
	if !strings.Contains(res.Detail, "not served") {
		t.Errorf("detail does not say the connection is unknown: %q", res.Detail)
	}
}

// The built-in connection resolved by an in-process handler has a synthetic
// base URL and no upstream, so dialing it would report the platform's own
// connection as unreachable.
func TestProbeConnection_InternalHandler(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetInternalHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err := tk.AddConnection("util", map[string]any{
		"handler": HandlerInternal, "connect_timeout": "5s", "call_timeout": "10s",
	}); err != nil {
		t.Fatalf("adding the internal connection: %v", err)
	}

	res := tk.ProbeConnection(context.Background(), "util")

	if !res.OK {
		t.Fatalf("the platform's own connection was reported as broken: %+v", res)
	}
	if !strings.Contains(res.Detail, "no upstream to reach") {
		t.Errorf("detail does not say why nothing was dialed: %q", res.Detail)
	}
}

// A connection that sends its api_key in the query string is tested against a
// closed upstream: the failure names the address, never the key (#2358).
func TestProbeConnection_UnreachableNamesNoQueryKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tk := NewMulti(MultiConfig{})
	if err := tk.AddConnection("vendor", map[string]any{
		"base_url": srv.URL, "connect_timeout": "5s", "call_timeout": "10s",
		"auth_mode": "api_key", "credential": "k3y-2358-probe", "api_key_placement": "query", "api_key_param": "key",
	}); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	srv.Close()

	res := tk.ProbeConnection(context.Background(), "vendor")
	if res.OK {
		t.Fatal("a closed upstream was reported as healthy")
	}
	if strings.Contains(res.Error, "k3y-2358-probe") || strings.Contains(res.Detail, "k3y-2358-probe") {
		t.Errorf("the failure carries the key: %+v", res)
	}
	if !strings.Contains(res.Error, "key=REDACTED") {
		t.Errorf("error = %q, want the parameter named with its value replaced", res.Error)
	}
}

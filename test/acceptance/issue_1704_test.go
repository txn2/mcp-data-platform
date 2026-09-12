//go:build integration

package acceptance

// Issue #1704: POST .../refresh-schema answered HTTP 502 for an endpoint that
// refuses introspection, the CDN in front of the deployment replaced the body
// of that 502 with its own, and the Schema card's Re-read button printed
// "Request failed with status 502". Other routes the portal calls answered 502
// or 504 for an upstream failure and lost their body the same way.
//
// Each criterion reaches the platform through a forwarder the test starts in
// front of it that does what the ticket's CDN did, as Cloudflare documents it:
// an origin 502 or 504 is answered with the CDN's own `error code: <status>`
// text body, and every other response is passed through unchanged. What a
// browser behind that CDN receives is what each criterion reads.
//
// Wire forms: `POST .../refresh-schema` takes an empty body (a re-read), SDL,
// or a saved introspection result; all three are sent, and a body that does
// not parse
// (TestIssue1704_TheSchemaRefreshRouteReachesABrowserBehindACDN).
// `POST /api/v1/admin/gateway/connections/{name}/test` takes one body shape,
// `{"config": {...}}`
// (TestIssue1704_AGatewayTestDialThatFailsReachesABrowserBehindACDN).

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// issue1704CDN starts the forwarder in front of the platform one client talks
// to and returns a client whose REST calls go through it.
func issue1704CDN(t *testing.T, c *client) *client {
	t.Helper()
	target, err := url.Parse(c.base)
	if err != nil {
		t.Fatalf("parsing %s: %v", c.base, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(res *http.Response) error {
		if res.StatusCode != http.StatusBadGateway && res.StatusCode != http.StatusGatewayTimeout {
			return nil
		}
		_ = res.Body.Close() //nolint:errcheck // the origin body is what the CDN discards
		body := fmt.Sprintf("error code: %d", res.StatusCode)
		res.Body = io.NopCloser(strings.NewReader(body))
		res.ContentLength = int64(len(body))
		res.Header = http.Header{}
		res.Header.Set("Content-Type", "text/plain; charset=UTF-8")
		res.Header.Set("Content-Length", strconv.Itoa(len(body)))
		res.Header.Set("Server", "cloudflare")
		return nil
	}
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)
	behind := *c
	behind.base = srv.URL
	return &behind
}

// issue1704Raw issues a REST call and returns the status, content type and
// body as received, for a criterion about what reached the caller rather than
// what decodes.
func issue1704Raw(t *testing.T, c *client, method, path string, body []byte) (status int, contentType, raw string) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the body: %v", method, path, err)
	}
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

// TestIssue1704_TheCDNModelReplacesA502Body is the control: the forwarder does
// to a 502 what the ticket's CDN did, so a criterion that passes through it is
// a criterion a real 502 would have failed.
func TestIssue1704_TheCDNModelReplacesA502Body(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"detail":"graphql: the endpoint answered HTTP 302 to the introspection query: "}`)
	}))
	t.Cleanup(origin.Close)
	cdn := issue1704CDN(t, &client{t: t, ctx: t.Context(), base: origin.URL})
	status, contentType, raw := issue1704Raw(t, cdn, http.MethodPost, "/", nil)
	if status != http.StatusBadGateway || !strings.HasPrefix(contentType, "text/plain") || raw != "error code: 502" {
		t.Fatalf("the CDN model passed a 502 through: HTTP %d %s %q", status, contentType, raw)
	}
}

// TestIssue1704_TheSchemaRefreshRouteReachesABrowserBehindACDN is the ticket's
// first two expectations: a refused re-read reaches the browser as the state
// GET .../schema answers, 200 with `error` filled, and the upstream's sentence
// is in it. Every form the route takes is sent through the CDN.
func TestIssue1704_TheSchemaRefreshRouteReachesABrowserBehindACDN(t *testing.T) {
	a := connect(t)
	cdn := issue1704CDN(t, a)
	name, _ := issue1676Connect(t, a, "cdn", issue1676Upstream(t))
	path := "/api/v1/admin/connection-instances/graphql/" + name + "/refresh-schema"

	// A re-read on a connection that holds no schema: the refusal is the state.
	status, info := cdn.rest(http.MethodPost, path, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("a refused re-read on a connection with no schema answered HTTP %d: %v", status, info)
	}
	issue1676AssertNoSchema(t, "re-read with no schema", info)

	// Both upload forms install the schema through the CDN.
	var hash string
	var count float64
	for form, payload := range issue1703Forms {
		status, info = cdn.rest(http.MethodPost, path, strings.NewReader(payload))
		if status != http.StatusOK {
			t.Fatalf("uploading the %s form answered HTTP %d: %v", form, status, info)
		}
		_, hash, _, count = issue1676Fields(info)
		issue1676AssertUpload(t, form+" upload", info, hash, count, false)
	}

	// A re-read beside the held schema: 200, the schema, and the sentence.
	status, contentType, raw := issue1704Raw(t, cdn, http.MethodPost, path, nil)
	if status != http.StatusOK || contentType != "application/json" {
		t.Fatalf("a refused re-read answered HTTP %d %s: %s", status, contentType, raw)
	}
	if !strings.Contains(raw, "HTTP 302 to the introspection query") {
		t.Errorf("the upstream's sentence did not reach the browser: %s", raw)
	}
	status, reread := cdn.rest(http.MethodPost, path, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("a refused re-read answered HTTP %d: %v", status, reread)
	}
	issue1676AssertUpload(t, "re-read beside the upload", reread, hash, count, true)
	if got := issue1676Schema(t, cdn, name); got["error"] != reread["error"] || got["schema_hash"] != reread["schema_hash"] {
		t.Errorf("the refresh answered %v and GET .../schema answers %v; one state, one shape", reread, got)
	}

	// A body that does not parse is the caller's input: a 400 with its detail.
	status, contentType, raw = issue1704Raw(t, cdn, http.MethodPost, path, []byte("type Query {"))
	if status != http.StatusBadRequest || contentType != "application/problem+json" || !strings.Contains(raw, "detail") {
		t.Errorf("an unparseable upload answered HTTP %d %s: %s", status, contentType, raw)
	}
}

// TestIssue1704_AGatewayTestDialThatFailsReachesABrowserBehindACDN is the
// ticket's third expectation on a route it did not name: Admin > Connections'
// Test button on an MCP gateway connection answered 502 with its result in the
// body, which the CDN replaced. The dial goes to a port nothing listens on, so
// the failure is a real one and not a response the test wrote.
func TestIssue1704_AGatewayTestDialThatFailsReachesABrowserBehindACDN(t *testing.T) {
	a := connect(t)
	cdn := issue1704CDN(t, a)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	closed := listener.Addr().String()
	_ = listener.Close() //nolint:errcheck // the port is wanted closed

	name := fmt.Sprintf("acc-1704-dial-%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{"config":{"endpoint":"http://%s/mcp","connect_timeout":"2s","call_timeout":"5s"}}`, closed))
	status, contentType, raw := issue1704Raw(t, cdn, http.MethodPost,
		"/api/v1/admin/gateway/connections/"+name+"/test", body)

	if status != http.StatusServiceUnavailable {
		t.Errorf("a failed dial answered HTTP %d; want 503: %s", status, raw)
	}
	if contentType != "application/json" || !strings.Contains(raw, `"healthy":false`) || !strings.Contains(raw, `"error":"`) {
		t.Errorf("the dial's result did not reach the browser: %s %s", contentType, raw)
	}
	if strings.Contains(raw, "error code:") {
		t.Errorf("the CDN replaced the body: %s", raw)
	}
}

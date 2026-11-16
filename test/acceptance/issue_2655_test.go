//go:build integration

package acceptance

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

// Acceptance for #2655: an endpoint's answer is sent brotli or gzip when the
// client accepts it, by one rule for api mode and page mode, and an answer
// that may hold one of the platform's tokens (here a platform.mint_token
// value) is sent as it is, so its size never tells an observer how well a
// guess matched the token (BREACH).
//
// Wire forms: handler_set kind=endpoint takes endpoint, path, auth_mode and
// response_mode as strings, methods and callers as JSON arrays of strings;
// the endpoint requests are GET and HEAD with query strings and an
// Accept-Encoding header of "br, gzip", "gzip" and none.

// handler2655 answers a schedule of about 3 KB of JSON, and by ?kind a small
// body, an image, a body it encoded itself, or a 204.
const handler2655 = `CASES = {
    "small": {"json": {"ok": True}},
    "image": {"headers": {"Content-Type": "image/png"}, "body": "x" * 4000},
    "encoded": {"headers": {"Content-Type": "application/json", "Content-Encoding": "identity"}, "body": "{" + " " * 4000 + "}"},
    "empty": {"status": 204},
}

def schedule():
    """The day's schedule, about 3 KB of JSON."""
    return {"json": {"slots": [{"time": "09:%02d" % (i % 60), "room": "Hall A", "talk": "Session " + str(i)} for i in range(60)]}}

def handle(req):
    """Answers the schedule, or the case ?kind names."""
    return CASES.get(req["query"].get("kind")) or schedule()
`

// get2655 requests path on the endpoint with accept as its Accept-Encoding.
func get2655(t *testing.T, c *client, method, path, accept string) endpointResponse {
	t.Helper()
	h := bearer(c.apiKey)
	if accept != "" {
		h.Set("Accept-Encoding", accept)
	}
	return endpointRequest(t, c.base, method, path, h, "")
}

// decoded is a response's body as the bytes it encodes.
func decoded2655(t *testing.T, r endpointResponse) string {
	t.Helper()
	var rd io.Reader = strings.NewReader(r.body)
	switch r.header.Get("Content-Encoding") {
	case "br":
		rd = brotli.NewReader(rd)
	case "gzip":
		z, err := gzip.NewReader(rd)
		if err != nil {
			t.Fatalf("a gzip answer did not open: %v", err)
		}
		rd = z
	}
	out, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	return string(out)
}

func endpoint2655(t *testing.T, c *client, source string) string {
	t.Helper()
	name := "acc-2655-" + unique1579()
	saveEndpointScript(t, c, name, source, `{"query": {}, "params": {}, "caller": None}`)
	bindEndpoint(t, c, name, "", map[string]any{"endpoint": name, "auth_mode": "authenticated", "callers": []string{"*"}, "methods": []string{"GET", "HEAD"}})
	return name
}

// TestIssue2655_AnAnswerIsSentInTheFormTheClientAccepts: brotli for br, gzip
// for gzip alone, identity for none, Vary on each, and the decoded body is
// the identity body byte for byte.
func TestIssue2655_AnAnswerIsSentInTheFormTheClientAccepts(t *testing.T) {
	c := connect(t)
	name := endpoint2655(t, c, handler2655)
	plain := get2655(t, c, http.MethodGet, "/endpoints/"+name, "")
	if plain.status != http.StatusOK || plain.header.Get("Content-Encoding") != "" || len(plain.body) < 1024 {
		t.Fatalf("the identity answer: %d %q, %d bytes", plain.status, plain.header.Get("Content-Encoding"), len(plain.body))
	}
	for accept, want := range map[string]string{"br, gzip": "br", "gzip": "gzip"} {
		got := get2655(t, c, http.MethodGet, "/endpoints/"+name, accept)
		if got.header.Get("Content-Encoding") != want || !strings.Contains(got.header.Get("Vary"), "Accept-Encoding") {
			t.Errorf("Accept-Encoding %q: Content-Encoding %q, Vary %q", accept, got.header.Get("Content-Encoding"), got.header.Get("Vary"))
			continue
		}
		if len(got.body) >= len(plain.body) {
			t.Errorf("Accept-Encoding %q: %d bytes is no smaller than %d", accept, len(got.body), len(plain.body))
		}
		if decoded2655(t, got) != plain.body {
			t.Errorf("Accept-Encoding %q: the decoded body differs from the identity answer", accept)
		}
	}
}

// TestIssue2655_WhatIsNotCompressed: a HEAD, a 204, a small body, an image and
// a body the handler encoded itself are sent as they are.
func TestIssue2655_WhatIsNotCompressed(t *testing.T) {
	c := connect(t)
	name := endpoint2655(t, c, handler2655)
	for label, path := range map[string]string{
		"small": "?kind=small", "an image": "?kind=image", "a 204": "?kind=empty",
	} {
		got := get2655(t, c, http.MethodGet, "/endpoints/"+name+path, "br, gzip")
		if got.header.Get("Content-Encoding") != "" {
			t.Errorf("%s was sent %q", label, got.header.Get("Content-Encoding"))
		}
	}
	if got := get2655(t, c, http.MethodGet, "/endpoints/"+name+"?kind=encoded", "br, gzip"); got.header.Get("Content-Encoding") != "identity" {
		t.Errorf("a body the handler encoded was re-encoded: %q", got.header.Get("Content-Encoding"))
	}
	if got := get2655(t, c, http.MethodHead, "/endpoints/"+name, "br, gzip"); got.header.Get("Content-Encoding") != "" || got.body != "" {
		t.Errorf("a HEAD was sent %q with %d bytes", got.header.Get("Content-Encoding"), len(got.body))
	}
}

// TestIssue2655_AnAnswerHoldingATokenIsNeverCompressed: an answer after the
// run called platform.mint_token is sent as it is to a client accepting
// brotli, and carries the token as written.
func TestIssue2655_AnAnswerHoldingATokenIsNeverCompressed(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{"*"})
	name := "acc-2655-mint-" + unique1579()
	issue2175Endpoint(t, admin, name, secret)
	q := url.Values{"applicant": {strings.Repeat("a-17 ", 400)}}
	got := get2655(t, admin, http.MethodGet, "/endpoints/"+name+"?"+q.Encode(), "br, gzip")
	if got.status != http.StatusOK {
		t.Fatalf("the mint answered %d: %s", got.status, got.body)
	}
	if got.header.Get("Content-Encoding") != "" {
		t.Errorf("an answer holding a minted token was sent %q", got.header.Get("Content-Encoding"))
	}
	if !strings.Contains(got.body, `"token":"`) {
		t.Errorf("the answer does not carry the token as written: %.200s", got.body)
	}
}

// TestIssue2655_APageIsCompressedByTheSameRule: a page-mode endpoint on an
// application's page origin is sent brotli to a client accepting it.
func TestIssue2655_APageIsCompressedByTheSameRule(t *testing.T) {
	admin := connect(t)
	name := "acc2655-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2655"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	saveEndpointScript(t, admin, name, `def handle(req):
    """Answers a long page."""
    return {"headers": {"Content-Type": "text/html; charset=utf-8"}, "body": "<ul>" + "<li>Session</li>" * 400 + "</ul>"}
`, `{"params": {}, "caller": None}`)
	addScriptPart2517(t, admin, name, name)
	bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "methods": []string{"GET"}, "path": "/", "auth_mode": "public", "callers": []string{}, "response_mode": "page",
	})
	admin.call("manage_application", map[string]any{"command": "release", "application": name, "note": "first"})
	got := onHost(t, name+"."+pagesDomain2517, "GET", "/", map[string]string{"Accept-Encoding": "br"}, "")
	if got.status != http.StatusOK || got.header.Get("Content-Encoding") != "br" {
		t.Fatalf("the page answered %d with Content-Encoding %q", got.status, got.header.Get("Content-Encoding"))
	}
	body, err := io.ReadAll(brotli.NewReader(bytes.NewReader([]byte(got.body))))
	if err != nil || !strings.Contains(string(body), "<li>Session</li>") {
		t.Errorf("the page does not decode to what the handler answered: %v", err)
	}
}

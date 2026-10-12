//go:build integration

package acceptance

import (
	"net/http"
	"testing"
)

// Acceptance for #2108: a source created on one replica is served by every
// replica from its first request, even when requests for other names the
// replica does not hold arrive in the same second. The dev stack's proxy
// spreads requests over two replicas. Every request to /hooks/{source} is raw
// HTTP with Content-Type application/json and application/x-www-form-urlencoded.

// TestIssue2108_NewSourcesAreServedFromTheirFirstRequest creates sources back
// to back and posts to each straight after its creation, in both body forms:
// every one is answered by its authentication (401 for a forged signature,
// 202 for a signed one), never 404. A name no source has is still 404.
func TestIssue2108_NewSourcesAreServedFromTheirFirstRequest(t *testing.T) {
	c := connect(t)
	const secret = "whsec_2108"
	for i := range 6 {
		name := issue1870Name("acc2108")
		issue1870Create(t, c, issue1870HMAC(name, secret, map[string]any{"event_id_path": "$.id"}))
		f := issue1870Forms[i%len(issue1870Forms)]
		forged := f.encode(`{"id":"forged"}`)
		res, text := issue1870Post(t, baseURL(), name, f.contentType, forged,
			map[string]string{"X-Signature": issue1870Sign("not-the-secret", forged)})
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("source %d's first request (%s) was answered %d, want 401: %s", i, f.name, res.StatusCode, text)
		}
		res, text = issue1870Signed(t, name, secret, f.contentType, f.encode(`{"id":"signed"}`))
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("source %d's signed request (%s) was answered %d, want 202: %s", i, f.name, res.StatusCode, text)
		}
	}
	if res, text := issue1870Post(t, baseURL(), "no-such-source-2108", "application/json", []byte(`{}`), nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("a name no source has was answered %d: %s", res.StatusCode, text)
	}
}

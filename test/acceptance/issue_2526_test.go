//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"testing"
)

// Acceptance for #2526: the platform answers a cross-origin request from any
// origin, and allows credentials only for its own. A page on another origin,
// whose requests the browser sends with the session cookie when it is on the
// same site, is answered without Access-Control-Allow-Credentials, so the
// browser withholds what the cookie unlocked from it. A client on another
// origin calling with an API key is still answered.
//
// Wire forms: HTTP requests with the Origin header a browser sends, for a
// read (GET) and its preflight (OPTIONS); the API key in X-API-Key and as a
// bearer token.

// TestIssue2526_OnlyThePlatformsOwnOriginGetsCredentials: a foreign origin,
// a sibling host on the platform's own domain and a sandboxed frame get no
// credentials on a portal read or its preflight; the platform's own origin
// does.
func TestIssue2526_OnlyThePlatformsOwnOriginGetsCredentials(t *testing.T) {
	admin := connect(t)
	own, err := url.Parse(admin.base)
	if err != nil {
		t.Fatalf("parsing %s: %v", admin.base, err)
	}
	cases := []struct {
		origin      string
		credentials bool
	}{
		{own.Scheme + "://" + own.Host, true},
		{"https://evil.example", false},
		{"http://shop.apps." + own.Hostname() + ".test", false},
		{"null", false},
	}
	for _, tc := range cases {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			header := http.Header{"Origin": {tc.origin}}
			if method == http.MethodOptions {
				header.Set("Access-Control-Request-Method", http.MethodGet)
			}
			got := endpointRequest(t, admin.base, method, "/api/v1/portal/me", header, "")
			if has := got.header.Get("Access-Control-Allow-Credentials") == "true"; has != tc.credentials {
				t.Errorf("%s /api/v1/portal/me from %s: credentials = %v, want %v (%v)", method, tc.origin, has, tc.credentials, got.header)
			}
		}
	}
}

// TestIssue2526_AnAPIKeyClientOnAnotherOriginIsStillAnswered: a browser
// client on another origin that authenticates with an API key gets its
// answer and its origin back, in both forms the key is sent in.
func TestIssue2526_AnAPIKeyClientOnAnotherOriginIsStillAnswered(t *testing.T) {
	admin := connect(t)
	origin := "https://client.example"
	for name, header := range map[string]http.Header{
		"X-API-Key": {"Origin": {origin}, "X-API-Key": {admin.apiKey}},
		"bearer":    {"Origin": {origin}, "Authorization": {"Bearer " + admin.apiKey}},
	} {
		got := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/me", header, "")
		if got.status != http.StatusOK {
			t.Errorf("%s from %s: %d %.200s", name, origin, got.status, got.body)
		}
		if a := got.header.Get("Access-Control-Allow-Origin"); a != origin {
			t.Errorf("%s from %s: Allow-Origin = %q, want the origin", name, origin, a)
		}
	}
}

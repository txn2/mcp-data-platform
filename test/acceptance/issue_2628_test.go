//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2628: on the platform's own address, a request to an
// endpoint whose path holds an empty or a dot segment is answered by the
// endpoint's router with the documented 404, not redirected to the cleaned
// path where the handler would then run. A clean path still reaches the
// handler.
//
// Wire forms: the requests are GETs with the path sent as written, a
// literal "//", "/./" and percent-encoded "%2e%2e" segment included, and no
// redirect followed.

const issue2628Echo = `def handle(req):
    """Answers with the route it was asked for."""
    return {"json": {"route": req["params"].get("route", "")}}
`

func TestIssue2628_AnEmptyOrDotSegmentIsA404OnThePlatformsAddress(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2628")
	saveEndpointScript(t, admin, name, issue2628Echo, `{"params": {"route": "a/b"}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "methods": []string{"GET"}, "path": "/{route...}", "auth_mode": "public", "callers": []string{},
	})
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, path := range []string{"/a//b", "/a/./b", "/a/%2e%2e/b", "/a/b//"} {
		got := endpointRequestWith(t, noRedirect, admin.base, http.MethodGet, "/endpoints/"+name+path, nil, "")
		if got.status != http.StatusNotFound || got.header.Get("Location") != "" {
			t.Errorf("GET /endpoints/%s%s: %d (Location %q) %s; want 404 and no redirect", name, path, got.status, got.header.Get("Location"), got.body)
			continue
		}
		if !strings.Contains(got.body, "no endpoint answers at this path") || strings.Contains(got.body, `"route"`) {
			t.Errorf("GET /endpoints/%s%s answered %s; want the router's refusal and no handler answer", name, path, got.body)
		}
	}
	clean := endpointRequestWith(t, noRedirect, admin.base, http.MethodGet, "/endpoints/"+name+"/a/b", nil, "")
	if clean.status != http.StatusOK || !strings.Contains(clean.body, `"route":"a/b"`) {
		t.Fatalf("GET /endpoints/%s/a/b: %d %s; want the handler's answer", name, clean.status, clean.body)
	}
}

//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2525: an endpoint's handle(req) is never shown the form
// viewer token. The token lets its holder call a form as the person it names
// for twelve hours, so it is a credential, and req.headers drops it with the
// platform's other credentials.
//
// Wire forms: manage_script endpoint_set takes methods a JSON array of
// strings and endpoint and auth_mode strings, sent as the literal tools/call
// params; the endpoint itself is called over HTTP with the headers as sent by
// a browser.

const issue2525EchoHeaders = `def handle(req):
    """Answers with the headers it was shown."""
    return {"json": req["headers"]}
`

// TestIssue2525_AHandlerIsNotShownTheViewerToken: a request carrying
// X-Form-Viewer, a cookie and a proxy credential reaches the handler with
// none of them, and with an ordinary header intact.
func TestIssue2525_AHandlerIsNotShownTheViewerToken(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2525")
	saveEndpointScript(t, admin, name, issue2525EchoHeaders, `{"headers": {}}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "auth_mode": "public"})

	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, http.Header{
		"X-Form-Viewer":       {"acc2525-viewer-token"},
		"Cookie":              {"mcp_session=acc2525-cookie"},
		"Proxy-Authorization": {"Basic acc2525-proxy"},
		"Accept-Language":     {"en-AU"},
	}, "")
	if got.status != http.StatusOK {
		t.Fatalf("GET /endpoints/%s: %d %s", name, got.status, got.body)
	}
	body := strings.ToLower(got.body)
	for _, leaked := range []string{"x-form-viewer", "acc2525-viewer-token", "acc2525-cookie", "acc2525-proxy"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the handler was shown %q: %s", leaked, got.body)
		}
	}
	if !strings.Contains(got.body, "en-AU") {
		t.Errorf("the handler was not shown an ordinary header: %s", got.body)
	}
}

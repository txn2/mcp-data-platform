//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Acceptance for #2457: an HTML asset declares mcp:endpoint:<name> and calls
// an authenticated endpoint from its sandboxed frame as the signed-in viewer.
// Every viewing surface rewrites the reference to the endpoint's address with
// a signed embed marker and, for a signed-in reader, their viewer token; the
// call from the opaque origin is answered, never with credentials, and the
// handler sees the reader in req["caller"]. Without the marker an Origin: null
// request is refused as before; a public share has no reader, so an
// authenticated endpoint refuses it and a public one answers it; and only the
// endpoint's owner or an administrator may declare an authenticated endpoint.
//
// Wire forms: save_asset takes name, content, content_type and purpose as
// strings and references as a list of strings; manage_asset share takes
// action, asset_id, access_mode and expires_in as strings; the portal share
// route takes shared_with_email and permission as strings; handler_set kind=endpoint takes
// endpoint, path and auth_mode as strings and methods and callers as lists. A
// served asset is an HTTP GET of its content route, and the asset's call is an
// HTTP GET with Origin: null of the address it was served.

const purpose2457 = "Acceptance 2457: an asset calls an endpoint as its reader."

// asset2457 is HTML calling the endpoint name.
func asset2457(name string) string {
	return `<!doctype html><script>const EP = "mcp:endpoint:` + name + `";
const u = new URL(EP); u.searchParams.set("q", "late");
fetch(u).then((r) => r.json()).then((j) => { document.body.textContent = j.caller; });</script>`
}

// bindLogSearch2457 binds an endpoint answering who called it and what they
// asked, in authMode.
func bindLogSearch2457(t *testing.T, admin *client, authMode string) string {
	t.Helper()
	name := "acc2457-" + authMode + "-" + unique1579()
	saveEndpointScript(t, admin, name, `def handle(req):
    """Answers who called and what they asked."""
    caller = req["caller"]["email"] if req["caller"] else None
    return {"json": {"caller": caller, "query": req["query"]}}
`, `{"params": {}, "caller": None, "query": {}}`)
	callers := []string{}
	if authMode == "authenticated" {
		callers = []string{"*"}
	}
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "path": "", "auth_mode": authMode, "callers": callers})
	return name
}

func saveAsset2457(t *testing.T, c *client, endpoint string) string {
	t.Helper()
	saved := c.call("save_asset", map[string]any{
		"name": "Acceptance 2457 " + endpoint, "content": asset2457(endpoint), "content_type": "text/html",
		"references": []any{"mcp:endpoint:" + endpoint}, "purpose": purpose2457,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset carried no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": purpose2457})
	})
	return id
}

// servedAddress is the endpoint address a served copy of an asset carries.
func servedAddress2457(t *testing.T, body, endpoint string) *url.URL {
	t.Helper()
	_, after, found := strings.Cut(body, "/endpoints/"+endpoint+"?")
	if !found {
		t.Fatalf("the served content carries no address for %s: %s", endpoint, body)
	}
	raw := after[:max(strings.IndexAny(after, `"'`), 0)]
	q, err := url.ParseQuery(raw)
	if err != nil || q.Get("endpoint_embed") == "" {
		t.Fatalf("the served address carries no embed marker: %q", raw)
	}
	return &url.URL{Path: "/endpoints/" + endpoint, RawQuery: raw}
}

// call2457 makes the asset's call: the served address plus q=late, from the
// frame's opaque origin.
func call2457(t *testing.T, address *url.URL) (int, map[string]any, http.Header) {
	t.Helper()
	q := address.Query()
	q.Set("q", "late")
	res := endpointRequest(t, baseURL(), http.MethodGet, address.Path+"?"+q.Encode(), http.Header{"Origin": {"null"}}, "")
	var out map[string]any
	_ = json.Unmarshal([]byte(res.body), &out)
	return res.status, out, res.header
}

// The asset page: the owner's copy calls the authenticated endpoint as the
// owner, from the opaque origin, never with credentials; the preflight is
// answered; and the marker and token never reach the handler.
func TestIssue2457_AnAssetCallsAnAuthenticatedEndpointAsItsReader(t *testing.T) {
	admin := connect(t)
	endpoint := bindLogSearch2457(t, admin, "authenticated")
	id := saveAsset2457(t, admin, endpoint)
	content := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/assets/"+id+"/content", bearer(devAPIKey()), "")
	if content.status != http.StatusOK {
		t.Fatalf("the asset's content answered %d", content.status)
	}
	address := servedAddress2457(t, content.body, endpoint)
	if address.Query().Get("endpoint_viewer") == "" {
		t.Fatalf("the signed-in reader's copy hands the endpoint no viewer token: %s", address.RawQuery)
	}

	pre := endpointRequest(t, baseURL(), http.MethodOptions, address.String(), http.Header{"Origin": {"null"}, "Access-Control-Request-Method": {"GET"}}, "")
	if pre.status != http.StatusNoContent || pre.header.Get("Access-Control-Allow-Origin") != "null" {
		t.Fatalf("the preflight from the asset answered %d (allow origin %q)", pre.status, pre.header.Get("Access-Control-Allow-Origin"))
	}
	status, out, header := call2457(t, address)
	if status != http.StatusOK || out["caller"] != "admin@example.com" {
		t.Fatalf("the asset's call answered %d: %v", status, out)
	}
	if header.Get("Access-Control-Allow-Origin") != "null" || header.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("the answer allows origin %q, credentials %q", header.Get("Access-Control-Allow-Origin"), header.Get("Access-Control-Allow-Credentials"))
	}
	query, _ := out["query"].(map[string]any)
	if query["q"] != "late" || query["endpoint_embed"] != nil || query["endpoint_viewer"] != nil {
		t.Errorf("the handler read the query %v; want q alone", query)
	}
}

// An access-gated share: the asset shared with a person calls the endpoint as
// that person.
func TestIssue2457_ASharedAssetCallsTheEndpointAsTheSharedReader(t *testing.T) {
	admin := connect(t)
	endpoint := bindLogSearch2457(t, admin, "authenticated")
	id := saveAsset2457(t, admin, endpoint)
	if status, share := admin.rest(http.MethodPost, "/api/v1/portal/assets/"+id+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": devPeerEmailAddr, "permission": "viewer"})); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("share: HTTP %d: %v", status, share)
	}
	content := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/assets/"+id+"/content", bearer(devPeerAPIKey), "")
	if content.status != http.StatusOK {
		t.Fatalf("the shared reader's content answered %d", content.status)
	}
	status, out, _ := call2457(t, servedAddress2457(t, content.body, endpoint))
	if status != http.StatusOK || out["caller"] != devPeerEmailAddr {
		t.Fatalf("the shared reader's call answered %d: %v", status, out)
	}
}

// Without a valid marker an Origin: null request is still refused, and a
// viewer token alone is no caller.
func TestIssue2457_WithoutTheMarkerTheOpaqueOriginIsRefused(t *testing.T) {
	admin := connect(t)
	endpoint := bindLogSearch2457(t, admin, "authenticated")
	id := saveAsset2457(t, admin, endpoint)
	content := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/assets/"+id+"/content", bearer(devAPIKey()), "")
	address := servedAddress2457(t, content.body, endpoint)
	pre := endpointRequest(t, baseURL(), http.MethodOptions, "/endpoints/"+endpoint, http.Header{"Origin": {"null"}, "Access-Control-Request-Method": {"GET"}}, "")
	if pre.status != http.StatusForbidden {
		t.Fatalf("a preflight from the opaque origin with no marker answered %d, want 403", pre.status)
	}
	tokenOnly := url.Values{"endpoint_viewer": {address.Query().Get("endpoint_viewer")}}
	res := endpointRequest(t, baseURL(), http.MethodGet, "/endpoints/"+endpoint+"?"+tokenOnly.Encode(), http.Header{"Origin": {"null"}}, "")
	if res.status != http.StatusUnauthorized {
		t.Fatalf("a viewer token with no marker answered %d, want 401", res.status)
	}
}

// A public share has no reader: an authenticated endpoint refuses its call,
// and a public endpoint answers it.
func TestIssue2457_APublicShareReachesAPublicEndpointAndNotAnAuthenticatedOne(t *testing.T) {
	admin := connect(t)
	for authMode, want := range map[string]int{"authenticated": http.StatusUnauthorized, "public": http.StatusOK} {
		endpoint := bindLogSearch2457(t, admin, authMode)
		id := saveAsset2457(t, admin, endpoint)
		share := admin.call("manage_asset", map[string]any{
			"action": "share", "asset_id": id, "access_mode": "public", "expires_in": "1h", "purpose": purpose2457,
		})
		shareURL, _ := share["share_url"].(string)
		token := shareURL[strings.LastIndex(shareURL, "/")+1:]
		content := endpointRequest(t, baseURL(), http.MethodGet, "/portal/view/"+token+"/content", nil, "")
		if content.status != http.StatusOK {
			t.Fatalf("the public share's content answered %d", content.status)
		}
		address := servedAddress2457(t, content.body, endpoint)
		if address.Query().Get("endpoint_viewer") != "" {
			t.Errorf("a public share's copy carries a viewer token")
		}
		if status, out, _ := call2457(t, address); status != want {
			t.Errorf("a public share's call to the %s endpoint answered %d (%v), want %d", authMode, status, out, want)
		}
	}
}

// Only the endpoint's owner or an administrator may declare an authenticated
// endpoint in an asset.
func TestIssue2457_OnlyItsOwnerMayCallAnAuthenticatedEndpointFromAnAsset(t *testing.T) {
	admin := connect(t)
	endpoint := bindLogSearch2457(t, admin, "authenticated")
	peer := connectAs(t, devPeerAPIKey)
	res, text, err := peer.callRaw("save_asset", map[string]any{
		"name": "Acceptance 2457 peer", "content": asset2457(endpoint), "content_type": "text/html",
		"references": []any{"mcp:endpoint:" + endpoint}, "purpose": purpose2457,
	})
	if err != nil || !res.IsError || !strings.Contains(text, "only its owner or an administrator") {
		t.Fatalf("a reader who does not own the endpoint declared it: %v %s", err, text)
	}
}

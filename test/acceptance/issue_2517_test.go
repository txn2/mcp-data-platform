//go:build integration

package acceptance

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Acceptance for #2517: endpoints that serve browser pages on their
// application's own origin.
//
// Wire forms: manage_application takes command, name, application, kind and
// part_id as strings; manage_script takes command, name and source as strings;
// endpoint_set takes endpoint, path, auth_mode and response_mode as strings and
// methods, callers and page_cdns as lists of strings; save_asset takes name,
// content and content_type as strings. A page is an HTTP GET naming the page
// origin in Host; a call from its script is a GET or a POST with a JSON object
// body and an Origin header.

const pagesDomain2517 = "pages.localhost"

// pageApp2517 is an application with a page, a JSON endpoint and two assets.
type pageApp2517 struct {
	admin    *client
	name     string
	page     string
	api      string
	origin   string
	host     string
	styleURL string
}

func pageHandler2517(api string) string {
	return `def handle(req):
    """Answers a page naming its viewer."""
    who = "nobody"
    if req["caller"]:
        who = req["caller"]["email"]
    nonce = req["csp_nonce"]
    body = "<!doctype html><link rel=stylesheet href=/_static/site.css><img src=/_static/logo.svg>"
    body += "<script nonce=" + nonce + ">fetch('/endpoints/` + api + `/').then(r => r.json()).then(j => document.title = j.ok)</script>"
    body += "<p id=viewer>" + who + "</p><pre>" + json.encode(req["headers"]) + "</pre>"
    return {"status": 200, "headers": {"Content-Type": "text/html; charset=utf-8"}, "body": body}
`
}

func newPageApp2517(t *testing.T, authMode string) *pageApp2517 {
	t.Helper()
	admin := connect(t)
	name := "acc2517-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2517"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	api := name + "-api"
	saveEndpointScript(t, admin, api, `def handle(req):
    """Answers JSON."""
    who = None
    if req["caller"]:
        who = req["caller"]["email"]
    return {"json": {"ok": "yes", "who": who}}
`, `{"params": {}, "caller": None}`)
	addScriptPart2517(t, admin, name, api)
	bindEndpoint(t, admin, api, "", map[string]any{"endpoint": api, "methods": []string{"GET", "POST"}, "path": "/", "auth_mode": authMode, "callers": callersFor2517(authMode)})
	saveEndpointScript(t, admin, name, pageHandler2517(api), `{"params": {}, "caller": None, "csp_nonce": "n", "headers": {}}`)
	addScriptPart2517(t, admin, name, name)
	bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "methods": []string{"GET"}, "path": "/", "auth_mode": authMode, "callers": callersFor2517(authMode),
		"response_mode": "page", "page_cdns": []string{"https://cdn.jsdelivr.net"},
	})
	for _, a := range [][2]string{{"site.css", "body{color:red}"}, {"logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`}} {
		ct := "text/css"
		if strings.HasSuffix(a[0], ".svg") {
			ct = "image/svg+xml"
		}
		saved := admin.call("save_asset", map[string]any{"name": a[0], "content": a[1], "content_type": ct})
		id, _ := saved["asset_id"].(string)
		if id == "" {
			t.Fatalf("save_asset %s carried no asset_id: %v", a[0], saved)
		}
		admin.call("manage_application", map[string]any{"command": "add_part", "application": name, "kind": "asset", "part_id": id})
	}
	admin.call("manage_application", map[string]any{"command": "release", "application": name, "note": "first"})
	return &pageApp2517{admin: admin, name: name, page: name, api: api, host: name + "." + pagesDomain2517, origin: "http://" + name + "." + pagesDomain2517}
}

func callersFor2517(authMode string) []string {
	if authMode == "authenticated" {
		return []string{"*"}
	}
	return []string{}
}

func addScriptPart2517(t *testing.T, admin *client, app, scriptName string) {
	t.Helper()
	got := admin.call("manage_script", map[string]any{"command": "get", "name": scriptName})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("script %s carried no id: %v", scriptName, got)
	}
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "script", "part_id": id})
}

// pageResponse is one answer from a page origin.
type pageResponse struct {
	status int
	header http.Header
	body   string
}

// noRedirect is a client that hands back each redirect.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// onHost sends a request naming host, with headers.
func onHost(t *testing.T, host, method, target string, headers map[string]string, body string) pageResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, baseURL()+target, strings.NewReader(body)) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("building %s %s: %v", method, target, err)
	}
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := noRedirect.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("%s %s on %s: %v", method, target, host, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	return pageResponse{status: res.StatusCode, header: res.Header, body: string(raw)}
}

func TestIssue2517_APageServesItsScriptStylesImagesAndCallsItsEndpoints(t *testing.T) {
	app := newPageApp2517(t, "public")
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, app.host, http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusOK
	})
	policy := got.header.Get("Content-Security-Policy")
	if strings.Contains(policy, "sandbox") || !strings.Contains(policy, "script-src 'self' 'nonce-") {
		t.Fatalf("the page's policy runs no script: %q", policy)
	}
	nonce := between2517(policy, "'nonce-", "'")
	if nonce == "" || !strings.Contains(got.body, "<script nonce="+nonce+">") {
		t.Errorf("the page's script does not carry the policy's nonce %q: %s", nonce, got.body)
	}
	if !strings.Contains(policy, "connect-src 'self'") || !strings.Contains(policy, "https://cdn.jsdelivr.net") {
		t.Errorf("the policy lacks its own endpoints or its CDN: %q", policy)
	}
	if !strings.Contains(policy, "frame-ancestors 'none'") {
		t.Errorf("a page with no allowed origins can be framed: %q", policy)
	}
	for _, p := range []struct{ path, want string }{{"/_static/site.css", "text/css"}, {"/_static/logo.svg", "image/svg+xml"}} {
		r := onHost(t, app.host, http.MethodGet, p.path, nil, "")
		if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), p.want) {
			t.Errorf("%s: %d %q", p.path, r.status, r.header.Get("Content-Type"))
		}
		if etag := r.header.Get("ETag"); etag == "" {
			t.Errorf("%s has no ETag", p.path)
		} else if again := onHost(t, app.host, http.MethodGet, p.path, map[string]string{"If-None-Match": etag}, ""); again.status != http.StatusNotModified {
			t.Errorf("%s with its ETag: %d, want 304", p.path, again.status)
		}
	}
	if r := onHost(t, app.host, http.MethodGet, "/endpoints/"+app.api+"/", nil, ""); r.status != http.StatusOK || !strings.Contains(r.body, `"ok":"yes"`) {
		t.Errorf("the application's endpoint on its origin: %d %s", r.status, r.body)
	}
	if r := onHost(t, app.host, http.MethodGet, "/portal/", nil, ""); r.status == http.StatusOK && strings.Contains(r.body, "portal") {
		t.Errorf("the page origin answers a portal route: %d", r.status)
	}
	if r := onHost(t, app.host, http.MethodGet, "/api/v1/portal/me", map[string]string{"X-API-Key": devAPIKey()}, ""); r.status == http.StatusOK {
		t.Errorf("the page origin answers the portal API: %d", r.status)
	}
}

func between2517(s, open, end string) string {
	_, rest, ok := strings.Cut(s, open)
	if !ok {
		return ""
	}
	out, _, _ := strings.Cut(rest, end)
	return out
}

// signIn2517 walks the handoff as a browser does, with a key standing in for
// the session on the portal step, and returns the viewer cookie.
func signIn2517(t *testing.T, app *pageApp2517, apiKey string) (cookie string, code string) {
	t.Helper()
	start := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, app.host, http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusFound
	})
	state := cookieOf2517(start.header, "page_state")
	loc, err := url.Parse(start.header.Get("Location"))
	if err != nil || state == "" || !strings.HasSuffix(loc.Path, "/pages/handoff") {
		t.Fatalf("an unsigned page visit did not start the handoff: %d %v state %q", start.status, start.header, state)
	}
	if strings.Contains(loc.RawQuery, state) {
		t.Errorf("the state cookie's value is in the portal URL: %s", loc.RawQuery)
	}
	back := onHost(t, hostOf(t), http.MethodGet, loc.RequestURI(), map[string]string{"X-API-Key": apiKey}, "")
	if back.status != http.StatusFound {
		t.Fatalf("the portal handoff answered %d: %s", back.status, back.body)
	}
	target, err := url.Parse(back.header.Get("Location"))
	if err != nil || target.Hostname() != app.host || target.Path != "/_auth" {
		t.Fatalf("the handoff sent the browser to %q", back.header.Get("Location"))
	}
	if ref := back.header.Get("Referrer-Policy"); ref != "no-referrer" {
		t.Errorf("the handoff's Referrer-Policy is %q", ref)
	}
	code = target.Query().Get("code")
	for _, q := range []url.Values{
		{"application": {app.name}, "host": {app.host + ":443@evil.example"}, "path": {"/"}, "state": {strings.Repeat("a", 64)}},
		{"application": {"nope"}, "host": {"nope." + pagesDomain2517}, "path": {"/"}, "state": {strings.Repeat("a", 64)}},
		{"application": {app.name}, "host": {app.host}, "path": {"//evil.example"}, "state": {strings.Repeat("a", 64)}},
	} {
		if r := onHost(t, hostOf(t), http.MethodGet, "/pages/handoff?"+q.Encode(), map[string]string{"X-API-Key": apiKey}, ""); r.status != http.StatusBadRequest {
			t.Errorf("a handoff to %v answered %d %v", q, r.status, r.header.Get("Location"))
		}
	}
	done := onHost(t, app.host, http.MethodGet, target.RequestURI(), map[string]string{"Cookie": "page_state=" + state}, "")
	cookie = cookieOf2517(done.header, "page_viewer")
	if done.status != http.StatusFound || cookie == "" {
		t.Fatalf("completing the handoff: %d %v %s", done.status, done.header, done.body)
	}
	return cookie, code
}

func cookieOf2517(h http.Header, name string) string {
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func hostOf(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(baseURL())
	if err != nil {
		t.Fatalf("base URL: %v", err)
	}
	return u.Host
}

func TestIssue2517_AnAuthenticatedPageKnowsItsViewerAndCallsAsThem(t *testing.T) {
	app := newPageApp2517(t, "authenticated")
	cookie, code := signIn2517(t, app, analystKey1892)
	withCookie := map[string]string{"Cookie": "page_viewer=" + cookie}

	page := onHost(t, app.host, http.MethodGet, "/", withCookie, "")
	if page.status != http.StatusOK || !strings.Contains(page.body, "rachel.thompson@example.com") {
		t.Fatalf("the page does not know its viewer: %d %s", page.status, page.body)
	}
	who := onHost(t, app.host, http.MethodGet, "/_viewer", withCookie, "")
	if !strings.Contains(who.body, "rachel.thompson@example.com") {
		t.Errorf("/_viewer: %d %s", who.status, who.body)
	}
	api := onHost(t, app.host, http.MethodGet, "/endpoints/"+app.api+"/", withCookie, "")
	if api.status != http.StatusOK || !strings.Contains(api.body, "rachel.thompson@example.com") {
		t.Errorf("the page's call did not run as its viewer: %d %s", api.status, api.body)
	}

	// A code works once, and a request that changes something must prove it
	// came from this origin.
	again := onHost(t, app.host, http.MethodGet, "/_auth?path=/&code="+url.QueryEscape(code), map[string]string{"Cookie": "page_state=x"}, "")
	if again.status == http.StatusFound {
		t.Errorf("a used code was honored again")
	}
	post := map[string]string{"Cookie": "page_viewer=" + cookie, "Content-Type": "application/json"}
	if r := onHost(t, app.host, http.MethodPost, "/endpoints/"+app.api+"/", post, "{}"); r.status != http.StatusForbidden {
		t.Errorf("a POST with no same-origin proof: %d, want 403", r.status)
	}
	post["Origin"] = "http://other." + pagesDomain2517
	if r := onHost(t, app.host, http.MethodPost, "/endpoints/"+app.api+"/", post, "{}"); r.status != http.StatusForbidden {
		t.Errorf("a POST from a sibling origin: %d, want 403", r.status)
	}
	post["Origin"] = app.origin
	if r := onHost(t, app.host, http.MethodPost, "/endpoints/"+app.api+"/", post, "{}"); r.status != http.StatusOK {
		t.Errorf("a POST from its own origin: %d %s", r.status, r.body)
	}
}

func TestIssue2517_APageCannotReadOrUseThePortalSession(t *testing.T) {
	app := newPageApp2517(t, "public")
	const secret = "portal-session-secret-2517"
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, app.host, http.MethodGet, "/", map[string]string{
			"Cookie": "mcp_session=" + secret, "Authorization": "Bearer " + secret, "X-API-Key": secret,
		}, "")
		return r, r.status == http.StatusOK
	})
	if strings.Contains(got.body, secret) {
		t.Errorf("the handler was shown a portal credential: %s", got.body)
	}
	authed := newPageApp2517(t, "authenticated")
	r := pollUntil2518(t, func() (pageResponse, bool) {
		res := onHost(t, authed.host, http.MethodGet, "/endpoints/"+authed.api+"/", map[string]string{"X-API-Key": devAPIKey()}, "")
		return res, res.status != http.StatusNotFound
	})
	if r.status != http.StatusUnauthorized {
		t.Errorf("an API key was honored on the page origin: %d %s", r.status, r.body)
	}
	if strings.Contains(r.header.Get("Set-Cookie"), "mcp_session") {
		t.Errorf("the page origin set the portal's cookie: %v", r.header)
	}
	// One application's viewer is nobody on another.
	cookie, _ := signIn2517(t, authed, analystKey1892)
	other := newPageApp2517(t, "authenticated")
	cross := pollUntil2518(t, func() (pageResponse, bool) {
		res := onHost(t, other.host, http.MethodGet, "/endpoints/"+other.api+"/", map[string]string{"Cookie": "page_viewer=" + cookie}, "")
		return res, res.status != http.StatusNotFound
	})
	if cross.status != http.StatusUnauthorized {
		t.Errorf("a viewer token for one application opened on another: %d %s", cross.status, cross.body)
	}
}

func TestIssue2517_TheViewerTokensReachNoAuditRowOrLog(t *testing.T) {
	app := newPageApp2517(t, "authenticated")
	cookie, code := signIn2517(t, app, analystKey1892)
	onHost(t, app.host, http.MethodGet, "/", map[string]string{"Cookie": "page_viewer=" + cookie}, "")
	db := issue1904DB(t)
	for _, secret := range []string{cookie, code} {
		pollAbsent2518(t, func() bool {
			var n int
			err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_logs WHERE parameters::text LIKE '%' || $1 || '%' OR error_message LIKE '%' || $1 || '%'`, secret).Scan(&n)
			return err == nil && n == 0
		}, "a viewer token in audit_logs")
		searchLogs2518(t, secret)
	}
}

func TestIssue2517_EndpointsThatDoNotOptInKeepTheSandbox(t *testing.T) {
	app := newPageApp2517(t, "public")
	portal := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, hostOf(t), http.MethodGet, "/endpoints/"+app.api+"/", nil, "")
		return r, r.status == http.StatusOK
	})
	if got := portal.header.Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Errorf("an api endpoint's policy: %q", got)
	}
	onOrigin := onHost(t, app.host, http.MethodGet, "/endpoints/"+app.api+"/", nil, "")
	if got := onOrigin.header.Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Errorf("an api endpoint on the page origin: %q", got)
	}
	page := onHost(t, hostOf(t), http.MethodGet, "/endpoints/"+app.page+"/", nil, "")
	if got := page.header.Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Errorf("a page served on the portal's origin must stay sandboxed: %q", got)
	}
}

func TestIssue2517_AScriptInNoApplicationCannotBeAPage(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2517-orphan")
	saveEndpointScript(t, admin, name, "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return {\"body\": \"x\"}\n", `{"params": {}, "caller": None}`)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	out, text := endpointSet(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "path": "/", "auth_mode": "public", "response_mode": "page"})
	if out != nil || !strings.Contains(text, "application") {
		t.Errorf("a script in no application became a page: %v %s", out, text)
	}
}

func TestIssue2517_ThePageAndItsFilesReleaseAndRollBackTogether(t *testing.T) {
	app := newPageApp2517(t, "public")
	first := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, app.host, http.MethodGet, "/_static/site.css", nil, "")
		return r, r.status == http.StatusOK
	})
	if first.body != "body{color:red}" {
		t.Fatalf("first release's style: %q", first.body)
	}
	got := app.admin.call("manage_application", map[string]any{"command": "get", "application": app.name})
	id := assetPartNamed2517(t, app, got, "site.css")
	app.admin.call("manage_asset", map[string]any{"action": "update", "asset_id": id, "content": "body{color:blue}"})

	if r := onHost(t, app.host, http.MethodGet, "/_static/site.css", nil, ""); r.body != "body{color:red}" {
		t.Errorf("a saved asset went live before its release: %q", r.body)
	}
	app.admin.call("manage_application", map[string]any{"command": "release", "application": app.name, "note": "second"})
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, app.host, http.MethodGet, "/_static/site.css", nil, "")
		return r, r.body == "body{color:blue}"
	})
	app.admin.call("manage_application", map[string]any{"command": "rollback", "application": app.name})
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, app.host, http.MethodGet, "/_static/site.css", nil, "")
		return r, r.body == "body{color:red}"
	})
}

func assetPartNamed2517(t *testing.T, app *pageApp2517, got map[string]any, name string) string {
	t.Helper()
	appRec, _ := got["application"].(map[string]any)
	parts, _ := appRec["parts"].([]any)
	for _, p := range parts {
		m, _ := p.(map[string]any)
		if m["kind"] != "asset" {
			continue
		}
		id, _ := m["id"].(string)
		a := app.admin.call("manage_asset", map[string]any{"action": "get", "asset_id": id})
		if a["name"] == name {
			return id
		}
	}
	t.Fatalf("no asset part named %s in %v", name, got)
	return ""
}

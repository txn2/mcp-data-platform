//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Acceptance for #2553: a page endpoint's path can end in a catch-all segment,
// {name...}, so an application's own address answers every path it does not
// reserve. The handler reads the rest of the path as req["params"]["route"],
// routes it and chooses each status. A catch-all anywhere but last is refused
// at handler_set kind=endpoint; a template without one matches exactly as before; and a
// path whose segments are empty, relative steps or backslashes never reaches
// the handler.
//
// Wire forms: manage_application takes command, name, application, kind and
// part_id as strings; manage_script takes command, name and source as strings;
// handler_set kind=endpoint takes endpoint, path, auth_mode and response_mode as strings and
// methods and callers as lists of strings; save_asset takes name, content and
// content_type as strings. A page is an HTTP GET naming the page origin in
// Host.

// catchAllHandler2553 routes the application's own addresses.
const catchAllHandler2553 = `def handle(req):
    """Routes the application's own addresses."""
    route = req["params"]["route"]
    if route == "robots.txt":
        return {"headers": {"Content-Type": "text/plain; charset=utf-8"}, "body": "User-agent: *\nDisallow:\n"}
    known = route in ("", "cart") or route.startswith("products/") or route.startswith("a/")
    if not known:
        return {"status": 404, "headers": {"Content-Type": "text/html; charset=utf-8"}, "body": "<h1>No such page</h1>"}
    return {"json": {"route": route}}
`

// app2553 is an application whose root endpoint catches every path, beside a
// second endpoint with no catch-all and one asset.
type app2553 struct {
	admin *client
	name  string
	other string
	host  string
}

func newApp2553(t *testing.T) *app2553 {
	t.Helper()
	admin := connect(t)
	name := "acc2553-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2553"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	saveEndpointScript(t, admin, name, catchAllHandler2553, `{"params": {"route": "cart"}, "caller": None}`)
	addScriptPart2517(t, admin, name, name)
	bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "methods": []string{"GET"}, "path": "/{route...}", "auth_mode": "public",
		"callers": []string{}, "response_mode": "page",
	})
	other := name + "-other"
	saveEndpointScript(t, admin, other, `def handle(req):
    """Answers at its name alone."""
    return {"json": {"other": True}}
`, `{"params": {}, "caller": None}`)
	addScriptPart2517(t, admin, name, other)
	bindEndpoint(t, admin, other, "", map[string]any{"endpoint": other, "methods": []string{"GET"}, "path": "/", "auth_mode": "public", "callers": []string{}})
	saved := admin.call("save_asset", map[string]any{"name": "site.css", "content": "body{color:red}", "content_type": "text/css"})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset carried no asset_id: %v", saved)
	}
	admin.call("manage_application", map[string]any{"command": "add_part", "application": name, "kind": "asset", "part_id": id})
	admin.call("manage_application", map[string]any{"command": "release", "application": name, "note": "first"})
	return &app2553{admin: admin, name: name, other: other, host: name + "." + pagesDomain2517}
}

func routeOf2553(t *testing.T, res pageResponse) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatalf("not the handler's JSON (%d): %s", res.status, res.body)
	}
	route, _ := out["route"].(string)
	return route
}

// Criterion 1: with path "/{route...}", /, /cart, /products/shoe and
// /a/b/c/d each reach the handler with the matching route, and a trailing
// slash is the same path.
func TestIssue2553_EveryPathReachesTheHandlerWithItsRoute(t *testing.T) {
	a := newApp2553(t)
	for target, want := range map[string]string{
		"/": "", "/cart": "cart", "/cart/": "cart", "/products/shoe": "products/shoe", "/a/b/c/d": "a/b/c/d",
	} {
		res := onHost(t, a.host, "GET", target, nil, "")
		if res.status != 200 {
			t.Fatalf("GET %s answered %d: %s", target, res.status, res.body)
		}
		if got := routeOf2553(t, res); got != want {
			t.Errorf("GET %s: route %q, want %q", target, got, want)
		}
	}
}

// Criterion 2: /robots.txt answers the text/plain body the handler returned,
// and an unknown path answers the status and body the handler chose.
func TestIssue2553_TheHandlerChoosesTheTypeAndTheStatus(t *testing.T) {
	a := newApp2553(t)
	robots := onHost(t, a.host, "GET", "/robots.txt", nil, "")
	if robots.status != 200 || !strings.HasPrefix(robots.header.Get("Content-Type"), "text/plain") || !strings.Contains(robots.body, "User-agent: *") {
		t.Fatalf("robots.txt answered %d %q: %s", robots.status, robots.header.Get("Content-Type"), robots.body)
	}
	missing := onHost(t, a.host, "GET", "/no/such/page", nil, "")
	if missing.status != 404 || !strings.Contains(missing.body, "<h1>No such page</h1>") {
		t.Fatalf("an unknown path answered %d: %s", missing.status, missing.body)
	}
}

// Criterion 3: /_static/<asset> and /endpoints/<other> still answer as before
// with the catch-all bound.
func TestIssue2553_TheReservedPathsKeepTheirMeaning(t *testing.T) {
	a := newApp2553(t)
	css := onHost(t, a.host, "GET", "/_static/site.css", nil, "")
	if css.status != 200 || !strings.Contains(css.body, "color:red") {
		t.Fatalf("/_static/site.css answered %d: %s", css.status, css.body)
	}
	other := onHost(t, a.host, "GET", "/endpoints/"+a.other, nil, "")
	if other.status != 200 || !strings.Contains(other.body, `"other":true`) {
		t.Fatalf("/endpoints/%s answered %d: %s", a.other, other.status, other.body)
	}
	viewer := onHost(t, a.host, "GET", "/_viewer", nil, "")
	if viewer.status != 401 {
		t.Errorf("/_viewer answered %d with nobody signed in, want its own 401: %s", viewer.status, viewer.body)
	}
}

// Criterion 4: a catch-all in a middle segment, or two of them, is refused at
// handler_set kind=endpoint naming the path.
func TestIssue2553_ACatchAllAnywhereButLastIsRefused(t *testing.T) {
	a := newApp2553(t)
	for _, path := range []string{"/{route...}/edit", "/{a...}/{b...}"} {
		out, text := endpointSet(t, a.admin, a.name, "", map[string]any{"path": path})
		if out != nil {
			t.Fatalf("handler_set kind=endpoint path %q was accepted", path)
		}
		if !strings.Contains(text, path) || !strings.Contains(text, "only the last segment may be a catch-all") {
			t.Errorf("path %q refused with %q, want the path named and the rule", path, text)
		}
	}
}

// Criterion 5: a binding without a catch-all answers 404 for a longer path,
// as it does today.
func TestIssue2553_ABindingWithoutACatchAllMatchesExactly(t *testing.T) {
	a := newApp2553(t)
	res := onHost(t, a.host, "GET", "/endpoints/"+a.other+"/x/y", nil, "")
	if res.status != 404 || !strings.Contains(res.body, "no endpoint answers at this path") {
		t.Fatalf("a longer path answered %d: %s", res.status, res.body)
	}
}

// The threat model: a doubly encoded relative step, an empty segment or a
// backslash never reaches the handler; the platform answers its own 404.
func TestIssue2553_ARouteIsAlwaysACleanRelativePath(t *testing.T) {
	a := newApp2553(t)
	for _, target := range []string{"/a/%2e%2e/b", "/a//b", "/a/%5Cb", "/a/.%2e/b"} {
		res := onHost(t, a.host, "GET", target, nil, "")
		if res.status != 404 || !strings.Contains(res.body, "no endpoint answers at this path") {
			t.Errorf("GET %s answered %d: %s; want the platform's 404, the handler never run", target, res.status, res.body)
		}
	}
}

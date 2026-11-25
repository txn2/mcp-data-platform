//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// Issue #2822: an application's page says where each environment's endpoints
// answer. The application detail route lists, by environment, each endpoint
// with every URL it answers at: the platform's host (/endpoints/<name> for
// the last environment, /environments/<env>/endpoints/<name> for another),
// the application's own address for the last environment, and each domain of
// a site environment part of the environment's name. manage_script's
// handler_list url and handler_get page_address are read from the same
// computation, and every URL listed answers.
//
// Wire forms: manage_application's command, name, application, environment,
// kind, part_id and release_mode are strings; manage_script's command and name
// are strings; handler_set's endpoint, path and auth_mode are strings and
// methods a list of strings; the portal application route and each listed URL
// are GETs with no body. No parameter touched admits a second form.

// app2822 is an application with an endpoint named like itself, bound at
// /{route...}, a dev environment before prod, and a linked part of a site's
// prod with a domain added.
type app2822 struct {
	name   string
	site   *site2604
	domain string
}

func newApp2822(t *testing.T) *app2822 {
	t.Helper()
	admin := connect(t)
	name := "acc2822-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2822"})
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	saveEndpointScript(t, admin, name, `def handle(req):
    """Answers that it was reached."""
    return {"json": {"answered": "acc2822"}}
`, `{"params": {}, "caller": None}`)
	addScriptPart2517(t, admin, name, name)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "path": "/{route...}", "auth_mode": "public"})
	admin.call("manage_application", map[string]any{"command": "add_environment", "application": name, "environment": "dev"})
	// A release runs in dev, the first environment, so dev answers too.
	admin.call("manage_application", map[string]any{"command": "release", "application": name, "note": "acceptance 2822"})

	s := newSite2604(t, keyAdmin2604, "acc2822-web-")
	s.publish(t, s.key, "v1")
	s.promote(t, "dev", "prod")
	domain := s.publicDomain(t, "prod")
	admin.call("manage_application", map[string]any{"command": "add_part", "application": name, "kind": "site", "part_id": s.name + "/prod", "release_mode": "linked"})
	return &app2822{name: name, site: s, domain: domain}
}

// endpoints2822 is the application detail route's endpoints for env: each
// endpoint's name and its URLs.
func (a *app2822) endpoints2822(t *testing.T, env string) map[string][]string {
	t.Helper()
	status, got := connect(t).rest(http.MethodGet, "/api/v1/portal/applications/"+a.name, nil)
	if status != http.StatusOK {
		t.Fatalf("GET the application: %d %v", status, got)
	}
	byEnv, _ := got["endpoints"].(map[string]any)
	list, ok := byEnv[env].([]any)
	if !ok {
		t.Fatalf("the detail has no endpoints for %s: %v", env, got["endpoints"])
	}
	out := map[string][]string{}
	for _, e := range list {
		ep, _ := e.(map[string]any)
		name, _ := ep["name"].(string)
		for _, u := range asSlice(ep["urls"]) {
			if s, ok := u.(string); ok {
				out[name] = append(out[name], s)
			}
		}
	}
	return out
}

// answers2822 GETs a listed URL, its template filled, through the platform
// with the URL's host, and reports whether the handler answered.
func answers2822(t *testing.T, raw string) (bool, string) {
	t.Helper()
	u, err := url.Parse(strings.ReplaceAll(raw, "{route...}", "probe"))
	if err != nil {
		t.Fatalf("a listed URL does not parse: %q", raw)
	}
	target := u.EscapedPath()
	if target == "" {
		target = "/probe"
	}
	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, u.Hostname(), http.MethodGet, target, nil, "")
		return r, r.status == http.StatusOK && strings.Contains(r.body, "acc2822")
	})
	return r.status == http.StatusOK && strings.Contains(r.body, "acc2822"), r.body
}

// TestIssue2822_TheLastEnvironmentListsItsAddressesAndEachAnswers is
// criteria 2, 4 and 5: prod lists the platform host's /endpoints/<name>, the
// application's own address and the site domain's /endpoints/<name>; each
// answers; and handler_list's url and handler_get's page_address are among
// them.
func TestIssue2822_TheLastEnvironmentListsItsAddressesAndEachAnswers(t *testing.T) {
	a := newApp2822(t)
	urls := a.endpoints2822(t, "prod")[a.name]
	want := map[string]bool{"platform": false, "own address": false, "site domain": false}
	for _, u := range urls {
		switch {
		case strings.HasSuffix(u, "/endpoints/"+a.name+"/{route...}") && !strings.Contains(u, pagesDomain2517) && !strings.Contains(u, a.domain):
			want["platform"] = true
		case strings.Contains(u, "://"+a.name+"."+pagesDomain2517) && !strings.Contains(u, "/endpoints/"):
			want["own address"] = true
		case strings.Contains(u, "://"+a.domain) && strings.HasSuffix(u, "/endpoints/"+a.name+"/{route...}"):
			want["site domain"] = true
		}
	}
	for what, found := range want {
		if !found {
			t.Errorf("prod does not list its %s: %v", what, urls)
		}
	}
	for _, u := range urls {
		// The site's own addresses on the pages domain are private: a caller
		// is sent to sign in, which is how they answer anyone not signed in.
		// The domain added public reaches the handler.
		if strings.Contains(u, "://"+a.site.name) && strings.Contains(u, pagesDomain2517) {
			if r := onHost(t, hostOf2822(t, u), http.MethodGet, "/endpoints/"+a.name+"/probe", nil, ""); r.status != http.StatusFound || !strings.Contains(r.header.Get("Location"), "/sites/handoff") {
				t.Errorf("%s answered %d, not the sign-in redirect", u, r.status)
			}
			continue
		}
		if ok, body := answers2822(t, u); !ok {
			t.Errorf("%s did not reach the handler: %.300s", u, body)
		}
	}

	admin := connect(t)
	handlers := admin.handlersOf(a.name, "endpoint", "")
	if len(handlers) != 1 {
		t.Fatalf("handler_list = %v", handlers)
	}
	listed, _ := handlers[0]["url"].(string)
	if !slices.Contains(urls, listed) {
		t.Errorf("handler_list's url %q is not among the card's %v", listed, urls)
	}
	described := admin.call("manage_script", map[string]any{"command": "handler_get", "handler": handlers[0]["handler"]})
	if page, _ := described["page_address"].(string); page == "" || !slices.Contains(urls, page) {
		t.Errorf("handler_get's page_address %q is not among the card's %v", page, urls)
	}
}

// TestIssue2822_AnotherEnvironmentListsItsOwnPath is criterion 3: dev, not
// the last, lists /environments/dev/endpoints/<name> on the platform host,
// and that URL answers.
func TestIssue2822_AnotherEnvironmentListsItsOwnPath(t *testing.T) {
	a := newApp2822(t)
	urls := a.endpoints2822(t, "dev")[a.name]
	found := false
	for _, u := range urls {
		if strings.Contains(u, "/environments/dev/endpoints/"+a.name+"/") {
			found = true
			if ok, body := answers2822(t, u); !ok {
				t.Errorf("%s did not reach the handler: %.300s", u, body)
			}
		}
		if strings.Contains(u, "://"+a.name+"."+pagesDomain2517) {
			t.Errorf("dev lists the application's own address, which runs prod: %s", u)
		}
	}
	if !found {
		t.Errorf("dev does not list its /environments/dev/ path: %v", urls)
	}
}

// TestIssue2822_AnApplicationWithNoEndpointListsNone is criterion 1's data:
// every environment is present with an empty list.
func TestIssue2822_AnApplicationWithNoEndpointListsNone(t *testing.T) {
	admin := connect(t)
	name := "acc2822-none-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2822, no endpoint"})
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	status, got := admin.rest(http.MethodGet, "/api/v1/portal/applications/"+name, nil)
	if status != http.StatusOK {
		t.Fatalf("GET the application: %d %v", status, got)
	}
	byEnv, _ := got["endpoints"].(map[string]any)
	if len(byEnv) == 0 {
		t.Fatalf("the detail lists no environment: %v", got["endpoints"])
	}
	for env, list := range byEnv {
		if l, ok := list.([]any); !ok || len(l) != 0 {
			t.Errorf("%s lists %v, want an empty list", env, list)
		}
	}
}

// hostOf2822 is a listed URL's host.
func hostOf2822(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("a listed URL does not parse: %q", raw)
	}
	return u.Hostname()
}

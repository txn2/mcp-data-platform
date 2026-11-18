//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2687: one domain of an environment is its primary, and the
// domains set to redirect answer a permanent redirect to it, from the gateway,
// in either direction, with no new release and untouched by promote.
//
// Wire forms: manage_site takes command, site, environment, hostname and mode
// as strings and public and primary as booleans; each is sent as literal
// tools/call params. The portal and admin routes take one JSON object each.
// The gateway is asked by Host header through the platform's own address,
// which answers in the clear on the dev stack, so a redirect names http.

type site2687 struct {
	admin       *client
	name        string
	bare, www   string
	releaseBody string
}

func newSite2687(t *testing.T) *site2687 {
	t.Helper()
	admin := connect(t)
	stamp := unique1579()
	s := &site2687{admin: admin, name: "acc2687-" + stamp, bare: "acc2687-" + stamp + ".example.test", releaseBody: "release " + stamp}
	s.www = "www." + s.bare
	admin.call("manage_site", map[string]any{"command": "create", "name": s.name, "description": "Acceptance #2687: www and the bare domain"})
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_site", map[string]any{"command": "delete", "site": s.name}) })
	files := []map[string]any{{"path": "index.html", "content": s.releaseBody}, {"path": "a/b.html", "content": s.releaseBody}}
	if got := admin.call("manage_site", map[string]any{"command": "publish", "site": s.name, "files": files}); got["release"] == nil {
		t.Fatalf("publishing: %v", got)
	}
	agentsMayPromote2643(t, s.name, "prod") // about serving, not who decides (#2643)
	admin.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"})
	return s
}

// redirectsTo polls until host answers a redirect, and returns it.
func (s *site2687) redirect(t *testing.T, host, method, target string) pageResponse {
	t.Helper()
	return pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, method, target, nil, "")
		return r, r.status == http.StatusMovedPermanently || r.status == http.StatusPermanentRedirect
	})
}

func (s *site2687) serves(t *testing.T, host, target string) {
	t.Helper()
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, target, nil, "")
		return r, r.status == http.StatusOK
	})
	if !strings.Contains(got.body, s.releaseBody) {
		t.Errorf("%s%s did not serve the release: %d %q", host, target, got.status, got.body)
	}
}

// TestIssue2687_TheWWWDomainRedirectsToThePrimaryKeepingPathAndQuery: with the
// bare domain primary and www set to redirect, GET answers 301 to the primary
// with path and query, a POST 308, the redirect is cacheable, and the primary
// serves the release.
func TestIssue2687_TheWWWDomainRedirectsToThePrimaryKeepingPathAndQuery(t *testing.T) {
	s := newSite2687(t)
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.bare, "public": true, "primary": true})
	added := s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.www, "public": true, "mode": "redirect"})
	if added["mode"] != "redirect" || added["redirects_to"] != s.bare {
		t.Fatalf("add_domain answered %v", added)
	}

	got := s.redirect(t, s.www, http.MethodGet, "/a/b?c=1")
	if got.status != http.StatusMovedPermanently || got.header.Get("Location") != "http://"+s.bare+"/a/b?c=1" {
		t.Fatalf("GET on www answered %d Location %q", got.status, got.header.Get("Location"))
	}
	if cc := got.header.Get("Cache-Control"); !strings.Contains(cc, "max-age=") || strings.Contains(cc, "no-store") {
		t.Errorf("the redirect is not cacheable: Cache-Control %q", cc)
	}
	if post := onHost(t, s.www, http.MethodPost, "/form?x=1", nil, "a=1"); post.status != http.StatusPermanentRedirect || post.header.Get("Location") != "http://"+s.bare+"/form?x=1" {
		t.Errorf("POST on www answered %d Location %q, want 308", post.status, post.header.Get("Location"))
	}
	s.serves(t, s.bare, "/a/b.html")
}

// TestIssue2687_TheReverseConfigurationIsTheMirrorImage: making www primary
// and the bare domain a redirect, with no new release, turns it around.
func TestIssue2687_TheReverseConfigurationIsTheMirrorImage(t *testing.T) {
	s := newSite2687(t)
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.bare, "public": true, "primary": true})
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.www, "public": true, "mode": "redirect"})
	s.redirect(t, s.www, http.MethodGet, "/")

	s.admin.call("manage_site", map[string]any{"command": "set_domain", "hostname": s.www, "primary": true})
	s.admin.call("manage_site", map[string]any{"command": "set_domain", "hostname": s.bare, "mode": "redirect"})
	got := s.redirect(t, s.bare, http.MethodGet, "/a/b?c=1")
	if got.header.Get("Location") != "http://"+s.www+"/a/b?c=1" {
		t.Fatalf("the bare domain redirected to %q", got.header.Get("Location"))
	}
	s.serves(t, s.www, "/a/b.html")
}

// TestIssue2687_APrimaryOthersRedirectToIsNotRemoved: removing the primary,
// or giving it up, is refused naming the redirect that depends on it, through
// manage_site and the portal route; setting the redirect to serve frees it.
func TestIssue2687_APrimaryOthersRedirectToIsNotRemoved(t *testing.T) {
	s := newSite2687(t)
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.bare, "primary": true})
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.www, "mode": "redirect"})

	_, text, err := s.admin.callRaw("manage_site", map[string]any{"command": "remove_domain", "hostname": s.bare})
	if err != nil || !strings.Contains(text, s.www) || !strings.Contains(text, "before removing it") {
		t.Fatalf("remove_domain of the primary: %v %s", err, text)
	}
	status, body := s.admin.rest(http.MethodDelete, "/api/v1/portal/sites/"+s.name+"/domains/"+s.bare, http.NoBody)
	if status == http.StatusNoContent || !strings.Contains(text2687(body), s.www) {
		t.Fatalf("the portal removed the primary: %d %v", status, body)
	}
	_, text, _ = s.admin.callRaw("manage_site", map[string]any{"command": "set_domain", "hostname": s.bare, "primary": false})
	if !strings.Contains(text, "redirect to") {
		t.Fatalf("giving up the primary was not refused: %s", text)
	}
	_, text, _ = s.admin.callRaw("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "stage", "hostname": "x." + s.bare, "mode": "redirect"})
	if !strings.Contains(text, "no primary domain") {
		t.Fatalf("a redirect with no primary was not refused: %s", text)
	}

	if status := s.admin.restJSON(http.MethodPut, "/api/v1/portal/sites/"+s.name+"/domains/"+s.www, map[string]any{"mode": "serve"}); status != http.StatusOK {
		t.Fatalf("setting www to serve answered %d", status)
	}
	if _, text, _ := s.admin.callRaw("manage_site", map[string]any{"command": "remove_domain", "hostname": s.bare}); !strings.Contains(text, `"removed"`) {
		t.Fatalf("removing the freed primary: %s", text)
	}
}

// TestIssue2687_PromoteLeavesTheRolesAndEveryReadShowsThem: a promotion leaves
// the primary and the redirect as they were, and manage_site get, the portal
// read and the administrator's domain list each show every domain's role.
func TestIssue2687_PromoteLeavesTheRolesAndEveryReadShowsThem(t *testing.T) {
	s := newSite2687(t)
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.bare, "public": true, "primary": true})
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.www, "public": true, "mode": "redirect"})
	files := []map[string]any{{"path": "index.html", "content": s.releaseBody + " two"}}
	s.admin.call("manage_site", map[string]any{"command": "publish", "site": s.name, "files": files})
	s.admin.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"})
	s.redirect(t, s.www, http.MethodGet, "/")

	want := map[string]string{s.bare: "serve/primary", s.www: "redirect/" + s.bare}
	read := func(label string, domains []any) {
		got := map[string]string{}
		for _, d := range domains {
			m, _ := d.(map[string]any)
			host, _ := m["hostname"].(string)
			if host != s.bare && host != s.www {
				continue
			}
			role := m["mode"].(string) + "/"
			if m["primary"] == true {
				role += "primary"
			} else {
				role += text2687(m["redirects_to"])
			}
			got[host] = role
		}
		for h, w := range want {
			if got[h] != w {
				t.Errorf("%s shows %s as %q, want %q", label, h, got[h], w)
			}
		}
	}
	view := s.admin.call("manage_site", map[string]any{"command": "get", "site": s.name})
	read("manage_site get", list2687(view["domains"]))
	_, portal := s.admin.rest(http.MethodGet, "/api/v1/portal/sites/"+s.name, http.NoBody)
	read("the portal read", list2687(portal["domains"]))
	_, adminList := s.admin.rest(http.MethodGet, "/api/v1/admin/sites/domains", http.NoBody)
	read("GET /api/v1/admin/sites/domains", list2687(adminList["addresses"]))
	if got := text2687(view["production_address"]); !strings.Contains(got, s.bare) {
		t.Errorf("the production address is %q, want the primary %s", got, s.bare)
	}
}

// TestIssue2687_ARedirectToAPrivatePrimaryAsksForTheSignIn: when the primary
// is private, its redirect domain does not hand its hostname to anyone.
func TestIssue2687_ARedirectToAPrivatePrimaryAsksForTheSignIn(t *testing.T) {
	s := newSite2687(t)
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.bare, "primary": true})
	s.admin.call("manage_site", map[string]any{"command": "add_domain", "site": s.name, "environment": "prod", "hostname": s.www, "public": true, "mode": "redirect"})
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.www, http.MethodGet, "/a", nil, "")
		return r, r.status != http.StatusNotFound || !strings.Contains(r.body, "not published")
	})
	if got.status == http.StatusMovedPermanently || strings.Contains(got.header.Get("Location"), s.bare+"/a") {
		t.Fatalf("an anonymous request to a private primary's redirect was redirected to it: %d %q", got.status, got.header.Get("Location"))
	}
}

func list2687(v any) []any {
	l, _ := v.([]any)
	return l
}

func text2687(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
}

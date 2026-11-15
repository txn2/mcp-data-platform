//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Acceptance for #2632: an environment other than dev can redirect to another
// https site for a while. Every address answers 302 with no-store, keeping the
// path when asked and never letting the path change the target's host; the
// target is checked when it is set; on an environment that requires approval a
// non-administrator's redirect waits in Approvals. The criteria use a prod with
// a public domain.
//
// Wire forms: PUT /api/v1/portal/sites/{site}/environments/{name} takes status
// and redirect_to as strings and keep_path as a boolean, absent and present;
// manage_site set_environment takes the same as literal tools/call params. A
// request to a site is a GET or HEAD naming the hostname in Host.

const target2632 = "https://status.example.com"

// redirected2632 waits until host answers 302 on every replica.
func redirected2632(t *testing.T, host, path string) pageResponse {
	t.Helper()
	return settledOn2631(t, host, path, nil, func(r pageResponse) bool { return r.status == http.StatusFound })
}

// TestIssue2632_RedirectWithAndWithoutThePath: without keep_path every path
// goes to the target as written; with it /pricing/?a=1 keeps its path and
// query, and //evil.example/x lands on the target's host. HEAD answers alike.
func TestIssue2632_RedirectWithAndWithoutThePath(t *testing.T) {
	s, domain := offlineSite2631(t)
	s.setEnv(t, s.key, "prod", map[string]any{"status": "redirect", "redirect_to": target2632})
	r := redirected2632(t, domain, "/pricing/?a=1")
	if r.header.Get("Location") != target2632 || r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("without keep_path: %v", r.header)
	}
	if h := onHost(t, domain, http.MethodHead, "/pricing/?a=1", nil, ""); h.status != http.StatusFound || h.header.Get("Location") != target2632 {
		t.Errorf("HEAD: %d %v", h.status, h.header)
	}

	connectAs(t, s.key).call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "prod", "status": "redirect", "redirect_to": target2632, "keep_path": true})
	r = settledOn2631(t, domain, "/pricing/?a=1", nil, func(r pageResponse) bool { return r.header.Get("Location") == target2632+"/pricing/?a=1" })
	if r.status != http.StatusFound {
		t.Errorf("with keep_path: %d", r.status)
	}
	evil := onHost(t, domain, http.MethodGet, "//evil.example/x", nil, "")
	if u, err := url.Parse(evil.header.Get("Location")); err != nil || u.Host != "status.example.com" {
		t.Errorf("//evil.example/x went to %q", evil.header.Get("Location"))
	}
}

// TestIssue2632_ATargetIsCheckedWhenSet: each refused target is refused naming
// its rule, through the portal and manage_site.
func TestIssue2632_ATargetIsCheckedWhenSet(t *testing.T) {
	s, domain := offlineSite2631(t)
	for target, rule := range map[string]string{
		"javascript:alert(1)":                            "https://",
		"http://x.example":                               "https://",
		"https://u:p@x.example":                          "user name or password",
		"/relative":                                      "https://",
		"https://" + domain:                              "loop",
		"https://x." + pagesDomain2517:                   "loop",
		"https://x.example/a\r\nb":                       "control character",
		"https://x.example/" + strings.Repeat("a", 2100): "at most 2048",
	} {
		status, got := s.rest(t, s.key, http.MethodPut, "/environments/prod", map[string]any{"status": "redirect", "redirect_to": target})
		if status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), rule) {
			t.Errorf("%q: %d %v", firstRunes(target, 40), status, got["_raw"])
		}
		_, text, _ := connectAs(t, s.key).callRaw("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "prod", "status": "redirect", "redirect_to": target})
		if !strings.Contains(text, rule) {
			t.Errorf("manage_site %q: %s", firstRunes(target, 40), text)
		}
	}
	served2518(t, domain, "live")
}

// TestIssue2632_ARedirectOnAnApprovalEnvironmentWaits: with prod requiring
// approval, the owner's redirect waits and prod keeps serving; once an
// administrator approves, the owner's redirect is live. An administrator's own
// redirect applies at once, and Redirecting moves to Offline and Online
// directly.
func TestIssue2632_ARedirectOnAnApprovalEnvironmentWaits(t *testing.T) {
	s, domain := offlineSite2631(t)
	s.setEnv(t, keyAdmin2604, "prod", map[string]any{"requires_approval": true})
	body := map[string]any{"status": "redirect", "redirect_to": target2632, "reason": "migration"}
	got := s.setEnv(t, s.key, "prod", body)
	if got["status"] != "awaiting_approval" {
		t.Fatalf("the owner's redirect of an approval environment: %v", got["_raw"])
	}
	served2518(t, domain, "live")
	req, _ := got["approval"].(map[string]any)
	id, _ := req["id"].(string)
	decideInPortal2643(t, id)
	if got = s.setEnv(t, s.key, "prod", body); got["status"] != "done" {
		t.Fatalf("after the approval: %v", got["_raw"])
	}
	redirected2632(t, domain, "/")

	s.setEnv(t, keyAdmin2604, "prod", map[string]any{"status": "redirect", "redirect_to": "https://other.example.com"})
	r := settledOn2631(t, domain, "/", nil, func(r pageResponse) bool { return r.header.Get("Location") == "https://other.example.com" })
	if r.status != http.StatusFound {
		t.Errorf("an administrator's redirect: %d", r.status)
	}
	s.setEnv(t, s.key, "prod", map[string]any{"status": "offline"})
	offline2631(t, domain, "/", nil)
	s.setEnv(t, s.key, "prod", map[string]any{"status": "online"})
	served2518(t, domain, "live")
}

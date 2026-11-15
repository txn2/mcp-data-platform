//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2631: an environment other than dev can be taken offline
// with a message. Every address answers 503 with the message, Retry-After while
// the expected-back time is ahead, no-store and noindex, behind the access
// check; the release pointer is untouched, so a promote while offline lands
// and Online serves it. The criteria use a prod with a public domain.
//
// Wire forms: PUT /api/v1/portal/sites/{site}/environments/{name} takes status
// as a string, message as a string (absent and present) and back_at as an RFC
// 3339 string (absent and present); manage_site set_environment takes the same
// as literal tools/call params. A request to a site is a GET or HEAD naming the
// hostname in Host, with and without a site sign-in cookie.

// offlineSite2631 is a site whose prod serves "live" at a public domain, with
// pricing and image paths beside its home page.
func offlineSite2631(t *testing.T) (s *site2604, domain string) {
	t.Helper()
	s = newSite2604(t, keyOwner2604, "acc2631-")
	connectAs(t, s.key).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "live",
		"files": []map[string]any{
			{"path": "index.html", "content": "<h1>live</h1><p>" + marker2604 + "</p>"},
			{"path": "pricing/index.html", "content": "<h1>pricing</h1>"},
			{"path": "img/logo.png", "content": "iVBORw0KGgo=", "encoding": "base64"},
		},
	})
	s.promote(t, "dev", "prod")
	domain = s.name + ".example.test"
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": domain, "environment": "prod", "public": true})
	want2559(t, "adding prod's public domain", status, http.StatusCreated, got)
	served2518(t, domain, "live")
	return s, domain
}

// offline2631 waits until host answers 503 on every replica.
func offline2631(t *testing.T, host, path string, headers map[string]string) pageResponse {
	t.Helper()
	return settledOn2631(t, host, path, headers, func(r pageResponse) bool { return r.status == http.StatusServiceUnavailable })
}

// settledOn2631 waits until six answers in a row from host pass ok, so every
// replica has read the change.
func settledOn2631(t *testing.T, host, path string, headers map[string]string, ok func(pageResponse) bool) pageResponse {
	t.Helper()
	in := 0
	return pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, path, headers, "")
		if ok(r) {
			in++
		} else {
			in = 0
		}
		return r, in >= 6
	})
}

// TestIssue2631_TakingProdOfflineAnswers503Everywhere: with message "Back at
// 2pm" and a back_at an hour ahead, the public domain answers 503 on GET and
// HEAD for /, /pricing/ and /img/logo.png with the message, Retry-After,
// no-store and noindex and nothing of the release; the main address and
// <site>--prod ask someone not signed in to sign in, and show a signed-in
// person the offline page.
func TestIssue2631_TakingProdOfflineAnswers503Everywhere(t *testing.T) {
	s, domain := offlineSite2631(t)
	back := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	got := s.setEnv(t, s.key, "prod", map[string]any{"status": "offline", "message": "Back at 2pm", "back_at": back})
	if got["status"] != "done" {
		t.Fatalf("taking prod offline: %v", got["_raw"])
	}
	offline2631(t, domain, "/", nil)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/", "/pricing/", "/img/logo.png"} {
			r := onHost(t, domain, method, path, nil, "")
			if r.status != http.StatusServiceUnavailable {
				t.Errorf("%s %s answered %d", method, path, r.status)
				continue
			}
			if r.header.Get("Retry-After") == "" || r.header.Get("Cache-Control") != "no-store" || r.header.Get("X-Robots-Tag") != "noindex" {
				t.Errorf("%s %s headers: %v", method, path, r.header)
			}
			if strings.Contains(r.body, marker2604) || strings.Contains(r.body, "pricing</h1>") {
				t.Errorf("%s %s served the release while offline", method, path)
			}
			if method == http.MethodGet && !strings.Contains(r.body, "Back at 2pm") {
				t.Errorf("GET %s does not show the message: %s", path, firstRunes(r.body, 300))
			}
		}
	}
	for _, host := range []string{s.host(""), s.host("prod")} {
		signInPage2619(t, host)
		cookie := signIn2604(t, host, emailAdminKC2604, passAdminKC2604)
		r := offline2631(t, host, "/", withCookie2559(cookie))
		if !strings.Contains(r.body, "Back at 2pm") {
			t.Errorf("a signed-in person on %s sees no message: %s", host, firstRunes(r.body, 300))
		}
	}
}

// TestIssue2631_TheMessageIsText: a message holding a script element appears as
// text, and the page holds no script element.
func TestIssue2631_TheMessageIsText(t *testing.T) {
	s, domain := offlineSite2631(t)
	s.setEnv(t, s.key, "prod", map[string]any{"status": "offline", "message": "<script>alert(1)</script>"})
	r := offline2631(t, domain, "/", nil)
	if strings.Contains(strings.ToLower(r.body), "<script") || !strings.Contains(r.body, "&lt;script&gt;") {
		t.Errorf("the message is not text: %s", firstRunes(r.body, 600))
	}
	if r.header.Get("Retry-After") != "" {
		t.Errorf("Retry-After with no back_at: %v", r.header)
	}
}

// TestIssue2631_PromoteWhileOfflineThenBringItBack: a promote of stage to prod
// while prod is offline succeeds and prod still answers 503; bringing it online
// serves the promoted release, and the same release with the same ETag as
// before it went offline when nothing was promoted.
func TestIssue2631_PromoteWhileOfflineThenBringItBack(t *testing.T) {
	s, domain := offlineSite2631(t)
	before := onHost(t, domain, http.MethodGet, "/", nil, "")
	agent := connectAs(t, s.key)
	agent.call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "prod", "status": "offline"})
	offline2631(t, domain, "/", nil)
	agent.call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "prod", "status": "online"})
	after := settledOn2631(t, domain, "/", nil, func(r pageResponse) bool { return r.status == http.StatusOK })
	if after.body != before.body || after.header.Get("ETag") != before.header.Get("ETag") {
		t.Errorf("online serves other bytes than before: %q vs %q", after.header.Get("ETag"), before.header.Get("ETag"))
	}

	s.setEnv(t, s.key, "prod", map[string]any{"status": "offline"})
	s.publish(t, s.key, "next")
	s.promote(t, "dev", "prod")
	offline2631(t, domain, "/", nil)
	s.setEnv(t, s.key, "prod", map[string]any{"status": "online"})
	served2518(t, domain, "next")
}

// TestIssue2631_WhoMayTakeItOffline: dev cannot be taken offline; someone who
// does not manage the site is refused, through the portal and manage_site.
func TestIssue2631_WhoMayTakeItOffline(t *testing.T) {
	s, _ := offlineSite2631(t)
	if status, got := s.rest(t, s.key, http.MethodPut, "/environments/dev", map[string]any{"status": "offline"}); status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), "always online") {
		t.Errorf("dev offline: %d %v", status, got["_raw"])
	}
	if status, got := s.rest(t, keyPeer2604, http.MethodPut, "/environments/prod", map[string]any{"status": "offline"}); status != http.StatusForbidden {
		t.Errorf("someone who does not manage the site: %d %v", status, got["_raw"])
	}
	if res, text, _ := connectAs(t, keyPeer2604).callRaw("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "prod", "status": "offline"}); !res.IsError {
		t.Errorf("manage_site let someone who does not manage the site take prod offline: %s", text)
	}
}

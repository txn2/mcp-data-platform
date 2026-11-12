//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2619: every generated address (a site's main address and
// each <site>--<env> on the pages domain) answers only to someone signed in,
// and only an added domain, set public, answers anyone. Public is a setting of
// a domain: added private unless asked for, changed on its own, refused on an
// environment naming the domain setting. Every request here goes through the
// dev stack's replica proxy (baseURL), naming the address in Host.
//
// Wire forms: POST /api/v1/portal/sites/{site}/domains takes a JSON object
// whose hostname and environment are strings and public a boolean (sent true,
// and left out for a private domain); PUT .../domains/{hostname} takes public
// as a boolean; PUT .../environments/{name} takes public as a boolean, which is
// refused. manage_site takes command, site, hostname and environment as
// strings and public as a boolean. A request to a site is an HTTP GET naming
// the hostname in Host, with no cookie and with a site's sign-in cookie.

// signInPage2619 reports whether an anonymous answer is the sign-in and holds
// nothing of the release.
func signInPage2619(t *testing.T, host string) {
	t.Helper()
	r := onHost(t, host, http.MethodGet, "/", nil, "")
	if r.status == http.StatusOK || strings.Contains(r.body, marker2604) {
		t.Errorf("%s answered someone who has not signed in: %d %s", host, r.status, firstRunes(r.body, 200))
	}
	if r.status != http.StatusFound || !strings.Contains(r.header.Get("Location"), "/sites/handoff") {
		t.Errorf("%s did not send someone who has not signed in to the sign-in: %d %v", host, r.status, r.header.Get("Location"))
	}
}

// settled2619 waits until settledRuns answers in a row from host pass ok, so
// every replica the proxy spreads requests over has read the route again (each
// does within a moment of a change, sitegw routeTTL).
func settled2619(t *testing.T, host string, headers map[string]string, ok func(pageResponse) bool) pageResponse {
	t.Helper()
	const settledRuns = 6
	in := 0
	return pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, "/", headers, "")
		if ok(r) {
			in++
		} else {
			in = 0
		}
		return r, in >= settledRuns
	})
}

// publicHost2619 is the domain publicDomain adds to env.
func publicHost2619(s *site2604, env string) string { return env + "." + s.name + ".example.test" }

// publicDomain adds a domain to env, public, as an administrator: the one way
// an environment answers someone who has not signed in (#2619).
func (s *site2604) publicDomain(t *testing.T, env string) string {
	t.Helper()
	host := publicHost2619(s, env)
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": host, "environment": env, "public": true})
	want2559(t, "adding "+env+"'s public domain", status, http.StatusCreated, got)
	if got["public"] != true {
		t.Fatalf("the domain was not added public: %v", got["_raw"])
	}
	return host
}

// publicSite2619 is a site whose prod serves a release, with a public domain.
func publicSite2619(t *testing.T) (s *site2604, domain string) {
	t.Helper()
	s = newSite2604(t, keyOwner2604, "acc2619-")
	domain = s.name + ".example.test"
	s.publish(t, s.key, "live")
	s.promote(t, "dev", "prod")
	status, got := s.rest(t, s.key, http.MethodPost, "/domains", map[string]any{"hostname": domain, "environment": "prod", "public": true})
	want2559(t, "adding prod's domain public", status, http.StatusCreated, got)
	if got["public"] != true || got["status"] != "done" {
		t.Fatalf("the domain was not added public: %v", got["_raw"])
	}
	return s, domain
}

// TestIssue2619_OnlyThePublicDomainAnswersAnyone: with d4l.example.test added
// public on prod, anonymous requests to it are answered; anonymous requests to
// the main address and d4l--prod get the sign-in and no release content; a
// signed-in person the environment opens for is answered at both.
func TestIssue2619_OnlyThePublicDomainAnswersAnyone(t *testing.T) {
	s, domain := publicSite2619(t)
	served2518(t, domain, "live")
	signInPage2619(t, s.host(""))
	signInPage2619(t, s.host("prod"))

	s.share(t, s.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"prod"}})
	for _, host := range []string{s.host(""), s.host("prod")} {
		member := signIn2604(t, host, emailKCAnalyst, passKCAnalyst)
		opens2604(t, host, member, "live")
	}
}

// TestIssue2619_PublicIsADomainSetting: making an environment public is
// refused through the portal API and manage_site, naming the domain setting.
func TestIssue2619_PublicIsADomainSetting(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2619-env-")
	const want = "public is a setting of a domain, not of an environment"
	status, got := s.rest(t, s.key, http.MethodPut, "/environments/prod", map[string]any{"public": true})
	if status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), want) {
		t.Errorf("the portal: %d %v", status, got["_raw"])
	}
	_, text, _ := connectAs(t, s.key).callRaw("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "prod", "public": true})
	if !strings.Contains(text, want) || !strings.Contains(text, "set_domain") {
		t.Errorf("manage_site: %s", text)
	}
}

// TestIssue2619_ADomainIsPrivateUntilSetPublic: a domain added without public
// asks for a sign-in; a second domain added private on the same environment
// asks for one while the first answers anyone; set_domain opens and closes it.
func TestIssue2619_ADomainIsPrivateUntilSetPublic(t *testing.T) {
	s, public := publicSite2619(t)
	private := "beta." + public
	status, got := s.rest(t, s.key, http.MethodPost, "/domains", map[string]any{"hostname": private, "environment": "prod"})
	want2559(t, "adding a second domain", status, http.StatusCreated, got)
	if got["public"] != false {
		t.Errorf("a domain added without public is %v", got["public"])
	}
	served2518(t, public, "live")
	asksSignIn := func(r pageResponse) bool { return r.status == http.StatusFound }
	settled2619(t, private, nil, asksSignIn)
	signInPage2619(t, private)

	opened := connectAs(t, s.key).call("manage_site", map[string]any{"command": "set_domain", "hostname": private, "public": true})
	if opened["public"] != true {
		t.Fatalf("set_domain public true: %v", opened)
	}
	served2518(t, private, "live")
	status, got = s.rest(t, s.key, http.MethodPut, "/domains/"+private, map[string]any{"public": false})
	want2559(t, "making the domain private", status, http.StatusOK, got)
	settled2619(t, private, nil, asksSignIn)
	signInPage2619(t, private)
}

// TestIssue2619_CachingFollowsTheAddress: the generated addresses answer
// Cache-Control: private, no-cache to someone signed in; the public domain
// answers as a public answer does.
func TestIssue2619_CachingFollowsTheAddress(t *testing.T) {
	s, domain := publicSite2619(t)
	public := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, domain, http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusOK
	})
	if got := public.header.Get("Cache-Control"); got != "public, max-age=0, must-revalidate" {
		t.Errorf("the public domain's page answers Cache-Control %q", got)
	}
	for _, host := range []string{s.host(""), s.host("prod")} {
		owner := signIn2604(t, host, emailAdminKC2604, passAdminKC2604)
		r := pollUntil2518(t, func() (pageResponse, bool) {
			r := onHost(t, host, http.MethodGet, "/", withCookie2559(owner), "")
			return r, r.status == http.StatusOK
		})
		if got := r.header.Get("Cache-Control"); got != "private, no-cache" {
			t.Errorf("%s answers Cache-Control %q to someone signed in", host, got)
		}
	}
}

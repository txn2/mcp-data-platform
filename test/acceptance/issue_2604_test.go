//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Acceptance for #2604: agents work in dev, people use what was promoted. dev
// is the working copy every publish, patch and edit lands in; every other
// environment moves only by promote and rollback, is private or public on its
// own, answers at <site>--<env>.<pages domain> (one of them, or none, at the
// main address <site>.<pages domain>), and is shared with people and personas.
// A private environment hands the site who signed in (/_viewer) and runs its
// application's endpoints as that person. Since #2617 a new site holds dev and
// prod, prod on the main address; since #2619 public is a setting of a domain,
// and every address on the pages domain asks for a sign-in.
//
// Wire forms: REST bodies are JSON objects whose fields are strings, booleans
// and lists of strings as each route documents (environments is a list of
// strings; public, internal_view and requires_approval are booleans and may be
// left out). manage_site takes command, site, name, environment, persona, email,
// level and main_environment as strings, environments as a list of strings,
// public, main and requires_approval as booleans, and files as a list of
// objects whose content is utf-8 text. manage_asset takes action, asset_id,
// version and edits. A request to a site is an HTTP GET naming the hostname in Host, with
// and without Cookie and Sec-Fetch-Site; /sites/handoff takes site, host, path
// and state in its query.

const (
	keyAdmin2604       = "acme-dev-key-2024"
	keyOwner2604       = "acme-owner-key" // asset.owner@example.com, collaborator
	keyPeer2604        = "acme-peer-key"  // asset.peer@example.com, collaborator
	emailPeer2604      = "asset.peer@example.com"
	emailAdminKC2604   = "admin@example.com"
	passAdminKC2604    = "admin-password"
	personaAnalyst2604 = "inventory-analyst" // analyst@example.com's persona
	marker2604         = "release-marker-2604"
)

// site2604 is one site under test, owned by the holder of key.
type site2604 struct {
	name string
	key  string
}

func newSite2604(t *testing.T, key, prefix string) *site2604 {
	t.Helper()
	name := prefix + unique1579()
	status, got := rest2559(t, key, http.MethodPost, portal2559, map[string]any{"name": name, "description": "Acceptance 2604"})
	want2559(t, "creating a site", status, http.StatusCreated, got)
	t.Cleanup(func() { _, _ = rest2559(t, keyAdmin2604, http.MethodDelete, portal2559+"/"+name, nil) })
	return &site2604{name: name, key: key}
}

func (s *site2604) path(rest string) string { return portal2559 + "/" + s.name + rest }

// host is an environment's address on the pages domain; "" is the main address.
func (s *site2604) host(env string) string {
	if env == "" {
		return s.name + "." + pagesDomain2517
	}
	return s.name + "--" + env + "." + pagesDomain2517
}

func (s *site2604) rest(t *testing.T, key, method, rest string, body any) (int, map[string]any) {
	t.Helper()
	return rest2559(t, key, method, s.path(rest), body)
}

func (s *site2604) setEnv(t *testing.T, key, env string, body map[string]any) map[string]any {
	t.Helper()
	status, got := s.rest(t, key, http.MethodPut, "/environments/"+env, body)
	want2559(t, "setting "+env, status, http.StatusOK, got)
	return got
}

func (s *site2604) share(t *testing.T, key string, body map[string]any) {
	t.Helper()
	status, got := s.rest(t, key, http.MethodPut, "/access", body)
	want2559(t, "sharing", status, http.StatusOK, got)
}

// publish is an agent publishing to dev through manage_site.
func (s *site2604) publish(t *testing.T, key, version string) map[string]any {
	t.Helper()
	return connectAs(t, key).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": version,
		"files": []map[string]any{{"path": "index.html", "content": "<h1>" + version + "</h1><p>" + marker2604 + "</p>"}},
	})
}

// promote moves from's release to to; a request that waits on Approvals is
// approved by an administrator and finished by the owner.
func (s *site2604) promote(t *testing.T, from, to string) {
	t.Helper()
	body := map[string]any{"from": from, "to": to, "reason": "acceptance 2604"}
	status, got := s.rest(t, s.key, http.MethodPost, "/promote", body)
	want2559(t, "promoting", status, http.StatusOK, got)
	if got["status"] == "awaiting_approval" {
		approve2604(t, got)
		status, got = s.rest(t, s.key, http.MethodPost, "/promote", body)
		want2559(t, "finishing the promote", status, http.StatusOK, got)
	}
	if got["status"] != "promoted" {
		t.Fatalf("promoting %s to %s: %v", from, to, got["_raw"])
	}
}

func approve2604(t *testing.T, got map[string]any) {
	t.Helper()
	req, _ := got["approval"].(map[string]any)
	id, _ := req["id"].(string)
	if id == "" {
		t.Fatalf("no approval request: %v", got["_raw"])
	}
	connectAs(t, keyAdmin2604).call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"})
}

// signIn2604 signs a person in through Keycloak and walks the handoff to host,
// as a browser does, returning the cookie that host set.
func signIn2604(t *testing.T, host, email, password string) string {
	t.Helper()
	hc := browserSignIn(t, baseURL(), email, password)
	start := onHost(t, host, http.MethodGet, "/_signin?path=%2F", nil, "")
	state := cookieOf2517(start.header, "site_state")
	loc, err := url.Parse(start.header.Get("Location"))
	if err != nil || state == "" || start.status != http.StatusFound || !strings.HasSuffix(loc.Path, "/sites/handoff") {
		t.Fatalf("signing in on %s did not start the handoff: %d %v", host, start.status, start.header)
	}
	if loc.Query().Get("host") != host {
		t.Errorf("the handoff does not name the host it started on: %s", loc.RawQuery)
	}
	manual := &http.Client{Jar: hc.Jar, Timeout: hc.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := manual.Get(baseURL() + loc.RequestURI()) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("the portal handoff: %v", err)
	}
	_ = res.Body.Close()
	back, err := url.Parse(res.Header.Get("Location"))
	if err != nil || res.StatusCode != http.StatusFound || back.Hostname() != host || back.Path != "/_auth" {
		t.Fatalf("the handoff for %s answered %d to %q", host, res.StatusCode, res.Header.Get("Location"))
	}
	code := back.Query().Get("code")
	done := onHost(t, host, http.MethodGet, back.RequestURI(), map[string]string{"Cookie": "site_state=" + state}, "")
	cookie := cookieOf2517(done.header, "site_viewer")
	if done.status != http.StatusFound || cookie == "" {
		t.Fatalf("completing the handoff on %s: %d %s", host, done.status, done.body)
	}
	searchLogs2518(t, code)
	searchLogs2518(t, cookie)
	return cookie
}

// opens waits for host to serve want to the cookie.
func opens2604(t *testing.T, host, cookie, want string) {
	t.Helper()
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, "/", withCookie2559(cookie), "")
		return r, r.status == http.StatusOK && strings.Contains(r.body, "<h1>"+want+"</h1>")
	})
}

// refused waits for host to refuse the cookie with the no-access page.
func refused2604(t *testing.T, host, cookie, what string) {
	t.Helper()
	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, "/", withCookie2559(cookie), "")
		return r, r.status == http.StatusForbidden
	})
	if strings.Contains(r.body, marker2604) || !strings.Contains(r.body, "You don't have access to this site") {
		t.Errorf("%s: the refusal is not the no-access page, or holds the release: %s", what, r.body)
	}
}

// UC1: an intranet whose dev is for the people who develop it and whose prod,
// on the main address, is for a persona.
func TestIssue2604_IntranetDevIsForItsDevelopersAndProdForThePersona(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-intranet-")
	s.setEnv(t, s.key, "prod", map[string]any{"requires_approval": true})
	status, got := s.rest(t, s.key, http.MethodPut, "/main-environment", map[string]any{"environment": "prod"})
	want2559(t, "giving prod the main address", status, http.StatusOK, got)
	s.share(t, s.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"prod"}})

	s.publish(t, s.key, "first")
	s.promote(t, "dev", "prod")
	s.publish(t, s.key, "second")

	developer := signIn2604(t, s.host("dev"), emailAdminKC2604, passAdminKC2604)
	opens2604(t, s.host("dev"), developer, "second")

	member := signIn2604(t, s.host(""), emailKCAnalyst, passKCAnalyst)
	opens2604(t, s.host(""), member, "first")
	memberOnDev := signIn2604(t, s.host("dev"), emailKCAnalyst, passKCAnalyst)
	refused2604(t, s.host("dev"), memberOnDev, "a persona member on dev")

	outsider := signIn2604(t, s.host(""), emailKCClient, passKCClient)
	refused2604(t, s.host(""), outsider, "someone outside both on prod")
	outsiderOnDev := signIn2604(t, s.host("dev"), emailKCClient, passKCClient)
	refused2604(t, s.host("dev"), outsiderOnDev, "someone outside both on dev")

	s.promote(t, "dev", "prod")
	opens2604(t, s.host(""), member, "second")
}

// UC2: a client site whose dev is for a persona, whose stage is the client's,
// and whose prod is public on a domain after approval.
func TestIssue2604_ClientSiteStageForTheClientAndProdPublicAfterApproval(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-cool-")
	domain := s.name + ".example.test"
	s.share(t, s.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"dev"}})
	s.setEnv(t, s.key, "stage", map[string]any{})
	s.share(t, s.key, map[string]any{"email": emailKCClient, "level": "view", "environments": []string{"stage"}})
	status, got := s.rest(t, s.key, http.MethodPost, "/domains", map[string]any{"hostname": domain, "environment": "prod", "requires_approval": true})
	want2559(t, "adding prod's domain", status, http.StatusCreated, got)
	if r := onHost(t, domain, http.MethodGet, "/", nil, ""); r.status == http.StatusOK {
		t.Fatalf("a domain an agent or manager added answers before anyone made it public: %d", r.status)
	}

	s.publish(t, s.key, "cool")
	webTeam := signIn2604(t, s.host("dev"), emailKCAnalyst, passKCAnalyst)
	opens2604(t, s.host("dev"), webTeam, "cool")

	s.promote(t, "dev", "stage")
	client := signIn2604(t, s.host("stage"), emailKCClient, passKCClient)
	opens2604(t, s.host("stage"), client, "cool")
	clientOnDev := signIn2604(t, s.host("dev"), emailKCClient, passKCClient)
	refused2604(t, s.host("dev"), clientOnDev, "the client on dev")

	asked := s.domainAccess2604(t, s.key, domain, map[string]any{"public": true, "reason": "launch"})
	if asked["status"] != "awaiting_approval" {
		t.Fatalf("making an approval environment's domain public did not wait: %v", asked["_raw"])
	}
	if r := onHost(t, domain, http.MethodGet, "/", nil, ""); r.status == http.StatusOK {
		t.Fatal("prod's domain went public before approval")
	}
	approve2604(t, asked)
	done := s.domainAccess2604(t, s.key, domain, map[string]any{"public": true})
	if done["status"] != "done" {
		t.Fatalf("finishing make public: %v", done["_raw"])
	}
	s.promote(t, "stage", "prod")
	served2518(t, domain, "cool")
}

// An agent never changes what people use: publish, an overlay and an in-place
// edit aimed at prod or stage are refused, naming dev and promote.
func TestIssue2604_AgentsCannotTouchWhatPeopleUse(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-agent-")
	s.setEnv(t, s.key, "stage", map[string]any{})
	s.setEnv(t, s.key, "prod", map[string]any{"requires_approval": true})
	s.publish(t, s.key, "base")
	s.promote(t, "dev", "stage")
	agent := connectAs(t, s.key)
	for _, env := range []string{"prod", "stage"} {
		_, text, err := agent.callRaw("manage_site", map[string]any{
			"command": "publish", "site": s.name, "environment": env,
			"files": []map[string]any{{"path": "index.html", "content": "<h1>direct</h1>"}},
		})
		if err != nil || !strings.Contains(text, "is read-only") || !strings.Contains(text, "dev") || !strings.Contains(text, "promote") {
			t.Errorf("publish to %s: %v %s", env, err, text)
		}
		_, text, err = agent.callRaw("manage_site", map[string]any{
			"command": "publish", "site": s.name, "environment": env, "base": 1,
			"files": []map[string]any{{"path": "index.html", "content": "<h1>overlay</h1>"}},
		})
		if err != nil || !strings.Contains(text, "is read-only") {
			t.Errorf("an overlay on %s: %v %s", env, err, text)
		}
		_, text, err = agent.callRaw("manage_asset", map[string]any{
			"action": "patch", "asset_id": "mcp:site:" + s.name + "/" + env + "/index.html", "version": 1,
			"edits": []map[string]any{{"find": "base", "replace": "patched"}},
		})
		if err != nil || !strings.Contains(text, "is read-only") {
			t.Errorf("an edit on %s: %v %s", env, err, text)
		}
	}
	served2518For(t, s, "stage", "base")
}

// served2518For reads a private environment as an administrator signed in.
func served2518For(t *testing.T, s *site2604, env, want string) {
	t.Helper()
	opens2604(t, s.host(env), signIn2604(t, s.host(env), emailAdminKC2604, passAdminKC2604), want)
}

// A persona grant is matched against the persona on every request: a person
// whose persona stops matching is refused on their next request, and let in
// again on the one after it matches.
func TestIssue2604_APersonaChangeAppliesOnTheNextRequest(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-persona-")
	s.setEnv(t, s.key, "prod", map[string]any{})
	s.publish(t, s.key, "team")
	s.promote(t, "dev", "prod")
	team := "acc2604-team-" + unique1579()
	admin := connectAs(t, keyAdmin2604)
	persona := func(roles []string) map[string]any {
		return map[string]any{
			"name": team, "display_name": "Acceptance 2604 team", "roles": roles, "allow_tools": []string{"*"},
			"allow_connections": []string{"*"}, "priority": 10000,
		}
	}
	if code := admin.restJSON(http.MethodPost, "/api/v1/admin/personas", persona([]string{"acc2604-nobody"})); code != http.StatusCreated {
		t.Fatalf("creating the persona: %d", code)
	}
	t.Cleanup(func() {
		_, _ = admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+team+"?confirm=true", http.NoBody)
	})
	holds := func(roles ...string) {
		t.Helper()
		if code := admin.restJSON(http.MethodPut, "/api/v1/admin/personas/"+team, persona(roles)); code != http.StatusOK {
			t.Fatalf("changing who the persona holds: %d", code)
		}
	}
	s.share(t, s.key, map[string]any{"persona": team, "level": "view", "environments": []string{"prod"}})

	member := signIn2604(t, s.host("prod"), emailKCAnalyst, passKCAnalyst)
	refused2604(t, s.host("prod"), member, "before the persona holds them")
	holds("dp_analyst")
	opens2604(t, s.host("prod"), member, "team")
	holds("acc2604-nobody")
	refused2604(t, s.host("prod"), member, "after leaving the persona, with the same sign-in")
	holds("dp_analyst")
	opens2604(t, s.host("prod"), member, "team")
	holds("acc2604-nobody")
}

// Making a domain public is the owner's or an administrator's, and waits on
// Approvals where its environment requires approval; an environment has no
// public setting of its own (#2619), and dev takes no domain.
func TestIssue2604_MakingADomainPublicIsHeld(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-public-")
	s.share(t, s.key, map[string]any{"email": emailPeer2604, "level": "manage"})
	s.setEnv(t, s.key, "stage", map[string]any{})
	s.setEnv(t, s.key, "prod", map[string]any{"requires_approval": true})
	stage, prod := "stage."+s.name+".example.test", "www."+s.name+".example.test"
	for host, env := range map[string]string{stage: "stage", prod: "prod"} {
		status, got := s.rest(t, s.key, http.MethodPost, "/domains", map[string]any{"hostname": host, "environment": env})
		want2559(t, "adding "+env+"'s domain", status, http.StatusCreated, got)
	}

	status, got := s.rest(t, keyPeer2604, http.MethodPut, "/domains/"+stage, map[string]any{"public": true})
	want2559(t, "someone who manages without owning", status, http.StatusForbidden, got)
	status, got = s.rest(t, s.key, http.MethodPut, "/environments/dev", map[string]any{"public": true})
	want2559(t, "a public dev", status, http.StatusUnprocessableEntity, got)
	if !strings.Contains(fmt.Sprint(got["_raw"]), "public is a setting of a domain") {
		t.Errorf("the refusal does not say why: %v", got["_raw"])
	}

	if done := s.domainAccess2604(t, s.key, stage, map[string]any{"public": true}); done["status"] != "done" {
		t.Errorf("the owner on an environment with no approval: %v", done["_raw"])
	}
	asked := s.domainAccess2604(t, s.key, prod, map[string]any{"public": true})
	if asked["status"] != "awaiting_approval" {
		t.Fatalf("the owner on prod did not wait: %v", asked["_raw"])
	}
	status, list := rest2559(t, keyAdmin2604, http.MethodGet, "/api/v1/portal/approvals", nil)
	want2559(t, "reading Approvals", status, http.StatusOK, list)
	if !strings.Contains(fmt.Sprint(list["_raw"]), "site_public") {
		t.Errorf("the request is not in Approvals: %v", list["_raw"])
	}
	approve2604(t, asked)
	if got := s.domainAccess2604(t, s.key, prod, map[string]any{"public": true}); got["status"] != "done" {
		t.Errorf("finishing after approval: %v", got["_raw"])
	}
	status, got = s.rest(t, keyPeer2604, http.MethodPut, "/domains/"+prod, map[string]any{"public": false})
	want2559(t, "someone who manages makes it private", status, http.StatusOK, got)
}

// domainAccess2604 makes a domain public or private through the portal.
func (s *site2604) domainAccess2604(t *testing.T, key, host string, body map[string]any) map[string]any {
	t.Helper()
	status, got := s.rest(t, key, http.MethodPut, "/domains/"+host, body)
	want2559(t, "setting "+host+"'s access", status, http.StatusOK, got)
	return got
}

// The main address moves between environments, and to none, with no ingress
// or DNS change: every environment keeps <site>--<env>.
func TestIssue2604_TheMainAddressMovesCleanly(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-main-")
	s.setEnv(t, s.key, "prod", map[string]any{})
	s.publish(t, s.key, "on-prod")
	s.promote(t, "dev", "prod")
	s.publish(t, s.key, "on-dev")

	status, got := s.rest(t, keyPeer2604, http.MethodPut, "/main-environment", map[string]any{"environment": "prod"})
	if status == http.StatusOK {
		t.Errorf("someone with no grant moved the main address: %v", got["_raw"])
	}
	status, got = s.rest(t, s.key, http.MethodPut, "/main-environment", map[string]any{"environment": "prod"})
	want2559(t, "moving the main address", status, http.StatusOK, got)
	served2518For(t, s, "", "on-prod")
	served2518For(t, s, "dev", "on-dev")
	served2518For(t, s, "prod", "on-prod")

	status, got = s.rest(t, s.key, http.MethodPut, "/main-environment", map[string]any{"environment": ""})
	want2559(t, "giving the main address to none", status, http.StatusOK, got)
	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.host(""), http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusNotFound
	})
	if !strings.Contains(r.body, "Nothing is published here") || strings.Contains(r.body, marker2604) {
		t.Errorf("the main address given to none: %s", r.body)
	}
}

// An environment whose address could not be one DNS label is refused when it
// is created, with the reason, through the portal and through manage_site.
func TestIssue2604_NamesAreRefusedWhereTheyMustBe(t *testing.T) {
	long := "acc2604-" + strings.Repeat("x", 51) // 59: dev's address would be 64
	status, got := rest2559(t, keyOwner2604, http.MethodPost, portal2559, map[string]any{"name": long})
	want2559(t, "a site whose dev address is too long", status, http.StatusUnprocessableEntity, got)
	if !strings.Contains(fmt.Sprint(got["_raw"]), "at most 63") {
		t.Errorf("the refusal does not say why: %v", got["_raw"])
	}
	_, text, _ := connectAs(t, keyOwner2604).callRaw("manage_site", map[string]any{"command": "create", "name": "xn"})
	if !strings.Contains(text, "international domain name") {
		t.Errorf("a site named xn: %s", text)
	}
	s := newSite2604(t, keyOwner2604, "acc2604-"+strings.Repeat("n", 30)+"-")
	status, got = s.rest(t, s.key, http.MethodPut, "/environments/"+strings.Repeat("e", 30), map[string]any{})
	want2559(t, "an environment whose address is too long", status, http.StatusUnprocessableEntity, got)
	if !strings.Contains(fmt.Sprint(got["_raw"]), "at most 63") {
		t.Errorf("the refusal does not say why: %v", got["_raw"])
	}
	_, text, _ = connectAs(t, keyAdmin2604).callRaw("manage_application", map[string]any{"command": "create", "name": s.name + "--prod"})
	if !strings.Contains(text, "--") || strings.Contains(text, "\"id\"") {
		t.Errorf("an application named like an environment's address was not refused: %s", text)
	}
}

// The handoff sends a person only to a host the store says is a private
// environment of the named site.
func TestIssue2604_TheHandoffCannotBeSteered(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-steer-")
	other := newSite2604(t, keyOwner2604, "acc2604-other-")
	s.setEnv(t, s.key, "stage", map[string]any{})
	public := s.publicDomain(t, "prod")
	otherDomain := other.name + ".example.test"
	status, got := other.rest(t, other.key, http.MethodPost, "/domains", map[string]any{"hostname": otherDomain, "environment": "prod"})
	want2559(t, "adding the other site's domain", status, http.StatusCreated, got)

	start := onHost(t, s.host("stage"), http.MethodGet, "/_signin?path=%2F", nil, "")
	loc, err := url.Parse(start.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	hc := browserSignIn(t, baseURL(), emailKCAnalyst, passKCAnalyst)
	manual := &http.Client{Jar: hc.Jar, Timeout: hc.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for what, host := range map[string]string{
		"a host nobody serves":        "evil.example",
		"another site's address":      other.host(""),
		"another site's domain":       otherDomain,
		"a public domain of the site": public,
		"no host":                     "",
	} {
		q := loc.Query()
		q.Set("host", host)
		res, err := manual.Get(baseURL() + loc.Path + "?" + q.Encode()) // #nosec G704 -- the platform under test
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || res.Header.Get("Location") != "" {
			t.Errorf("%s: %d to %q", what, res.StatusCode, res.Header.Get("Location"))
		}
	}
}

// What a site created before #2604 becomes: dev where default was, and the old
// name still works for one release with a note.
func TestIssue2604_TheOldNameOfDevStillWorksWithANote(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-alias-")
	got := connectAs(t, s.key).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "environment": "default",
		"files": []map[string]any{{"path": "index.html", "content": "<h1>aliased</h1>"}},
	})
	if got["environment"] != "dev" || !strings.Contains(fmt.Sprint(got["note"]), `"default" is now "dev"`) {
		t.Errorf("publish to default: %v", got)
	}
	status, view := s.rest(t, s.key, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, view)
	if view["main_environment"] != "prod" {
		t.Errorf("a new site's main address is not on prod: %v", view["_raw"])
	}
	if names := order2617(t, view); len(names) != 2 || names[0] != "dev" || names[1] != "prod" {
		t.Errorf("a new site holds dev and prod: %v", view["_raw"])
	}
}

// A private environment on a domain beside the portal asks for the platform's
// sign-in on that domain and returns to it; its sign-in cookie stays host-only,
// and the portal's session cookie names no Domain, so the browser never sends
// it to a site.
func TestIssue2604_APrivateDomainSignsInOnItselfAndNeverGetsThePortalSession(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2604-docs-")
	domain := s.name + ".example.test"
	s.setEnv(t, s.key, "prod", map[string]any{})
	status, got := s.rest(t, s.key, http.MethodPost, "/domains", map[string]any{"hostname": domain, "environment": "prod"})
	want2559(t, "adding a private domain", status, http.StatusCreated, got)
	status, got = s.rest(t, s.key, http.MethodPut, "/main-environment", map[string]any{"environment": ""})
	want2559(t, "no main address", status, http.StatusOK, got)
	s.share(t, s.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"prod"}})
	s.publish(t, s.key, "docs")
	s.promote(t, "dev", "prod")

	if r := onHost(t, domain, http.MethodGet, "/", nil, ""); r.status != http.StatusFound || !strings.Contains(r.header.Get("Location"), "host="+domain) {
		t.Errorf("a private domain signed out: %d %v", r.status, r.header)
	}
	cookie := signIn2604(t, domain, emailKCAnalyst, passKCAnalyst)
	opens2604(t, domain, cookie, "docs")
	if r := onHost(t, s.host(""), http.MethodGet, "/", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("the main address given to none answered %d", r.status)
	}
	hc := browserSignIn(t, baseURL(), emailKCAnalyst, passKCAnalyst)
	base, _ := url.Parse(baseURL())
	for _, c := range hc.Jar.Cookies(base) {
		if strings.Contains(c.Name, "session") && c.Domain != "" {
			t.Errorf("the portal session cookie names a Domain: %+v", c)
		}
	}
	if r := onHost(t, domain, http.MethodGet, "/", map[string]string{"Cookie": "site_viewer=" + cookie}, ""); r.status != http.StatusOK {
		t.Errorf("the site's own cookie on its own host: %d", r.status)
	}
}

// A private environment tells its pages who signed in, and an application's
// endpoints on the environment's own host run as that person; a public domain
// has nobody.
func TestIssue2604_APrivateSiteKnowsItsViewerAndRunsItsEndpointsAsThem(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2604-viewer-")
	s.setEnv(t, s.key, "open", map[string]any{})
	s.share(t, s.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"prod"}})
	s.publish(t, s.key, "app")
	s.promote(t, "dev", "open")
	s.promote(t, "open", "prod")
	open := s.publicDomain(t, "open")

	admin := connect(t)
	app := "acc2604-app-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "a site with endpoints"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	api := app + "-who"
	saveEndpointScript(t, admin, api, `def handle(req):
    """Answers who called."""
    who = None
    if req["caller"]:
        who = req["caller"]["email"]
    return {"json": {"who": who}}
`, `{"params": {}, "caller": None}`)
	addScriptPart2517(t, admin, app, api)
	bindEndpoint(t, admin, api, "", map[string]any{"endpoint": api, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})
	for _, env := range []string{"prod", "open"} {
		admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": s.name + "/" + env})
	}

	cookie := signIn2604(t, s.host("prod"), emailKCAnalyst, passKCAnalyst)
	viewer := onHost(t, s.host("prod"), http.MethodGet, "/_viewer", withCookie2559(cookie), "")
	if viewer.status != http.StatusOK || !strings.Contains(viewer.body, emailKCAnalyst) || !strings.Contains(viewer.body, personaAnalyst2604) {
		t.Errorf("/_viewer on a private environment: %d %s", viewer.status, viewer.body)
	}
	if strings.Contains(viewer.body, cookie) {
		t.Error("/_viewer repeats the sign-in cookie")
	}
	same := withCookie2559(cookie)
	same["Sec-Fetch-Site"] = "same-origin"
	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.host("prod"), http.MethodGet, "/endpoints/"+api+"/", same, "")
		return r, r.status == http.StatusOK
	})
	if !strings.Contains(r.body, emailKCAnalyst) {
		t.Errorf("the endpoint did not run as the viewer: %s", r.body)
	}
	sibling := withCookie2559(cookie)
	sibling["Sec-Fetch-Site"] = "same-site"
	if r := onHost(t, s.host("prod"), http.MethodGet, "/endpoints/"+api+"/", sibling, ""); r.status == http.StatusOK && strings.Contains(r.body, emailKCAnalyst) {
		t.Errorf("a sibling site's page ran the endpoint as the viewer: %s", r.body)
	}
	r = pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, open, http.MethodGet, "/_viewer", nil, "")
		return r, r.status != http.StatusNotFound
	})
	if r.status != http.StatusUnauthorized || !strings.Contains(r.body, "nobody is signed in") {
		t.Errorf("/_viewer on a public domain: %d %s", r.status, r.body)
	}
}

// The knowledge page and manage_site lead with the model.
func TestIssue2604_TheDocsLeadWithTheModel(t *testing.T) {
	c := connect(t)
	page := c.call("fetch", map[string]any{"reference": "mcp:knowledge_page:platform-pages-and-sites", "purpose": "Checking the sites guide leads with the dev and promote model."})
	if !strings.Contains(strings.ToLower(fmt.Sprint(page)), "agents work in dev") {
		t.Errorf("the knowledge page does not lead with the model")
	}
	for _, tool := range c.tools() {
		if tool.Name == "manage_site" && !strings.Contains(tool.Description, "AGENTS WORK IN dev") {
			t.Errorf("manage_site's description does not lead with the model")
		}
	}
}

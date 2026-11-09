//go:build integration

package acceptance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2559: the portal's Sites section. A person with the tool
// creates a site, which has one default environment at <site>.<pages domain>
// that opens only for people who sign in and have access; the people who
// manage it add domains, public at the platform, that serve other environments.
//
// Wire forms: REST bodies are JSON objects whose fields are strings, integers
// and booleans as each route documents; a body that is not JSON is refused 400.
// manage_site takes command, site, name, environment, hostname, email, level and
// url as strings, release as an integer, and requires_approval and internal_view
// as booleans; files is a list of objects whose content is utf-8 or base64 text.
// A request to a site is an HTTP GET or HEAD naming the hostname in Host, with
// and without Cookie and If-None-Match. The upload is multipart/form-data.

const (
	keyAdmin2559    = "acme-dev-key-2024"
	keyEngineer2559 = "acme-owner-key"
	keyDirector2559 = "acme-peer-key"
	keyAnalyst2559  = "acme-peer-key"
	keyReporter2559 = "acme-reporter-key"

	emailDirector2559 = "asset.peer@example.com"
	emailKCAnalyst    = "analyst@example.com"
	passKCAnalyst     = "analyst-password"
	emailKCClient     = "client@client.example"
	passKCClient      = "client-password"
	emailUnverified   = "unverified@example.com"
	passUnverified    = "unverified-password"

	portal2559 = "/api/v1/portal/sites"
	marker2559 = "release-body-marker-2559"
)

// rest2559 calls a platform route as the holder of an API key.
func rest2559(t *testing.T, key, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return restOn2559(t, baseURL(), key, method, path, body)
}

func restOn2559(t *testing.T, base, key, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	// Not t.Context(): the cleanups that delete a site run after it is canceled.
	req, err := http.NewRequestWithContext(context.Background(), method, base+path, reader) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("building %s %s: %v", method, path, err)
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["_raw"] = string(raw)
	return res.StatusCode, out
}

func want2559(t *testing.T, what string, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %d, want %d: %v", what, got, want, body["_raw"])
	}
}

// site2559 is a site owned by the holder of key.
type site2559 struct {
	name string
	key  string
}

func newSite2559(t *testing.T, key string) *site2559 {
	t.Helper()
	name := "acc2559-" + unique1579()
	status, got := rest2559(t, key, http.MethodPost, portal2559, map[string]any{"name": name, "description": "Acceptance 2559"})
	want2559(t, "creating a site", status, http.StatusCreated, got)
	t.Cleanup(func() { _, _ = rest2559(t, keyAdmin2559, http.MethodDelete, portal2559+"/"+name, nil) })
	return &site2559{name: name, key: key}
}

func (s *site2559) host() string { return s.name + "." + pagesDomain2517 }

func (s *site2559) path(rest string) string { return portal2559 + "/" + s.name + rest }

func zip2559(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upload2559 puts a file in the caller's managed resources, the way the
// portal's publish dialog does, and returns its id.
func upload2559(t *testing.T, key, name string, body []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_, me := rest2559(t, key, http.MethodGet, "/api/v1/portal/me", nil)
	for k, v := range map[string]string{"scope": "user", "scope_id": fmt.Sprint(me["user_id"]), "path": "sites-" + unique1579(), "display_name": name, "description": "Acceptance 2559", "category": "other"} {
		_ = w.WriteField(k, v)
	}
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(body)
	_ = w.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL()+"/api/v1/resources", &buf) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	got := map[string]any{}
	_ = json.Unmarshal(raw, &got)
	id, _ := got["id"].(string)
	if res.StatusCode >= http.StatusBadRequest || id == "" {
		t.Fatalf("uploading %s: %d %s", name, res.StatusCode, raw)
	}
	return id
}

// waitJob2559 follows a job to its end, within a bound.
func (s *site2559) waitJob(t *testing.T, key, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(pollFor2518)
	for {
		status, got := rest2559(t, key, http.MethodGet, s.path("/jobs/"+id), nil)
		want2559(t, "reading the job", status, http.StatusOK, got)
		if got["status"] != "running" {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job did not end within %s: %v", pollFor2518, got["_raw"])
		}
		time.Sleep(pollGap2518)
	}
}

// publish2559 uploads a zip and publishes it to env through the portal's routes.
func (s *site2559) publish(t *testing.T, key, env, version string) map[string]any {
	t.Helper()
	id := upload2559(t, key, s.name+"-"+version+".zip", zip2559(t, map[string]string{
		"index.html":        "<h1>" + version + "</h1><p>" + marker2559 + "</p>",
		"about/a.css":       "body{color:red}/*" + marker2559 + "*/",
		"404.html":          "<h1>gone " + marker2559 + "</h1>",
		"_redirects":        "/moved /secret-target-2559 301\n",
		"logo.3f2a9c1b.svg": "<svg xmlns=\"http://www.w3.org/2000/svg\"><title>" + marker2559 + "</title></svg>",
	}))
	status, job := rest2559(t, key, http.MethodPost, s.path("/publish"), map[string]any{"environment": env, "note": version, "archive": "mcp:resource:" + id, "source": version + ".zip"})
	want2559(t, "starting the publish", status, http.StatusAccepted, job)
	jobID, _ := job["id"].(string)
	done := s.waitJob(t, key, jobID)
	if done["status"] != "done" {
		t.Fatalf("the publish ended %v", done["_raw"])
	}
	return done
}

// viewer2559 signs a person in through Keycloak and walks the handoff to a
// site's own address, as a browser does, returning the site's cookie.
func viewer2559(t *testing.T, s *site2559, email, password string) string {
	t.Helper()
	hc := browserSignIn(t, baseURL(), email, password)
	return handoff2559(t, s, hc)
}

func handoff2559(t *testing.T, s *site2559, hc *http.Client) string {
	t.Helper()
	start := onHost(t, s.host(), http.MethodGet, "/_signin?path=%2F", nil, "")
	state := cookieOf2517(start.header, "site_state")
	loc, err := url.Parse(start.header.Get("Location"))
	if err != nil || state == "" || start.status != http.StatusFound || !strings.HasSuffix(loc.Path, "/sites/handoff") {
		t.Fatalf("signing in did not start the handoff: %d %v", start.status, start.header)
	}
	if strings.Contains(loc.RawQuery, state) {
		t.Error("the state cookie's value is in the portal URL")
	}
	manual := &http.Client{Jar: hc.Jar, Timeout: hc.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := manual.Get(baseURL() + loc.RequestURI()) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("the portal handoff: %v", err)
	}
	_ = res.Body.Close()
	back, err := url.Parse(res.Header.Get("Location"))
	if err != nil || res.StatusCode != http.StatusFound || back.Hostname() != s.host() || back.Path != "/_auth" {
		t.Fatalf("the handoff answered %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
	done := onHost(t, s.host(), http.MethodGet, back.RequestURI(), map[string]string{"Cookie": "site_state=" + state}, "")
	cookie := cookieOf2517(done.header, "site_viewer")
	if done.status != http.StatusFound || cookie == "" {
		t.Fatalf("completing the handoff: %d %v %s", done.status, done.header, done.body)
	}
	for _, c := range (&http.Response{Header: done.header}).Cookies() {
		if c.Name == "site_viewer" && (!c.HttpOnly || c.Domain != "") {
			t.Errorf("the site's cookie is not HttpOnly and host-only: %+v", c)
		}
	}
	return cookie
}

func withCookie2559(cookie string) map[string]string {
	return map[string]string{"Cookie": "site_viewer=" + cookie}
}

// openUntil2559 waits for the address to serve want to the cookie.
func openUntil2559(t *testing.T, s *site2559, cookie, want string) pageResponse {
	t.Helper()
	return pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.host(), http.MethodGet, "/", withCookie2559(cookie), "")
		return r, r.status == http.StatusOK && strings.Contains(r.body, "<h1>"+want+"</h1>")
	})
}

func (s *site2559) grant(t *testing.T, key, email, level string) {
	t.Helper()
	status, got := rest2559(t, key, http.MethodPut, s.path("/access/"+url.PathEscape(email)), map[string]any{"level": level})
	want2559(t, "granting "+level, status, http.StatusOK, got)
}

func (s *site2559) revoke(t *testing.T, key, email string) {
	t.Helper()
	status, got := rest2559(t, key, http.MethodDelete, s.path("/access/"+url.PathEscape(email)), nil)
	want2559(t, "revoking", status, http.StatusNoContent, got)
}

func (s *site2559) addDomain(t *testing.T, key, host, env string, approval bool) {
	t.Helper()
	status, got := rest2559(t, key, http.MethodPost, s.path("/domains"), map[string]any{"hostname": host, "environment": env, "requires_approval": approval})
	want2559(t, "adding a domain", status, http.StatusCreated, got)
	if _, has := got["token"]; has {
		t.Errorf("a domain carries a verification token: %v", got["_raw"])
	}
}

func refusedNoContent2559(t *testing.T, r pageResponse, status int, what string) {
	t.Helper()
	if r.status != status {
		t.Errorf("%s: %d, want %d", what, r.status, status)
	}
	if strings.Contains(r.body, marker2559) || strings.Contains(r.header.Get("Location"), "secret-target-2559") {
		t.Errorf("%s: the refusal holds the release's content: %v %s", what, r.header, r.body)
	}
	if r.header.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("%s: X-Robots-Tag is %q", what, r.header.Get("X-Robots-Tag"))
	}
}

func TestIssue2559_APersonCreatesASiteUploadsAZipAndOpensItsOwnAddress(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	status, got := rest2559(t, keyEngineer2559, http.MethodGet, s.path(""), nil)
	want2559(t, "reading the site", status, http.StatusOK, got)
	envs, _ := got["environments"].([]any)
	if len(envs) != 1 || envs[0].(map[string]any)["name"] != "default" {
		t.Fatalf("a new site's environments: %v", envs)
	}
	if want := "://" + s.host(); !strings.Contains(fmt.Sprint(got["address"]), want) {
		t.Errorf("the site's address is %v", got["address"])
	}
	s.publish(t, keyEngineer2559, "", "one")

	r := onHost(t, s.host(), http.MethodGet, "/", nil, "")
	refusedNoContent2559(t, r, http.StatusUnauthorized, "the address with no sign-in")
	if !strings.Contains(r.body, "Sign in") {
		t.Errorf("no sign-in page: %s", r.body)
	}

	s.grant(t, keyEngineer2559, emailKCAnalyst, "view")
	got2 := openUntil2559(t, s, viewer2559(t, s, emailKCAnalyst, passKCAnalyst), "one")
	if got2.header.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("the served page is indexable: %v", got2.header)
	}
}

func TestIssue2559_PromoteMovesARollBackReturnsAndHistoryShowsThem(t *testing.T) {
	s := newSite2559(t, keyAdmin2559)
	s.addDomain(t, keyAdmin2559, s.name+"-prod.example.test", "prod", true)
	s.publish(t, keyAdmin2559, "", "first")
	status, got := rest2559(t, keyAdmin2559, http.MethodPost, s.path("/promote"), map[string]any{"from": "default", "to": "prod"})
	want2559(t, "promoting", status, http.StatusOK, got)
	if got["status"] != "promoted" {
		t.Fatalf("an administrator's promote: %v", got["_raw"])
	}
	served2518(t, s.name+"-prod.example.test", "first")

	s.publish(t, keyAdmin2559, "", "second")
	status, got = rest2559(t, keyAdmin2559, http.MethodPost, s.path("/promote"), map[string]any{"from": "default", "to": "prod"})
	want2559(t, "promoting again", status, http.StatusOK, got)
	served2518(t, s.name+"-prod.example.test", "second")

	status, cmp := rest2559(t, keyAdmin2559, http.MethodGet, s.path("/compare?from=1&to=2"), nil)
	want2559(t, "comparing", status, http.StatusOK, cmp)
	if changed, _ := cmp["changed"].([]any); len(changed) == 0 {
		t.Errorf("comparing two releases found no change: %v", cmp["_raw"])
	}

	status, got = rest2559(t, keyAdmin2559, http.MethodPost, s.path("/rollback"), map[string]any{"environment": "prod"})
	want2559(t, "rolling back", status, http.StatusOK, got)
	served2518(t, s.name+"-prod.example.test", "first")

	status, hist := rest2559(t, keyAdmin2559, http.MethodGet, s.path("/deployments"), nil)
	want2559(t, "reading history", status, http.StatusOK, hist)
	seen := map[string]bool{}
	for _, d := range hist["deployments"].([]any) {
		seen[d.(map[string]any)["action"].(string)] = true
	}
	for _, action := range []string{"publish", "promote", "rollback"} {
		if !seen[action] {
			t.Errorf("history has no %s: %v", action, hist["_raw"])
		}
	}
}

func TestIssue2559_ANonAdministratorsPromoteWaitsInApprovalsAndFinishes(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	host := s.name + "-live.example.test"
	s.addDomain(t, keyEngineer2559, host, "prod", true)
	s.publish(t, keyEngineer2559, "", "reviewed")
	body := map[string]any{"from": "default", "to": "prod", "reason": "ready"}
	status, got := rest2559(t, keyEngineer2559, http.MethodPost, s.path("/promote"), body)
	want2559(t, "asking", status, http.StatusOK, got)
	if got["status"] != "awaiting_approval" {
		t.Fatalf("a non-administrator's promote: %v", got["_raw"])
	}
	id, _ := got["approval"].(map[string]any)["id"].(string)
	status, list := rest2559(t, keyAdmin2559, http.MethodGet, "/api/v1/portal/approvals", nil)
	want2559(t, "reading Approvals", status, http.StatusOK, list)
	if !strings.Contains(fmt.Sprint(list["_raw"]), id) {
		t.Errorf("the request is not in Approvals: %v", list["_raw"])
	}
	if r := onHost(t, host, http.MethodGet, "/", nil, ""); strings.Contains(r.body, "<h1>reviewed</h1>") {
		t.Error("the release served before it was approved")
	}
	connectAs(t, keyAdmin2559).call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"})
	status, got = rest2559(t, keyEngineer2559, http.MethodPost, s.path("/promote"), body)
	want2559(t, "finishing", status, http.StatusOK, got)
	if got["status"] != "promoted" {
		t.Fatalf("finishing the promote: %v", got["_raw"])
	}
	served2518(t, host, "reviewed")
}

func TestIssue2559_ADomainServesAsSoonAsItIsAddedAndAnyoneCanOpenIt(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	host := s.name + "-dev.example.test"
	s.addDomain(t, keyEngineer2559, host, "dev", false)
	s.publish(t, keyEngineer2559, "dev", "public")
	served2518(t, host, "public")
	if r := onHost(t, host, http.MethodGet, "/", nil, ""); strings.Contains(r.header.Get("Cache-Control"), "private") {
		t.Errorf("a domain is served as private: %v", r.header)
	}
	status, got := rest2559(t, keyEngineer2559, http.MethodDelete, s.path("/domains/"+host), nil)
	want2559(t, "removing the domain", status, http.StatusNoContent, got)
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, "/", nil, "")
		return r, !strings.Contains(r.body, "<h1>public</h1>")
	})
}

func TestIssue2559_TheSitesOwnAddressServesItsDefaultEnvironmentNotAnother(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.addDomain(t, keyEngineer2559, s.name+"-other.example.test", "other", false)
	s.publish(t, keyEngineer2559, "", "the-default")
	s.publish(t, keyEngineer2559, "other", "the-other")
	s.grant(t, keyEngineer2559, emailKCAnalyst, "view")
	openUntil2559(t, s, viewer2559(t, s, emailKCAnalyst, passKCAnalyst), "the-default")
	if r := onHost(t, "other--"+s.host(), http.MethodGet, "/", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("an <environment>--<site> address answered %d", r.status)
	}
}

func TestIssue2559_SavingAPagesDomainAppliesOnEveryReplicaAndThePortalsOwnIsRefused(t *testing.T) {
	status, before := rest2559(t, keyAdmin2559, http.MethodGet, "/api/v1/admin/settings/sites", nil)
	want2559(t, "reading the settings", status, http.StatusOK, before)
	restore := map[string]any{}
	for _, k := range []string{"pages_domain", "max_release_files", "max_file_bytes", "max_release_bytes", "crawl_max_pages", "crawl_max_bytes"} {
		restore[k] = before[k]
	}
	reps := replicas(t)
	t.Cleanup(func() {
		_, _ = rest2559(t, keyAdmin2559, http.MethodPut, "/api/v1/admin/settings/sites", restore)
		awaitPagesDomain2559(t, reps, fmt.Sprint(restore["pages_domain"]))
	})

	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "moved")

	next := clone2559(restore)
	next["pages_domain"] = "other-pages.localhost"
	status, got := rest2559(t, keyAdmin2559, http.MethodPut, "/api/v1/admin/settings/sites", next)
	want2559(t, "saving a pages domain", status, http.StatusOK, got)
	awaitPagesDomain2559(t, reps, "other-pages.localhost")
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.name+".other-pages.localhost", http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusUnauthorized
	})

	refused := clone2559(restore)
	refused["pages_domain"] = "localhost"
	status, got = rest2559(t, keyAdmin2559, http.MethodPut, "/api/v1/admin/settings/sites", refused)
	if status != http.StatusUnprocessableEntity {
		t.Errorf("saving the portal's own hostname as the pages domain: %d %v", status, got["_raw"])
	}
}

// awaitPagesDomain2559 waits for every replica to have read the pages domain.
func awaitPagesDomain2559(t *testing.T, reps []replica, want string) {
	t.Helper()
	for _, rep := range reps {
		deadline := time.Now().Add(pollFor2518)
		for {
			status, list := restOn2559(t, rep.base, keyAdmin2559, http.MethodGet, portal2559, nil)
			want2559(t, "reading the list on a replica", status, http.StatusOK, list)
			if list["pages_domain"] == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("replica %s did not take the pages domain %s: %v", rep.name, want, list["pages_domain"])
			}
			time.Sleep(pollGap2518)
		}
	}
}

func clone2559(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestIssue2559_EveryRouteIsRefusedWithoutTheAreaAndForSomeoneNotGivenAccess(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "guarded")
	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, portal2559, nil},
		{http.MethodPost, portal2559, map[string]any{"name": "nope"}},
		{http.MethodGet, s.path(""), nil},
		{http.MethodPatch, s.path(""), map[string]any{"description": "x"}},
		{http.MethodDelete, s.path(""), nil},
		{http.MethodGet, s.path("/releases"), nil},
		{http.MethodGet, s.path("/releases/1"), nil},
		{http.MethodGet, s.path("/compare?to=1"), nil},
		{http.MethodGet, s.path("/deployments"), nil},
		{http.MethodPost, s.path("/publish"), map[string]any{"archive": "x"}},
		{http.MethodPost, s.path("/copy"), map[string]any{"url": "https://example.com"}},
		{http.MethodGet, s.path("/jobs"), nil},
		{http.MethodGet, s.path("/jobs/x"), nil},
		{http.MethodPost, s.path("/jobs/x/stop"), nil},
		{http.MethodPost, s.path("/promote"), map[string]any{"to": "default"}},
		{http.MethodPost, s.path("/rollback"), map[string]any{"environment": "default"}},
		{http.MethodPut, s.path("/environments/x"), map[string]any{}},
		{http.MethodPost, s.path("/domains"), map[string]any{"hostname": "x.example.test"}},
		{http.MethodDelete, s.path("/domains/x.example.test"), nil},
		{http.MethodGet, s.path("/access"), nil},
		{http.MethodPut, s.path("/access/a@b.example"), map[string]any{"level": "view"}},
		{http.MethodDelete, s.path("/access/a@b.example"), nil},
	}
	for _, rt := range routes {
		if status, got := rest2559(t, keyReporter2559, rt.method, rt.path, rt.body); status != http.StatusForbidden {
			t.Errorf("%s %s with no Sites area: %d %v", rt.method, rt.path, status, got["_raw"])
		}
	}
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/sites/domains"},
		{http.MethodPost, "/api/v1/admin/sites/domains"},
		{http.MethodDelete, "/api/v1/admin/sites/domains/x.example.test"},
		{http.MethodGet, "/api/v1/admin/settings/sites"},
		{http.MethodPut, "/api/v1/admin/settings/sites"},
	} {
		if status, got := rest2559(t, keyEngineer2559, rt.method, rt.path, map[string]any{}); status != http.StatusForbidden {
			t.Errorf("%s %s as a non-administrator: %d %v", rt.method, rt.path, status, got["_raw"])
		}
	}

	// Someone with the area who was given nothing, then given Can manage.
	publish := map[string]any{"archive": "x"}
	promote := map[string]any{"from": "default", "to": "default"}
	mustRefuse := func(what string) {
		t.Helper()
		if status, got := rest2559(t, keyDirector2559, http.MethodPost, s.path("/publish"), publish); status != http.StatusForbidden {
			t.Errorf("%s: publish: %d %v", what, status, got["_raw"])
		}
		if status, got := rest2559(t, keyDirector2559, http.MethodPost, s.path("/promote"), promote); status != http.StatusForbidden {
			t.Errorf("%s: promote: %d %v", what, status, got["_raw"])
		}
		res, text, err := connectAs(t, keyDirector2559).callRaw("manage_site", map[string]any{
			"command": "promote", "site": s.name, "from": "default", "to": "default",
		})
		if err != nil || !res.IsError || !strings.Contains(text, "changed by its owner") {
			t.Errorf("%s: manage_site promote: %v %s", what, err, text)
		}
	}
	mustRefuse("not given access")

	s.grant(t, keyEngineer2559, emailDirector2559, "manage")
	s.publish(t, keyDirector2559, "", "by-a-manager")
	status, got := rest2559(t, keyDirector2559, http.MethodPost, s.path("/promote"), promote)
	want2559(t, "promoting as someone who manages", status, http.StatusOK, got)

	if status, got := rest2559(t, keyDirector2559, http.MethodPut, s.path("/access/someone@example.com"), map[string]any{"level": "manage"}); status != http.StatusForbidden {
		t.Errorf("adding someone who manages, as someone who manages: %d %v", status, got["_raw"])
	}
	if status, got := rest2559(t, keyDirector2559, http.MethodDelete, s.path(""), nil); status != http.StatusForbidden {
		t.Errorf("deleting as someone who manages: %d %v", status, got["_raw"])
	}
	s.grant(t, keyDirector2559, "viewer@client.example", "view")

	s.revoke(t, keyEngineer2559, emailDirector2559)
	mustRefuse("after being removed")
}

func TestIssue2559_AnExternalPersonOpensOnlyTheSiteTheyWereGivenAndLosesItOnRemoval(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	other := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "for-the-client")
	other.publish(t, keyEngineer2559, "", "not-for-the-client")
	s.grant(t, keyEngineer2559, emailKCClient, "view")

	hc := browserSignIn(t, baseURL(), emailKCClient, passKCClient)
	cookie := handoff2559(t, s, hc)
	openUntil2559(t, s, cookie, "for-the-client")

	otherCookie := handoff2559(t, other, hc)
	r := onHost(t, other.host(), http.MethodGet, "/", withCookie2559(otherCookie), "")
	refusedNoContent2559(t, r, http.StatusForbidden, "another site's address")
	if !strings.Contains(r.body, "access") {
		t.Errorf("no no-access page: %s", r.body)
	}

	// One site's cookie opens no other.
	refusedNoContent2559(t, onHost(t, other.host(), http.MethodGet, "/", withCookie2559(cookie), ""), http.StatusUnauthorized, "a cookie for another site")

	manual := &http.Client{Jar: hc.Jar, Timeout: hc.Timeout}
	for _, p := range []string{portal2559, s.path("")} {
		res, err := manual.Get(baseURL() + p) // #nosec G704 -- the platform under test
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s as an external person: %d", p, res.StatusCode)
		}
	}

	s.revoke(t, keyEngineer2559, emailKCClient)
	refusedNoContent2559(t, onHost(t, s.host(), http.MethodGet, "/", withCookie2559(cookie), ""), http.StatusForbidden, "the next request after removal")
}

func TestIssue2559_EveryPathAndMethodIsGuardedAndARemovedPersonCannotRevalidate(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "guarded")
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodHead, "/"},
		{http.MethodGet, "/index.html"},
		{http.MethodGet, "/about/a.css"},
		{http.MethodHead, "/about/a.css"},
		{http.MethodGet, "/no-such-file"},
		{http.MethodGet, "/moved"},
		{http.MethodGet, "/logo.3f2a9c1b.svg"},
		{http.MethodPost, "/"},
	} {
		r := onHost(t, s.host(), rt.method, rt.path, nil, "")
		refusedNoContent2559(t, r, http.StatusUnauthorized, rt.method+" "+rt.path)
		if cc := r.header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s %s: a refusal is cacheable: %q", rt.method, rt.path, cc)
		}
	}

	s.grant(t, keyEngineer2559, emailKCAnalyst, "view")
	cookie := viewer2559(t, s, emailKCAnalyst, passKCAnalyst)
	var etag string
	for _, p := range []string{"/", "/logo.3f2a9c1b.svg"} {
		r := onHost(t, s.host(), http.MethodGet, p, withCookie2559(cookie), "")
		if r.status != http.StatusOK || r.header.Get("Cache-Control") != "private, no-cache" {
			t.Errorf("%s while allowed: %d %q", p, r.status, r.header.Get("Cache-Control"))
		}
		etag = r.header.Get("ETag")
	}
	if again := onHost(t, s.host(), http.MethodGet, "/logo.3f2a9c1b.svg", map[string]string{"Cookie": "site_viewer=" + cookie, "If-None-Match": etag}, ""); again.status != http.StatusNotModified {
		t.Errorf("revalidating while allowed: %d", again.status)
	}
	s.revoke(t, keyEngineer2559, emailKCAnalyst)
	after := onHost(t, s.host(), http.MethodGet, "/logo.3f2a9c1b.svg", map[string]string{"Cookie": "site_viewer=" + cookie, "If-None-Match": etag}, "")
	refusedNoContent2559(t, after, http.StatusForbidden, "revalidating after removal")
}

func TestIssue2559_EveryoneInternalCanViewWhenTheSiteSaysSoAndNoExternalPerson(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "intranet")
	cookie := viewer2559(t, s, emailKCAnalyst, passKCAnalyst)
	refusedNoContent2559(t, onHost(t, s.host(), http.MethodGet, "/", withCookie2559(cookie), ""), http.StatusForbidden, "before it is opened to everyone internal")

	status, got := rest2559(t, keyEngineer2559, http.MethodPatch, s.path(""), map[string]any{"internal_view": true})
	want2559(t, "opening it", status, http.StatusOK, got)
	openUntil2559(t, s, cookie, "intranet")

	client := viewer2559(t, s, emailKCClient, passKCClient)
	refusedNoContent2559(t, onHost(t, s.host(), http.MethodGet, "/", withCookie2559(client), ""), http.StatusForbidden, "an external person on an intranet site")
}

func TestIssue2559_AnEmailTheProviderHasNotVerifiedMatchesNoGrant(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "verified-only")
	s.grant(t, keyEngineer2559, emailUnverified, "view")
	cookie := viewer2559(t, s, emailUnverified, passUnverified)
	r := onHost(t, s.host(), http.MethodGet, "/", withCookie2559(cookie), "")
	refusedNoContent2559(t, r, http.StatusForbidden, "an unverified email")
	if !strings.Contains(r.body, "has not verified") {
		t.Errorf("the page does not say why: %s", r.body)
	}
}

func TestIssue2559_AnAPIKeySignsNobodyInAndAHandoffOpensOnce(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "keyed")
	for _, h := range []map[string]string{
		{"X-API-Key": keyAdmin2559},
		{"Authorization": "Bearer " + keyAdmin2559},
		{"Cookie": "mcp_session=forged"},
		{"Cookie": "site_viewer=forged"},
	} {
		refusedNoContent2559(t, onHost(t, s.host(), http.MethodGet, "/", h, ""), http.StatusUnauthorized, fmt.Sprintf("%v", h))
	}

	start := onHost(t, s.host(), http.MethodGet, "/_signin?path=%2F", nil, "")
	loc, _ := url.Parse(start.header.Get("Location"))
	res := onHost(t, hostOf(t), http.MethodGet, loc.RequestURI(), map[string]string{"X-API-Key": keyAdmin2559}, "")
	if res.status != http.StatusFound || !strings.Contains(res.header.Get("Location"), "/portal/auth/login") {
		t.Errorf("a handoff with an API key and no session: %d %v", res.status, res.header.Get("Location"))
	}

	hc := browserSignIn(t, baseURL(), emailKCAnalyst, passKCAnalyst)
	begin := onHost(t, s.host(), http.MethodGet, "/_signin?path=%2F", nil, "")
	state := cookieOf2517(begin.header, "site_state")
	begun, _ := url.Parse(begin.header.Get("Location"))
	manual := &http.Client{Jar: hc.Jar, Timeout: hc.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	got, err := manual.Get(baseURL() + begun.RequestURI()) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	_ = got.Body.Close()
	back, _ := url.Parse(got.Header.Get("Location"))
	if bad := onHost(t, s.host(), http.MethodGet, back.RequestURI(), map[string]string{"Cookie": "site_state=" + strings.Repeat("b", 64)}, ""); bad.status != http.StatusBadRequest {
		t.Errorf("a code in a browser that did not start it: %d", bad.status)
	}
	if first := onHost(t, s.host(), http.MethodGet, back.RequestURI(), map[string]string{"Cookie": "site_state=" + state}, ""); first.status != http.StatusFound {
		t.Fatalf("the first use: %d %s", first.status, first.body)
	}
	if second := onHost(t, s.host(), http.MethodGet, back.RequestURI(), map[string]string{"Cookie": "site_state=" + state}, ""); second.status != http.StatusBadRequest {
		t.Errorf("the second use: %d", second.status)
	}
	code := back.Query().Get("code")
	for _, form := range []string{code, base64.StdEncoding.EncodeToString([]byte(code)), fmt.Sprintf("%x", code), url.QueryEscape(code)} {
		searchLogs2518(t, form)
		searchAudit2559(t, s.name, form)
	}
}

// searchAudit2559 fails when secret is in an audit row of the site.
func searchAudit2559(t *testing.T, siteName, secret string) {
	t.Helper()
	status, got := rest2559(t, keyAdmin2559, http.MethodGet, "/api/v1/admin/audit/events?per_page=200&tool_name=manage_site", nil)
	if status != http.StatusOK {
		t.Fatalf("reading the audit rows: %d %v", status, got["_raw"])
	}
	raw := fmt.Sprint(got["_raw"])
	if strings.Contains(raw, siteName) && strings.Contains(raw, secret) {
		t.Errorf("an audit row of %s holds a secret", siteName)
	}
}

func TestIssue2559_ACopyKeepsNoCredentialsAndSaysWhyAnAddressWasSkipped(t *testing.T) {
	const secret = "s3cr3t-2559"
	s := newSite2559(t, keyEngineer2559)
	status, got := rest2559(t, keyEngineer2559, http.MethodPost, s.path("/copy"), map[string]any{"url": "https://user:" + secret + "@example.test/"})
	if status != http.StatusAccepted {
		t.Fatalf("starting a copy: %d %v", status, got["_raw"])
	}
	id, _ := got["id"].(string)
	end := s.waitJob(t, keyEngineer2559, id)
	if end["status"] != "failed" || strings.Contains(fmt.Sprint(end["_raw"]), secret) {
		t.Errorf("a copy naming a password: %v", end["_raw"])
	}
	if strings.Contains(fmt.Sprint(got["_raw"]), secret) {
		t.Errorf("the job holds the password: %v", got["_raw"])
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<a href="/missing">a page that is gone</a>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	fixture := srv.URL
	status, got = rest2559(t, keyEngineer2559, http.MethodPost, s.path("/copy"), map[string]any{"url": fixture + "/?token=" + secret, "max_pages": 50})
	want2559(t, "starting a copy", status, http.StatusAccepted, got)
	id, _ = got["id"].(string)
	end = s.waitJob(t, keyEngineer2559, id)
	if end["status"] != "done" {
		t.Fatalf("the copy ended %v", end["_raw"])
	}
	if strings.Contains(fmt.Sprint(end["_raw"]), secret) {
		t.Errorf("the job holds the query's token: %v", end["_raw"])
	}
	_, rels := rest2559(t, keyEngineer2559, http.MethodGet, s.path("/releases"), nil)
	if strings.Contains(fmt.Sprint(rels["_raw"]), secret) {
		t.Errorf("the release records the token: %v", rels["_raw"])
	}
	searchAudit2559(t, s.name, secret)
	searchLogs2518(t, secret)
	connectAs(t, keyEngineer2559).call("manage_site", map[string]any{"command": "crawl", "site": s.name, "url": fixture + "/?token=" + secret})
	pollAbsent2518(t, func() bool {
		_, got := rest2559(t, keyAdmin2559, http.MethodGet, "/api/v1/admin/audit/events?per_page=200&tool_name=manage_site", nil)
		return !strings.Contains(fmt.Sprint(got["_raw"]), secret)
	}, "the query's token in an audit row of a crawl through manage_site")
	skipped, _ := end["skipped"].([]any)
	if len(skipped) == 0 {
		t.Fatalf("nothing was skipped: %v", end["_raw"])
	}
	for _, sk := range skipped {
		if fmt.Sprint(sk.(map[string]any)["reason"]) == "" {
			t.Errorf("a skipped address has no reason: %v", sk)
		}
	}
}

func TestIssue2559_APublishFromAFileThePersonCannotOpenIsRefused(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.grant(t, keyEngineer2559, emailDirector2559, "manage")
	private := upload2559(t, keyEngineer2559, s.name+"-private.zip", zip2559(t, map[string]string{"index.html": "<h1>private</h1>"}))
	status, got := rest2559(t, keyDirector2559, http.MethodPost, s.path("/publish"), map[string]any{"archive": "mcp:resource:" + private})
	want2559(t, "starting the publish", status, http.StatusAccepted, got)
	id, _ := got["id"].(string)
	if end := s.waitJob(t, keyDirector2559, id); end["status"] != "failed" {
		t.Errorf("publishing someone else's file: %v", end["_raw"])
	}
}

func TestIssue2559_AReleaseCannotStandInForTheSignInRoutes(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	admin := connectAs(t, keyEngineer2559)
	admin.call("manage_site", map[string]any{"command": "publish", "site": s.name, "files": []map[string]any{
		{"path": "index.html", "content": "<h1>home</h1>"},
		{"path": "_auth", "content": marker2559},
		{"path": "_signin/index.html", "content": base64.StdEncoding.EncodeToString([]byte(marker2559)), "encoding": "base64"},
	}})
	for _, p := range []string{"/_auth", "/_signin"} {
		if r := onHost(t, s.host(), http.MethodGet, p, nil, ""); strings.Contains(r.body, marker2559) {
			t.Errorf("%s served the release's file: %s", p, r.body)
		}
	}
}

func TestIssue2559_SomeoneWhoManagesCannotMoveADomainOffAnApprovalEnvironment(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	host := s.name + "-gated.example.test"
	s.addDomain(t, keyEngineer2559, host, "prod", true)
	status, got := rest2559(t, keyEngineer2559, http.MethodDelete, s.path("/domains/"+host), nil)
	if status != http.StatusForbidden {
		t.Errorf("the owner removing a domain from an environment that requires approval: %d %v", status, got["_raw"])
	}
	status, got = rest2559(t, keyAdmin2559, http.MethodDelete, s.path("/domains/"+host), nil)
	want2559(t, "an administrator removing it", status, http.StatusNoContent, got)
}

func TestIssue2559_ASiteAndAnApplicationCannotShareAName(t *testing.T) {
	admin := connectAs(t, keyAdmin2559)
	s := newSite2559(t, keyAdmin2559)
	res, text, err := admin.callRaw("manage_application", map[string]any{"command": "create", "name": s.name, "description": "x"})
	if err != nil || !res.IsError {
		t.Errorf("an application named after a site: %v %s", err, text)
	}
	name := "acc2559-app-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "x"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "name": name})
	})
	status, got := rest2559(t, keyAdmin2559, http.MethodPost, portal2559, map[string]any{"name": name})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("a site named after an application: %d %v", status, got["_raw"])
	}
}

func TestIssue2559_ADomainTheCallerManagesIsRefusedWhereThePlatformAnswers(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	for _, host := range []string{"x." + pagesDomain2517, hostOf(t)} {
		status, got := rest2559(t, keyEngineer2559, http.MethodPost, s.path("/domains"), map[string]any{"hostname": host, "environment": "dev"})
		if status != http.StatusUnprocessableEntity {
			t.Errorf("adding %s: %d %v", host, status, got["_raw"])
		}
	}
	status, got := rest2559(t, keyEngineer2559, http.MethodPost, s.path("/domains"), map[string]any{"hostname": s.name + ".example.test", "environment": "default"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("mapping the default environment: %d %v", status, got["_raw"])
	}
	status, got = rest2559(t, keyEngineer2559, http.MethodPost, "/api/v1/admin/sites/domains", map[string]any{"hostname": "app.example.test", "application": "x"})
	if status != http.StatusForbidden {
		t.Errorf("mapping an application as a non-administrator: %d %v", status, got["_raw"])
	}
}

func TestIssue2559_GrantAndRevokeThroughManageSiteAndAPublishRefusedNamesTheFile(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	owner := connectAs(t, keyEngineer2559)
	owner.call("manage_site", map[string]any{"command": "grant", "site": s.name, "email": emailKCAnalyst, "level": "manage"})
	got := owner.call("manage_site", map[string]any{"command": "get", "site": s.name})
	if !strings.Contains(fmt.Sprint(got["grants"]), emailKCAnalyst) {
		t.Errorf("get lists no grant: %v", got)
	}
	if strings.Contains(fmt.Sprint(connectAs(t, keyAnalyst2559).call("manage_site", map[string]any{"command": "get", "site": s.name})["grants"]), emailKCAnalyst) {
		t.Error("get shows who has access to someone who does not manage the site")
	}
	owner.call("manage_site", map[string]any{"command": "revoke", "site": s.name, "email": emailKCAnalyst})
	owner.call("manage_site", map[string]any{"command": "update", "site": s.name, "internal_view": true})

	big := upload2559(t, keyEngineer2559, s.name+"-big.zip", zip2559(t, map[string]string{".env": "SECRET=1"}))
	status, job := rest2559(t, keyEngineer2559, http.MethodPost, s.path("/publish"), map[string]any{"archive": "mcp:resource:" + big})
	want2559(t, "starting the publish", status, http.StatusAccepted, job)
	id, _ := job["id"].(string)
	end := s.waitJob(t, keyEngineer2559, id)
	if end["status"] != "failed" || !strings.Contains(fmt.Sprint(end["error"]), ".env") {
		t.Errorf("a refused publish does not name the file: %v", end["_raw"])
	}
}

func TestIssue2559_AnApplicationsSitePartAndAPageEndpointStillServe(t *testing.T) {
	app := newPageApp2517(t, "public")
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, app.host, http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusOK
	})
}

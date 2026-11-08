//go:build integration

package acceptance

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2518: static sites served by the platform.
//
// Wire forms: manage_site takes command, site, name, environment, from, to,
// hostname, url and archive as strings, release as an integer,
// requires_approval as a boolean, form_map as an object of strings and files
// as a list of objects whose content is sent as utf-8 and as base64 text;
// manage_approval takes command, id and decision as strings; a request to a
// site is an HTTP GET naming the hostname in Host, with and without
// Accept-Encoding, If-None-Match and Range.

const (
	pollFor2518 = 20 * time.Second
	pollGap2518 = 100 * time.Millisecond
	cssBody2518 = "/* a stylesheet long enough to be worth compressing */\n"
)

// pollUntil2518 repeats read until it reports done, within a bound: what a
// hostname serves is read again on each replica within a moment.
func pollUntil2518(t *testing.T, read func() (pageResponse, bool)) pageResponse {
	t.Helper()
	deadline := time.Now().Add(pollFor2518)
	for {
		got, done := read()
		if done {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("not within %s: last answer %d %v %s", pollFor2518, got.status, got.header, got.body)
		}
		time.Sleep(pollGap2518)
	}
}

// pollAbsent2518 confirms a condition that a delayed write would break, after
// the audit writer has had its moment.
func pollAbsent2518(t *testing.T, ok func() bool, what string) {
	t.Helper()
	time.Sleep(2 * time.Second)
	if !ok() {
		t.Errorf("found %s", what)
	}
}

// searchLogs2518 fails when secret is in a log the platform wrote, plain or
// encoded.
func searchLogs2518(t *testing.T, secret string) {
	t.Helper()
	forms := []string{secret, base64.StdEncoding.EncodeToString([]byte(secret)), fmt.Sprintf("%x", secret)}
	for _, file := range stderrLogFiles(t) {
		raw, err := os.ReadFile(file) // #nosec G304 -- the dev stack's own log files
		if err != nil {
			continue
		}
		for _, form := range forms {
			if bytes.Contains(raw, []byte(form)) {
				t.Errorf("%s holds a token", file)
			}
		}
	}
}

// siteClient is one site under test.
type siteClient struct {
	c    *client
	name string
}

func newSite2518(t *testing.T, c *client) *siteClient {
	t.Helper()
	name := "acc2518-" + unique1579()
	c.call("manage_site", map[string]any{"command": "create", "name": name, "description": "Acceptance 2518"})
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("manage_site", map[string]any{"command": "delete", "site": name})
	})
	return &siteClient{c: c, name: name}
}

func (s *siteClient) preview(env string) string { return env + "--" + s.name + "." + pagesDomain2517 }

func files2518(version string) []map[string]any {
	css := strings.Repeat(cssBody2518, 80)
	return []map[string]any{
		{"path": "index.html", "content": "<h1>" + version + "</h1>" + strings.Repeat("<p>body</p>", 200)},
		{"path": "about/index.html", "content": "<h1>about</h1>"},
		{"path": "assets/site.3f2a9c1b.css", "content": base64.StdEncoding.EncodeToString([]byte(css)), "encoding": "base64"},
		{"path": "plain.css", "content": css},
		{"path": "404.html", "content": "<h1>nothing here</h1>"},
		{"path": "_redirects", "content": "/old /about/ 301\n/blog/* /news/:splat 302\n"},
	}
}

func (s *siteClient) publish(t *testing.T, env, version string) map[string]any {
	t.Helper()
	return s.c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "environment": env, "files": files2518(version), "note": version})
}

func served2518(t *testing.T, host, want string) {
	t.Helper()
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusOK && strings.Contains(r.body, "<h1>"+want+"</h1>")
	})
}

// challtest posts to the DNS test server's management port.
func challtest(t *testing.T, path string, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	port := os.Getenv("DEV_CHALLTEST_MGMT_PORT")
	if port == "" {
		port = "8055"
	}
	res, err := http.Post("http://127.0.0.1:"+port+path, "application/json", bytes.NewReader(raw)) // #nosec G107 -- the dev stack's own server
	if err != nil {
		t.Fatalf("the DNS test server (make dev runs it): %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("challtestsrv %s: %d", path, res.StatusCode)
	}
}

// mapDomain2518 adds a hostname and verifies it by a TXT record.
func mapDomain2518(t *testing.T, s *siteClient, host, env string) {
	t.Helper()
	admin := connect(t)
	added := admin.call("manage_site", map[string]any{"command": "add_domain", "hostname": host, "site": s.name, "environment": env})
	token, _ := added["token"].(string)
	if token == "" {
		t.Fatalf("add_domain carried no token: %v", added)
	}
	challtest(t, "/set-txt", map[string]any{"host": "_platform-site-verify." + host + ".", "value": token})
	t.Cleanup(func() { challtest(t, "/clear-txt", map[string]any{"host": "_platform-site-verify." + host + "."}) })
	admin.call("manage_site", map[string]any{"command": "verify_domain", "hostname": host})
}

func auditedCommands2518(t *testing.T, site string) map[string]bool {
	t.Helper()
	rows, err := issue1904DB(t).QueryContext(t.Context(),
		`SELECT parameters->>'command' FROM audit_logs WHERE tool_name = 'manage_site' AND parameters->>'site' = $1`, site)
	if err != nil {
		t.Fatalf("reading audit_logs: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var cmd string
		if err := rows.Scan(&cmd); err == nil {
			out[cmd] = true
		}
	}
	return out
}

func TestIssue2518_APublishPromotesAndRollsBackOnThreeHostnamesAudited(t *testing.T) {
	admin := connect(t)
	s := newSite2518(t, admin)
	id := s.name
	dev, stage, prod := "dev."+id+".test", "stage."+id+".test", "www."+id+".test"
	mapDomain2518(t, s, dev, "dev")
	mapDomain2518(t, s, stage, "stage")
	mapDomain2518(t, s, prod, "prod")

	one := s.publish(t, "dev", "one")
	if url, _ := one["preview_url"].(string); !strings.Contains(url, s.preview("dev")) {
		t.Errorf("publish's preview URL: %v", one["preview_url"])
	}
	served2518(t, s.preview("dev"), "one")
	served2518(t, dev, "one")

	admin.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "stage"})
	served2518(t, stage, "one")
	got := admin.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "stage", "to": "prod"})
	if got["status"] != "promoted" {
		t.Fatalf("an administrator's promotion to prod: %v", got)
	}
	served2518(t, prod, "one")

	s.publish(t, "dev", "two")
	served2518(t, dev, "two")
	admin.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"})
	served2518(t, prod, "two")
	if r := onHost(t, stage, http.MethodGet, "/", nil, ""); !strings.Contains(r.body, "<h1>one</h1>") {
		t.Errorf("a promotion to prod moved stage: %s", r.body)
	}

	back := admin.call("manage_site", map[string]any{"command": "rollback", "site": s.name, "environment": "prod"})
	if back["release"] != float64(1) {
		t.Errorf("rollback went to %v, want release 1", back["release"])
	}
	served2518(t, prod, "one")

	audited := auditedCommands2518(t, s.name)
	for _, cmd := range []string{"publish", "promote", "rollback"} {
		if !audited[cmd] {
			t.Errorf("%s has no audit row: %v", cmd, audited)
		}
	}
}

func TestIssue2518_APromotionToProdByTheOwnerWaitsForApproval(t *testing.T) {
	owner := connectAs(t, "acme-owner-key")
	admin := connect(t)
	s := newSite2518(t, owner)
	s.publish(t, "dev", "one")

	waiting := owner.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod", "reason": "launch"})
	if waiting["status"] != "awaiting_approval" {
		t.Fatalf("the owner's promotion: %v", waiting)
	}
	if r := onHost(t, s.preview("prod"), http.MethodGet, "/", nil, ""); r.status == http.StatusOK {
		t.Errorf("prod served before the approval: %s", r.body)
	}
	if out, text, err := owner.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "environment": "prod", "files": files2518("x")}); err == nil && !strings.Contains(fmt.Sprint(text, out), "promote") {
		t.Errorf("a publish straight to prod was not refused: %v %v", out, text)
	}
	request, _ := waiting["approval"].(map[string]any)
	id, _ := request["id"].(string)
	admin.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"})
	done := owner.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "prod"})
	if done["status"] != "promoted" {
		t.Fatalf("after the approval: %v", done)
	}
	served2518(t, s.preview("prod"), "one")

	peer := connectAs(t, "acme-peer-key")
	if _, text, _ := peer.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "files": files2518("x")}); !strings.Contains(fmt.Sprint(text), "owner or an administrator") {
		t.Errorf("someone else published to the site: %v", text)
	}
	if _, text, _ := owner.callRaw("manage_site", map[string]any{"command": "add_domain", "hostname": "x." + s.name + ".test", "site": s.name, "environment": "dev"}); !strings.Contains(fmt.Sprint(text), "administrators") {
		t.Errorf("a non-administrator mapped a hostname: %v", text)
	}
}

func TestIssue2518_ServedCompressedConditionalAndImmutableWithNoCDN(t *testing.T) {
	admin := connect(t)
	s := newSite2518(t, admin)
	s.publish(t, "dev", "one")
	host := s.preview("dev")
	served2518(t, host, "one")

	page := onHost(t, host, http.MethodGet, "/", map[string]string{"Accept-Encoding": "br"}, "")
	if !strings.HasPrefix(page.header.Get("Content-Type"), "text/html") || page.header.Get("Content-Encoding") != "br" {
		t.Errorf("a page is not served as compressed HTML: %v", page.header)
	}
	if got := page.header.Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("a page was cached forever: %q", got)
	}
	br := onHost(t, host, http.MethodGet, "/assets/site.3f2a9c1b.css", map[string]string{"Accept-Encoding": "br, gzip"}, "")
	if br.header.Get("Content-Encoding") != "br" || !strings.Contains(br.header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("brotli not chosen: %v", br.header)
	}
	if got := br.header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("a fingerprinted file's Cache-Control: %q", got)
	}
	gz := onHost(t, host, http.MethodGet, "/assets/site.3f2a9c1b.css", map[string]string{"Accept-Encoding": "gzip"}, "")
	if gz.header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip not chosen: %v", gz.header)
	}
	zr, err := gzip.NewReader(strings.NewReader(gz.body))
	if err != nil {
		t.Fatalf("the gzip body: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil || string(plain) != strings.Repeat(cssBody2518, 80) {
		t.Errorf("the gzip body decodes to %d bytes, %v", len(plain), err)
	}
	identity := onHost(t, host, http.MethodGet, "/plain.css", map[string]string{"Accept-Encoding": "identity"}, "")
	if !strings.Contains(identity.header.Get("Cache-Control"), "must-revalidate") {
		t.Errorf("an unfingerprinted file's Cache-Control: %q", identity.header.Get("Cache-Control"))
	}
	etag := identity.header.Get("ETag")
	if again := onHost(t, host, http.MethodGet, "/plain.css", map[string]string{"Accept-Encoding": "identity", "If-None-Match": etag}, ""); again.status != http.StatusNotModified {
		t.Errorf("If-None-Match: %d, want 304", again.status)
	}
	if ranged := onHost(t, host, http.MethodGet, "/plain.css", map[string]string{"Accept-Encoding": "identity", "Range": "bytes=0-9"}, ""); ranged.status != http.StatusPartialContent || len(ranged.body) != 10 {
		t.Errorf("Range: %d, %d bytes", ranged.status, len(ranged.body))
	}
	if r := onHost(t, host, http.MethodGet, "/old", nil, ""); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/about/" {
		t.Errorf("a redirect rule: %d %q", r.status, r.header.Get("Location"))
	}
	if r := onHost(t, host, http.MethodGet, "/blog/2020/post", nil, ""); r.header.Get("Location") != "/news/2020/post" {
		t.Errorf("a splat rule: %q", r.header.Get("Location"))
	}
	if r := onHost(t, host, http.MethodGet, "/about/", nil, ""); r.status != http.StatusOK || !strings.Contains(r.body, "about") {
		t.Errorf("a directory index: %d", r.status)
	}
	if r := onHost(t, host, http.MethodGet, "/nope", nil, ""); r.status != http.StatusNotFound || !strings.Contains(r.body, "nothing here") {
		t.Errorf("the custom 404: %d %s", r.status, r.body)
	}
	if r := onHost(t, host, http.MethodGet, "/_redirects", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("the redirect file was served: %d", r.status)
	}
	if r := onHost(t, host, http.MethodPost, "/", nil, ""); r.status != http.StatusMethodNotAllowed {
		t.Errorf("POST to a site: %d", r.status)
	}
}

func TestIssue2518_AnUnverifiedHostnameRoutesNowhereAndSitesAreIsolated(t *testing.T) {
	admin := connect(t)
	s := newSite2518(t, admin)
	s.publish(t, "dev", "one")
	host := "unverified." + s.name + ".test"
	admin.call("manage_site", map[string]any{"command": "add_domain", "hostname": host, "site": s.name, "environment": "dev"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_site", map[string]any{"command": "remove_domain", "hostname": host})
	})
	if r := onHost(t, host, http.MethodGet, "/", nil, ""); strings.Contains(r.body, "<h1>one</h1>") {
		t.Errorf("an unverified hostname served the site")
	}
	if _, text, _ := admin.callRaw("manage_site", map[string]any{"command": "verify_domain", "hostname": host}); !strings.Contains(fmt.Sprint(text), "not verified") {
		t.Errorf("a hostname with no record verified: %v", text)
	}
	for _, bad := range []string{"localhost.example", "x." + pagesDomain2517} {
		if strings.HasSuffix(bad, pagesDomain2517) {
			if _, text, _ := admin.callRaw("manage_site", map[string]any{"command": "add_domain", "hostname": bad, "site": s.name, "environment": "dev"}); !strings.Contains(fmt.Sprint(text), "pages domain") {
				t.Errorf("%s under the pages domain: %v", bad, text)
			}
		}
	}
	served := onHost(t, s.preview("dev"), http.MethodGet, "/", map[string]string{"Cookie": "mcp_session=secret"}, "")
	if served.header.Get("Set-Cookie") != "" {
		t.Errorf("a site set a cookie: %v", served.header)
	}
	if r := onHost(t, s.preview("dev"), http.MethodGet, "/api/v1/portal/me", map[string]string{"X-API-Key": devAPIKey()}, ""); r.status == http.StatusOK {
		t.Errorf("a site origin answered the portal API")
	}
}

func TestIssue2518_TheIngressReportNamesTheHostnamesAndTheirCertificate(t *testing.T) {
	admin := connect(t)
	s := newSite2518(t, admin)
	host := "www." + s.name + ".test"
	mapDomain2518(t, s, host, "dev")
	got := admin.call("manage_site", map[string]any{"command": "hosts"})
	if !strings.Contains(asJSON(got), host) || !strings.Contains(fmt.Sprint(got["cert_manager"]), "kind: Certificate") {
		t.Errorf("the report: %v", got)
	}
	r := onHost(t, hostOf(t), http.MethodGet, "/api/v1/admin/sites/hosts", map[string]string{"X-API-Key": devAPIKey()}, "")
	if r.status != http.StatusOK || !strings.Contains(r.body, host) {
		t.Errorf("the REST report: %d %s", r.status, r.body)
	}
	if denied := onHost(t, hostOf(t), http.MethodGet, "/api/v1/admin/sites/hosts", map[string]string{"X-API-Key": analystKey1892}, ""); denied.status == http.StatusOK {
		t.Errorf("a non-administrator read the report")
	}
}

// hostIP2518 is the address a container reaches the platform's listener at.
func hostIP2518(t *testing.T) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "exec", "acme-dev-platform-lb", "getent", "ahostsv4", "host.docker.internal").Output() // #nosec G204 -- the dev stack's own container
	if err != nil {
		t.Fatalf("reading the host's address from the load balancer: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		t.Fatal("no address for host.docker.internal")
	}
	return fields[0]
}

func TestIssue2518_BuiltInACMEIssuesACertificateForAVerifiedHostname(t *testing.T) {
	admin := connect(t)
	s := newSite2518(t, admin)
	host := "tls." + s.name + ".test"
	s.publish(t, "dev", "secure")
	mapDomain2518(t, s, host, "dev")
	challtest(t, "/add-a", map[string]any{"host": host + ".", "addresses": []string{hostIP2518(t)}})
	t.Cleanup(func() { challtest(t, "/clear-a", map[string]any{"host": host + "."}) })

	pool := pebbleRoots2518(t)
	var body string
	deadline := time.Now().Add(90 * time.Second)
	for {
		var err error
		if body, err = overTLS2518(t, host, pool); err == nil && strings.Contains(body, "<h1>secure</h1>") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no certificate for %s: %v (last body %q)", host, err, body)
		}
		time.Sleep(2 * time.Second)
	}
	rows, err := issue1904DB(t).QueryContext(t.Context(), `SELECT key, data FROM site_certificates`)
	if err != nil {
		t.Fatalf("reading site_certificates: %v", err)
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var key, data string
		if err := rows.Scan(&key, &data); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		seen++
		decoded, _ := base64.StdEncoding.DecodeString(data)
		if strings.Contains(data, "PRIVATE KEY") || strings.Contains(data, "BEGIN") || bytes.Contains(decoded, []byte("PRIVATE KEY")) {
			t.Errorf("certificate row %s is not sealed", key)
		}
	}
	if seen == 0 {
		t.Error("no certificate was cached")
	}
	if _, err := overTLS2518(t, "not-verified."+s.name+".test", pool); err == nil {
		t.Error("the listener served a certificate for a hostname it does not serve")
	}
}

func pebbleRoots2518(t *testing.T) *x509.CertPool {
	t.Helper()
	insecure := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 -- the dev stack's test CA, read to learn its root
	res, err := insecure.Get("https://127.0.0.1:15000/roots/0")
	if err != nil {
		t.Fatalf("Pebble's root (make dev runs it): %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading Pebble's root: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		t.Fatalf("Pebble's root did not parse: %s", raw)
	}
	return pool
}

func overTLS2518(t *testing.T, host string, roots *x509.CertPool) (string, error) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, "127.0.0.1:5443")
		},
	}}
	res, err := client.Get("https://" + host + "/") // #nosec G107 -- the platform under test
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	return string(raw), err
}

func TestIssue2518_AMigratedSiteKeepsItsURLsAndItsFormsPostToPlatformForms(t *testing.T) {
	admin := connect(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><a href="/about">a</a><a href="/services/">s</a><a href="/contact">c</a><a href="/legacy">l</a><a href="/blog/post">b</a><img src="/logo.png"><a href="https://elsewhere.example/x">x</a></html>`))
	})
	page := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/about", page("<h1>about us</h1>"))
	mux.HandleFunc("/services/", page("<h1>services</h1>"))
	mux.HandleFunc("/contact", page(`<h1>contact</h1><form method="post" action="/wp-json/contact"><input name="email"></form>`))
	mux.HandleFunc("/blog/post", page(`<h1>post</h1><a href="next">relative</a>`))
	mux.HandleFunc("/blog/next", page("<h1>the next post</h1>"))
	mux.HandleFunc("/legacy", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/about", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	})
	mux.HandleFunc("/sitemap.xml", page("<urlset></urlset>"))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	s := newSite2518(t, admin)
	app := "acc2518-cms-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "a migrated site"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	form, _ := bindForm(t, admin, "acc-2518-form", formFields, map[string]any{"auth_mode": "public", "path": "/"})
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": form}) })
	addScriptPart2517(t, admin, app, form)
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": s.name + "/dev"})

	got := admin.call("manage_site", map[string]any{
		"command": "crawl", "site": s.name, "url": srv.URL + "/", "form_map": map[string]any{"/contact": form},
	})
	if got["pages"].(float64) < 5 || got["redirects"].(float64) < 1 {
		t.Fatalf("the crawl: %v", got)
	}
	host := s.preview("dev")
	for path, want := range map[string]string{"/about": "about us", "/services/": "services", "/": "href", "/blog/next": "the next post"} {
		r := pollUntil2518(t, func() (pageResponse, bool) {
			res := onHost(t, host, http.MethodGet, path, nil, "")
			return res, res.status == http.StatusOK
		})
		if !strings.Contains(r.body, want) {
			t.Errorf("%s lost its content: %s", path, r.body)
		}
	}
	if r := onHost(t, host, http.MethodGet, "/legacy", nil, ""); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/about" {
		t.Errorf("an old address: %d %q", r.status, r.header.Get("Location"))
	}
	if r := onHost(t, host, http.MethodGet, "/sitemap.xml", nil, ""); r.status != http.StatusOK {
		t.Errorf("the sitemap: %d", r.status)
	}
	contact := onHost(t, host, http.MethodGet, "/contact", nil, "")
	if !strings.Contains(contact.body, `/forms/`+form) || strings.Contains(contact.body, "<form") {
		t.Errorf("the form was not replaced by the platform form: %s", contact.body)
	}
	framed := onHost(t, hostOf(t), http.MethodGet, "/forms/"+form, nil, "")
	if policy := framed.header.Get("Content-Security-Policy"); !strings.Contains(policy, host) {
		t.Errorf("the site cannot frame its form: %q", policy)
	}
	preflight := onHost(t, hostOf(t), http.MethodOptions, "/endpoints/"+form+"/", map[string]string{
		"Origin": strings.TrimSuffix(fmt.Sprint(got["published"].(map[string]any)["preview_url"]), "/"), "Access-Control-Request-Method": "POST",
	}, "")
	if preflight.status == http.StatusForbidden {
		t.Errorf("the endpoint refused its own site's origin: %d %v", preflight.status, preflight.header)
	}
}

func TestIssue2518_ASiteReleasesAndRollsBackWithItsApplication(t *testing.T) {
	admin := connect(t)
	s := newSite2518(t, admin)
	s.publish(t, "dev", "one")
	app := "acc2518-app-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "holds a site"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": s.name + "/stage"})
	admin.call("manage_application", map[string]any{"command": "release", "application": app})
	served2518(t, s.preview("stage"), "one")
	s.publish(t, "dev", "two")
	admin.call("manage_application", map[string]any{"command": "release", "application": app})
	served2518(t, s.preview("stage"), "two")
	admin.call("manage_application", map[string]any{"command": "rollback", "application": app})
	served2518(t, s.preview("stage"), "one")
}

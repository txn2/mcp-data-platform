//go:build integration

package acceptance

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2557: a release's _headers file sets the policies its pages
// are held to, from an allowed set; anything else, and a value with a control
// character, refuses the publish naming the line. An answer whose release
// declares no framing rule carries frame-ancestors 'self'; one declaring its
// own carries only that. With sites.tls.hsts on, an https answer carries
// Strict-Transport-Security, and with it off none does.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of objects whose path is a string and whose content is sent as utf-8
// text and as base64 (encoding "base64"); rollback takes environment as a
// string. The portal's publish takes a JSON object whose archive and
// environment are strings, after a multipart upload of the zip. A request to a
// site is an HTTP GET naming the hostname in Host, signed in, and over TLS for
// the HSTS criterion.

const (
	csp2557         = "default-src 'self'; frame-ancestors 'none'"
	permissions2557 = "camera=()"
)

// headersFile2557 is a _headers file setting two policies on every path.
var headersFile2557 = "# every page\n/*\n  Content-Security-Policy: " + csp2557 + "\n  Permissions-Policy: " + permissions2557 + "\n"

// files2557 is a site of three files, with the _headers file given unless it
// is empty.
func files2557(version, headers string) []map[string]any {
	out := []map[string]any{
		{"path": "index.html", "content": "<h1>" + version + "</h1>"},
		{"path": "about/index.html", "content": "<h1>about " + version + "</h1>"},
		{"path": "css/site.css", "content": "body{color:#123}"},
	}
	if headers != "" {
		out = append(out, map[string]any{"path": "_headers", "content": headers})
	}
	return out
}

// publish2557 publishes files to dev through manage_site.
func publish2557(t *testing.T, s *site2604, version string, files []map[string]any) {
	t.Helper()
	connectAs(t, s.key).call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": version, "files": files})
}

// headersOn2557 waits until every replica serves version at / to cookie, and
// returns the answers to every file.
func headersOn2557(t *testing.T, s *site2604, cookie, version string) map[string]http.Header {
	t.Helper()
	settled2619(t, s.host("dev"), withCookie2559(cookie), func(r pageResponse) bool {
		return r.status == http.StatusOK && strings.Contains(r.body, "<h1>"+version+"</h1>")
	})
	out := map[string]http.Header{}
	for _, p := range []string{"/", "/about/", "/css/site.css"} {
		r := onHost(t, s.host("dev"), http.MethodGet, p, withCookie2559(cookie), "")
		if r.status != http.StatusOK {
			t.Fatalf("%s answered %d", p, r.status)
		}
		out[p] = r.header
	}
	return out
}

// TestIssue2557_AReleasesHeadersServeOnEveryFileAndARollbackDropsThem: a
// release whose _headers file sets a policy on /* serves it on every file,
// and a rollback to a release without one serves the framing default alone.
func TestIssue2557_AReleasesHeadersServeOnEveryFileAndARollbackDropsThem(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2557-")
	publish2557(t, s, "plain", files2557("plain", ""))
	publish2557(t, s, "headed", files2557("headed", headersFile2557))
	cookie := signIn2604(t, s.host("dev"), emailAdminKC2604, passAdminKC2604)
	for p, h := range headersOn2557(t, s, cookie, "headed") {
		if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != csp2557 {
			t.Errorf("%s: Content-Security-Policy %q, want only %q", p, got, csp2557)
		}
		if got := h.Get("Permissions-Policy"); got != permissions2557 {
			t.Errorf("%s: Permissions-Policy %q", p, got)
		}
	}

	connectAs(t, s.key).call("manage_site", map[string]any{"command": "rollback", "site": s.name, "environment": "dev"})
	for p, h := range headersOn2557(t, s, cookie, "plain") {
		if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != "frame-ancestors 'self'" {
			t.Errorf("%s after the rollback: Content-Security-Policy %q, want only the default", p, got)
		}
		if got := h.Get("Permissions-Policy"); got != "" {
			t.Errorf("%s after the rollback still carries Permissions-Policy %q", p, got)
		}
	}
}

// TestIssue2557_AForbiddenHeaderOrAControlCharacterRefusesThePublish: a
// _headers entry for Set-Cookie or Cache-Control, and a value holding a
// control character (sent as utf-8 and as base64), refuse the publish through
// manage_site naming the line, and the portal's zip publish fails the same way.
func TestIssue2557_AForbiddenHeaderOrAControlCharacterRefusesThePublish(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2557-refuse-")
	bell := "/*\n  Content-Security-Policy: default-src 'self'\a\n"
	for what, tc := range map[string]struct {
		file map[string]any
		want string
	}{
		"Set-Cookie":    {map[string]any{"path": "_headers", "content": "/*\n  Set-Cookie: session=1\n"}, "the _headers file, line 2: a release cannot set Set-Cookie"},
		"Cache-Control": {map[string]any{"path": "_headers", "content": "# caching\n/*\n  Cache-Control: max-age=31536000\n"}, "the _headers file, line 3: a release cannot set Cache-Control"},
		"a bell, utf-8": {map[string]any{"path": "_headers", "content": bell}, "the _headers file, line 2: the value of Content-Security-Policy holds a control character"},
		"a bell, base64": {
			map[string]any{"path": "_headers", "content": base64.StdEncoding.EncodeToString([]byte(bell)), "encoding": "base64"},
			"the _headers file, line 2: the value of Content-Security-Policy holds a control character",
		},
	} {
		files := append(files2557("refused", ""), tc.file)
		res, text, err := connectAs(t, s.key).callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "files": files})
		if err != nil || !res.IsError || !strings.Contains(text, tc.want) {
			t.Errorf("%s: %v %s; want %q", what, err, firstRunes(text, 300), tc.want)
		}
	}

	id := upload2559(t, keyOwner2604, s.name+"-cookie.zip", zip2559(t, map[string]string{
		"index.html": "<h1>zip</h1>", "_headers": "/*\n  Set-Cookie: session=1\n",
	}))
	status, job := s.rest(t, keyOwner2604, http.MethodPost, "/publish", map[string]any{"environment": "dev", "archive": "mcp:resource:" + id})
	want2559(t, "starting the portal publish", status, http.StatusAccepted, job)
	jobID, _ := job["id"].(string)
	done := (&site2559{name: s.name, key: keyOwner2604}).waitJob(t, keyOwner2604, jobID)
	if done["status"] != "failed" || !strings.Contains(fmt.Sprint(done["_raw"]), "the _headers file, line 2: a release cannot set Set-Cookie") {
		t.Errorf("the portal publish of a zip setting Set-Cookie: %v", done["_raw"])
	}
	if releases, _ := connectAs(t, s.key).call("manage_site", map[string]any{"command": "get", "site": s.name})["site"].(map[string]any); fmt.Sprint(env2617(releases, "dev")["release"]) != "0" {
		t.Errorf("a refused publish left a release on dev: %v", releases)
	}
}

// env2617 is one environment of a site view.
func env2617(site map[string]any, name string) map[string]any {
	for _, e := range asList(site["environments"]) {
		if e["name"] == name {
			return e
		}
	}
	return map[string]any{}
}

// TestIssue2557_FramingDefaultsToSelfAndARulesOwnFramingReplacesIt: a release
// declaring nothing answers with frame-ancestors 'self'; one declaring
// X-Frame-Options answers with only that, and one whose policy names
// frame-ancestors answers with only its policy.
func TestIssue2557_FramingDefaultsToSelfAndARulesOwnFramingReplacesIt(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2557-frame-")
	cookie := signIn2604(t, s.host("dev"), emailAdminKC2604, passAdminKC2604)

	publish2557(t, s, "none", files2557("none", ""))
	for p, h := range headersOn2557(t, s, cookie, "none") {
		if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != "frame-ancestors 'self'" {
			t.Errorf("%s: a release declaring nothing answers Content-Security-Policy %q", p, got)
		}
	}

	publish2557(t, s, "xfo", files2557("xfo", "/*\n  X-Frame-Options: DENY\n"))
	for p, h := range headersOn2557(t, s, cookie, "xfo") {
		if h.Get("X-Frame-Options") != "DENY" || len(h.Values("Content-Security-Policy")) != 0 {
			t.Errorf("%s: X-Frame-Options %q with Content-Security-Policy %q; want only DENY", p, h.Get("X-Frame-Options"), h.Values("Content-Security-Policy"))
		}
	}

	own := "frame-ancestors https://partner.example"
	publish2557(t, s, "own", files2557("own", "/*\n  Content-Security-Policy: "+own+"\n"))
	for p, h := range headersOn2557(t, s, cookie, "own") {
		if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != own {
			t.Errorf("%s: Content-Security-Policy %q, want only %q", p, got, own)
		}
	}
}

// issue2557Config is a platform answering over TLS on the dev stack's
// database and portal bucket, so it serves the dev stack's sites, with HSTS
// on or off.
const issue2557Config = `
server:
  name: acceptance-2557
  transport: http
  address: "%%s"
  tls:
    enabled: true
    cert_file: "%s"
    key_file: "%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2557-key"
        name: "acceptance-2557"
        roles: ["admin"]
personas:
  admin:
    display_name: Admin
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "postgres://platform:platform_secret@localhost:%s/mcp_platform?sslmode=disable"
  max_open_conns: 5
toolkits:
  s3:
    enabled: true
    instances:
      acc-2557:
        enabled: true
        endpoint: "http://localhost:%s"
        region: "us-east-1"
        access_key_id: "dev-access-key"
        secret_access_key: "dev-secret-key"
        use_path_style: true
    default: acc-2557
portal:
  s3_connection: acc-2557
  s3_bucket: portal-assets
  public_base_url: "https://acceptance-2557.localhost"
sites:
  pages_domain: pages.localhost
  tls:
    hsts:
      enabled: %t
      max_age: 600
thumbnails:
  enabled: false
`

// cert2557 writes a self-signed certificate and key for 127.0.0.1.
func cert2557(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "acceptance-2557"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// overTLS2557 starts a platform over TLS with HSTS on or off and waits for it
// to answer domain with want.
func overTLS2557(t *testing.T, hsts bool, domain, want string) http.Header {
	t.Helper()
	certFile, keyFile := cert2557(t)
	p := startPlatform(t, fmt.Sprintf(issue2557Config, certFile, keyFile, devPostgresPort(), devS3Port2616(), hsts))
	base := strings.Replace(p.base, "http://", "https://", 1)
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 -- the test's own self-signed certificate
	var last string
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		select {
		case err := <-p.done:
			t.Fatalf("the platform exited (%v) before it served:\n%s", err, p.out.String())
		default:
		}
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/", http.NoBody)
		req.Host = domain
		res, err := hc.Do(req)
		if err != nil {
			last = err.Error()
			continue
		}
		body := make([]byte, 4096)
		n, _ := res.Body.Read(body)
		_ = res.Body.Close()
		if res.TLS != nil && res.StatusCode == http.StatusOK && strings.Contains(string(body[:n]), "<h1>"+want+"</h1>") {
			return res.Header
		}
		last = fmt.Sprintf("%d %s", res.StatusCode, firstRunes(string(body[:n]), 200))
	}
	t.Fatalf("the platform over TLS did not serve %s within 2m: %s", domain, last)
	return nil
}

// TestIssue2557_HSTSIsSentOverHTTPSOnlyWhenOn: a site's public domain served
// over TLS carries Strict-Transport-Security with the configured max-age when
// sites.tls.hsts is on, and none when it is off.
func TestIssue2557_HSTSIsSentOverHTTPSOnlyWhenOn(t *testing.T) {
	_, domain := publicSite2619(t)
	if got := overTLS2557(t, true, domain, "live").Get("Strict-Transport-Security"); got != "max-age=600" {
		t.Errorf("with HSTS on, an https answer carries Strict-Transport-Security %q, want max-age=600", got)
	}
	if got := overTLS2557(t, false, domain, "live").Get("Strict-Transport-Security"); got != "" {
		t.Errorf("with HSTS off, an https answer carries Strict-Transport-Security %q", got)
	}
}

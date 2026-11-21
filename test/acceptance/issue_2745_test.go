//go:build integration

package acceptance

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Issue #2745: the hardening batch released as v1.166.3. Each criterion runs
// through the surface a person or an application uses: the OAuth endpoints a
// client registers and signs in through, with the dev stack's Keycloak as the
// identity provider; the MCP tools; the portal and admin REST routes. The
// criteria about how a process is configured (OAuth with dynamic client
// registration, OIDC with no audience, no ENCRYPTION_KEY) start this tree's
// own binary on the dev stack's Postgres, as #2014's do, since the running dev
// stack is configured otherwise.
//
// Wire forms: /oauth/register takes a JSON object whose redirect_uris is a
// list of strings, sent as one; /oauth/token and /oauth/consent take form
// bodies; the portal sections route takes a JSON object; manage_asset's
// action, collection_id and name are strings and sections a list of objects;
// trino_query's sql and connection are strings; trino_explain's sql a string;
// manage_script's command, name and source strings; run_script's name a string
// and wait_seconds an integer; memory_manage's command a string; manage_schema's
// command, connection, schema, name and up strings and version an integer;
// api_invoke_endpoint's body an object; the admin secret body a JSON object
// whose allow_connections is a list. No parameter touched admits a second form.

const (
	acc2745Key      = "acceptance-2745-key"
	acc2745Realm    = "mcp-platform"
	acc2745Title    = "Acceptance Deployment 2745"
	acc2745Password = "analyst-password"
	acc2745Person   = "analyst@example.com"
)

// acc2745Platform is one process of this tree's binary, configured with OAuth
// and dynamic client registration over the dev stack's Keycloak, OIDC with a
// client_id and no audience, and no ENCRYPTION_KEY.
type acc2745Platform struct {
	*platformProcess
	db       string
	upstream string // the Keycloak client the platform signs people in through
}

// acc2745Config is the process's configuration; {{ADDR}} and the other
// placeholders are filled in by startAcc2745.
const acc2745Config = `
server:
  name: acceptance-2745
  transport: http
  address: "{{ADDR}}"
auth:
  oidc:
    enabled: true
    issuer: "{{KEYCLOAK}}/realms/mcp-platform"
    client_id: "mcp-data-platform-portal"
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"
  api_keys:
    enabled: true
    keys:
      - key: "` + acc2745Key + `"
        name: "acceptance"
        email: "acceptance-2745@example.com"
        roles: ["admin"]
oauth:
  enabled: true
  issuer: "http://{{ADDR}}"
  # The criteria register a client per flow; the default register limit
  # (10 a minute, burst 3) is for the internet.
  rate_limit:
    register:
      requests_per_minute: 600
      burst: 100
  signing_key: "{{SIGNKEY}}"
  dcr:
    enabled: true
    allowed_redirect_patterns:
      - "https://.*\\.example\\.com/callback"
      - "http://127\\.0\\.0\\.1:[0-9]+/callback"
  upstream:
    issuer: "{{KEYCLOAK}}/realms/mcp-platform"
    client_id: "{{CLIENT}}"
    client_secret: "{{SECRET}}"
    redirect_uri: "http://{{ADDR}}/oauth/callback"
portal:
  title: "` + acc2745Title + `"
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
  analyst:
    display_name: "Analyst"
    roles: ["analyst"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "{{DSN}}"
scripts:
  worker:
    enabled: false
notifications:
  enabled: false
thumbnails:
  enabled: false
`

// startAcc2745 creates the Keycloak client the platform signs people in
// through and a database of its own, then starts the process with no
// ENCRYPTION_KEY and waits for it to serve.
func startAcc2745(t *testing.T) *acc2745Platform {
	t.Helper()
	addr := freeAddress(t)
	stamp := strings.ToLower(unique1579())
	p := &acc2745Platform{db: "acc2745_" + stamp, upstream: "acc-2745-upstream-" + stamp}
	secret := "acc2745-" + stamp
	kc := kcAdminToken2061(t)
	if status, out := kcAdmin2061(t, kc, http.MethodPost, "/clients", map[string]any{
		"clientId": p.upstream, "secret": secret, "publicClient": false, "protocol": "openid-connect",
		"standardFlowEnabled": true, "redirectUris": []string{"http://" + addr + "/oauth/callback"},
	}); status != http.StatusCreated {
		t.Fatalf("creating the upstream client in Keycloak: %d %s", status, out)
	}
	t.Cleanup(func() { acc2745DeleteClient(t, p.upstream) })

	devPSQL(t, "postgres", "CREATE DATABASE "+p.db)
	t.Cleanup(func() { devPSQL(t, "postgres", "DROP DATABASE IF EXISTS "+p.db+" WITH (FORCE)") })
	dsn := fmt.Sprintf("postgres://platform:platform_secret@localhost:%s/%s?sslmode=disable", devPostgresPort(), p.db)

	signKey := make([]byte, 32)
	_, _ = rand.Read(signKey)
	config := strings.NewReplacer(
		"{{ADDR}}", addr, "{{KEYCLOAK}}", issue1759KeycloakBase(), "{{SIGNKEY}}", base64.StdEncoding.EncodeToString(signKey),
		"{{CLIENT}}", p.upstream, "{{SECRET}}", secret, "{{DSN}}", dsn,
	).Replace(acc2745Config)
	path := filepath.Join(t.TempDir(), "platform.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	proc := &platformProcess{out: &syncBuffer{}, base: "http://" + addr, done: make(chan error, 1)}
	proc.cmd = exec.Command(platformBinary(t), "--config", path, "--transport", "http", "--address", addr) // #nosec G204 -- the binary this suite built
	// No ENCRYPTION_KEY: the last value of a key in Env is the one used.
	proc.cmd.Env = append(os.Environ(), "OTEL_METRICS_ADDR="+freeAddress(t), "ENCRYPTION_KEY=")
	proc.cmd.Stdout, proc.cmd.Stderr = proc.out, proc.out
	if err := proc.cmd.Start(); err != nil {
		t.Fatalf("starting the platform: %v", err)
	}
	go func() { proc.done <- proc.cmd.Wait() }()
	t.Cleanup(func() {
		proc.stop(t)
		proc.keepOutput(t, addr)
	})
	proc.serving(t, 3*time.Minute)
	p.platformProcess = proc
	return p
}

// acc2745DeleteClient removes a Keycloak client this file created.
func acc2745DeleteClient(t *testing.T, clientID string) {
	t.Helper()
	kc := kcAdminToken2061(t)
	_, out := kcAdmin2061(t, kc, http.MethodGet, "/clients?clientId="+url.QueryEscape(clientID), nil)
	var found []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(out, &found)
	for _, c := range found {
		kcAdmin2061(t, kc, http.MethodDelete, "/clients/"+c.ID, nil)
	}
}

// register posts a client registration and returns the status and body.
func (p *acc2745Platform) register(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(p.base+"/oauth/register", "application/json", strings.NewReader(body)) //nolint:noctx // a loopback call
	if err != nil {
		t.Fatalf("POST /oauth/register: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// acc2745Flow is one browser's trip through a self-registered client's
// sign-in: the client, its PKCE verifier and the browser's cookies.
type acc2745Flow struct {
	p                  *acc2745Platform
	browser            *http.Client
	clientID, secret   string
	redirect, verifier string
	state              string
}

// newFlow registers a client whose redirect is a loopback address nothing
// listens on, and returns a browser that stops at that redirect.
func (p *acc2745Platform) newFlow(t *testing.T) *acc2745Flow {
	t.Helper()
	redirect := "http://" + freeAddress(t) + "/callback"
	status, reg := p.register(t, `{"client_name":"Acceptance Client","redirect_uris":["`+redirect+`"]}`)
	if status != http.StatusCreated {
		t.Fatalf("registering a loopback client: %d %v", status, reg)
	}
	jar, _ := cookiejar.New(nil)
	stopAt, _ := url.Parse(redirect)
	verifier := make([]byte, 32)
	_, _ = rand.Read(verifier)
	f := &acc2745Flow{
		p: p, clientID: fmt.Sprint(reg["client_id"]), secret: fmt.Sprint(reg["client_secret"]),
		redirect: redirect, verifier: base64.RawURLEncoding.EncodeToString(verifier), state: "st-" + unique1579(),
	}
	f.browser = &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if req.URL.Host == stopAt.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return f
}

// signIn opens the authorization URL, signs the person in at Keycloak, and
// returns the page the platform answered the identity provider's callback
// with.
func (f *acc2745Flow) signIn(t *testing.T) (*http.Response, string) {
	t.Helper()
	sum := sha256.Sum256([]byte(f.verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {f.redirect}, "state": {f.state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	res, err := f.browser.Get(f.p.base + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatalf("opening the authorization URL: %v", err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	m := regexp.MustCompile(`<form[^>]*id="kc-form-login"[^>]*action="([^"]+)"`).FindSubmatch(page)
	if m == nil {
		t.Fatalf("the identity provider's sign-in page has no form: %d %.400s", res.StatusCode, page)
	}
	res, err = f.browser.PostForm(html.UnescapeString(string(m[1])), url.Values{"username": {acc2745Person}, "password": {acc2745Password}})
	if err != nil {
		t.Fatalf("signing in at the identity provider: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res, string(body)
}

var acc2745ConsentID = regexp.MustCompile(`name="consent_id" value="([^"]+)"`)

// answer posts a decision for the consent page, from this browser.
func (f *acc2745Flow) answer(t *testing.T, page, decision string) *http.Response {
	t.Helper()
	m := acc2745ConsentID.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the consent page names no request: %.400s", page)
	}
	res, err := f.browser.PostForm(f.p.base+"/oauth/consent", url.Values{"consent_id": {m[1]}, "decision": {decision}})
	if err != nil {
		t.Fatalf("answering the consent page: %v", err)
	}
	return res
}

// TestIssue2745_OAuthDynamicRegistrationAndConsent holds the OAuth criteria on
// one process: a registration whose redirect URI a pattern would match only
// for another host is refused; a self-registered client gets an authorization
// code only after the person allows it on the platform's consent page, which
// names the deployment, the person and where they are sent, and cannot be
// framed; Deny sends access_denied; the page is answered once, and not from
// another browser; a registration body past its bound is refused. On the same
// process, a token Keycloak issued to another of its clients is refused while
// the portal client's is accepted (no audience is configured), and a stored
// secret's value is refused with no ENCRYPTION_KEY.
func TestIssue2745_OAuthDynamicRegistrationAndConsent(t *testing.T) {
	p := startAcc2745(t)

	t.Run("a redirect a pattern matches for another host is not registered", func(t *testing.T) {
		for _, uri := range []string{
			"https://attacker.test/x.example.com/callback",
			"https://attacker.test/?x=.example.com/callback",
			"https://app.example.com@attacker.test/callback",
			"https://app.example.com/callback/../../x",
		} {
			status, out := p.register(t, `{"client_name":"x","redirect_uris":["`+uri+`"]}`)
			if status != http.StatusBadRequest {
				t.Errorf("%s registered: %d %v", uri, status, out)
			}
		}
		status, out := p.register(t, `{"client_name":"x","redirect_uris":["https://attacker.test/x.example.com/callback"]}`)
		if !strings.Contains(fmt.Sprint(out), "would also match other hosts") {
			t.Errorf("the refusal does not say why: %d %v", status, out)
		}
		if status, out := p.register(t, `{"client_name":"x","redirect_uris":["https://app.example.com/callback"]}`); status != http.StatusCreated {
			t.Errorf("a host the pattern names was refused: %d %v", status, out)
		}
	})

	t.Run("a registration body past its bound is refused", func(t *testing.T) {
		body := `{"client_name":"` + strings.Repeat("x", 70_000) + `","redirect_uris":["https://app.example.com/callback"]}`
		if status, out := p.register(t, body); status != http.StatusBadRequest {
			t.Errorf("an oversized registration: %d %v", status, out)
		}
	})

	t.Run("a code is sent only after the person allows the client", func(t *testing.T) {
		f := p.newFlow(t)
		res, page := f.signIn(t)
		if res.StatusCode != http.StatusOK || res.Header.Get("Location") != "" {
			t.Fatalf("the callback did not answer with the consent page: %d %q %.300s", res.StatusCode, res.Header.Get("Location"), page)
		}
		for _, want := range []string{acc2745Title, acc2745Person, "An application on this computer", f.redirect, "Acceptance Client", "Allow", "Deny"} {
			if !strings.Contains(page, want) {
				t.Errorf("the consent page does not show %q", want)
			}
		}
		if res.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("the consent page can be framed: %v", res.Header)
		}

		allowed := f.answer(t, page, "allow")
		_ = allowed.Body.Close()
		loc, err := url.Parse(allowed.Header.Get("Location"))
		if err != nil || allowed.StatusCode != http.StatusFound || !strings.HasPrefix(loc.String(), f.redirect+"?") {
			t.Fatalf("Allow did not send the browser to the client: %d %q", allowed.StatusCode, allowed.Header.Get("Location"))
		}
		code := loc.Query().Get("code")
		if code == "" || loc.Query().Get("state") != f.state {
			t.Fatalf("Allow's redirect carries no code or another state: %v", loc.Query())
		}

		res2, err := http.PostForm(p.base+"/oauth/token", url.Values{ //nolint:noctx // a loopback call
			"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {f.redirect},
			"client_id": {f.clientID}, "client_secret": {f.secret}, "code_verifier": {f.verifier},
		})
		if err != nil {
			t.Fatal(err)
		}
		var tok struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.NewDecoder(res2.Body).Decode(&tok)
		_ = res2.Body.Close()
		if res2.StatusCode != http.StatusOK || tok.AccessToken == "" {
			t.Fatalf("the code did not exchange for a token: %d", res2.StatusCode)
		}
		if status := acc2745Initialize(t, p.base, tok.AccessToken); status != http.StatusOK {
			t.Errorf("the client's token was refused by the MCP endpoint: %d", status)
		}

		again := f.answer(t, page, "allow")
		body, _ := io.ReadAll(again.Body)
		_ = again.Body.Close()
		if again.StatusCode != http.StatusBadRequest || again.Header.Get("Location") != "" || !strings.Contains(string(body), "This request has ended") {
			t.Errorf("the consent page was answered twice: %d %q", again.StatusCode, again.Header.Get("Location"))
		}
	})

	t.Run("deny sends access_denied and no code", func(t *testing.T) {
		f := p.newFlow(t)
		_, page := f.signIn(t)
		denied := f.answer(t, page, "deny")
		_ = denied.Body.Close()
		loc, _ := url.Parse(denied.Header.Get("Location"))
		if denied.StatusCode != http.StatusFound || loc == nil || loc.Query().Get("error") != "access_denied" ||
			loc.Query().Get("code") != "" || loc.Query().Get("state") != f.state {
			t.Errorf("Deny: %d %q", denied.StatusCode, denied.Header.Get("Location"))
		}
	})

	t.Run("another browser cannot answer the consent page", func(t *testing.T) {
		f := p.newFlow(t)
		_, page := f.signIn(t)
		m := acc2745ConsentID.FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no consent page: %.300s", page)
		}
		res, err := http.PostForm(p.base+"/oauth/consent", url.Values{"consent_id": {m[1]}, "decision": {"allow"}}) //nolint:noctx // a loopback call
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || res.Header.Get("Location") != "" {
			t.Errorf("a browser without the page's cookie answered it: %d %q", res.StatusCode, res.Header.Get("Location"))
		}
		late := f.answer(t, page, "allow")
		_ = late.Body.Close()
		if late.StatusCode != http.StatusBadRequest {
			t.Errorf("the request answered from another browser could still be answered: %d", late.StatusCode)
		}
	})

	t.Run("a token issued to another client of the identity provider is refused", func(t *testing.T) {
		other := "acc-2745-other-" + strings.ToLower(unique1579())
		kc := kcAdminToken2061(t)
		if status, out := kcAdmin2061(t, kc, http.MethodPost, "/clients", map[string]any{
			"clientId": other, "secret": "other-secret", "publicClient": false, "protocol": "openid-connect",
			"directAccessGrantsEnabled": true, "standardFlowEnabled": false,
		}); status != http.StatusCreated {
			t.Fatalf("creating another client in Keycloak: %d %s", status, out)
		}
		t.Cleanup(func() { acc2745DeleteClient(t, other) })
		foreign := acc2745PasswordGrant(t, other, "other-secret")
		if status := acc2745Initialize(t, p.base, foreign); status != http.StatusUnauthorized {
			t.Errorf("a token issued to %s was answered %d, want 401", other, status)
		}
		mine := issue1759SignIn(t, acc2745Person, acc2745Password)
		if status := acc2745Initialize(t, p.base, mine); status != http.StatusOK {
			t.Errorf("the portal client's own token was answered %d", status)
		}
	})

	t.Run("with no encryption key a secret value is refused and nothing is stored", func(t *testing.T) {
		value := "acc2745-plain-" + unique1579()
		req, _ := http.NewRequest(http.MethodPut, p.base+"/api/v1/admin/secrets/acc2745_nokey", strings.NewReader( //nolint:noctx // a loopback call
			`{"description":"Acceptance #2745","value":"`+value+`","allow_connections":["anything"]}`))
		req.Header.Set("Authorization", "Bearer "+acc2745Key)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "ENCRYPTION_KEY") {
			t.Errorf("saving a secret with no key: %d %s", res.StatusCode, body)
		}
		if n := acc2745Count(t, p.db, "SELECT count(*) FROM gateway_secrets"); n != "0" {
			t.Errorf("gateway_secrets holds %s rows after a refused save", n)
		}
	})
}

// acc2745Initialize posts an MCP initialize with bearer and returns the
// status the endpoint answered.
func acc2745Initialize(t *testing.T, base, bearer string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader( //nolint:noctx // a loopback call
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"acc-2745","version":"1"}}}`))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

// acc2745PasswordGrant signs the analyst in through clientID and returns the
// access token Keycloak issued to that client.
func acc2745PasswordGrant(t *testing.T, clientID, secret string) string {
	t.Helper()
	res, err := http.PostForm(issue1759KeycloakBase()+"/realms/"+acc2745Realm+"/protocol/openid-connect/token", url.Values{ //nolint:noctx // the dev identity provider
		"grant_type": {"password"}, "client_id": {clientID}, "client_secret": {secret},
		"username": {acc2745Person}, "password": {acc2745Password},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.AccessToken == "" {
		t.Fatalf("signing in through %s answered %d with no token", clientID, res.StatusCode)
	}
	return out.AccessToken
}

// acc2745Count runs a count statement in database db of the dev Postgres.
func acc2745Count(t *testing.T, db, statement string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", "acme-dev-postgres", // #nosec G204 -- the dev stack's own container
		"psql", "-tA", "-U", "platform", "-d", db, "-c", statement).CombinedOutput()
	if err != nil {
		t.Fatalf("psql %q: %v: %s", statement, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestIssue2745_ACollectionTakesOnlyAssetsItsEditorCanOpen: an asset goes into
// a collection only when the person putting it there can open it, through the
// MCP tool's create_collection and set_sections and the portal's sections
// route alike; once it is shared with them, it goes in.
func TestIssue2745_ACollectionTakesOnlyAssetsItsEditorCanOpen(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	assetID, _ := saveReport(t, owner, "2745 private report")
	sections := []any{map[string]any{"title": "Mine", "items": []any{map[string]any{"asset_id": assetID}}}}

	_, text, _ := peer.callRaw("manage_asset", map[string]any{"action": "create_collection", "name": "acc-2745 " + unique1579(), "sections": sections})
	if !strings.Contains(text, "do not have access to this asset") {
		t.Errorf("create_collection took another person's asset: %.300s", text)
	}

	created := peer.call("manage_asset", map[string]any{"action": "create_collection", "name": "acc-2745 " + unique1579()})
	collID := fmt.Sprint(created["collection_id"])
	t.Cleanup(func() {
		_, _, _ = peer.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID})
	})
	_, text, _ = peer.callRaw("manage_asset", map[string]any{"action": "set_sections", "collection_id": collID, "sections": sections})
	if !strings.Contains(text, "do not have access to this asset") {
		t.Errorf("set_sections took another person's asset: %.300s", text)
	}
	status, out := peer.rest(http.MethodPut, "/api/v1/portal/collections/"+collID+"/sections", jsonBody(t, map[string]any{"sections": sections}))
	if status != http.StatusForbidden {
		t.Errorf("the portal sections route took another person's asset: %d %v", status, out)
	}
	_, coll := peer.rest(http.MethodGet, "/api/v1/portal/collections/"+collID, http.NoBody)
	if strings.Contains(asJSON(coll), assetID) {
		t.Fatalf("the collection holds the asset after every refusal: %v", coll)
	}

	big := strings.Repeat("x", 2<<20)
	status, _ = peer.rest(http.MethodPut, "/api/v1/portal/collections/"+collID+"/sections", strings.NewReader(`{"sections":[{"title":"`+big+`"}]}`))
	if status != http.StatusBadRequest {
		t.Errorf("a 2 MiB sections body: %d, want 400", status)
	}

	peerID := issue2745UserID(t, peer)
	if status, out := owner.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares", jsonBody(t, map[string]any{
		"shared_with_user_id": peerID, "permission": "viewer", "notify": false,
	})); status != http.StatusCreated {
		t.Fatalf("sharing the asset with the peer: %d %v", status, out)
	}
	if res := peer.call("manage_asset", map[string]any{"action": "set_sections", "collection_id": collID, "sections": sections}); !strings.Contains(asJSON(res), assetID) {
		t.Errorf("an asset shared with the editor was refused: %v", res)
	}
}

// issue2745UserID is the user id the portal knows a session's person by.
func issue2745UserID(t *testing.T, c *client) string {
	t.Helper()
	_, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	id, _ := me["user_id"].(string)
	if id == "" {
		t.Fatalf("the portal reported no user id: %v", me)
	}
	return id
}

// acc2745TrinoPurpose is the sentence the Trino criteria state, as an agent
// states one for every data-access call.
const acc2745TrinoPurpose = "Acceptance #2745: checking that the read-only query tool refuses statements that write."

// TestIssue2745_TrinoQueryRunsOnlyStatementsThatRead: trino_query refuses a
// statement that writes whatever its first word, EXPLAIN ANALYZE included,
// and trino_explain refuses to explain one it would run; a read still runs.
func TestIssue2745_TrinoQueryRunsOnlyStatementsThatRead(t *testing.T) {
	c := connect(t)
	for _, sql := range []string{
		"EXPLAIN ANALYZE SELECT 1",
		"REFRESH MATERIALIZED VIEW acc2745_mv",
		"COMMENT ON TABLE acc2745_t IS 'x'",
		"ANALYZE acc2745_t",
		"DENY SELECT ON acc2745_t TO acc2745_user",
	} {
		_, text, _ := c.callRaw("trino_query", map[string]any{"connection": scratchResourceConnection, "sql": sql, "purpose": acc2745TrinoPurpose})
		if !strings.Contains(text, "runs only statements that read") {
			t.Errorf("trino_query ran %q: %.300s", sql, text)
		}
	}
	_, text, _ := c.callRaw("trino_explain", map[string]any{"connection": scratchResourceConnection, "sql": "EXPLAIN ANALYZE SELECT 1", "purpose": acc2745TrinoPurpose})
	if !strings.Contains(text, "explains a statement without running it") {
		t.Errorf("trino_explain explained an EXPLAIN ANALYZE: %.300s", text)
	}
	out := c.call("trino_query", map[string]any{"connection": scratchResourceConnection, "sql": "SELECT 1 AS one", "purpose": acc2745TrinoPurpose})
	if !strings.Contains(asJSON(out), "one") {
		t.Errorf("a read did not run: %v", out)
	}
}

// TestIssue2745_ATimeseriesRangeTooWideIsABadRequest: an activity timeseries
// over centuries at minute resolution is refused with 400 on the portal and
// the admin routes, where it used to be materialized bucket by bucket; a
// week by the hour still answers.
func TestIssue2745_ATimeseriesRangeTooWideIsABadRequest(t *testing.T) {
	c := connect(t)
	wide := "resolution=minute&start_time=0001-01-01T00:00:00Z&end_time=2026-11-20T00:00:00Z"
	for _, path := range []string{"/api/v1/portal/activity/timeseries?" + wide, "/api/v1/admin/audit/metrics/timeseries?" + wide} {
		if status, out := c.rest(http.MethodGet, path, http.NoBody); status != http.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", path, status, out)
		}
	}
	week := "resolution=hour&start_time=" + time.Now().Add(-7*24*time.Hour).UTC().Format(time.RFC3339)
	if status, _ := c.rest(http.MethodGet, "/api/v1/portal/activity/timeseries?"+week, http.NoBody); status != http.StatusOK {
		t.Errorf("a week by the hour: %d", status)
	}
}

// TestIssue2745_AScriptCannotAllocatePastItsBudget: a repeat that would build
// gigabytes in one step is refused with a memory failure before it allocates,
// a loop that builds past the budget with no host call is stopped at a step
// checkpoint, and the platform keeps answering.
func TestIssue2745_AScriptCannotAllocatePastItsBudget(t *testing.T) {
	c := connectFor(t, 5*time.Minute)
	_, text, err := c.callRaw("manage_script", map[string]any{
		"command": "run_draft", "name": "acc-2745-repeat-" + unique1579(),
		"source": `def main():
    """Asks for a list of a billion slots in one step."""
    held = [0] * (2 * 1024 * 1024 * 1024 - 2)
    print(len(held))
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "memory budget") || !strings.Contains(text, "a repeat (*)") {
		t.Errorf("a repeat of a billion slots: %.400s", text)
	}

	name := fmt.Sprintf("acc-2745-loop-%d", time.Now().UnixNano())
	created := c.seedPreGate(t, map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2745: a loop past its budget with no host call.",
		"source": `def main():
    """Holds about 300 MiB of strings and calls the host never."""
    held = []
    for i in range(300000):
        held.append("x" * 1024 + str(i))
    print(len(held))
`,
	})
	if created["status"] != "created" {
		t.Fatalf("manage_script create %s: %v", name, created)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	if got["status"] != "failed" || got["cause"] != "memory" {
		t.Fatalf("a loop holding 300 MiB under a 128 MiB budget: status %v cause %v: %v", got["status"], got["cause"], got)
	}
	if errText, _ := got["error"].(string); !strings.Contains(errText, "the code between host calls") {
		t.Errorf("error %q does not say it was measured between host calls", errText)
	}
	c.call("platform_info", map[string]any{})
}

// TestIssue2745_AnAPIKeyIsFoundByItsDigest: a database key is stored with the
// SHA-256 of its value beside its hash, and authenticates by it; a token of
// the same shape that is no key is refused.
func TestIssue2745_AnAPIKeyIsFoundByItsDigest(t *testing.T) {
	admin := connect(t)
	name := "acc-2745-digest-" + strings.ToLower(unique1579())
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []string{"admin"}, "description": "Acceptance #2745",
	}))
	if status != http.StatusCreated {
		t.Fatalf("issuing a key: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	key, _ := out["key"].(string)
	sum := sha256.Sum256([]byte(key))
	if got := acc2745Count(t, "mcp_platform", "SELECT key_digest FROM api_keys WHERE name = '"+name+"'"); got != hex.EncodeToString(sum[:]) {
		t.Errorf("api_keys.key_digest = %q, want the SHA-256 of the key", got)
	}
	if got := issue1759Me(t, key); got.UserID == "" {
		t.Errorf("the key did not authenticate")
	}
	junk := hex.EncodeToString(make([]byte, 32))
	if status, _ := issue1759Request(t, junk, http.MethodGet, issue1759MePath, nil); status != http.StatusUnauthorized {
		t.Errorf("a key-shaped token that is no key: %d", status)
	}
}

// TestIssue2745_ReviewStaleShowsACallerOnlyTheirOwn: memory_manage
// review_stale shows an administrator every stale memory and anyone else only
// the ones they created.
func TestIssue2745_ReviewStaleShowsACallerOnlyTheirOwn(t *testing.T) {
	admin := connect(t)
	marker := "acc-2745 stale marker " + unique1579()
	captured := admin.call("memory_capture", map[string]any{
		"type": "personal_preference", "content": marker + ": the orders table is refreshed nightly.",
	})
	id, _ := captured["id"].(string)
	if id == "" {
		t.Fatalf("memory_capture returned no id: %v", captured)
	}
	t.Cleanup(func() { _, _, _ = admin.callRaw("memory_manage", map[string]any{"command": "forget", "id": id}) })
	devPSQL(t, "mcp_platform", "UPDATE memory_records SET status = 'stale' WHERE id = '"+id+"'")

	if got := asJSON(admin.call("memory_manage", map[string]any{"command": "review_stale", "limit": 200})); !strings.Contains(got, marker) {
		t.Errorf("an administrator's review does not hold the stale memory")
	}
	peer := connectAs(t, devPeerAPIKey)
	if got := asJSON(peer.call("memory_manage", map[string]any{"command": "review_stale", "limit": 200})); strings.Contains(got, marker) {
		t.Errorf("another person's review holds the administrator's stale memory")
	}
}

// TestIssue2745_ABearerTokenCannotIssueAnAPIKey: a person's own key is issued
// only from their signed-in portal session; their identity provider's token
// lists their keys and is refused when it asks for a new one.
func TestIssue2745_ABearerTokenCannotIssueAnAPIKey(t *testing.T) {
	token := issue1759SignIn(t, acc2745Person, acc2745Password)
	status, body := issue1759Request(t, token, http.MethodPost, issue1759PortalKeysPath, []byte(`{"name":"acc-2745-bearer","expires_in":"720h"}`))
	if status != http.StatusForbidden || !strings.Contains(fmt.Sprint(body), "signed-in portal session") {
		t.Errorf("a bearer token issuing a key: %d %v", status, body)
	}
	if status, _ := issue1759Request(t, token, http.MethodGet, issue1759PortalKeysPath, nil); status != http.StatusOK {
		t.Errorf("the bearer token listing its keys: %d", status)
	}
	s := issue1759Session(t, acc2745Person, acc2745Password)
	name := issue1759Name("2745-session")
	key := issue1759CreateOwn(t, s, name)
	t.Cleanup(func() { issue1759DeleteOwn(t, token, name) })
	if key == "" {
		t.Error("the signed-in session was not issued a key")
	}
}

// TestIssue2745_AStoredSecretThatReadsLikeCiphertextIsSealed: a secret whose
// value begins "enc:" is stored sealed, never as written, and a request
// naming it sends that value upstream.
func TestIssue2745_AStoredSecretThatReadsLikeCiphertextIsSealed(t *testing.T) {
	admin := connect(t)
	name := "acc2745_enc_" + strings.ToLower(unique1579())
	value := "enc:acc2745-" + unique1579()
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+name, map[string]any{
		"description": "Acceptance #2745", "value": value, "allow_connections": []string{fixture2051}, "allow_personas": []string{},
	}); status != http.StatusCreated {
		t.Fatalf("saving the secret: %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+name, http.NoBody) })
	stored := acc2745Count(t, "mcp_platform", "SELECT value FROM gateway_secrets WHERE name = '"+name+"'")
	if stored == value || !strings.HasPrefix(stored, "enc:") || strings.Contains(stored, value[len("enc:"):]) {
		t.Errorf("the value is stored as written: %q", stored)
	}
	out := admin.call("api_invoke_endpoint", map[string]any{
		"connection": fixture2051, "method": http.MethodPost, "path": "/v1/echo",
		"body":    map[string]any{"text": "{{secret:" + name + "}}"},
		"purpose": "Acceptance #2745: a secret that reads like ciphertext reaches the upstream as written.",
	})
	if echoed := asJSON(out["body"]); !strings.Contains(echoed, "[REDACTED:"+name+"]") || strings.Contains(echoed, value) {
		t.Errorf("the upstream did not receive the value: %s", firstRunes(echoed, 400))
	}
}

// TestIssue2745_APasswordInsideADoBlockIsRefusedAndRedacted: a migration whose
// up gives a role a password inside a DO block is refused by the review, and
// a script running that DO block leaves no copy of the password in any sink.
func TestIssue2745_APasswordInsideADoBlockIsRefusedAndRedacted(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	schema := schema2404(t, "pwdo")
	ok, text := k.call(admin, "manage_schema", map[string]any{
		"command": "add", "connection": conn2404, "schema": schema, "version": 1, "name": "acceptance 2745 do block",
		"up": "DO $$ BEGIN CREATE ROLE acc2745_r LOGIN PASSWORD '" + k.plant("migration") + "'; END $$",
	})
	if ok || !strings.Contains(text, "writes a credential") {
		t.Errorf("a password inside a DO block was not refused: ok=%v %s", ok, firstRunes(text, 400))
	}
	value := k.plant("script")
	tail := value[len(value)-4:]
	k.call(admin, "manage_script", map[string]any{
		"command": "run_draft", "name": "acceptance-2745-" + unique1579(), "allow_writes": true,
		"source": `def main():
    """Sets a role's password inside a DO block."""
    print("` + k.control + `")
    password = "` + value[:len(value)-len(tail)] + `" + "` + tail + `"
    platform.execute("DO $$ BEGIN ALTER ROLE acc2745_nobody PASSWORD '" + password + "'; EXCEPTION WHEN OTHERS THEN NULL; END $$", connection = "` + conn2404 + `")
`,
	})
	k.awaitWriters(admin)
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
	if !k.found["response"] {
		t.Error("the control value was not found in any response; the search would miss a leak there")
	}
}

// TestIssue2745_ASpecFetchRefusesWhatTheEgressGuardRefuses: a catalog spec
// fetched from a host in a NAT64, 6to4 or IPv4-compatible range is refused,
// and counted as embedded_ipv4.
func TestIssue2745_ASpecFetchRefusesWhatTheEgressGuardRefuses(t *testing.T) {
	c := connect(t)
	catalogID := "acc-2745-" + strings.ToLower(unique1579())
	if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Acceptance 2745", "description": "Spec fetch ranges.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating the catalog: %d", status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
	before := metricSeries(scrapeRaw(t), "egress_blocked_total", map[string]string{"reason": "embedded_ipv4"})
	for _, host := range []string{"[64:ff9b::a9fe:a9fe]", "[2002:a9fe:a9fe::1]", "[::a9fe:a9fe]"} {
		status, out := c.rest(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/s", jsonBody(t, map[string]any{
			"source_kind": "url", "source_url": "https://" + host + "/openapi.json",
		}))
		if status < 400 || !strings.Contains(fmt.Sprint(out), "embedded_ipv4") {
			t.Errorf("a spec at %s: %d %v", host, status, out)
		}
	}
	deadline := time.Now().Add(spanWait)
	for metricSeries(scrapeRaw(t), "egress_blocked_total", map[string]string{"reason": "embedded_ipv4"}) < before+3 {
		if time.Now().After(deadline) {
			t.Fatalf("egress_blocked_total{reason=embedded_ipv4} did not count the three refusals (%v)", before)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue2745_ACollectionListingCarriesThePermissionOfEachShare: the
// collections shared with a person are listed with the permission each share
// grants them, read for the page at once.
func TestIssue2745_ACollectionListingCarriesThePermissionOfEachShare(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	peerID := issue2745UserID(t, peer)
	ids := map[string]string{}
	for _, perm := range []string{"viewer", "editor"} {
		created := owner.call("manage_asset", map[string]any{"action": "create_collection", "name": "acc-2745 " + perm + " " + unique1579()})
		id := fmt.Sprint(created["collection_id"])
		ids[perm] = id
		t.Cleanup(func() {
			_, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": id})
		})
		if status, out := owner.rest(http.MethodPost, "/api/v1/portal/collections/"+id+"/shares", jsonBody(t, map[string]any{
			"shared_with_user_id": peerID, "permission": perm, "notify": false,
		})); status != http.StatusCreated {
			t.Fatalf("sharing the collection as %s: %d %v", perm, status, out)
		}
	}
	_, list := peer.rest(http.MethodGet, "/api/v1/portal/collections?scope=shared&limit=200", http.NoBody)
	perms, _ := list["permissions"].(map[string]any)
	for perm, id := range ids {
		if perms[id] != perm {
			t.Errorf("the listing gives %v for a collection shared as %s", perms[id], perm)
		}
	}
}

// TestIssue2745_AViewersLinkNamesTheServedVersion: on an asset a release holds
// at version 1 with version 2 newer, a viewer without history is refused a
// link to version 2 and given one to version 1; the owner gets version 2.
func TestIssue2745_AViewersLinkNamesTheServedVersion(t *testing.T) {
	s := newShop2411(t)
	s.admin.call("manage_asset", map[string]any{"action": "update", "asset_id": s.asset, "content": "<h1>v2</h1>"})
	viewer := newIssue2241Member(t, s.admin, "2745-viewer", nil)
	shareAs(t, s.admin, s.asset, viewer, "viewer")
	base := "/api/v1/portal/assets/" + s.asset + "/content-url"
	if status, body := rawGet(t, s.admin.base, base+"?version=2&ttl=60", viewer.key); status != http.StatusForbidden {
		t.Errorf("a viewer's link to the held-back version: %d %.200s", status, body)
	}
	status, body := rawGet(t, s.admin.base, base+"?ttl=60", viewer.key)
	if status != http.StatusOK || !strings.Contains(string(body), `"version":1`) {
		t.Errorf("a viewer's link with no version: %d %.200s", status, body)
	}
	if status, body := rawGet(t, s.admin.base, base+"?version=2&ttl=60", s.admin.apiKey); status != http.StatusOK {
		t.Errorf("the owner's link to version 2: %d %.200s", status, body)
	}
}

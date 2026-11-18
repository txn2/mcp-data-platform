//go:build integration

package acceptance

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Acceptance for #2689: an api connection obtains its token with the OAuth
// password grant, for a legacy upstream that offers no other. The platform is
// only ever the client here; it issues no password-grant tokens itself.
//
// The token server is the dev stack's Keycloak, playing a legacy upstream's
// token endpoint: the realm's mcp-acceptance-short-lived client takes the
// password grant and issues access tokens that live one second, inside the
// oauth2 library's ten-second expiry margin, so every call after the first
// renews and no criterion waits on a clock. Each criterion signs in as a user
// it creates with a password of its own, so a sink search looks for a value
// nothing else on the stack holds. The API is a server in this test that
// answers 200 only to a bearer whose RS256 signature verifies against the
// realm's published keys, and records each token it was sent; Keycloak's
// session id (sid) in a token tells a refresh (same session) from a new
// password sign-in (a new one). The calls are pinned to one replica, since
// each replica holds its own token.
//
// Wire forms: the admin API takes one JSON object per route, and
// oauth_password is sent as a literal string and as a {{secret:<name>}}
// reference to a stored secret; api_invoke_endpoint takes connection, method,
// path and purpose as strings. The refresh token is held in the platform's
// memory and never shown to this test, so the sink search covers the
// password, the client secret and every access token the API received.

const (
	issue2689Purpose = "Acceptance for #2689: read through a connection signed in with the password grant."
	issue2689Client  = "mcp-acceptance-short-lived"
	issue2689Secret  = "acceptance-short-lived-secret"
)

// issue2689Token is what the API learned from one bearer it accepted.
type issue2689Token struct {
	raw, user, client, session, id string
}

// issue2689API is the upstream: it accepts a bearer the realm signed and
// records it.
type issue2689API struct {
	t      *testing.T
	mu     sync.Mutex
	keys   map[string]*rsa.PublicKey
	tokens []issue2689Token
}

func (a *issue2689API) serve(w http.ResponseWriter, r *http.Request) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	claims := jwt.MapClaims{}
	// Expiry is not checked: the realm issues these for one second, and what
	// is proved here is that the realm issued it, to whom and in which
	// session.
	_, err := jwt.ParseWithClaims(raw, claims, func(tok *jwt.Token) (any, error) {
		kid, _ := tok.Header["kid"].(string)
		if key := a.keys[kid]; key != nil {
			return key, nil
		}
		return nil, fmt.Errorf("unknown key %q", kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithoutClaimsValidation())
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	str := func(k string) string { s, _ := claims[k].(string); return s }
	a.mu.Lock()
	a.tokens = append(a.tokens, issue2689Token{raw: raw, user: str("preferred_username"), client: str("azp"), session: str("sid"), id: str("jti")})
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"user":%q}`, str("preferred_username"))
}

func (a *issue2689API) last() issue2689Token {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.tokens) == 0 {
		a.t.Fatal("the API was sent no token")
	}
	return a.tokens[len(a.tokens)-1]
}

func (a *issue2689API) all() []issue2689Token {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]issue2689Token(nil), a.tokens...)
}

// issue2689Realm is the realm's token endpoint and signing keys.
func issue2689Realm(t *testing.T) (tokenURL string, keys map[string]*rsa.PublicKey) {
	t.Helper()
	base := keycloak2061() + "/realms/" + realm2061 + "/protocol/openid-connect"
	resp, err := http.Get(base + "/certs")
	if err != nil {
		t.Fatalf("reading the realm's keys at %s: %v (is the dev stack up? make dev)", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var set struct {
		Keys []struct{ Kid, Kty, Alg, Use, N, E string } `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		t.Fatal(err)
	}
	keys = map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Use == "enc" {
			continue
		}
		n, _ := base64.RawURLEncoding.DecodeString(k.N)
		e, _ := base64.RawURLEncoding.DecodeString(k.E)
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		t.Fatal("the realm publishes no RSA signing key")
	}
	return base + "/token", keys
}

// issue2689User creates a realm user with a password nothing else holds and
// removes it afterwards.
func issue2689User(t *testing.T) (username, password string) {
	t.Helper()
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	username = "acc-2689-" + strings.ToLower(unique1579())
	password = "Pw2689-" + hex.EncodeToString(b) + `"&%`
	token := kcAdminToken2061(t)
	if status, out := kcAdmin2061(t, token, http.MethodPost, "/users", map[string]any{
		"username": username, "enabled": true, "emailVerified": true,
		"email": username + "@example.com", "firstName": "Acceptance", "lastName": "2689",
		"credentials": []map[string]any{{"type": "password", "value": password, "temporary": false}},
	}); status != http.StatusCreated {
		t.Fatalf("creating the realm user answered %d: %s", status, out)
	}
	t.Cleanup(func() {
		tok := kcAdminToken2061(t)
		if id := issue2689UserID(t, tok, username); id != "" {
			kcAdmin2061(t, tok, http.MethodDelete, "/users/"+id, nil)
		}
	})
	return username, password
}

func issue2689UserID(t *testing.T, token, username string) string {
	t.Helper()
	_, out := kcAdmin2061(t, token, http.MethodGet, "/users?exact=true&username="+username, nil)
	var users []struct{ ID string }
	_ = json.Unmarshal(out, &users)
	if len(users) == 0 {
		return ""
	}
	return users[0].ID
}

// issue2689Setup is the API, the user, and a client pinned to one replica.
type issue2689Setup struct {
	admin    *client
	api      *issue2689API
	base     string
	tokenURL string
	user     string
	password string
}

func issue2689Start(t *testing.T) *issue2689Setup {
	t.Helper()
	r := replicas(t)[0]
	tokenURL, keys := issue2689Realm(t)
	api := &issue2689API{t: t, keys: keys}
	srv := httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	user, password := issue2689User(t)
	return &issue2689Setup{admin: connectAt(t, r.base, devAPIKey()), api: api, base: srv.URL, tokenURL: tokenURL, user: user, password: password}
}

// connection saves an api connection on the password grant, with password as
// oauth_password, and returns its name.
func (s *issue2689Setup) connection(t *testing.T, password string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2689-%d", time.Now().UnixNano())
	s.save(t, name, password)
	return name
}

func (s *issue2689Setup) save(t *testing.T, name, password string) {
	t.Helper()
	status, body := s.admin.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, jsonBody(t, map[string]any{
		"description": "Acceptance 2689: a legacy API whose tokens come from the password grant.",
		"config": map[string]any{
			"base_url": s.base, "connection_name": name,
			"auth_mode": "oauth", "oauth_grant": "password",
			"oauth_token_url": s.tokenURL, "oauth_client_id": issue2689Client, "oauth_client_secret": issue2689Secret,
			"oauth_username": s.user, "oauth_password": password, "oauth_scope": "openid",
		},
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
	t.Cleanup(func() { s.admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
}

// read is one GET through the connection; it returns the status and the text.
func (s *issue2689Setup) read(t *testing.T, conn string) (int, string) {
	t.Helper()
	res, text, err := s.admin.callRaw("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "GET", "path": "/v1/me", "purpose": issue2689Purpose,
	})
	if err != nil {
		t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	if res.IsError {
		return 0, text
	}
	var out struct {
		Status int `json:"status"`
	}
	_ = json.Unmarshal([]byte(text), &out)
	return out.Status, text
}

// probe runs the admin connection test and returns its result: 200 for a
// test that passed, 503 with ok false for one that did not.
func (s *issue2689Setup) probe(t *testing.T, conn string) map[string]any {
	t.Helper()
	status, body := s.admin.rest(http.MethodPost, "/api/v1/admin/connection-instances/api/"+conn+"/test", http.NoBody)
	if status != http.StatusOK && status != http.StatusServiceUnavailable {
		t.Fatalf("the connection test answered HTTP %d: %v", status, body)
	}
	if (status == http.StatusOK) != (body["ok"] == true) {
		t.Fatalf("the connection test answered HTTP %d with ok %v", status, body["ok"])
	}
	return body
}

// TestIssue2689_AReadCarriesATokenFromThePasswordGrant: the connection saves,
// its test obtains a token for the API user, and a read returns 200 carrying
// a token the realm issued to that user through the connection's client, with
// oauth_password sent as written and as a stored secret.
func TestIssue2689_AReadCarriesATokenFromThePasswordGrant(t *testing.T) {
	s := issue2689Start(t)
	literal := s.connection(t, s.password)

	viaSecret := fmt.Sprintf("acc-2689-secret-%d", time.Now().UnixNano())
	secret := issue2066Secret(t, s.admin, viaSecret, s.password)
	s.save(t, viaSecret, "{{secret:"+secret+"}}")

	for form, conn := range map[string]string{"literal": literal, "stored secret": viaSecret} {
		result := s.probe(t, conn)
		if result["ok"] != true {
			t.Fatalf("%s: the connection test failed: %v", form, result)
		}
		if !strings.Contains(fmt.Sprint(result), "issued a token for "+s.user) {
			t.Errorf("%s: the connection test does not name the API user: %v", form, result)
		}
		status, text := s.read(t, conn)
		if status != http.StatusOK {
			t.Fatalf("%s: a read through the connection answered %d: %s", form, status, text)
		}
		got := s.api.last()
		if got.user != s.user || got.client != issue2689Client {
			t.Errorf("%s: the API was sent a token for %q through %q, want %q through %q", form, got.user, got.client, s.user, issue2689Client)
		}
	}
}

// TestIssue2689_AnExpiredTokenIsRenewedWithTheRefreshToken: the second read,
// after the first token expired, carries a new token in the same realm
// session: the refresh token renewed it, not the password.
func TestIssue2689_AnExpiredTokenIsRenewedWithTheRefreshToken(t *testing.T) {
	s := issue2689Start(t)
	conn := s.connection(t, s.password)
	if status, text := s.read(t, conn); status != http.StatusOK {
		t.Fatalf("the first read answered %d: %s", status, text)
	}
	first := s.api.last()
	if status, text := s.read(t, conn); status != http.StatusOK {
		t.Fatalf("the read after expiry answered %d: %s", status, text)
	}
	second := s.api.last()
	if second.id == first.id {
		t.Fatal("the read after expiry carried the expired token")
	}
	if first.session == "" || second.session != first.session {
		t.Errorf("the renewed token is in session %q, the first in %q: it was not renewed with the refresh token", second.session, first.session)
	}
}

// TestIssue2689_ARefusedRefreshIsRenewedWithThePassword: with the realm
// session ended, the upstream refuses the refresh token, and the next read
// still returns 200, carrying a token from a new password sign-in.
func TestIssue2689_ARefusedRefreshIsRenewedWithThePassword(t *testing.T) {
	s := issue2689Start(t)
	conn := s.connection(t, s.password)
	if status, text := s.read(t, conn); status != http.StatusOK {
		t.Fatalf("the first read answered %d: %s", status, text)
	}
	first := s.api.last()
	token := kcAdminToken2061(t)
	if status, out := kcAdmin2061(t, token, http.MethodDelete, "/sessions/"+first.session, nil); status != http.StatusNoContent {
		t.Fatalf("ending the realm session answered %d: %s", status, out)
	}
	if status, text := s.read(t, conn); status != http.StatusOK {
		t.Fatalf("the read after the refresh was refused answered %d: %s", status, text)
	}
	second := s.api.last()
	if second.session == "" || second.session == first.session {
		t.Errorf("the token after a refused refresh is in session %q, the first in %q: no new password sign-in", second.session, first.session)
	}
}

// TestIssue2689_AWrongPasswordIsAuthFailedWithTheUpstreamReason: the test
// and a read fail naming the upstream's invalid_grant and its description,
// and the connection's state turns auth_failed.
func TestIssue2689_AWrongPasswordIsAuthFailedWithTheUpstreamReason(t *testing.T) {
	s := issue2689Start(t)
	conn := s.connection(t, s.password+"-wrong")
	result := s.probe(t, conn)
	if result["ok"] == true {
		t.Fatalf("the connection test passed with a wrong password: %v", result)
	}
	for _, text := range []string{fmt.Sprint(result), func() string { _, txt := s.read(t, conn); return txt }()} {
		if !strings.Contains(text, "invalid_grant") || !strings.Contains(text, "Invalid user credentials") {
			t.Errorf("the failure does not carry the upstream's invalid_grant and description: %s", text)
		}
	}
	rec := waitLog2442(t, conn, "connection state changed")
	if rec["to_state"] != "auth_failed" {
		t.Errorf("the connection's state change: %v", rec)
	}
}

// TestIssue2689_TheCredentialsAndTokensReachNoSink: after reads, a refused
// refresh and a wrong password, none of the password, the client secret or
// any access token the API received is found, in any spelling, in the
// connection read, the config export, the audit rows, the call catalog, the
// connection test, the tool errors, or the logs and spans.
func TestIssue2689_TheCredentialsAndTokensReachNoSink(t *testing.T) {
	s := issue2689Start(t)
	conn := s.connection(t, s.password)
	for range 2 {
		if status, text := s.read(t, conn); status != http.StatusOK {
			t.Fatalf("a read answered %d: %s", status, text)
		}
	}
	wrong := s.connection(t, s.password+"-wrong")
	_, refusal := s.read(t, wrong)

	sinks := map[string]string{"tool error": refusal}
	sinks["connection test"] = fmt.Sprint(s.probe(t, wrong))
	for _, name := range []string{conn, wrong} {
		status, body := s.admin.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("reading the connection back answered %d", status)
		}
		cfg, _ := body["config"].(map[string]any)
		if cfg["oauth_password"] != "[REDACTED]" || cfg["oauth_client_secret"] != "[REDACTED]" {
			t.Errorf("the connection read does not redact the password and client secret: %v", cfg)
		}
		raw, _ := json.Marshal(body)
		sinks["connection read "+name] = string(raw)
	}
	_, export := s.admin.rest(http.MethodGet, "/api/v1/admin/config/export", http.NoBody)
	raw, _ := json.Marshal(export)
	sinks["config export"] = string(raw)
	raw, _ = json.Marshal(s.admin.list("/api/v1/admin/audit/events?session_id=" + s.admin.sessionID + "&per_page=50"))
	sinks["audit rows"] = string(raw)
	_, calls := s.admin.rest(http.MethodGet, "/api/v1/admin/calls?connection="+conn+"&per_page=25", http.NoBody)
	raw, _ = json.Marshal(calls)
	sinks["call catalog"] = string(raw)
	sinks["logs and spans"] = devSinkText(t)

	secrets := map[string]string{"password": s.password, "client secret": issue2689Secret}
	for i, tok := range s.api.all() {
		secrets[fmt.Sprintf("access token %d", i+1)] = tok.raw
	}
	for sink, text := range sinks {
		for what, value := range secrets {
			for _, form := range secretForms(value) {
				if strings.Contains(text, form) {
					t.Errorf("the %s reached the %s", what, sink)
				}
			}
		}
	}
}

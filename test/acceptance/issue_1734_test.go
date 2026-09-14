//go:build integration

package acceptance

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issue #1734: oauth_grant jwt_bearer signs a short-lived assertion with a key
// the upstream registered and exchanges it at the upstream's token endpoint
// (RFC 7523) for the access token every call then carries. A refusal reaches
// the model with the upstream's error code and description and opens the
// connection alert, which the next accepted exchange clears.
//
// The token endpoint is a fixture this file starts, for the reason #1648's is:
// no upstream the deployments run offers this grant, and the providers that do
// are account-scoped. The fixture asserts the platform's contract and nothing
// else. It verifies the assertion's signature against the public half of the
// key the connection was configured with, compares grant_type and the claims
// to what it was told to expect, refuses a replayed jti, issues an access
// token, and serves a resource route that accepts only a token it issued. Its
// refusal is a plain RFC 6749 error object with a description of its own
// wording.
//
// The fixture listens on the loopback interface of the machine running the
// suite, which is the machine `make dev` runs the platform on.
//
// Wire forms: api_invoke_endpoint's `body` is untyped and admits an object and
// a string of JSON; both are sent as literal tools/call params against a
// jwt_bearer connection and asserted to reach the resource with the exchanged
// token (TestIssue1734_AcrossWireForms). The connection config fields written
// through the admin route (`auth_mode`, `oauth_grant`, `oauth_token_url`,
// `oauth_client_id`, `oauth_client_secret`, `oauth_scope`,
// `oauth_endpoint_auth_style`, `jwt_*`) are strings, the one form each admits;
// the alert settings route takes `enabled` as a bool, `escalate_after_hours`
// as a number and `recipients` as an array of strings.

const (
	issue1734Purpose  = "Acceptance for #1734: the jwt_bearer grant exchanges a signed assertion for an access token."
	issue1734Issuer   = "3MVG9-acc1734-consumer-key"
	issue1734Subject  = "integration@acc1734.example.com"
	issue1734Refusal  = "user hasn't approved this consumer"
	issue1734AlertAPI = "/api/v1/admin/settings/connection-alert"
)

// issue1734Expect is what one label's token endpoint requires of an assertion
// and answers with.
type issue1734Expect struct {
	key      any    // the public key the assertion must verify against
	alg      string // the algorithm it must be signed with
	aud      string // the audience it must carry; empty means the label's token URL
	clientID string // when set, the token request must authenticate this client
	secret   string
	// expiresIn is the expires_in the token response carries; zero omits it.
	expiresIn int
}

// issue1734TokenRequest is what one exchange put on the wire.
type issue1734TokenRequest struct {
	grantType     string
	scope         string
	authorization string
	formClientID  string
	claims        jwt.MapClaims
	kid           any
}

// issue1734Fixture is the token endpoint and resource server. Routes are
// /<label>/token, /<label>/echo and /<label>/graphql.
type issue1734Fixture struct {
	url string

	mu        sync.Mutex
	expect    map[string]issue1734Expect
	refuse    map[string]bool
	exchanges map[string][]issue1734TokenRequest
	issued    map[string]string // access token -> label
	presented map[string][]string
	jtis      map[string]bool
	counter   atomic.Int64
}

func issue1734StartFixture(t *testing.T) *issue1734Fixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the jwt_bearer fixture: %v", err)
	}
	f := &issue1734Fixture{
		url:       "http://" + ln.Addr().String(),
		expect:    map[string]issue1734Expect{},
		refuse:    map[string]bool{},
		exchanges: map[string][]issue1734TokenRequest{},
		issued:    map[string]string{},
		presented: map[string][]string{},
		jtis:      map[string]bool{},
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

// register declares a label and returns its base URL and token URL.
func (f *issue1734Fixture) register(label string, e issue1734Expect) (base, tokenURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expect[label] = e
	return f.url + "/" + label, f.url + "/" + label + "/token"
}

func (f *issue1734Fixture) setRefuse(label string, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse[label] = v
}

func (f *issue1734Fixture) exchangesFor(label string) []issue1734TokenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]issue1734TokenRequest(nil), f.exchanges[label]...)
}

func (f *issue1734Fixture) presentedTo(label string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.presented[label]...)
}

func (f *issue1734Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	label, route := parts[0], parts[1]
	f.mu.Lock()
	expect, known := f.expect[label]
	f.mu.Unlock()
	if !known {
		http.Error(w, "no expectation registered for "+label, http.StatusNotFound)
		return
	}
	if route == "token" {
		f.token(w, r, label, expect)
		return
	}
	f.resource(w, r, label)
}

// token is the RFC 7523 section 2.1 token endpoint.
func (f *issue1734Fixture) token(w http.ResponseWriter, r *http.Request, label string, expect issue1734Expect) {
	_ = r.ParseForm()
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), claims,
		func(*jwt.Token) (any, error) { return expect.key, nil },
		jwt.WithValidMethods([]string{expect.alg}))
	seen := issue1734TokenRequest{
		grantType:     r.PostForm.Get("grant_type"),
		scope:         r.PostForm.Get("scope"),
		authorization: r.Header.Get("Authorization"),
		formClientID:  r.PostForm.Get("client_id"),
		claims:        claims,
	}
	if parsed != nil {
		seen.kid = parsed.Header["kid"]
	}

	f.mu.Lock()
	f.exchanges[label] = append(f.exchanges[label], seen)
	refuse := f.refuse[label]
	jti, _ := claims["jti"].(string)
	replayed := jti == "" || f.jtis[jti]
	f.jtis[jti] = true
	f.mu.Unlock()

	wantAud := expect.aud
	if wantAud == "" {
		wantAud = f.url + "/" + label + "/token"
	}
	switch {
	case refuse:
		issue1734Error(w, http.StatusBadRequest, "invalid_grant", issue1734Refusal)
		return
	case err != nil:
		issue1734Error(w, http.StatusBadRequest, "invalid_grant", "the assertion did not verify against the registered key")
		return
	case seen.grantType != "urn:ietf:params:oauth:grant-type:jwt-bearer":
		issue1734Error(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type was "+seen.grantType)
		return
	case replayed:
		issue1734Error(w, http.StatusBadRequest, "invalid_grant", "the assertion's jti is missing or was already used")
		return
	case claims["iss"] != issue1734Issuer || claims["sub"] != issue1734Subject || claims["aud"] != wantAud:
		issue1734Error(w, http.StatusBadRequest, "invalid_grant", fmt.Sprintf("claims iss=%v sub=%v aud=%v", claims["iss"], claims["sub"], claims["aud"]))
		return
	}
	if expect.clientID != "" {
		user, pass, ok := r.BasicAuth()
		if !ok || user != expect.clientID || pass != expect.secret {
			issue1734Error(w, http.StatusUnauthorized, "invalid_client", "the client did not authenticate")
			return
		}
	}

	token := fmt.Sprintf("acc-1734-%s-at-%d", label, f.counter.Add(1))
	f.mu.Lock()
	f.issued[token] = label
	f.mu.Unlock()
	answer := map[string]any{"access_token": token, "token_type": "Bearer"}
	if expect.expiresIn > 0 {
		answer["expires_in"] = expect.expiresIn
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// resource accepts only an access token the token endpoint issued for the
// label, and answers with the token and the body it received.
func (f *issue1734Fixture) resource(w http.ResponseWriter, r *http.Request, label string) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.presented[label] = append(f.presented[label], token)
	issuedFor := f.issued[token]
	f.mu.Unlock()
	if issuedFor != label {
		http.Error(w, "not a token this endpoint issued", http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "request_body": string(body)})
}

func issue1734Error(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}

var (
	issue1734RSAKey = sync.OnceValues(func() (*rsa.PrivateKey, string) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		return key, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	})
	issue1734ECKey = sync.OnceValues(func() (*ecdsa.PrivateKey, string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			panic(err)
		}
		return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	})
)

// issue1734Connect saves a jwt_bearer connection of kind through the admin
// route and returns its name.
func issue1734Connect(t *testing.T, c *client, kind, label string, base map[string]any, cfg map[string]any) string {
	t.Helper()
	name := fmt.Sprintf("acc-1734-%s-%s-%d", kind, label, time.Now().UnixNano())
	config := map[string]any{
		"connection_name": name,
		"connect_timeout": "5s",
		"call_timeout":    "10s",
		"auth_mode":       "oauth",
		"oauth_grant":     "jwt_bearer",
		"jwt_issuer":      issue1734Issuer,
		"jwt_subject":     issue1734Subject,
	}
	for k, v := range base {
		config[k] = v
	}
	for k, v := range cfg {
		config[k] = v
	}
	path := "/api/v1/admin/connection-instances/" + kind + "/" + name
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": config, "description": "Acceptance 1734: " + label,
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register %s/%s: HTTP %d (%v)", kind, label, status, body)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	return name
}

// issue1734Call invokes the resource through the connection and returns the
// tool result's text and whether it was a tool error.
func issue1734Call(t *testing.T, c *client, connection string, extra map[string]any) (text string, isError bool) {
	t.Helper()
	args := map[string]any{
		"connection": connection,
		"method":     http.MethodGet,
		"path":       "/echo",
		"purpose":    issue1734Purpose,
	}
	for k, v := range extra {
		args[k] = v
	}
	res, text, err := c.callRaw("api_invoke_endpoint", args)
	if err != nil {
		t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	return text, res.IsError
}

// issue1734Reached calls the resource and returns what it answered, failing
// unless the call reached it with a token the endpoint issued.
func issue1734Reached(t *testing.T, c *client, connection string, extra map[string]any) map[string]any {
	t.Helper()
	text, isError := issue1734Call(t, c, connection, extra)
	if isError {
		t.Fatalf("the call failed: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, text)
	}
	if got := number(t, out, "status"); got != http.StatusOK {
		t.Fatalf("the resource refused the call: HTTP %v, body %v", got, out["body"])
	}
	body, ok := out["body"].(map[string]any)
	if !ok {
		t.Fatalf("the resource's answer is not an object: %v", out["body"])
	}
	return body
}

// TestIssue1734_TheAssertionIsExchangedForAnAccessToken is the central
// criterion: an api connection on the grant POSTs a verified RS256 assertion
// carrying the configured issuer and subject, the token URL as its default
// audience and a jti, with grant_type jwt-bearer and the configured scope and
// no client credential, and the call reaches the resource with the access
// token the exchange issued. The signing key is not readable back.
func TestIssue1734_TheAssertionIsExchangedForAnAccessToken(t *testing.T) {
	c := connect(t)
	f := issue1734StartFixture(t)
	rsaKey, rsaPEM := issue1734RSAKey()
	base, tokenURL := f.register("exchange", issue1734Expect{key: &rsaKey.PublicKey, alg: "RS256", expiresIn: 3600})
	conn := issue1734Connect(t, c, "api", "exchange", map[string]any{"base_url": base}, map[string]any{
		"oauth_token_url":     tokenURL,
		"oauth_scope":         "api refresh_token",
		"jwt_private_key_pem": rsaPEM,
	})

	body := issue1734Reached(t, c, conn, nil)
	token, _ := body["token"].(string)
	if !strings.HasPrefix(token, "acc-1734-exchange-at-") {
		t.Errorf("the resource received %q, want an access token the endpoint issued", token)
	}

	exchanges := f.exchangesFor("exchange")
	if len(exchanges) == 0 {
		t.Fatal("the token endpoint was never called")
	}
	sent := exchanges[0]
	if sent.grantType != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant_type = %q", sent.grantType)
	}
	if sent.scope != "api refresh_token" {
		t.Errorf("scope = %q, want the configured scope", sent.scope)
	}
	if sent.authorization != "" || sent.formClientID != "" {
		t.Errorf("a connection with no client credential sent one: Authorization=%q client_id=%q", sent.authorization, sent.formClientID)
	}
	if sent.claims["iss"] != issue1734Issuer || sent.claims["sub"] != issue1734Subject {
		t.Errorf("iss/sub = %v/%v", sent.claims["iss"], sent.claims["sub"])
	}
	if sent.claims["aud"] != tokenURL {
		t.Errorf("aud = %v, want the token URL %q", sent.claims["aud"], tokenURL)
	}
	if jti, _ := sent.claims["jti"].(string); jti == "" {
		t.Error("the assertion carries no jti")
	}
	iat, _ := sent.claims["iat"].(float64)
	exp, _ := sent.claims["exp"].(float64)
	if exp-iat != 300 {
		t.Errorf("exp - iat = %v, want the default 300s", exp-iat)
	}

	status, read := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+conn, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("read the connection back: HTTP %d", status)
	}
	if text := fmt.Sprint(read); strings.Contains(text, "PRIVATE KEY") || !strings.Contains(text, "[REDACTED]") {
		t.Errorf("the connection read does not redact the signing key: %s", text)
	}
}

// TestIssue1734_TheUpstreamsOptionsReachTheExchange covers the configuration an
// upstream can ask for beyond the defaults: ES256 with a kid, an audience other
// than the token URL, and client authentication on the token request.
func TestIssue1734_TheUpstreamsOptionsReachTheExchange(t *testing.T) {
	c := connect(t)
	f := issue1734StartFixture(t)
	ecKey, ecPEM := issue1734ECKey()
	base, tokenURL := f.register("options", issue1734Expect{
		key: &ecKey.PublicKey, alg: "ES256", aud: "https://login.acc1734.example.com",
		clientID: "acc-1734-client", secret: "acc-1734-client-secret", expiresIn: 3600,
	})
	conn := issue1734Connect(t, c, "api", "options", map[string]any{"base_url": base}, map[string]any{
		"oauth_token_url":           tokenURL,
		"oauth_client_id":           "acc-1734-client",
		"oauth_client_secret":       "acc-1734-client-secret",
		"oauth_endpoint_auth_style": "header",
		"jwt_algorithm":             "ES256",
		"jwt_private_key_pem":       ecPEM,
		"jwt_key_id":                "acc-1734-es",
		"jwt_audience":              "https://login.acc1734.example.com",
	})

	issue1734Reached(t, c, conn, nil)
	sent := f.exchangesFor("options")[0]
	if sent.kid != "acc-1734-es" {
		t.Errorf("kid = %v, want the configured key id", sent.kid)
	}
	if !strings.HasPrefix(sent.authorization, "Basic ") {
		t.Errorf("the token request carried no Basic client authentication: %q", sent.authorization)
	}
}

// TestIssue1734_EveryHTTPKindExchanges proves the grant lives on the shared
// seam: a graphql connection reads its schema when it attaches, and that read
// reaches the endpoint with an access token the exchange issued.
func TestIssue1734_EveryHTTPKindExchanges(t *testing.T) {
	c := connect(t)
	f := issue1734StartFixture(t)
	rsaKey, rsaPEM := issue1734RSAKey()
	base, tokenURL := f.register("graphql-kind", issue1734Expect{key: &rsaKey.PublicKey, alg: "RS256", expiresIn: 3600})
	issue1734Connect(t, c, "graphql", "graphql-kind", map[string]any{"endpoint_url": base + "/graphql"}, map[string]any{
		"oauth_token_url":     tokenURL,
		"jwt_private_key_pem": rsaPEM,
	})

	deadline := time.Now().Add(20 * time.Second)
	for len(f.presentedTo("graphql-kind")) == 0 && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	presented := f.presentedTo("graphql-kind")
	if len(presented) == 0 {
		t.Fatal("the graphql connection made no call to its endpoint")
	}
	if !strings.HasPrefix(presented[0], "acc-1734-graphql-kind-at-") {
		t.Errorf("the graphql kind presented %q, want an access token the endpoint issued", presented[0])
	}
}

// TestIssue1734_TheAccessTokenIsReused proves the access token, not the
// assertion, is what is cached, on every replica: once a replica holds a token
// (here one whose response carried no expires_in, which the platform bounds
// rather than treating as eternal), further calls do not exchange again.
func TestIssue1734_TheAccessTokenIsReused(t *testing.T) {
	f := issue1734StartFixture(t)
	rsaKey, rsaPEM := issue1734RSAKey()
	base, tokenURL := f.register("reuse", issue1734Expect{key: &rsaKey.PublicKey, alg: "RS256"})
	conn := issue1734Connect(t, connect(t), "api", "reuse", map[string]any{"base_url": base}, map[string]any{
		"oauth_token_url":     tokenURL,
		"jwt_private_key_pem": rsaPEM,
	})

	forEachReplica(t, func(t *testing.T, c *client) {
		issue1734Reached(t, c, conn, nil)
		held := len(f.exchangesFor("reuse"))
		issue1734Reached(t, c, conn, nil)
		issue1734Reached(t, c, conn, nil)
		if got := len(f.exchangesFor("reuse")); got != held {
			t.Errorf("token requests went from %d to %d over two calls on a replica holding a token", held, got)
		}
	})
}

// TestIssue1734_AcrossWireForms sends api_invoke_endpoint's untyped body in both
// forms its schema admits and asserts each reaches the resource with the
// exchanged token.
func TestIssue1734_AcrossWireForms(t *testing.T) {
	c := connect(t)
	f := issue1734StartFixture(t)
	rsaKey, rsaPEM := issue1734RSAKey()
	base, tokenURL := f.register("wire-forms", issue1734Expect{key: &rsaKey.PublicKey, alg: "RS256", expiresIn: 3600})
	conn := issue1734Connect(t, c, "api", "wire-forms", map[string]any{"base_url": base}, map[string]any{
		"oauth_token_url":     tokenURL,
		"jwt_private_key_pem": rsaPEM,
	})

	for _, form := range []struct {
		name string
		body any
	}{
		{"body as an object", map[string]any{"note": "acc-1734"}},
		{"body as a string of JSON", `{"note":"acc-1734"}`},
	} {
		t.Run(form.name, func(t *testing.T) {
			got := issue1734Reached(t, c, conn, map[string]any{"method": http.MethodPost, "body": form.body})
			if token, _ := got["token"].(string); !strings.HasPrefix(token, "acc-1734-wire-forms-at-") {
				t.Errorf("the resource received %q, want the exchanged token", token)
			}
			if rb, _ := got["request_body"].(string); !strings.Contains(rb, "acc-1734") {
				t.Errorf("the resource received body %q", rb)
			}
		})
	}
}

// TestIssue1734_TheSaveRefusesAnUnusableConfig is the save-time criterion: each
// refused shape names the key to fix, the unknown-grant refusal lists the new
// grant, and the mcp kind refuses the grant it does not sign.
func TestIssue1734_TheSaveRefusesAnUnusableConfig(t *testing.T) {
	c := connect(t)
	_, rsaPEM := issue1734RSAKey()
	complete := map[string]any{
		"base_url":            "https://upstream.invalid/api",
		"auth_mode":           "oauth",
		"oauth_grant":         "jwt_bearer",
		"oauth_token_url":     "https://login.invalid/token",
		"jwt_private_key_pem": rsaPEM,
		"jwt_issuer":          issue1734Issuer,
		"jwt_subject":         issue1734Subject,
	}
	tests := []struct {
		name   string
		kind   string
		change map[string]any
		drop   []string
		want   string
	}{
		{"no private key", "api", nil, []string{"jwt_private_key_pem"}, "jwt_private_key_pem"},
		{"no issuer", "api", nil, []string{"jwt_issuer"}, "jwt_issuer"},
		{"no subject", "api", nil, []string{"jwt_subject"}, "jwt_subject"},
		{"no token URL", "api", nil, []string{"oauth_token_url"}, "oauth_token_url"},
		{"a client secret with no client id", "api", map[string]any{"oauth_client_secret": "s3cret"}, nil, "oauth_client_id"},
		{"a shared secret under RS256", "api", map[string]any{"jwt_client_secret": "shared"}, nil, "jwt_client_secret"},
		{"a mistyped grant", "api", map[string]any{"oauth_grant": "jwt-bearer"}, nil, "want authorization_code, client_credentials or jwt_bearer"},
		{"the mcp kind", "mcp", map[string]any{
			"endpoint": "https://upstream.invalid/mcp", "oauth_client_id": "cid", "oauth_client_secret": "csec",
		}, []string{"base_url"}, `oauth.grant "jwt_bearer" not supported`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("acc-1734-invalid-%d", time.Now().UnixNano())
			cfg := map[string]any{"connection_name": name}
			for k, v := range complete {
				cfg[k] = v
			}
			for _, k := range tc.drop {
				delete(cfg, k)
			}
			for k, v := range tc.change {
				cfg[k] = v
			}
			path := "/api/v1/admin/connection-instances/" + tc.kind + "/" + name
			status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg}))
			if status == http.StatusCreated || status == http.StatusOK {
				c.rest(http.MethodDelete, path, http.NoBody)
				t.Fatalf("the save accepted %s", tc.name)
			}
			text := fmt.Sprint(body)
			if !strings.Contains(text, tc.want) {
				t.Errorf("the refusal %q does not name %q", text, tc.want)
			}
			if strings.Contains(text, "PRIVATE KEY") {
				t.Errorf("the refusal carries key material: %s", text)
			}
		})
	}
}

// issue1734AlertRow is one queued connection alert as the admin Notifications
// tab reads it.
type issue1734AlertRow struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	ItemTitle string `json:"item_title"`
}

func issue1734AlertsFor(t *testing.T, c *client, recipient, connection string) []issue1734AlertRow {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/notifications?category=connection_auth&recipient="+
		url.QueryEscape(recipient)+"&per_page=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the notification history: HTTP %d (%v)", status, body)
	}
	raw, _ := json.Marshal(body["data"])
	var rows []issue1734AlertRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decoding the notification page: %v", err)
	}
	var out []issue1734AlertRow
	for _, row := range rows {
		if row.ItemTitle == connection {
			out = append(out, row)
		}
	}
	return out
}

// issue1734AwaitAlerts waits for want alerts naming the connection, then a
// little longer, so a count above want is seen rather than raced past.
func issue1734AwaitAlerts(t *testing.T, c *client, recipient, connection string, want int) []issue1734AlertRow {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	rows := issue1734AlertsFor(t, c, recipient, connection)
	for len(rows) < want && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		rows = issue1734AlertsFor(t, c, recipient, connection)
	}
	time.Sleep(2 * time.Second)
	return issue1734AlertsFor(t, c, recipient, connection)
}

// issue1734AlertTo points the connection alert at recipient for the test and
// restores what was configured before.
func issue1734AlertTo(t *testing.T, c *client, recipient string) {
	t.Helper()
	status, before := c.rest(http.MethodGet, issue1734AlertAPI, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the alert settings: HTTP %d (%v)", status, before)
	}
	t.Cleanup(func() {
		c.rest(http.MethodPut, issue1734AlertAPI, jsonBody(t, map[string]any{
			"enabled": before["enabled"], "escalate_after_hours": before["escalate_after_hours"],
			"recipients": before["recipients"],
		}))
	})
	status, body := c.rest(http.MethodPut, issue1734AlertAPI, jsonBody(t, map[string]any{
		"enabled": true, "escalate_after_hours": 24, "recipients": []string{recipient},
	}))
	if status != http.StatusOK {
		t.Fatalf("writing the alert settings: HTTP %d (%v)", status, body)
	}
}

// TestIssue1734_ARefusalReachesTheModelAndTheAlert is the refusal criterion: a
// token endpoint's invalid_grant reaches the model with its code and
// description; the operator's recipient is mailed once for the open refusal,
// however many calls fail, in the refused-assertion wording; the call after the
// upstream accepts again succeeds; and that accepted exchange closed the alert,
// which is observed as the next refusal being announced again.
func TestIssue1734_ARefusalReachesTheModelAndTheAlert(t *testing.T) {
	c := connect(t)
	recipient := fmt.Sprintf("oncall-%d@acc1734.example.com", time.Now().UnixNano())
	issue1734AlertTo(t, c, recipient)

	f := issue1734StartFixture(t)
	rsaKey, rsaPEM := issue1734RSAKey()
	// expires_in 1 is inside the platform's expiry margin, so every call
	// exchanges: the refusal and the recovery are each one call away.
	base, tokenURL := f.register("refusal", issue1734Expect{key: &rsaKey.PublicKey, alg: "RS256", expiresIn: 1})
	conn := issue1734Connect(t, c, "api", "refusal", map[string]any{"base_url": base}, map[string]any{
		"oauth_token_url":     tokenURL,
		"jwt_private_key_pem": rsaPEM,
	})
	issue1734Reached(t, c, conn, nil)

	f.setRefuse("refusal", true)
	for range 2 {
		text, isError := issue1734Call(t, c, conn, nil)
		if !isError {
			t.Fatalf("a refused exchange produced a successful call: %s", text)
		}
		if want := "invalid_grant: " + issue1734Refusal; !strings.Contains(text, want) {
			t.Errorf("the model was told %q, want it to carry %q", text, want)
		}
	}
	rows := issue1734AwaitAlerts(t, c, recipient, conn, 1)
	if len(rows) != 1 {
		t.Fatalf("%d alerts for two refused calls, want 1: %+v", len(rows), rows)
	}
	if want := fmt.Sprintf("The connection %q cannot get an access token", conn); rows[0].Subject != want {
		t.Errorf("subject = %q, want %q", rows[0].Subject, want)
	}

	f.setRefuse("refusal", false)
	issue1734Reached(t, c, conn, nil)

	f.setRefuse("refusal", true)
	if _, isError := issue1734Call(t, c, conn, nil); !isError {
		t.Fatal("the second refusal produced a successful call")
	}
	if rows = issue1734AwaitAlerts(t, c, recipient, conn, 2); len(rows) != 2 {
		t.Errorf("%d alerts after recovery and a new refusal, want 2: the accepted exchange did not close the first", len(rows))
	}
}

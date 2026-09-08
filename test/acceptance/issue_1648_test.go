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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issue #1648: auth_mode=signed_jwt mints the short-lived JWT an upstream
// validates from an identifier and a signing key issued out of band. There is
// no token endpoint and nothing is exchanged, so no existing mode can reach
// this class of upstream.
//
// The upstream here is a claim-validating fixture this file starts, because no
// upstream in this class is reachable from the project: Sage X3, Snowflake and
// App Store Connect are all customer- or account-scoped. The fixture asserts
// the PLATFORM's contract and nothing else — it decodes the presented token,
// verifies the signature against the key the connection was configured with,
// compares the claims to what it was told to expect, and reports what it saw.
// Its refusal bodies are its own plain text, not any vendor's error table:
// what the criteria prove is that whatever the upstream answered reaches the
// model unchanged.
//
// The fixture listens on the loopback interface of the machine running the
// suite, which is the machine `make dev` runs the platform on.
//
// Wire forms: api_invoke_endpoint's `body` is untyped and admits an object and
// a string of JSON. Both are sent as literal tools/call params against a
// signed_jwt connection and asserted to present the same verified token.

const (
	issue1648Purpose = "Acceptance for #1648: auth_mode=signed_jwt mints the assertion the upstream validates."
	issue1648Secret  = "acc-1648-shared-secret-issued-out-of-band"
	issue1648Issuer  = "CLIENTID-acc1648"
	issue1648Subject = "svc-acceptance"
)

// issue1648Expect is what the fixture requires of the token presented on one
// label's routes. An empty iss, sub or aud means the fixture does not check
// that claim.
type issue1648Expect struct {
	key      any
	iss      string
	sub      string
	aud      string
	requires string // the algorithm the token must be signed with
}

// issue1648Fixture is the claim-validating upstream. Routes are
// /<label>/echo and /<label>/graphql; the label selects which expectation the
// presented token is judged against, so one server serves every connection the
// suite registers.
type issue1648Fixture struct {
	url string

	mu     sync.Mutex
	expect map[string]issue1648Expect
	seen   map[string][]string
}

// issue1648StartFixture starts the fixture on an ephemeral loopback port and
// stops it when the test ends.
func issue1648StartFixture(t *testing.T) *issue1648Fixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the signed_jwt fixture: %v", err)
	}
	f := &issue1648Fixture{
		url:    "http://" + ln.Addr().String(),
		expect: map[string]issue1648Expect{},
		seen:   map[string][]string{},
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

// register declares what a label's tokens must satisfy and returns the base
// URL a connection with that label is pointed at.
func (f *issue1648Fixture) register(label string, e issue1648Expect) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expect[label] = e
	return f.url + "/" + label
}

// tokens returns the assertions the fixture accepted or refused for a label,
// in arrival order.
func (f *issue1648Fixture) tokens(label string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen[label]...)
}

func (f *issue1648Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	label := parts[0]
	f.mu.Lock()
	expect, known := f.expect[label]
	f.mu.Unlock()
	if !known {
		http.Error(w, "no expectation registered for "+label, http.StatusNotFound)
		return
	}

	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" || raw == r.Header.Get("Authorization") {
		issue1648Refuse(w, "no-bearer: the request carried no Bearer assertion")
		return
	}
	f.mu.Lock()
	f.seen[label] = append(f.seen[label], raw)
	f.mu.Unlock()

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return expect.key, nil },
		jwt.WithValidMethods([]string{expect.requires}))
	if err != nil {
		issue1648Refuse(w, "bad-signature: the assertion did not verify against the registered key")
		return
	}
	for _, check := range []struct{ name, want string }{
		{"iss", expect.iss}, {"sub", expect.sub}, {"aud", expect.aud},
	} {
		if check.want == "" {
			continue
		}
		got, _ := claims[check.name].(string)
		if got != check.want {
			issue1648Refuse(w, fmt.Sprintf("wrong-%s: the assertion claimed %q where this application registered %q",
				check.name, got, check.want))
			return
		}
	}

	// A verified assertion is answered with the claims and header the
	// fixture read out of it, which is what every criterion below reads.
	seen := map[string]any{
		"alg":   token.Method.Alg(),
		"kid":   token.Header["kid"],
		"iss":   claims["iss"],
		"sub":   claims["sub"],
		"aud":   claims["aud"],
		"iat":   claims["iat"],
		"exp":   claims["exp"],
		"token": raw,
	}
	if r.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		seen["request_body"] = string(body)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(seen)
}

// issue1648Refuse answers the way the upstreams in this class do: an HTTP 401
// whose plain-text body says which claim was wrong. The wording is the
// fixture's own.
func issue1648Refuse(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(reason))
}

// issue1648Connect registers one api connection in signed_jwt mode against the
// fixture and returns its name.
func issue1648Connect(t *testing.T, c *client, label, baseURL string, jwtCfg map[string]any) string {
	t.Helper()
	return issue1648ConnectKind(t, c, "api", label, map[string]any{"base_url": baseURL}, jwtCfg)
}

// issue1648ConnectKind registers one connection of any HTTP-based kind in
// signed_jwt mode. The mode lives on the shared seam, so the same keys are
// written on an api connection and on a graphql one.
func issue1648ConnectKind(t *testing.T, c *client, kind, label string, base, jwtCfg map[string]any) string {
	t.Helper()
	name := fmt.Sprintf("acc-1648-%s-%s-%d", kind, label, time.Now().UnixNano())
	cfg := map[string]any{
		"connection_name": name,
		"connect_timeout": "5s",
		"call_timeout":    "10s",
		"auth_mode":       "signed_jwt",
	}
	for k, v := range base {
		cfg[k] = v
	}
	for k, v := range jwtCfg {
		cfg[k] = v
	}
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/"+kind+"/"+name, map[string]any{
		"config":      cfg,
		"description": "Acceptance 1648: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register %s/%s connection: HTTP %d", kind, label, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/"+kind+"/"+name, http.NoBody)
	})
	return name
}

// issue1648Call invokes the fixture through the connection and returns the
// whole tool result, so a criterion can read the upstream status and body as
// well as the claims.
func issue1648Call(t *testing.T, c *client, connection string, extra map[string]any) map[string]any {
	t.Helper()
	args := map[string]any{
		"connection": connection,
		"method":     http.MethodGet,
		"path":       "/echo",
		"purpose":    issue1648Purpose,
	}
	for k, v := range extra {
		args[k] = v
	}
	return c.call("api_invoke_endpoint", args)
}

// issue1648Claims invokes the fixture and returns the claims it read out of
// the presented assertion, failing when the upstream refused it.
func issue1648Claims(t *testing.T, c *client, connection string, extra map[string]any) map[string]any {
	t.Helper()
	out := issue1648Call(t, c, connection, extra)
	if got := number(t, out, "status"); got != http.StatusOK {
		t.Fatalf("the fixture refused the assertion: HTTP %v, body %v", got, out["body"])
	}
	body, ok := out["body"].(map[string]any)
	if !ok {
		t.Fatalf("the fixture's answer is not an object: %v", out["body"])
	}
	return body
}

// issue1648RSAKey and issue1648ECKey generate one key each per test binary,
// in the PEM forms the vendors in this class hand out.
var (
	issue1648RSAKey = sync.OnceValues(func() (*rsa.PrivateKey, string) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		return key, string(pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		}))
	})
	issue1648ECKey = sync.OnceValues(func() (*ecdsa.PrivateKey, string) {
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

// TestIssue1648_SignedJWTPresentsTheConfiguredAssertion is the first
// criterion: a signed_jwt connection reaches the upstream, and the token it
// presented decodes to the configured claims with the configured timings.
func TestIssue1648_SignedJWTPresentsTheConfiguredAssertion(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)
	base := f.register("hs256", issue1648Expect{
		key: []byte(issue1648Secret), iss: issue1648Issuer, sub: issue1648Subject,
		aud: "acc-1648-audience", requires: "HS256",
	})
	conn := issue1648Connect(t, c, "hs256", base, map[string]any{
		"jwt_algorithm":      "HS256",
		"jwt_client_secret":  issue1648Secret,
		"jwt_issuer":         issue1648Issuer,
		"jwt_subject":        issue1648Subject,
		"jwt_audience":       "acc-1648-audience",
		"jwt_token_lifetime": "300s",
		"jwt_issued_at_skew": "30s",
	})

	before := time.Now()
	seen := issue1648Claims(t, c, conn, nil)

	if seen["iss"] != issue1648Issuer {
		t.Errorf("iss = %v, want %q", seen["iss"], issue1648Issuer)
	}
	if seen["sub"] != issue1648Subject {
		t.Errorf("sub = %v, want %q", seen["sub"], issue1648Subject)
	}
	if seen["aud"] != "acc-1648-audience" {
		t.Errorf("aud = %v, want the configured audience", seen["aud"])
	}
	iat, exp := seen["iat"].(float64), seen["exp"].(float64)
	if got := exp - iat; got != 300 {
		t.Errorf("exp - iat = %v, want the configured 300s lifetime", got)
	}
	if want := before.Add(-30 * time.Second).Unix(); int64(iat) > want {
		t.Errorf("iat = %v, want at or before %v (30s of configured skew)", int64(iat), want)
	}
	if seen["alg"] != "HS256" {
		t.Errorf("alg = %v, want HS256", seen["alg"])
	}
	if seen["kid"] != nil {
		t.Errorf("kid = %v, want no kid header with jwt_key_id unset", seen["kid"])
	}
}

// TestIssue1648_SignedJWTAudienceDefaultsToTheEndpoint proves the default the
// mode applies when the operator states no audience: the connection's own
// endpoint URL, which is what these upstreams register.
func TestIssue1648_SignedJWTAudienceDefaultsToTheEndpoint(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)
	base := f.register("default-aud", issue1648Expect{
		key: []byte(issue1648Secret), iss: issue1648Issuer, aud: "", requires: "HS256",
	})
	conn := issue1648Connect(t, c, "default-aud", base, map[string]any{
		"jwt_client_secret": issue1648Secret,
		"jwt_issuer":        issue1648Issuer,
	})
	seen := issue1648Claims(t, c, conn, nil)
	if seen["aud"] != base {
		t.Errorf("aud = %v, want the connection's base_url %q", seen["aud"], base)
	}
}

// TestIssue1648_SignedJWTSignsWithEachAlgorithm is the second and third
// criteria: HS256 over a shared secret, RS256 and ES256 over a PEM signing
// key, and a configured key id arriving as the token's kid header.
func TestIssue1648_SignedJWTSignsWithEachAlgorithm(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)
	rsaKey, rsaPEM := issue1648RSAKey()
	ecKey, ecPEM := issue1648ECKey()

	tests := []struct {
		alg   string
		key   any
		keyID string
		cfg   map[string]any
	}{
		{"HS256", []byte(issue1648Secret), "", map[string]any{
			"jwt_algorithm": "HS256", "jwt_client_secret": issue1648Secret,
		}},
		{"RS256", &rsaKey.PublicKey, "ACC1648RS", map[string]any{
			"jwt_algorithm": "RS256", "jwt_private_key_pem": rsaPEM, "jwt_key_id": "ACC1648RS",
		}},
		{"ES256", &ecKey.PublicKey, "ACC1648ES", map[string]any{
			"jwt_algorithm": "ES256", "jwt_private_key_pem": ecPEM, "jwt_key_id": "ACC1648ES",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.alg, func(t *testing.T) {
			label := "alg-" + strings.ToLower(tc.alg)
			base := f.register(label, issue1648Expect{
				key: tc.key, iss: issue1648Issuer, requires: tc.alg,
			})
			cfg := map[string]any{"jwt_issuer": issue1648Issuer}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			conn := issue1648Connect(t, c, label, base, cfg)
			seen := issue1648Claims(t, c, conn, nil)
			if seen["alg"] != tc.alg {
				t.Errorf("alg = %v, want %q", seen["alg"], tc.alg)
			}
			if tc.keyID == "" {
				if seen["kid"] != nil {
					t.Errorf("kid = %v, want none with jwt_key_id unset", seen["kid"])
				}
				return
			}
			if seen["kid"] != tc.keyID {
				t.Errorf("kid = %v, want the configured %q", seen["kid"], tc.keyID)
			}
		})
	}
}

// TestIssue1648_UpstreamRefusalsReachTheModelUnchanged is the pass-through
// criterion: whatever the upstream said about the token comes back verbatim,
// because that body is the only diagnosis of a claim mismatch the operator
// gets.
func TestIssue1648_UpstreamRefusalsReachTheModelUnchanged(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)

	tests := []struct {
		name     string
		expect   issue1648Expect
		cfg      map[string]any
		wantBody string
	}{
		{"wrong key material", issue1648Expect{
			key: []byte("a-different-secret-entirely"), iss: issue1648Issuer, requires: "HS256",
		}, map[string]any{
			"jwt_client_secret": issue1648Secret, "jwt_issuer": issue1648Issuer,
		}, "bad-signature: the assertion did not verify against the registered key"},
		{"wrong issuer", issue1648Expect{
			key: []byte(issue1648Secret), iss: "CLIENTID-registered", requires: "HS256",
		}, map[string]any{
			"jwt_client_secret": issue1648Secret, "jwt_issuer": "CLIENTID-mistyped",
		}, `wrong-iss: the assertion claimed "CLIENTID-mistyped" where this application registered "CLIENTID-registered"`},
		{"wrong subject", issue1648Expect{
			key: []byte(issue1648Secret), sub: "svc-registered", requires: "HS256",
		}, map[string]any{
			"jwt_client_secret": issue1648Secret, "jwt_subject": "svc-mistyped",
		}, `wrong-sub: the assertion claimed "svc-mistyped" where this application registered "svc-registered"`},
		{"wrong audience", issue1648Expect{
			key: []byte(issue1648Secret), iss: issue1648Issuer, aud: "the-registered-audience", requires: "HS256",
		}, map[string]any{
			"jwt_client_secret": issue1648Secret, "jwt_issuer": issue1648Issuer,
			"jwt_audience": "the-mistyped-audience",
		}, `wrong-aud: the assertion claimed "the-mistyped-audience" where this application registered "the-registered-audience"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			label := strings.ReplaceAll(tc.name, " ", "-")
			base := f.register(label, tc.expect)
			conn := issue1648Connect(t, c, label, base, tc.cfg)
			out := issue1648Call(t, c, conn, nil)
			if got := number(t, out, "status"); got != http.StatusUnauthorized {
				t.Fatalf("status = %v, want the upstream's 401", got)
			}
			body, _ := out["body"].(string)
			if body != tc.wantBody {
				t.Errorf("body = %q, want the upstream's own text %q", body, tc.wantBody)
			}
		})
	}
}

// TestIssue1648_SignedJWTReusesTheTokenUntilItNearsExpiry is the caching
// criterion, observed where it matters: in the tokens the upstream actually
// received.
func TestIssue1648_SignedJWTReusesTheTokenUntilItNearsExpiry(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)
	base := f.register("cache", issue1648Expect{
		key: []byte(issue1648Secret), iss: issue1648Issuer, requires: "HS256",
	})
	// A four-second lifetime with one second of skew is reused for the
	// first two seconds and abandoned after them, so both halves of the
	// rule are observable inside one test.
	conn := issue1648Connect(t, c, "cache", base, map[string]any{
		"jwt_client_secret":  issue1648Secret,
		"jwt_issuer":         issue1648Issuer,
		"jwt_token_lifetime": "4s",
		"jwt_issued_at_skew": "1s",
	})

	issue1648Claims(t, c, conn, nil)
	issue1648Claims(t, c, conn, nil)
	seen := f.tokens("cache")
	if len(seen) != 2 {
		t.Fatalf("the fixture saw %d assertions, want 2", len(seen))
	}
	if seen[0] != seen[1] {
		t.Error("a second call inside the lifetime minted a new assertion instead of reusing the cached one")
	}

	time.Sleep(4 * time.Second)
	issue1648Claims(t, c, conn, nil)
	seen = f.tokens("cache")
	if len(seen) != 3 {
		t.Fatalf("the fixture saw %d assertions, want 3", len(seen))
	}
	if seen[2] == seen[0] {
		t.Error("a call after the lifetime presented the expired assertion instead of minting a new one")
	}
}

// TestIssue1648_SignedJWTAcrossWireForms sends the one untyped parameter on
// the path in both forms its schema admits, as literal tools/call params, and
// asserts the same verified assertion reaches the upstream for each.
func TestIssue1648_SignedJWTAcrossWireForms(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)
	base := f.register("wire-forms", issue1648Expect{
		key: []byte(issue1648Secret), iss: issue1648Issuer, requires: "HS256",
	})
	conn := issue1648Connect(t, c, "wire-forms", base, map[string]any{
		"jwt_client_secret": issue1648Secret,
		"jwt_issuer":        issue1648Issuer,
	})

	forms := []struct {
		name string
		body any
	}{
		{"body as an object", map[string]any{"note": "acc-1648"}},
		{"body as a string of JSON", `{"note":"acc-1648"}`},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			seen := issue1648Claims(t, c, conn, map[string]any{
				"method": http.MethodPost, "body": form.body,
			})
			if seen["iss"] != issue1648Issuer {
				t.Errorf("iss = %v, want %q", seen["iss"], issue1648Issuer)
			}
			if got, _ := seen["request_body"].(string); !strings.Contains(got, "acc-1648") {
				t.Errorf("the upstream received body %q, want the note this form sent", got)
			}
		})
	}
}

// TestIssue1648_SignedJWTIsAvailableToEveryHTTPKind proves the mode lives on
// the shared seam rather than in one toolkit: a graphql connection configured
// the same way presents the same verified assertion. The tool's own verdict is
// not read — the fixture is not a GraphQL server — because the criterion is
// about which credential the second kind put on the wire.
func TestIssue1648_SignedJWTIsAvailableToEveryHTTPKind(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)
	base := f.register("graphql-kind", issue1648Expect{
		key: []byte(issue1648Secret), iss: issue1648Issuer, requires: "HS256",
	})
	issue1648ConnectKind(t, c, "graphql", "graphql-kind",
		map[string]any{"endpoint_url": base + "/graphql"},
		map[string]any{
			"jwt_client_secret": issue1648Secret,
			"jwt_issuer":        issue1648Issuer,
			"jwt_audience":      "acc-1648-graphql",
		})

	// The kind reads its schema when the connection attaches, and that
	// read is the outbound call this criterion is about.
	deadline := time.Now().Add(20 * time.Second)
	for len(f.tokens("graphql-kind")) == 0 && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	seen := f.tokens("graphql-kind")
	if len(seen) == 0 {
		t.Fatal("the graphql connection made no authenticated call to the fixture")
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(seen[0], claims,
		func(*jwt.Token) (any, error) { return []byte(issue1648Secret), nil },
		jwt.WithValidMethods([]string{"HS256"})); err != nil {
		t.Fatalf("the graphql kind presented an assertion that does not verify: %v", err)
	}
	if claims["iss"] != issue1648Issuer {
		t.Errorf("iss = %v, want %q", claims["iss"], issue1648Issuer)
	}
	if claims["aud"] != "acc-1648-graphql" {
		t.Errorf("aud = %v, want the configured audience", claims["aud"])
	}
}

// TestIssue1648_ValidationRefusesAnUnusableConfig is the validation criterion:
// each refused shape comes back from the connection save with a message naming
// the key the operator has to fix.
func TestIssue1648_ValidationRefusesAnUnusableConfig(t *testing.T) {
	c := connect(t)
	_, rsaPEM := issue1648RSAKey()

	tests := []struct {
		name    string
		cfg     map[string]any
		wantKey string
	}{
		{"no key material", map[string]any{"jwt_issuer": issue1648Issuer}, "jwt_client_secret"},
		{"HS256 carrying a private key", map[string]any{
			"jwt_issuer": issue1648Issuer, "jwt_client_secret": issue1648Secret,
			"jwt_private_key_pem": rsaPEM,
		}, "jwt_private_key_pem"},
		{"RS256 with no private key", map[string]any{
			"jwt_issuer": issue1648Issuer, "jwt_algorithm": "RS256",
		}, "jwt_private_key_pem"},
		{"unparseable private key", map[string]any{
			"jwt_issuer": issue1648Issuer, "jwt_algorithm": "RS256",
			"jwt_private_key_pem": "not a pem block at all",
		}, "jwt_private_key_pem"},
		{"unknown algorithm", map[string]any{
			"jwt_issuer": issue1648Issuer, "jwt_client_secret": issue1648Secret,
			"jwt_algorithm": "HS512",
		}, "jwt_algorithm"},
		{"no issuer and no subject", map[string]any{
			"jwt_client_secret": issue1648Secret,
		}, "jwt_issuer"},
		{"zero lifetime", map[string]any{
			"jwt_issuer": issue1648Issuer, "jwt_client_secret": issue1648Secret,
			"jwt_token_lifetime": "0s",
		}, "jwt_token_lifetime"},
		{"skew past the lifetime", map[string]any{
			"jwt_issuer": issue1648Issuer, "jwt_client_secret": issue1648Secret,
			"jwt_token_lifetime": "60s", "jwt_issued_at_skew": "120s",
		}, "jwt_issued_at_skew"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("acc-1648-invalid-%d", time.Now().UnixNano())
			cfg := map[string]any{
				"base_url":        "https://upstream.invalid/api",
				"connection_name": name,
				"auth_mode":       "signed_jwt",
			}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			status, body := c.rest(http.MethodPut,
				"/api/v1/admin/connection-instances/api/"+name,
				jsonBody(t, map[string]any{"config": cfg}))
			if status == http.StatusCreated || status == http.StatusOK {
				c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
				t.Fatalf("the connection save accepted %s", tc.name)
			}
			text := fmt.Sprint(body)
			if !strings.Contains(text, tc.wantKey) {
				t.Errorf("the refusal %q does not name the offending key %q", text, tc.wantKey)
			}
			if strings.Contains(text, issue1648Secret) || strings.Contains(text, "PRIVATE KEY") {
				t.Errorf("the refusal %q carries key material", text)
			}
		})
	}
}

// TestIssue1648_SecretsNeverLeaveThePlatform is the last criterion: neither
// the shared secret nor the signing key is readable back through the admin
// API that wrote them.
func TestIssue1648_SecretsNeverLeaveThePlatform(t *testing.T) {
	c := connect(t)
	f := issue1648StartFixture(t)
	_, ecPEM := issue1648ECKey()
	base := f.register("redaction", issue1648Expect{
		key: []byte(issue1648Secret), iss: issue1648Issuer, requires: "HS256",
	})
	conn := issue1648Connect(t, c, "redaction", base, map[string]any{
		"jwt_algorithm": "ES256", "jwt_private_key_pem": ecPEM,
		"jwt_issuer": issue1648Issuer,
	})

	status, body := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+conn, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("read the connection back: HTTP %d", status)
	}
	text := fmt.Sprint(body)
	if strings.Contains(text, "PRIVATE KEY") || strings.Contains(text, issue1648Secret) {
		t.Fatalf("the connection read carries key material: %s", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Errorf("the connection read does not redact the signing key: %s", text)
	}
}

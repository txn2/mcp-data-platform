package upstreamauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// tokenEndpoint is an RFC 7523 token endpoint. It records every request it
// is sent and answers with the response the test set.
type tokenEndpoint struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []tokenRequest
	status   int
	body     string
}

// tokenRequest is what one exchange put on the wire.
type tokenRequest struct {
	form          url.Values
	authorization string
}

func newTokenEndpoint(t *testing.T) *tokenEndpoint {
	t.Helper()
	e := &tokenEndpoint{status: http.StatusOK, body: `{"access_token":"at-1","token_type":"Bearer","expires_in":3600}`}
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token request: %v", err)
		}
		e.mu.Lock()
		e.requests = append(e.requests, tokenRequest{form: r.PostForm, authorization: r.Header.Get("Authorization")})
		status, body := e.status, e.body
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(e.server.Close)
	return e
}

func (e *tokenEndpoint) url() string { return e.server.URL + "/token" }

func (e *tokenEndpoint) answer(status int, body string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status, e.body = status, body
}

func (e *tokenEndpoint) seen() []tokenRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]tokenRequest(nil), e.requests...)
}

// jwtBearerConfig is a valid RS256 jwt_bearer connection against tokenURL,
// with the given overrides applied.
func jwtBearerConfig(tokenURL string, mutate func(*Config)) Config {
	c := Config{
		Kind:           "api",
		ErrPrefix:      "apigateway",
		ConnectionName: "erp",
		AuthMode:       AuthModeOAuth,
		OAuth2: OAuth2Config{
			Grant:             connoauth.GrantJWTBearer,
			TokenURL:          tokenURL,
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
		SignedJWT: SignedJWTConfig{
			Algorithm:     SignedJWTAlgRS256,
			PrivateKeyPEM: testRSAKeyPEM(),
			Issuer:        testJWTIssuer,
			Subject:       testJWTSubject,
			Audience:      tokenURL,
			TokenLifetime: 3 * time.Minute,
			IssuedAtSkew:  DefaultSignedJWTIssuedAtSkew,
		},
	}
	if mutate != nil {
		mutate(&c)
	}
	return c
}

// sinkRecorder is the RevocationSink the authenticator's writer announces
// to in these tests.
type sinkRecorder struct {
	mu       sync.Mutex
	revoked  []authevents.Revocation
	restored []string
}

func (s *sinkRecorder) Revoked(_ context.Context, rev authevents.Revocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = append(s.revoked, rev)
}

func (s *sinkRecorder) Restored(_ context.Context, kind, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restored = append(s.restored, kind+"/"+name)
}

func (s *sinkRecorder) snapshot() (revoked []authevents.Revocation, restored []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]authevents.Revocation(nil), s.revoked...), append([]string(nil), s.restored...)
}

// newWiredJWTBearer builds the authenticator through NewAuthenticator and
// wires a writer announcing to the returned sink, the way a kind does.
func newWiredJWTBearer(t *testing.T, c Config) (Authenticator, *sinkRecorder) {
	t.Helper()
	a, err := NewAuthenticator(c)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	sink := &sinkRecorder{}
	if !SetAuthEvents(a, authevents.NewWriter(authevents.NewMemoryStore(), nil).WithRevocations(sink)) {
		t.Fatal("the jwt_bearer authenticator did not accept an auth-event writer")
	}
	return a, sink
}

func applyTo(t *testing.T, a Authenticator) (*http.Request, error) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://erp.example.com/v1/thing", http.NoBody)
	return req, a.Apply(req) //nolint:wrapcheck // the test reads the authenticator's own error
}

func TestParseJWTBearerAppliesTheGrantDefaults(t *testing.T) {
	const tokenURL = "https://login.example.com/services/oauth2/token"
	c, err := Parse("api", "apigateway", "https://erp.example.com", map[string]any{
		"auth_mode":           AuthModeOAuth,
		"oauth_grant":         connoauth.GrantJWTBearer,
		"oauth_token_url":     tokenURL,
		"jwt_private_key_pem": testRSAKeyPEM(),
		"jwt_issuer":          testJWTIssuer,
		"jwt_subject":         testJWTSubject,
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.OAuth2.Grant != connoauth.GrantJWTBearer {
		t.Errorf("grant = %q, want jwt_bearer", c.OAuth2.Grant)
	}
	if c.SignedJWT.Algorithm != SignedJWTAlgRS256 {
		t.Errorf("algorithm = %q, want the RS256 default this grant applies", c.SignedJWT.Algorithm)
	}
	if c.SignedJWT.Audience != tokenURL {
		t.Errorf("audience = %q, want the token endpoint %q, not the connection's base URL", c.SignedJWT.Audience, tokenURL)
	}
	if c.SignedJWT.TokenLifetime != DefaultSignedJWTTokenLifetime || c.SignedJWT.IssuedAtSkew != DefaultSignedJWTIssuedAtSkew {
		t.Errorf("timings = %s/%s, want the shared defaults", c.SignedJWT.TokenLifetime, c.SignedJWT.IssuedAtSkew)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("a complete jwt_bearer connection was refused: %v", err)
	}

	c, err = Parse("api", "apigateway", "https://erp.example.com", map[string]any{
		"auth_mode":       AuthModeOAuth,
		"oauth_grant":     connoauth.GrantJWTBearer,
		"oauth_token_url": tokenURL,
		"jwt_audience":    "https://login.example.com",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.SignedJWT.Audience != "https://login.example.com" {
		t.Errorf("audience = %q, want the configured value", c.SignedJWT.Audience)
	}

	c, err = Parse("api", "apigateway", "https://erp.example.com", map[string]any{
		"auth_mode":       AuthModeOAuth,
		"oauth_grant":     connoauth.GrantClientCredentials,
		"oauth_token_url": tokenURL,
		"jwt_issuer":      testJWTIssuer,
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.SignedJWT != (SignedJWTConfig{}) {
		t.Errorf("a client_credentials connection read assertion keys: %+v", c.SignedJWT)
	}
}

func TestValidateJWTBearerRefusals(t *testing.T) {
	const scope = `when auth_mode is "oauth" and oauth_grant is "jwt_bearer"`
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			"no token endpoint", func(c *Config) { c.OAuth2.TokenURL = "" },
			"apigateway: oauth_token_url is required " + scope,
		},
		{
			"a secret with no client id", func(c *Config) { c.OAuth2.ClientSecret = "s3cret" },
			"apigateway: oauth_client_id is required when oauth_client_secret is set and auth_mode is \"oauth\" and oauth_grant is \"jwt_bearer\"",
		},
		{
			"an unknown endpoint auth style", func(c *Config) { c.OAuth2.EndpointAuthStyle = "cookie" },
			`apigateway: invalid oauth_endpoint_auth_style "cookie"`,
		},
		{
			"no private key", func(c *Config) { c.SignedJWT.PrivateKeyPEM = "" },
			`apigateway: jwt_private_key_pem is required when jwt_algorithm is "RS256"`,
		},
		{
			"a shared secret where the algorithm is asymmetric", func(c *Config) { c.SignedJWT.ClientSecret = testJWTSecret },
			`apigateway: jwt_client_secret is not used when jwt_algorithm is "RS256"`,
		},
		{
			"an ES256 algorithm over an RSA key", func(c *Config) { c.SignedJWT.Algorithm = SignedJWTAlgES256 },
			`apigateway: jwt_private_key_pem is not a usable ES256 private key`,
		},
		{
			"no issuer", func(c *Config) { c.SignedJWT.Issuer = "" },
			"apigateway: jwt_issuer is required " + scope,
		},
		{
			"no subject", func(c *Config) { c.SignedJWT.Subject = "" },
			"apigateway: jwt_subject is required " + scope,
		},
		{
			"a skew past the lifetime", func(c *Config) { c.SignedJWT.IssuedAtSkew = 5 * time.Minute },
			"apigateway: jwt_issued_at_skew (5m0s) must be under jwt_token_lifetime (3m0s)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := jwtBearerConfig("https://login.example.com/token", tc.mutate)
			err := c.ValidateAuth()
			if err == nil {
				t.Fatal("ValidateAuth accepted the config")
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to start with %q", err, tc.want)
			}
			if _, err := NewAuthenticator(c); err == nil {
				t.Error("NewAuthenticator built an authenticator ValidateAuth refused")
			}
		})
	}

	t.Run("HS256 over a shared secret is accepted", func(t *testing.T) {
		c := jwtBearerConfig("https://login.example.com/token", func(c *Config) {
			c.SignedJWT.Algorithm = SignedJWTAlgHS256
			c.SignedJWT.PrivateKeyPEM = ""
			c.SignedJWT.ClientSecret = testJWTSecret
		})
		if err := c.ValidateAuth(); err != nil {
			t.Errorf("ValidateAuth: %v", err)
		}
	})
}

// TestJWTBearerExchangesTheAssertion is the grant's central criterion: the
// token endpoint is sent grant_type=jwt-bearer with an assertion carrying the
// configured claims, and the access token it issues is what reaches the
// upstream request.
func TestJWTBearerExchangesTheAssertion(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	c := jwtBearerConfig(endpoint.url(), func(c *Config) {
		c.OAuth2.Scopes = []string{"api", "refresh"}
		c.SignedJWT.KeyID = "key-2026"
	})
	a, _ := newWiredJWTBearer(t, c)

	before := time.Now()
	req, err := applyTo(t, a)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get(AuthorizationHeader); got != "Bearer at-1" {
		t.Errorf("Authorization = %q, want the access token the endpoint issued", got)
	}

	requests := endpoint.seen()
	if len(requests) != 1 {
		t.Fatalf("token requests = %d, want 1", len(requests))
	}
	sent := requests[0]
	if got := sent.form.Get("grant_type"); got != JWTBearerGrantType {
		t.Errorf("grant_type = %q, want %q", got, JWTBearerGrantType)
	}
	if got := sent.form.Get("scope"); got != "api refresh" {
		t.Errorf("scope = %q, want the configured scopes", got)
	}
	if sent.authorization != "" || sent.form.Has("client_id") || sent.form.Has("client_secret") {
		t.Errorf("a connection with no client credential sent one: Authorization=%q form=%v", sent.authorization, sent.form)
	}

	rsaKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(testRSAKeyPEM()))
	if err != nil {
		t.Fatalf("parse RSA key: %v", err)
	}
	claims, header := decodeWith(t, sent.form.Get("assertion"), &rsaKey.PublicKey)
	if header["alg"] != SignedJWTAlgRS256 || header["kid"] != "key-2026" {
		t.Errorf("header = %v, want RS256 with kid key-2026", header)
	}
	if claims["iss"] != testJWTIssuer || claims["sub"] != testJWTSubject || claims["aud"] != endpoint.url() {
		t.Errorf("claims iss/sub/aud = %v/%v/%v, want %q/%q/%q",
			claims["iss"], claims["sub"], claims["aud"], testJWTIssuer, testJWTSubject, endpoint.url())
	}
	iat, _ := claims["iat"].(float64)
	exp, _ := claims["exp"].(float64)
	if exp-iat != (3 * time.Minute).Seconds() {
		t.Errorf("exp - iat = %v, want the configured 180s lifetime", exp-iat)
	}
	if int64(iat) > before.Add(-DefaultSignedJWTIssuedAtSkew).Unix() {
		t.Errorf("iat = %v, want it backdated by the configured skew", int64(iat))
	}
	if jti, _ := claims["jti"].(string); len(jti) != 32 {
		t.Errorf("jti = %v, want 128 random bits hex-encoded", claims["jti"])
	}
}

// TestJWTBearerReusesTheAccessToken proves the access token, not the
// assertion, is the cached thing: calls inside its lifetime do not
// exchange again, and a token near its expiry is exchanged for with a new
// assertion.
func TestJWTBearerReusesTheAccessToken(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	a, _ := newWiredJWTBearer(t, jwtBearerConfig(endpoint.url(), nil))
	for range 3 {
		if _, err := applyTo(t, a); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	if got := len(endpoint.seen()); got != 1 {
		t.Errorf("token requests = %d over three calls inside the token's lifetime, want 1", got)
	}

	// A token already inside the library's expiry margin is exchanged for
	// again on every call, each time with a fresh assertion.
	near := newTokenEndpoint(t)
	near.answer(http.StatusOK, `{"access_token":"at-short","token_type":"Bearer","expires_in":1}`)
	a, _ = newWiredJWTBearer(t, jwtBearerConfig(near.url(), nil))
	for range 2 {
		if _, err := applyTo(t, a); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	requests := near.seen()
	if len(requests) != 2 {
		t.Fatalf("token requests = %d for a token at its expiry, want 2", len(requests))
	}
	if requests[0].form.Get("assertion") == requests[1].form.Get("assertion") {
		t.Error("the second exchange presented the first assertion again")
	}
}

// TestJWTBearerBoundsATokenWithNoExpiry covers an upstream whose response has
// no expires_in: the exchange composed as newJWTBearerAuth composes it gives
// the token DefaultUpstreamAccessTokenLifetime rather than the unbounded
// lifetime the library would infer, and any refresh token in the response is
// not held.
func TestJWTBearerBoundsATokenWithNoExpiry(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	endpoint.answer(http.StatusOK, `{"access_token":"at-forever","token_type":"Bearer","refresh_token":"rt-unused"}`)
	c := jwtBearerConfig(endpoint.url(), nil)
	signer, err := c.signedJWTSigningKey()
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	var accepted int
	exchange := &jwtBearerExchange{
		cfg:      c,
		minter:   assertionMinter{cfg: c, signer: signer, mode: jwtBearerMode, withJTI: true},
		ctx:      context.Background(),
		now:      func() time.Time { return now },
		accepted: func(context.Context) { accepted++ },
	}
	tok, err := withBoundedExpiry(exchange, exchange.now).Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if want := now.Add(DefaultUpstreamAccessTokenLifetime); !tok.Expiry.Equal(want) {
		t.Errorf("expiry = %s, want %s", tok.Expiry, want)
	}
	if tok.RefreshToken != "" {
		t.Errorf("refresh token = %q, want it dropped", tok.RefreshToken)
	}
	if accepted != 1 {
		t.Errorf("accepted called %d times, want once per successful exchange", accepted)
	}
}

// TestJWTBearerClientAuthentication covers the upstream that also wants the
// client authenticated on the token request: the credential goes where
// oauth_endpoint_auth_style puts it.
func TestJWTBearerClientAuthentication(t *testing.T) {
	tests := []struct {
		name      string
		style     string
		secret    string
		wantBasic bool
		wantForm  map[string]string
	}{
		{"a secret in the header", OAuth2AuthStyleHeader, "s3cret", true, nil},
		{
			"a secret in the form", OAuth2AuthStyleParams, "s3cret", false,
			map[string]string{"client_id": "platform-client", "client_secret": "s3cret"},
		},
		{
			"a client id with no secret", OAuth2AuthStyleHeader, "", false,
			map[string]string{"client_id": "platform-client"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := newTokenEndpoint(t)
			a, _ := newWiredJWTBearer(t, jwtBearerConfig(endpoint.url(), func(c *Config) {
				c.OAuth2.ClientID = "platform-client"
				c.OAuth2.ClientSecret = tc.secret
				c.OAuth2.EndpointAuthStyle = tc.style
			}))
			if _, err := applyTo(t, a); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			sent := endpoint.seen()[0]
			if got := strings.HasPrefix(sent.authorization, "Basic "); got != tc.wantBasic {
				t.Errorf("Basic client authentication = %v (%q), want %v", got, sent.authorization, tc.wantBasic)
			}
			for k, want := range tc.wantForm {
				if got := sent.form.Get(k); got != want {
					t.Errorf("form %s = %q, want %q", k, got, want)
				}
			}
			if tc.secret == "" && sent.form.Has("client_secret") {
				t.Error("an empty client secret was sent")
			}
		})
	}
}

// TestJWTBearerRefusalIsAnnouncedAndCleared is the alert criterion at this
// layer: an upstream refusal reaches the model with its code and
// description and is announced to the sink, and the next accepted exchange
// announces the restoration.
func TestJWTBearerRefusalIsAnnouncedAndCleared(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	endpoint.answer(http.StatusBadRequest,
		`{"error":"invalid_grant","error_description":"user hasn't approved\r\nthis consumer"}`)
	a, sink := newWiredJWTBearer(t, jwtBearerConfig(endpoint.url(), nil))

	req, err := applyTo(t, a)
	if !errors.Is(err, ErrAssertionRejected) {
		t.Fatalf("Apply error = %v, want ErrAssertionRejected", err)
	}
	const want = "apigateway: oauth jwt_bearer: the upstream token endpoint rejected the signed assertion: " +
		"invalid_grant: user hasn't approved this consumer"
	if err.Error() != want {
		t.Errorf("error = %q\nwant    %q", err, want)
	}
	if got := req.Header.Get(AuthorizationHeader); got != "" {
		t.Errorf("a refused exchange still set Authorization to %q", got)
	}

	revoked, restored := sink.snapshot()
	if len(revoked) != 1 {
		t.Fatalf("announced %d refusals, want 1", len(revoked))
	}
	rev := revoked[0]
	u, _ := url.Parse(endpoint.url())
	if rev.Kind != "api" || rev.Name != "erp" || rev.IDPHost != u.Host || rev.Reason != "invalid_grant" ||
		rev.Description != "user hasn't approved this consumer" || !rev.SignedAssertion || rev.AuthorizedBy != "" {
		t.Errorf("announced %+v", rev)
	}
	if len(restored) != 0 {
		t.Errorf("a refusal announced a restoration: %v", restored)
	}

	endpoint.answer(http.StatusOK, `{"access_token":"at-2","token_type":"Bearer","expires_in":3600}`)
	req, err = applyTo(t, a)
	if err != nil {
		t.Fatalf("Apply after the upstream accepted: %v", err)
	}
	if got := req.Header.Get(AuthorizationHeader); got != "Bearer at-2" {
		t.Errorf("Authorization = %q, want the new access token", got)
	}
	if _, restored = sink.snapshot(); len(restored) != 1 || restored[0] != "api/erp" {
		t.Errorf("restorations = %v, want one for api/erp", restored)
	}
}

// TestJWTBearerFailuresThatAreNotRefusals proves what is not the operator's to
// fix at the upstream is neither announced nor echoed: a server error keeps
// the scrubbed status-only message, and a malformed-request code is not an
// alert.
func TestJWTBearerFailuresThatAreNotRefusals(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"a server error", http.StatusInternalServerError, `{"error":"server_error","error_description":"db down at 10.0.0.4"}`},
		{"a malformed request", http.StatusBadRequest, `{"error":"invalid_request","error_description":"missing assertion"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := newTokenEndpoint(t)
			endpoint.answer(tc.status, tc.body)
			a, sink := newWiredJWTBearer(t, jwtBearerConfig(endpoint.url(), nil))
			_, err := applyTo(t, a)
			if err == nil || errors.Is(err, ErrAssertionRejected) {
				t.Fatalf("Apply error = %v, want a non-refusal failure", err)
			}
			if strings.Contains(err.Error(), "10.0.0.4") || strings.Contains(err.Error(), "missing assertion") {
				t.Errorf("error %q repeats the upstream's body", err)
			}
			if revoked, _ := sink.snapshot(); len(revoked) != 0 {
				t.Errorf("announced %v for a failure that is not a refusal", revoked)
			}
		})
	}

	t.Run("an unreachable endpoint", func(t *testing.T) {
		endpoint := newTokenEndpoint(t)
		endpoint.server.Close()
		a, sink := newWiredJWTBearer(t, jwtBearerConfig(endpoint.url(), nil))
		if _, err := applyTo(t, a); err == nil {
			t.Fatal("Apply succeeded against a closed token endpoint")
		}
		if revoked, _ := sink.snapshot(); len(revoked) != 0 {
			t.Errorf("announced %v for a network failure", revoked)
		}
	})

	t.Run("a response with no access token", func(t *testing.T) {
		endpoint := newTokenEndpoint(t)
		endpoint.answer(http.StatusOK, `{"token_type":"Bearer","expires_in":3600}`)
		a, _ := newWiredJWTBearer(t, jwtBearerConfig(endpoint.url(), nil))
		if _, err := applyTo(t, a); err == nil {
			t.Fatal("Apply succeeded with no access token")
		}
	})
}

func TestIsAssertionRefusal(t *testing.T) {
	for code, want := range map[string]bool{
		"invalid_grant": true, "invalid_client": true, "unauthorized_client": true,
		"invalid_request": false, "invalid_scope": false, "server_error": false, "": false,
	} {
		if got := isAssertionRefusal(code); got != want {
			t.Errorf("isAssertionRefusal(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestBoundedDescription(t *testing.T) {
	if got := boundedDescription("  a\tb\r\n\x00c  "); got != "a b c" {
		t.Errorf("control characters: got %q", got)
	}
	long := strings.Repeat("é", maxUpstreamDescriptionRunes+20)
	got := boundedDescription(long)
	if want := strings.Repeat("é", maxUpstreamDescriptionRunes) + "..."; got != want {
		t.Errorf("a long description was cut to %d runes, want %d plus an ellipsis", len([]rune(got)), maxUpstreamDescriptionRunes)
	}
	if boundedDescription("") != "" {
		t.Error("an empty description is not empty")
	}
}

// TestJWTBearerRefusalWithoutADescription covers the upstream that sends only
// the error code.
func TestJWTBearerRefusalWithoutADescription(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	endpoint.answer(http.StatusUnauthorized, `{"error":"invalid_client"}`)
	a, sink := newWiredJWTBearer(t, jwtBearerConfig(endpoint.url(), nil))
	_, err := applyTo(t, a)
	const want = "apigateway: oauth jwt_bearer: the upstream token endpoint rejected the signed assertion: invalid_client"
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
	if revoked, _ := sink.snapshot(); len(revoked) != 1 || revoked[0].Description != "" {
		t.Errorf("announced %+v, want one refusal with no description", revoked)
	}
}

// TestJWTBearerWithNoWriterStillWorks proves announcing is an addition to the
// exchange, not a condition on it.
func TestJWTBearerWithNoWriterStillWorks(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	a, err := NewAuthenticator(jwtBearerConfig(endpoint.url(), nil))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if _, err := applyTo(t, a); err != nil {
		t.Errorf("Apply with no writer wired: %v", err)
	}
	endpoint.answer(http.StatusBadRequest, `{"error":"invalid_grant"}`)
	a, _ = NewAuthenticator(jwtBearerConfig(endpoint.url(), nil))
	if _, err := applyTo(t, a); !errors.Is(err, ErrAssertionRejected) {
		t.Errorf("Apply error = %v, want ErrAssertionRejected with no writer wired", err)
	}
}

// failingRand is a jti source that cannot be read.
type failingRand struct{}

func (failingRand) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

// TestJWTBearerMintFailureIsNotScrubbed proves a failure to build the
// assertion reaches the model as that step, in the kind's voice once, rather
// than as a token-endpoint failure the exchange never reached.
func TestJWTBearerMintFailureIsNotScrubbed(t *testing.T) {
	endpoint := newTokenEndpoint(t)
	c := jwtBearerConfig(endpoint.url(), nil)
	a, err := newJWTBearerAuth(c)
	if err != nil {
		t.Fatalf("newJWTBearerAuth: %v", err)
	}
	signer, err := c.signedJWTSigningKey()
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	a.src = &jwtBearerExchange{
		cfg:      c,
		minter:   assertionMinter{cfg: c, signer: signer, mode: jwtBearerMode, withJTI: true, rand: failingRand{}},
		ctx:      context.Background(),
		now:      time.Now,
		accepted: a.accepted,
	}
	_, err = applyTo(t, a)
	const want = "apigateway: oauth jwt_bearer: minting the assertion id failed: entropy unavailable"
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
	if len(endpoint.seen()) != 0 {
		t.Error("the token endpoint was called without an assertion")
	}
}

// TestJWTBearerIsDispatchedByGrant pins the authenticator NewAuthenticator
// builds for each grant, so the jwt_bearer grant never falls through to
// client_credentials.
func TestJWTBearerIsDispatchedByGrant(t *testing.T) {
	a, err := NewAuthenticator(jwtBearerConfig("https://login.example.com/token", nil))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if _, ok := a.(*jwtBearerAuth); !ok {
		t.Errorf("authenticator = %T, want *jwtBearerAuth", a)
	}
	if SetConnOAuthStore(a, nil) {
		t.Error("a jwt_bearer authenticator accepted a token store it has no use for")
	}
}

// TestJWTBearerExchangeRequestShape guards the fields of the request the
// exchange hands the oauth2 library, which the wire tests above observe only
// through what the library did with them.
func TestJWTBearerExchangeRequestShape(t *testing.T) {
	e := &jwtBearerExchange{cfg: jwtBearerConfig("https://login.example.com/token", nil)}
	got := e.request("signed.assertion.value")
	raw, err := json.Marshal(map[string]any{
		"grant_type": got.EndpointParams.Get("grant_type"),
		"assertion":  got.EndpointParams.Get("assertion"),
		"token_url":  got.TokenURL,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"assertion":"signed.assertion.value","grant_type":"urn:ietf:params:oauth:grant-type:jwt-bearer","token_url":"https://login.example.com/token"}`
	if string(raw) != want {
		t.Errorf("request = %s\nwant      %s", raw, want)
	}
}

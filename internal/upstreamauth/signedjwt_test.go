package upstreamauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testJWTSecret   = "a-shared-secret-issued-out-of-band"
	testJWTIssuer   = "CLIENTID-2f7c"
	testJWTSubject  = "svc-integration"
	testJWTAudience = "https://erp.example.com/api1/syracuse/collaboration/syracuse"
)

// testRSAKeyPEM and testECKeyPEM generate one key each per test binary.
// Key generation is the slowest thing in this file, and every case that
// needs a key needs the same one.
var (
	testRSAKeyPEM = sync.OnceValue(func() string {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		}))
	})
	testECKeyPEM = sync.OnceValue(func() string {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			panic(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	})
)

// signedJWTConfig is a valid HS256 connection with the given overrides
// applied to its signed_jwt block.
func signedJWTConfig(mutate func(*SignedJWTConfig)) Config {
	c := Config{
		Kind:      "api",
		ErrPrefix: "apigateway",
		AuthMode:  AuthModeSignedJWT,
		SignedJWT: SignedJWTConfig{
			Algorithm:     SignedJWTAlgHS256,
			ClientSecret:  testJWTSecret,
			Issuer:        testJWTIssuer,
			Subject:       testJWTSubject,
			Audience:      testJWTAudience,
			TokenLifetime: DefaultSignedJWTTokenLifetime,
			IssuedAtSkew:  DefaultSignedJWTIssuedAtSkew,
		},
	}
	if mutate != nil {
		mutate(&c.SignedJWT)
	}
	return c
}

// presentedToken runs Apply against a throwaway request and returns the
// bearer token the authenticator attached.
func presentedToken(t *testing.T, a Authenticator) string {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://erp.example.com/v1/thing", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := req.Header.Get(AuthorizationHeader)
	if !strings.HasPrefix(got, "Bearer ") {
		t.Fatalf("Authorization = %q, want a Bearer token", got)
	}
	return strings.TrimPrefix(got, "Bearer ")
}

// decodeWith verifies the presented token against key and returns its
// claims and header. A verification failure is a test failure: the
// upstream this mode serves would reject the same token.
func decodeWith(t *testing.T, token string, key any) (claims jwt.MapClaims, header map[string]any) {
	t.Helper()
	claims = jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return key, nil },
		jwt.WithoutClaimsValidation())
	if err != nil {
		t.Fatalf("verify presented token: %v", err)
	}
	return claims, parsed.Header
}

func TestParseSignedJWTAppliesDefaults(t *testing.T) {
	c, err := Parse("api", "apigateway", testJWTAudience, map[string]any{
		"auth_mode":         AuthModeSignedJWT,
		"jwt_client_secret": testJWTSecret,
		"jwt_issuer":        testJWTIssuer,
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.SignedJWT.Algorithm != SignedJWTAlgHS256 {
		t.Errorf("algorithm = %q, want the HS256 default", c.SignedJWT.Algorithm)
	}
	if c.SignedJWT.Audience != testJWTAudience {
		t.Errorf("audience = %q, want the connection's endpoint URL %q", c.SignedJWT.Audience, testJWTAudience)
	}
	if c.SignedJWT.TokenLifetime != DefaultSignedJWTTokenLifetime {
		t.Errorf("token_lifetime = %v, want %v", c.SignedJWT.TokenLifetime, DefaultSignedJWTTokenLifetime)
	}
	if c.SignedJWT.IssuedAtSkew != DefaultSignedJWTIssuedAtSkew {
		t.Errorf("issued_at_skew = %v, want %v", c.SignedJWT.IssuedAtSkew, DefaultSignedJWTIssuedAtSkew)
	}
	if c.SignedJWT.Subject != "" {
		t.Errorf("subject = %q, want empty when unset", c.SignedJWT.Subject)
	}
	if err := c.ValidateAuth(); err != nil {
		t.Errorf("ValidateAuth on a minimal HS256 connection: %v", err)
	}
}

func TestParseSignedJWTReadsEveryKey(t *testing.T) {
	c, err := Parse("graphql", "graphql", "https://ignored.example.com/graphql", map[string]any{
		"auth_mode":           AuthModeSignedJWT,
		"jwt_algorithm":       SignedJWTAlgES256,
		"jwt_private_key_pem": testECKeyPEM(),
		"jwt_key_id":          "ABC1234567",
		"jwt_issuer":          testJWTIssuer,
		"jwt_subject":         testJWTSubject,
		"jwt_audience":        testJWTAudience,
		"jwt_token_lifetime":  "10m",
		"jwt_issued_at_skew":  "5s",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	j := c.SignedJWT
	if j.Algorithm != SignedJWTAlgES256 || j.KeyID != "ABC1234567" || j.Issuer != testJWTIssuer ||
		j.Subject != testJWTSubject || j.Audience != testJWTAudience {
		t.Errorf("parsed claims block = %+v, want the configured values", j)
	}
	if j.PrivateKeyPEM != testECKeyPEM() {
		t.Error("jwt_private_key_pem was not read")
	}
	if j.TokenLifetime != 10*time.Minute || j.IssuedAtSkew != 5*time.Second {
		t.Errorf("lifetime/skew = %v/%v, want 10m/5s", j.TokenLifetime, j.IssuedAtSkew)
	}
	if err := c.ValidateAuth(); err != nil {
		t.Errorf("ValidateAuth: %v", err)
	}
}

// TestParseSignedJWTIgnoredForOtherModes proves the block is only read
// when the mode asks for it, so a connection that once used signed_jwt
// and moved to bearer is not validated against leftover keys.
func TestParseSignedJWTIgnoredForOtherModes(t *testing.T) {
	c, err := Parse("api", "apigateway", testJWTAudience, map[string]any{
		"auth_mode":         AuthModeBearer,
		"credential":        "static-token",
		"jwt_client_secret": testJWTSecret,
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.SignedJWT != (SignedJWTConfig{}) {
		t.Errorf("SignedJWT = %+v, want the zero value for a bearer connection", c.SignedJWT)
	}
}

func TestValidateSignedJWTRefusals(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SignedJWTConfig)
		wantKey string
	}{
		{"no key material at all", func(j *SignedJWTConfig) { j.ClientSecret = "" }, "jwt_client_secret"},
		{"HS256 carrying a private key", func(j *SignedJWTConfig) { j.PrivateKeyPEM = testRSAKeyPEM() }, "jwt_private_key_pem"},
		{"RS256 with no private key", func(j *SignedJWTConfig) {
			j.Algorithm, j.ClientSecret = SignedJWTAlgRS256, ""
		}, "jwt_private_key_pem"},
		{"RS256 carrying a shared secret", func(j *SignedJWTConfig) {
			j.Algorithm, j.PrivateKeyPEM = SignedJWTAlgRS256, testRSAKeyPEM()
		}, "jwt_client_secret"},
		{"unparseable RS256 key", func(j *SignedJWTConfig) {
			j.Algorithm, j.ClientSecret, j.PrivateKeyPEM = SignedJWTAlgRS256, "", "not a pem block"
		}, "jwt_private_key_pem"},
		{"ES256 handed an RSA key", func(j *SignedJWTConfig) {
			j.Algorithm, j.ClientSecret, j.PrivateKeyPEM = SignedJWTAlgES256, "", testRSAKeyPEM()
		}, "jwt_private_key_pem"},
		{"unknown algorithm", func(j *SignedJWTConfig) { j.Algorithm = "HS512" }, "jwt_algorithm"},
		{"neither issuer nor subject", func(j *SignedJWTConfig) { j.Issuer, j.Subject = "", "" }, "jwt_issuer"},
		{"no audience", func(j *SignedJWTConfig) { j.Audience = "" }, "jwt_audience"},
		{"zero lifetime", func(j *SignedJWTConfig) { j.TokenLifetime = 0 }, "jwt_token_lifetime"},
		{"negative lifetime", func(j *SignedJWTConfig) { j.TokenLifetime = -time.Second }, "jwt_token_lifetime"},
		{"negative skew", func(j *SignedJWTConfig) { j.IssuedAtSkew = -time.Second }, "jwt_issued_at_skew"},
		{"skew past the lifetime", func(j *SignedJWTConfig) {
			j.TokenLifetime, j.IssuedAtSkew = time.Minute, 2*time.Minute
		}, "jwt_issued_at_skew"},
		{"skew equal to the lifetime", func(j *SignedJWTConfig) {
			j.TokenLifetime, j.IssuedAtSkew = time.Minute, time.Minute
		}, "jwt_issued_at_skew"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := signedJWTConfig(tc.mutate)
			err := c.ValidateAuth()
			if err == nil {
				t.Fatalf("ValidateAuth accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("error %q does not name the offending key %q", err, tc.wantKey)
			}
			if !strings.HasPrefix(err.Error(), "apigateway: ") {
				t.Errorf("error %q does not speak in the calling kind's voice", err)
			}
			if strings.Contains(err.Error(), testJWTSecret) || strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Errorf("error %q carries key material", err)
			}
			if _, authErr := NewAuthenticator(c); authErr == nil {
				t.Errorf("NewAuthenticator built an authenticator for %s", tc.name)
			}
		})
	}
}

func TestSignedJWTMintsTheConfiguredClaims(t *testing.T) {
	c := signedJWTConfig(func(j *SignedJWTConfig) {
		j.TokenLifetime, j.IssuedAtSkew = 300*time.Second, 30*time.Second
	})
	a, err := NewAuthenticator(c)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	before := time.Now()
	claims, header := decodeWith(t, presentedToken(t, a), []byte(testJWTSecret))

	if claims["iss"] != testJWTIssuer {
		t.Errorf("iss = %v, want %q", claims["iss"], testJWTIssuer)
	}
	if claims["sub"] != testJWTSubject {
		t.Errorf("sub = %v, want %q", claims["sub"], testJWTSubject)
	}
	if claims["aud"] != testJWTAudience {
		t.Errorf("aud = %v, want %q", claims["aud"], testJWTAudience)
	}
	iat, iatOK := claims["iat"].(float64)
	exp, expOK := claims["exp"].(float64)
	if !iatOK || !expOK {
		t.Fatalf("iat/exp are not numbers in %v", claims)
	}
	if got := exp - iat; got != 300 {
		t.Errorf("exp - iat = %v, want the configured 300s lifetime", got)
	}
	// iat is set back by the skew, so it lands at or before 30s ago.
	if wantAtOrBefore := before.Add(-30 * time.Second).Unix(); int64(iat) > wantAtOrBefore {
		t.Errorf("iat = %v, want at or before %v (30s of skew)", int64(iat), wantAtOrBefore)
	}
	if header["alg"] != SignedJWTAlgHS256 {
		t.Errorf("alg header = %v, want %q", header["alg"], SignedJWTAlgHS256)
	}
	if _, ok := header["kid"]; ok {
		t.Errorf("kid header present with no jwt_key_id configured: %v", header["kid"])
	}
}

// TestSignedJWTOmitsAnUnsetIdentityClaim covers both halves of the
// identity pair, because each is a real upstream: an App Store Connect
// team key registers an issuer and no subject, an individual key
// registers a subject and no issuer, and a token carrying the claim the
// upstream did not register is refused.
func TestSignedJWTOmitsAnUnsetIdentityClaim(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SignedJWTConfig)
		omitted string
		kept    string
	}{
		{"issuer only", func(j *SignedJWTConfig) { j.Subject = "" }, "sub", "iss"},
		{"subject only", func(j *SignedJWTConfig) { j.Issuer = "" }, "iss", "sub"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAuthenticator(signedJWTConfig(tc.mutate))
			if err != nil {
				t.Fatalf("NewAuthenticator: %v", err)
			}
			claims, _ := decodeWith(t, presentedToken(t, a), []byte(testJWTSecret))
			if _, ok := claims[tc.omitted]; ok {
				t.Errorf("%s claim present with the field unset: %v", tc.omitted, claims[tc.omitted])
			}
			if _, ok := claims[tc.kept]; !ok {
				t.Errorf("%s claim missing with the field set", tc.kept)
			}
		})
	}
}

func TestSignedJWTEmitsKeyID(t *testing.T) {
	a, err := NewAuthenticator(signedJWTConfig(func(j *SignedJWTConfig) { j.KeyID = "ABC1234567" }))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	_, header := decodeWith(t, presentedToken(t, a), []byte(testJWTSecret))
	if header["kid"] != "ABC1234567" {
		t.Errorf("kid header = %v, want %q", header["kid"], "ABC1234567")
	}
}

func TestSignedJWTSignsWithEachAlgorithm(t *testing.T) {
	tests := []struct {
		alg    string
		mutate func(*SignedJWTConfig)
		verify func(t *testing.T) any
	}{
		{SignedJWTAlgHS256, nil, func(*testing.T) any { return []byte(testJWTSecret) }},
		{SignedJWTAlgRS256, func(j *SignedJWTConfig) {
			j.Algorithm, j.ClientSecret, j.PrivateKeyPEM = SignedJWTAlgRS256, "", testRSAKeyPEM()
		}, func(t *testing.T) any {
			t.Helper()
			key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(testRSAKeyPEM()))
			if err != nil {
				t.Fatalf("parse RSA key: %v", err)
			}
			return &key.PublicKey
		}},
		{SignedJWTAlgES256, func(j *SignedJWTConfig) {
			j.Algorithm, j.ClientSecret, j.PrivateKeyPEM = SignedJWTAlgES256, "", testECKeyPEM()
		}, func(t *testing.T) any {
			t.Helper()
			key, err := jwt.ParseECPrivateKeyFromPEM([]byte(testECKeyPEM()))
			if err != nil {
				t.Fatalf("parse EC key: %v", err)
			}
			return &key.PublicKey
		}},
	}
	for _, tc := range tests {
		t.Run(tc.alg, func(t *testing.T) {
			c := signedJWTConfig(tc.mutate)
			if err := c.ValidateAuth(); err != nil {
				t.Fatalf("ValidateAuth: %v", err)
			}
			a, err := NewAuthenticator(c)
			if err != nil {
				t.Fatalf("NewAuthenticator: %v", err)
			}
			claims, header := decodeWith(t, presentedToken(t, a), tc.verify(t))
			if header["alg"] != tc.alg {
				t.Errorf("alg header = %v, want %q", header["alg"], tc.alg)
			}
			if claims["iss"] != testJWTIssuer {
				t.Errorf("iss = %v, want %q", claims["iss"], testJWTIssuer)
			}
		})
	}
}

// TestSignedJWTReusesTheCachedToken drives the clock rather than
// sleeping: the cache boundary is the skew window before expiry, and a
// real 300-second lifetime cannot be waited out in a unit test.
func TestSignedJWTReusesTheCachedToken(t *testing.T) {
	c := signedJWTConfig(func(j *SignedJWTConfig) {
		j.TokenLifetime, j.IssuedAtSkew = 300*time.Second, 30*time.Second
	})
	a, err := newSignedJWTAuth(c)
	if err != nil {
		t.Fatalf("newSignedJWTAuth: %v", err)
	}
	base := time.Now()
	now := base
	a.now = func() time.Time { return now }

	first := presentedToken(t, a)

	// Well inside the lifetime: the same token comes back.
	now = base.Add(100 * time.Second)
	if again := presentedToken(t, a); again != first {
		t.Error("a call inside the lifetime minted a new token instead of reusing the cached one")
	}

	// The token expires at base - 30s skew + 300s = base + 270s, and is
	// abandoned once it is within the 30s skew window of that, so
	// base + 241s must mint a new one.
	now = base.Add(241 * time.Second)
	renewed := presentedToken(t, a)
	if renewed == first {
		t.Error("a call inside the skew window of expiry presented the about-to-expire token")
	}
	claims, _ := decodeWith(t, renewed, []byte(testJWTSecret))
	got, ok := claims["iat"].(float64)
	if !ok {
		t.Fatalf("iat is not a number in %v", claims)
	}
	if int64(got) != now.Add(-30*time.Second).Unix() {
		t.Errorf("renewed iat = %v, want the current clock less the skew %v", int64(got), now.Add(-30*time.Second).Unix())
	}
}

// TestSignedJWTApplyIsConcurrencySafe runs the shared Authenticator the
// way a connection does: many in-flight calls against one instance.
func TestSignedJWTApplyIsConcurrencySafe(t *testing.T) {
	a, err := NewAuthenticator(signedJWTConfig(nil))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://erp.example.com/v1/thing", http.NoBody)
			if applyErr := a.Apply(req); applyErr != nil {
				t.Errorf("Apply: %v", applyErr)
			}
		})
	}
	wg.Wait()
}

// TestSignedJWTApplyReportsASigningFailure drives the one runtime failure the
// mode has. Construction resolves the key, so this state is unreachable
// through NewAuthenticator; it is built directly because the criterion is what
// the model is told when signing fails, and the answer must name the step
// without carrying the material.
func TestSignedJWTApplyReportsASigningFailure(t *testing.T) {
	cfg := signedJWTConfig(nil)
	rsaKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(testRSAKeyPEM()))
	if err != nil {
		t.Fatalf("parse RSA key: %v", err)
	}
	// An HMAC method handed an RSA key: SignedString refuses it.
	a := &signedJWTAuth{
		cfg:    cfg,
		signer: signedJWTSigner{method: jwt.SigningMethodHS256, key: rsaKey},
		now:    time.Now,
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://erp.example.com/v1/thing", http.NoBody)
	err = a.Apply(req)
	if err == nil {
		t.Fatal("Apply succeeded with a key the signing method cannot use")
	}
	if !strings.Contains(err.Error(), AuthModeSignedJWT) {
		t.Errorf("error %q does not name the mode that failed", err)
	}
	if !strings.HasPrefix(err.Error(), "apigateway: ") {
		t.Errorf("error %q does not speak in the calling kind's voice", err)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), testJWTSecret) {
		t.Errorf("error %q carries key material", err)
	}
	if got := req.Header.Get(AuthorizationHeader); got != "" {
		t.Errorf("a failed Apply still set Authorization to %q", got)
	}
}

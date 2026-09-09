package graphql

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
)

func TestParseConfigAppliesTheKindsDefaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"endpoint_url": "https://x.example.com/graphql/"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.EndpointURL != "https://x.example.com/graphql" {
		t.Errorf("endpoint = %q; the trailing slash is stripped at parse time", cfg.EndpointURL)
	}
	if cfg.SchemaValidation != SchemaValidationStrict {
		t.Errorf("schema_validation = %q; strict is the default", cfg.SchemaValidation)
	}
	if cfg.MaxQueryDepth != DefaultMaxQueryDepth {
		t.Errorf("max_query_depth = %d", cfg.MaxQueryDepth)
	}
	if cfg.NamespaceDepth != gqlschema.DefaultNamespaceDepth {
		t.Errorf("namespace_depth = %d", cfg.NamespaceDepth)
	}
	if cfg.MaxInlineBytes != DefaultMaxInlineBytes {
		t.Errorf("max_inline_bytes = %d", cfg.MaxInlineBytes)
	}
	if cfg.AuthMode != AuthModeNone {
		t.Errorf("auth_mode = %q; none is the default", cfg.AuthMode)
	}
	if cfg.ConnectTimeout != DefaultConnectTimeout || cfg.CallTimeout != DefaultCallTimeout {
		t.Errorf("timeouts = %v/%v", cfg.ConnectTimeout, cfg.CallTimeout)
	}
	if cfg.ReadOnly {
		t.Error("read_only defaults on")
	}
}

func TestParseConfigReadsThisKindsOwnKeys(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"endpoint_url":      "https://x.example.com/graphql",
		"description":       "The ERP.",
		"schema_validation": "warn",
		"max_query_depth":   5,
		"namespace_depth":   4,
		"max_inline_bytes":  1024,
		"read_only":         true,
		"call_timeout":      "12s",
		"static_headers":    map[string]any{"x-tenant": "acme"},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Description != "The ERP." || cfg.SchemaValidation != SchemaValidationWarn {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.MaxQueryDepth != 5 || cfg.NamespaceDepth != 4 || cfg.MaxInlineBytes != 1024 {
		t.Errorf("limits = %d/%d/%d", cfg.MaxQueryDepth, cfg.NamespaceDepth, cfg.MaxInlineBytes)
	}
	if !cfg.ReadOnly {
		t.Error("read_only was not read")
	}
	if cfg.CallTimeout != 12*time.Second {
		t.Errorf("call_timeout = %v", cfg.CallTimeout)
	}
	if cfg.StaticHeaders["x-tenant"] != "acme" {
		t.Errorf("static headers = %v; the shared seam reads them from the same map", cfg.StaticHeaders)
	}
}

func TestParseConfigRefusesWhatCannotWork(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{"no endpoint", map[string]any{}, "endpoint_url is required"},
		{"unknown validation mode", map[string]any{"endpoint_url": "https://x", "schema_validation": "loose"}, "invalid schema_validation"},
		{"zero depth", map[string]any{"endpoint_url": "https://x", "max_query_depth": 0}, "max_query_depth must be positive"},
		{"negative namespace depth", map[string]any{"endpoint_url": "https://x", "namespace_depth": -1}, "namespace_depth must be positive"},
		{"zero inline budget", map[string]any{"endpoint_url": "https://x", "max_inline_bytes": -5}, "max_inline_bytes must be positive"},
		{"bearer with no credential", map[string]any{"endpoint_url": "https://x", "auth_mode": "bearer"}, "credential"},
		{"a header the model must not claim", map[string]any{"endpoint_url": "https://x", "static_headers": map[string]any{"Authorization": "x"}}, "Authorization"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseConfig(c.config)
			if err == nil {
				t.Fatalf("config was accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q; want it to name %q", err, c.want)
			}
			if !strings.HasPrefix(err.Error(), "graphql:") {
				t.Errorf("err = %q; an operator reads the surface they configured", err)
			}
		})
	}
}

func TestParseMultiConfigSkipsABadInstanceRatherThanFailingStartup(t *testing.T) {
	multi, err := ParseMultiConfig("good", map[string]map[string]any{
		"good": {"endpoint_url": "https://good.example.com/graphql"},
		"bad":  {"schema_validation": "nonsense"},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := multi.Instances["bad"]; ok {
		t.Error("the invalid instance was kept")
	}
	good, ok := multi.Instances["good"]
	if !ok {
		t.Fatal("the valid instance was dropped")
	}
	if good.ConnectionName != "good" {
		t.Errorf("connection name = %q; it comes from the instance name", good.ConnectionName)
	}
}

func TestIsOAuthAuthorizationCode(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"endpoint_url":            "https://x.example.com/graphql",
		"auth_mode":               "oauth",
		"oauth_grant":             "authorization_code",
		"oauth_token_url":         "https://idp.example.com/token",
		"oauth_authorization_url": "https://idp.example.com/authorize",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "secret",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.IsOAuthAuthorizationCode() {
		t.Error("an authorization_code connection did not report itself as one")
	}
	plain, _ := ParseConfig(map[string]any{"endpoint_url": "https://x"})
	if plain.IsOAuthAuthorizationCode() {
		t.Error("an unauthenticated connection reported an authorization_code grant")
	}
}

func TestNewAuthenticatorBuildsTheConfiguredMode(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"endpoint_url": "https://x.example.com/graphql",
		"auth_mode":    "bearer",
		"credential":   "t0ken",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	if auth == nil {
		t.Fatal("no authenticator")
	}
}

// TestParseConfigSignedJWT proves the graphql kind carries the
// signed_jwt block through ParseConfig on the same terms as the api
// kind: the block reaches Config and the audience defaults to the
// connection's endpoint_url.
func TestParseConfigSignedJWT(t *testing.T) {
	const endpoint = "https://erp.example.com/api1/syracuse/collaboration/syracuse"
	cfg, err := ParseConfig(map[string]any{
		"endpoint_url":        endpoint,
		"auth_mode":           AuthModeSignedJWT,
		"jwt_algorithm":       SignedJWTAlgES256,
		"jwt_private_key_pem": testSignedJWTECKeyPEM(t),
		"jwt_key_id":          "ABC1234567",
		"jwt_issuer":          "CLIENTID-2f7c",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.SignedJWT.Algorithm != SignedJWTAlgES256 || cfg.SignedJWT.KeyID != "ABC1234567" {
		t.Errorf("signed_jwt block = %+v, want the configured ES256 key id", cfg.SignedJWT)
	}
	if cfg.SignedJWT.Audience != endpoint {
		t.Errorf("audience = %q, want the endpoint_url %q", cfg.SignedJWT.Audience, endpoint)
	}
	if cfg.SignedJWT.TokenLifetime != DefaultSignedJWTTokenLifetime ||
		cfg.SignedJWT.IssuedAtSkew != DefaultSignedJWTIssuedAtSkew {
		t.Errorf("lifetime/skew = %v/%v, want the shared defaults",
			cfg.SignedJWT.TokenLifetime, cfg.SignedJWT.IssuedAtSkew)
	}
	if _, err := NewAuthenticator(cfg); err != nil {
		t.Errorf("NewAuthenticator on a valid signed_jwt connection: %v", err)
	}
}

func TestParseConfigSignedJWTRefusesMismatchedKeyMaterial(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"endpoint_url":        "https://erp.example.com/graphql",
		"auth_mode":           AuthModeSignedJWT,
		"jwt_algorithm":       SignedJWTAlgES256,
		"jwt_private_key_pem": testSignedJWTECKeyPEM(t),
		"jwt_client_secret":   "a-shared-secret",
		"jwt_issuer":          "CLIENTID-2f7c",
	})
	if err == nil {
		t.Fatal("ParseConfig accepted an ES256 connection carrying a shared secret as well as a signing key")
	}
	if !strings.Contains(err.Error(), "jwt_client_secret") || !strings.HasPrefix(err.Error(), "graphql: ") {
		t.Errorf("error %q should name jwt_client_secret in the kind's voice", err)
	}
}

// testSignedJWTECKeyPEM generates one P-256 key for the signed_jwt
// cases, in the PKCS#8 PEM form the vendors in this class hand out.
func testSignedJWTECKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal EC key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

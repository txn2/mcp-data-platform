package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// FuzzParseToken fuzzes JWT token parsing to find crashes or panics.
func FuzzParseToken(f *testing.F) {
	// Seed corpus with various token formats
	f.Add("") // empty
	f.Add(".")
	f.Add("..")
	f.Add("...")
	f.Add("a.b.c")
	f.Add("header.payload.signature")
	f.Add("eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.signature")

	// Valid base64 but invalid JSON
	invalidJSON := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	f.Add("header." + invalidJSON + ".sig")

	// Valid structure
	validPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user","exp":9999999999}`))
	f.Add("header." + validPayload + ".sig")

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    "https://issuer.example.com",
		SkipIssuerVerification:    true,
		SkipSignatureVerification: true,
	})

	f.Fuzz(func(t *testing.T, token string) {
		// Should never panic
		ctx := WithToken(context.Background(), token)
		_, _ = auth.Authenticate(ctx)
	})
}

// FuzzClaimsExtraction fuzzes claims extraction from various claim structures.
func FuzzClaimsExtraction(f *testing.F) {
	// Seed corpus with various claim structures
	f.Add(`{}`)
	f.Add(`{"sub":"user"}`)
	f.Add(`{"sub":"user","email":"test@example.com"}`)
	f.Add(`{"sub":"user","roles":["admin","user"]}`)
	f.Add(`{"sub":"user","realm_access":{"roles":["admin"]}}`)
	f.Add(`{"sub":123}`) // wrong type for sub
	f.Add(`{"roles":"not-an-array"}`)
	f.Add(`{"realm_access":"not-an-object"}`)
	f.Add(`null`)
	f.Add(`[]`)

	extractor := &ClaimsExtractor{
		RoleClaimPath:    "realm_access.roles",
		RolePrefix:       "dp_",
		EmailClaimPath:   "email",
		NameClaimPath:    "name",
		SubjectClaimPath: "sub",
		GroupClaimPath:   "groups",
	}

	f.Fuzz(func(t *testing.T, claimsJSON string) {
		var claims map[string]any
		if err := json.Unmarshal([]byte(claimsJSON), &claims); err != nil {
			return // Skip invalid JSON
		}

		// Should never panic
		_, _ = extractor.Extract(claims)
	})
}

// FuzzRolePathExtraction fuzzes role path extraction with nested structures.
func FuzzRolePathExtraction(f *testing.F) {
	f.Add("roles", `{"roles":["admin"]}`)
	f.Add("realm_access.roles", `{"realm_access":{"roles":["admin"]}}`)
	f.Add("a.b.c.d.e", `{"a":{"b":{"c":{"d":{"e":["role"]}}}}}`)
	f.Add("deep.path", `{"deep":null}`)
	f.Add("", `{"roles":["admin"]}`)
	f.Add(".", `{"":{"":["role"]}}`)
	f.Add("...", `{}`)

	f.Fuzz(func(t *testing.T, path string, claimsJSON string) {
		var claims map[string]any
		if err := json.Unmarshal([]byte(claimsJSON), &claims); err != nil {
			return // Skip invalid JSON
		}

		extractor := &ClaimsExtractor{
			RoleClaimPath: path,
		}

		// Should never panic
		_ = extractor.getStringSlice(claims, path)
	})
}

// FuzzAPIKeyValidation fuzzes API key validation.
func FuzzAPIKeyValidation(f *testing.F) {
	f.Add("")
	f.Add("valid-key")
	f.Add("Bearer token")
	f.Add("key with spaces")
	f.Add("key\twith\ttabs")
	f.Add("key\nwith\nnewlines")
	f.Add("very-long-key-" + string(make([]byte, 1000)))

	auth := NewAPIKeyAuthenticator(APIKeyConfig{
		Keys: []APIKey{
			{Key: "valid-key", Name: "test", Roles: []string{"admin"}},
		},
	})

	f.Fuzz(func(t *testing.T, key string) {
		ctx := WithToken(context.Background(), key)
		// Should never panic
		_, _ = auth.Authenticate(ctx)
	})
}

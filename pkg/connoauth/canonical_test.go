package connoauth_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

func TestCanonicalize_MovesEveryLegacyKeyOntoItsCanonicalSibling(t *testing.T) {
	in := map[string]any{
		"base_url":                   "https://api.example.com",
		"auth_mode":                  "oauth2_authorization_code",
		"oauth2_token_url":           "https://idp.example.com/token",
		"oauth2_authorization_url":   "https://idp.example.com/auth",
		"oauth2_client_id":           "platform-client",
		"oauth2_client_secret":       "enc:abc",
		"oauth2_scopes":              []any{"read:users", "write:orders"},
		"oauth2_prompt":              "consent",
		"oauth2_endpoint_auth_style": "params",
	}
	got, err := connoauth.Canonicalize(in)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	want := map[string]any{
		"base_url":                  "https://api.example.com",
		"auth_mode":                 "oauth",
		"oauth_grant":               "authorization_code",
		"oauth_token_url":           "https://idp.example.com/token",
		"oauth_authorization_url":   "https://idp.example.com/auth",
		"oauth_client_id":           "platform-client",
		"oauth_client_secret":       "enc:abc",
		"oauth_scope":               "read:users write:orders",
		"oauth_prompt":              "consent",
		"oauth_endpoint_auth_style": "params",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Canonicalize produced\n%v\nwant\n%v", got, want)
	}
	// The operator's map is the caller's; rewriting it in place would
	// change a stored config under a caller that only asked a question.
	if _, still := in["oauth2_client_id"]; !still {
		t.Error("Canonicalize modified the input map")
	}
}

func TestCanonicalize_ParsesBackToTheSameConfig(t *testing.T) {
	legacy := map[string]any{
		"auth_mode":                  "oauth2_authorization_code",
		"oauth2_token_url":           "https://idp.example.com/token",
		"oauth2_authorization_url":   "https://idp.example.com/auth",
		"oauth2_client_id":           "platform-client",
		"oauth2_client_secret":       "shh",
		"oauth2_scopes":              []any{"openid", "profile"},
		"oauth2_endpoint_auth_style": "params",
		"oauth2_prompt":              "login",
	}
	before, err := connoauth.ParseConfig("api", "acme", legacy)
	if err != nil {
		t.Fatalf("ParseConfig(legacy): %v", err)
	}
	canonical, err := connoauth.Canonicalize(legacy)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	after, err := connoauth.ParseConfig("api", "acme", canonical)
	if err != nil {
		t.Fatalf("ParseConfig(canonical): %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("canonicalizing changed what the connection authenticates with:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestCanonicalize_LeavesACanonicalConfigAlone(t *testing.T) {
	in := map[string]any{
		"auth_mode":           "oauth",
		"oauth_grant":         "client_credentials",
		"oauth_token_url":     "https://idp.example.com/token",
		"oauth_client_id":     "platform-client",
		"oauth_client_secret": "shh",
		"oauth_scope":         "read",
	}
	got, err := connoauth.Canonicalize(in)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("Canonicalize rewrote a canonical config:\n%v\nwant\n%v", got, in)
	}
}

func TestCanonicalize_DropsALegacyKeyThatAgreesWithItsSibling(t *testing.T) {
	// Every sensitive value reaches the admin API as the literal
	// "[REDACTED]", so a mixed row re-saved untouched arrives with both
	// secret keys holding the same placeholder. That is resolvable and
	// must not be refused.
	in := map[string]any{
		"auth_mode":            "oauth",
		"oauth_grant":          "authorization_code",
		"oauth_client_secret":  "[REDACTED]",
		"oauth2_client_secret": "[REDACTED]",
		"oauth_client_id":      "same-client",
		"oauth2_client_id":     "same-client",
	}
	got, err := connoauth.Canonicalize(in)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	for _, key := range []string{"oauth2_client_secret", "oauth2_client_id"} {
		if _, still := got[key]; still {
			t.Errorf("%s survived canonicalization", key)
		}
	}
	if got["oauth_client_id"] != "same-client" {
		t.Errorf("oauth_client_id = %v, want same-client", got["oauth_client_id"])
	}
}

func TestCanonicalize_RefusesDisagreeingValuesNamingBothKeys(t *testing.T) {
	in := map[string]any{
		"auth_mode":        "oauth2_authorization_code",
		"oauth_grant":      "authorization_code",
		"oauth_client_id":  "PENDING-REPLACE-ME.apps.googleusercontent.com",
		"oauth2_client_id": "986495125425.apps.googleusercontent.com",
	}
	_, err := connoauth.Canonicalize(in)
	if err == nil {
		t.Fatal("Canonicalize accepted two disagreeing client ids")
	}
	if !errors.Is(err, connoauth.ErrVocabularyConflict) {
		t.Errorf("error does not wrap ErrVocabularyConflict: %v", err)
	}
	for _, want := range []string{"oauth_client_id", "oauth2_client_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not name %s: %v", want, err)
		}
	}
	// Neither value may appear: one of the pairs is a client secret and
	// this message reaches the operator through an HTTP response body.
	for _, secret := range []string{"PENDING-REPLACE-ME", "986495125425"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("message leaks a configured value (%s): %v", secret, err)
		}
	}
}

func TestCanonicalize_RefusesAGrantTheLegacyModeContradicts(t *testing.T) {
	in := map[string]any{
		"auth_mode":   "oauth2_client_credentials",
		"oauth_grant": "authorization_code",
	}
	_, err := connoauth.Canonicalize(in)
	if err == nil {
		t.Fatal("Canonicalize accepted a mode and a grant naming different flows")
	}
	if !errors.Is(err, connoauth.ErrVocabularyConflict) {
		t.Errorf("error does not wrap ErrVocabularyConflict: %v", err)
	}
	for _, want := range []string{"auth_mode", "oauth_grant"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not name %s: %v", want, err)
		}
	}
}

func TestCanonicalize_KeepsAnAgreeingLegacyModeAndGrant(t *testing.T) {
	got, err := connoauth.Canonicalize(map[string]any{
		"auth_mode":   "oauth2_authorization_code",
		"oauth_grant": "authorization_code",
	})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if got["auth_mode"] != "oauth" || got["oauth_grant"] != "authorization_code" {
		t.Errorf("got %v, want auth_mode=oauth oauth_grant=authorization_code", got)
	}
}

func TestCanonicalize_ReportsAMalformedLegacyScope(t *testing.T) {
	_, err := connoauth.Canonicalize(map[string]any{"oauth2_scopes": "read write"})
	if err == nil {
		t.Fatal("Canonicalize accepted a string where the legacy scope array goes")
	}
	if !errors.Is(err, connoauth.ErrInvalidConfig) {
		t.Errorf("error does not wrap ErrInvalidConfig: %v", err)
	}
}

func TestCanonicalize_NilAndNonOAuthConfigs(t *testing.T) {
	got, err := connoauth.Canonicalize(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("Canonicalize(nil) = %v, %v; want an empty config and no error", got, err)
	}
	in := map[string]any{"base_url": "https://api.example.com", "auth_mode": "bearer", "credential": "t"}
	out, err := connoauth.Canonicalize(in)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("Canonicalize rewrote a non-OAuth config: %v", out)
	}
}

func TestDescribeVocabulary(t *testing.T) {
	tests := []struct {
		name         string
		cfg          map[string]any
		wantVocab    string
		wantShadowed []string
	}{
		{
			name:      "no oauth configuration at all",
			cfg:       map[string]any{"auth_mode": "bearer", "credential": "t"},
			wantVocab: "",
		},
		{
			name:      "canonical",
			cfg:       map[string]any{"auth_mode": "oauth", "oauth_grant": "authorization_code", "oauth_client_id": "c"},
			wantVocab: connoauth.VocabularyCanonical,
		},
		{
			name:      "legacy",
			cfg:       map[string]any{"auth_mode": "oauth2_client_credentials", "oauth2_client_id": "c"},
			wantVocab: connoauth.VocabularyLegacy,
		},
		{
			name: "mixed names the shadowed keys",
			cfg: map[string]any{
				"auth_mode":            "oauth2_authorization_code",
				"oauth_grant":          "authorization_code",
				"oauth_client_id":      "canonical",
				"oauth2_client_id":     "shadowed",
				"oauth_client_secret":  "canonical",
				"oauth2_client_secret": "shadowed",
				"oauth2_scopes":        []any{"read"},
			},
			wantVocab: connoauth.VocabularyMixed,
			// oauth2_scopes has no canonical sibling here, so it is read,
			// not shadowed. auth_mode is, because oauth_grant decides.
			wantShadowed: []string{"oauth2_client_id", "oauth2_client_secret", "auth_mode"},
		},
		{
			name:      "a legacy key with no canonical sibling shadows nothing",
			cfg:       map[string]any{"auth_mode": "oauth", "oauth2_client_id": "read"},
			wantVocab: connoauth.VocabularyMixed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vocab, shadowed := connoauth.DescribeVocabulary(tt.cfg)
			if vocab != tt.wantVocab {
				t.Errorf("vocabulary = %q, want %q", vocab, tt.wantVocab)
			}
			if !reflect.DeepEqual(shadowed, tt.wantShadowed) {
				t.Errorf("shadowed = %v, want %v", shadowed, tt.wantShadowed)
			}
		})
	}
}

func TestDescribeVocabulary_NilConfig(t *testing.T) {
	if vocab, shadowed := connoauth.DescribeVocabulary(nil); vocab != "" || shadowed != nil {
		t.Errorf("DescribeVocabulary(nil) = %q, %v; want \"\", nil", vocab, shadowed)
	}
}

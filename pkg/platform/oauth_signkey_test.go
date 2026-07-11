package platform

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func b64key(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestParseOrGenerateSigningKey_HTTPHardFail(t *testing.T) {
	p := &Platform{config: &Config{
		Server: ServerConfig{Transport: "http"},
		OAuth:  OAuthConfig{Enabled: true, Issuer: "https://oauth.example.com"},
	}}

	_, err := p.parseOrGenerateSigningKey()
	if err == nil {
		t.Fatal("expected startup error for http + oauth + no signing key")
	}
}

func TestParseOrGenerateSigningKey_EphemeralEscapeHatch(t *testing.T) {
	p := &Platform{config: &Config{
		Server: ServerConfig{Transport: "http"},
		OAuth: OAuthConfig{
			Enabled:                  true,
			Issuer:                   "https://oauth.example.com",
			AllowEphemeralSigningKey: true,
		},
	}}

	key, err := p.parseOrGenerateSigningKey()
	if err != nil {
		t.Fatalf("expected ephemeral key generation, got error: %v", err)
	}
	if len(key) < minSigningKeyLength {
		t.Errorf("generated key length = %d, want >= %d", len(key), minSigningKeyLength)
	}
}

func TestParseOrGenerateSigningKey_StdioGenerates(t *testing.T) {
	p := &Platform{config: &Config{
		Server: ServerConfig{Transport: "stdio"},
		OAuth:  OAuthConfig{Enabled: true, Issuer: "https://oauth.example.com"},
	}}

	key, err := p.parseOrGenerateSigningKey()
	if err != nil {
		t.Fatalf("stdio should auto-generate, got error: %v", err)
	}
	if len(key) < minSigningKeyLength {
		t.Errorf("generated key length = %d, want >= %d", len(key), minSigningKeyLength)
	}
}

func TestParseOrGenerateSigningKey_ConfiguredKey(t *testing.T) {
	raw := "configured-signing-key-at-least-32-bytes"
	p := &Platform{config: &Config{
		Server: ServerConfig{Transport: "http"},
		OAuth: OAuthConfig{
			Enabled:    true,
			Issuer:     "https://oauth.example.com",
			SigningKey: b64key(raw),
		},
	}}

	key, err := p.parseOrGenerateSigningKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(key, []byte(raw)) {
		t.Errorf("decoded key = %q, want %q", key, raw)
	}
}

func TestParsePreviousSigningKeys(t *testing.T) {
	t.Run("empty yields nil", func(t *testing.T) {
		keys, err := parsePreviousSigningKeys(nil)
		if err != nil || keys != nil {
			t.Errorf("parsePreviousSigningKeys() = %v, %v; want nil, nil", keys, err)
		}
	})

	t.Run("valid keys decode", func(t *testing.T) {
		keys, err := parsePreviousSigningKeys([]string{
			b64key("previous-one-signing-key-32-bytes-long"),
			b64key("previous-two-signing-key-32-bytes-long"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(keys) != 2 {
			t.Fatalf("got %d keys, want 2", len(keys))
		}
	})

	t.Run("invalid base64 errors", func(t *testing.T) {
		if _, err := parsePreviousSigningKeys([]string{"not!base64!"}); err == nil {
			t.Error("expected error for invalid base64")
		}
	})

	t.Run("too-short key errors", func(t *testing.T) {
		if _, err := parsePreviousSigningKeys([]string{b64key("short")}); err == nil {
			t.Error("expected error for key under the minimum length")
		}
	})
}

func TestInitOAuthSigningKey_SetsCurrentAndPrevious(t *testing.T) {
	current := "current-signing-key-at-least-32-bytes-x"
	previous := "previous-signing-key-at-least-32-bytes"
	p := &Platform{config: &Config{
		Server: ServerConfig{Transport: "http"},
		OAuth: OAuthConfig{
			Enabled:             true,
			Issuer:              "https://oauth.example.com",
			SigningKey:          b64key(current),
			PreviousSigningKeys: []string{b64key(previous)},
		},
	}}

	if err := p.initOAuthSigningKey(); err != nil {
		t.Fatalf("initOAuthSigningKey() error = %v", err)
	}
	if !bytes.Equal(p.oauthKeys.current, []byte(current)) {
		t.Errorf("oauthKeys.current = %q, want %q", p.oauthKeys.current, current)
	}
	if len(p.oauthKeys.previous) != 1 || !bytes.Equal(p.oauthKeys.previous[0], []byte(previous)) {
		t.Errorf("oauthKeys.previous = %q, want one entry %q", p.oauthKeys.previous, previous)
	}
}

package oauth

import (
	"strings"
	"testing"
)

func TestValidateCodeVerifier(t *testing.T) {
	tests := []struct {
		name     string
		verifier string
		wantErr  bool
	}{
		{
			name:     "valid verifier",
			verifier: strings.Repeat("a", 43),
			wantErr:  false,
		},
		{
			name:     "max length",
			verifier: strings.Repeat("a", 128),
			wantErr:  false,
		},
		{
			name:     "too short",
			verifier: strings.Repeat("a", 42),
			wantErr:  true,
		},
		{
			name:     "too long",
			verifier: strings.Repeat("a", 129),
			wantErr:  true,
		},
		{
			name:     "invalid characters",
			verifier: strings.Repeat("a", 43) + "@",
			wantErr:  true,
		},
		{
			name:     "valid with special chars",
			verifier: strings.Repeat("a", 40) + "-._~",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCodeVerifier(tt.verifier)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCodeVerifier() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateCodeChallenge(t *testing.T) {
	tests := []struct {
		name      string
		challenge string
		wantErr   bool
	}{
		{
			name:      "valid challenge",
			challenge: strings.Repeat("a", 43),
			wantErr:   false,
		},
		{
			name:      "too short",
			challenge: strings.Repeat("a", 42),
			wantErr:   true,
		},
		{
			name:      "invalid characters",
			challenge: strings.Repeat("a", 43) + "+",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCodeChallenge(tt.challenge)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCodeChallenge() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGenerateCodeChallenge(t *testing.T) {
	verifier := strings.Repeat("a", 43)

	t.Run("plain method", func(t *testing.T) {
		challenge, err := GenerateCodeChallenge(verifier, PKCEMethodPlain)
		if err != nil {
			t.Fatalf("GenerateCodeChallenge() error = %v", err)
		}
		if challenge != verifier {
			t.Error("plain challenge should equal verifier")
		}
	})

	t.Run("S256 method", func(t *testing.T) {
		challenge, err := GenerateCodeChallenge(verifier, PKCEMethodS256)
		if err != nil {
			t.Fatalf("GenerateCodeChallenge() error = %v", err)
		}
		if challenge == verifier {
			t.Error("S256 challenge should not equal verifier")
		}
		// S256 produces base64url encoded SHA-256 hash (43 chars for 32 bytes)
		if len(challenge) != 43 {
			t.Errorf("S256 challenge length = %d, want 43", len(challenge))
		}
	})

	t.Run("invalid method", func(t *testing.T) {
		_, err := GenerateCodeChallenge(verifier, "invalid")
		if err == nil {
			t.Error("GenerateCodeChallenge() expected error for invalid method")
		}
	})

	t.Run("invalid verifier", func(t *testing.T) {
		_, err := GenerateCodeChallenge("short", PKCEMethodS256)
		if err == nil {
			t.Error("GenerateCodeChallenge() expected error for invalid verifier")
		}
	})
}

func TestVerifyCodeChallenge(t *testing.T) {
	verifier := strings.Repeat("a", 43)

	t.Run("S256 valid", func(t *testing.T) {
		challenge, _ := GenerateCodeChallenge(verifier, PKCEMethodS256)
		valid, err := VerifyCodeChallenge(verifier, challenge, PKCEMethodS256)
		if err != nil {
			t.Fatalf("VerifyCodeChallenge() error = %v", err)
		}
		if !valid {
			t.Error("VerifyCodeChallenge() = false, want true")
		}
	})

	t.Run("S256 invalid", func(t *testing.T) {
		valid, err := VerifyCodeChallenge(verifier, "wrong-challenge"+strings.Repeat("a", 30), PKCEMethodS256)
		if err != nil {
			t.Fatalf("VerifyCodeChallenge() error = %v", err)
		}
		if valid {
			t.Error("VerifyCodeChallenge() = true, want false")
		}
	})

	t.Run("plain valid", func(t *testing.T) {
		valid, err := VerifyCodeChallenge(verifier, verifier, PKCEMethodPlain)
		if err != nil {
			t.Fatalf("VerifyCodeChallenge() error = %v", err)
		}
		if !valid {
			t.Error("VerifyCodeChallenge() = false, want true")
		}
	})
}

func TestDefaultPKCEMethod(t *testing.T) {
	if DefaultPKCEMethod() != PKCEMethodS256 {
		t.Errorf("DefaultPKCEMethod() = %v, want %v", DefaultPKCEMethod(), PKCEMethodS256)
	}
}

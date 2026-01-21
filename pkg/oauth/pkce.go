package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
)

// PKCEMethod defines the code challenge method.
type PKCEMethod string

const (
	// PKCEMethodPlain uses plain text (not recommended).
	PKCEMethodPlain PKCEMethod = "plain"

	// PKCEMethodS256 uses SHA-256 hashing (recommended).
	PKCEMethodS256 PKCEMethod = "S256"
)

// ValidateCodeVerifier validates a code verifier.
// Per RFC 7636, it must be 43-128 characters of [A-Z] / [a-z] / [0-9] / "-" / "." / "_" / "~".
func ValidateCodeVerifier(verifier string) error {
	if len(verifier) < 43 || len(verifier) > 128 {
		return fmt.Errorf("code verifier must be between 43 and 128 characters")
	}

	validPattern := regexp.MustCompile(`^[A-Za-z0-9\-._~]+$`)
	if !validPattern.MatchString(verifier) {
		return fmt.Errorf("code verifier contains invalid characters")
	}

	return nil
}

// ValidateCodeChallenge validates a code challenge.
func ValidateCodeChallenge(challenge string) error {
	if len(challenge) < 43 || len(challenge) > 128 {
		return fmt.Errorf("code challenge must be between 43 and 128 characters")
	}

	// Base64 URL-safe characters
	validPattern := regexp.MustCompile(`^[A-Za-z0-9\-_]+$`)
	if !validPattern.MatchString(challenge) {
		return fmt.Errorf("code challenge contains invalid characters")
	}

	return nil
}

// GenerateCodeChallenge generates a code challenge from a verifier.
func GenerateCodeChallenge(verifier string, method PKCEMethod) (string, error) {
	if err := ValidateCodeVerifier(verifier); err != nil {
		return "", err
	}

	switch method {
	case PKCEMethodPlain:
		return verifier, nil
	case PKCEMethodS256:
		hash := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(hash[:]), nil
	default:
		return "", fmt.Errorf("unsupported PKCE method: %s", method)
	}
}

// VerifyCodeChallenge verifies a code verifier against a challenge.
func VerifyCodeChallenge(verifier, challenge string, method PKCEMethod) (bool, error) {
	computed, err := GenerateCodeChallenge(verifier, method)
	if err != nil {
		return false, err
	}
	return computed == challenge, nil
}

// DefaultPKCEMethod returns the default (and recommended) PKCE method.
func DefaultPKCEMethod() PKCEMethod {
	return PKCEMethodS256
}

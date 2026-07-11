package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
)

// PKCEMethod defines the code challenge method.
type PKCEMethod string

const (
	// PKCEMethodS256 uses SHA-256 hashing. Per OAuth 2.1 it is the only
	// supported code challenge method; the "plain" method is not accepted.
	PKCEMethodS256 PKCEMethod = "S256"

	// pkceMinLength is the minimum length for code verifiers and challenges per RFC 7636.
	pkceMinLength = 43

	// pkceMaxLength is the maximum length for code verifiers and challenges per RFC 7636.
	pkceMaxLength = 128
)

// ValidateCodeVerifier validates a code verifier.
// Per RFC 7636, it must be 43-128 characters of [A-Z] / [a-z] / [0-9] / "-" / "." / "_" / "~".
func ValidateCodeVerifier(verifier string) error {
	if len(verifier) < pkceMinLength || len(verifier) > pkceMaxLength {
		return fmt.Errorf("code verifier must be between %d and %d characters", pkceMinLength, pkceMaxLength)
	}

	validPattern := regexp.MustCompile(`^[A-Za-z0-9\-._~]+$`)
	if !validPattern.MatchString(verifier) {
		return errors.New("code verifier contains invalid characters")
	}

	return nil
}

// ValidateCodeChallenge validates a code challenge.
func ValidateCodeChallenge(challenge string) error {
	if len(challenge) < pkceMinLength || len(challenge) > pkceMaxLength {
		return fmt.Errorf("code challenge must be between %d and %d characters", pkceMinLength, pkceMaxLength)
	}

	// Base64 URL-safe characters
	validPattern := regexp.MustCompile(`^[A-Za-z0-9\-_]+$`)
	if !validPattern.MatchString(challenge) {
		return errors.New("code challenge contains invalid characters")
	}

	return nil
}

// GenerateCodeChallenge generates a code challenge from a verifier.
func GenerateCodeChallenge(verifier string, method PKCEMethod) (string, error) {
	if err := ValidateCodeVerifier(verifier); err != nil {
		return "", err
	}

	switch method {
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

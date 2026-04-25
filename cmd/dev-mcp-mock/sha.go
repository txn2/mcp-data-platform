package main

import (
	"crypto/sha256"
	"encoding/base64"
)

// base64URLNoPadSHA256 produces the canonical PKCE S256
// code_challenge: base64url(SHA-256(verifier)) without padding.
func base64URLNoPadSHA256(in []byte) string {
	sum := sha256.Sum256(in)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

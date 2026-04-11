package resource

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const idLength = 16 // 16 bytes → 32 hex characters

// GenerateID returns a cryptographically random 32-character hex string.
func GenerateID() (string, error) {
	b := make([]byte, idLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating resource ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

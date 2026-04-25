package platform

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// aes256KeyLength is the required key length for AES-256.
const aes256KeyLength = 32

// encryptedPrefix marks a value as AES-256-GCM encrypted + base64-encoded.
const encryptedPrefix = "enc:"

// sensitiveConfigKeys are the config map keys whose values must be encrypted at rest.
var sensitiveConfigKeys = map[string]bool{
	"password":            true,
	"secret_access_key":   true,
	"secret_key":          true,
	"token":               true,
	"access_token":        true,
	"refresh_token":       true,
	"api_key":             true,
	"credential":          true,
	"client_secret":       true,
	"oauth_client_secret": true,
}

// FieldEncryptor encrypts and decrypts sensitive fields within connection config maps.
// Uses AES-256-GCM with a random nonce per encryption. The encrypted value is stored
// as "enc:" + base64(nonce + ciphertext).
type FieldEncryptor struct {
	gcm cipher.AEAD
}

// NewFieldEncryptor creates an encryptor from a 32-byte (AES-256) key.
// The key should come from the ENCRYPTION_KEY environment variable.
// Returns nil if the key is empty (encryption disabled — values stored in plain text).
func NewFieldEncryptor(key []byte) (*FieldEncryptor, error) {
	if len(key) == 0 {
		return nil, nil //nolint:nilnil // nil encryptor = encryption disabled
	}
	if len(key) != aes256KeyLength {
		return nil, fmt.Errorf("encryption key must be exactly %d bytes (got %d)", aes256KeyLength, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	return &FieldEncryptor{gcm: gcm}, nil
}

// EncryptSensitiveFields returns a copy of the config map with sensitive field values encrypted.
// Non-sensitive fields and already-encrypted values are left unchanged.
// If the encryptor is nil, the original map is returned unchanged.
func (e *FieldEncryptor) EncryptSensitiveFields(config map[string]any) (map[string]any, error) {
	if e == nil || config == nil {
		return config, nil
	}

	result := make(map[string]any, len(config))
	for k, v := range config {
		if !sensitiveConfigKeys[k] {
			result[k] = v
			continue
		}

		str, ok := v.(string)
		if !ok || str == "" {
			result[k] = v
			continue
		}

		// Skip already-encrypted values.
		if strings.HasPrefix(str, encryptedPrefix) {
			result[k] = v
			continue
		}

		encrypted, err := e.encrypt(str)
		if err != nil {
			return nil, fmt.Errorf("encrypting field %q: %w", k, err)
		}
		result[k] = encryptedPrefix + encrypted
	}
	return result, nil
}

// DecryptSensitiveFields returns a copy of the config map with sensitive field values decrypted.
// Non-sensitive fields and plain-text values are left unchanged.
// If the encryptor is nil, the original map is returned unchanged.
func (e *FieldEncryptor) DecryptSensitiveFields(config map[string]any) (map[string]any, error) {
	if e == nil || config == nil {
		return config, nil
	}

	result := make(map[string]any, len(config))
	for k, v := range config {
		if !sensitiveConfigKeys[k] {
			result[k] = v
			continue
		}

		str, ok := v.(string)
		if !ok || !strings.HasPrefix(str, encryptedPrefix) {
			result[k] = v
			continue
		}

		decrypted, err := e.decrypt(strings.TrimPrefix(str, encryptedPrefix))
		if err != nil {
			return nil, fmt.Errorf("decrypting field %q: %w", k, err)
		}
		result[k] = decrypted
	}
	return result, nil
}

// encrypt encrypts plaintext with AES-256-GCM and returns base64(nonce + ciphertext).
func (e *FieldEncryptor) encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decrypt decodes base64, splits nonce + ciphertext, and decrypts with AES-256-GCM.
func (e *FieldEncryptor) decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decoding base64: %w", err)
	}

	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}

	return string(plaintext), nil
}

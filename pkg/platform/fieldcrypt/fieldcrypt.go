// Package fieldcrypt provides AES-256-GCM encryption of sensitive fields
// within connection-config maps, plus the RestFieldEncryptor adapter used by
// sub-package stores (gateway OAuth tokens, PKCE state). It is a standalone
// package (not tied to *platform.Platform) so the at-rest encryption concern
// lives in one place and does not count against the platform package budget.
package fieldcrypt

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

// KeyLength is the required key length in bytes for AES-256.
const KeyLength = 32

// encryptedPrefix marks a value as AES-256-GCM encrypted + base64-encoded.
const encryptedPrefix = "enc:"

// CfgKeyPassword is the conventional config-map key for password fields.
const CfgKeyPassword = "password"

// Conventional config-map keys for sensitive fields. Defined as
// constants because the same literals appear in multiple call sites
// (encryption allowlist + per-toolkit config extraction) and a typo
// or rename would silently bypass at-rest encryption.
const (
	// CfgKeySecretAccessKey is the S3 secret access key config-map key.
	CfgKeySecretAccessKey = "secret_access_key"
	cfgKeySecretKey       = "secret_key"
	// CfgKeyToken is the bearer/token config-map key.
	CfgKeyToken = "token"
)

// sensitiveConfigKeys are the config map keys whose values must be encrypted at rest.
var sensitiveConfigKeys = map[string]bool{
	CfgKeyPassword:         true,
	CfgKeySecretAccessKey:  true,
	cfgKeySecretKey:        true,
	CfgKeyToken:            true,
	"access_token":         true,
	"refresh_token":        true,
	"api_key":              true,
	"credential":           true,
	"client_secret":        true,
	"oauth_client_secret":  true,
	"oauth2_client_secret": true, // api gateway client_credentials grant
	"mtls_client_key_pem":  true, // api gateway mTLS private key
}

// CfgKeyStaticHeaders is the connection-config key whose value is a
// nested map[string]any of HTTP headers added to every outbound
// apigateway request. Exported so the toolkit and admin redaction
// share one canonical name. The inner values are encrypted at rest.
const CfgKeyStaticHeaders = "static_headers"

// sensitiveNestedMapKeys are top-level keys whose value is a
// map[string]any whose inner string values are themselves secrets that
// must be encrypted at rest. The shape is a separate set from
// sensitiveConfigKeys because the encryption walk has to recurse one
// level (not just encrypt the scalar at the top).
var sensitiveNestedMapKeys = map[string]bool{
	CfgKeyStaticHeaders: true,
}

// SensitiveNestedMapKeyList returns the nested-map sensitive key set
// as a slice for cross-package tests (mirrors SensitiveConfigKeyList).
func SensitiveNestedMapKeyList() []string {
	keys := make([]string, 0, len(sensitiveNestedMapKeys))
	for k := range sensitiveNestedMapKeys {
		keys = append(keys, k)
	}
	return keys
}

// coerceNestedMap upcasts a connection-config value into
// map[string]any when it carries map-of-strings semantics. Returns nil
// for any other shape so callers can distinguish "not a map" (return
// the input unchanged) from "a map but empty" (encrypt nothing).
func coerceNestedMap(raw any) map[string]any {
	switch v := raw.(type) {
	case map[string]any:
		return v
	case map[string]string:
		inner := make(map[string]any, len(v))
		for k, val := range v {
			inner[k] = val
		}
		return inner
	}
	return nil
}

// SensitiveConfigKeyList returns a copy of the sensitive-key set as a
// slice for tests in other packages (e.g. pkg/admin) that assert
// their redaction list covers the encryption layer's set.
// Returning a copy keeps the underlying map private.
func SensitiveConfigKeyList() []string {
	keys := make([]string, 0, len(sensitiveConfigKeys))
	for k := range sensitiveConfigKeys {
		keys = append(keys, k)
	}
	return keys
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
	if len(key) != KeyLength {
		return nil, fmt.Errorf("encryption key must be exactly %d bytes (got %d)", KeyLength, len(key))
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
		encrypted, err := e.encryptField(k, v)
		if err != nil {
			return nil, err
		}
		result[k] = encrypted
	}
	return result, nil
}

// encryptField dispatches to the right encryption path for a single
// config-map entry. Three branches: nested-map sensitive keys recurse
// one level; scalar sensitive keys encrypt the string value; everything
// else is passthrough.
func (e *FieldEncryptor) encryptField(key string, value any) (any, error) {
	if sensitiveNestedMapKeys[key] {
		return e.encryptNestedMap(key, value)
	}
	if !sensitiveConfigKeys[key] {
		return value, nil
	}
	str, ok := value.(string)
	if !ok || str == "" || strings.HasPrefix(str, encryptedPrefix) {
		return value, nil
	}
	encrypted, err := e.encrypt(str)
	if err != nil {
		return nil, fmt.Errorf("encrypting field %q: %w", key, err)
	}
	return encryptedPrefix + encrypted, nil
}

// encryptNestedMap encrypts every string value inside a nested
// map[string]any. Used for config keys whose value is itself a map of
// header-name → header-value pairs (e.g. static_headers). Non-string
// inner values and already-encrypted strings are left alone. Returns
// the original value unchanged when it is not a map.
func (e *FieldEncryptor) encryptNestedMap(parentKey string, raw any) (any, error) {
	inner := coerceNestedMap(raw)
	if inner == nil {
		return raw, nil
	}
	out := make(map[string]any, len(inner))
	for k, v := range inner {
		str, isStr := v.(string)
		if !isStr || str == "" || strings.HasPrefix(str, encryptedPrefix) {
			out[k] = v
			continue
		}
		encrypted, err := e.encrypt(str)
		if err != nil {
			return nil, fmt.Errorf("encrypting field %q.%q: %w", parentKey, k, err)
		}
		out[k] = encryptedPrefix + encrypted
	}
	return out, nil
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
		decrypted, err := e.decryptField(k, v)
		if err != nil {
			return nil, err
		}
		result[k] = decrypted
	}
	return result, nil
}

// decryptField is the inverse of encryptField.
func (e *FieldEncryptor) decryptField(key string, value any) (any, error) {
	if sensitiveNestedMapKeys[key] {
		return e.decryptNestedMap(key, value)
	}
	if !sensitiveConfigKeys[key] {
		return value, nil
	}
	str, ok := value.(string)
	if !ok || !strings.HasPrefix(str, encryptedPrefix) {
		return value, nil
	}
	decrypted, err := e.decrypt(strings.TrimPrefix(str, encryptedPrefix))
	if err != nil {
		return nil, fmt.Errorf("decrypting field %q: %w", key, err)
	}
	return decrypted, nil
}

// decryptNestedMap is the inverse of encryptNestedMap. Inner string
// values that lack the encryptedPrefix are returned as-is (handles
// configs written before encryption was enabled).
func (e *FieldEncryptor) decryptNestedMap(parentKey string, raw any) (any, error) {
	inner := coerceNestedMap(raw)
	if inner == nil {
		return raw, nil
	}
	out := make(map[string]any, len(inner))
	for k, v := range inner {
		str, isStr := v.(string)
		if !isStr || !strings.HasPrefix(str, encryptedPrefix) {
			out[k] = v
			continue
		}
		decrypted, err := e.decrypt(strings.TrimPrefix(str, encryptedPrefix))
		if err != nil {
			return nil, fmt.Errorf("decrypting field %q.%q: %w", parentKey, k, err)
		}
		out[k] = decrypted
	}
	return out, nil
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

// RestFieldEncryptor adapts a FieldEncryptor to the generic Encrypt/Decrypt
// interface used by sub-package stores (gateway OAuth tokens, PKCE state, etc.)
// so every at-rest secret uses the same key, algorithm, and
// enc:base64(nonce|cipher) prefix as connection credentials.
//
// Exported so consumers like main.go can pass it to constructors (e.g.,
// pkcestore.NewPostgresStore) without importing internal platform types.
type RestFieldEncryptor struct {
	enc *FieldEncryptor
}

// NewRestFieldEncryptor wraps a FieldEncryptor (which may be nil when
// encryption is disabled) as a RestFieldEncryptor.
func NewRestFieldEncryptor(enc *FieldEncryptor) *RestFieldEncryptor {
	return &RestFieldEncryptor{enc: enc}
}

// Encrypt wraps a string with the same enc:base64(nonce|cipher) prefix
// the field encryptor uses for sensitive config map values.
func (g *RestFieldEncryptor) Encrypt(plaintext string) (string, error) {
	if g.enc == nil || plaintext == "" {
		return plaintext, nil
	}
	if strings.HasPrefix(plaintext, encryptedPrefix) {
		return plaintext, nil // idempotent: already encrypted
	}
	out, err := g.enc.encrypt(plaintext)
	if err != nil {
		return "", fmt.Errorf("rest field encrypt: %w", err)
	}
	return encryptedPrefix + out, nil
}

// Decrypt reverses Encrypt. Plaintext (no enc: prefix) is returned
// unchanged so legacy/unencrypted rows degrade gracefully.
func (g *RestFieldEncryptor) Decrypt(ciphertext string) (string, error) {
	if g.enc == nil || ciphertext == "" {
		return ciphertext, nil
	}
	if !strings.HasPrefix(ciphertext, encryptedPrefix) {
		return ciphertext, nil
	}
	out, err := g.enc.decrypt(strings.TrimPrefix(ciphertext, encryptedPrefix))
	if err != nil {
		return "", fmt.Errorf("rest field decrypt: %w", err)
	}
	return out, nil
}

package platform

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestNewFieldEncryptor(t *testing.T) {
	t.Run("nil key returns nil encryptor", func(t *testing.T) {
		e, err := NewFieldEncryptor(nil)
		assert.NoError(t, err)
		assert.Nil(t, e)
	})

	t.Run("empty key returns nil encryptor", func(t *testing.T) {
		e, err := NewFieldEncryptor([]byte{})
		assert.NoError(t, err)
		assert.Nil(t, e)
	})

	t.Run("wrong key length returns error", func(t *testing.T) {
		_, err := NewFieldEncryptor([]byte("too-short"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "32 bytes")
	})

	t.Run("valid 32-byte key succeeds", func(t *testing.T) {
		e, err := NewFieldEncryptor(testKey(t))
		assert.NoError(t, err)
		assert.NotNil(t, e)
	})
}

func TestFieldEncryptor_RoundTrip(t *testing.T) {
	e, err := NewFieldEncryptor(testKey(t))
	require.NoError(t, err)

	config := map[string]any{
		"host":              "trino.example.com",
		"port":              float64(443),
		"password":          "super-secret",
		"secret_access_key": "AKIA-secret-key",
		"token":             "bearer-token-123",
		"catalog":           "iceberg",
		"ssl":               true,
	}

	// Encrypt
	encrypted, err := e.EncryptSensitiveFields(config)
	require.NoError(t, err)

	// Non-sensitive fields unchanged
	assert.Equal(t, "trino.example.com", encrypted["host"])
	assert.Equal(t, float64(443), encrypted["port"])
	assert.Equal(t, "iceberg", encrypted["catalog"])
	assert.Equal(t, true, encrypted["ssl"])

	// Sensitive fields are encrypted (prefixed with "enc:")
	pw, ok := encrypted["password"].(string)
	require.True(t, ok)
	assert.Contains(t, pw, "enc:")
	sak, ok := encrypted["secret_access_key"].(string)
	require.True(t, ok)
	assert.Contains(t, sak, "enc:")
	tok, ok := encrypted["token"].(string)
	require.True(t, ok)
	assert.Contains(t, tok, "enc:")
	assert.NotEqual(t, "super-secret", encrypted["password"])

	// Decrypt
	decrypted, err := e.DecryptSensitiveFields(encrypted)
	require.NoError(t, err)

	assert.Equal(t, "super-secret", decrypted["password"])
	assert.Equal(t, "AKIA-secret-key", decrypted["secret_access_key"])
	assert.Equal(t, "bearer-token-123", decrypted["token"])
	assert.Equal(t, "trino.example.com", decrypted["host"])
}

func TestFieldEncryptor_NilEncryptor(t *testing.T) {
	var e *FieldEncryptor

	config := map[string]any{"password": "secret", "host": "localhost"}

	encrypted, err := e.EncryptSensitiveFields(config)
	assert.NoError(t, err)
	assert.Equal(t, "secret", encrypted["password"]) // unchanged

	decrypted, err := e.DecryptSensitiveFields(config)
	assert.NoError(t, err)
	assert.Equal(t, "secret", decrypted["password"]) // unchanged
}

func TestFieldEncryptor_NilConfig(t *testing.T) {
	e, err := NewFieldEncryptor(testKey(t))
	require.NoError(t, err)

	encrypted, err := e.EncryptSensitiveFields(nil)
	assert.NoError(t, err)
	assert.Nil(t, encrypted)

	decrypted, err := e.DecryptSensitiveFields(nil)
	assert.NoError(t, err)
	assert.Nil(t, decrypted)
}

// TestRestFieldEncryptor_RoundTripAndIdempotence covers the adapter
// that sub-package stores (gateway tokens, PKCE state) use. It must:
//
//  1. round-trip: Encrypt then Decrypt returns the original
//  2. add the enc: prefix on first Encrypt
//  3. be idempotent: Encrypting an already-prefixed value is a no-op
//  4. degrade gracefully: Decrypt on plaintext (no prefix) returns it
//     unchanged so legacy unencrypted rows don't break
func TestRestFieldEncryptor_RoundTripAndIdempotence(t *testing.T) {
	inner, err := NewFieldEncryptor(testKey(t))
	require.NoError(t, err)
	r := &RestFieldEncryptor{enc: inner}

	// 1 + 2: Encrypt adds the enc: prefix and Decrypt restores plaintext.
	plain := "ref-token-value"
	enc1, err := r.Encrypt(plain)
	require.NoError(t, err)
	assert.NotEqual(t, plain, enc1)
	assert.True(t, len(enc1) > len(plain))
	dec1, err := r.Decrypt(enc1)
	require.NoError(t, err)
	assert.Equal(t, plain, dec1)

	// 3: Encrypting the ciphertext a second time is a no-op (same value).
	enc2, err := r.Encrypt(enc1)
	require.NoError(t, err)
	assert.Equal(t, enc1, enc2, "double-Encrypt must not double-wrap")

	// 4: Decrypt on a plaintext value (no prefix) returns it unchanged.
	got, err := r.Decrypt("plain-not-prefixed")
	require.NoError(t, err)
	assert.Equal(t, "plain-not-prefixed", got)

	// Empty input passes through both directions cleanly.
	emptyOut, err := r.Encrypt("")
	require.NoError(t, err)
	assert.Equal(t, "", emptyOut)
	emptyOut, err = r.Decrypt("")
	require.NoError(t, err)
	assert.Equal(t, "", emptyOut)
}

// TestRestFieldEncryptor_NilInnerIsPassthrough verifies the dev path
// where ENCRYPTION_KEY is unset and the inner FieldEncryptor is nil:
// Encrypt and Decrypt must both pass through verbatim so the platform
// can still boot for local development.
func TestRestFieldEncryptor_NilInnerIsPassthrough(t *testing.T) {
	r := &RestFieldEncryptor{enc: nil}
	got, err := r.Encrypt("anything")
	require.NoError(t, err)
	assert.Equal(t, "anything", got)
	got, err = r.Decrypt("anything")
	require.NoError(t, err)
	assert.Equal(t, "anything", got)
}

func TestFieldEncryptor_SkipsAlreadyEncrypted(t *testing.T) {
	e, err := NewFieldEncryptor(testKey(t))
	require.NoError(t, err)

	config := map[string]any{"password": "secret"}
	encrypted, err := e.EncryptSensitiveFields(config)
	require.NoError(t, err)

	// Encrypt again — should not double-encrypt
	doubleEncrypted, err := e.EncryptSensitiveFields(encrypted)
	require.NoError(t, err)
	assert.Equal(t, encrypted["password"], doubleEncrypted["password"])

	// Should still decrypt to original
	decrypted, err := e.DecryptSensitiveFields(doubleEncrypted)
	require.NoError(t, err)
	assert.Equal(t, "secret", decrypted["password"])
}

func TestFieldEncryptor_EmptyAndNonStringValues(t *testing.T) {
	e, err := NewFieldEncryptor(testKey(t))
	require.NoError(t, err)

	config := map[string]any{
		"password": "",          // empty string — skip
		"token":    float64(42), // non-string — skip
	}

	encrypted, err := e.EncryptSensitiveFields(config)
	require.NoError(t, err)
	assert.Equal(t, "", encrypted["password"])
	assert.Equal(t, float64(42), encrypted["token"])
}

func TestFieldEncryptor_WrongKeyFailsDecrypt(t *testing.T) {
	e1, err := NewFieldEncryptor(testKey(t))
	require.NoError(t, err)

	e2, err := NewFieldEncryptor(testKey(t))
	require.NoError(t, err)

	config := map[string]any{"password": "secret"}
	encrypted, err := e1.EncryptSensitiveFields(config)
	require.NoError(t, err)

	// Decrypt with wrong key should fail
	_, err = e2.DecryptSensitiveFields(encrypted)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decrypting")
}

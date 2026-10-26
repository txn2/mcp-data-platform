package platform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildFieldEncryptor takes ENCRYPTION_KEY, and the keys it replaced from
// ENCRYPTION_KEY_PREVIOUS, which open what they wrote (#2179).
func TestBuildFieldEncryptor_ReadsThePreviousKeys(t *testing.T) {
	oldKey := strings.Repeat("ab", 32)
	t.Setenv("ENCRYPTION_KEY", oldKey)
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", "")
	before, err := buildFieldEncryptor()
	require.NoError(t, err)
	written, err := before.EncryptSensitiveFields(map[string]any{"password": "s3cret"})
	require.NoError(t, err)

	t.Setenv("ENCRYPTION_KEY", strings.Repeat("cd", 32))
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", oldKey)
	after, err := buildFieldEncryptor()
	require.NoError(t, err)
	read, err := after.DecryptSensitiveFields(written)
	require.NoError(t, err)
	require.Equal(t, "s3cret", read["password"])

	t.Setenv("ENCRYPTION_KEY_PREVIOUS", "short")
	_, err = buildFieldEncryptor()
	require.ErrorContains(t, err, "previous key 1")
}

package webhookwire

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// A webhook source's secret is refused without a key only when the rest
// encryptor would pass it through (#2362).
func TestEncrypts(t *testing.T) {
	assert.False(t, encrypts(nil))
	assert.False(t, encrypts((*fieldcrypt.RestFieldEncryptor)(nil)))
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	fe, err := fieldcrypt.NewFieldEncryptor(key)
	require.NoError(t, err)
	assert.True(t, encrypts(fieldcrypt.NewRestFieldEncryptor(fe)))
}

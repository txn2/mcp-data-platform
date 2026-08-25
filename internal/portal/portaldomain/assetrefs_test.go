package portaldomain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateRefTokenIsAShareStrengthSecret is the property the grant model
// rests on: the URL is the authorization, so the token has to be as unguessable
// as the share link that carries a whole asset.
func TestGenerateRefTokenIsAShareStrengthSecret(t *testing.T) {
	first, err := GenerateRefToken()
	require.NoError(t, err)
	second, err := GenerateRefToken()
	require.NoError(t, err)

	share, err := GenerateShareToken()
	require.NoError(t, err)

	assert.Len(t, first, len(share), "a reference token carries the same 256 bits a share token does")
	assert.NotEqual(t, first, second)
}

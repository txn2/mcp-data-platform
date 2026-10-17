//go:build !windows

package whspool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVolumeOf(t *testing.T) {
	v, err := VolumeOf(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, v.Total)
	assert.LessOrEqual(t, v.Free, v.Total)
	_, err = VolumeOf("/no/such/spool/dir")
	require.Error(t, err)
}

package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFloor(t *testing.T) {
	for _, ok := range []string{"17.0", "17.6", "18.6", "18beta1", "19devel", "17.6 (Debian 17.6-1.pgdg120+1)"} {
		assert.NoError(t, floor(ok), ok)
	}
	err := floor("16.4 (Debian 16.4-1)")
	old, isOld := tooOld(fmt.Errorf("wrapped: %w", err))
	require.True(t, isOld, "the refusal is found through a wrap")
	assert.Equal(t, 16, old.Major)
	assert.Contains(t, err.Error(), "PostgreSQL 16.4")
	assert.Contains(t, err.Error(), "PostgreSQL 17 or later")

	_, isOld = tooOld(errors.New("other"))
	assert.False(t, isOld)
	assert.ErrorContains(t, floor(""), "reading the server's version")
	assert.ErrorContains(t, floor("unknown"), `"unknown"`)
}

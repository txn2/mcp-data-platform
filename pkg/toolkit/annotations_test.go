package toolkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnnotationsToMCP(t *testing.T) {
	tr, fa := true, false

	t.Run("all unset yields empty annotations", func(t *testing.T) {
		ann := AnnotationsToMCP(AnnotationConfig{})
		require.NotNil(t, ann)
		assert.False(t, ann.ReadOnlyHint)
		assert.Nil(t, ann.DestructiveHint)
		assert.False(t, ann.IdempotentHint)
		assert.Nil(t, ann.OpenWorldHint)
	})

	t.Run("set hints are applied", func(t *testing.T) {
		ann := AnnotationsToMCP(AnnotationConfig{
			ReadOnlyHint:    &tr,
			DestructiveHint: &fa,
			IdempotentHint:  &tr,
			OpenWorldHint:   &fa,
		})
		assert.True(t, ann.ReadOnlyHint)
		require.NotNil(t, ann.DestructiveHint)
		assert.False(t, *ann.DestructiveHint)
		assert.True(t, ann.IdempotentHint)
		require.NotNil(t, ann.OpenWorldHint)
		assert.False(t, *ann.OpenWorldHint)
	})
}

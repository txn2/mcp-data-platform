package portalnoop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func TestNoopShareStoreGetActiveShareForTarget(t *testing.T) {
	got, err := NewShareStore().GetActiveShareForTarget(context.Background(), portaldomain.TargetTypeAsset, "a", "u", "e")
	require.NoError(t, err)
	assert.Nil(t, got)
}

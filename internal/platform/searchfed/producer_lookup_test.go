package searchfed

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// producerStore is an asset store that answers the optional producer read.
type producerStore struct {
	portal.AssetStore
	has          bool
	err          error
	gotAsset     string
	gotKind, gID string
}

func (s *producerStore) AssetHasProducer(_ context.Context, assetID, kind, id string) (bool, error) {
	s.gotAsset, s.gotKind, s.gID = assetID, kind, id
	return s.has, s.err
}

// plainStore is an asset store that does not answer it.
type plainStore struct{ portal.AssetStore }

// The lookup a run dereferences its own output through reads the producer
// relation the store already holds, and asks for the SCRIPT kind: an asset a
// person's session wrote carries a producer of another kind, and it is not this
// run's output (#1579).
func TestScriptProducedAsset(t *testing.T) {
	store := &producerStore{has: true}
	lookup := scriptProducedAsset(store)
	require.NotNil(t, lookup)

	assert.True(t, lookup(context.Background(), "a1", "script-uuid"))
	assert.Equal(t, "a1", store.gotAsset)
	assert.Equal(t, producedby.KindScript, store.gotKind)
	assert.Equal(t, "script-uuid", store.gID)

	store.has = false
	assert.False(t, lookup(context.Background(), "a1", "script-uuid"))
}

// A store that cannot answer is treated as "no". The relation is a record of
// what wrote a file, not an authority to grant on a failed read.
func TestScriptProducedAssetFailsClosed(t *testing.T) {
	lookup := scriptProducedAsset(&producerStore{has: true, err: errors.New("connection refused")})
	require.NotNil(t, lookup)
	assert.False(t, lookup(context.Background(), "a1", "script-uuid"))
}

// A store that keeps no producer record binds no lookup, which leaves the
// provider serving exactly what it did before.
func TestScriptProducedAssetAbsentCapability(t *testing.T) {
	assert.Nil(t, scriptProducedAsset(&plainStore{}))
	assert.Nil(t, scriptProducedAsset(nil))
}

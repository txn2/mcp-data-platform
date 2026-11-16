package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// savedStore is the connection store narrowed to appdb, over a map, counting
// its reads.
type savedStore struct {
	mu      sync.Mutex
	configs map[string]map[string]any
	reads   int
	err     error
}

func (s *savedStore) GetConnection(_ context.Context, name string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	c, ok := s.configs[name]
	if !ok {
		return nil, fmt.Errorf("no appdb connection %s: %w", name, ErrConnectionNotFound)
	}
	return c, nil
}

func (s *savedStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// TestServesConnection_TakesOnWhatAnotherReplicaSaved pins #2662: a name this
// replica does not hold is read from the connection store and served, so the
// first call after another replica's save finds it; HasConnection stays this
// replica's live set until then; a name no replica saved is refused, and a
// deployment with no store serves only what it holds.
func TestServesConnection_TakesOnWhatAnotherReplicaSaved(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "a", Instances: map[string]Config{"a": baseConfig()}})
	t.Cleanup(func() { _ = tk.Close() })
	ctx := context.Background()

	assert.False(t, tk.ServesConnection(ctx, "shop"), "no store, nothing to catch up to")

	store := &savedStore{configs: map[string]map[string]any{"shop": validConfigMap()}}
	tk.SetConnectionStore(store)
	assert.False(t, tk.HasConnection("shop"), "the live set does not read the store")
	assert.True(t, tk.ServesConnection(ctx, "shop"), "a connection another replica saved is served")
	assert.True(t, tk.HasConnection("shop"))
	reads := store.readCount()
	assert.True(t, tk.ServesConnection(ctx, "shop"))
	assert.Equal(t, reads, store.readCount(), "a served connection is answered without the store")

	assert.False(t, tk.ServesConnection(ctx, "nowhere"), "a connection no replica saved is still refused")
	assert.True(t, tk.ServesConnection(ctx, "a"), "a configured connection")
}

// TestLookup_FindsAJustSavedConnection pins #2662 for every statement,
// migration and probe the toolkit runs: they find their connection through
// lookup, which takes on a stored connection on a miss.
func TestLookup_FindsAJustSavedConnection(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "a", Instances: map[string]Config{"a": baseConfig()}})
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetConnectionStore(&savedStore{configs: map[string]map[string]any{"shop": validConfigMap()}})

	c, ok := tk.lookup("shop")
	require.True(t, ok)
	assert.Equal(t, "orders", c.cfg.Database)
	_, ok = tk.lookup("nowhere")
	assert.False(t, ok)
}

// TestServesConnection_StoreFailureAndChild: a store that cannot answer
// serves nothing new, and a throwaway database's toolkit, which has no
// resolver, answers from what it holds.
func TestServesConnection_StoreFailureAndChild(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "a", Instances: map[string]Config{"a": baseConfig()}})
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetConnectionStore(&savedStore{err: errors.New("down")})
	assert.False(t, tk.ServesConnection(context.Background(), "shop"))

	child := &Toolkit{connections: map[string]*conn{}}
	assert.False(t, child.ServesConnection(context.Background(), "shop"))
	_, ok := child.lookup("shop")
	assert.False(t, ok)
}

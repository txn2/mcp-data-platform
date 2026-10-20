package trino

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// savedConnections is the connection store: what another replica saved.
type savedConnections struct {
	configs map[string]map[string]any
	reads   atomic.Int32
	err     error
}

func (s *savedConnections) GetConnection(_ context.Context, name string) (map[string]any, error) {
	s.reads.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	cfg, ok := s.configs[name]
	if !ok {
		return nil, ErrConnectionNotFound
	}
	return cfg, nil
}

func catchUpToolkit(t *testing.T) *Toolkit {
	t.Helper()
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]Config{"warehouse": {Host: "trino.example.com", User: "u"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

// A connection saved through another replica has its scratch target, its
// write setting and its client here as soon as the store holds it, before
// the save's announcement arrives (#2218).
func TestACatchUpServesAConnectionAnotherReplicaSaved(t *testing.T) {
	tk := catchUpToolkit(t)
	store := &savedConnections{configs: map[string]map[string]any{
		"lake": {
			"host": "trino.example.com", "user": "u", "read_only": false,
			"scratch": map[string]any{"catalog": "scratch", "schema": "uploads"},
		},
	}}

	_, ok := tk.ScratchTarget("lake")
	assert.False(t, ok, "with no store wired, an unknown connection has no target")

	tk.SetConnectionStore(store)
	target, ok := tk.ScratchTarget("lake")
	require.True(t, ok, "the stored connection's scratch target")
	assert.Equal(t, ScratchConfig{Catalog: "scratch", Schema: "uploads"}, target)
	assert.True(t, tk.HasConnection("lake"))
	assert.True(t, tk.AcceptsWrites("lake"))

	reads := store.reads.Load()
	assert.True(t, tk.ServesConnection(context.Background(), "lake"))
	assert.Equal(t, reads, store.reads.Load(), "a connection held is answered without a read")
}

// A name the store does not hold, a store that cannot answer, and the
// default connection are each answered without serving anything new.
func TestACatchUpLeavesUnknownNamesUnknown(t *testing.T) {
	tk := catchUpToolkit(t)
	tk.SetConnectionStore(&savedConnections{configs: map[string]map[string]any{}})
	_, ok := tk.ScratchTarget("missing")
	assert.False(t, ok)
	assert.False(t, tk.HasConnection("missing"))
	assert.True(t, tk.ServesConnection(context.Background(), ""), "the default connection")

	failing := catchUpToolkit(t)
	failing.SetConnectionStore(&savedConnections{err: errors.New("database unavailable")})
	assert.False(t, failing.AcceptsWrites("lake"))
	assert.False(t, failing.HasConnection("lake"))

	// Exec catches up too, and a connection the store does not hold is
	// refused as unknown rather than run on the default.
	err := tk.Exec(context.Background(), "missing", "CREATE TABLE x (a int)")
	require.Error(t, err)

	// A connection a deletion removed while it was being read is taken back
	// out by the catch-up's own check.
	srv := catchUpServer{t: tk}
	require.NoError(t, srv.Install(context.Background(), "gone", map[string]any{"host": "trino.example.com", "user": "u"}))
	srv.Withdraw("gone")
	assert.False(t, tk.HasConnection("gone"))

	// A toolkit with no catch-up (built without NewMulti) serves only what it
	// holds.
	assert.False(t, (&Toolkit{}).ServesConnection(context.Background(), "lake"))
}

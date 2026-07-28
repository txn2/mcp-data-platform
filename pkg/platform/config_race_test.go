package platform

import (
	"context"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

// mutableOverrideStore is an in-memory OverrideStore whose contents can change
// under concurrent readers, standing in for a database another replica is
// writing to while this process serves requests.
type mutableOverrideStore struct {
	mu      sync.RWMutex
	entries map[string]string
}

func newMutableOverrideStore() *mutableOverrideStore {
	return &mutableOverrideStore{entries: map[string]string{}}
}

func (s *mutableOverrideStore) set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = value
}

func (s *mutableOverrideStore) Get(_ context.Context, key string) (*configstore.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.entries[key]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	return &configstore.Entry{Key: key, Value: v}, nil
}

func (s *mutableOverrideStore) List(_ context.Context) ([]configstore.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]configstore.Entry, 0, len(s.entries))
	for k, v := range s.entries {
		out = append(out, configstore.Entry{Key: k, Value: v})
	}
	return out, nil
}

// TestConfig_ReadThrough_NoRace asserts that the override-backed reads can run
// concurrently with writes to the underlying store without the race detector
// flagging it. These reads sit on tools/list and platform_info, so they run on
// every request goroutine while an admin edit lands in the store.
//
// Run with: go test -race ./pkg/platform/ -run TestConfig_ReadThrough_NoRace.
func TestConfig_ReadThrough_NoRace(_ *testing.T) {
	store := newMutableOverrideStore()
	store.set(ConfigKeyToolsDeny, `["a"]`)
	store.set("tool.trino_query.description", "v1")

	cfg := &Config{}
	cfg.BindOverrideStore(store)

	ctx := context.Background()
	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 200

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				m := cfg.ToolDescriptionOverridesSnapshot(ctx)
				_ = m["trino_query"]
				_ = cfg.ToolsDenySnapshot(ctx)
				_ = cfg.ToolsAllowSnapshot()
				_ = cfg.ServerDescription(ctx)
				_ = cfg.ServerAgentInstructions(ctx)
			}
		})
	}

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				store.set("tool.trino_query.description", "v2")
				store.set(ConfigKeyToolsDeny, `["a","b"]`)
				store.set(ConfigKeyServerDescription, "edited")
			}
		})
	}

	wg.Wait()
}

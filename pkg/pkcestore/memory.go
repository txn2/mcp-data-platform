package pkcestore

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-process map keyed by state token. It sweeps
// expired entries on every put/take AND on a background ticker so an
// idle server still releases stranded oauth-start records.
//
// Single-replica deployments and tests use this directly. Production
// multi-replica deployments use PostgresStore.
type MemoryStore struct {
	mu     sync.Mutex
	states map[string]*State

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewMemoryStore returns a store with a background GC goroutine running
// on gcInterval. Callers MUST Close() it to release the goroutine —
// typically via t.Cleanup() in tests or a deferred close in production
// process teardown.
func NewMemoryStore() *MemoryStore {
	return newMemoryStoreWithInterval(gcInterval)
}

// newMemoryStoreWithInterval is an internal constructor for tests that
// want to drive the GC interval directly (or disable it with 0).
func newMemoryStoreWithInterval(interval time.Duration) *MemoryStore {
	s := &MemoryStore{
		states: map[string]*State{},
		stopCh: make(chan struct{}),
	}
	if interval > 0 {
		go s.gcLoop(interval)
	}
	return s
}

// Put stores a state under the given token, sweeping expired entries
// opportunistically.
func (s *MemoryStore) Put(_ context.Context, state string, val *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.states[state] = val
	return nil
}

// Take returns and deletes the matching state, or ErrStateNotFound if
// absent. Expired entries are GC'd before lookup so callers don't need
// to check TTL themselves.
func (s *MemoryStore) Take(_ context.Context, state string) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	v, ok := s.states[state]
	if !ok {
		return nil, ErrStateNotFound
	}
	delete(s.states, state)
	return v, nil
}

// Close stops the background GC goroutine. Idempotent.
func (s *MemoryStore) Close() error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return nil
}

func (s *MemoryStore) gcLoop(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.mu.Lock()
			s.gcLocked()
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}

func (s *MemoryStore) gcLocked() {
	cutoff := time.Now().Add(-TTL)
	for k, v := range s.states {
		if v.CreatedAt.Before(cutoff) {
			delete(s.states, k)
		}
	}
}

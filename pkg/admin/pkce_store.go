package admin

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrPKCEStateNotFound is returned by PKCEStore.Take when no state row
// matches (or the row had already expired). Callers use errors.Is to
// distinguish "no such pending flow" from a transport/IO error.
var ErrPKCEStateNotFound = errors.New("admin: pkce state not found")

// PKCEStore holds in-flight PKCE state across the oauth-start →
// /oauth/callback round trip. Implementations must be safe for
// concurrent use.
//
// Two implementations ship: an in-memory map (single-replica, default)
// and a Postgres-backed store (multi-replica safe). The Handler picks
// based on whether a database is configured.
type PKCEStore interface {
	// Put stores a state record. Implementations may evict entries that
	// have outlived the platform's pkceTTL on every Put.
	Put(ctx context.Context, state string, val *PKCEState) error

	// Take atomically reads-and-deletes a state record. Returns
	// ErrPKCEStateNotFound when no row matches (or the row had
	// already expired). Other errors indicate transport/IO failure.
	Take(ctx context.Context, state string) (*PKCEState, error)

	// Close releases any background goroutines or DB resources. Safe
	// to call multiple times.
	Close() error
}

// memoryPKCEStore is an in-process map keyed by state token. Sweeps
// expired entries on every put/take AND on a background ticker so an
// idle server still releases stranded oauth-start records.
type memoryPKCEStore struct {
	mu     sync.Mutex
	states map[string]*PKCEState

	stopOnce sync.Once
	stopCh   chan struct{}
}

// newMemoryPKCEStore returns a store with a background GC goroutine
// running on the given interval. Callers must Close() to release it.
func newMemoryPKCEStore(gcInterval time.Duration) *memoryPKCEStore {
	s := &memoryPKCEStore{
		states: map[string]*PKCEState{},
		stopCh: make(chan struct{}),
	}
	if gcInterval > 0 {
		go s.gcLoop(gcInterval)
	}
	return s
}

// Put stores a state under the given token, sweeping expired entries
// opportunistically.
func (s *memoryPKCEStore) Put(_ context.Context, state string, val *PKCEState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.states[state] = val
	return nil
}

// Take returns and deletes the matching state, or ErrPKCEStateNotFound
// if absent. Expired entries are GC'd before lookup so callers don't
// need to check pkceTTL themselves.
func (s *memoryPKCEStore) Take(_ context.Context, state string) (*PKCEState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	v, ok := s.states[state]
	if !ok {
		return nil, ErrPKCEStateNotFound
	}
	delete(s.states, state)
	return v, nil
}

// Close stops the background GC goroutine. Idempotent.
func (s *memoryPKCEStore) Close() error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return nil
}

func (s *memoryPKCEStore) gcLoop(interval time.Duration) {
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

func (s *memoryPKCEStore) gcLocked() {
	cutoff := time.Now().Add(-pkceTTL)
	for k, v := range s.states {
		if v.createdAt.Before(cutoff) {
			delete(s.states, k)
		}
	}
}

// pkceGCInterval is how often the in-memory store sweeps stranded
// oauth-start entries on its own (separate from opportunistic
// put/take sweeps). One minute keeps the leak ceiling tight without
// being chatty for a feature that's idle most of the time.
const pkceGCInterval = 1 * time.Minute

// noopPKCEStoreLogger lets us silently swallow the rare Close error
// in test paths where teardown order doesn't care.
func noopCloseLog(err error) {
	if err != nil {
		slog.Debug("pkce_store: close error", "err", err)
	}
}

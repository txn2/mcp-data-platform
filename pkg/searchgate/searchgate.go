// Package searchgate stores, per session, whether a discovery tool has been
// called — the signal the search-first gate (middleware.MCPWorkflowGateMiddleware)
// blocks query tools on. It exposes a small Store interface with an in-memory
// default and a PostgreSQL implementation (pkg/searchgate/postgres) so the
// signal is shared across replicas, the same way sessions and audit externalize.
//
// The in-memory store is correct for single-replica / no-database deployments.
// A multi-replica deployment must use a shared store: otherwise a discovery
// recorded on one replica is invisible to a query handled by another, and the
// hard gate refuses the query even though the agent did call search (#789).
package searchgate

import (
	"context"
	"sync"
	"time"
)

// Store records and reports per-session discovery. Discovery is monotonic:
// once a session has performed discovery, it stays discovered until its entry
// expires (bounded by the session timeout).
type Store interface {
	// MarkDiscovered records that the session has performed discovery.
	MarkDiscovered(ctx context.Context, sessionID string) error

	// HasDiscovered reports whether the session has performed discovery and
	// the record has not expired.
	HasDiscovered(ctx context.Context, sessionID string) (bool, error)

	// Cleanup evicts expired entries.
	Cleanup(ctx context.Context) error

	// Close releases any resources held by the store.
	Close() error
}

// MemoryStore is an in-memory Store. It is correct for single-replica or
// no-database deployments; it is NOT shared across replicas.
type MemoryStore struct {
	mu  sync.RWMutex
	ttl time.Duration
	m   map[string]time.Time // sessionID -> expiry
}

// NewMemoryStore creates an in-memory discovery store. Entries expire ttl after
// the most recent MarkDiscovered.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	return &MemoryStore{ttl: ttl, m: make(map[string]time.Time)}
}

// MarkDiscovered records discovery for the session, (re)setting its expiry.
func (s *MemoryStore) MarkDiscovered(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sessionID] = time.Now().Add(s.ttl)
	return nil
}

// HasDiscovered reports whether the session has a live discovery record.
func (s *MemoryStore) HasDiscovered(_ context.Context, sessionID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	exp, ok := s.m[sessionID]
	if !ok {
		return false, nil
	}
	return time.Now().Before(exp), nil
}

// Cleanup removes expired entries.
func (s *MemoryStore) Cleanup(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, exp := range s.m {
		if now.After(exp) {
			delete(s.m, id)
		}
	}
	return nil
}

// Close is a no-op for the in-memory store.
func (*MemoryStore) Close() error { return nil }

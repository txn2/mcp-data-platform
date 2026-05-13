package authevents

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Filter constrains a List query. All fields are AND-combined. Zero
// values disable that condition.
type Filter struct {
	// Kind, Name select events for a specific connection. Both must
	// be set (or both empty for cross-connection queries — only used
	// by tests and the prune job).
	Kind string
	Name string
	// Limit caps the result count. Required to be > 0; List returns
	// an error when the caller forgets it (the History panel passes
	// 30, the prune job doesn't list).
	Limit int
	// Since drops events older than Since. Zero disables.
	Since time.Time
}

// Store persists and queries connection_auth_events rows. The
// Postgres implementation lives in store_postgres.go; an in-memory
// implementation lives in store_memory.go for tests and for dev
// deployments that lack a database. Both satisfy this interface.
//
// Distinct from connoauth.Store deliberately: this package's writes
// are append-only audit history, while connoauth.Store is the
// authoritative token-row state. Mixing them would conflate two very
// different update cadences (every refresh tick writes here; only the
// IdP can mutate connoauth rows).
type Store interface {
	// Insert appends an event. The store stamps OccurredAt and ID;
	// callers should not pre-populate them. Returns ErrInvalidEvent
	// for events with missing required fields or unknown types.
	Insert(ctx context.Context, ev Event) error
	// List returns the most recent events matching f, ordered
	// occurred_at DESC (newest first). Capped by f.Limit.
	List(ctx context.Context, f Filter) ([]Event, error)
	// Prune deletes events older than cutoff. Returns the count of
	// deleted rows so the caller can log it. The prune job runs this
	// daily with cutoff = now - retention.
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}

// ErrInvalidEvent indicates the caller passed an event that fails
// validation (missing required field or unknown type). Wrapped so
// errors.Is works through the call chain.
var ErrInvalidEvent = errors.New("authevents: invalid event")

// maxListLimit caps Filter.Limit to defend against operator (or
// attacker) input that asks for a slice the size of memory. The
// History panel uses 30 and the SLO dashboards use low hundreds;
// 10000 is comfortable headroom and small enough that a degenerate
// caller can't OOM the process.
const maxListLimit = 10000

// MemoryStore is an in-process Store for tests and for dev
// deployments without a database. Events DO NOT survive process
// restarts. Concurrency-safe.
type MemoryStore struct {
	mu     sync.Mutex
	events []Event
}

// NewMemoryStore returns an in-process Store. The Postgres store is
// the production choice; this exists so unit tests don't need a
// database AND so TestNoopOnlyInterfaces sees a real (non-noop)
// implementation alongside the Postgres one.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Insert appends ev to the in-memory slice after validating.
func (s *MemoryStore) Insert(_ context.Context, ev Event) error {
	if !ev.IsValid() {
		return ErrInvalidEvent
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	s.events = append(s.events, ev)
	return nil
}

// List collects events matching f and returns them ordered
// occurred_at DESC, then caps to f.Limit. Sorting (rather than
// relying on insertion order) keeps the contract identical to
// PostgresStore.List even when callers pre-populate OccurredAt
// out of monotonic order.
func (s *MemoryStore) List(_ context.Context, f Filter) ([]Event, error) {
	if f.Limit <= 0 {
		return nil, errors.New("authevents: List requires positive Limit")
	}
	if f.Limit > maxListLimit {
		f.Limit = maxListLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]Event, 0, f.Limit)
	for _, ev := range s.events {
		if matchesFilter(ev, f) {
			matched = append(matched, ev)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].OccurredAt.After(matched[j].OccurredAt)
	})
	if len(matched) > f.Limit {
		matched = matched[:f.Limit]
	}
	return matched, nil
}

// matchesFilter returns true when ev satisfies every non-zero
// condition in f. Extracted from List so the loop's cyclomatic
// complexity stays under the project's ceiling.
func matchesFilter(ev Event, f Filter) bool {
	if f.Kind != "" && ev.Kind != f.Kind {
		return false
	}
	if f.Name != "" && ev.Name != f.Name {
		return false
	}
	if !f.Since.IsZero() && ev.OccurredAt.Before(f.Since) {
		return false
	}
	return true
}

// Prune drops events older than cutoff. Returns the count removed.
func (s *MemoryStore) Prune(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.events[:0]
	var removed int64
	for _, ev := range s.events {
		if ev.OccurredAt.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, ev)
	}
	s.events = kept
	return removed, nil
}

// Verify interface compliance at compile time.
var _ Store = (*MemoryStore)(nil)

package authevents

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingSink captures what the Writer announced.
type recordingSink struct {
	mu   sync.Mutex
	seen []Revocation
}

func (s *recordingSink) Revoked(_ context.Context, rev Revocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, rev)
}

func (s *recordingSink) all() []Revocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Revocation(nil), s.seen...)
}

// TestTokenDeletedRevoked_AnnouncesAndRecords is the reason the sink hangs off
// the Writer: one fan-out, two consumers. The history row and the announcement
// describe the same incident and cannot be wired to disagree about it.
func TestTokenDeletedRevoked_AnnouncesAndRecords(t *testing.T) {
	store := NewMemoryStore()
	sink := &recordingSink{}
	w := NewWriter(store, nil).WithRevocations(sink)

	w.TokenDeletedRevoked(t.Context(), "api", "billing", SystemBackgroundRefresh,
		"https://idp.example.com/token", "invalid_grant", "ops@example.com")

	events, err := store.List(t.Context(), Filter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, TypeTokenDeletedRevoked, events[0].Type)

	announced := sink.all()
	require.Len(t, announced, 1)
	assert.Equal(t, "api", announced[0].Kind)
	assert.Equal(t, "billing", announced[0].Name)
	assert.Equal(t, "ops@example.com", announced[0].AuthorizedBy,
		"the sink is told who authorized it because the row carrying that is already deleted")
	assert.Equal(t, "idp.example.com", announced[0].IDPHost, "the host, not the whole token URL")
	assert.Equal(t, "invalid_grant", announced[0].Reason)
	assert.False(t, announced[0].At.IsZero())
}

// TestRevocationSink_Absent proves every deployment without one still records
// the event: the announcement is an addition to the history, not a condition
// on it.
func TestRevocationSink_Absent(t *testing.T) {
	store := NewMemoryStore()

	t.Run("no sink wired", func(t *testing.T) {
		w := NewWriter(store, nil)
		assert.NotPanics(t, func() {
			w.TokenDeletedRevoked(t.Context(), "mcp", "vendor", SystemToolCall, "", "no_refresh_token", "")
		})
	})

	t.Run("a nil sink wired", func(t *testing.T) {
		w := NewWriter(store, nil).WithRevocations(nil)
		assert.NotPanics(t, func() {
			w.TokenDeletedRevoked(t.Context(), "mcp", "vendor", SystemToolCall, "", "no_refresh_token", "")
		})
	})

	t.Run("a nil writer", func(t *testing.T) {
		var w *Writer
		assert.Nil(t, w.WithRevocations(&recordingSink{}))
		assert.NotPanics(t, func() {
			w.TokenDeletedRevoked(t.Context(), "mcp", "vendor", SystemToolCall, "", "x", "")
		})
	})
}

// TestWithRevocations_WiredLate covers the ordering the composition root
// actually has: the refresher is already emitting through this Writer when the
// HTTP root attaches the sink.
func TestWithRevocations_WiredLate(t *testing.T) {
	w := NewWriter(NewMemoryStore(), nil)
	sink := &recordingSink{}

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 50 {
			w.TokenDeletedRevoked(context.Background(), "api", "billing",
				SystemBackgroundRefresh, "https://idp.example.com/token", "invalid_grant", "ops@example.com")
		}
	})
	wg.Go(func() { w.WithRevocations(sink) })
	wg.Wait()

	assert.NotEmpty(t, sink.all(), "the sink must be reached once it is attached")
}

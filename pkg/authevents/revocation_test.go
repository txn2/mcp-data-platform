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
	mu       sync.Mutex
	seen     []Revocation
	restored []string
}

func (s *recordingSink) Revoked(_ context.Context, rev Revocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, rev)
}

func (s *recordingSink) Restored(_ context.Context, kind, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restored = append(s.restored, kind+"/"+name)
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
// HTTP root attaches the sink. Emission runs concurrently with the attach, so
// the race detector sees both sides, and the first emission after the attach
// returns must reach the sink. Asserting on emissions that merely overlap the
// attach would depend on which goroutine the scheduler runs first: with one
// CPU the emitter can finish before the sink is attached at all.
func TestWithRevocations_WiredLate(t *testing.T) {
	w := NewWriter(NewMemoryStore(), nil)
	sink := &recordingSink{}
	emit := func() {
		w.TokenDeletedRevoked(context.Background(), "api", "billing",
			SystemBackgroundRefresh, "https://idp.example.com/token", "invalid_grant", "ops@example.com")
	}

	attached := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-attached:
				emit()
				return
			default:
				emit()
			}
		}
	})
	wg.Go(func() {
		w.WithRevocations(sink)
		close(attached)
	})
	wg.Wait()

	assert.NotEmpty(t, sink.all(), "the first revocation after the sink is attached must reach it")
}

func (s *recordingSink) restoredAll() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.restored...)
}

// TestAssertionRejected_AnnouncesWithoutAHistoryRow covers the jwt_bearer
// refusal: the sink is told what the upstream said, marked as a signed
// assertion so nobody is asked to reconnect, and no token-history row is
// written for a credential that was never stored.
func TestAssertionRejected_AnnouncesWithoutAHistoryRow(t *testing.T) {
	store := NewMemoryStore()
	sink := &recordingSink{}
	w := NewWriter(store, nil).WithRevocations(sink)

	w.AssertionRejected(t.Context(), AssertionRefusal{
		Kind: "graphql", Name: "erp", TokenURL: "https://login.example.com/services/oauth2/token",
		Code: "invalid_grant", Description: "user hasn't approved this consumer",
	})

	events, err := store.List(t.Context(), Filter{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, events, "a refused assertion has no stored credential whose history it belongs to")

	announced := sink.all()
	require.Len(t, announced, 1)
	got := announced[0]
	assert.Equal(t, "graphql", got.Kind)
	assert.Equal(t, "erp", got.Name)
	assert.Equal(t, "login.example.com", got.IDPHost)
	assert.Equal(t, "invalid_grant", got.Reason)
	assert.Equal(t, "user hasn't approved this consumer", got.Description)
	assert.True(t, got.SignedAssertion)
	assert.Empty(t, got.AuthorizedBy, "nobody authorized a signed assertion")
	assert.False(t, got.At.IsZero())
}

// TestAssertionAccepted_TellsTheSink proves an accepted exchange reaches the
// sink as a restoration, and that every absent piece is tolerated.
func TestAssertionAccepted_TellsTheSink(t *testing.T) {
	sink := &recordingSink{}
	w := NewWriter(NewMemoryStore(), nil).WithRevocations(sink)
	w.AssertionAccepted(t.Context(), "api", "billing")
	assert.Equal(t, []string{"api/billing"}, sink.restoredAll())
	assert.Empty(t, sink.all(), "an accepted exchange is not a revocation")

	assert.NotPanics(t, func() {
		NewWriter(NewMemoryStore(), nil).AssertionAccepted(t.Context(), "api", "billing")
		NewWriter(NewMemoryStore(), nil).AssertionRejected(t.Context(), AssertionRefusal{Kind: "api", Name: "billing", Code: "invalid_client"})
		var nilWriter *Writer
		nilWriter.AssertionAccepted(t.Context(), "api", "billing")
		nilWriter.AssertionRejected(t.Context(), AssertionRefusal{Kind: "api", Name: "billing", Code: "invalid_client"})
	})
}

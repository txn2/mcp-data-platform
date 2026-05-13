package connoauth

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

func TestClassifyRevokedReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{errRefreshTokenRevoked, "invalid_grant"},
		{errNoRefreshToken, "no_refresh_token"},
		{errRefreshExpired, "refresh_expired"},
		{errors.New("other"), "revoked"},
	}
	for _, tc := range cases {
		got := classifyRevokedReason(tc.err)
		if got != tc.want {
			t.Errorf("classifyRevokedReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestHandleRevokedEmitsEventsAndDeletes(t *testing.T) {
	t.Parallel()
	tokenStore := NewMemoryStore()
	eventStore := authevents.NewMemoryStore()
	writer := authevents.NewWriter(eventStore, nil)
	key := Key{Kind: KindMCP, Name: "alpha"}
	persisted := PersistedToken{
		Key: key, AccessToken: "at", RefreshToken: "rt",
	}
	if err := tokenStore.Set(context.Background(), persisted); err != nil {
		t.Fatalf("Set: %v", err)
	}
	src := NewSource(tokenStore, key, Config{TokenURL: "https://idp/token"}).
		WithEvents(writer).
		WithActor(authevents.SystemBackgroundRefresh)
	src.handleRevoked(context.Background(), &persisted, errRefreshTokenRevoked)
	// Token row is gone.
	if _, err := tokenStore.Get(context.Background(), key); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("token row should be gone, got %v", err)
	}
	// Two events emitted in order: refresh_failed_revoked, token_deleted_revoked.
	got, _ := eventStore.List(context.Background(),
		authevents.Filter{Kind: key.Kind, Name: key.Name, Limit: 10})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
	}
	// Newest first ordering.
	if got[0].Type != authevents.TypeTokenDeletedRevoked {
		t.Errorf("got[0].Type = %v, want token_deleted_revoked", got[0].Type)
	}
	if got[1].Type != authevents.TypeRefreshFailedRevoked {
		t.Errorf("got[1].Type = %v, want refresh_failed_revoked", got[1].Type)
	}
	if got[0].Actor != authevents.SystemBackgroundRefresh {
		t.Errorf("actor = %q, want %q", got[0].Actor, authevents.SystemBackgroundRefresh)
	}
}

// TestRefreshSkippedNoTokenSilent: when refresh() encounters an empty
// RefreshToken, it returns errNoRefreshToken WITHOUT emitting its own
// event — the caller's handleRevoked path records the cause via
// IDPErrorCode="no_refresh_token" on the RefreshFailedRevoked +
// TokenDeletedRevoked pair. This avoids triple-emission for a
// single operator-visible incident.
func TestRefreshSkippedNoTokenSilent(t *testing.T) {
	t.Parallel()
	tokenStore := NewMemoryStore()
	eventStore := authevents.NewMemoryStore()
	writer := authevents.NewWriter(eventStore, nil)
	key := Key{Kind: KindMCP, Name: "beta"}
	src := NewSource(tokenStore, key, Config{TokenURL: "https://idp/token"}).WithEvents(writer)
	if _, err := src.refresh(context.Background(), &PersistedToken{Key: key}); !errors.Is(err, errNoRefreshToken) {
		t.Fatalf("expected errNoRefreshToken, got %v", err)
	}
	got, _ := eventStore.List(context.Background(), authevents.Filter{
		Kind: key.Kind, Name: key.Name, Limit: 5,
	})
	if len(got) != 0 {
		t.Errorf("refresh() should not emit any event for empty RefreshToken; got %+v", got)
	}
}

func TestSourceWithActorAcceptsEmpty(t *testing.T) {
	t.Parallel()
	src := NewSource(NewMemoryStore(), Key{Kind: KindMCP, Name: "x"}, Config{})
	if src.actor != authevents.SystemToolCall {
		t.Fatalf("default actor = %q, want %q", src.actor, authevents.SystemToolCall)
	}
	src.WithActor("")
	if src.actor != authevents.SystemToolCall {
		t.Fatalf("WithActor(empty) should not clobber default; got %q", src.actor)
	}
	src.WithActor("custom")
	if src.actor != "custom" {
		t.Fatalf("WithActor(custom) failed; got %q", src.actor)
	}
}

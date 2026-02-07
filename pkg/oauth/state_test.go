package oauth

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryStateStore(t *testing.T) {
	store := NewMemoryStateStore()

	state := &AuthorizationState{
		ClientID:            "client-123",
		RedirectURI:         "http://localhost:8080/callback",
		State:               "client-state",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		Scope:               "read",
		UpstreamState:       "upstream-state",
		CreatedAt:           time.Now(),
	}

	t.Run("save state", func(t *testing.T) {
		err := store.Save("key-1", state)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("get state", func(t *testing.T) {
		got, err := store.Get("key-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ClientID != state.ClientID {
			t.Errorf("expected client_id %q, got %q", state.ClientID, got.ClientID)
		}
		if got.State != state.State {
			t.Errorf("expected state %q, got %q", state.State, got.State)
		}
	})

	t.Run("get nonexistent state", func(t *testing.T) {
		_, err := store.Get("nonexistent")
		if !errors.Is(err, ErrStateNotFound) {
			t.Errorf("expected ErrStateNotFound, got %v", err)
		}
	})

	t.Run("delete state", func(t *testing.T) {
		err := store.Delete("key-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = store.Get("key-1")
		if !errors.Is(err, ErrStateNotFound) {
			t.Error("expected state to be deleted")
		}
	})

	t.Run("cleanup old states", func(t *testing.T) {
		// Add old state
		oldState := &AuthorizationState{
			ClientID:  "old-client",
			CreatedAt: time.Now().Add(-time.Hour),
		}
		_ = store.Save("old-key", oldState)

		// Add new state
		newState := &AuthorizationState{
			ClientID:  "new-client",
			CreatedAt: time.Now(),
		}
		_ = store.Save("new-key", newState)

		// Cleanup states older than 30 minutes
		err := store.Cleanup(30 * time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Old should be gone
		_, err = store.Get("old-key")
		if !errors.Is(err, ErrStateNotFound) {
			t.Error("expected old state to be cleaned up")
		}

		// New should remain
		_, err = store.Get("new-key")
		if err != nil {
			t.Error("expected new state to remain")
		}
	})
}

// Verify MemoryStateStore implements StateStore.
var _ StateStore = (*MemoryStateStore)(nil)

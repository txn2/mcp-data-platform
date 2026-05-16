package connoauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKey_IsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		k    Key
		want bool
	}{
		{"zero", Key{}, false},
		{"kind only", Key{Kind: KindMCP}, false},
		{"name only", Key{Name: "test"}, false},
		{"populated", Key{Kind: KindMCP, Name: "test"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.k.IsValid(); got != tc.want {
				t.Fatalf("IsValid = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryStore_RoundTrip(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	key := Key{Kind: KindMCP, Name: "alpha"}
	now := time.Now().Truncate(time.Second).UTC()
	tok := PersistedToken{
		Key:              key,
		AccessToken:      "access",
		RefreshToken:     "refresh",
		ExpiresAt:        now.Add(time.Hour),
		RefreshExpiresAt: now.Add(24 * time.Hour),
		Scope:            "openid offline_access",
		AuthenticatedBy:  "user@example.com",
		AuthenticatedAt:  now,
	}
	if err := s.Set(ctx, tok); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "access" || got.RefreshToken != "refresh" {
		t.Fatalf("token round-trip mismatch: %+v", got)
	}
	if got.Scope != "openid offline_access" {
		t.Fatalf("scope mismatch: %q", got.Scope)
	}
	if got.AuthenticatedBy != "user@example.com" {
		t.Fatalf("authenticated_by mismatch: %q", got.AuthenticatedBy)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt should be stamped on Set")
	}
}

func TestMemoryStore_GetMissing(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	_, err := s.Get(context.Background(), Key{Kind: KindAPI, Name: "missing"})
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	key := Key{Kind: KindMCP, Name: "to-delete"}
	_ = s.Set(ctx, PersistedToken{Key: key, AccessToken: "x"})
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound after delete, got %v", err)
	}
	// Idempotent: second delete is fine.
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
}

func TestMemoryStore_InvalidKey(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	if _, err := s.Get(ctx, Key{}); !errors.Is(err, errInvalidKey) {
		t.Fatalf("Get with zero key: want errInvalidKey, got %v", err)
	}
	if err := s.Set(ctx, PersistedToken{}); !errors.Is(err, errInvalidKey) {
		t.Fatalf("Set with zero key: want errInvalidKey, got %v", err)
	}
	if err := s.Delete(ctx, Key{}); !errors.Is(err, errInvalidKey) {
		t.Fatalf("Delete with zero key: want errInvalidKey, got %v", err)
	}
}

func TestMemoryStore_TwoKindsCoexist(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	mcpKey := Key{Kind: KindMCP, Name: "alpha"}
	apiKey := Key{Kind: KindAPI, Name: "alpha"} // same name, different kind
	_ = s.Set(ctx, PersistedToken{Key: mcpKey, AccessToken: "mcp-token"})
	_ = s.Set(ctx, PersistedToken{Key: apiKey, AccessToken: "api-token"})
	mcpGot, _ := s.Get(ctx, mcpKey)
	apiGot, _ := s.Get(ctx, apiKey)
	if mcpGot.AccessToken != "mcp-token" || apiGot.AccessToken != "api-token" {
		t.Fatalf("kinds should not collide: mcp=%q api=%q", mcpGot.AccessToken, apiGot.AccessToken)
	}
}

func TestNoopEncryptor(t *testing.T) {
	t.Parallel()
	e := noopEncryptor{}
	enc, err := e.Encrypt("plain")
	if err != nil || enc != "plain" {
		t.Fatalf("Encrypt: got (%q, %v)", enc, err)
	}
	dec, err := e.Decrypt("cipher")
	if err != nil || dec != "cipher" {
		t.Fatalf("Decrypt: got (%q, %v)", dec, err)
	}
}

func TestMemoryStore_Lock_Serializes(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	key := Key{Kind: KindAPI, Name: "race"}

	// First Lock acquires immediately.
	release1, err := store.Lock(context.Background(), key)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}

	// Second Lock for the same key must block until release1 fires.
	type lockResult struct {
		release func()
		err     error
	}
	ch := make(chan lockResult, 1)
	go func() {
		r, err := store.Lock(context.Background(), key)
		ch <- lockResult{r, err}
	}()

	select {
	case <-ch:
		t.Fatal("second Lock should block while first holds the key")
	case <-time.After(50 * time.Millisecond):
		// expected: blocked
	}

	release1()

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("second Lock: %v", res.err)
		}
		res.release()
	case <-time.After(time.Second):
		t.Fatal("second Lock did not acquire after first release")
	}
}

func TestMemoryStore_Lock_DifferentKeysDoNotBlock(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()

	releaseA, err := store.Lock(context.Background(), Key{Kind: KindAPI, Name: "a"})
	if err != nil {
		t.Fatalf("Lock A: %v", err)
	}
	defer releaseA()

	// A different key must NOT block on A's lock.
	done := make(chan error, 1)
	go func() {
		r, err := store.Lock(context.Background(), Key{Kind: KindAPI, Name: "b"})
		if r != nil {
			r()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Lock B: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Lock B should not block on Lock A (different key)")
	}
}

func TestMemoryStore_Lock_ReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	key := Key{Kind: KindAPI, Name: "x"}
	release, err := store.Lock(context.Background(), key)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	// Calling release twice MUST NOT panic from a double-unlock of
	// the underlying sync.Mutex.
	release()
	release()
}

func TestMemoryStore_Lock_RejectsInvalidKey(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	if _, err := store.Lock(context.Background(), Key{}); !errors.Is(err, errInvalidKey) {
		t.Fatalf("Lock with empty key should return errInvalidKey, got %v", err)
	}
}

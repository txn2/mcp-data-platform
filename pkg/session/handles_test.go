package session

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGenerateHandle(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		h, err := GenerateHandle()
		if err != nil {
			t.Fatalf("GenerateHandle: %v", err)
		}
		if !strings.HasPrefix(h, HandlePrefix) {
			t.Fatalf("handle %q missing prefix %q", h, HandlePrefix)
		}
		// dps_ (4) + 32 hex chars for 16 random bytes.
		if want := len(HandlePrefix) + 32; len(h) != want {
			t.Fatalf("handle %q length = %d, want %d", h, len(h), want)
		}
		if seen[h] {
			t.Fatalf("duplicate handle generated: %q", h)
		}
		seen[h] = true
		if !IsHandle(h) {
			t.Fatalf("IsHandle(%q) = false, want true", h)
		}
	}
}

func TestIsHandle(t *testing.T) {
	cases := map[string]bool{
		"dps_abc123":                      true,
		"dps_":                            true,
		"":                                false,
		"9f2c41e7a8b34d6c":                false, // legacy transport ID (bare hex)
		"stdio":                           false,
		"DPS_uppercaseprefixdoesnotmatch": false,
	}
	for id, want := range cases {
		if got := IsHandle(id); got != want {
			t.Errorf("IsHandle(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestMintHandle(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	sess, err := MintHandle(context.Background(), store, "user-1", "analyst", 2*time.Hour)
	if err != nil {
		t.Fatalf("MintHandle: %v", err)
	}
	if !IsHandle(sess.ID) {
		t.Errorf("minted ID %q is not a handle", sess.ID)
	}
	if sess.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", sess.UserID)
	}
	if sess.State[StateKeyMintedBy] != MintedByPlatformInfo {
		t.Errorf("minted_by = %v, want %q", sess.State[StateKeyMintedBy], MintedByPlatformInfo)
	}
	if sess.State[StateKeyPersona] != "analyst" {
		t.Errorf("persona = %v, want analyst", sess.State[StateKeyPersona])
	}
	if !sess.ExpiresAt.After(time.Now().Add(time.Hour)) {
		t.Errorf("ExpiresAt %v should honor the 2h TTL", sess.ExpiresAt)
	}

	// The session is retrievable from the store.
	got, err := store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if got == nil || got.ID != sess.ID {
		t.Fatal("minted handle not persisted")
	}
}

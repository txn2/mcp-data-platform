package signkey

import (
	"bytes"
	"testing"
)

func TestKeyID(t *testing.T) {
	key := []byte("test-signing-key-at-least-32-bytes-long")

	// Deterministic: same input yields the same kid across calls.
	if got, want := KeyID(key), KeyID(key); got != want {
		t.Fatalf("KeyID not deterministic: %q vs %q", got, want)
	}

	// 8 bytes rendered as hex is 16 characters.
	if got := KeyID(key); len(got) != 16 {
		t.Errorf("KeyID length = %d, want 16 (%q)", len(got), got)
	}

	// Distinct keys yield distinct kids.
	if KeyID(key) == KeyID([]byte("another-signing-key-at-least-32-bytes")) {
		t.Error("distinct keys produced the same kid")
	}
}

func TestNewRing_NilOnEmptyCurrent(t *testing.T) {
	if r := NewRing(nil, nil); r != nil {
		t.Errorf("NewRing(nil, nil) = %v, want nil", r)
	}
	if r := NewRing([]byte{}, [][]byte{[]byte("prev")}); r != nil {
		t.Error("NewRing with empty current should be nil regardless of previous keys")
	}
}

func TestRing_VerificationKey(t *testing.T) {
	current := []byte("current-signing-key-at-least-32-bytes")
	prev := []byte("previous-signing-key-at-least-32-byte")
	r := NewRing(current, [][]byte{prev})

	t.Run("current kid resolves", func(t *testing.T) {
		key, ok := r.VerificationKey(KeyID(current))
		if !ok || !bytes.Equal(key, current) {
			t.Errorf("VerificationKey(current kid) = %q, %v", key, ok)
		}
	})

	t.Run("previous kid resolves", func(t *testing.T) {
		key, ok := r.VerificationKey(KeyID(prev))
		if !ok || !bytes.Equal(key, prev) {
			t.Errorf("VerificationKey(prev kid) = %q, %v", key, ok)
		}
	})

	t.Run("unknown kid rejected", func(t *testing.T) {
		if _, ok := r.VerificationKey("deadbeefdeadbeef"); ok {
			t.Error("VerificationKey(unknown) returned ok=true")
		}
	})
}

func TestRing_CandidateKeys(t *testing.T) {
	current := []byte("current-signing-key-at-least-32-bytes")
	prev1 := []byte("previous-one-signing-key-32-bytes-long")
	prev2 := []byte("previous-two-signing-key-32-bytes-long")
	r := NewRing(current, [][]byte{prev1, prev2})

	got := r.CandidateKeys()
	if len(got) != 3 {
		t.Fatalf("CandidateKeys len = %d, want 3", len(got))
	}
	if !bytes.Equal(got[0], current) {
		t.Error("CandidateKeys[0] must be the current key")
	}
}

func TestNewRing_DedupesAndSkipsEmpty(t *testing.T) {
	current := []byte("current-signing-key-at-least-32-bytes")
	// A previous key equal to current (same kid) is deduplicated, and empty
	// entries are skipped.
	r := NewRing(current, [][]byte{current, {}, []byte("other-key-at-least-32-bytes-padding")})

	got := r.CandidateKeys()
	if len(got) != 2 {
		t.Fatalf("CandidateKeys len = %d, want 2 (current + one distinct previous)", len(got))
	}
}

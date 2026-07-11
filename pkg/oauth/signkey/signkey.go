// Package signkey manages the HMAC key ring used to sign and verify HS256 JWT
// access tokens. It derives a stable key id (kid) from a raw key, selects the
// active signing key, and resolves a verification key by kid across the current
// key plus verify-only previous keys so a signing-key rotation does not
// invalidate live sessions.
//
// The kid derivation is deterministic and process-independent: every replica
// computes the same kid for the same key, which is what lets a token signed by
// one replica verify on another after a rolling key rotation.
package signkey

import (
	"crypto/sha256"
	"encoding/hex"
)

// kidBytes is the number of leading SHA-256 digest bytes rendered as hex to form
// a key id. Eight bytes (16 hex chars) is ample to distinguish the small set of
// keys in a rotation window while keeping the header compact.
const kidBytes = 8

// KeyID derives the kid header value for a raw HMAC key as the hex encoding of
// the first kidBytes bytes of its SHA-256 digest. It is stable across processes
// so every replica computes the same kid for the same key.
func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:kidBytes])
}

// Ring resolves a verification key for a token by its kid header across the
// current signing key plus verify-only previous keys, and yields the ordered
// candidate list for legacy no-kid tokens. A Ring is immutable after
// construction and safe for concurrent use.
//
// The Ring is used by the resource-server (verification) side. The
// authorization-server (signing) side stamps its kid with KeyID directly, since
// it only ever signs with the single active key.
type Ring struct {
	byKID   map[string][]byte
	ordered [][]byte
}

// NewRing builds a Ring from the current signing key and zero or more
// verify-only previous keys. Empty previous keys are skipped, and a previous key
// equal to the current key (same kid) is deduplicated. NewRing returns nil when
// current is empty, which signals opaque-token mode (no HS256 signing key
// configured); callers must nil-check before use.
func NewRing(current []byte, previous [][]byte) *Ring {
	if len(current) == 0 {
		return nil
	}
	r := &Ring{
		byKID:   make(map[string][]byte, 1+len(previous)),
		ordered: make([][]byte, 0, 1+len(previous)),
	}
	r.add(current)
	for _, p := range previous {
		r.add(p)
	}
	return r
}

// add registers a key under its kid, skipping empty keys and duplicates so the
// ordered candidate list stays free of repeats (e.g. current listed again in
// previous_signing_keys).
func (r *Ring) add(key []byte) {
	if len(key) == 0 {
		return
	}
	kid := KeyID(key)
	if _, exists := r.byKID[kid]; exists {
		return
	}
	r.byKID[kid] = key
	r.ordered = append(r.ordered, key)
}

// VerificationKey resolves the key to verify a token by its kid header. A token
// whose kid is not in the ring returns ok=false and must be rejected: an unknown
// kid means the key was retired (dropped from previous_signing_keys) or the
// token was minted by a foreign issuer.
func (r *Ring) VerificationKey(kid string) (key []byte, ok bool) {
	k, ok := r.byKID[kid]
	return k, ok
}

// CandidateKeys returns every key (current first, then previous) for verifying a
// legacy token that carries no kid header. Such tokens were issued before kid
// support existed; callers try each key in order so those live sessions survive
// the upgrade.
func (r *Ring) CandidateKeys() [][]byte {
	return r.ordered
}

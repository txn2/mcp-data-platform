package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"runtime"
	"slices"

	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// DigestKey is the lookup digest of an API key's value: its SHA-256, in hex.
// It is taken only of a value shaped like a generated key (generatedKeyBytes
// random bytes, hex), so a digest cannot be reversed or guessed from, and
// finding a presented key by digest costs one hash where a bcrypt comparison
// per stored key cost tens of milliseconds each.
func DigestKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// DigestRecorder is a HashedKeySource that keeps a key's digest: a key stored
// before digests existed is found by bcrypt once, and recording its digest
// then makes every later use of it, on every replica, a lookup.
type DigestRecorder interface {
	RecordKeyDigest(ctx context.Context, name, keyHash, digest string) error
}

// legacyCompares bounds how many bcrypt comparisons of presented tokens run at
// once, process-wide: only keys stored before digests existed are compared,
// and a stream of unrecognized tokens must not take every core from the
// requests that present a real key.
//
//nolint:gochecknoglobals // one bound for the process, shared by every authenticator.
var legacyCompares = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2))

// undigested is the keys in keys with no digest: the only ones a token is
// compared with by bcrypt.
func undigested(keys []*APIKey) []*APIKey {
	var out []*APIKey
	for _, k := range keys {
		if k.KeyDigest == "" {
			out = append(out, k)
		}
	}
	return out
}

// matchLegacy compares token by bcrypt with keys, which carry no digest, and
// records the digest of the one it matches, in memory and in the store, when
// the token has one (a key whose value was not generated keeps only its
// bcrypt hash). The comparisons wait for a slot of legacyCompares; a request
// that goes away while waiting matches nothing.
func (a *APIKeyAuthenticator) matchLegacy(ctx context.Context, keys []*APIKey, token, digest string) *APIKey {
	if len(keys) == 0 {
		return nil
	}
	select {
	case legacyCompares <- struct{}{}:
	case <-ctx.Done():
		return nil
	}
	var matched *APIKey
	for _, k := range keys {
		if bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(token)) == nil {
			matched = k
			break
		}
	}
	<-legacyCompares
	if matched != nil && digest != "" {
		a.recordDigest(ctx, matched, digest)
	}
	return matched
}

// recordDigest gives key its digest in memory, and in the store when the store
// keeps digests, so the next use of it anywhere is a lookup. A key is never
// changed in place, since requests holding it read it without the lock: the
// digest goes on a copy that replaces it in the key set and the index. A key
// the set no longer holds (a sync removed it meanwhile) is not indexed.
func (a *APIKeyAuthenticator) recordDigest(ctx context.Context, key *APIKey, digest string) {
	a.mu.Lock()
	if i := slices.Index(a.hashedKeys, key); i >= 0 {
		updated := *key
		updated.KeyDigest = digest
		a.hashedKeys[i] = &updated
		a.byDigest[digest] = &updated
	}
	source := a.source
	a.mu.Unlock()
	if rec, ok := source.(DigestRecorder); ok {
		if err := rec.RecordKeyDigest(ctx, key.Name, key.KeyHash, digest); err != nil {
			slog.WarnContext(ctx, "api key: recording a key's digest failed; it is found by bcrypt until it is",
				"name", logsan.SanitizeForLog(key.Name), "error", logsan.SanitizeForLog(err.Error()))
		}
	}
}

package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// digestStore is a fakeKeyStore that keeps digests, as the database does.
type digestStore struct {
	*fakeKeyStore
	mu       sync.Mutex
	recorded map[string]string
	err      error
}

func (d *digestStore) RecordKeyDigest(_ context.Context, name, keyHash, digest string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	d.recorded[name+"|"+keyHash] = digest
	return nil
}

// TestAPIKey_FoundByDigestWithoutBcrypt: a key that carries a digest is found
// by it. Its stored hash is not a bcrypt hash at all here, so a comparison
// could never have matched it.
func TestAPIKey_FoundByDigestWithoutBcrypt(t *testing.T) {
	a, store := storeBacked(t)
	value, row := storedKey(t, a, "ci", "analyst")
	row.KeyHash = "not-a-bcrypt-hash"
	row.KeyDigest = DigestKey(value)
	store.put(row)
	if err := a.SyncHashedKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := a.Authenticate(WithToken(context.Background(), value))
	if err != nil || info.APIKeyName != "ci" {
		t.Fatalf("Authenticate = %+v, %v", info, err)
	}
	if _, err := a.Authenticate(WithToken(context.Background(), value+"0")); err == nil {
		t.Fatal("a token with another digest authenticated")
	}
}

// TestAPIKey_LegacyKeyGetsItsDigestOnFirstUse: a key stored before digests is
// found by bcrypt once, and its digest is recorded in memory and in the store,
// so the next use is a lookup on every replica.
func TestAPIKey_LegacyKeyGetsItsDigestOnFirstUse(t *testing.T) {
	a := NewAPIKeyAuthenticator(APIKeyConfig{})
	store := &digestStore{fakeKeyStore: newFakeKeyStore(), recorded: map[string]string{}}
	a.SetHashedKeySource(store)
	value, row := storedKey(t, a, "legacy")
	store.put(row)
	if err := a.SyncHashedKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(WithToken(context.Background(), value)); err != nil {
		t.Fatalf("legacy key refused: %v", err)
	}
	if got := store.recorded["legacy|"+row.KeyHash]; got != DigestKey(value) {
		t.Fatalf("recorded digest = %q", got)
	}
	a.mu.RLock()
	held := a.byDigest[DigestKey(value)]
	a.mu.RUnlock()
	if held == nil || held.Name != "legacy" {
		t.Fatal("the digest was not held in memory")
	}

	// A store that cannot record leaves the key working.
	store.err = errors.New("read-only")
	b := NewAPIKeyAuthenticator(APIKeyConfig{})
	b.SetHashedKeySource(store)
	if err := b.SyncHashedKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Authenticate(WithToken(context.Background(), value)); err != nil {
		t.Fatalf("a key whose digest could not be recorded was refused: %v", err)
	}
}

func TestAPIKey_DigestIndexFollowsTheKeySet(t *testing.T) {
	a := NewAPIKeyAuthenticator(APIKeyConfig{})
	v, w := strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	a.AddHashedKey(APIKey{Name: "k", KeyHash: "h", KeyDigest: DigestKey(v)})
	if _, err := a.Authenticate(WithToken(context.Background(), v)); err != nil {
		t.Fatalf("an added key is not found by digest: %v", err)
	}
	a.mu.RLock()
	k := a.byDigest[DigestKey(v)]
	a.mu.RUnlock()
	a.dropHashedKey(k)
	if _, err := a.Authenticate(WithToken(context.Background(), v)); err == nil {
		t.Fatal("a dropped key still authenticates")
	}
	a.ReplaceHashedKeys([]APIKey{{Name: "n", KeyHash: "h2", KeyDigest: DigestKey(w)}})
	if _, err := a.Authenticate(WithToken(context.Background(), w)); err != nil {
		t.Fatalf("a replaced key set is not found by digest: %v", err)
	}
}

// TestAPIKey_AChosenValueIsNeverDigested: a key stored before digests whose
// value a person chose (not the generated shape) is found by bcrypt and keeps
// only its bcrypt hash: no fast digest of a guessable value is ever recorded,
// in memory or in the store.
func TestAPIKey_AChosenValueIsNeverDigested(t *testing.T) {
	a := NewAPIKeyAuthenticator(APIKeyConfig{})
	store := &digestStore{fakeKeyStore: newFakeKeyStore(), recorded: map[string]string{}}
	a.SetHashedKeySource(store)
	const chosen = "correct-horse-battery"
	hash, err := bcrypt.GenerateFromPassword([]byte(chosen), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store.put(APIKey{Name: "chosen", KeyHash: string(hash)})
	if err := a.SyncHashedKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := a.Authenticate(WithToken(context.Background(), chosen)); err != nil {
			t.Fatalf("a legacy key with a chosen value was refused: %v", err)
		}
	}
	if len(store.recorded) != 0 {
		t.Fatalf("a digest was recorded in the store: %v", store.recorded)
	}
	a.mu.RLock()
	n, digest := len(a.byDigest), a.hashedKeys[0].KeyDigest
	a.mu.RUnlock()
	if n != 0 || digest != "" {
		t.Fatalf("a digest was held in memory: %d indexed, key digest %q", n, digest)
	}
}

func TestMatchLegacy_GivesUpWhenTheRequestGoes(t *testing.T) {
	a := NewAPIKeyAuthenticator(APIKeyConfig{})
	for range cap(legacyCompares) {
		legacyCompares <- struct{}{}
	}
	defer func() {
		for range cap(legacyCompares) {
			<-legacyCompares
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := a.matchLegacy(ctx, []*APIKey{{Name: "x", KeyHash: "h"}}, "t", "d"); got != nil {
		t.Fatalf("matched %v with no slot", got)
	}
	if got := a.matchLegacy(context.Background(), nil, "t", "d"); got != nil {
		t.Fatal("matched with no keys")
	}
}

// TestRecordDigest_NeverChangesAKeyARequestHolds: the digest goes on a copy
// that replaces the key in the set and the index, so a request reading the
// key it matched sees it unchanged; a key a sync removed meanwhile is not
// indexed.
func TestRecordDigest_NeverChangesAKeyARequestHolds(t *testing.T) {
	a := NewAPIKeyAuthenticator(APIKeyConfig{})
	a.AddHashedKey(APIKey{Name: "legacy", KeyHash: "h"})
	a.mu.RLock()
	held := a.hashedKeys[0]
	a.mu.RUnlock()

	a.recordDigest(context.Background(), held, "d1")
	if held.KeyDigest != "" {
		t.Fatal("the key a request held was changed in place")
	}
	a.mu.RLock()
	inSet, indexed := a.hashedKeys[0], a.byDigest["d1"]
	a.mu.RUnlock()
	if inSet == held || inSet.KeyDigest != "d1" || indexed != inSet {
		t.Fatalf("set holds %+v, index holds %+v", inSet, indexed)
	}

	gone := &APIKey{Name: "removed", KeyHash: "h2"}
	a.recordDigest(context.Background(), gone, "d2")
	a.mu.RLock()
	_, indexedGone := a.byDigest["d2"]
	a.mu.RUnlock()
	if indexedGone {
		t.Fatal("a key the set no longer holds was indexed")
	}
}

package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// fakeKeyStore models the key store contract the authenticator relies on: one
// row per name, a row identified by its name AND its hash, and reads that can
// fail. It counts reads so a test can tell whether the store was consulted.
type fakeKeyStore struct {
	mu       sync.Mutex
	rows     map[string]APIKey
	listErr  error
	holdsErr error
	lists    int
	holds    int
}

func newFakeKeyStore() *fakeKeyStore {
	return &fakeKeyStore{rows: map[string]APIKey{}}
}

func (f *fakeKeyStore) put(k APIKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[k.Name] = k
}

func (f *fakeKeyStore) remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, name)
}

func (f *fakeKeyStore) counts() (lists, holds int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists, f.holds
}

func (f *fakeKeyStore) HashedKeys(_ context.Context) ([]APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	if f.listErr != nil {
		return nil, f.listErr
	}
	keys := make([]APIKey, 0, len(f.rows))
	for _, k := range f.rows {
		keys = append(keys, k)
	}
	return keys, nil
}

func (f *fakeKeyStore) HoldsKey(_ context.Context, name, keyHash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds++
	if f.holdsErr != nil {
		return false, f.holdsErr
	}
	row, ok := f.rows[name]
	return ok && row.KeyHash == keyHash, nil
}

// storedKey mints a generated-shape key value and the stored row for it.
func storedKey(t *testing.T, a *APIKeyAuthenticator, name string, roles ...string) (string, APIKey) {
	t.Helper()
	value, err := a.GenerateKey(APIKey{Name: name})
	if err != nil {
		t.Fatalf("GenerateKey(%q): %v", name, err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(value), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashing %q: %v", name, err)
	}
	return value, APIKey{Name: name, KeyHash: string(hash), Roles: roles}
}

// storeBacked returns an authenticator with a fake store attached.
func storeBacked(t *testing.T, fileKeys ...APIKey) (*APIKeyAuthenticator, *fakeKeyStore) {
	t.Helper()
	a := NewAPIKeyAuthenticator(APIKeyConfig{Keys: fileKeys})
	store := newFakeKeyStore()
	a.SetHashedKeySource(store)
	return a, store
}

func authenticate(a *APIKeyAuthenticator, token string) (*authResult, error) {
	info, err := a.Authenticate(WithToken(context.Background(), token))
	if err != nil {
		return nil, err
	}
	return &authResult{UserID: info.UserID, Roles: info.Roles}, nil
}

// authResult is the part of an authenticated identity these tests read.
type authResult struct {
	UserID string
	Roles  []string
}

func holdsName(a *APIKeyAuthenticator, name string) bool {
	for _, s := range a.ListKeys() {
		if s.Name == name {
			return true
		}
	}
	return false
}

// TestAuthenticate_AStoredKeyIsAcceptedWhileTheStoreHoldsIt is #1715's
// same-replica defect: a key the store no longer holds is refused on the next
// request, with no reload, and is no longer held in memory.
func TestAuthenticate_AStoredKeyIsAcceptedWhileTheStoreHoldsIt(t *testing.T) {
	a, store := storeBacked(t)
	value, row := storedKey(t, a, "ci", testRoleAnalyst)
	store.put(row)
	if err := a.SyncHashedKeys(context.Background()); err != nil {
		t.Fatalf("SyncHashedKeys: %v", err)
	}

	got, err := authenticate(a, value)
	if err != nil {
		t.Fatalf("a stored key was refused: %v", err)
	}
	if got.UserID != "apikey:ci" || len(got.Roles) != 1 || got.Roles[0] != testRoleAnalyst {
		t.Errorf("identity = %+v; want apikey:ci with role %s", got, testRoleAnalyst)
	}

	store.remove("ci")
	if _, err := authenticate(a, value); !errors.Is(err, errInvalidAPIKey) {
		t.Errorf("a key deleted from the store authenticated or failed otherwise: %v", err)
	}
	if holdsName(a, "ci") {
		t.Error("the refused key is still held in memory")
	}
}

// TestAuthenticate_AKeyReplacedUnderItsNameIsRefused holds that the store
// check is on the hash as well as the name: a name deleted and created again
// through another replica carries a new key, and the old one stops working.
func TestAuthenticate_AKeyReplacedUnderItsNameIsRefused(t *testing.T) {
	a, store := storeBacked(t)
	oldValue, oldRow := storedKey(t, a, "ci", testRoleAnalyst)
	store.put(oldRow)
	if err := a.SyncHashedKeys(context.Background()); err != nil {
		t.Fatalf("SyncHashedKeys: %v", err)
	}

	store.remove("ci")
	newValue, newRow := storedKey(t, NewAPIKeyAuthenticator(APIKeyConfig{}), "ci", testRoleAdmin)
	store.put(newRow)

	if _, err := authenticate(a, oldValue); err == nil {
		t.Error("the replaced key authenticated")
	}
	got, err := authenticate(a, newValue)
	if err != nil {
		t.Fatalf("the key now stored under the name was refused: %v", err)
	}
	if got.Roles[0] != testRoleAdmin {
		t.Errorf("roles = %v; want the new key's %s", got.Roles, testRoleAdmin)
	}
}

// TestAuthenticate_AKeyStoredElsewhereAuthenticatesAtOnce is the other half of
// the window: a key another replica stored authenticates on its first request
// here, without waiting for a reload.
func TestAuthenticate_AKeyStoredElsewhereAuthenticatesAtOnce(t *testing.T) {
	a, store := storeBacked(t)
	value, row := storedKey(t, NewAPIKeyAuthenticator(APIKeyConfig{}), "peer-made", testRoleAdmin)
	store.put(row)

	if _, err := authenticate(a, value); err != nil {
		t.Fatalf("a key the store holds was refused: %v", err)
	}
	if !holdsName(a, "peer-made") {
		t.Error("the key the store read found is not held for the next request")
	}
	lists, _ := store.counts()
	if _, err := authenticate(a, value); err != nil {
		t.Fatalf("second request: %v", err)
	}
	if again, _ := store.counts(); again != lists {
		t.Errorf("a held key re-read the store (%d reads, then %d)", lists, again)
	}
}

// TestAuthenticate_OnlyAGeneratedShapeReadsTheStoreOnAMiss holds that a token
// that cannot be a generated key, such as a JWT another authenticator refused,
// never costs a store read.
func TestAuthenticate_OnlyAGeneratedShapeReadsTheStoreOnAMiss(t *testing.T) {
	a, store := storeBacked(t)
	for _, token := range []string{
		"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln",
		"mistyped-config-key",
		strings.Repeat("z", 64), // right length, not hex
		strings.Repeat("a", 62), // hex, wrong length
	} {
		if _, err := authenticate(a, token); !errors.Is(err, errInvalidAPIKey) {
			t.Errorf("token %q: err = %v; want invalid API key", token, err)
		}
	}
	if lists, holds := store.counts(); lists != 0 || holds != 0 {
		t.Errorf("tokens that cannot be generated keys read the store (%d lists, %d checks)", lists, holds)
	}

	if _, err := authenticate(a, strings.Repeat("ab", 32)); !errors.Is(err, errInvalidAPIKey) {
		t.Errorf("an unknown generated-shape token: err = %v; want invalid API key", err)
	}
	if lists, _ := store.counts(); lists != 1 {
		t.Errorf("an unknown generated-shape token read the store %d times; want 1", lists)
	}
}

// TestAuthenticate_AStoreThatCannotAnswerRefusesTheKey holds that a key is not
// accepted when the one record of its deletion cannot be read, and that the
// key is kept in memory so it works again once the store answers.
func TestAuthenticate_AStoreThatCannotAnswerRefusesTheKey(t *testing.T) {
	a, store := storeBacked(t)
	value, row := storedKey(t, a, "ci", testRoleAnalyst)
	store.put(row)
	if err := a.SyncHashedKeys(context.Background()); err != nil {
		t.Fatalf("SyncHashedKeys: %v", err)
	}

	store.mu.Lock()
	store.holdsErr = errors.New("connection reset")
	store.mu.Unlock()
	_, err := authenticate(a, value)
	if err == nil || !strings.Contains(err.Error(), "could not be confirmed against the key store") {
		t.Fatalf("err = %v; want the key refused as unconfirmed", err)
	}
	if !holdsName(a, "ci") {
		t.Error("a key the store could not confirm was dropped from memory")
	}

	store.mu.Lock()
	store.holdsErr = nil
	store.mu.Unlock()
	if _, err := authenticate(a, value); err != nil {
		t.Errorf("the key was refused after the store recovered: %v", err)
	}
}

// TestAuthenticate_AStoreReadFailureOnAMissRefuses holds that a miss whose
// store read fails is refused as unknown rather than accepted or retried.
func TestAuthenticate_AStoreReadFailureOnAMissRefuses(t *testing.T) {
	a, store := storeBacked(t)
	store.listErr = errors.New("connection reset")
	if _, err := authenticate(a, strings.Repeat("cd", 32)); !errors.Is(err, errInvalidAPIKey) {
		t.Errorf("err = %v; want invalid API key", err)
	}
}

// TestAuthenticate_AFileKeyNeverReadsTheStore holds the fast path: a config
// file key is in memory by definition, so the store is not consulted for it.
func TestAuthenticate_AFileKeyNeverReadsTheStore(t *testing.T) {
	a, store := storeBacked(t, APIKey{Key: "file-secret", Name: "from-file", Roles: []string{testRoleAdmin}})
	if _, err := authenticate(a, "file-secret"); err != nil {
		t.Fatalf("file key refused: %v", err)
	}
	if lists, holds := store.counts(); lists != 0 || holds != 0 {
		t.Errorf("a file key read the store (%d lists, %d checks)", lists, holds)
	}
}

// TestAuthenticate_AnExpiredStoredKeyIsRefused holds that expiry still applies
// to a key the store confirms.
func TestAuthenticate_AnExpiredStoredKeyIsRefused(t *testing.T) {
	a, store := storeBacked(t)
	value, row := storedKey(t, a, "old", testRoleAnalyst)
	past := time.Now().Add(-time.Minute)
	row.ExpiresAt = &past
	store.put(row)

	_, err := authenticate(a, value)
	if err == nil || !strings.Contains(err.Error(), "has expired") {
		t.Errorf("err = %v; want the expired key refused", err)
	}
}

// TestAuthenticate_ConcurrentMissesAndDeletes exercises the unlocked bcrypt
// comparisons against a key set that syncs and drops change underneath them.
func TestAuthenticate_ConcurrentMissesAndDeletes(t *testing.T) {
	a, store := storeBacked(t)
	value, row := storedKey(t, a, "busy", testRoleAnalyst)
	store.put(row)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if i%2 == 0 {
				_, _ = authenticate(a, value)
				return
			}
			_, _ = authenticate(a, strings.Repeat("ef", 32))
		})
	}
	wg.Go(func() { a.ReplaceHashedKeys(nil) })
	wg.Go(func() { _ = a.SyncHashedKeys(context.Background()) })
	wg.Wait()

	store.remove("busy")
	if _, err := authenticate(a, value); err == nil {
		t.Error("the key authenticated after the store stopped holding it")
	}
}

func TestSyncHashedKeys(t *testing.T) {
	t.Run("no store attached changes nothing", func(t *testing.T) {
		a := NewAPIKeyAuthenticator(APIKeyConfig{})
		a.AddHashedKey(APIKey{Name: "held", KeyHash: "$2a$10$placeholder"})
		if err := a.SyncHashedKeys(context.Background()); err != nil {
			t.Fatalf("SyncHashedKeys: %v", err)
		}
		if !holdsName(a, "held") {
			t.Error("a sync with no store dropped a held key")
		}
	})

	t.Run("replaces the held keys with the stored ones", func(t *testing.T) {
		a, store := storeBacked(t)
		a.AddHashedKey(APIKey{Name: "gone", KeyHash: "$2a$10$gone"})
		store.put(APIKey{Name: "kept", KeyHash: "$2a$10$kept"})
		if err := a.SyncHashedKeys(context.Background()); err != nil {
			t.Fatalf("SyncHashedKeys: %v", err)
		}
		if holdsName(a, "gone") || !holdsName(a, "kept") {
			t.Errorf("held keys after sync = %+v; want only kept", a.ListKeys())
		}
	})

	t.Run("a store read failure keeps the held keys and says why", func(t *testing.T) {
		a, store := storeBacked(t)
		a.AddHashedKey(APIKey{Name: "held", KeyHash: "$2a$10$placeholder"})
		store.listErr = errors.New("connection reset")
		err := a.SyncHashedKeys(context.Background())
		if err == nil || !strings.Contains(err.Error(), "reading api keys from the key store") {
			t.Fatalf("err = %v; want the read failure", err)
		}
		if !holdsName(a, "held") {
			t.Error("a failed sync dropped a held key")
		}
	})
}

func TestIsGeneratedKeyShape(t *testing.T) {
	tests := []struct {
		token string
		want  bool
	}{
		{strings.Repeat("0a", generatedKeyBytes), true},
		{strings.Repeat("0A", generatedKeyBytes), true},
		{strings.Repeat("0a", generatedKeyBytes-1), false},
		{strings.Repeat("0a", generatedKeyBytes) + "0", false},
		{strings.Repeat("0g", generatedKeyBytes), false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isGeneratedKeyShape(tt.token); got != tt.want {
			t.Errorf("isGeneratedKeyShape(%q) = %v; want %v", tt.token, got, tt.want)
		}
	}
}

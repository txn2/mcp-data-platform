package auth

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAPIKeyAuthenticator(t *testing.T) {
	cfg := APIKeyConfig{
		Keys: []APIKey{
			{Key: "test-key-1", Name: testRoleAdmin, Roles: []string{testRoleAdmin}},
			{Key: "test-key-2", Name: testRoleAnalyst, Roles: []string{testRoleAnalyst}},
		},
	}
	auth := NewAPIKeyAuthenticator(cfg)

	t.Run("valid key", func(t *testing.T) {
		ctx := WithToken(context.Background(), "test-key-1")
		userInfo, err := auth.Authenticate(ctx)
		if err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
		if userInfo.AuthType != "apikey" {
			t.Errorf("AuthType = %q, want %q", userInfo.AuthType, "apikey")
		}
		if len(userInfo.Roles) != 1 || userInfo.Roles[0] != testRoleAdmin {
			t.Errorf("Roles = %v, want [%s]", userInfo.Roles, testRoleAdmin)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		ctx := WithToken(context.Background(), "invalid-key")
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("Authenticate() expected error for invalid key")
		}
	})

	t.Run("no key", func(t *testing.T) {
		ctx := context.Background()
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("Authenticate() expected error for missing key")
		}
	})

	t.Run("AddKey", func(t *testing.T) {
		auth.AddKey(APIKey{Key: "new-key", Name: "new", Roles: []string{"new"}})
		ctx := WithToken(context.Background(), "new-key")
		userInfo, err := auth.Authenticate(ctx)
		if err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
		if userInfo.Roles[0] != "new" {
			t.Errorf("Roles = %v, want [new]", userInfo.Roles)
		}
	})

	t.Run("RemoveKey", func(t *testing.T) {
		auth.RemoveKey("new-key")
		ctx := WithToken(context.Background(), "new-key")
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("Authenticate() expected error after key removal")
		}
	})
}

func TestListKeys(t *testing.T) {
	cfg := APIKeyConfig{
		Keys: []APIKey{
			{Key: "key-b", Name: "bravo", Roles: []string{testRoleAnalyst}},
			{Key: "key-a", Name: "alpha", Roles: []string{testRoleAdmin}},
		},
	}
	auth := NewAPIKeyAuthenticator(cfg)

	summaries := auth.ListKeys()
	if len(summaries) != 2 {
		t.Fatalf("ListKeys() returned %d keys, want 2", len(summaries))
	}

	// Should be sorted by name
	if summaries[0].Name != "alpha" {
		t.Errorf("first key name = %q, want %q", summaries[0].Name, "alpha")
	}
	if summaries[1].Name != "bravo" {
		t.Errorf("second key name = %q, want %q", summaries[1].Name, "bravo")
	}

	// Should never contain the key value — verify via JSON tags on the struct
	if summaries[0].Roles[0] != testRoleAdmin {
		t.Errorf("first key roles = %v, want [%s]", summaries[0].Roles, testRoleAdmin)
	}
}

func TestListKeysIncludesHashedKeys(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{
		Keys: []APIKey{
			{Key: "file-key", Name: "file-entry", Roles: []string{testRoleAdmin}},
		},
	})
	auth.AddHashedKey(APIKey{
		KeyHash: "$2a$10$placeholder", // not used for auth in this test
		Name:    "db-entry",
		Roles:   []string{testRoleAnalyst},
	})

	summaries := auth.ListKeys()
	if len(summaries) != 2 {
		t.Fatalf("ListKeys() returned %d keys, want 2", len(summaries))
	}

	names := map[string]bool{}
	for _, s := range summaries {
		names[s.Name] = true
	}
	if !names["file-entry"] {
		t.Error("file-entry not found in ListKeys()")
	}
	if !names["db-entry"] {
		t.Error("db-entry not found in ListKeys()")
	}
}

func TestReplaceHashedKeys(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{
		Keys: []APIKey{{Key: "file-key", Name: "file", Roles: []string{testRoleAdmin}}},
	})

	hashFor := func(raw string) string {
		h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("bcrypt hash: %v", err)
		}
		return string(h)
	}

	// Seed an initial DB key, then replace the set with a different one:
	// the old key is revoked, the new key is admitted.
	auth.AddHashedKey(APIKey{KeyHash: hashFor("old-db-key"), Name: "old", Roles: []string{testRoleAnalyst}})
	auth.ReplaceHashedKeys([]APIKey{
		{KeyHash: hashFor("new-db-key"), Name: "new", Roles: []string{testRoleAnalyst}},
	})

	if _, err := auth.Authenticate(WithToken(context.Background(), "old-db-key")); err == nil {
		t.Error("revoked key still authenticates after ReplaceHashedKeys")
	}
	if _, err := auth.Authenticate(WithToken(context.Background(), "new-db-key")); err != nil {
		t.Errorf("new key should authenticate after ReplaceHashedKeys: %v", err)
	}
	// File-config key must survive a DB-key replacement.
	if _, err := auth.Authenticate(WithToken(context.Background(), "file-key")); err != nil {
		t.Errorf("file-config key must survive ReplaceHashedKeys: %v", err)
	}

	// Replacing with an empty set drops all DB keys but keeps file keys.
	auth.ReplaceHashedKeys(nil)
	if _, err := auth.Authenticate(WithToken(context.Background(), "new-db-key")); err == nil {
		t.Error("DB key should be gone after ReplaceHashedKeys(nil)")
	}
	if _, err := auth.Authenticate(WithToken(context.Background(), "file-key")); err != nil {
		t.Errorf("file-config key must survive ReplaceHashedKeys(nil): %v", err)
	}
}

func TestGenerateKey(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{
		Keys: []APIKey{{Key: "file-secret", Name: "from-file", Roles: []string{testRoleAdmin}}},
	})
	auth.AddHashedKey(APIKey{KeyHash: "$2a$10$placeholder", Name: "from-db", Roles: []string{testRoleAnalyst}})

	t.Run("generates a value in the generated-key shape", func(t *testing.T) {
		first, err := auth.GenerateKey(APIKey{Name: "test-gen", Roles: []string{testRoleAdmin}})
		if err != nil {
			t.Fatalf("GenerateKey() error = %v", err)
		}
		second, err := auth.GenerateKey(APIKey{Name: "test-gen", Roles: []string{testRoleAdmin}})
		if err != nil {
			t.Fatalf("GenerateKey() error = %v", err)
		}
		if !isGeneratedKeyShape(first) || len(first) != 64 {
			t.Errorf("key %q is not the hex of %d random bytes", first, generatedKeyBytes)
		}
		if first == second {
			t.Error("two generated values are equal")
		}
	})

	t.Run("holds nothing until the key is stored", func(t *testing.T) {
		keyValue, err := auth.GenerateKey(APIKey{Name: "unstored", Roles: []string{testRoleAdmin}})
		if err != nil {
			t.Fatalf("GenerateKey() error = %v", err)
		}
		if _, err := auth.Authenticate(WithToken(context.Background(), keyValue)); err == nil {
			t.Error("a generated value authenticated before any store held its hash")
		}
		for _, s := range auth.ListKeys() {
			if s.Name == "unstored" {
				t.Errorf("ListKeys() carries a key that was only generated: %+v", s)
			}
		}
	})

	t.Run("rejects a name a file key carries", func(t *testing.T) {
		if _, err := auth.GenerateKey(APIKey{Name: "from-file"}); err == nil {
			t.Error("GenerateKey() accepted the name of a file key")
		}
	})

	t.Run("rejects a name a stored key carries", func(t *testing.T) {
		if _, err := auth.GenerateKey(APIKey{Name: "from-db"}); err == nil {
			t.Error("GenerateKey() accepted the name of a stored key")
		}
	})
}

func TestAddHashedKey(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	rawKey := "test-key-for-hashing"
	hash, err := bcrypt.GenerateFromPassword([]byte(rawKey), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generating bcrypt hash: %v", err)
	}

	auth.AddHashedKey(APIKey{
		KeyHash: string(hash),
		Name:    "hashed-key",
		Email:   "hashed@example.com",
		Roles:   []string{testRoleAdmin},
	})

	// Verify key appears in ListKeys.
	summaries := auth.ListKeys()
	found := false
	for _, s := range summaries {
		if s.Name == "hashed-key" {
			found = true
			if s.Email != "hashed@example.com" {
				t.Errorf("email = %q, want %q", s.Email, "hashed@example.com")
			}
			if len(s.Roles) != 1 || s.Roles[0] != testRoleAdmin {
				t.Errorf("roles = %v, want [%s]", s.Roles, testRoleAdmin)
			}
		}
	}
	if !found {
		t.Fatal("hashed key not found in ListKeys()")
	}

	// Verify authentication works with the raw key via bcrypt comparison.
	ctx := WithToken(context.Background(), rawKey)
	info, err := auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if info.UserID != "apikey:hashed-key" {
		t.Errorf("UserID = %q, want %q", info.UserID, "apikey:hashed-key")
	}
	if info.Email != "hashed@example.com" {
		t.Errorf("Email = %q, want %q", info.Email, "hashed@example.com")
	}
	if info.AuthType != "apikey" {
		t.Errorf("AuthType = %q, want %q", info.AuthType, "apikey")
	}

	// Wrong raw key should fail.
	ctx = WithToken(context.Background(), "wrong-key")
	_, err = auth.Authenticate(ctx)
	if err == nil {
		t.Error("Authenticate() should fail with wrong key for hashed entry")
	}
}

func TestAddKeyGuardsKeyHash(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	// AddKey with KeyHash set — KeyHash should be cleared.
	auth.AddKey(APIKey{
		Key:     "plaintext-val",
		KeyHash: "should-be-cleared",
		Name:    "guarded",
		Roles:   []string{testRoleAdmin},
	})

	auth.mu.RLock()
	k := auth.fileKeys["plaintext-val"]
	auth.mu.RUnlock()

	if k == nil {
		t.Fatal("key not found in fileKeys")
	}
	if k.KeyHash != "" {
		t.Errorf("KeyHash = %q, want empty (should be cleared by AddKey)", k.KeyHash)
	}
}

func TestAddHashedKeyGuardsPlaintext(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	// AddHashedKey with Key set — Key should be cleared.
	auth.AddHashedKey(APIKey{
		Key:     "should-be-cleared",
		KeyHash: "$2a$10$placeholder",
		Name:    "guarded-hash",
		Roles:   []string{testRoleAdmin},
	})

	auth.mu.RLock()
	if len(auth.hashedKeys) != 1 {
		auth.mu.RUnlock()
		t.Fatal("expected 1 hashed key")
	}
	k := auth.hashedKeys[0]
	auth.mu.RUnlock()

	if k.Key != "" {
		t.Errorf("Key = %q, want empty (should be cleared by AddHashedKey)", k.Key)
	}
}

func TestNoCollisionFileKeyAndDBKeyWithSameName(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	// Add a file key with name "admin".
	auth.AddKey(APIKey{
		Key:   "file-admin-key",
		Name:  "admin",
		Roles: []string{testRoleAdmin},
	})

	// Add a DB key also with name "admin" — different credentials.
	rawDBKey := "db-admin-secret"
	hash, err := bcrypt.GenerateFromPassword([]byte(rawDBKey), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("generating bcrypt hash: %v", err)
	}
	auth.AddHashedKey(APIKey{
		KeyHash: string(hash),
		Name:    "admin",
		Roles:   []string{testRoleAnalyst}, // different role to distinguish
	})

	// Both keys should work independently.
	ctx := WithToken(context.Background(), "file-admin-key")
	info, err := auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf("file key auth failed: %v", err)
	}
	if info.Roles[0] != testRoleAdmin {
		t.Errorf("file key role = %q, want %q", info.Roles[0], testRoleAdmin)
	}

	ctx = WithToken(context.Background(), rawDBKey)
	info, err = auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf("DB key auth failed: %v", err)
	}
	if info.Roles[0] != testRoleAnalyst {
		t.Errorf("DB key role = %q, want %q", info.Roles[0], testRoleAnalyst)
	}

	// Same-name keys are deduplicated with Source "both".
	summaries := auth.ListKeys()
	adminCount := 0
	var adminSource string
	for _, s := range summaries {
		if s.Name == "admin" {
			adminCount++
			adminSource = s.Source
		}
	}
	if adminCount != 1 {
		t.Errorf("expected 1 deduplicated key named 'admin', got %d", adminCount)
	}
	if adminSource != "both" {
		t.Errorf("expected source 'both' for deduplicated key, got %q", adminSource)
	}
}

func TestListKeysSource(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{
		Keys: []APIKey{
			{Key: "file-key-1", Name: "file-only", Roles: []string{"admin"}},
		},
	})

	hash, _ := bcrypt.GenerateFromPassword([]byte("db-key-1"), bcrypt.MinCost)
	auth.AddHashedKey(APIKey{KeyHash: string(hash), Name: "db-only", Roles: []string{"analyst"}})

	summaries := auth.ListKeys()
	sourceByName := make(map[string]string, len(summaries))
	for _, s := range summaries {
		sourceByName[s.Name] = s.Source
	}

	if sourceByName["file-only"] != "file" {
		t.Errorf("file-only key source = %q, want 'file'", sourceByName["file-only"])
	}
	if sourceByName["db-only"] != "database" {
		t.Errorf("db-only key source = %q, want 'database'", sourceByName["db-only"])
	}
}

func TestFileKeyFastPathWhenDBKeysExist(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	// Add a file key.
	auth.AddKey(APIKey{
		Key:   "fast-path-key",
		Name:  "fast",
		Roles: []string{testRoleAdmin},
	})

	// Add multiple DB keys (bcrypt is expensive).
	for i := range 5 {
		hash, _ := bcrypt.GenerateFromPassword(
			fmt.Appendf(nil, "db-key-%d", i), bcrypt.MinCost,
		)
		auth.AddHashedKey(APIKey{
			KeyHash: string(hash),
			Name:    fmt.Sprintf("db-%d", i),
			Roles:   []string{testRoleAnalyst},
		})
	}

	// File key should still authenticate quickly (fast path).
	ctx := WithToken(context.Background(), "fast-path-key")
	info, err := auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf("fast path auth failed: %v", err)
	}
	if info.UserID != "apikey:fast" {
		t.Errorf("UserID = %q, want %q", info.UserID, "apikey:fast")
	}
}

func TestConcurrentAPIKeyAccess(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{
		Keys: []APIKey{
			{Key: "initial-key", Name: "initial", Roles: []string{testRoleAdmin}},
		},
	})

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Concurrent reads
	for range goroutines {
		go func() {
			defer wg.Done()
			ctx := WithToken(context.Background(), "initial-key")
			_, _ = auth.Authenticate(ctx)
		}()
	}

	// Concurrent adds
	for i := range goroutines {
		go func() {
			defer wg.Done()
			auth.AddKey(APIKey{
				Key:   fmt.Sprintf("concurrent-key-%d", i),
				Name:  fmt.Sprintf("concurrent-%d", i),
				Roles: []string{"user"},
			})
		}()
	}

	// Concurrent list
	for range goroutines {
		go func() {
			defer wg.Done()
			_ = auth.ListKeys()
		}()
	}

	wg.Wait()

	// After concurrent operations, the initial key should still work
	ctx := WithToken(context.Background(), "initial-key")
	info, err := auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf("Authenticate() after concurrent ops: %v", err)
	}
	if info.UserID != "apikey:initial" {
		t.Errorf("UserID = %q, want %q", info.UserID, "apikey:initial")
	}
}

// An API key's attributes reach the caller as claims, so a script parameter
// bound to caller.<claim> reads the same value for a key and for a person
// (#1846). A key with none has an empty claims map, never nil.
func TestAPIKeyAttributesBecomeClaims(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{Keys: []APIKey{
		{Key: "tenant-key", Name: "tenant", Roles: []string{testRoleAnalyst}, Attributes: map[string]string{"tenant_id": "acme"}},
		{Key: "plain-key", Name: "plain", Roles: []string{testRoleAnalyst}},
	}})

	info, err := auth.Authenticate(WithToken(context.Background(), "tenant-key"))
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got := info.Claims["tenant_id"]; got != "acme" {
		t.Errorf("Claims[tenant_id] = %v, want acme", got)
	}

	info, err = auth.Authenticate(WithToken(context.Background(), "plain-key"))
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if info.Claims == nil || len(info.Claims) != 0 {
		t.Errorf("Claims = %#v, want an empty map", info.Claims)
	}
}

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

func TestRemoveByName(t *testing.T) {
	cfg := APIKeyConfig{
		Keys: []APIKey{
			{Key: "key-1", Name: "one", Roles: []string{testRoleAdmin}},
			{Key: "key-2", Name: "two", Roles: []string{testRoleAnalyst}},
		},
	}
	auth := NewAPIKeyAuthenticator(cfg)

	t.Run("removes existing key by name", func(t *testing.T) {
		if !auth.RemoveByName("one") {
			t.Error("RemoveByName() returned false for existing key")
		}

		ctx := WithToken(context.Background(), "key-1")
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("Authenticate() should fail after RemoveByName")
		}
	})

	t.Run("returns false for non-existent name", func(t *testing.T) {
		if auth.RemoveByName("nonexistent") {
			t.Error("RemoveByName() returned true for non-existent key")
		}
	})
}

func TestRemoveByNameHashedKey(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	rawKey := "hashed-remove-test"
	hash, err := bcrypt.GenerateFromPassword([]byte(rawKey), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("generating bcrypt hash: %v", err)
	}

	auth.AddHashedKey(APIKey{
		KeyHash: string(hash),
		Name:    "db-key-to-remove",
		Roles:   []string{testRoleAdmin},
	})

	// Verify the key works before removal.
	ctx := WithToken(context.Background(), rawKey)
	_, err = auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf("Authenticate() before removal: %v", err)
	}

	// Remove by name.
	if !auth.RemoveByName("db-key-to-remove") {
		t.Fatal("RemoveByName() returned false for existing hashed key")
	}

	// Verify authentication fails after removal.
	_, err = auth.Authenticate(ctx)
	if err == nil {
		t.Error("Authenticate() should fail after RemoveByName on hashed key")
	}

	// Verify it's gone from ListKeys.
	for _, s := range auth.ListKeys() {
		if s.Name == "db-key-to-remove" {
			t.Error("removed hashed key still appears in ListKeys()")
		}
	}
}

func TestGenerateKey(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	t.Run("generates valid key", func(t *testing.T) {
		keyValue, err := auth.GenerateKey(APIKey{Name: "test-gen", Roles: []string{testRoleAdmin}})
		if err != nil {
			t.Fatalf("GenerateKey() error = %v", err)
		}
		// 32 bytes hex-encoded = 64 chars
		if len(keyValue) != 64 {
			t.Errorf("key length = %d, want 64", len(keyValue))
		}

		// Key should be usable for authentication
		ctx := WithToken(context.Background(), keyValue)
		info, err := auth.Authenticate(ctx)
		if err != nil {
			t.Fatalf("Authenticate() after GenerateKey error = %v", err)
		}
		if info.Roles[0] != testRoleAdmin {
			t.Errorf("Roles = %v, want [%s]", info.Roles, testRoleAdmin)
		}
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		_, err := auth.GenerateKey(APIKey{Name: "test-gen", Roles: []string{testRoleAnalyst}})
		if err == nil {
			t.Error("GenerateKey() expected error for duplicate name")
		}
	})

	t.Run("rejects duplicate name from hashed keys", func(t *testing.T) {
		auth.AddHashedKey(APIKey{
			KeyHash: "$2a$10$placeholder",
			Name:    "db-dup-check",
			Roles:   []string{testRoleAnalyst},
		})
		_, err := auth.GenerateKey(APIKey{Name: "db-dup-check", Roles: []string{testRoleAdmin}})
		if err == nil {
			t.Error("GenerateKey() should reject name that exists in hashed keys")
		}
	})

	t.Run("appears in ListKeys", func(t *testing.T) {
		summaries := auth.ListKeys()
		found := false
		for _, s := range summaries {
			if s.Name == "test-gen" {
				found = true
				break
			}
		}
		if !found {
			t.Error("generated key not found in ListKeys()")
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

	// Both should appear in ListKeys.
	summaries := auth.ListKeys()
	adminCount := 0
	for _, s := range summaries {
		if s.Name == "admin" {
			adminCount++
		}
	}
	if adminCount != 2 {
		t.Errorf("expected 2 keys named 'admin', got %d", adminCount)
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

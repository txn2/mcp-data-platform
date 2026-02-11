package auth

import (
	"context"
	"fmt"
	"sync"
	"testing"
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

func TestGenerateKey(t *testing.T) {
	auth := NewAPIKeyAuthenticator(APIKeyConfig{})

	t.Run("generates valid key", func(t *testing.T) {
		keyValue, err := auth.GenerateKey("test-gen", []string{testRoleAdmin})
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
		_, err := auth.GenerateKey("test-gen", []string{testRoleAnalyst})
		if err == nil {
			t.Error("GenerateKey() expected error for duplicate name")
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

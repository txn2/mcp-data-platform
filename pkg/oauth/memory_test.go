package oauth

import (
	"context"
	"testing"
	"time"
)

const (
	testMemID       = "id-1"
	testMemClientID = "client-123"
)

func TestMemoryStorage_Client(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()

	client := &Client{
		ID:           testMemID,
		ClientID:     testMemClientID,
		ClientSecret: "secret",
		Name:         "Test Client",
		RedirectURIs: []string{"http://localhost:8080/callback"},
		GrantTypes:   []string{"authorization_code"},
		RequirePKCE:  true,
		Active:       true,
	}

	t.Run("create client", func(t *testing.T) {
		err := storage.CreateClient(ctx, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("create duplicate client", func(t *testing.T) {
		err := storage.CreateClient(ctx, client)
		if err == nil {
			t.Error("expected error for duplicate client")
		}
	})

	t.Run("get client", func(t *testing.T) {
		got, err := storage.GetClient(ctx, testMemClientID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ClientID != client.ClientID {
			t.Errorf("expected client_id %q, got %q", client.ClientID, got.ClientID)
		}
	})

	t.Run("get nonexistent client", func(t *testing.T) {
		_, err := storage.GetClient(ctx, "nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent client")
		}
	})

	t.Run("update client", func(t *testing.T) {
		client.Name = "Updated Client"
		err := storage.UpdateClient(ctx, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, _ := storage.GetClient(ctx, testMemClientID)
		if got.Name != "Updated Client" {
			t.Errorf("expected name 'Updated Client', got %q", got.Name)
		}
	})

	t.Run("update nonexistent client", func(t *testing.T) {
		err := storage.UpdateClient(ctx, &Client{ClientID: "nonexistent"})
		if err == nil {
			t.Error("expected error for nonexistent client")
		}
	})
}

func TestMemoryStorage_Client_ListAndDelete(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()

	client := &Client{
		ID:           testMemID,
		ClientID:     testMemClientID,
		ClientSecret: "secret",
		Name:         "Test Client",
		RedirectURIs: []string{"http://localhost:8080/callback"},
		GrantTypes:   []string{"authorization_code"},
		RequirePKCE:  true,
		Active:       true,
	}
	_ = storage.CreateClient(ctx, client)

	t.Run("list clients", func(t *testing.T) {
		clients, err := storage.ListClients(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(clients) != 1 {
			t.Errorf("expected 1 client, got %d", len(clients))
		}
	})

	t.Run("delete client", func(t *testing.T) {
		err := storage.DeleteClient(ctx, testMemClientID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = storage.GetClient(ctx, testMemClientID)
		if err == nil {
			t.Error("expected error for deleted client")
		}
	})
}

func TestMemoryStorage_AuthorizationCode(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()

	code := &AuthorizationCode{
		ID:            testMemID,
		Code:          "code-123",
		ClientID:      testMemClientID,
		UserID:        "user-123",
		CodeChallenge: "challenge",
		RedirectURI:   "http://localhost:8080/callback",
		Scope:         "read",
		ExpiresAt:     time.Now().Add(10 * time.Minute),
		Used:          false,
		CreatedAt:     time.Now(),
	}

	t.Run("save authorization code", func(t *testing.T) {
		err := storage.SaveAuthorizationCode(ctx, code)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code.Code != "code-123" {
			t.Errorf("save must not mutate the caller's struct, got code %q", code.Code)
		}
	})

	t.Run("get authorization code", func(t *testing.T) {
		got, err := storage.GetAuthorizationCode(ctx, "code-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The store holds only the digest, never the raw credential.
		if got.Code != HashToken("code-123") {
			t.Errorf("expected stored code to be the digest %q, got %q", HashToken("code-123"), got.Code)
		}
		if got.UserID != code.UserID {
			t.Errorf("expected user ID %q, got %q", code.UserID, got.UserID)
		}
	})

	t.Run("get nonexistent code", func(t *testing.T) {
		_, err := storage.GetAuthorizationCode(ctx, "nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent code")
		}
	})

	t.Run("delete authorization code", func(t *testing.T) {
		err := storage.DeleteAuthorizationCode(ctx, "code-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = storage.GetAuthorizationCode(ctx, "code-123")
		if err == nil {
			t.Error("expected error for deleted code")
		}
	})

	t.Run("consume authorization code", func(t *testing.T) {
		consumable := &AuthorizationCode{
			Code:      "consume-me",
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}
		if err := storage.SaveAuthorizationCode(ctx, consumable); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, err := storage.ConsumeAuthorizationCode(ctx, "consume-me")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Code != HashToken("consume-me") {
			t.Errorf("expected stored code to be the digest of 'consume-me', got %q", got.Code)
		}

		// Second consume must fail: the code is single-use.
		if _, err := storage.ConsumeAuthorizationCode(ctx, "consume-me"); err == nil {
			t.Error("expected error consuming an already-consumed code")
		}
	})

	t.Run("consume nonexistent code", func(t *testing.T) {
		if _, err := storage.ConsumeAuthorizationCode(ctx, "nonexistent"); err == nil {
			t.Error("expected error for nonexistent code")
		}
	})

	t.Run("cleanup expired codes", func(t *testing.T) {
		// Add expired code
		expiredCode := &AuthorizationCode{
			Code:      "expired-code",
			ExpiresAt: time.Now().Add(-time.Hour),
		}
		_ = storage.SaveAuthorizationCode(ctx, expiredCode)

		// Add valid code
		validCode := &AuthorizationCode{
			Code:      "valid-code",
			ExpiresAt: time.Now().Add(time.Hour),
		}
		_ = storage.SaveAuthorizationCode(ctx, validCode)

		err := storage.CleanupExpiredCodes(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Expired should be gone
		_, err = storage.GetAuthorizationCode(ctx, "expired-code")
		if err == nil {
			t.Error("expected expired code to be cleaned up")
		}

		// Valid should remain
		_, err = storage.GetAuthorizationCode(ctx, "valid-code")
		if err != nil {
			t.Error("expected valid code to remain")
		}
	})
}

func TestMemoryStorage_RefreshToken(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()

	token := &RefreshToken{
		ID:        testMemID,
		Token:     "token-123",
		ClientID:  testMemClientID,
		UserID:    "user-123",
		Scope:     "read",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	t.Run("save refresh token", func(t *testing.T) {
		err := storage.SaveRefreshToken(ctx, token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token.Token != "token-123" {
			t.Errorf("save must not mutate the caller's struct, got token %q", token.Token)
		}
	})

	t.Run("get refresh token", func(t *testing.T) {
		got, err := storage.GetRefreshToken(ctx, "token-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The store holds only the digest, never the raw credential.
		if got.Token != HashToken("token-123") {
			t.Errorf("expected stored token to be the digest %q, got %q", HashToken("token-123"), got.Token)
		}
		if got.UserID != token.UserID {
			t.Errorf("expected user ID %q, got %q", token.UserID, got.UserID)
		}
	})

	t.Run("get nonexistent token", func(t *testing.T) {
		_, err := storage.GetRefreshToken(ctx, "nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent token")
		}
	})

	t.Run("delete refresh token", func(t *testing.T) {
		err := storage.DeleteRefreshToken(ctx, "token-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = storage.GetRefreshToken(ctx, "token-123")
		if err == nil {
			t.Error("expected error for deleted token")
		}
	})

	t.Run("consume refresh token", func(t *testing.T) {
		consumable := &RefreshToken{
			Token:     "rotate-me",
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		if err := storage.SaveRefreshToken(ctx, consumable); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, err := storage.ConsumeRefreshToken(ctx, "rotate-me")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Token != HashToken("rotate-me") {
			t.Errorf("expected stored token to be the digest of 'rotate-me', got %q", got.Token)
		}

		// Second consume must fail: rotation invalidated the token.
		if _, err := storage.ConsumeRefreshToken(ctx, "rotate-me"); err == nil {
			t.Error("expected error consuming an already-consumed token")
		}
	})

	t.Run("consume nonexistent token", func(t *testing.T) {
		if _, err := storage.ConsumeRefreshToken(ctx, "nonexistent"); err == nil {
			t.Error("expected error for nonexistent token")
		}
	})
}

func TestMemoryStorage_RefreshToken_Cleanup(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()

	t.Run("delete tokens for client", func(t *testing.T) {
		// Add tokens for two clients
		_ = storage.SaveRefreshToken(ctx, &RefreshToken{
			Token:     "client1-token1",
			ClientID:  "client-1",
			ExpiresAt: time.Now().Add(time.Hour),
		})
		_ = storage.SaveRefreshToken(ctx, &RefreshToken{
			Token:     "client1-token2",
			ClientID:  "client-1",
			ExpiresAt: time.Now().Add(time.Hour),
		})
		_ = storage.SaveRefreshToken(ctx, &RefreshToken{
			Token:     "client2-token1",
			ClientID:  "client-2",
			ExpiresAt: time.Now().Add(time.Hour),
		})

		err := storage.DeleteRefreshTokensForClient(ctx, "client-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Client 1 tokens should be gone
		_, err = storage.GetRefreshToken(ctx, "client1-token1")
		if err == nil {
			t.Error("expected client-1 tokens to be deleted")
		}

		// Client 2 token should remain
		_, err = storage.GetRefreshToken(ctx, "client2-token1")
		if err != nil {
			t.Error("expected client-2 token to remain")
		}
	})

	t.Run("cleanup expired tokens", func(t *testing.T) {
		// Add expired token
		_ = storage.SaveRefreshToken(ctx, &RefreshToken{
			Token:     "expired-token",
			ExpiresAt: time.Now().Add(-time.Hour),
		})

		// Add valid token
		_ = storage.SaveRefreshToken(ctx, &RefreshToken{
			Token:     "valid-token",
			ExpiresAt: time.Now().Add(time.Hour),
		})

		err := storage.CleanupExpiredTokens(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Expired should be gone
		_, err = storage.GetRefreshToken(ctx, "expired-token")
		if err == nil {
			t.Error("expected expired token to be cleaned up")
		}

		// Valid should remain
		_, err = storage.GetRefreshToken(ctx, "valid-token")
		if err != nil {
			t.Error("expected valid token to remain")
		}
	})
}

// Verify MemoryStorage implements Storage interface.
var _ Storage = (*MemoryStorage)(nil)

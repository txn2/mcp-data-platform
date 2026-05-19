package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const testAnonymousID = "anonymous"

// mockAuthenticator is a mock for testing.
type mockAuthenticator struct {
	userInfo *middleware.UserInfo
	err      error
}

func (m *mockAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return m.userInfo, m.err
}

func TestChainedAuthenticator(t *testing.T) {
	t.Run("first authenticator succeeds", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			userInfo: &middleware.UserInfo{UserID: "user1", AuthType: "type1"},
		}
		auth2 := &mockAuthenticator{
			userInfo: &middleware.UserInfo{UserID: "user2", AuthType: "type2"},
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{}, auth1, auth2)

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != "user1" {
			t.Errorf("UserID = %q, want 'user1'", userInfo.UserID)
		}
	})

	t.Run("first fails second succeeds", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			err: fmt.Errorf("auth1 failed"),
		}
		auth2 := &mockAuthenticator{
			userInfo: &middleware.UserInfo{UserID: "user2", AuthType: "type2"},
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{}, auth1, auth2)

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != "user2" {
			t.Errorf("UserID = %q, want 'user2'", userInfo.UserID)
		}
	})

	t.Run("all fail returns last error", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			err: fmt.Errorf("auth1 failed"),
		}
		auth2 := &mockAuthenticator{
			err: fmt.Errorf("auth2 failed"),
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{}, auth1, auth2)

		_, err := chained.Authenticate(context.Background())
		if err == nil {
			t.Error("expected error")
		}
		if err.Error() != "auth2 failed" {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("all fail with allow anonymous", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			err: fmt.Errorf("auth1 failed"),
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{AllowAnonymous: true}, auth1)

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != testAnonymousID {
			t.Errorf("UserID = %q, want 'anonymous'", userInfo.UserID)
		}
		if userInfo.AuthType != testAnonymousID {
			t.Errorf("AuthType = %q", userInfo.AuthType)
		}
	})
}

func TestChainedAuthenticator_EmptyChain(t *testing.T) {
	t.Run("empty chain fails", func(t *testing.T) {
		chained := NewChainedAuthenticator(ChainedAuthConfig{})

		_, err := chained.Authenticate(context.Background())
		if err == nil {
			t.Error("expected error for empty chain")
		}
	})

	t.Run("empty chain with allow anonymous", func(t *testing.T) {
		chained := NewChainedAuthenticator(ChainedAuthConfig{AllowAnonymous: true})

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != testAnonymousID {
			t.Errorf("UserID = %q", userInfo.UserID)
		}
	})
}

func TestBearerTokenExtractor(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		extractor := &BearerTokenExtractor{}
		_, err := extractor.Extract(context.Background())
		if err == nil {
			t.Error("expected error for missing token")
		}
	})

	t.Run("token without Bearer prefix", func(t *testing.T) {
		extractor := &BearerTokenExtractor{}
		ctx := WithToken(context.Background(), "mytoken")

		token, err := extractor.Extract(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "mytoken" {
			t.Errorf("token = %q", token)
		}
	})

	t.Run("token with Bearer prefix", func(t *testing.T) {
		extractor := &BearerTokenExtractor{}
		ctx := WithToken(context.Background(), "Bearer mytoken")

		token, err := extractor.Extract(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "mytoken" {
			t.Errorf("token = %q", token)
		}
	})
}

func TestAPIKeyExtractor(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		extractor := &APIKeyExtractor{}
		_, err := extractor.Extract(context.Background())
		if err == nil {
			t.Error("expected error for missing token")
		}
	})

	t.Run("token present", func(t *testing.T) {
		extractor := &APIKeyExtractor{
			HeaderName: "X-API-Key",
		}
		ctx := WithToken(context.Background(), "api-key-123")

		token, err := extractor.Extract(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "api-key-123" {
			t.Errorf("token = %q", token)
		}
	})
}

func TestLooksLikeJWT(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"plain api key", "nifi-etl", false},
		{"hex api key", "abc123def456", false},
		{"two segments", "header.payload", false},
		{"three segments empty middle", "header..signature", false},
		{"three segments empty header", ".payload.signature", false},
		{"three segments empty signature", "header.payload.", false},
		{"four segments", "a.b.c.d", false},
		{"valid three segments", "header.payload.signature", true},
		// Realistic-shaped JWT (not a real one — we never invoke the JWT
		// library here, so we don't need valid base64 or signatures).
		{"realistic shape", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LooksLikeJWT(tc.in)
			if got != tc.want {
				t.Errorf("LooksLikeJWT(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestChainedAuthenticator_NotAJWTSentinelFallsThrough(t *testing.T) {
	// Models the production chain: two JWT-based authenticators that
	// can't recognize an API key (return ErrNotAJWT) followed by an
	// API-key authenticator that succeeds. The chain must return the
	// API-key user without surfacing the JWT sentinel.
	jwtAuth1 := &mockAuthenticator{err: ErrNotAJWT}
	jwtAuth2 := &mockAuthenticator{err: fmt.Errorf("invalid token: %w", ErrNotAJWT)}
	apiKeyAuth := &mockAuthenticator{
		userInfo: &middleware.UserInfo{UserID: "apikey:svc", AuthType: "apikey"},
	}

	chained := NewChainedAuthenticator(ChainedAuthConfig{}, jwtAuth1, jwtAuth2, apiKeyAuth)

	userInfo, err := chained.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userInfo == nil || userInfo.UserID != "apikey:svc" {
		t.Errorf("UserID = %v, want 'apikey:svc'", userInfo)
	}
}

func TestChainedAuthenticator_AllSentinelNoLoudErrors(t *testing.T) {
	// All authenticators return the not-a-JWT sentinel and no real
	// authenticator matches: anonymous-disabled chain falls back to
	// the generic "authentication failed" error rather than leaking
	// the sentinel as the surface-level error.
	jwt1 := &mockAuthenticator{err: ErrNotAJWT}
	jwt2 := &mockAuthenticator{err: fmt.Errorf("invalid token: %w", ErrNotAJWT)}

	chained := NewChainedAuthenticator(ChainedAuthConfig{}, jwt1, jwt2)

	_, err := chained.Authenticate(context.Background())
	if err == nil {
		t.Fatal("expected error when all authenticators fall through")
	}
	if errors.Is(err, ErrNotAJWT) {
		t.Errorf("error leaked sentinel ErrNotAJWT: %v", err)
	}
}

func TestChainedAuthenticator_RealErrorPreservedAfterSentinel(t *testing.T) {
	// When a real verification failure follows a sentinel, the chain
	// should surface the real failure (not the sentinel and not nil)
	// so operators see the actual reason in the WARN line emitted by
	// the caller (MCPToolCallMiddleware).
	jwt1 := &mockAuthenticator{err: ErrNotAJWT}
	jwt2 := &mockAuthenticator{err: fmt.Errorf("verifying token: bad signature")}

	chained := NewChainedAuthenticator(ChainedAuthConfig{}, jwt1, jwt2)

	_, err := chained.Authenticate(context.Background())
	if err == nil {
		t.Fatal("expected error from chain")
	}
	if err.Error() != "verifying token: bad signature" {
		t.Errorf("error = %q, want real verification failure", err.Error())
	}
}

func TestCorrelationFields(t *testing.T) {
	t.Run("no platform context", func(t *testing.T) {
		reqID, tool := correlationFields(context.Background())
		if reqID != "" || tool != "" {
			t.Errorf("got (%q, %q), want empty pair", reqID, tool)
		}
	})

	t.Run("platform context populated", func(t *testing.T) {
		pc := &middleware.PlatformContext{RequestID: "req-abc123", ToolName: "trino_query"}
		ctx := middleware.WithPlatformContext(context.Background(), pc)
		reqID, tool := correlationFields(ctx)
		if reqID != "req-abc123" {
			t.Errorf("reqID = %q, want 'req-abc123'", reqID)
		}
		if tool != "trino_query" {
			t.Errorf("tool = %q, want 'trino_query'", tool)
		}
	})
}

func TestChainedAuthConfig(t *testing.T) {
	cfg := ChainedAuthConfig{
		AllowAnonymous: true,
	}
	if !cfg.AllowAnonymous {
		t.Error("AllowAnonymous = false")
	}
}

// Verify interface compliance.
var (
	_ middleware.Authenticator = (*mockAuthenticator)(nil)
	_ middleware.Authenticator = (*ChainedAuthenticator)(nil)
	_ TokenExtractor           = (*BearerTokenExtractor)(nil)
	_ TokenExtractor           = (*APIKeyExtractor)(nil)
)

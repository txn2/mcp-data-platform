package middleware

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPlatformContext(t *testing.T) {
	t.Run("NewPlatformContext", func(t *testing.T) {
		pc := NewPlatformContext("req-123")
		if pc.RequestID != "req-123" {
			t.Errorf("RequestID = %q, want %q", pc.RequestID, "req-123")
		}
		if pc.StartTime.IsZero() {
			t.Error("StartTime should not be zero")
		}
		if pc.UserClaims == nil {
			t.Error("UserClaims should be initialized")
		}
	})

	t.Run("WithPlatformContext and GetPlatformContext", func(t *testing.T) {
		pc := NewPlatformContext("req-456")
		pc.UserID = "user123"
		pc.ToolName = mcpTestToolName

		ctx := WithPlatformContext(context.Background(), pc)
		got := GetPlatformContext(ctx)

		if got == nil {
			t.Fatal("GetPlatformContext() returned nil")
		}
		if got.UserID != "user123" {
			t.Errorf("UserID = %q, want %q", got.UserID, "user123")
		}
		if got.ToolName != mcpTestToolName {
			t.Errorf("ToolName = %q, want %q", got.ToolName, mcpTestToolName)
		}
	})

	t.Run("GetPlatformContext not set", func(t *testing.T) {
		ctx := context.Background()
		got := GetPlatformContext(ctx)
		if got != nil {
			t.Error("GetPlatformContext() expected nil for empty context")
		}
	})

	t.Run("mustGetPlatformContext panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("mustGetPlatformContext() expected panic")
			}
		}()
		ctx := context.Background()
		mustGetPlatformContext(ctx)
	})

	t.Run("mustGetPlatformContext succeeds", func(t *testing.T) {
		pc := NewPlatformContext("req-789")
		ctx := WithPlatformContext(context.Background(), pc)
		got := mustGetPlatformContext(ctx)
		if got.RequestID != "req-789" {
			t.Errorf("RequestID = %q, want %q", got.RequestID, "req-789")
		}
	})
}

func TestPlatformContext_DiscoveryScopeKey(t *testing.T) {
	tests := []struct {
		name     string
		userID   string
		authType string
		session  string
		want     string
	}{
		{
			name:     "distinct authenticated user preferred over session",
			userID:   "alice",
			authType: "oidc",
			session:  "sess-1",
			want:     "user:alice",
		},
		{
			name:     "oauth user (claude.ai) preferred over session",
			userID:   "alice",
			authType: "oauth",
			session:  "sess-1",
			want:     "user:alice",
		},
		{
			name:     "anonymous identity falls back to session (no collapse)",
			userID:   "anonymous",
			authType: "anonymous",
			session:  "sess-1",
			want:     "session:sess-1",
		},
		{
			name:     "noop identity falls back to session (no collapse)",
			userID:   "anonymous",
			authType: "noop",
			session:  "sess-1",
			want:     "session:sess-1",
		},
		{
			name:     "user id without auth type falls back to session (defensive)",
			userID:   "alice",
			authType: "",
			session:  "sess-1",
			want:     "session:sess-1",
		},
		{
			name:     "falls back to session when no user",
			userID:   "",
			authType: "",
			session:  "sess-1",
			want:     "session:sess-1",
		},
		{
			name:     "empty when neither is known",
			userID:   "",
			authType: "",
			session:  "",
			want:     "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pc := &PlatformContext{UserID: tc.userID, AuthType: tc.authType, SessionID: tc.session}
			if got := pc.DiscoveryScopeKey(); got != tc.want {
				t.Errorf("DiscoveryScopeKey() = %q, want %q", got, tc.want)
			}
		})
	}

	// The whole point of preferring the user: two calls that carry different
	// session IDs (a client opening a fresh session per tool call) but the same
	// authenticated user must resolve to the SAME scope key, while the raw
	// session IDs differ. This is what keeps the search-first gate from falsely
	// re-gating such a client.
	t.Run("stable across per-call session churn for one authenticated user", func(t *testing.T) {
		a := &PlatformContext{UserID: "bob", AuthType: "oauth", SessionID: "throwaway-1"}
		b := &PlatformContext{UserID: "bob", AuthType: "oauth", SessionID: "throwaway-2"}
		if a.SessionID == b.SessionID {
			t.Fatal("test setup: session IDs must differ to model churn")
		}
		if a.DiscoveryScopeKey() != b.DiscoveryScopeKey() {
			t.Errorf("same user, different sessions produced different scope keys: %q vs %q",
				a.DiscoveryScopeKey(), b.DiscoveryScopeKey())
		}
	})

	// Conversely, distinct anonymous callers (auth disabled: same "anonymous"
	// UserID, different sessions) must NOT collapse onto one scope, or a single
	// caller's search would open the gate for everyone.
	t.Run("distinct anonymous callers do not collapse", func(t *testing.T) {
		a := &PlatformContext{UserID: "anonymous", AuthType: "anonymous", SessionID: "sess-a"}
		b := &PlatformContext{UserID: "anonymous", AuthType: "anonymous", SessionID: "sess-b"}
		if a.DiscoveryScopeKey() == b.DiscoveryScopeKey() {
			t.Errorf("distinct anonymous callers collapsed onto one scope: %q", a.DiscoveryScopeKey())
		}
	})

	// Issue #859: a portal run (admin source) authenticates as the operating
	// admin, but must key on its isolated portal session id, never the admin's
	// user identity — otherwise a portal replay would advance/read the operator's
	// own agent-session gate state. The user-first rule is overridden ONLY for
	// admin source, so a normal authenticated admin (Source unset, e.g. a real
	// agent) still keys on user.
	t.Run("admin source keys on portal session, not the operator's user", func(t *testing.T) {
		portal := &PlatformContext{
			UserID: "admin-1", AuthType: "oidc", SessionID: "dpp_abc", Source: SourceAdmin,
		}
		if got, want := portal.DiscoveryScopeKey(), "session:dpp_abc"; got != want {
			t.Errorf("portal DiscoveryScopeKey() = %q, want %q", got, want)
		}
		agent := &PlatformContext{
			UserID: "admin-1", AuthType: "oidc", SessionID: "sess-1", Source: SourceMCP,
		}
		if got, want := agent.DiscoveryScopeKey(), "user:admin-1"; got != want {
			t.Errorf("agent DiscoveryScopeKey() = %q, want %q", got, want)
		}
		if portal.DiscoveryScopeKey() == agent.DiscoveryScopeKey() {
			t.Errorf("portal run and the operator's agent session collapsed onto one scope: %q",
				portal.DiscoveryScopeKey())
		}
	})

	// Degenerate case: an admin run whose portal session id failed to mint
	// (empty SessionID) must fall to the EMPTY ungateable scope, never the
	// operator's user scope — otherwise a portal search would open that
	// operator's own agent-session gate (issue #859).
	t.Run("admin source with empty session never keys on user", func(t *testing.T) {
		pc := &PlatformContext{
			UserID: "admin-1", AuthType: "oidc", SessionID: "", Source: SourceAdmin,
		}
		if got := pc.DiscoveryScopeKey(); got != "" {
			t.Errorf("admin+empty-session DiscoveryScopeKey() = %q, want \"\" (isolated, not user:admin-1)", got)
		}
	})
}

// TestPlatformContext_RateLimitKey pins the per-user rate-limit key derivation
// (issue #929): user-first when the identity is genuinely distinct, session
// fallback for shared/anonymous identities so one caller cannot drain another's
// bucket, and empty (fail-open) when neither is known.
func TestPlatformContext_RateLimitKey(t *testing.T) {
	tests := []struct {
		name     string
		userID   string
		authType string
		session  string
		want     string
	}{
		{"distinct authenticated user keyed by user", "alice", "oidc", "sess-1", "user:alice"},
		{"oauth user keyed by user", "alice", "oauth", "sess-1", "user:alice"},
		{"anonymous falls back to session", "anonymous", "anonymous", "sess-1", "session:sess-1"},
		{"noop falls back to session", "anonymous", "noop", "sess-1", "session:sess-1"},
		{"user without auth type falls back to session", "alice", "", "sess-1", "session:sess-1"},
		{"no user falls back to session", "", "", "sess-1", "session:sess-1"},
		{"empty when neither is known (fail-open)", "", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pc := &PlatformContext{UserID: tc.userID, AuthType: tc.authType, SessionID: tc.session}
			if got := pc.RateLimitKey(); got != tc.want {
				t.Errorf("RateLimitKey() = %q, want %q", got, tc.want)
			}
		})
	}

	// Distinct anonymous callers (auth disabled: same "anonymous" UserID,
	// different sessions) must not share one bucket.
	t.Run("distinct anonymous callers do not share a bucket", func(t *testing.T) {
		a := &PlatformContext{UserID: "anonymous", AuthType: "anonymous", SessionID: "sess-a"}
		b := &PlatformContext{UserID: "anonymous", AuthType: "anonymous", SessionID: "sess-b"}
		if a.RateLimitKey() == b.RateLimitKey() {
			t.Errorf("distinct anonymous callers collapsed onto one bucket key: %q", a.RateLimitKey())
		}
	})
}

func TestTokenContext(t *testing.T) {
	t.Run("WithToken and GetToken", func(t *testing.T) {
		ctx := WithToken(context.Background(), "test-token-123")
		got := GetToken(ctx)
		if got != "test-token-123" {
			t.Errorf("GetToken() = %q, want %q", got, "test-token-123")
		}
	})

	t.Run("GetToken not set", func(t *testing.T) {
		got := GetToken(context.Background())
		if got != "" {
			t.Errorf("GetToken() = %q, want empty string", got)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		ctx := WithToken(context.Background(), "")
		got := GetToken(ctx)
		if got != "" {
			t.Errorf("GetToken() = %q, want empty string", got)
		}
	})
}

func TestSourceContext(t *testing.T) {
	t.Run("WithSource and GetSource round-trip", func(t *testing.T) {
		ctx := WithSource(context.Background(), SourceREST)
		if got := GetSource(ctx); got != SourceREST {
			t.Errorf("GetSource() = %q, want %q", got, SourceREST)
		}
	})

	t.Run("GetSource not set returns empty", func(t *testing.T) {
		if got := GetSource(context.Background()); got != "" {
			t.Errorf("GetSource() = %q, want empty", got)
		}
	})

	t.Run("resolveSource defaults to mcp", func(t *testing.T) {
		if got := resolveSource(context.Background()); got != SourceMCP {
			t.Errorf("resolveSource() = %q, want %q", got, SourceMCP)
		}
	})

	t.Run("resolveSource honors override", func(t *testing.T) {
		ctx := WithSource(context.Background(), SourceREST)
		if got := resolveSource(ctx); got != SourceREST {
			t.Errorf("resolveSource() = %q, want %q", got, SourceREST)
		}
	})
}

func TestPreAuthenticatedUserContext(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		info := &UserInfo{
			UserID:   "user-123",
			Email:    "user@example.com",
			Roles:    []string{"admin"},
			AuthType: "browser_session",
		}
		ctx := WithPreAuthenticatedUser(context.Background(), info)
		got := GetPreAuthenticatedUser(ctx)
		if got == nil {
			t.Fatal("GetPreAuthenticatedUser() returned nil")
		}
		if got.UserID != "user-123" {
			t.Errorf("UserID = %q, want %q", got.UserID, "user-123")
		}
		if got.Email != "user@example.com" {
			t.Errorf("Email = %q, want %q", got.Email, "user@example.com")
		}
		if got.AuthType != "browser_session" {
			t.Errorf("AuthType = %q, want %q", got.AuthType, "browser_session")
		}
	})

	t.Run("not set returns nil", func(t *testing.T) {
		got := GetPreAuthenticatedUser(context.Background())
		if got != nil {
			t.Error("GetPreAuthenticatedUser() expected nil for empty context")
		}
	})
}

func TestServerSessionContext(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		// We can't construct a real ServerSession (private fields), but we can
		// verify nil handling and type safety of the context helpers.
		ctx := context.Background()
		got := getServerSession(ctx)
		if got != nil {
			t.Error("expected nil for empty context")
		}
	})

	t.Run("nil session stored", func(t *testing.T) {
		ctx := withServerSession(context.Background(), (*mcp.ServerSession)(nil))
		got := getServerSession(ctx)
		if got != nil {
			t.Error("expected nil for nil *ServerSession stored in context")
		}
	})
}

func TestProgressTokenContext(t *testing.T) {
	t.Run("round-trip string token", func(t *testing.T) {
		ctx := withProgressToken(context.Background(), "tok-123")
		got := getProgressToken(ctx)
		if got != "tok-123" {
			t.Errorf("getProgressToken() = %v, want %q", got, "tok-123")
		}
	})

	t.Run("round-trip int token", func(t *testing.T) {
		ctx := withProgressToken(context.Background(), 42)
		got := getProgressToken(ctx)
		if got != 42 {
			t.Errorf("getProgressToken() = %v, want %d", got, 42)
		}
	})

	t.Run("not set returns nil", func(t *testing.T) {
		got := getProgressToken(context.Background())
		if got != nil {
			t.Errorf("getProgressToken() = %v, want nil", got)
		}
	})

	t.Run("nil token stored", func(t *testing.T) {
		ctx := withProgressToken(context.Background(), nil)
		got := getProgressToken(ctx)
		if got != nil {
			t.Errorf("getProgressToken() = %v, want nil", got)
		}
	})
}

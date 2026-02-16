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

	t.Run("MustGetPlatformContext panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("MustGetPlatformContext() expected panic")
			}
		}()
		ctx := context.Background()
		MustGetPlatformContext(ctx)
	})

	t.Run("MustGetPlatformContext succeeds", func(t *testing.T) {
		pc := NewPlatformContext("req-789")
		ctx := WithPlatformContext(context.Background(), pc)
		got := MustGetPlatformContext(ctx)
		if got.RequestID != "req-789" {
			t.Errorf("RequestID = %q, want %q", got.RequestID, "req-789")
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

func TestServerSessionContext(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		// We can't construct a real ServerSession (private fields), but we can
		// verify nil handling and type safety of the context helpers.
		ctx := context.Background()
		got := GetServerSession(ctx)
		if got != nil {
			t.Error("expected nil for empty context")
		}
	})

	t.Run("nil session stored", func(t *testing.T) {
		ctx := WithServerSession(context.Background(), (*mcp.ServerSession)(nil))
		got := GetServerSession(ctx)
		if got != nil {
			t.Error("expected nil for nil *ServerSession stored in context")
		}
	})
}

func TestProgressTokenContext(t *testing.T) {
	t.Run("round-trip string token", func(t *testing.T) {
		ctx := WithProgressToken(context.Background(), "tok-123")
		got := GetProgressToken(ctx)
		if got != "tok-123" {
			t.Errorf("GetProgressToken() = %v, want %q", got, "tok-123")
		}
	})

	t.Run("round-trip int token", func(t *testing.T) {
		ctx := WithProgressToken(context.Background(), 42)
		got := GetProgressToken(ctx)
		if got != 42 {
			t.Errorf("GetProgressToken() = %v, want %d", got, 42)
		}
	})

	t.Run("not set returns nil", func(t *testing.T) {
		got := GetProgressToken(context.Background())
		if got != nil {
			t.Errorf("GetProgressToken() = %v, want nil", got)
		}
	})

	t.Run("nil token stored", func(t *testing.T) {
		ctx := WithProgressToken(context.Background(), nil)
		got := GetProgressToken(ctx)
		if got != nil {
			t.Errorf("GetProgressToken() = %v, want nil", got)
		}
	})
}

package mcpcontext

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerSessionContext(t *testing.T) {
	t.Run("not set returns nil", func(t *testing.T) {
		got := GetServerSession(context.Background())
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

func TestReadOnlyEnforcedContext(t *testing.T) {
	t.Run("not set returns false", func(t *testing.T) {
		if IsReadOnlyEnforced(context.Background()) {
			t.Error("expected false for empty context")
		}
	})

	t.Run("set true returns true", func(t *testing.T) {
		ctx := WithReadOnlyEnforced(context.Background(), true)
		if !IsReadOnlyEnforced(ctx) {
			t.Error("expected true after WithReadOnlyEnforced(true)")
		}
	})

	t.Run("set false returns false", func(t *testing.T) {
		ctx := WithReadOnlyEnforced(context.Background(), false)
		if IsReadOnlyEnforced(ctx) {
			t.Error("expected false after WithReadOnlyEnforced(false)")
		}
	})
}

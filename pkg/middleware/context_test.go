package middleware

import (
	"context"
	"testing"
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
		pc.ToolName = "test_tool"

		ctx := WithPlatformContext(context.Background(), pc)
		got := GetPlatformContext(ctx)

		if got == nil {
			t.Fatal("GetPlatformContext() returned nil")
		}
		if got.UserID != "user123" {
			t.Errorf("UserID = %q, want %q", got.UserID, "user123")
		}
		if got.ToolName != "test_tool" {
			t.Errorf("ToolName = %q, want %q", got.ToolName, "test_tool")
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

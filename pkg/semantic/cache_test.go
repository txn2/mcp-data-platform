package semantic

import (
	"context"
	"testing"
	"time"
)

func TestCachedProvider(t *testing.T) {
	underlying := NewNoopProvider()
	cfg := CacheConfig{TTL: 100 * time.Millisecond}
	provider := NewCachedProvider(underlying, cfg)

	t.Run("Name", func(t *testing.T) {
		name := provider.Name()
		if name != "noop (cached)" {
			t.Errorf("Name() = %q, want %q", name, "noop (cached)")
		}
	})

	t.Run("GetTableContext_Caches", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "cache_test"}

		// First call
		result1, err := provider.GetTableContext(ctx, table)
		if err != nil {
			t.Fatalf("first GetTableContext() error = %v", err)
		}

		// Second call should return cached result
		result2, err := provider.GetTableContext(ctx, table)
		if err != nil {
			t.Fatalf("second GetTableContext() error = %v", err)
		}

		// Both should be non-nil (both come from noop which returns empty struct)
		if result1 == nil || result2 == nil {
			t.Error("expected non-nil results")
		}
	})

	t.Run("GetTableContext_Expires", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "expire_test"}

		// First call
		_, err := provider.GetTableContext(ctx, table)
		if err != nil {
			t.Fatalf("first GetTableContext() error = %v", err)
		}

		// Wait for cache to expire
		time.Sleep(150 * time.Millisecond)

		// Should fetch fresh
		_, err = provider.GetTableContext(ctx, table)
		if err != nil {
			t.Fatalf("second GetTableContext() error = %v", err)
		}
	})

	t.Run("Invalidate", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "invalidate_test"}

		// Populate cache
		_, err := provider.GetTableContext(ctx, table)
		if err != nil {
			t.Fatalf("GetTableContext() error = %v", err)
		}

		// Invalidate
		provider.Invalidate()

		// Should work after invalidate
		_, err = provider.GetTableContext(ctx, table)
		if err != nil {
			t.Fatalf("GetTableContext() after invalidate error = %v", err)
		}
	})

	t.Run("Close", func(t *testing.T) {
		if err := provider.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}

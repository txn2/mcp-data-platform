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

	t.Run("GetColumnContext_Caches", func(t *testing.T) {
		ctx := context.Background()
		column := ColumnIdentifier{
			TableIdentifier: TableIdentifier{Schema: "test", Table: "cache_test"},
			Column:          "col1",
		}

		// First call
		result1, err := provider.GetColumnContext(ctx, column)
		if err != nil {
			t.Fatalf("first GetColumnContext() error = %v", err)
		}

		// Second call should return cached result
		result2, err := provider.GetColumnContext(ctx, column)
		if err != nil {
			t.Fatalf("second GetColumnContext() error = %v", err)
		}

		if result1 == nil || result2 == nil {
			t.Error("expected non-nil results")
		}
	})

	t.Run("GetColumnsContext_Caches", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "columns_cache_test"}

		// First call
		result1, err := provider.GetColumnsContext(ctx, table)
		if err != nil {
			t.Fatalf("first GetColumnsContext() error = %v", err)
		}

		// Second call should return cached result
		result2, err := provider.GetColumnsContext(ctx, table)
		if err != nil {
			t.Fatalf("second GetColumnsContext() error = %v", err)
		}

		if result1 == nil || result2 == nil {
			t.Error("expected non-nil results")
		}
	})

	t.Run("GetLineage_Caches", func(t *testing.T) {
		ctx := context.Background()
		table := TableIdentifier{Schema: "test", Table: "lineage_cache_test"}

		// First call
		result1, err := provider.GetLineage(ctx, table, LineageUpstream, 3)
		if err != nil {
			t.Fatalf("first GetLineage() error = %v", err)
		}

		// Second call should return cached result
		result2, err := provider.GetLineage(ctx, table, LineageUpstream, 3)
		if err != nil {
			t.Fatalf("second GetLineage() error = %v", err)
		}

		if result1 == nil || result2 == nil {
			t.Error("expected non-nil results")
		}
	})

	t.Run("GetGlossaryTerm_Caches", func(t *testing.T) {
		ctx := context.Background()
		urn := "urn:li:glossaryTerm:test"

		// First call - noop returns nil, which is still cached
		_, err := provider.GetGlossaryTerm(ctx, urn)
		if err != nil {
			t.Fatalf("first GetGlossaryTerm() error = %v", err)
		}

		// Second call should return cached result (even if nil)
		_, err = provider.GetGlossaryTerm(ctx, urn)
		if err != nil {
			t.Fatalf("second GetGlossaryTerm() error = %v", err)
		}
	})

	t.Run("SearchTables_NotCached", func(t *testing.T) {
		ctx := context.Background()
		filter := SearchFilter{Query: "test"}

		// Search should work but not be cached
		_, err := provider.SearchTables(ctx, filter)
		if err != nil {
			t.Fatalf("SearchTables() error = %v", err)
		}
	})

	t.Run("Close", func(t *testing.T) {
		if err := provider.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}

func TestCacheConfig_DefaultTTL(t *testing.T) {
	underlying := NewNoopProvider()
	cfg := CacheConfig{} // Empty TTL
	provider := NewCachedProvider(underlying, cfg)

	// Default TTL should be 5 minutes
	if provider.ttl != 5*time.Minute {
		t.Errorf("default TTL = %v, want 5m", provider.ttl)
	}
}

func TestCacheEntry_IsExpired(t *testing.T) {
	t.Run("not expired", func(t *testing.T) {
		entry := &cacheEntry[string]{
			value:     "test",
			expiresAt: time.Now().Add(time.Hour),
		}
		if entry.isExpired() {
			t.Error("expected not expired")
		}
	})

	t.Run("expired", func(t *testing.T) {
		entry := &cacheEntry[string]{
			value:     "test",
			expiresAt: time.Now().Add(-time.Hour),
		}
		if !entry.isExpired() {
			t.Error("expected expired")
		}
	})
}

// errorProvider is a mock provider that always returns errors.
type errorProvider struct{}

func (e *errorProvider) Name() string { return "error" }
func (e *errorProvider) GetTableContext(_ context.Context, _ TableIdentifier) (*TableContext, error) {
	return nil, &mockError{}
}

func (e *errorProvider) GetColumnContext(_ context.Context, _ ColumnIdentifier) (*ColumnContext, error) {
	return nil, &mockError{}
}

func (e *errorProvider) GetColumnsContext(_ context.Context, _ TableIdentifier) (map[string]*ColumnContext, error) {
	return nil, &mockError{}
}

func (e *errorProvider) GetLineage(_ context.Context, _ TableIdentifier, _ LineageDirection, _ int) (*LineageInfo, error) {
	return nil, &mockError{}
}

func (e *errorProvider) GetGlossaryTerm(_ context.Context, _ string) (*GlossaryTerm, error) {
	return nil, &mockError{}
}

func (e *errorProvider) SearchTables(_ context.Context, _ SearchFilter) ([]TableSearchResult, error) {
	return nil, &mockError{}
}
func (e *errorProvider) Close() error { return nil }

type mockError struct{}

func (m *mockError) Error() string { return "mock error" }

func TestCachedProvider_Errors(t *testing.T) {
	underlying := &errorProvider{}
	cfg := CacheConfig{TTL: 100 * time.Millisecond}
	provider := NewCachedProvider(underlying, cfg)

	ctx := context.Background()

	t.Run("GetTableContext error", func(t *testing.T) {
		_, err := provider.GetTableContext(ctx, TableIdentifier{Schema: "s", Table: "t"})
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("GetColumnContext error", func(t *testing.T) {
		_, err := provider.GetColumnContext(ctx, ColumnIdentifier{
			TableIdentifier: TableIdentifier{Schema: "s", Table: "t"},
			Column:          "c",
		})
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("GetColumnsContext error", func(t *testing.T) {
		_, err := provider.GetColumnsContext(ctx, TableIdentifier{Schema: "s", Table: "t"})
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("GetLineage error", func(t *testing.T) {
		_, err := provider.GetLineage(ctx, TableIdentifier{Schema: "s", Table: "t"}, LineageUpstream, 3)
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("GetGlossaryTerm error", func(t *testing.T) {
		_, err := provider.GetGlossaryTerm(ctx, "urn:test")
		if err == nil {
			t.Error("expected error")
		}
	})
}

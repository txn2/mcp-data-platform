package semantic

import (
	"context"
	"testing"
	"time"
)

const (
	cacheTestTTLMs          = 100
	cacheTestSchema         = "test"
	cacheTestLineageDepth   = 3
	cacheTestDefaultTTLMin  = 5
	cacheTestNonNilResults  = "expected non-nil results"
)

func newTestCachedProvider() *CachedProvider {
	underlying := NewNoopProvider()
	cfg := CacheConfig{TTL: cacheTestTTLMs * time.Millisecond}
	return NewCachedProvider(underlying, cfg)
}

func TestCachedProvider_Name(t *testing.T) {
	provider := newTestCachedProvider()
	if name := provider.Name(); name != "noop (cached)" {
		t.Errorf("Name() = %q, want %q", name, "noop (cached)")
	}
}

func TestCachedProvider_GetTableContext_Caches(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: cacheTestSchema, Table: "cache_test"}

	result1, err := provider.GetTableContext(ctx, table)
	if err != nil {
		t.Fatalf("first GetTableContext() error = %v", err)
	}
	result2, err := provider.GetTableContext(ctx, table)
	if err != nil {
		t.Fatalf("second GetTableContext() error = %v", err)
	}
	if result1 == nil || result2 == nil {
		t.Error(cacheTestNonNilResults)
	}
}

func TestCachedProvider_GetTableContext_Expires(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: cacheTestSchema, Table: "expire_test"}

	if _, err := provider.GetTableContext(ctx, table); err != nil {
		t.Fatalf("first GetTableContext() error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := provider.GetTableContext(ctx, table); err != nil {
		t.Fatalf("second GetTableContext() error = %v", err)
	}
}

func TestCachedProvider_Invalidate(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: cacheTestSchema, Table: "invalidate_test"}

	if _, err := provider.GetTableContext(ctx, table); err != nil {
		t.Fatalf("GetTableContext() error = %v", err)
	}
	provider.Invalidate()
	if _, err := provider.GetTableContext(ctx, table); err != nil {
		t.Fatalf("GetTableContext() after invalidate error = %v", err)
	}
}

func TestCachedProvider_GetColumnContext_Caches(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()
	column := ColumnIdentifier{
		TableIdentifier: TableIdentifier{Schema: cacheTestSchema, Table: "cache_test"},
		Column:          "col1",
	}

	result1, err := provider.GetColumnContext(ctx, column)
	if err != nil {
		t.Fatalf("first GetColumnContext() error = %v", err)
	}
	result2, err := provider.GetColumnContext(ctx, column)
	if err != nil {
		t.Fatalf("second GetColumnContext() error = %v", err)
	}
	if result1 == nil || result2 == nil {
		t.Error(cacheTestNonNilResults)
	}
}

func TestCachedProvider_GetColumnsContext_Caches(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: cacheTestSchema, Table: "columns_cache_test"}

	result1, err := provider.GetColumnsContext(ctx, table)
	if err != nil {
		t.Fatalf("first GetColumnsContext() error = %v", err)
	}
	result2, err := provider.GetColumnsContext(ctx, table)
	if err != nil {
		t.Fatalf("second GetColumnsContext() error = %v", err)
	}
	if result1 == nil || result2 == nil {
		t.Error(cacheTestNonNilResults)
	}
}

func TestCachedProvider_GetLineage_Caches(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()
	table := TableIdentifier{Schema: cacheTestSchema, Table: "lineage_cache_test"}

	result1, err := provider.GetLineage(ctx, table, LineageUpstream, cacheTestLineageDepth)
	if err != nil {
		t.Fatalf("first GetLineage() error = %v", err)
	}
	result2, err := provider.GetLineage(ctx, table, LineageUpstream, cacheTestLineageDepth)
	if err != nil {
		t.Fatalf("second GetLineage() error = %v", err)
	}
	if result1 == nil || result2 == nil {
		t.Error(cacheTestNonNilResults)
	}
}

func TestCachedProvider_GetGlossaryTerm_Caches(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()
	urn := "urn:li:glossaryTerm:test"

	if _, err := provider.GetGlossaryTerm(ctx, urn); err != nil {
		t.Fatalf("first GetGlossaryTerm() error = %v", err)
	}
	if _, err := provider.GetGlossaryTerm(ctx, urn); err != nil {
		t.Fatalf("second GetGlossaryTerm() error = %v", err)
	}
}

func TestCachedProvider_SearchAndClose(t *testing.T) {
	provider := newTestCachedProvider()
	ctx := context.Background()

	if _, err := provider.SearchTables(ctx, SearchFilter{Query: "test"}); err != nil {
		t.Fatalf("SearchTables() error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestCacheConfig_DefaultTTL(t *testing.T) {
	underlying := NewNoopProvider()
	cfg := CacheConfig{} // Empty TTL
	provider := NewCachedProvider(underlying, cfg)

	// Default TTL should be 5 minutes
	if provider.ttl != cacheTestDefaultTTLMin*time.Minute {
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

func (*errorProvider) Name() string { return "error" }
func (*errorProvider) GetTableContext(_ context.Context, _ TableIdentifier) (*TableContext, error) {
	return nil, &mockError{}
}

func (*errorProvider) GetColumnContext(_ context.Context, _ ColumnIdentifier) (*ColumnContext, error) {
	return nil, &mockError{}
}

func (*errorProvider) GetColumnsContext(_ context.Context, _ TableIdentifier) (map[string]*ColumnContext, error) {
	return nil, &mockError{}
}

func (*errorProvider) GetLineage(_ context.Context, _ TableIdentifier, _ LineageDirection, _ int) (*LineageInfo, error) {
	return nil, &mockError{}
}

func (*errorProvider) GetGlossaryTerm(_ context.Context, _ string) (*GlossaryTerm, error) {
	return nil, &mockError{}
}

func (*errorProvider) SearchTables(_ context.Context, _ SearchFilter) ([]TableSearchResult, error) {
	return nil, &mockError{}
}
func (*errorProvider) Close() error { return nil }

type mockError struct{}

func (*mockError) Error() string { return "mock error" }

func TestCachedProvider_Errors(t *testing.T) {
	underlying := &errorProvider{}
	cfg := CacheConfig{TTL: cacheTestTTLMs * time.Millisecond}
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
		_, err := provider.GetLineage(ctx, TableIdentifier{Schema: "s", Table: "t"}, LineageUpstream, cacheTestLineageDepth)
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

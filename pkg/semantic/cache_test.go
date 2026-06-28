package semantic

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

const (
	cacheTestTTLMs         = 100
	cacheTestSchema        = "test"
	cacheTestLineageDepth  = 3
	cacheTestDefaultTTLMin = 5
	cacheTestNonNilResults = "expected non-nil results"
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

func TestCachedProvider_GetCuratedQueryCount_Caches(t *testing.T) {
	callCount := 0
	underlying := &countingCuratedQueryProvider{callCount: &callCount}
	cfg := CacheConfig{TTL: cacheTestTTLMs * time.Millisecond}
	provider := NewCachedProvider(underlying, cfg)
	ctx := context.Background()
	urn := "urn:li:dataset:curated_test"

	result1, err := provider.GetCuratedQueryCount(ctx, urn)
	if err != nil {
		t.Fatalf("first GetCuratedQueryCount() error = %v", err)
	}
	if result1 != 5 {
		t.Errorf("first call: expected 5, got %d", result1)
	}

	result2, err := provider.GetCuratedQueryCount(ctx, urn)
	if err != nil {
		t.Fatalf("second GetCuratedQueryCount() error = %v", err)
	}
	if result2 != 5 {
		t.Errorf("second call: expected 5, got %d", result2)
	}

	// Should have only called the underlying provider once (cached on second call)
	if callCount != 1 {
		t.Errorf("expected 1 underlying call, got %d", callCount)
	}
}

func TestCachedProvider_GetCuratedQueryCount_Expires(t *testing.T) {
	callCount := 0
	underlying := &countingCuratedQueryProvider{callCount: &callCount}
	cfg := CacheConfig{TTL: cacheTestTTLMs * time.Millisecond}
	provider := NewCachedProvider(underlying, cfg)
	ctx := context.Background()
	urn := "urn:li:dataset:expire_curated"

	if _, err := provider.GetCuratedQueryCount(ctx, urn); err != nil {
		t.Fatalf("first GetCuratedQueryCount() error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := provider.GetCuratedQueryCount(ctx, urn); err != nil {
		t.Fatalf("second GetCuratedQueryCount() error = %v", err)
	}

	// Both calls should hit the underlying provider (cache expired)
	if callCount != 2 {
		t.Errorf("expected 2 underlying calls after expiry, got %d", callCount)
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

// countingCuratedQueryProvider tracks how many times GetCuratedQueryCount is called.
type countingCuratedQueryProvider struct {
	NoopProvider
	callCount *int
}

func (c *countingCuratedQueryProvider) GetCuratedQueryCount(_ context.Context, _ string) (int, error) {
	*c.callCount++
	return 5, nil
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

func (*errorProvider) GetCuratedQueryCount(_ context.Context, _ string) (int, error) {
	return 0, &mockError{}
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

	t.Run("GetCuratedQueryCount error", func(t *testing.T) {
		_, err := provider.GetCuratedQueryCount(ctx, "urn:test")
		if err == nil {
			t.Error("expected error")
		}
	})
}

// docProvider is a Provider (via embedded NoopProvider) that also implements
// DocumentSearcher, to exercise the cache decorator's forward of the optional
// document-search capability (#692).
var errTestBrowse = errors.New("browse failed")

type docProvider struct {
	*NoopProvider
	docs         []DocumentResult
	relatedCalls int
	browseErr    error
}

func (d *docProvider) SearchDocuments(_ context.Context, _ string, _ int) ([]DocumentResult, error) {
	return d.docs, nil
}

func (d *docProvider) GetRelatedDocuments(_ context.Context, _ string) ([]DocumentResult, error) {
	d.relatedCalls++
	return d.docs, nil
}

func (d *docProvider) GetDocument(_ context.Context, urn string) (*DocumentResult, error) {
	for i := range d.docs {
		if d.docs[i].URN == urn {
			return &d.docs[i], nil
		}
	}
	return nil, fmt.Errorf("document %s: %w", urn, ErrDocumentNotFound)
}

func (d *docProvider) BrowseDocuments(_ context.Context, _, _ int) ([]DocumentResult, int, error) {
	if d.browseErr != nil {
		return nil, 0, d.browseErr
	}
	return d.docs, len(d.docs), nil
}

func TestCachedProvider_GetRelatedDocumentsCachesByURN(t *testing.T) {
	inner := &docProvider{NoopProvider: NewNoopProvider(), docs: []DocumentResult{{URN: "urn:li:document:d1"}}}
	c := NewCachedProvider(inner, CacheConfig{TTL: time.Minute})

	for range 3 {
		got, err := c.GetRelatedDocuments(context.Background(), "urn:li:dataset:(t)")
		if err != nil || len(got) != 1 || got[0].URN != "urn:li:document:d1" {
			t.Fatalf("unexpected: got=%v err=%v", got, err)
		}
	}
	// Same URN three times hits the wrapped provider once; the rest serve from cache.
	if inner.relatedCalls != 1 {
		t.Errorf("relatedCalls = %d, want 1 (cached by URN)", inner.relatedCalls)
	}
}

func TestCachedProvider_SearchDocuments(t *testing.T) {
	// Inner implements DocumentSearcher: the cache forwards it.
	inner := &docProvider{NoopProvider: NewNoopProvider(), docs: []DocumentResult{{URN: "urn:li:document:d1"}}}
	c := NewCachedProvider(inner, CacheConfig{})
	got, err := c.SearchDocuments(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].URN != "urn:li:document:d1" {
		t.Errorf("forward = %+v, want d1", got)
	}

	// The entity-keyed arm forwards through the cache the same way.
	rel, err := c.GetRelatedDocuments(context.Background(), "urn:li:dataset:(t)")
	if err != nil || len(rel) != 1 || rel[0].URN != "urn:li:document:d1" {
		t.Errorf("GetRelatedDocuments forward = %+v err=%v, want d1", rel, err)
	}

	// The single-document read forwards through the cache as well (#694).
	doc, err := c.GetDocument(context.Background(), "urn:li:document:d1")
	if err != nil || doc == nil || doc.URN != "urn:li:document:d1" {
		t.Errorf("GetDocument forward = %+v err=%v, want d1", doc, err)
	}

	// Document enumeration forwards through the cache too (#695).
	browsed, total, err := c.BrowseDocuments(context.Background(), 0, 10)
	if err != nil || total != 1 || len(browsed) != 1 || browsed[0].URN != "urn:li:document:d1" {
		t.Errorf("BrowseDocuments forward = %+v total=%d err=%v, want 1 doc, total 1", browsed, total, err)
	}
	// A browse error from the wrapped provider propagates (wrapped) through the cache.
	cErr := NewCachedProvider(&docProvider{NoopProvider: NewNoopProvider(), browseErr: errTestBrowse}, CacheConfig{})
	if _, _, err := cErr.BrowseDocuments(context.Background(), 0, 10); err == nil {
		t.Error("a browse error should propagate through the cache")
	}
	// A URN the inner does not know surfaces ErrDocumentNotFound through the cache.
	if _, err := c.GetDocument(context.Background(), "urn:li:document:missing"); !errors.Is(err, ErrDocumentNotFound) {
		t.Errorf("GetDocument(missing) err = %v, want ErrDocumentNotFound", err)
	}

	// Inner does NOT implement DocumentSearcher: the capability is absent, so the
	// search methods return nil without error (no documents source is registered)
	// and GetDocument reports not-found.
	c2 := NewCachedProvider(NewNoopProvider(), CacheConfig{})
	got2, err := c2.SearchDocuments(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got2 != nil {
		t.Errorf("expected nil for non-DocumentSearcher inner, got %+v", got2)
	}
	rel2, err := c2.GetRelatedDocuments(context.Background(), "urn:li:dataset:(t)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel2 != nil {
		t.Errorf("expected nil related docs for non-DocumentSearcher inner, got %+v", rel2)
	}
	if _, err := c2.GetDocument(context.Background(), "urn:li:document:d1"); !errors.Is(err, ErrDocumentNotFound) {
		t.Errorf("GetDocument on non-DocumentSearcher inner err = %v, want ErrDocumentNotFound", err)
	}
	if docs, total, err := c2.BrowseDocuments(context.Background(), 0, 10); err != nil || docs != nil || total != 0 {
		t.Errorf("BrowseDocuments on non-DocumentSearcher inner = %+v total=%d err=%v, want empty", docs, total, err)
	}
}

func TestDocumentSearcherFrom_UnwrapsCacheDecorator(t *testing.T) {
	// Cache wrapping a document-capable provider: capability detected, and the
	// returned searcher routes through the cache to the inner (not bypassing it).
	docInner := &docProvider{NoopProvider: NewNoopProvider(), docs: []DocumentResult{{URN: "d1"}}}
	ds, ok := DocumentSearcherFrom(NewCachedProvider(docInner, CacheConfig{}))
	if !ok {
		t.Fatal("cache over a DocumentSearcher should report the capability")
	}
	got, err := ds.SearchDocuments(context.Background(), "q", 5)
	if err != nil || len(got) != 1 || got[0].URN != "d1" {
		t.Errorf("returned searcher should route through the cache to the inner: got=%v err=%v", got, err)
	}

	// The regression guard: CachedProvider defines SearchDocuments unconditionally, so
	// a naive type-assertion would falsely succeed here. The probe must unwrap and
	// report NO capability when the wrapped provider cannot search documents.
	if _, ok := DocumentSearcherFrom(NewCachedProvider(NewNoopProvider(), CacheConfig{})); ok {
		t.Error("cache over a non-DocumentSearcher must NOT report the capability")
	}

	// Undecorated providers probe directly.
	if _, ok := DocumentSearcherFrom(docInner); !ok {
		t.Error("a raw DocumentSearcher should report the capability")
	}
	if _, ok := DocumentSearcherFrom(NewNoopProvider()); ok {
		t.Error("a raw non-DocumentSearcher must not report the capability")
	}
}

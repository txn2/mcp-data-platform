package semantic

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const defaultCacheTTL = 5 * time.Minute

// CachedProvider wraps a Provider with caching.
type CachedProvider struct {
	provider Provider
	ttl      time.Duration

	mu                sync.RWMutex
	tableCache        map[string]*cacheEntry[*TableContext]
	columnCache       map[string]*cacheEntry[*ColumnContext]
	columnsCache      map[string]*cacheEntry[map[string]*ColumnContext]
	lineageCache      map[string]*cacheEntry[*LineageInfo]
	termCache         map[string]*cacheEntry[*GlossaryTerm]
	curatedQueryCache map[string]*cacheEntry[int]
	relatedDocsCache  map[string]*cacheEntry[[]DocumentResult]
}

type cacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

func (e *cacheEntry[T]) isExpired() bool {
	return time.Now().After(e.expiresAt)
}

// CacheConfig configures the cache.
type CacheConfig struct {
	TTL time.Duration
}

// NewCachedProvider creates a caching wrapper around a provider.
func NewCachedProvider(provider Provider, cfg CacheConfig) *CachedProvider {
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = defaultCacheTTL
	}
	return &CachedProvider{
		provider:          provider,
		ttl:               ttl,
		tableCache:        make(map[string]*cacheEntry[*TableContext]),
		columnCache:       make(map[string]*cacheEntry[*ColumnContext]),
		columnsCache:      make(map[string]*cacheEntry[map[string]*ColumnContext]),
		lineageCache:      make(map[string]*cacheEntry[*LineageInfo]),
		termCache:         make(map[string]*cacheEntry[*GlossaryTerm]),
		curatedQueryCache: make(map[string]*cacheEntry[int]),
		relatedDocsCache:  make(map[string]*cacheEntry[[]DocumentResult]),
	}
}

// Name returns the underlying provider name.
func (c *CachedProvider) Name() string {
	return c.provider.Name() + " (cached)"
}

// GetTableContext retrieves table context with caching.
func (c *CachedProvider) GetTableContext(ctx context.Context, table TableIdentifier) (*TableContext, error) {
	key := table.String()

	c.mu.RLock()
	if entry, ok := c.tableCache[key]; ok && !entry.isExpired() {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	result, err := c.provider.GetTableContext(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("getting table context from provider: %w", err)
	}

	c.mu.Lock()
	c.tableCache[key] = &cacheEntry[*TableContext]{
		value:     result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}

// GetColumnContext retrieves column context with caching.
func (c *CachedProvider) GetColumnContext(ctx context.Context, column ColumnIdentifier) (*ColumnContext, error) {
	key := column.String()

	c.mu.RLock()
	if entry, ok := c.columnCache[key]; ok && !entry.isExpired() {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	result, err := c.provider.GetColumnContext(ctx, column)
	if err != nil {
		return nil, fmt.Errorf("getting column context from provider: %w", err)
	}

	c.mu.Lock()
	c.columnCache[key] = &cacheEntry[*ColumnContext]{
		value:     result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}

// GetColumnsContext retrieves columns context with caching.
func (c *CachedProvider) GetColumnsContext(ctx context.Context, table TableIdentifier) (map[string]*ColumnContext, error) {
	key := table.String()

	c.mu.RLock()
	if entry, ok := c.columnsCache[key]; ok && !entry.isExpired() {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	result, err := c.provider.GetColumnsContext(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("getting columns context from provider: %w", err)
	}

	c.mu.Lock()
	c.columnsCache[key] = &cacheEntry[map[string]*ColumnContext]{
		value:     result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}

// GetLineage retrieves lineage with caching.
func (c *CachedProvider) GetLineage(ctx context.Context, table TableIdentifier, direction LineageDirection, maxDepth int) (*LineageInfo, error) {
	key := table.String() + ":" + string(direction) + ":" + strconv.Itoa(maxDepth)

	c.mu.RLock()
	if entry, ok := c.lineageCache[key]; ok && !entry.isExpired() {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	result, err := c.provider.GetLineage(ctx, table, direction, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("getting lineage from provider: %w", err)
	}

	c.mu.Lock()
	c.lineageCache[key] = &cacheEntry[*LineageInfo]{
		value:     result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}

// GetGlossaryTerm retrieves a glossary term with caching.
func (c *CachedProvider) GetGlossaryTerm(ctx context.Context, urn string) (*GlossaryTerm, error) {
	c.mu.RLock()
	if entry, ok := c.termCache[urn]; ok && !entry.isExpired() {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	result, err := c.provider.GetGlossaryTerm(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting glossary term from provider: %w", err)
	}

	c.mu.Lock()
	c.termCache[urn] = &cacheEntry[*GlossaryTerm]{
		value:     result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}

// GetCuratedQueryCount retrieves curated query count with caching.
func (c *CachedProvider) GetCuratedQueryCount(ctx context.Context, urn string) (int, error) {
	c.mu.RLock()
	if entry, ok := c.curatedQueryCache[urn]; ok && !entry.isExpired() {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	result, err := c.provider.GetCuratedQueryCount(ctx, urn)
	if err != nil {
		return 0, fmt.Errorf("getting curated query count from provider: %w", err)
	}

	c.mu.Lock()
	c.curatedQueryCache[urn] = &cacheEntry[int]{
		value:     result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}

// SearchTables searches without caching (queries vary too much).
func (c *CachedProvider) SearchTables(ctx context.Context, filter SearchFilter) ([]TableSearchResult, error) {
	results, err := c.provider.SearchTables(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("searching tables from provider: %w", err)
	}
	return results, nil
}

// Unwrap returns the wrapped provider, so a capability probe can inspect the real
// provider behind the decorator instead of the decorator's unconditional
// pass-throughs (SearchDocuments below always exists on CachedProvider, which would
// otherwise make an optional-capability type-assertion falsely succeed).
func (c *CachedProvider) Unwrap() Provider { return c.provider }

// SearchDocuments forwards the optional document-search capability (#692) to the
// wrapped provider, preserving it through the cache decorator. A wrapped provider
// that does not implement it (e.g. a noop catalog) yields no documents, so the
// capability is absent rather than always-empty. Not cached: queries vary too much.
func (c *CachedProvider) SearchDocuments(ctx context.Context, query string, limit int) ([]DocumentResult, error) {
	ds, ok := c.provider.(DocumentSearcher)
	if !ok {
		return nil, nil
	}
	results, err := ds.SearchDocuments(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("searching documents from provider: %w", err)
	}
	return results, nil
}

// GetDocument forwards the single-document read (#694) to the wrapped provider,
// preserving the DocumentSearcher capability through the cache decorator. It is
// not cached: a fetch is an explicit, low-frequency dereference whose whole point
// is the current full body, so serving a stale cached copy (e.g. just after an
// edit) would defeat it; the hot search path is what the snippet caches serve.
func (c *CachedProvider) GetDocument(ctx context.Context, urn string) (*DocumentResult, error) {
	ds, ok := c.provider.(DocumentSearcher)
	if !ok {
		return nil, fmt.Errorf("document %s: %w", urn, ErrDocumentNotFound)
	}
	doc, err := ds.GetDocument(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting document from provider: %w", err)
	}
	return doc, nil
}

// BrowseDocuments forwards the document enumeration (#695) to the wrapped provider,
// preserving the DocumentSearcher capability through the cache decorator. It is not
// cached: a browse pages a mutating corpus and reports a live total, so a stale
// cached page or count would be worse than a fresh round trip. A wrapped provider
// without the capability yields an empty page and a zero total.
func (c *CachedProvider) BrowseDocuments(ctx context.Context, offset, limit int) ([]DocumentResult, int, error) {
	ds, ok := c.provider.(DocumentSearcher)
	if !ok {
		return nil, 0, nil
	}
	docs, total, err := ds.BrowseDocuments(ctx, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("browsing documents from provider: %w", err)
	}
	return docs, total, nil
}

// GetRelatedDocuments forwards the entity-keyed document lookup (#692) to the wrapped
// provider, preserving the DocumentSearcher capability through the cache decorator.
// It is keyed on a single entity URN (like GetGlossaryTerm/GetTableContext), so it is
// cached by URN: lineage expansion produces overlapping URN sets across successive
// searches, and serving repeats from cache spares the DataHub round trip on the hot
// search path.
func (c *CachedProvider) GetRelatedDocuments(ctx context.Context, urn string) ([]DocumentResult, error) {
	c.mu.RLock()
	if entry, ok := c.relatedDocsCache[urn]; ok && !entry.isExpired() {
		c.mu.RUnlock()
		return entry.value, nil
	}
	c.mu.RUnlock()

	ds, ok := c.provider.(DocumentSearcher)
	if !ok {
		return nil, nil
	}
	results, err := ds.GetRelatedDocuments(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting related documents from provider: %w", err)
	}

	c.mu.Lock()
	c.relatedDocsCache[urn] = &cacheEntry[[]DocumentResult]{
		value:     results,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return results, nil
}

// Close closes the underlying provider.
func (c *CachedProvider) Close() error {
	if err := c.provider.Close(); err != nil {
		return fmt.Errorf("closing provider: %w", err)
	}
	return nil
}

// Invalidate clears the cache.
func (c *CachedProvider) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tableCache = make(map[string]*cacheEntry[*TableContext])
	c.columnCache = make(map[string]*cacheEntry[*ColumnContext])
	c.columnsCache = make(map[string]*cacheEntry[map[string]*ColumnContext])
	c.lineageCache = make(map[string]*cacheEntry[*LineageInfo])
	c.termCache = make(map[string]*cacheEntry[*GlossaryTerm])
	c.curatedQueryCache = make(map[string]*cacheEntry[int])
	c.relatedDocsCache = make(map[string]*cacheEntry[[]DocumentResult])
}

// Verify interface compliance.
var _ Provider = (*CachedProvider)(nil)

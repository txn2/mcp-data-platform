package semantic

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// CachedProvider wraps a Provider with caching.
type CachedProvider struct {
	provider Provider
	ttl      time.Duration

	mu           sync.RWMutex
	tableCache   map[string]*cacheEntry[*TableContext]
	columnCache  map[string]*cacheEntry[*ColumnContext]
	columnsCache map[string]*cacheEntry[map[string]*ColumnContext]
	lineageCache map[string]*cacheEntry[*LineageInfo]
	termCache    map[string]*cacheEntry[*GlossaryTerm]
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
		ttl = 5 * time.Minute
	}
	return &CachedProvider{
		provider:     provider,
		ttl:          ttl,
		tableCache:   make(map[string]*cacheEntry[*TableContext]),
		columnCache:  make(map[string]*cacheEntry[*ColumnContext]),
		columnsCache: make(map[string]*cacheEntry[map[string]*ColumnContext]),
		lineageCache: make(map[string]*cacheEntry[*LineageInfo]),
		termCache:    make(map[string]*cacheEntry[*GlossaryTerm]),
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
		return nil, err
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
		return nil, err
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
		return nil, err
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
		return nil, err
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
		return nil, err
	}

	c.mu.Lock()
	c.termCache[urn] = &cacheEntry[*GlossaryTerm]{
		value:     result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}

// SearchTables searches without caching (queries vary too much).
func (c *CachedProvider) SearchTables(ctx context.Context, filter SearchFilter) ([]TableSearchResult, error) {
	return c.provider.SearchTables(ctx, filter)
}

// Close closes the underlying provider.
func (c *CachedProvider) Close() error {
	return c.provider.Close()
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
}

// Verify interface compliance.
var _ Provider = (*CachedProvider)(nil)

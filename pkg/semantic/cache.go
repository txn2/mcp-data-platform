package semantic

import (
	"context"
	"errors"
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
}

type cacheEntry[T any] struct {
	value T
	// absent records that the provider reported the key as one it holds no
	// entity for; value is the zero value and the read replays ErrNotFound. A
	// by-URN read that misses is as durable an answer as one that hits, and
	// costs the catalog the same to produce, so it is remembered for the same
	// TTL (#1610). Only ErrNotFound is remembered: a transport or authorization
	// failure says nothing about what the catalog holds.
	absent    bool
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
	}
}

// Name returns the underlying provider name.
func (c *CachedProvider) Name() string {
	return c.provider.Name() + " (cached)"
}

// cachedRead reads one entry, reporting a remembered miss as ErrNotFound. hit
// is false when nothing valid is held for the key, in which case the caller
// reads through to the provider. The cache map is passed in, which is safe
// because no field holding one is ever reassigned after construction (see
// Invalidate).
func cachedRead[T any](c *CachedProvider, cache map[string]*cacheEntry[T], key string) (value T, hit bool, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := cache[key]
	if !ok || entry.isExpired() {
		return value, false, nil
	}
	if entry.absent {
		return value, true, fmt.Errorf("no entity for %s: %w", key, ErrNotFound)
	}
	return entry.value, true, nil
}

// cacheWrite records a provider answer, whether it was an entity or a miss.
func cacheWrite[T any](c *CachedProvider, cache map[string]*cacheEntry[T], key string, value T, absent bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cache[key] = &cacheEntry[T]{
		value:     value,
		absent:    absent,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// GetTableContext retrieves table context with caching.
func (c *CachedProvider) GetTableContext(ctx context.Context, table TableIdentifier) (*TableContext, error) {
	key := table.String()

	if value, hit, err := cachedRead(c, c.tableCache, key); hit {
		return value, err
	}

	result, err := c.provider.GetTableContext(ctx, table)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			cacheWrite(c, c.tableCache, key, (*TableContext)(nil), true)
		}
		return nil, fmt.Errorf("getting table context from provider: %w", err)
	}

	cacheWrite(c, c.tableCache, key, result, false)

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
	if value, hit, err := cachedRead(c, c.termCache, urn); hit {
		return value, err
	}

	result, err := c.provider.GetGlossaryTerm(ctx, urn)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			cacheWrite(c, c.termCache, urn, (*GlossaryTerm)(nil), true)
		}
		return nil, fmt.Errorf("getting glossary term from provider: %w", err)
	}

	cacheWrite(c, c.termCache, urn, result, false)

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

// Close closes the underlying provider.
func (c *CachedProvider) Close() error {
	if err := c.provider.Close(); err != nil {
		return fmt.Errorf("closing provider: %w", err)
	}
	return nil
}

// Invalidate clears the cache. It empties each map rather than replacing it, so
// the map values themselves are fixed at construction: cachedRead and
// cacheWrite take one as an argument, which the caller reads outside the lock,
// and a reassignment here would race with that read.
func (c *CachedProvider) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.tableCache)
	clear(c.columnCache)
	clear(c.columnsCache)
	clear(c.lineageCache)
	clear(c.termCache)
	clear(c.curatedQueryCache)
}

// Verify interface compliance.
var _ Provider = (*CachedProvider)(nil)

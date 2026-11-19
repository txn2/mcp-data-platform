// Package graphcache keeps derived script graphs: a least-recently-used map
// keyed by the digest of what a graph was derived from (scriptflow).
package graphcache

import (
	"container/list"
	"crypto/sha256"
	"sort"
	"sync"
)

// Key is the digest a value is kept under.
type Key = [sha256.Size]byte

// KeyOf is the digest of source and each named part, the parts in name order,
// so the same inputs always give the same key.
func KeyOf(source string, parts map[string]string) Key {
	if len(parts) == 0 {
		return sha256.Sum256([]byte(source))
	}
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	_, _ = h.Write([]byte(source))
	for _, n := range names {
		part := sha256.Sum256([]byte(parts[n]))
		_, _ = h.Write([]byte("\x00" + n + "\x00"))
		_, _ = h.Write(part[:])
	}
	var k Key
	copy(k[:], h.Sum(nil))
	return k
}

type entry[V any] struct {
	key   Key
	value V
}

// Cache is a least-recently-used map of values by key.
type Cache[V any] struct {
	mu    sync.Mutex
	limit int
	order *list.List
	byKey map[Key]*list.Element
}

// New is a cache keeping at most limit values.
func New[V any](limit int) *Cache[V] {
	return &Cache[V]{limit: limit, order: list.New(), byKey: map[Key]*list.Element{}}
}

// Get is the value kept under key, marking it the most recently used.
func (c *Cache[V]) Get(key Key) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	e, _ := el.Value.(entry[V])
	return e.value, true
}

// Put keeps value under key, dropping the least recently used past the limit.
func (c *Cache[V]) Put(key Key, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		c.order.MoveToFront(el)
		return
	}
	c.byKey[key] = c.order.PushFront(entry[V]{key: key, value: value})
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		e, _ := oldest.Value.(entry[V])
		delete(c.byKey, e.key)
		c.order.Remove(oldest)
	}
}

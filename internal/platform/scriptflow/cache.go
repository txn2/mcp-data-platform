package scriptflow

import (
	"container/list"
	"crypto/sha256"
	"sync"
)

// cacheSize is how many distinct sources' graphs are kept. A graph is a few
// kilobytes, and the sources a portal reads repeatedly are the latest version
// of each script someone has open.
const cacheSize = 256

// cache holds derived graphs by source digest. A graph depends on the source
// alone, so a hit is exact and nothing ever invalidates an entry; the bound is
// the only reason one leaves.
var cache = newGraphCache(cacheSize)

// digest keys the cache: the SHA-256 of a source.
type digest = [sha256.Size]byte

type cacheEntry struct {
	key   digest
	graph Graph
}

// graphCache is a least-recently-used map of graphs.
type graphCache struct {
	mu    sync.Mutex
	limit int
	order *list.List
	byKey map[digest]*list.Element
}

func newGraphCache(limit int) *graphCache {
	return &graphCache{limit: limit, order: list.New(), byKey: map[digest]*list.Element{}}
}

func (c *graphCache) get(key digest) (Graph, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		return Graph{}, false
	}
	c.order.MoveToFront(el)
	entry, _ := el.Value.(cacheEntry)
	return entry.graph, true
}

func (c *graphCache) put(key digest, g Graph) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		c.order.MoveToFront(el)
		return
	}
	c.byKey[key] = c.order.PushFront(cacheEntry{key: key, graph: g})
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		entry, _ := oldest.Value.(cacheEntry)
		delete(c.byKey, entry.key)
		c.order.Remove(oldest)
	}
}

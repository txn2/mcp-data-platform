package headless

import (
	"container/list"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Bounds on the public responses a renderer keeps between renders (#2122).
const (
	// publicCacheTTL is how long a public response is reused. A library of
	// documents on one CDN is drawn in one pass of the worker, which is the
	// reuse this exists for; a CDN file that changes is fetched again by the
	// next pass.
	publicCacheTTL = 10 * time.Minute
	// publicCacheBytes bounds the bodies kept, oldest dropped first.
	publicCacheBytes = 64 << 20
)

// publicCache keeps the public responses a document fetched, so the next
// document naming the same file is answered without fetching it again: a
// library of documents styled from one CDN used to fetch each stylesheet,
// script and module once per tile, at worker concurrency (#2122).
//
// Only a complete 200 answer to a GET is kept, and never one its server marked
// no-store or private. A response that reflected the requesting origin in
// Access-Control-Allow-Origin is reflected for the next requester too: every
// render has its own origin, and the server would have answered that one the
// same way.
type publicCache struct {
	ttl      time.Duration
	maxBytes int
	now      func() time.Time

	mu    sync.Mutex
	size  int
	order *list.List // of *cachedPublic, oldest at the front
	byKey map[string]*list.Element
}

// cachedPublic is one kept response.
type cachedPublic struct {
	key       string
	stored    time.Time
	headers   map[string]string
	body      []byte
	reflected bool
}

func newPublicCache() *publicCache {
	return &publicCache{
		ttl: publicCacheTTL, maxBytes: publicCacheBytes, now: time.Now,
		order: list.New(), byKey: map[string]*list.Element{},
	}
}

// cacheKey is what decides a public response: its URL and the request headers
// a server chooses a representation by. Origin is not among them: it is
// reflected rather than matched.
func cacheKey(p pausedRequest) (string, bool) {
	if p.Request.Method != http.MethodGet || headerValue(p.Request.Headers, "Range") != "" {
		return "", false
	}
	return p.Request.URL + "\n" + headerValue(p.Request.Headers, "Accept") + "\n" +
		headerValue(p.Request.Headers, "Accept-Language") + "\n" + headerValue(p.Request.Headers, "User-Agent"), true
}

// get returns the kept reply for a request, with its CORS origin answered for
// this requester, or false.
func (c *publicCache) get(p pausedRequest) (reply, bool) {
	key, ok := cacheKey(p)
	if c == nil || !ok {
		return reply{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		return reply{}, false
	}
	e, ok := el.Value.(*cachedPublic)
	if !ok || c.now().Sub(e.stored) > c.ttl {
		c.remove(el)
		return reply{}, false
	}
	headers := make(map[string]string, len(e.headers))
	maps.Copy(headers, e.headers)
	if e.reflected {
		headers["Access-Control-Allow-Origin"] = headerValue(p.Request.Headers, "Origin")
	}
	return reply{http.StatusOK, headers, e.body}, true
}

// put keeps a reply when it may be reused.
func (c *publicCache) put(p pausedRequest, r reply, cacheControl string) {
	key, ok := cacheKey(p)
	if c == nil || !ok || r.status != http.StatusOK || len(r.body) > c.maxBytes || !reusable(cacheControl) {
		return
	}
	origin := headerValue(p.Request.Headers, "Origin")
	e := &cachedPublic{
		key: key, stored: c.now(), headers: r.headers, body: r.body,
		reflected: origin != "" && r.headers["Access-Control-Allow-Origin"] == origin,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.byKey[key]; ok {
		c.remove(old)
	}
	c.byKey[key] = c.order.PushBack(e)
	c.size += len(e.body)
	for c.size > c.maxBytes {
		c.remove(c.order.Front())
	}
}

// remove drops one kept response. The caller holds mu.
func (c *publicCache) remove(el *list.Element) {
	e, ok := c.order.Remove(el).(*cachedPublic)
	if !ok {
		return
	}
	delete(c.byKey, e.key)
	c.size -= len(e.body)
}

// reusable reports whether a response's Cache-Control lets a shared cache keep
// it.
func reusable(cacheControl string) bool {
	for d := range strings.SplitSeq(strings.ToLower(cacheControl), ",") {
		switch strings.TrimSpace(d) {
		case "no-store", "private":
			return false
		}
	}
	return true
}

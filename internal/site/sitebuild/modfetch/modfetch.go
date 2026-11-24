// Package modfetch reads the modules a site release bundles at publish (#2774):
// by URL, through the egress guard, kept in the replica's memory by URL. It is
// the network half of a site build; internal/site/sitebuild reads the tree.
package modfetch

import (
	"container/list"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/outbound"
)

// What a fetch may take, and what the cache keeps.
const (
	fetchTimeout    = 30 * time.Second
	maxModuleBytes  = int64(8 << 20)
	maxCachedBytes  = int64(64 << 20)
	acceptedModules = "text/javascript, application/javascript, text/css;q=0.9, */*;q=0.1"
)

// HTTPFetcher reads modules by URL through a client the caller builds (with
// the egress guard under it), keeping what it read by URL: a URL a release may
// import names an exact version, so what it serves does not change.
type HTTPFetcher struct {
	client *http.Client

	mu    sync.Mutex
	order *list.List
	byURL map[string]*list.Element
	held  int64
}

type cached struct {
	url         string
	body        []byte
	contentType string
}

// NewGuardedFetcher is the fetcher a deployment publishes with: a client whose
// every dial passes the egress guard, so a URL a release names cannot reach a
// private, loopback, link-local or metadata address, and which follows no
// redirect.
func NewGuardedFetcher() (*HTTPFetcher, error) {
	guard, err := egressguard.New(nil)
	if err != nil {
		return nil, fmt.Errorf("building the egress guard: %w", err)
	}
	return NewHTTPFetcher(outbound.NewClient(outbound.Options{Kind: outbound.KindSite, Base: guard.Transport(), Timeout: fetchTimeout})), nil
}

// NewHTTPFetcher is a fetcher over client.
func NewHTTPFetcher(client *http.Client) *HTTPFetcher {
	return &HTTPFetcher{client: client, order: list.New(), byURL: map[string]*list.Element{}}
}

// Fetch reads one module, from the cache when it holds it.
func (f *HTTPFetcher) Fetch(ctx context.Context, u string) (body []byte, contentType string, err error) {
	if c, ok := f.lookup(u); ok {
		return c.body, c.contentType, nil
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Accept", acceptedModules)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("requesting the module: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("the server answered %s", resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxModuleBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading the module: %w", err)
	}
	if int64(len(body)) > maxModuleBytes {
		return nil, "", fmt.Errorf("the module is more than %d bytes", maxModuleBytes)
	}
	contentType = resp.Header.Get("Content-Type")
	f.store(cached{url: u, body: body, contentType: contentType})
	return body, contentType, nil
}

func (f *HTTPFetcher) lookup(u string) (cached, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	el, ok := f.byURL[u]
	if !ok {
		return cached{}, false
	}
	f.order.MoveToFront(el)
	c, _ := el.Value.(cached)
	return c, true
}

func (f *HTTPFetcher) store(c cached) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byURL[c.url]; ok {
		return
	}
	f.byURL[c.url] = f.order.PushFront(c)
	f.held += int64(len(c.body))
	for f.held > maxCachedBytes && f.order.Len() > 1 {
		last := f.order.Back()
		old, _ := last.Value.(cached)
		f.order.Remove(last)
		delete(f.byURL, old.url)
		f.held -= int64(len(old.body))
	}
}

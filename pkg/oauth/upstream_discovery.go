package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/txn2/mcp-data-platform/pkg/oidcdiscovery"
)

// discoveryFetchTimeout bounds a single upstream discovery fetch. The fetch runs
// on a detached context (see resolveDocument) so one caller canceling its
// request cannot abort a fetch other concurrent callers are waiting on; this
// timeout is what keeps a hung IdP from pinning those callers indefinitely.
const discoveryFetchTimeout = 10 * time.Second

// upstreamEndpointResolver resolves the upstream IdP's authorization and token
// endpoints. Each endpoint is resolved INDEPENDENTLY: an explicitly configured
// endpoint is returned without any discovery, so a discovery outage cannot block
// a path whose endpoint is already known from config (e.g. an explicit
// authorization_endpoint still lets /authorize redirect while token discovery is
// down).
//
// A successful discovery document is cached for the process lifetime; a failure
// is not cached, so the next call retries and a request-time failure surfaces as
// the retryable server_error the package uses for transient upstream outages,
// rather than a hardcoded Keycloak-path fallback. Concurrent cold-cache fetches
// collapse into one via single-flight.
//
// The fetch/parse is shared with pkg/auth's JWKS discovery through the
// pkg/oidcdiscovery leaf package (pkg/auth transitively imports pkg/oauth, so a
// helper in pkg/auth would form an import cycle; a shared leaf breaks none).
type upstreamEndpointResolver struct {
	issuer        string
	explicitAuth  string
	explicitToken string
	httpClient    *http.Client

	mu    sync.RWMutex
	doc   *oidcdiscovery.Document
	group singleflight.Group
}

// newUpstreamEndpointResolver builds a resolver from the upstream IdP config.
// Explicit endpoints are trimmed; empty means "discover".
func newUpstreamEndpointResolver(cfg *UpstreamConfig, client *http.Client) *upstreamEndpointResolver {
	return &upstreamEndpointResolver{
		issuer:        cfg.Issuer,
		explicitAuth:  strings.TrimSpace(cfg.AuthorizationEndpoint),
		explicitToken: strings.TrimSpace(cfg.TokenEndpoint),
		httpClient:    client,
	}
}

// authorizationEndpoint returns the upstream authorization endpoint, using the
// explicit config value when set and otherwise discovering it. Discovery is not
// performed when the endpoint is explicitly configured.
func (r *upstreamEndpointResolver) authorizationEndpoint(ctx context.Context) (string, error) {
	if r.explicitAuth != "" {
		return r.explicitAuth, nil
	}
	doc, err := r.resolveDocument(ctx)
	if err != nil {
		return "", err
	}
	if doc.AuthorizationEndpoint == "" {
		return "", errors.New("authorization_endpoint not found in discovery document")
	}
	return doc.AuthorizationEndpoint, nil
}

// tokenEndpoint returns the upstream token endpoint, using the explicit config
// value when set and otherwise discovering it. Discovery is not performed when
// the endpoint is explicitly configured.
func (r *upstreamEndpointResolver) tokenEndpoint(ctx context.Context) (string, error) {
	if r.explicitToken != "" {
		return r.explicitToken, nil
	}
	doc, err := r.resolveDocument(ctx)
	if err != nil {
		return "", err
	}
	if doc.TokenEndpoint == "" {
		return "", errors.New("token_endpoint not found in discovery document")
	}
	return doc.TokenEndpoint, nil
}

// resolveDocument returns the cached discovery document or fetches it once. A
// successful fetch is cached for the process lifetime; a failure is not cached.
// Concurrent cold-cache callers collapse into a single fetch via single-flight.
func (r *upstreamEndpointResolver) resolveDocument(ctx context.Context) (*oidcdiscovery.Document, error) {
	r.mu.RLock()
	doc := r.doc
	r.mu.RUnlock()
	if doc != nil {
		return doc, nil
	}

	// The fetch runs on a detached, bounded context so a single caller canceling
	// its request cannot poison the shared fetch for the other collapsed callers.
	ch := r.group.DoChan("discover", func() (any, error) {
		r.mu.RLock()
		cached := r.doc
		r.mu.RUnlock()
		if cached != nil {
			return cached, nil
		}

		fctx, cancel := context.WithTimeout(context.Background(), discoveryFetchTimeout)
		defer cancel()
		fetched, err := oidcdiscovery.Fetch(fctx, r.httpClient, r.issuer)
		if err != nil {
			return nil, fmt.Errorf("resolving upstream discovery document: %w", err)
		}

		r.mu.Lock()
		r.doc = fetched
		r.mu.Unlock()
		return fetched, nil
	})

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("awaiting upstream discovery: %w", ctx.Err())
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err // already wrapped inside the fetch closure
		}
		doc, ok := res.Val.(*oidcdiscovery.Document)
		if !ok {
			return nil, fmt.Errorf("unexpected upstream discovery result type %T", res.Val)
		}
		return doc, nil
	}
}

package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// Result ranking modes, reported so the caller knows how results were ranked:
// semantically, by keyword, or by exact entity lookup (no text arm).
const (
	rankingHybrid  = "hybrid"
	rankingLexical = "lexical"
	rankingEntity  = "entity"
)

// Limit bounds for a knowledge search.
const (
	defaultLimit = 10
	maxLimit     = 50
)

// Result is one knowledge search response: the fused, ranked hits and the
// ranking mode used to produce them.
type Result struct {
	Hits    []Hit
	Ranking string
}

// Router fans one query across every registered provider, normalizes each
// provider's local relevance scores onto a common scale, fuses them into one
// ranked list, and enforces per-user scope. It is the single read path behind
// both the knowledge_search tool and (later) push injection, so the scope and
// fusion rules live here once rather than in each surface.
type Router struct {
	embedder  embedding.Provider
	providers []Provider
}

// NewRouter builds a router over an embedder and a set of providers. The
// embedder may be nil or the noop placeholder; the router then ranks
// lexically. Provider order does not affect ranking (scores are fused), only
// the deterministic tie-break.
func NewRouter(embedder embedding.Provider, providers ...Provider) *Router {
	return &Router{embedder: embedder, providers: providers}
}

// Providers returns the registered providers, for introspection and wiring
// checks.
func (r *Router) Providers() []Provider { return r.providers }

// clampLimit constrains the per-provider result limit to valid bounds.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// Search runs one knowledge search from a caller-built Query. It embeds the
// intent once (when present) and shares the vector across providers, queries
// every shared provider plus every per-user provider for which the caller
// carries an identity, fuses the results, and trims to limit. The query may be
// text-based (Intent), entity-keyed (EntityURNs), or both; each provider uses
// the parts it supports.
//
// Provider failures are tolerated: a single provider erroring is logged and its
// results omitted, so one unhealthy store does not blank the whole search. An
// error is returned only when every queried provider failed, so an all-stores-
// down condition is not reported as an empty-but-successful result.
func (r *Router) Search(ctx context.Context, q Query) (Result, error) {
	q.Intent = strings.TrimSpace(q.Intent)
	q.Limit = clampLimit(q.Limit)

	ranking := rankingEntity
	if q.Intent != "" {
		q.Embedding = embedding.EmbedForSearch(ctx, r.embedder, q.Intent)
		if len(q.Embedding) > 0 {
			ranking = rankingHybrid
		} else {
			ranking = rankingLexical
		}
	}

	perProvider, attempted, errs := r.fanOut(ctx, q)

	// Every queried provider failed: surface the failure rather than an empty
	// success.
	if attempted > 0 && len(errs) == attempted {
		return Result{Ranking: ranking}, fmt.Errorf("all knowledge providers failed: %w", errors.Join(errs...))
	}

	fused := normalizeAndFuse(perProvider)
	if len(fused) > q.Limit {
		fused = fused[:q.Limit]
	}
	return Result{Hits: fused, Ranking: ranking}, nil
}

// fanOut queries every applicable provider with the prepared query, returning
// each provider's hits, the number of providers actually queried, and any
// errors. Per-user providers are skipped for an anonymous caller (the secure
// default, not an error); a provider error is logged and collected so a single
// unhealthy store does not blank the search.
func (r *Router) fanOut(ctx context.Context, q Query) (perProvider [][]Hit, attempted int, errs []error) {
	for _, p := range r.providers {
		if p.Scope() == ScopePerUser && q.Caller.Anonymous() {
			continue
		}
		attempted++
		hits, err := p.Search(ctx, q)
		if err != nil {
			slog.Warn("knowledge provider search failed", "provider", p.Name(), "error", err)
			errs = append(errs, err)
			continue
		}
		if len(hits) > 0 {
			perProvider = append(perProvider, hits)
		}
	}
	return perProvider, attempted, errs
}

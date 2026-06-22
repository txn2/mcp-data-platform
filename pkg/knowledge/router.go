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

	// candidateLimitPerSource is how many ranked candidates each provider
	// returns to the allocator, independent of the display budget. It is
	// larger than a typical display budget so the allocator has material to
	// balance across sources and so coverage counts ("14 datasets matched")
	// are meaningful beyond the few that are shown. Matched counts are capped
	// at this value.
	candidateLimitPerSource = 25
)

// Result is one knowledge search response: the balanced, grouped-by-source
// display set, the coverage summary (per-source matched vs shown counts so the
// agent sees breadth beyond what is displayed), and the ranking mode used to
// produce it.
type Result struct {
	Groups   []SourceGroup
	Coverage []SourceCoverage
	Ranking  string
}

// LineageExpander optionally widens a set of entity URNs along lineage so an
// entity-keyed lookup also recalls knowledge about upstream and downstream
// datasets (the old memory_recall "graph" strategy). Implemented by an adapter
// over the semantic provider; a nil expander disables expansion, leaving a
// plain entity lookup.
//
// It lives on the Router, not on any single provider, so the expansion runs
// once per search and every entity-keyed provider (memory, insights, the
// technical catalog) sees the same widened URN set, the same way the query
// embedding is computed once and shared.
type LineageExpander interface {
	Expand(ctx context.Context, urns []string) []string
}

// Router fans one query across every registered provider, normalizes each
// provider's local relevance scores onto a common scale, fuses them into one
// ranked list, and enforces per-user scope. It is the single read path behind
// both the search tool and (later) push injection, so the scope and
// fusion rules live here once rather than in each surface.
type Router struct {
	embedder  embedding.Provider
	lineage   LineageExpander
	providers []Provider
}

// NewRouter builds a router over an embedder, an optional lineage expander, and
// a set of providers. The embedder may be nil or the noop placeholder; the
// router then ranks lexically. lineage may be nil, leaving entity-keyed lookups
// unexpanded. Provider order does not affect ranking (scores are fused), only
// the deterministic tie-break.
func NewRouter(embedder embedding.Provider, lineage LineageExpander, providers ...Provider) *Router {
	return &Router{embedder: embedder, lineage: lineage, providers: providers}
}

// Providers returns the registered providers, for introspection and wiring
// checks.
func (r *Router) Providers() []Provider { return r.providers }

// sourceSet builds a lookup of the requested source names, trimming and
// lower-casing each, or returns nil when no narrowing was requested (the
// default: query every accessible provider). A set with only blank entries
// also collapses to nil so an all-empty Sources does not silently match
// nothing.
func sourceSet(sources []string) map[string]bool {
	set := make(map[string]bool, len(sources))
	for _, s := range sources {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			set[s] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

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
	// The caller's limit is the display budget for the balanced set; each
	// provider returns a deeper candidate list so the allocator can balance
	// and so coverage counts mean something beyond what is shown.
	displayBudget := clampLimit(q.Limit)
	q.Limit = candidateLimitPerSource

	ranking := rankingEntity
	if q.Intent != "" {
		q.Embedding = embedding.EmbedForSearch(ctx, r.embedder, q.Intent)
		if len(q.Embedding) > 0 {
			ranking = rankingHybrid
		} else {
			ranking = rankingLexical
		}
	}

	// Widen the entity-keyed lookup along lineage once, so every entity-keyed
	// provider fans out over the same upstream/downstream neighbors rather than
	// each re-expanding (which would re-hit the catalog lineage API per source).
	if len(q.EntityURNs) > 0 && r.lineage != nil {
		q.EntityURNs = r.lineage.Expand(ctx, q.EntityURNs)
	}

	perProvider, attempted, errs := r.fanOut(ctx, q)

	// Every queried provider failed: surface the failure rather than an empty
	// success.
	if attempted > 0 && len(errs) == attempted {
		return Result{Ranking: ranking}, fmt.Errorf("all knowledge providers failed: %w", errors.Join(errs...))
	}

	groups, coverage := allocate(perProvider, displayBudget)
	return Result{Groups: groups, Coverage: coverage, Ranking: ranking}, nil
}

// fanOut queries every applicable provider with the prepared query, returning
// each provider's hits, the number of providers actually queried, and any
// errors. Per-user providers are skipped for an anonymous caller (the secure
// default, not an error); a provider error is logged and collected so a single
// unhealthy store does not blank the search.
func (r *Router) fanOut(ctx context.Context, q Query) (perProvider [][]Hit, attempted int, errs []error) {
	allowed := sourceSet(q.Sources)
	for _, p := range r.providers {
		// Sources narrows the federation; it never widens it. A name absent
		// from a non-empty Sources set is skipped, but membership still has to
		// clear the per-user scope check below, so narrowing can never opt a
		// caller into a provider their identity does not grant.
		if allowed != nil && !allowed[p.Name()] {
			continue
		}
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

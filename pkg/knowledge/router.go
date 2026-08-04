package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// defaultProviderSearchTimeout bounds each knowledge provider's search arm in
// the fan-out. Without a per-provider deadline the fan-out is only as fast as
// its slowest source: a WaitGroup awaits every arm, so one slow store (a lagging
// catalog index, an unreachable provider) stalls the whole search until the
// client's own timeout fires. Five seconds is well above a healthy provider's
// latency yet bounds the worst case; override with
// knowledge.search_provider_timeout.
const defaultProviderSearchTimeout = 5 * time.Second

// defaultSearchEmbedTimeout bounds the serial intent-embedding step in Search.
// It is a distinct knob from the fan-out timeout because the two have different
// failure semantics: a slow fan-out arm should drop out quickly so it does not
// hold up the others, whereas cutting the embedding short silently downgrades
// ranking from hybrid to lexical and loses semantic relevance. Keeping the same
// 5s default preserves v1.98.2 behavior byte-for-byte; operators who want to
// protect ranking quality on a slow (e.g. cold, CPU-only) embedder can raise
// knowledge.search_embed_timeout without loosening the fan-out bound. Override
// with knowledge.search_embed_timeout.
const defaultSearchEmbedTimeout = 5 * time.Second

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
	// UnknownSources lists requested Sources names that match no known source (a
	// typo or an unsupported source), so a caller is told why a narrowed search
	// returned little or nothing instead of being silently given an empty result.
	UnknownSources []string
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
	embedder        embedding.Provider
	lineage         LineageExpander
	providers       []Provider
	providerTimeout time.Duration
	embedTimeout    time.Duration
	connScope       ConnectionScope
}

// NewRouter builds a router over an embedder, an optional lineage expander, and
// a set of providers. The embedder may be nil or the noop placeholder; the
// router then ranks lexically. lineage may be nil, leaving entity-keyed lookups
// unexpanded. Provider order does not affect ranking (scores are fused), only
// the deterministic tie-break. The per-provider fan-out timeout defaults to
// defaultProviderSearchTimeout and the search-embedding timeout to
// defaultSearchEmbedTimeout; override with SetProviderTimeout and
// SetEmbedTimeout respectively.
func NewRouter(embedder embedding.Provider, lineage LineageExpander, providers ...Provider) *Router {
	return &Router{
		embedder:        embedder,
		lineage:         lineage,
		providers:       providers,
		providerTimeout: defaultProviderSearchTimeout,
		embedTimeout:    defaultSearchEmbedTimeout,
	}
}

// SetProviderTimeout overrides the per-provider fan-out deadline. A zero
// duration leaves the default in place (so callers can pass an unset config
// value unconditionally); a negative duration disables the bound (each provider
// then runs under the request context only), matching the pre-timeout behavior.
// Not safe for concurrent use with Search; call once at wiring time.
func (r *Router) SetProviderTimeout(d time.Duration) {
	if d != 0 {
		r.providerTimeout = d
	}
}

// providerContext derives the per-provider context: the request context bounded
// by providerTimeout, or the request context unchanged (with a no-op cancel)
// when the bound is disabled.
func (r *Router) providerContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.providerTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, r.providerTimeout)
}

// SetEmbedTimeout overrides the deadline on the serial search-embedding step. It
// mirrors SetProviderTimeout's semantics: a zero duration leaves the default in
// place (so callers can pass an unset config value unconditionally); a negative
// duration disables the bound (the embed then runs under the request context
// only). It is a separate knob from SetProviderTimeout so an operator can give a
// slow embedder headroom without loosening the fan-out bound. Not safe for
// concurrent use with Search; call once at wiring time.
func (r *Router) SetEmbedTimeout(d time.Duration) {
	if d != 0 {
		r.embedTimeout = d
	}
}

// SetConnectionScope binds the persona connection boundary that the topology
// sources (catalog, connections, endpoints) apply to discovery, so a caller
// never sees material belonging to a connection their persona could not reach
// (#1108). Nil (the default) leaves discovery unfiltered, which is what a
// deployment with no persona registry gets. Not safe for concurrent use with
// Search; call once at wiring time.
func (r *Router) SetConnectionScope(s ConnectionScope) {
	r.connScope = s
}

// scoped returns caller with this router's connection boundary attached and a
// fresh withheld counter, plus the gate to read the count back from. Every arm
// (each search provider, each fetch attempt) gets its own gate, so counting
// needs no synchronization even though the search fan-out is concurrent.
func (r *Router) scoped(caller Caller) (Caller, *connGate) {
	gate := &connGate{scope: r.connScope}
	caller.conn = gate
	return caller, gate
}

// embedContext derives the context for the intent-embedding call: the request
// context bounded by embedTimeout, or the request context unchanged (with a
// no-op cancel) when the bound is disabled.
func (r *Router) embedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.embedTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, r.embedTimeout)
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

// knownSourceNames is every valid search-source label (the Source* provenance
// constants), the single authority for validating a caller's Sources filter. A
// requested name absent from this set is a typo or an unsupported source; it is
// surfaced to the caller rather than silently narrowing the search to nothing.
// (A name present here but unavailable on a given deployment, e.g. memory without a
// database, is still "known": it is scope-filtered, not a typo.)
var knownSourceNames = map[string]bool{
	SourceCatalog:          true,
	SourceContextDocuments: true,
	SourceGovernance:       true,
	SourceKnowledgePages:   true,
	SourceMemory:           true,
	SourceInsights:         true,
	SourceFeedback:         true,
	SourceAssets:           true,
	SourceResources:        true,
	SourcePrompts:          true,
	SourceEndpoints:        true,
	SourceConnections:      true,
}

// KnownSources returns the valid search-source names (the Source* provenance
// constants) sorted, so other packages and their drift guards validate against one
// authority instead of a hand-maintained copy.
func KnownSources() []string {
	out := make([]string, 0, len(knownSourceNames))
	for s := range knownSourceNames {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// unknownSources returns the requested source names that match no known source,
// de-duplicated and in request order, so a caller learns a Sources entry was a typo
// or unsupported instead of silently getting an empty result. Blank entries are
// ignored (they mean "no narrowing").
func unknownSources(sources []string) []string {
	if len(sources) == 0 {
		return nil
	}
	var unknown []string
	seen := make(map[string]bool)
	for _, s := range sources {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		if !knownSourceNames[s] {
			unknown = append(unknown, s)
		}
	}
	return unknown
}

// clampInt constrains limit to [1, max], substituting def for a non-positive
// (unset) value. It is the one clamp rule shared by the search display budget and
// the browse page size, so the two cannot drift.
func clampInt(limit, def, upper int) int {
	if limit <= 0 {
		return def
	}
	if limit > upper {
		return upper
	}
	return limit
}

// clampLimit constrains the per-provider result limit to valid bounds.
func clampLimit(limit int) int { return clampInt(limit, defaultLimit, maxLimit) }

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
		// Bound the embedding call with its own timeout (separate from the fan-out
		// bound): a slow or unreachable embedder must degrade to lexical ranking,
		// not stall the whole search before the fan-out even runs. The embed step
		// is serial and downgrading it loses semantic relevance, so it gets its own
		// knob and can be given more headroom than a fan-out arm.
		embCtx, cancel := r.embedContext(ctx)
		q.Embedding = embedding.EmbedForSearch(embCtx, r.embedder, q.Intent)
		cancel()
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

	results, attempted, errs := r.fanOut(ctx, q)

	// Every queried provider failed: surface the failure rather than an empty
	// success.
	if attempted > 0 && len(errs) == attempted {
		return Result{Ranking: ranking}, fmt.Errorf("all knowledge providers failed: %w", errors.Join(errs...))
	}

	groups, coverage := allocate(results, displayBudget)
	return Result{
		Groups:         groups,
		Coverage:       coverage,
		Ranking:        ranking,
		UnknownSources: unknownSources(q.Sources),
	}, nil
}

// selectProviders returns the providers a query actually runs, in registration
// order: Sources narrows the federation (a name absent from a non-empty Sources set
// is skipped) but never widens it, and a per-user provider is skipped for an
// anonymous caller, so narrowing can never opt a caller into a provider their
// identity does not grant.
func (r *Router) selectProviders(q Query) []Provider {
	allowed := sourceSet(q.Sources)
	selected := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		if allowed != nil && !allowed[p.Name()] {
			continue
		}
		if p.Scope() == ScopePerUser && q.Caller.Anonymous() {
			continue
		}
		selected = append(selected, p)
	}
	return selected
}

// fanOut queries every applicable provider with the prepared query, returning each
// provider's hits (plus the count its connection boundary withheld), the number of
// providers actually queried, and any errors. A provider error is logged and
// collected so a single unhealthy store does not blank the search.
//
// The applicable providers are independent (each Search shares no state with the
// others, and several issue their own DB or network call), so the fan-out runs them
// concurrently: a DataHub-backed deployment otherwise pays one serial round trip per
// source. Results land in index-keyed slots so the assembled output keeps
// provider-registration order regardless of completion order, keeping the downstream
// allocation deterministic. A WaitGroup (not errgroup) is used deliberately: every
// provider must run to completion even when another errors, so one unhealthy store
// still cannot blank the search.
//
// Each arm gets its own copy of the query carrying its own connection gate, so the
// concurrent arms record their withheld counts without sharing state.
func (r *Router) fanOut(ctx context.Context, q Query) (out []sourceResult, attempted int, errs []error) {
	selected := r.selectProviders(q)
	if len(selected) == 0 {
		return nil, 0, nil
	}

	type providerResult struct {
		hits     []Hit
		withheld int
		err      error
	}
	results := make([]providerResult, len(selected))
	var wg sync.WaitGroup
	wg.Add(len(selected))
	for i := range selected {
		go func(i int, p Provider) {
			defer wg.Done()
			// A panic in a child goroutine is NOT catchable by the request handler's
			// deferred recover (that only unwinds its own stack), so without this a
			// single provider panic would crash the whole server. Recover here and
			// turn it into this provider's error: one bad provider fails its own arm
			// and is collected like any other error, while the rest still return.
			defer func() {
				if rec := recover(); rec != nil {
					results[i] = providerResult{err: fmt.Errorf("provider %s panicked: %v", p.Name(), rec)}
				}
			}()
			// Bound this arm so a single slow provider cannot stall the fan-out:
			// its call is canceled at the deadline and surfaces as this provider's
			// error (collected and logged like any other), while the remaining
			// providers still return. Providers thread this ctx into their DB and
			// network calls, so cancellation unblocks the WaitGroup promptly.
			pctx, cancel := r.providerContext(ctx)
			defer cancel()
			pq := q
			var gate *connGate
			pq.Caller, gate = r.scoped(q.Caller)
			hits, err := p.Search(pctx, pq)
			results[i] = providerResult{hits: hits, withheld: gate.withheld, err: err}
		}(i, selected[i])
	}
	wg.Wait()

	attempted = len(selected)
	for i := range selected {
		if results[i].err != nil {
			slog.Warn("knowledge provider search failed", "provider", selected[i].Name(), "error", results[i].err)
			errs = append(errs, results[i].err)
			continue
		}
		if len(results[i].hits) > 0 || results[i].withheld > 0 {
			out = append(out, sourceResult{
				source:   selected[i].Name(),
				hits:     results[i].hits,
				withheld: results[i].withheld,
			})
		}
	}
	return out, attempted, errs
}

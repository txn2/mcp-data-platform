package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// SourceCatalog is the provenance label for technical-catalog hits.
const SourceCatalog = "catalog"

// tableSearcher is the catalog capability the datahub provider needs: relevance
// search (text path) and exact entity lookup by table identifier (entity path).
// It matches semantic.Provider; declared locally so the provider depends on the
// capability and tests can supply a fake.
type tableSearcher interface {
	SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
	GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error)
}

// CatalogIndexQuery is one ranking request against the platform's own index of
// catalog datasets. A non-nil Embedding selects hybrid ranking; a nil one
// selects the lexical fallback, so a query whose intent could not be embedded
// still ranks against the index rather than dropping it.
type CatalogIndexQuery struct {
	QueryText string
	Embedding []float32
	Limit     int
}

// CatalogIndexHit is one catalog dataset the platform's own index matched: the
// URN it is cited and fetched by, the text a search hit renders, and the
// index's own relevance score.
type CatalogIndexHit struct {
	URN         string
	Name        string
	Description string
	Score       float64
}

// CatalogIndexSearcher ranks the platform's own index of catalog dataset text
// (#1131). It is what makes a fact written into a dataset's description
// reachable from a topical query that names no entity: DataHub's own search is
// keyword-only and lags its indexer, so without this a DataHub-sink fact was
// discoverable only through the entity path. The index is an accelerator over
// the same corpus, never a second source of truth — every hit it produces is
// dereferenced against DataHub by URN.
//
// Implemented by the platform's catalog-dataset index; nil on a deployment with
// no database or with the index disabled, which leaves the catalog ranked by
// DataHub's keyword search alone.
type CatalogIndexSearcher interface {
	SearchCatalogIndex(ctx context.Context, q CatalogIndexQuery) ([]CatalogIndexHit, error)
}

// CatalogProvider exposes the technical catalog (DataHub) to the router. It
// serves two query shapes: a relevance search on Intent (folding datahub_search
// into search) and an exact entity-keyed lookup on EntityURNs that returns the
// catalog entity itself, so handing search a dataset URN surfaces its catalog
// entry alongside the URN-linked memory and insights. Structured catalog
// navigation (platform/domain/tag/entity-type filters) stays in datahub_browse.
//
// It is shared: the catalog holds no per-user records, so it is queried for
// every request. Its entities are still connection-scoped (#1108): a dataset
// belongs to the connections that can serve its URN, and a caller whose persona
// is not granted any of them does not see it. A URN that maps to no connection
// stays visible — the mapping failed, not the permission check, and hiding on a
// guess would silently drop entities no connection claims.
//
// DataHub ranks search results but does not return a numeric score, so the
// provider derives a descending positional score from the result order;
// entity-keyed hits take the max score. The router's per-provider normalization
// then places these on the common scale.
type CatalogProvider struct {
	searcher tableSearcher
	index    CatalogIndexSearcher
}

// NewCatalogProvider builds the catalog provider over a catalog searcher.
func NewCatalogProvider(searcher tableSearcher) *CatalogProvider {
	return &CatalogProvider{searcher: searcher}
}

// SetIndexSearcher attaches the platform's own index of catalog dataset text to
// the text path (#1131). Nil (the default) leaves the text path ranked by
// DataHub's keyword search alone. Not safe for concurrent use with Search; call
// once at wiring time.
func (p *CatalogProvider) SetIndexSearcher(index CatalogIndexSearcher) {
	p.index = index
}

// Name returns the provenance label.
func (*CatalogProvider) Name() string { return SourceCatalog }

// Scope marks the catalog shared (global, always queried).
func (*CatalogProvider) Scope() Scope { return ScopeShared }

// Search returns catalog entities for the query: the entities named by
// EntityURNs (entity path) plus those relevant to Intent (text path), merged and
// de-duplicated by URN.
func (p *CatalogProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	return mergeArms(ctx, q, p.searchByEntity, p.searchByText)
}

// searchByEntity fetches the catalog entity for each requested URN (already
// lineage-expanded by the Router). A URN that does not parse as a dataset, or
// that the catalog cannot resolve, is skipped rather than failing the search:
// the entity set is probed across many (lineage-expanded) URNs, most of which
// legitimately have no catalog entry, so a miss must not blank the provider.
// Only entities the catalog actually returned (non-empty URN) yield a hit, so a
// non-existent URN produces nothing.
//
// The connection boundary is applied after resolution, against the URN the
// CATALOG returned rather than the one the caller passed. Those differ, and only
// the resolved one can be trusted: the lookup keys on the table identifier and
// re-derives the platform from the adapter's configuration, so a caller who
// writes a real table name under a platform no connection claims would otherwise
// make its dataset unattributable — and therefore visible — while still
// resolving it. Checking after resolution also makes the withheld count mean "an
// entity exists here that you may not see" rather than counting URNs that
// resolve to nothing.
func (p *CatalogProvider) searchByEntity(ctx context.Context, q Query, seen map[string]bool) []Hit {
	var hits []Hit
	withheld := 0
	for _, urn := range q.EntityURNs {
		if seen[urn] {
			continue
		}
		table, err := memory.ParseURNToTable(urn)
		if err != nil {
			continue
		}
		tc, err := p.searcher.GetTableContext(ctx, table)
		if err != nil {
			slog.Debug("catalog entity lookup skipped", "urn", urn, "error", err)
			continue
		}
		if tc == nil || tc.URN == "" {
			continue
		}
		// Mark the URN handled before the boundary check so the text arm does not
		// re-consider (and re-count) an entity this arm already withheld.
		seen[urn] = true
		if !q.Caller.allowsURN(tc.URN) {
			withheld++
			continue
		}
		hits = append(hits, Hit{
			Text:       catalogContextText(table, tc),
			Source:     SourceCatalog,
			Ref:        urn,
			Score:      entityMatchScore,
			EntityURNs: []string{urn},
			// A DataHub reference is its URN verbatim (the canonical citable form).
			Reference: urn,
		})
	}
	q.Caller.withhold(withheld)
	return hits
}

// catalogCandidate is one catalog entity a text arm surfaced, before scoring:
// the URN it is keyed, cited, and boundary-checked by, and the snippet a hit
// renders.
type catalogCandidate struct {
	urn  string
	text string
}

// searchByText returns catalog entities relevant to the intent, minus those
// belonging to connections the caller's persona may not reach. A query with no
// intent yields nothing.
//
// Two sources feed it: the platform's own index of dataset text (#1131) and
// DataHub's keyword search. The index leads because it ranks the same corpus
// semantically and is consistent the moment a sweep lands, so a fact applied to
// a description is reachable from a topical query that shares none of its
// words; DataHub's search contributes the recall tail, covering the fields the
// mirror does not carry (column names, glossary terms, ownership). A dataset
// both return is emitted once, at its index rank.
//
// Scores are positional over the merged order rather than each source's own
// scale: DataHub returns no numeric score at all, so a blend of the index's
// cosine-derived score with an invented one would be a false precision. The
// Router's per-provider normalization then places these on the common scale.
func (p *CatalogProvider) searchByText(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	indexed := p.indexCandidates(ctx, q)
	remote, err := p.catalogCandidates(ctx, q)
	if err != nil {
		return nil, err
	}
	candidates := mergeCandidates(indexed, remote, q.Limit)

	n := len(candidates)
	hits := make([]Hit, 0, n)
	withheld := 0
	for i := range candidates {
		if seen[candidates[i].urn] {
			continue
		}
		seen[candidates[i].urn] = true
		if !q.Caller.allowsURN(candidates[i].urn) {
			withheld++
			continue
		}
		hits = append(hits, Hit{
			Text:       candidates[i].text,
			Source:     SourceCatalog,
			Ref:        candidates[i].urn,
			Score:      positionalScore(i, n),
			EntityURNs: []string{candidates[i].urn},
			// A DataHub reference is its URN verbatim (the canonical citable form).
			Reference: candidates[i].urn,
		})
	}
	q.Caller.withhold(withheld)
	return hits, nil
}

// mergeCandidates interleaves the index's ranked candidates with DataHub's,
// de-duplicates by URN keeping the index's copy, and truncates to the
// per-source candidate budget.
//
// Interleaving rather than concatenating is what keeps the two arms from
// crowding each other out of a fixed budget: concatenation would let a full
// page of index hits consume every slot, dropping exactly the keyword matches
// (a column name, a glossary term) the index cannot produce, while the reverse
// order would bury the semantic hits this source exists to surface. The index
// still leads, so the top catalog hit is its top hit.
//
// The truncation is what keeps the coverage contract honest: SourceCoverage
// documents Matched as capped at the per-source candidate limit, and two
// unbounded arms would report up to twice that.
func mergeCandidates(indexed, remote []catalogCandidate, limit int) []catalogCandidate {
	merged := make([]catalogCandidate, 0, len(indexed)+len(remote))
	seen := make(map[string]bool, len(indexed)+len(remote))
	add := func(c catalogCandidate) {
		if seen[c.urn] {
			return
		}
		seen[c.urn] = true
		merged = append(merged, c)
	}
	for i := 0; i < len(indexed) || i < len(remote); i++ {
		if i < len(indexed) {
			add(indexed[i])
		}
		if i < len(remote) {
			add(remote[i])
		}
	}
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// indexCandidates ranks the intent against the platform's own index of catalog
// dataset text. No index wired means no candidates. An index failure is logged
// and degrades to the DataHub arm rather than blanking the provider: the index
// is an accelerator over a corpus DataHub still serves, so losing it must cost
// recall, not the whole catalog source.
func (p *CatalogProvider) indexCandidates(ctx context.Context, q Query) []catalogCandidate {
	if p.index == nil {
		return nil
	}
	hits, err := p.index.SearchCatalogIndex(ctx, CatalogIndexQuery{
		QueryText: q.Intent,
		Embedding: q.Embedding,
		Limit:     q.Limit,
	})
	if err != nil {
		slog.Debug("catalog index search skipped", "error", err)
		return nil
	}
	out := make([]catalogCandidate, 0, len(hits))
	for i := range hits {
		if hits[i].URN == "" {
			continue
		}
		out = append(out, catalogCandidate{
			urn:  hits[i].URN,
			text: catalogSnippet(hits[i].Name, hits[i].Description),
		})
	}
	return out
}

// catalogCandidates ranks the intent through DataHub's own keyword search. Its
// failure DOES blank the provider (the error propagates): unlike the index, it
// is the catalog itself, so an error here means the source is unhealthy rather
// than an accelerator being unavailable.
func (p *CatalogProvider) catalogCandidates(ctx context.Context, q Query) ([]catalogCandidate, error) {
	results, err := p.searcher.SearchTables(ctx, semantic.SearchFilter{
		Query: q.Intent,
		Limit: q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("catalog search: %w", err)
	}
	out := make([]catalogCandidate, 0, len(results))
	for i := range results {
		out = append(out, catalogCandidate{urn: results[i].URN, text: catalogHitText(results[i])})
	}
	return out, nil
}

// datasetPrefix is the URN form of a catalog dataset reference. The catalog owns
// exactly this prefix for fetch; the context-documents source owns
// urn:li:document:, so the two urn:li: sources never contend for a reference.
const datasetPrefix = "urn:li:dataset:"

// Fetch dereferences a urn:li:dataset:<id> reference to the dataset's full catalog
// context (#694), folding what datahub_get_entity returns into the one fetch verb.
// It owns only the dataset URN form; any other reference is declined (owned=false).
// A URN that does not parse as a dataset, that the catalog has no entry for, or that
// the catalog errors on is ErrNotFound: DataHub reports a missing/deleted entity as
// an error rather than an empty result (mcp-datahub GetEntity), and the search
// entity path treats that same lookup error as a skip (searchByEntity), so a stale
// dataset citation must be a clean not-found here too, not a hard tool failure.
//
// A dataset belonging only to connections the caller's persona is not granted is
// ErrNotFound as well (#1108): fetch must not hand back by citation what search
// declined to show. The boundary is evaluated against the URN the catalog
// resolved, not the one the reference carried — the lookup keys on the table
// identifier and re-derives the platform, so trusting the caller's platform
// segment would let a crafted reference read around the boundary.
func (p *CatalogProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	if !strings.HasPrefix(ref, datasetPrefix) {
		return nil, false, nil
	}
	table, err := memory.ParseURNToTable(ref)
	if err != nil {
		return nil, true, ErrNotFound
	}
	tc, err := p.searcher.GetTableContext(ctx, table)
	if err != nil {
		// DataHub conflates "no such entity" with an error, the same condition
		// searchByEntity skips; surface it as not-found so a stale citation is a clean
		// answer rather than a failure.
		slog.Debug("catalog entity fetch miss", "urn", ref, "error", err)
		return nil, true, ErrNotFound
	}
	if tc == nil || tc.URN == "" {
		return nil, true, ErrNotFound
	}
	if !caller.allowsURN(tc.URN) {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference:  ref,
		Source:     SourceCatalog,
		Title:      table.String(),
		Content:    tc,
		EntityURNs: []string{ref},
	}, true, nil
}

// positionalScore turns a 0-based rank into a descending score in (0,1],
// highest for the first result. DataHub returns an ordered list without
// numeric scores, so order is the only relevance signal available.
func positionalScore(i, n int) float64 {
	if n <= 1 {
		return entityMatchScore
	}
	return float64(n-i) / float64(n)
}

// catalogHitText renders a search-ranked catalog entity as a knowledge snippet:
// its name and its description when present.
func catalogHitText(r semantic.TableSearchResult) string {
	return catalogSnippet(r.Name, r.Description)
}

// catalogContextText renders an entity-keyed catalog hit: the table's dotted
// name and its description when present.
func catalogContextText(table semantic.TableIdentifier, tc *semantic.TableContext) string {
	return catalogSnippet(table.String(), tc.Description)
}

// catalogSnippet joins a catalog entity's name and optional description into one
// knowledge snippet.
func catalogSnippet(name, description string) string {
	if description == "" {
		return name
	}
	return strings.TrimSpace(name + "\n" + description)
}

package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/query"
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
	// datasets is the full-record read behind the dataset fetch arm (#1590);
	// nil leaves fetch on the enrichment-shaped TableContext read.
	datasets semantic.DatasetReader
	// products is the read behind the data product fetch arm (#1590); nil
	// makes every urn:li:dataProduct: reference a clean not-found.
	products semantic.DataProductReader
	// availability answers whether a fetched dataset is queryable and where;
	// nil leaves the record without a query_availability.
	availability AvailabilityResolver
}

// AvailabilityResolver answers whether and where one catalog dataset is
// queryable. It is the query provider's own read, declared here so the
// provider depends on the capability alone. It is deliberately not the
// search-time availability cache: that cache exists to mark positive answers
// on a page of hits and drops a negative one, whereas a fetched dataset must
// say "not queryable, and why" as plainly as "queryable, here".
type AvailabilityResolver interface {
	GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error)
}

// availabilityTimeout bounds the query-side lookup a dataset fetch makes. Row
// estimation can run a COUNT(*) against the warehouse; the record must not
// wait on it indefinitely, and a lookup that runs out of time leaves the
// record without an availability rather than failing the fetch.
const availabilityTimeout = 5 * time.Second

// CatalogDataset is the content of a fetched dataset reference: the catalog's
// full record of the dataset and, when a query provider is wired, whether and
// where it can be queried right now. The record is embedded so its fields
// serialize at the top level.
type CatalogDataset struct {
	*semantic.Dataset
	// QueryAvailability is the query engine's answer for this dataset: the table
	// to query, the connection it is reachable on, and the estimated row count.
	// Nil when no query provider is wired or the lookup did not resolve.
	QueryAvailability *query.TableAvailability `json:"query_availability,omitempty"`
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

// SetDatasetReader attaches the full-record dataset read (#1590), so a fetched
// dataset carries its schema, saved queries, and linked documents beside its
// business context. Nil (the default) leaves fetch on the TableContext read.
// Call once at wiring time.
func (p *CatalogProvider) SetDatasetReader(r semantic.DatasetReader) {
	p.datasets = r
}

// SetDataProductReader attaches the data product read (#1590), which opens the
// urn:li:dataProduct: fetch arm. Call once at wiring time.
func (p *CatalogProvider) SetDataProductReader(r semantic.DataProductReader) {
	p.products = r
}

// SetAvailabilityResolver attaches the query-side answer to "can this dataset
// be queried, and where" (#1590), so a fetched dataset says so without a
// second call. Nil (the default) leaves the record without one. Call once at
// wiring time.
func (p *CatalogProvider) SetAvailabilityResolver(r AvailabilityResolver) {
	p.availability = r
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
// Only entities the catalog actually holds yield a hit, so a URN that names
// nothing produces nothing. A non-empty URN in the response is not that test:
// DataHub answers a URN it has never ingested with a stub built from that URN
// rather than an error, so the entity has to be recognized by carrying something
// the reference did not supply (tableContextExists, #1605). Without it a search
// by a made-up URN reported one match, and fetching the reference it handed back
// then reported the entity missing.
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
			slog.Debug("catalog entity lookup skipped", logKeyURN, urn, logKeyError, err)
			continue
		}
		if tc == nil || tc.URN == "" {
			continue
		}
		if !tableContextExists(tc) {
			// A stub built from the URN, not an entry. Left unseen so the text arm
			// may still match the same URN on its own evidence.
			slog.Debug("catalog entity lookup resolved to a urn-only record", logKeyURN, urn)
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
	// The de-duplication and the connection boundary run INSIDE the merge, ahead
	// of its truncation, so neither an entity the caller already has from the
	// entity arm nor one their persona may not see consumes a candidate slot. A
	// dropped candidate that consumed a slot is not only recall lost for no
	// reason: it also makes a truncated arm come back short, which is how the
	// router reads "this source had nothing more to give" (#1585).
	withheld := 0
	candidates := mergeCandidates(q.Limit, func(urn string) bool {
		if seen[urn] {
			return false
		}
		seen[urn] = true
		if !q.Caller.allowsURN(urn) {
			withheld++
			return false
		}
		return true
	}, indexed, remote)

	n := len(candidates)
	hits := make([]Hit, 0, n)
	for i := range candidates {
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

// mergeCandidates round-robins several ranked candidate lists into one, keeping
// the earliest list's copy of a URN that appears in more than one, and truncates
// to the per-source candidate budget. Lists are consumed in argument order within
// each round, so the first list leads.
//
// Interleaving rather than concatenating is what keeps the arms from crowding
// each other out of a fixed budget: concatenation would let a full page from one
// arm consume every slot. In the catalog source that would drop exactly the
// keyword matches (a column name, a glossary term) the index cannot produce,
// while the reverse order would bury the semantic hits the source exists to
// surface; in the governance source it would let one vocabulary hide the other
// two. The first list still leads, so the top hit is its top hit.
//
// keep is the caller's per-candidate predicate (de-duplication against an arm
// that already ran, the persona connection boundary), and it is applied BEFORE
// the truncation rather than by the caller afterwards. Order matters twice: a
// rejected candidate must not consume one of the limited slots, and the router
// reads a full list as evidence that the source had more to give, so a slot
// spent on something never emitted would report a truncated source as exhausted
// (#1585). A nil keep admits every candidate.
//
// The truncation is what keeps the coverage contract honest: SourceCoverage
// documents Matched as bounded by the per-source candidate depth, and unbounded
// arms would report a multiple of that.
func mergeCandidates(limit int, keep func(urn string) bool, lists ...[]catalogCandidate) []catalogCandidate {
	total, longest := mergeSpans(lists)
	merged := make([]catalogCandidate, 0, total)
	seen := make(map[string]bool, total)
	for i := range longest {
		for _, l := range lists {
			if i < len(l) && admitCandidate(seen, keep, l[i].urn) {
				merged = append(merged, l[i])
			}
		}
	}
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// mergeSpans reports the combined length of the candidate lists and the length
// of the longest: the capacity one merged slice needs, and the number of
// round-robin rounds that visits every element.
func mergeSpans(lists [][]catalogCandidate) (total, longest int) {
	for _, l := range lists {
		total += len(l)
		if len(l) > longest {
			longest = len(l)
		}
	}
	return total, longest
}

// admitCandidate records a URN as considered and reports whether it belongs in
// the merged set: a URN an earlier list already contributed is a duplicate, and
// one the caller's predicate rejects is dropped. Marking before the predicate
// runs is what keeps a rejected URN from being re-examined (and, where the
// predicate counts withheld candidates, re-counted) from a later list.
func admitCandidate(seen map[string]bool, keep func(urn string) bool, urn string) bool {
	if seen[urn] {
		return false
	}
	seen[urn] = true
	return keep == nil || keep(urn)
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
		slog.Debug("catalog index search skipped", logKeyError, err)
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

// logKeyError and logKeyURN are the structured-log keys an error and the
// reference it concerns are recorded under.
const (
	logKeyError = "error"
	logKeyURN   = "urn"
)

// datasetPrefix is the URN form of a catalog dataset reference. The catalog owns
// exactly this prefix for fetch; the context-documents source owns
// urn:li:document:, so the two urn:li: sources never contend for a reference.
const datasetPrefix = "urn:li:dataset:"

// Fetch dereferences a urn:li:dataset:<id> reference to the dataset's full catalog
// record (#694, #1590): the business context, identity, declared schema, saved
// queries, and linked documents the catalog holds, plus whether and where the
// dataset is queryable. It is the one answer to "tell me about this dataset",
// where the former datahub_get_entity, datahub_get_schema, and
// datahub_get_queries reads were folded. It also owns the urn:li:dataProduct:
// form (fetchDataProduct); any other reference is declined (owned=false).
// A URN that does not parse as a dataset, that the catalog has no entry for, or that
// the catalog errors on is ErrNotFound. A lookup error is a miss because the search
// entity path treats that same error as a skip (searchByEntity), so a stale dataset
// citation is a clean not-found here too rather than a hard tool failure. An entry
// the catalog does not hold is a miss on the strength of what the record carries
// rather than of an error, because DataHub answers such a URN with a stub built
// from it (datasetExists, #1605).
//
// A dataset belonging only to connections the caller's persona is not granted is
// ErrNotFound as well (#1108): fetch must not hand back by citation what search
// declined to show. The boundary is evaluated against the URN the catalog
// resolved, not the one the reference carried — the lookup keys on the table
// identifier and re-derives the platform, so trusting the caller's platform
// segment would let a crafted reference read around the boundary.
func (p *CatalogProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	if strings.HasPrefix(ref, dataProductPrefix) {
		return p.fetchDataProduct(ctx, ref, caller)
	}
	if !strings.HasPrefix(ref, datasetPrefix) {
		return nil, false, nil
	}
	table, err := memory.ParseURNToTable(ref)
	if err != nil {
		return nil, true, ErrNotFound
	}
	ds := p.resolveDataset(ctx, ref, table)
	if ds == nil || !caller.allowsURN(ds.URN) {
		return nil, true, ErrNotFound
	}
	avail := p.resolveAvailability(ctx, ds.URN)
	doc := &Document{
		Reference:  ref,
		Source:     SourceCatalog,
		Title:      table.String(),
		Content:    CatalogDataset{Dataset: ds, QueryAvailability: avail},
		EntityURNs: []string{ref},
	}
	if avail != nil && avail.Available {
		doc.Verifiable = &query.Verifiable{URN: ds.URN, QueryTable: avail.QueryTable, Connection: avail.Connection}
	}
	return doc, true, nil
}

// resolveDataset reads the dataset the reference names, or nil when the catalog
// does not hold it. Each of the three ways it can fail to hold it is a miss
// rather than a failure: a read error, because DataHub conflates "no such
// entity" with an error and the search entity path skips that same error
// (searchByEntity), so a stale citation stays a clean answer; a record with no
// URN; and a record carrying nothing the reference did not supply, which is what
// the catalog returns for a URN it has never ingested (#1605).
func (p *CatalogProvider) resolveDataset(ctx context.Context, ref string, table semantic.TableIdentifier) *semantic.Dataset {
	ds, err := p.readDataset(ctx, table)
	if err != nil {
		slog.Debug("catalog entity fetch miss", logKeyURN, ref, logKeyError, err)
		return nil
	}
	if ds == nil || ds.URN == "" {
		return nil
	}
	if !datasetExists(ds) {
		slog.Debug("catalog entity fetch resolved to a urn-only record", logKeyURN, ref)
		return nil
	}
	return ds
}

// readDataset reads the full dataset record when a DatasetReader is wired and
// the enrichment-shaped context otherwise, so a deployment with a plain
// catalog searcher still answers a dataset fetch.
func (p *CatalogProvider) readDataset(ctx context.Context, table semantic.TableIdentifier) (*semantic.Dataset, error) {
	if p.datasets != nil {
		return p.datasets.GetDataset(ctx, table) //nolint:wrapcheck // the caller maps every error to not-found
	}
	tc, err := p.searcher.GetTableContext(ctx, table)
	if err != nil || tc == nil {
		return nil, err //nolint:wrapcheck // the caller maps every error to not-found
	}
	return &semantic.Dataset{TableContext: *tc}, nil
}

// resolveAvailability asks the query side whether and where the dataset is
// queryable; nil when no resolver is wired or the lookup failed or ran out of
// time. A negative answer (Available false, with the provider's reason) is
// kept: it is what the reader needs to know before writing a query.
func (p *CatalogProvider) resolveAvailability(ctx context.Context, urn string) *query.TableAvailability {
	if p.availability == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, availabilityTimeout)
	defer cancel()
	avail, err := p.availability.GetTableAvailability(ctx, urn)
	if err != nil || ctx.Err() != nil {
		slog.Debug("dataset availability lookup skipped", logKeyURN, urn, logKeyError, err)
		return nil
	}
	return avail
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

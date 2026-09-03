package semantic

import (
	"context"
	"errors"
	"fmt"
)

// Provider retrieves semantic metadata from catalog systems.
// DataHub implements this. Future alternatives (Atlas, Unity Catalog) can too.
type Provider interface {
	// Name returns the provider name.
	Name() string

	// GetTableContext retrieves semantic context for a table.
	GetTableContext(ctx context.Context, table TableIdentifier) (*TableContext, error)

	// GetColumnContext retrieves semantic context for a single column.
	GetColumnContext(ctx context.Context, column ColumnIdentifier) (*ColumnContext, error)

	// GetColumnsContext retrieves semantic context for all columns of a table.
	GetColumnsContext(ctx context.Context, table TableIdentifier) (map[string]*ColumnContext, error)

	// GetLineage retrieves lineage information for a table.
	GetLineage(ctx context.Context, table TableIdentifier, direction LineageDirection, maxDepth int) (*LineageInfo, error)

	// GetGlossaryTerm retrieves a glossary term by URN.
	GetGlossaryTerm(ctx context.Context, urn string) (*GlossaryTerm, error)

	// SearchTables searches for tables matching the filter.
	SearchTables(ctx context.Context, filter SearchFilter) ([]TableSearchResult, error)

	// GetCuratedQueryCount returns the number of curated/saved queries for a dataset.
	GetCuratedQueryCount(ctx context.Context, urn string) (int, error)

	// Close releases resources.
	Close() error
}

// DocumentSearcher is the optional document-search capability (#692): relevance
// search over DataHub context documents, the non-dataset knowledge home that
// predates knowledge pages. Only a real catalog provider implements it (the DataHub
// adapter); the noop provider does not, so a noop catalog adds no documents search
// source. The cache decorator forwards it. A consumer type-asserts a Provider to
// this to decide whether to register a documents search source.
type DocumentSearcher interface {
	// SearchDocuments ranks context documents by relevance to query; a query of "*"
	// lists all (an empty query does not list). Results carry ShowInGlobalContext and
	// Status so the caller can filter to globally-visible, published documents. limit
	// caps results (0 means the provider default).
	SearchDocuments(ctx context.Context, query string, limit int) ([]DocumentResult, error)

	// GetRelatedDocuments returns the context documents linked to an entity URN (the
	// reverse of a document's related assets), for entity-keyed discovery. Results
	// carry the same fields as SearchDocuments so the caller applies the same filter.
	GetRelatedDocuments(ctx context.Context, urn string) ([]DocumentResult, error)

	// GetDocument reads one context document by its URN, returning the full
	// untruncated body (in DocumentResult.Body) so an agent can dereference a
	// urn:li:document:<id> reference search emitted to the complete content. A URN
	// that resolves to no document returns ErrDocumentNotFound, which the fetch
	// surface maps to a structured not-found rather than an error.
	GetDocument(ctx context.Context, urn string) (*DocumentResult, error)

	// BrowseDocuments enumerates context documents for the browse surface (#695):
	// the offset/limit page of the complete document set plus the total document
	// count, so an agent can page the whole corpus to audit, dedup, or migrate it.
	// Unlike SearchDocuments this applies NO relevance threshold and NO
	// visibility/status filter: every document is enumerable (drafts and hidden
	// documents included), so the returned page and total describe the same complete
	// set. Results carry the same fields as SearchDocuments (a bounded Snippet, not
	// the full Body) since a listing shows what each document is, not its contents.
	BrowseDocuments(ctx context.Context, offset, limit int) (docs []DocumentResult, total int, err error)
}

// ErrNotFound reports that the catalog holds no entity for the reference a
// by-URN read was given. The read returns it (wrapped) so a caller can tell a
// reference the catalog has never ingested from a transport or authorization
// failure, and so CachedProvider can remember the miss for its TTL the way it
// remembers a hit. It is defined here, on the abstraction, rather than
// per-implementation so every provider agrees on the sentinel; the DataHub
// adapter maps the upstream client's own not-found onto it (#1610).
var ErrNotFound = errors.New("entity not found")

// ErrDocumentNotFound reports that a document URN did not resolve to a document.
// GetDocument returns it (wrapped) so a caller can distinguish a stale reference
// from a transport failure. It is defined here, on the capability interface,
// rather than per-implementation so every DocumentSearcher agrees on the sentinel.
var ErrDocumentNotFound = errors.New("document not found")

// DocumentSearcherFrom reports the document-search capability of p, unwrapping any
// decorator chain (e.g. CachedProvider) so the answer reflects the real underlying
// provider rather than a decorator's unconditional pass-through. ok is false when no
// provider in the chain can search documents (so no documents source is registered);
// when ok, the returned searcher is p itself, so searches still flow through the
// decorator (and its cache/forwarding) rather than bypassing it.
func DocumentSearcherFrom(p Provider) (DocumentSearcher, bool) {
	inner := p
	for {
		u, ok := inner.(interface{ Unwrap() Provider })
		if !ok {
			break
		}
		inner = u.Unwrap()
	}
	if _, ok := inner.(DocumentSearcher); !ok {
		return nil, false
	}
	ds, ok := p.(DocumentSearcher)
	return ds, ok
}

// CatalogPicker enumerates business-context entities (domains, glossary terms)
// for topic-shaped argument autocompletion. These are not part of the core
// Provider interface — only a real semantic backend (the DataHub adapter)
// implements them — so completion probes for the capability with
// CatalogPickerFrom. Glossary/domain names are catalog metadata; callers apply
// their own persona gating.
type CatalogPicker interface {
	// ListDomains returns every DataHub domain; the caller filters client-side.
	ListDomains(ctx context.Context) ([]EntityRef, error)
	// SearchGlossaryTerms name-searches glossary terms, bounded by limit. An
	// empty query lists: an implementation must substitute its backend's
	// match-everything query rather than forward the empty string, which a
	// relevance backend reads as "match nothing".
	SearchGlossaryTerms(ctx context.Context, query string, limit int) ([]EntityRef, error)
}

// CatalogPickerFrom reports the catalog-picker capability of p, returning the
// innermost provider that implements it. Unlike DocumentSearcherFrom it returns
// the unwrapped provider rather than p, because the caching decorator does not
// forward these picker methods; the picker lists (domains, glossary terms) are
// small and not per-table, so bypassing the cache is correct. ok is false when
// no provider in the chain can pick.
func CatalogPickerFrom(p Provider) (CatalogPicker, bool) {
	return innermostCapability[CatalogPicker](p)
}

// GovernanceReader is the optional governance-vocabulary capability (#1160): the
// reads that make DataHub's glossary terms, tags, and domains discoverable and
// readable as entities in their own right rather than as attributes of a
// dataset. Only a real catalog backend (the DataHub adapter) implements it, so a
// noop catalog registers no governance source.
//
// The three kinds are deliberately not read through one uniform method, because
// upstream does not offer one: a glossary term has a by-URN read and a name
// search, a tag has a name search only, and a domain has neither and is
// enumerated whole. SearchTables completes the set — with a tag, domain, or
// glossary-term filter it lists the datasets that carry a governance entity,
// which is what makes the entity useful to read.
type GovernanceReader interface {
	CatalogPicker

	// SearchTags name-searches tags, bounded by limit. An empty query lists them,
	// under the same substitution rule SearchGlossaryTerms carries.
	SearchTags(ctx context.Context, query string, limit int) ([]EntityRef, error)

	// GetGlossaryTerm reads one term by URN. It is the only by-URN read any
	// governance vocabulary has, which is why a tag or a domain is resolved by
	// listing its vocabulary and matching instead.
	GetGlossaryTerm(ctx context.Context, urn string) (*GlossaryTerm, error)

	// SearchTables ranks datasets; filtered by tag, domain, or glossary term it
	// lists the datasets carrying that governance entity.
	SearchTables(ctx context.Context, filter SearchFilter) ([]TableSearchResult, error)
}

// GovernanceReaderFrom reports the governance-read capability of p, returning the
// innermost provider that implements it. It resolves like CatalogPickerFrom (and
// unlike DocumentSearcherFrom) because it builds on the same picker reads, which
// the caching decorator does not forward. ok is false when no provider in the
// chain can read the governance vocabulary.
func GovernanceReaderFrom(p Provider) (GovernanceReader, bool) {
	return innermostCapability[GovernanceReader](p)
}

// DatasetReader is the optional full-record read of one catalog dataset (#1590):
// business context, identity, declared schema, saved queries, and linked
// context documents in one call. It is the read behind fetch's urn:li:dataset:
// arm, where the former datahub_get_entity, datahub_get_schema, and
// datahub_get_queries tools were folded. Only a real catalog backend (the
// DataHub adapter) implements it; the noop provider does not, so a noop catalog
// leaves fetch on the enrichment-shaped TableContext read alone.
type DatasetReader interface {
	// GetDataset reads the dataset the table identifier names. A table the
	// catalog has no entry for is an error, as GetTableContext reports one.
	GetDataset(ctx context.Context, table TableIdentifier) (*Dataset, error)
}

// DatasetReaderFrom reports the full-dataset read capability of p, returning the
// innermost provider that implements it. It resolves like CatalogPickerFrom
// because the caching decorator does not forward it: a fetch is one read of one
// record, not the per-query enrichment read the cache exists for.
func DatasetReaderFrom(p Provider) (DatasetReader, bool) {
	return innermostCapability[DatasetReader](p)
}

// DataProductReader is the optional by-URN read of a catalog data product
// (#1590), the read behind fetch's urn:li:dataProduct: arm. Only a real
// catalog backend implements it.
type DataProductReader interface {
	// GetDataProduct reads one data product by URN. A URN the catalog has no
	// product for is an error, as the other by-URN reads report one.
	GetDataProduct(ctx context.Context, urn string) (*DataProduct, error)
}

// DataProductReaderFrom reports the data-product read capability of p, returning
// the innermost provider that implements it, under the same rule as
// DatasetReaderFrom.
func DataProductReaderFrom(p Provider) (DataProductReader, bool) {
	return innermostCapability[DataProductReader](p)
}

// TotalUnknown is the match count reported when no provider in the chain can
// count. It is deliberately negative rather than zero so it can never be read as
// "no matches" or compared against a page length as if it were a total: a caller
// must branch on it explicitly.
const TotalUnknown = -1

// TableMatchCounter is the optional total-matches capability for the dataset
// search: it reports how many matches the backend found, not merely how many rows
// it put in the page it returned. Only a real catalog backend implements it (the
// DataHub adapter), because only the backend knows the total.
//
// It exists because a page-bounded backend silently reduces the requested limit —
// the DataHub client caps every search at its MaxLimit (100) — so a caller cannot
// discover "more matches exist" by asking for one row more than it needs. The
// extra row never arrives and the short page reads as a complete set (#1238). The
// total survives the clamp and rides back on the same response, so reading it
// costs no extra round trip.
type TableMatchCounter interface {
	// SearchTablesCounted runs the same search as Provider.SearchTables and also
	// reports the backend's total match count. The total may exceed len(results)
	// both because the caller's limit bounded the page and because the backend
	// bounded it further.
	SearchTablesCounted(ctx context.Context, filter SearchFilter) (results []TableSearchResult, total int, err error)
}

// GlossaryMatchCounter is the glossary counterpart of TableMatchCounter, kept a
// separate capability rather than a second method on it so a backend that can
// count one search is never mistaken for one that can count both. It carries the
// same rationale, doubly so: the glossary page is bounded by the picker's own
// limit before the client's clamp applies.
type GlossaryMatchCounter interface {
	// SearchGlossaryTermsCounted runs the same search as
	// CatalogPicker.SearchGlossaryTerms, under the same empty-query listing rule,
	// and also reports the backend's total match count.
	SearchGlossaryTermsCounted(ctx context.Context, query string, limit int) (refs []EntityRef, total int, err error)
}

// SearchTablesCounted searches p and reports the backend's total match count
// alongside the page. When no provider in p's chain can count, total is
// TotalUnknown and the caller has learned nothing about the matches beyond the
// rows it holds — in particular it must not read a full page as proof that more
// exist, nor a short one as proof that none do.
//
// The count is read from the innermost counting provider, bypassing the cache
// decorator, which is what CachedProvider.SearchTables does with the search
// itself: searches vary too much to cache, so the decorator only forwards.
func SearchTablesCounted(ctx context.Context, p Provider, filter SearchFilter) (results []TableSearchResult, total int, err error) {
	if p == nil {
		return nil, TotalUnknown, nil
	}
	if mc, ok := innermostCapability[TableMatchCounter](p); ok {
		results, total, err = mc.SearchTablesCounted(ctx, filter)
		if err != nil {
			return nil, TotalUnknown, fmt.Errorf("counted table search: %w", err)
		}
		return results, total, nil
	}
	results, err = p.SearchTables(ctx, filter)
	if err != nil {
		return nil, TotalUnknown, fmt.Errorf("table search: %w", err)
	}
	return results, TotalUnknown, nil
}

// SearchGlossaryTermsCounted searches picker's glossary and reports the total
// match count, falling back to the uncounted picker search (total TotalUnknown)
// when the picker cannot count. It takes the picker rather than the Provider
// because CatalogPickerFrom already resolves to the innermost implementation,
// which is the same provider that counts.
func SearchGlossaryTermsCounted(ctx context.Context, picker CatalogPicker, query string, limit int) (refs []EntityRef, total int, err error) {
	if picker == nil {
		return nil, TotalUnknown, nil
	}
	if mc, ok := picker.(GlossaryMatchCounter); ok {
		refs, total, err = mc.SearchGlossaryTermsCounted(ctx, query, limit)
		if err != nil {
			return nil, TotalUnknown, fmt.Errorf("counted glossary search: %w", err)
		}
		return refs, total, nil
	}
	refs, err = picker.SearchGlossaryTerms(ctx, query, limit)
	if err != nil {
		return nil, TotalUnknown, fmt.Errorf("glossary search: %w", err)
	}
	return refs, TotalUnknown, nil
}

// innermostCapability walks a provider's decorator chain and returns the first
// (innermost-reaching) member that satisfies T. It holds the capability-probe
// rule once so the picker and governance probes cannot drift: both return the
// implementing provider itself rather than the decorator, because the caching
// decorator forwards neither's methods.
func innermostCapability[T any](p Provider) (T, bool) {
	inner := p
	for {
		if c, ok := inner.(T); ok {
			return c, true
		}
		u, ok := inner.(interface{ Unwrap() Provider })
		if !ok {
			var zero T
			return zero, false
		}
		inner = u.Unwrap()
	}
}

// FilterFieldGlossaryTerms is the catalog search-filter field that matches the
// datasets carrying a glossary term. DataHub folds a column-level assignment into
// the dataset's glossaryTerms index, so this field matches a dataset whose TABLE
// or whose COLUMN carries the term; fieldGlossaryTerms narrows to column-level
// assignments only. Exported so the governance search source and the portal's
// catalog REST surface name the field from one authority.
const FilterFieldGlossaryTerms = "glossaryTerms"

// URNResolver can resolve URNs to table identifiers.
type URNResolver interface {
	// ResolveURN converts a URN to a table identifier.
	ResolveURN(ctx context.Context, urn string) (*TableIdentifier, error)

	// BuildURN creates a URN from a table identifier.
	BuildURN(ctx context.Context, table TableIdentifier) (string, error)
}

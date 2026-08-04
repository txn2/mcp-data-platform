package semantic

import (
	"context"
	"errors"
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

package semantic

import (
	"context"
	"errors"
	"fmt"
)

// Provider retrieves semantic metadata from the catalog. The platform catalog
// implements it (internal/platform/catalogsemantic).
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

// ErrNotFound reports that the catalog holds no entity for the reference a
// by-URN read was given. The read returns it (wrapped) so a caller can tell a
// reference the catalog has never ingested from a transport or authorization
// failure, and so CachedProvider can remember the miss for its TTL the way it
// remembers a hit. It is defined here, on the abstraction, rather than
// per-implementation so every provider agrees on the sentinel (#1610).
var ErrNotFound = errors.New("entity not found")

// CatalogPicker enumerates business-context entities (domains, glossary terms,
// data products) for topic-shaped argument autocompletion. These are not part of the core
// Provider interface (the platform catalog implements them, the noop provider
// does not), so completion probes for the capability with
// CatalogPickerFrom. Glossary/domain names are catalog metadata; callers apply
// their own persona gating.
type CatalogPicker interface {
	// ListDomains returns every domain; the caller filters client-side.
	ListDomains(ctx context.Context) ([]EntityRef, error)
	// SearchGlossaryTerms name-searches glossary terms, bounded by limit. An
	// empty query lists: an implementation must substitute its backend's
	// match-everything query rather than forward the empty string, which a
	// relevance backend reads as "match nothing".
	SearchGlossaryTerms(ctx context.Context, query string, limit int) ([]EntityRef, error)
	// SearchDataProducts name-searches data products, bounded by limit, under
	// the same empty-query listing rule.
	SearchDataProducts(ctx context.Context, query string, limit int) ([]EntityRef, error)
}

// CatalogPickerFrom reports the catalog-picker capability of p, returning the
// innermost provider that implements it: the unwrapped provider rather than
// p, because the caching decorator does not
// forward these picker methods; the picker lists (domains, glossary terms) are
// small and not per-table, so bypassing the cache is correct. ok is false when
// no provider in the chain can pick.
func CatalogPickerFrom(p Provider) (CatalogPicker, bool) {
	return innermostCapability[CatalogPicker](p)
}

// TotalUnknown is the match count reported when no provider in the chain can
// count. It is deliberately negative rather than zero so it can never be read as
// "no matches" or compared against a page length as if it were a total: a caller
// must branch on it explicitly.
const TotalUnknown = -1

// TableMatchCounter is the optional total-matches capability for the dataset
// search: it reports how many matches the backend found, not merely how many rows
// it put in the page it returned. Only a backend that knows whether its page is
// the whole answer implements it; the platform catalog does, because it returns
// exactly the page it is asked for.
//
// It exists because a page-bounded backend can silently reduce the requested
// limit, so a caller cannot discover "more matches exist" by asking for one row
// more than it needs: the extra row never arrives and the short page reads as a
// complete set (#1238). A total that rides back on the same response costs no
// extra round trip.
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

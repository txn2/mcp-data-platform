package portaldomain

import (
	"context"
	"strings"
)

// Search result limits. DefaultSearchLimit is the top-K returned when the caller
// does not specify one; maxSearchLimit bounds an explicit request so a single
// ranked query cannot ask for an unbounded result set. They mirror the prompt
// and memory search limits so every ranked surface clamps the same way.
const (
	DefaultSearchLimit = 20
	maxSearchLimit     = 100
)

// AssetIndexText composes the text an asset is embedded and lexically indexed
// on for relevance search: its name, description, and tags. The indexjobs asset
// consumer and the request-path search MUST agree on this composition so a
// stored embedding lives in the same space as the query; it is defined once
// here for both. Empty fields are skipped so a sparse asset does not pad the
// text with blank lines. The lexical arm's portal_asset_fts (migration 000063)
// composes the same corpus from the same columns.
func AssetIndexText(name, description string, tags []string) string {
	parts := make([]string, 0, 3)
	if name != "" {
		parts = append(parts, name)
	}
	if description != "" {
		parts = append(parts, description)
	}
	if len(tags) > 0 {
		parts = append(parts, strings.Join(tags, " "))
	}
	return strings.Join(parts, "\n")
}

// CollectionIndexText composes the text a collection is embedded and lexically
// indexed on: its name, description, and the denormalized section titles +
// descriptions (sectionsText). The indexjobs collection consumer and the
// request-path search MUST agree on this composition; portal_collection_fts
// (migration 000063) composes the same corpus from the same columns.
func CollectionIndexText(name, description, sectionsText string) string {
	parts := make([]string, 0, 3)
	if name != "" {
		parts = append(parts, name)
	}
	if description != "" {
		parts = append(parts, description)
	}
	if sectionsText != "" {
		parts = append(parts, sectionsText)
	}
	return strings.Join(parts, "\n")
}

// SectionsText flattens a collection's sections into the denormalized
// sections_text column: each section's title and description joined in position
// order, skipping empty fields. The store writes it whenever sections change,
// and both the lexical FTS index and the embedding read it (transitively, via
// the column), so the request-path producers cannot drift. Migration 000063
// performs a one-time backfill of the same column for pre-existing collections;
// its SQL is whitespace-equivalent (to_tsvector and the embedder both ignore the
// extra spaces) rather than byte-identical to this function.
func SectionsText(sections []CollectionSection) string {
	parts := make([]string, 0, len(sections)*2)
	for _, s := range sections {
		if s.Title != "" {
			parts = append(parts, s.Title)
		}
		if s.Description != "" {
			parts = append(parts, s.Description)
		}
	}
	return strings.Join(parts, " ")
}

// clampSearchLimit clamps a requested limit into [1, maxSearchLimit], defaulting
// an unset or out-of-range value to DefaultSearchLimit.
func clampSearchLimit(limit int) int {
	if limit <= 0 || limit > maxSearchLimit {
		return DefaultSearchLimit
	}
	return limit
}

// AssetSearchQuery describes a relevance ranking request over saved assets.
// Owner scoping is applied before ranking (you cannot rank an asset you cannot
// view): Owner is mandatory and the search is restricted to the caller's own
// non-deleted assets, matched on either identifier the row records the way
// every other asset surface matches it (see AssetOwner). A nil Embedding
// selects lexical-only ranking (the graceful-degradation path when no embedding
// provider is configured); a non-nil Embedding selects hybrid ranking.
type AssetSearchQuery struct {
	Embedding []float32  // query vector; nil selects lexical-only ranking
	QueryText string     // raw query text for the lexical arm
	Owner     AssetOwner // caller identity; the owner scope
	// ProducedBy scopes the ranking to what one producer wrote, which is how a
	// managed-script run's own outputs are ranked: the producer id is the
	// script's, unlike the identifiers on the row (#1579). A query must carry
	// an Owner or a ProducedBy; SearchAssets refuses one carrying neither
	// rather than ranking every asset on the platform.
	ProducedBy ContentProducer
	Limit      int // max results; clamped into [1, maxSearchLimit]
}

// RankedAssetQuery ranks what one person can open (#2500): every asset in the
// reach whose text matches the query, and the ones the embedding model places
// near it, paged. Filters narrow the set before it is ranked.
type RankedAssetQuery struct {
	Embedding   []float32 // query vector; nil selects lexical-only ranking
	QueryText   string
	Reach       *Reach
	ContentType string
	Tags        []string
	Owners      []string
	Limit       int
	Offset      int
}

// RankedCollectionQuery is RankedAssetQuery for collections.
type RankedCollectionQuery struct {
	Embedding []float32
	QueryText string
	Reach     *Reach
	Owners    []string
	Limit     int
	Offset    int
}

// RankedPage clamps a ranked page to the search bounds and MaxRankedDepth.
func RankedPage(limit, offset int) (lim, off int) {
	lim = clampSearchLimit(limit)
	off = max(offset, 0)
	if off+lim > MaxRankedDepth {
		off = max(MaxRankedDepth-lim, 0)
	}
	return lim, off
}

// ReachSearcher ranks within a person's reach. The PostgreSQL stores implement
// it beside AssetSearcher and CollectionSearcher.
type ReachSearcher interface {
	SearchReach(ctx context.Context, q RankedAssetQuery) ([]ScoredAsset, int, error)
}

// CollectionReachSearcher is ReachSearcher for collections.
type CollectionReachSearcher interface {
	SearchCollectionReach(ctx context.Context, q RankedCollectionQuery) ([]ScoredCollection, int, error)
}

// AssetFaceter computes the tag and author facets over a listing's filter.
type AssetFaceter interface {
	AssetFacets(ctx context.Context, filter AssetFilter) (AssetFacets, error)
}

// CollectionAuthorFaceter lists the authors of the collections a reach covers.
type CollectionAuthorFaceter interface {
	CollectionAuthors(ctx context.Context, r *Reach) ([]FacetCount, error)
}

// ReachPermissions reports, per asset id, the highest permission the reach's
// shares grant on it, directly or through a collection: the share half of
// access.ResolveAssetPermission over a page of rows. Owned rows are not the
// share store's to answer and come back absent unless also shared.
type ReachPermissions interface {
	ReachPermissions(ctx context.Context, ids []string, r *Reach) (map[string]SharePermission, error)
}

// EffectiveLimit clamps the requested limit into the search bounds.
func (q AssetSearchQuery) EffectiveLimit() int { return clampSearchLimit(q.Limit) }

// ScoredAsset pairs an asset with its relevance score in [0,1].
type ScoredAsset struct {
	Asset Asset   `json:"asset"`
	Score float64 `json:"score"`
}

// AssetSearcher ranks the caller's assets by relevance to a query. It is a
// capability separate from AssetStore: only a backing store that can rank (the
// PostgreSQL store with pgvector) implements it, so the feature degrades to
// absent rather than forcing every AssetStore implementation to carry a ranking
// query.
type AssetSearcher interface {
	SearchAssets(ctx context.Context, q AssetSearchQuery) ([]ScoredAsset, error)
}

// CollectionSearchQuery describes a relevance ranking request over curated
// collections, scoped to the caller's own non-deleted collections by owner_id
// (the ownership key, as for assets — see AssetSearchQuery).
type CollectionSearchQuery struct {
	Embedding []float32 // query vector; nil selects lexical-only ranking
	QueryText string    // raw query text for the lexical arm
	OwnerID   string    // caller identity; mandatory owner scope (owner_id)
	Limit     int       // max results; clamped into [1, maxSearchLimit]
}

// EffectiveLimit clamps the requested limit into the search bounds.
func (q CollectionSearchQuery) EffectiveLimit() int { return clampSearchLimit(q.Limit) }

// ScoredCollection pairs a collection (header + asset tags, no sections) with
// its relevance score in [0,1].
type ScoredCollection struct {
	Collection Collection `json:"collection"`
	Score      float64    `json:"score"`
}

// CollectionSearcher ranks the caller's collections by relevance to a query.
type CollectionSearcher interface {
	SearchCollections(ctx context.Context, q CollectionSearchQuery) ([]ScoredCollection, error)
}

// Scope selects which part of what a person can open a listing or a search
// covers (#2500): what they own, what was shared with them, or both.
type Scope string

// The scopes a listing or search accepts.
const (
	ScopeMine   Scope = "mine"
	ScopeShared Scope = "shared"
	ScopeAll    Scope = "all"
)

// ParseScope reads a scope parameter, falling back when it is blank or names
// no scope.
func ParseScope(raw string, fallback Scope) Scope {
	switch s := Scope(strings.ToLower(strings.TrimSpace(raw))); s {
	case ScopeMine, ScopeShared, ScopeAll:
		return s
	default:
		return fallback
	}
}

// Reach is a person asking for what they can open, narrowed to a scope
// (#2500). A row is theirs when they own it on either identifier it records
// (AssetOwner), and shared with them when an active share names their id,
// their address or the persona they resolved to, on the row itself or, for an
// asset, on a collection that contains it. That is the rule the per-item
// access checks apply one row at a time (internal/portal/access); the store
// applies it to a set, so a listing, a ranked search and a facet count cannot
// disagree with the check that opens the row.
//
// An administrator's reach is their own: the administrator's all-assets
// listing is a separate route, and organizing never grants access.
type Reach struct {
	Owner   AssetOwner
	Persona string
	Scope   Scope
}

// NewReach builds a person's reach. The identifiers are normalized the way
// NewAssetOwner normalizes them, so the anonymous sentinel names nobody.
func NewReach(userID, email, persona string, scope Scope) *Reach {
	return &Reach{
		Owner:   NewAssetOwner(userID, email),
		Persona: strings.TrimSpace(persona),
		Scope:   ParseScope(string(scope), ScopeAll),
	}
}

// Identified reports whether the reach names anyone a row or a share could be
// addressed to.
func (r *Reach) Identified() bool {
	return r != nil && (r.Owner.UserID != "" || r.Owner.Email != "" || r.Persona != "")
}

// NormalizeOwners lowercases and trims an author filter, dropping blanks and
// repeats.
func NormalizeOwners(owners []string) []string {
	out := make([]string, 0, len(owners))
	seen := make(map[string]bool, len(owners))
	for _, raw := range owners {
		for part := range strings.SplitSeq(raw, ",") {
			o := strings.ToLower(strings.TrimSpace(part))
			if o == "" || seen[o] {
				continue
			}
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

// FacetCount is one value of a facet and how many rows carry it.
type FacetCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// AssetFacets are the tags and authors on the assets a listing covers, each
// with its count, computed over the caller's reach and every other filter
// (#2500). The tag facet ignores the tag filter and the author facet the
// author filter, so a picker lists the values a person can switch to.
type AssetFacets struct {
	Tags    []FacetCount `json:"tags"`
	Authors []FacetCount `json:"authors"`
}

// MaxFacetValues bounds each facet's list.
const MaxFacetValues = 200

// MaxRankedDepth bounds how deep a ranked search pages: offset plus limit.
const MaxRankedDepth = 1000

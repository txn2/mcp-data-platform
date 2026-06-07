package portal

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
// view): OwnerID is mandatory and the search is restricted to the caller's own
// non-deleted assets. OwnerID (not owner_email) is the scope key because it is
// the ownership key the rest of the asset subsystem uses — the library list,
// handleList, and the update/delete ownership checks all key on owner_id, and
// owner_email is a secondary field that can diverge from it (API-key vs OIDC
// identity, differing configured email, empty on legacy rows). A nil Embedding
// selects lexical-only ranking (the graceful-degradation path when no embedding
// provider is configured); a non-nil Embedding selects hybrid ranking.
type AssetSearchQuery struct {
	Embedding []float32 // query vector; nil selects lexical-only ranking
	QueryText string    // raw query text for the lexical arm
	OwnerID   string    // caller identity; mandatory owner scope (owner_id)
	Limit     int       // max results; clamped into [1, maxSearchLimit]
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

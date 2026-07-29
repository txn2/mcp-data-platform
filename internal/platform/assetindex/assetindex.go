// Package assetindex is the saved-asset consumer of the shared indexjobs
// framework (#550). It registers a Source/Sink pair under source_kind =
// "portal-assets" so the reconciler embeds saved assets off the request path: a
// newly saved asset (whose vector is still NULL) or one left stale by a provider
// model swap self-heals on the next sweep.
//
// Like the prompt and memory consumers, assets store their vectors inline on the
// portal_assets table (one embedding per row), not in a dedicated vector table.
// So this package's Store reads and writes the embedding / embedding_model /
// embedding_text_hash columns of portal_assets directly: an asset IS its own
// indexing unit. SourceID is the asset id; each unit yields exactly one Item
// whose text is portal.AssetIndexText (name + description + tags).
//
// Only non-deleted assets are indexed. Gap detection and coverage both filter on
// deleted_at IS NULL, so a soft-deleted asset is never embedded and never
// counted as missing coverage.
package assetindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "portal-assets"

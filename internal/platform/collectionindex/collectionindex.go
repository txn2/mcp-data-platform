// Package collectionindex is the curated-collection consumer of the shared
// indexjobs framework (#550). It registers a Source/Sink pair under source_kind
// = "portal-collections" so the reconciler embeds collections off the request
// path: a newly created collection (whose vector is still NULL), one whose
// sections changed, or one left stale by a provider model swap self-heals on the
// next sweep.
//
// Like the prompt, memory, and asset consumers, collections store their vectors
// inline on the portal_collections table (one embedding per row). So this
// package's Store reads and writes the embedding / embedding_model /
// embedding_text_hash columns directly: a collection IS its own indexing unit.
// SourceID is the collection id; each unit yields exactly one Item whose text is
// portal.CollectionIndexText (name + description + the denormalized
// sections_text, i.e. section titles + descriptions).
//
// Only non-deleted collections are indexed. Gap detection and coverage both
// filter on deleted_at IS NULL.
package collectionindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "portal-collections"

// Package memoryindex is the memory consumer of the shared indexjobs
// framework (#507). It registers a Source/Sink pair under
// source_kind = "memory" so the reconciler backfills embeddings that the
// synchronous write path could not produce: a memory saved while the
// embedder was down (embedding NULL) or left stale by a provider model
// swap (embedding_model differs from the current model). Backfill runs
// off the request path; the interactive write-then-recall flow keeps its
// synchronous embed, so read-your-writes is preserved.
//
// Unlike the api-catalog and tools consumers, memory stores its vectors
// inline on the memory_records table (one embedding per record), not in a
// dedicated vector table. So this package's Sink reads and writes the
// embedding / embedding_model / embedding_text_hash columns of
// memory_records directly: a record IS its own indexing unit. SourceID is
// the record id; each unit yields exactly one Item.
package memoryindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "memory"

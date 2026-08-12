// Package resourceindex is the managed-resource consumer of the shared
// indexjobs framework (#1012). It registers a Source/Sink pair under
// source_kind = "resources" so uploaded reference material is embedded off the
// request path. A newly uploaded resource, one whose metadata was edited (the
// request-path Update clears the vector), and one whose content was revised each
// enqueue their own job at write time (#1256); the reconciler is the backstop for
// those a write could not produce and for the corpus a provider model swap
// invalidates.
//
// Like the prompt, memory, and portal-asset consumers, resources store their
// vectors inline on the resources table (one embedding per row), not in a
// dedicated vector table. So this package's Store reads and writes the
// embedding / embedding_model / embedding_text_hash columns of resources
// directly: a resource IS its own indexing unit. SourceID is the resource id;
// each unit yields exactly one Item whose text is resource.IndexText.
//
// # What makes this consumer different from assets
//
// An asset's indexable text lives entirely in Postgres; a resource's does not.
// The bytes a human uploaded live in S3, and they are the whole point of
// indexing a resource — a data dictionary has to be findable by a column name
// that appears only inside the file. So the Source additionally reads the blob
// through the same reader contract and bucket the resources/read middleware
// uses, extracts a bounded text prefix from text-family content (classified by
// pkg/contenttype, never by ad-hoc MIME prefix matching), and denormalizes it
// onto the row's content_text column, which the lexical FTS index covers.
//
// Blob reads fail in two distinguishable ways and the consumer treats them
// differently. A transient failure (S3 unreachable, credentials rejected)
// degrades to metadata-only indexing while KEEPING whatever content_text was
// already extracted, and leaves content_indexed_at NULL. That NULL is what makes
// the retry real: the metadata embed succeeds either way, so if the row's only
// gap signal were its embedding, the job would succeed, the row would stop being
// a gap, and the file's contents would never be indexed — while coverage
// reported it done. A confirmed missing object (resource.IsObjectNotFound) means
// the content is permanently gone, so the stale extracted text is cleared and
// the row settles. Neither case deletes the resource row: pruning an orphan
// stays the read path's decision (it prunes on a real read, where the caller
// sees the failure), because a background sweep against a misconfigured bucket
// must never be able to delete a user's uploads.
//
// Deleting a resource needs no work here: the index lives on the row, so the
// DELETE removes the index entry with it, and a job left over for a deleted
// resource reports indexjobs.ErrSourceGone so the worker resolves it cleanly.
package resourceindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "resources"

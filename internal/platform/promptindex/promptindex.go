// Package promptindex is the prompt-library consumer of the shared indexjobs
// framework (#557, epic #525 phase 4). It registers a Source/Sink pair under
// source_kind = "prompts" so the reconciler embeds prompts off the request
// path: a freshly created prompt (whose vector is still NULL) or one left
// stale by a provider model swap self-heals on the next sweep.
//
// Like the memory consumer, and unlike api-catalog/tools, prompts store their
// vectors inline on the prompts table (one embedding per row), not in a
// dedicated vector table. So this package's Store reads and writes the
// embedding / embedding_model / embedding_text_hash columns of prompts
// directly: a prompt IS its own indexing unit. SourceID is the prompt id; each
// unit yields exactly one Item whose text is prompt.IndexText (title + body +
// description + tags).
//
// Every enabled prompt is indexed regardless of lifecycle status (#1124):
// ranked search decides visibility at query time — a caller's own drafts and
// an admin's whole library rank — so the index covers what any caller can
// rank. Gap detection and coverage filter on enabled only; a disabled prompt
// is never embedded and never counted as missing coverage.
package promptindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "prompts"

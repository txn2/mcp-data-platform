// Package scriptindex is the managed-script consumer of the shared indexjobs
// framework (#1370). It registers a Source/Sink pair under source_kind =
// "scripts" so scripts are embedded off the request path. Scripts were the only
// kind of knowledge the platform holds with no consumer here, which meant a
// script was found only when the words a caller typed matched the words its
// author wrote — hardest exactly where a script is most valuable, since a person
// looking for automation asks for what they want done rather than for the
// identifier somebody assigned it.
//
// Like the prompt and memory consumers, and unlike api-catalog/tools, scripts
// store their vectors inline on the scripts table (one embedding per row), not
// in a dedicated vector table. So this package's Store reads and writes the
// embedding / embedding_model / embedding_text_hash columns of scripts
// directly: a script IS its own indexing unit. SourceID is the script id; each
// unit yields exactly one Item whose text is script.IndexText.
//
// What is embedded is the script's description card and never its Starlark.
// docs/scripts/security.md admits the contract to the script's owner and the
// source to that owner and to administrators; one vector per row,
// stored inline, cannot be split along that line, so a vector built partly from
// source would let code a caller may not read decide how their results rank.
//
// Every enabled script is indexed regardless of lifecycle status, mirroring
// prompts: ranked search decides visibility at query time — the store's Search
// applies the ownership predicate and the discoverable-status filter itself — so the
// index covers what any caller can rank. Gap detection and coverage filter on
// enabled only; a disabled script is never embedded and never counted as
// missing coverage.
package scriptindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "scripts"

// Package scriptindex is the managed-script consumer of the shared indexjobs
// framework (#1370). It registers a Source/Sink pair under source_kind =
// "scripts" so scripts are embedded off the request path, and a person asking
// for what they want done reaches the automation that does it rather than
// needing the words its author used.
//
// A script is embedded as a set of chunks (#2027): its description card, then
// its source in pieces each within the provider's input, so the reasoning in
// its comments and the tables its SQL names are found as well as its
// description, and no part of it is trimmed. The vectors live in
// script_embedding_chunks, one row per chunk, the way knowledge pages keep
// theirs (000097); search scores each script by its best chunk. SourceID is the
// script id, and an item id is "<script id>:<chunk>".
//
// Whether a script owes an embedding is read from two hashes on its row: the
// hash of what it is indexed on now (index_text_hash, written by every save)
// and the hash of what its chunks were built from (index_embedded_hash,
// written here), with the model that built them (index_model).
//
// Every enabled script is indexed regardless of lifecycle status: the store's
// search applies the discoverable-status filter at query time, so the index
// covers what any caller can rank. A disabled script is never embedded and
// never counted as missing coverage.
package scriptindex

import (
	"database/sql"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "scripts"

// RegisterConsumer registers the scripts Source/Sink pair, chunking each
// script to maxInputBytes per item.
func RegisterConsumer(reg interface {
	Register(indexjobs.Source, indexjobs.Sink) error
}, db *sql.DB, currentModel string, maxInputBytes int,
) error {
	store := NewStore(db)
	if err := reg.Register(NewSource(store, maxInputBytes), NewSink(store, currentModel)); err != nil {
		return fmt.Errorf("registering scripts index consumer: %w", err)
	}
	return nil
}

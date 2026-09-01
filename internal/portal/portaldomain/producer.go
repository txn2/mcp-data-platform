package portaldomain

import "strings"

// ContentProducer names what wrote a row, as an enumeration scopes by it: the
// producer kind and the producer's stable id, which are the two identifying
// columns of content_producers (migration 000135, #1569).
//
// It is how a managed-script run's own INVENTORY is scoped, and it is the only
// identifier that answers "what did THIS script write". The alternatives all
// name something else:
//
//   - the run's principal is script:<name>, and idx_scripts_name_owner is
//     UNIQUE (owner_email, name), so a name is unique only within its OWNER:
//     scoped on it, a run of one person's daily-sales enumerates the outputs of
//     another person's daily-sales (#1579);
//   - the address a run carries is a person's, so scoped on it a run enumerates
//     that person's whole library, which is the widening the identity binding
//     refuses (#1419);
//   - the pair of the two is neither, because the address a run's outputs
//     RECORD is the script owner's at the moment the row was inserted, and a
//     transfer changes the script's owner without rewriting a single asset row.
//
// A producer id is a script's uuid. It survives a rename, it survives a
// transfer, and it is recorded by the write itself rather than derived from the
// row afterwards, so what a run enumerates is exactly what its own writes
// produced.
//
// The kind is carried rather than assumed because asset ids, resource ids and
// collection ids are separate id spaces and the same string can name one of
// each, which is why content_producers keys on both.
type ContentProducer struct {
	// Kind is the producer kind: producedby.KindScript for a managed-script
	// run, and the session or person kinds for everything else. It is a plain
	// string so the store contract does not depend on the recording package.
	Kind string
	// ID is the producer's stable identity: a script's id, a session id, a
	// user subject.
	ID string
}

// NewContentProducer builds a producer scope, discarding a half-named one.
// Whitespace is trimmed, and a producer missing either half names nothing: a
// scope with an empty id would match every row written by that KIND.
func NewContentProducer(kind, id string) ContentProducer {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	if kind == "" || id == "" {
		return ContentProducer{}
	}
	return ContentProducer{Kind: kind, ID: id}
}

// Named reports whether this producer names something to scope by. A surface
// checks it before scoping a listing rather than passing a half-named producer
// to the store.
func (p ContentProducer) Named() bool { return p.Kind != "" && p.ID != "" }

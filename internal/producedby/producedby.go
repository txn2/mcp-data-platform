// Package producedby holds the relation between a file the platform stores and
// what wrote it: which scripts, sessions and people produced or modified a
// portal asset or a managed resource (#1569).
//
// It exists because no other record answers that question. An asset's
// provenance says which data calls its content was built from, and cannot be
// read backwards. A script's outputs were linked only by the asset's
// idempotency key, one string per declared output that nothing joins on. A
// resource recorded its writer as an uploader subject, which for a run is the
// script's NAME, so a rename severed the link.
//
// The relation is many-to-many: one script writes many files, and one file is
// written by many producers over its life. A producer is identified by ID --
// never by name -- so a rename changes what a surface displays and nothing
// else, and a deleted script leaves its rows behind.
//
// Recording is best effort at the write funnels every write already passes
// through. Losing the note that a write happened must never lose the write.
//
// Since #1579 the relation is also LOAD-BEARING for what a managed-script run
// enumerates: a run's asset listing, its ranked asset search, its collection
// listing and the references its fetch will dereference are all scoped by the
// producer recorded here, because neither identifier a row records names one
// script. A note that is dropped therefore leaves the asset or collection out
// of that run's inventory permanently, with the write itself standing and only
// the warning below to say so. The two properties pull against each other and
// the order is deliberate: a lost note is a missing row in one listing, a lost
// write is a missing file.
package producedby

import (
	"context"
	"time"
)

// Target kinds. Asset, resource and collection ids are separate id spaces, so
// the kind is part of every key and every lookup.
const (
	TargetAsset    = "asset"
	TargetResource = "resource"
	// TargetCollection was added by #1579, which needed the one identifier
	// that says WHICH script created a collection: a run's collections record
	// the principal script:<name>, and a script name is unique only within its
	// owner, so two owners' same-named scripts share one owner id.
	TargetCollection = "collection"
)

// Producer kinds.
//
// A managed-script run is a script; every other call arriving over MCP is the
// session it was made in, which is the unit a reader can open and follow; a
// write made through the portal's own HTTP surface is the person who made it.
// Exactly one kind is recorded per write: an MCP write is not also filed under
// the person behind the session, or every asset would list two producers for
// one save.
const (
	KindScript  = "script"
	KindSession = "session"
	KindPerson  = "person"
)

// Producer names one writer.
type Producer struct {
	// Kind is one of KindScript, KindSession or KindPerson.
	Kind string
	// ID is the stable identity: a script id, a session id, a user subject.
	ID string
	// Label is what the producer was called when it wrote -- a script's name,
	// a person's address -- kept so a surface can still say which script that
	// was after the script is deleted. Display only; empty for a session,
	// whose id is its own label.
	Label string
}

// Valid reports whether the producer names something recordable. A producer
// missing either half of its identity is dropped rather than written: a row
// with an empty id names nothing and would collide with every other such row.
func (p Producer) Valid() bool {
	if p.ID == "" {
		return false
	}
	switch p.Kind {
	case KindScript, KindSession, KindPerson:
		return true
	default:
		return false
	}
}

// Write is one write of one target by one producer, as recorded.
type Write struct {
	TargetKind string
	TargetID   string
	Producer   Producer
	// Created marks the write that brought the target into existence. It is
	// set on the row once and never cleared: a producer that created a file
	// and later modified it is still its creator.
	Created bool
	// Version is the target version this write produced, or zero for a target
	// whose kind does not number its writes.
	Version int
	// Uncounted marks a write that advances the producer's last version but not
	// its write count, because another note has already counted it. The only
	// such write is an asset's version 1: it is the content half of the create
	// the asset store recorded, and counting it again would report every first
	// save as two writes. The zero value counts, so a caller that says nothing
	// gets the ordinary behavior.
	Uncounted bool
}

// Row is one producer of one target, as read back.
type Row struct {
	TargetKind   string
	TargetID     string
	Producer     Producer
	Created      bool
	FirstWriteAt time.Time
	LastWriteAt  time.Time
	WriteCount   int
	LastVersion  int
}

// Store persists and queries the relation from both ends.
type Store interface {
	// Record notes one write, creating the producer's row for this target or
	// folding the write into the row already there.
	Record(ctx context.Context, w Write) error
	// ListByTarget returns everything that has written one file, the most
	// recent writer first.
	ListByTarget(ctx context.Context, targetKind, targetID string) ([]Row, error)
	// ListByProducer returns everything one producer has written, the most
	// recently written first, capped at limit (non-positive selects
	// DefaultProducerLimit).
	ListByProducer(ctx context.Context, producerKind, producerID string, limit int) ([]Row, error)
}

// DefaultProducerLimit caps a by-producer listing that names no limit of its
// own. A script that refreshes a few outputs on a schedule has a handful of
// rows; the cap is what keeps a script that writes a file per run from
// returning an unbounded page.
const DefaultProducerLimit = 200

// contextKey is this package's private context key type.
type contextKey int

const producerKey contextKey = iota

// With returns a copy of ctx carrying the producer every write made under it is
// recorded against.
//
// The producer is stamped by the surface that knows it: a managed-script run
// stamps its own script id where it already stamps the run's identity and
// session, the MCP middleware stamps the session or the person for every other
// tool call, and the portal's HTTP middleware stamps the person. An invalid
// producer is not stamped, so a caller that cannot name itself leaves the
// context as it found it rather than shadowing an outer producer with a blank.
func With(ctx context.Context, p Producer) context.Context {
	if !p.Valid() {
		return ctx
	}
	return context.WithValue(ctx, producerKey, p)
}

// From returns the producer carried by ctx, and whether there was one.
func From(ctx context.Context) (Producer, bool) {
	p, ok := ctx.Value(producerKey).(Producer)
	return p, ok
}

// Has reports whether ctx already names a producer. A stamping middleware that
// runs inside one that has already named a more specific producer -- the MCP
// chain inside a script run -- uses this to leave that one in place.
func Has(ctx context.Context) bool {
	_, ok := From(ctx)
	return ok
}

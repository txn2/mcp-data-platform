package tableregister

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// The ports a Registrar acts through. Each is declared here rather than
// imported from its implementation, so the registrar depends on the capability
// it needs and can be exercised against a fake that records exactly what it was
// asked to do.

// Executor runs a statement on a named Trino connection and reports the
// registration target that connection writes into. The Trino toolkit satisfies
// it.
type Executor interface {
	Exec(ctx context.Context, connection, sql string) error
	ScratchTarget(connection string) (trino.ScratchConfig, bool)
	// AcceptsWrites reports whether Exec would be allowed to run write SQL on
	// this connection. It is on the port rather than discovered by assertion
	// because the picker MUST ask it: a connection that carries a scratch
	// target but refuses writes was offered and then refused its DDL.
	AcceptsWrites(connection string) bool
	// TableExists reports whether the catalog on a connection still holds a
	// table. It is what a write that ran DROP TABLE asks about every OTHER
	// registration on the connection afterwards (#1546): an object store
	// whose prefix listing does not stop at a directory boundary lets one
	// table's drop take a name-prefix sibling's metadata with it, and the
	// registration row would otherwise go on describing a table that is
	// not there.
	TableExists(ctx context.Context, connection, catalog, schema, table string) (bool, error)
}

// ObjectReader reads the source object and lists what sits beside it.
type ObjectReader interface {
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
	ListDirectory(ctx context.Context, bucket, prefix string) (entries []ObjectEntry, truncated bool, err error)
}

// ObjectEntry names one object in a directory listing.
type ObjectEntry struct {
	Key  string
	Size int64
}

// Reviser saves corrected content as a new version of the source it came from,
// through the version mechanism that kind already has, and reports where the
// new head sits.
//
// It is a port rather than a copy of either version writer because the two
// kinds keep their trails in different tables: a managed resource records a
// revision, a portal asset records a version, and both move the head to a
// fresh per-version directory in one transaction. That directory rule is what
// the registrar needs and is the only thing it asks of either.
//
// The original object is never modified. A correction is the version on top of
// it, so the file a person uploaded stays what they uploaded and the change is
// revertible from the panel every other version is.
//
// summary says why the file changed, in the terms the person who uploaded it
// would use. Both kinds record it on the version they write, so a reader of
// either history sees the reason beside the version without having to find the
// registration that made it.
type Reviser interface {
	Revise(ctx context.Context, src Source, caller Caller, content []byte, summary string) (Revised, error)
}

// Revised is where a correction landed: the object the source's head now points
// at, and the number the version trail gave it.
type Revised struct {
	Bucket  string
	Key     string
	Version int
}

// AuditLogger is the write half of audit.Logger. The registrar records events
// and never reads them, so it names only what it uses; audit.Logger satisfies
// it.
type AuditLogger interface {
	Log(ctx context.Context, event audit.Event) error
}

// ConnectionScope is the persona connection boundary, the same predicate the
// authorizer applies to a tool call.
type ConnectionScope interface {
	AllowConnection(persona, connection string) bool
}

// SourceRef is what a cross-source listing has to say about the record a
// registration was built over: what it is called, where its content sits now,
// and whether this caller may act on it.
//
// It exists because the listing spans sources and the per-source reads do not.
// A registration alone cannot answer either question a reader of the list has
// -- which file is this, and is the table still reading its current contents --
// and resolving that one row at a time is what the per-source panels already do
// (#1472).
type SourceRef struct {
	// Name is what the source is called, for a reader who is looking at a
	// table name and does not recognize it.
	Name string
	// Bucket and HeadKey are where the source's content sits NOW, which is the
	// half IsStale needs and the registration does not carry.
	Bucket  string
	HeadKey string
	// CanModify is authority over the source, the half of the unregister rule
	// the registration cannot answer. It is resolved here rather than at the
	// point of the action because a listing has to decide whether to offer the
	// action at all, and offering one that is then refused is the same defect
	// as refusing one that was never offered.
	CanModify bool
}

// Sources resolves the sources a page of registrations names, one kind at a
// time, for one caller.
//
// It is the bulk form of Subject and is separate from it for two reasons. A
// listing reads a page of sources at once, so a per-id resolver would cost one
// store read per row. And a listing shows a registration the caller may see by
// its connection, which is not the same set as the sources they may change:
// authority is a field on the answer here rather than the answer itself.
//
// An id absent from the returned map names a source that is gone.
type Sources func(ctx context.Context, kind string, ids []string, caller Caller) map[string]SourceRef

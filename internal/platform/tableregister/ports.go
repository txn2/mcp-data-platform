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

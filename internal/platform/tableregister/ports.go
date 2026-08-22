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

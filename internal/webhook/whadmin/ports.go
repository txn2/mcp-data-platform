package whadmin

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

// The ports the service acts through.

// Sources is the source record store. whsource.Store satisfies it.
type Sources interface {
	List(ctx context.Context) ([]whsource.Source, error)
	Get(ctx context.Context, name string) (whsource.Source, error)
	Create(ctx context.Context, src whsource.Source) error
	Update(ctx context.Context, src whsource.Source) error
	Delete(ctx context.Context, name string) error
}

// Tables is the query-engine side. whtable.Tables satisfies it.
type Tables interface {
	TargetFor(connection string) (whtable.Target, error)
	S3Location(prefix string) string
	Create(ctx context.Context, tg whtable.Target, src whsource.Source) error
	Probe(ctx context.Context, tg whtable.Target, src whsource.Source) error
	Drop(ctx context.Context, tg whtable.Target, src whsource.Source) error
}

// Registrations records the source's table. tableregister's store satisfies
// it.
type Registrations interface {
	Insert(ctx context.Context, r tableregister.Registration) error
	ByName(ctx context.Context, connection, catalog, schema, table string) (*tableregister.Registration, error)
	BySource(ctx context.Context, kind, sourceID string) ([]tableregister.Registration, error)
	Delete(ctx context.Context, id string) error
}

// Windows reads a source's status and the resources its windows were written as.
type Windows interface {
	Status(ctx context.Context, source string, now time.Time) (whstore.Status, error)
	ResourceIDs(ctx context.Context, source string) ([]string, error)
}

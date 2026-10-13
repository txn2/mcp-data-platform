package catalogscan

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Source lists the datasets one kind of connection can see.
type Source interface {
	// Kind is the connection kind, the first part of every reference the
	// source produces.
	Kind() string
	// Connections are the live connections of this kind.
	Connections() []Connection
	// Datasets lists what the connection can see, skipping any dataset the
	// exclusions name. It reports each dataset through yield so a large
	// connection is not held in memory at once.
	Datasets(ctx context.Context, c Connection, skip Exclusions, yield func(catalog.DatasetInput) error) error
	// Types are the dataset types the source produces, the ones a scan of it
	// may mark removed.
	Types() []catalog.DatasetType
}

// Writer is what a scan needs from the catalog store.
type Writer interface {
	PutDataset(ctx context.Context, in catalog.DatasetInput) (*catalog.Dataset, error)
	MarkRemoved(ctx context.Context, r catalog.Removal) (int, error)
}

// Recorder keeps each connection's last outcome.
type Recorder interface {
	RecordOutcome(ctx context.Context, o Outcome) error
}

// TrinoReader is what the Trino source reads through. Columns must report a
// table's rows together, in column order, and must skip information_schema.
type TrinoReader interface {
	Connections() []Connection
	Catalogs(ctx context.Context, bound string) ([]string, error)
	Columns(ctx context.Context, bound, catalogName string, fn func(ColumnRow) error) error
}

// OperationReader lists the operations of one kind of connection and the
// schema documents they were read from.
type OperationReader interface {
	Connections() []Connection
	Operations(ctx context.Context, bound string) ([]Operation, error)
	// Documents are the connection's schema documents, keyed as each
	// Operation's Document names them. A connection with none returns none.
	Documents(ctx context.Context, bound string) ([]Document, error)
}

// Document is a source's schema document as the platform holds it: an
// OpenAPI spec, a GraphQL SDL.
type Document struct {
	// Key is what an Operation's Document names it by.
	Key string
	// Filename is the name the document is kept under, extension included.
	Filename string
	MIMEType string
	Content  []byte
}

// DocumentKeeper keeps a connection's schema document as a managed resource
// and returns the resource's id, revising the resource only when the document
// changed.
type DocumentKeeper interface {
	Keep(ctx context.Context, kind, connection string, doc Document) (resourceID string, err error)
}

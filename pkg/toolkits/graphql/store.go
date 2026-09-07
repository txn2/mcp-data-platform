package graphql

import (
	"context"
	"errors"
	"time"
)

// Schema provenance values recorded on a stored schema, so an operator
// reading a connection knows whether what the platform holds came from
// the endpoint or from them.
const (
	// SchemaSourceIntrospection marks a schema read from the endpoint.
	SchemaSourceIntrospection = "introspection"
	// SchemaSourceUpload marks a schema an operator supplied through
	// the admin route, which is the path for an endpoint with
	// introspection disabled.
	SchemaSourceUpload = "upload"
)

// ErrSchemaNotFound reports a connection with no stored schema.
var ErrSchemaNotFound = errors.New("graphql: no stored schema for connection")

// StoredSchema is one connection's schema as it is kept between
// restarts: the SDL, the hash that identifies it, where it came from,
// and when it was read. FetchedAt is what a strict-mode refusal names,
// so a caller working from a newer schema than the platform holds can
// see that is what happened.
type StoredSchema struct {
	// Connection is the connection name the schema belongs to.
	Connection string
	// Hash is the sha256 of the SDL, hex-encoded. An operation index
	// and its embeddings are keyed on it.
	Hash string
	// SDL is the schema text.
	SDL string
	// Source is SchemaSourceIntrospection or SchemaSourceUpload.
	Source string
	// FetchedAt is when the schema was read or uploaded.
	FetchedAt time.Time
}

// SchemaStore persists a connection's schema. A deployment without one
// (no database) still works: every connection introspects at
// registration and holds its schema in memory for the process's life.
// What the store buys is a restart that does not re-read every
// endpoint, and an operation index that survives one.
type SchemaStore interface {
	// GetSchema returns the stored schema for a connection, or an error
	// wrapping ErrSchemaNotFound when there is none.
	GetSchema(ctx context.Context, connection string) (StoredSchema, error)
	// PutSchema writes a connection's schema, replacing any previous
	// one.
	PutSchema(ctx context.Context, s StoredSchema) error
	// DeleteSchema removes a connection's schema, called when the
	// connection is deleted.
	DeleteSchema(ctx context.Context, connection string) error
}

// VectorReader loads the operation embeddings written by the platform's
// index-jobs consumer. The toolkit only reads them: a connection never
// embeds its own operations on a request path, the same way the API
// gateway's connections never embed their catalog.
type VectorReader interface {
	// LoadVectors returns the persisted vectors for one connection's
	// schema version, keyed by operation id. An empty map means the
	// schema has not been indexed yet; ranking falls back to lexical
	// and says so.
	LoadVectors(ctx context.Context, connection, schemaHash string) (map[string][]float32, error)
}

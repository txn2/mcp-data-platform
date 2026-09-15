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
	// SchemaSourceCatalog marks a schema taken from the API catalog the
	// connection references, rather than read from its endpoint or
	// handed over by an operator (#1745). The catalog is the schema's
	// home; what is stored here is what the connection installed from
	// it, so a replica that cannot reach the catalog still serves the
	// version every replica agreed on.
	SchemaSourceCatalog = "catalog"
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
	// ReadError is why the last read of the connection's endpoint was
	// refused, empty when the last read or upload installed this schema.
	// It is kept with the schema so every replica, and a restart, reports
	// the refusal beside the schema that survived it (#1703).
	ReadError string
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
	// SchemaVersion returns what names the stored schema's version, the
	// hash, source, read time and recorded refusal, without the schema
	// itself, or an error wrapping ErrSchemaNotFound when there is none.
	// It is read on every request that uses a connection's schema, so
	// the SDL is left out of it.
	SchemaVersion(ctx context.Context, connection string) (StoredSchema, error)
	// PutSchema writes a connection's schema, replacing any previous
	// one.
	PutSchema(ctx context.Context, s StoredSchema) error
	// RecordReadError records s.ReadError, why a read of the endpoint was
	// refused, beside the stored schema for s.Connection without touching
	// the schema. It records only when the store still holds the version
	// s names by Hash and FetchedAt, the one the reader held when its
	// read began: a refusal is about that version, and a reader that had
	// not yet installed a newer one, or held none, must not mark the newer
	// one refused. Recording nothing is not an error.
	RecordReadError(ctx context.Context, s StoredSchema) error
	// DeleteSchema removes a connection's schema, called when the
	// connection is deleted.
	DeleteSchema(ctx context.Context, connection string) error
}

// ConnectionStore reads the configuration a connection is saved under,
// which every replica of a deployment shares. It is how a replica answers a
// request for a connection another replica saved before the announcement of
// that save has reached it (#1714).
type ConnectionStore interface {
	// GetConnection returns the saved configuration of the graphql
	// connection name, in the generic form AddConnection takes, or an
	// error wrapping ErrConnectionNotFound when none is saved.
	GetConnection(ctx context.Context, name string) (map[string]any, error)
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

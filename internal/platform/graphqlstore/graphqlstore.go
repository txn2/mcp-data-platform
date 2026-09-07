// Package graphqlstore is the PostgreSQL persistence behind the
// platform's graphql connection kind: the schema each connection was
// last read with, and the per-operation embedding vectors that ranking
// scores against.
//
// It is the concrete for two contracts the toolkit declares
// (graphql.SchemaStore and graphql.VectorReader) plus the vector writes
// the index-jobs consumer performs. Keeping it here rather than under
// pkg/ is what keeps the toolkit free of a database dependency: a
// deployment with no database still registers graphql connections,
// introspects them at startup, and ranks them lexically.
package graphqlstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/pgvector/pgvector-go"

	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// maxSDLBytes bounds what is decompressed out of a stored schema. A row
// is written by this package from a schema the toolkit parsed, so the
// bound is a guard on a corrupted or hostile row rather than an
// expected limit; 32 MiB is far past the largest published schema.
const maxSDLBytes = 32 << 20

// Store persists graphql schemas and operation embeddings.
type Store struct {
	db *sql.DB
}

// New builds a store over the platform's database handle.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Compile-time proof that the store satisfies the toolkit's contracts,
// so a change to either side is a build failure rather than a nil
// interface at startup.
var (
	_ graphqlkit.SchemaStore  = (*Store)(nil)
	_ graphqlkit.VectorReader = (*Store)(nil)
)

// GetSchema returns a connection's stored schema.
func (s *Store) GetSchema(ctx context.Context, connection string) (graphqlkit.StoredSchema, error) {
	const q = `SELECT schema_hash, sdl_gzip, source, fetched_at
	             FROM graphql_connection_schemas
	            WHERE connection = $1`
	var (
		hash      string
		gz        []byte
		source    string
		fetchedAt time.Time
	)
	err := s.db.QueryRowContext(ctx, q, connection).Scan(&hash, &gz, &source, &fetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return graphqlkit.StoredSchema{}, fmt.Errorf("connection %s: %w", connection, graphqlkit.ErrSchemaNotFound)
	}
	if err != nil {
		return graphqlkit.StoredSchema{}, fmt.Errorf("graphqlstore: reading schema: %w", err)
	}
	sdl, err := decompress(gz)
	if err != nil {
		return graphqlkit.StoredSchema{}, err
	}
	return graphqlkit.StoredSchema{
		Connection: connection, Hash: hash, SDL: sdl,
		Source: source, FetchedAt: fetchedAt,
	}, nil
}

// PutSchema writes a connection's schema, replacing any previous one.
func (s *Store) PutSchema(ctx context.Context, schema graphqlkit.StoredSchema) error {
	gz, err := compress(schema.SDL)
	if err != nil {
		return err
	}
	const q = `INSERT INTO graphql_connection_schemas
	                (connection, schema_hash, sdl_gzip, source, fetched_at, updated_at)
	           VALUES ($1, $2, $3, $4, $5, NOW())
	      ON CONFLICT (connection) DO UPDATE
	              SET schema_hash = EXCLUDED.schema_hash,
	                  sdl_gzip    = EXCLUDED.sdl_gzip,
	                  source      = EXCLUDED.source,
	                  fetched_at  = EXCLUDED.fetched_at,
	                  updated_at  = NOW()`
	if _, err := s.db.ExecContext(ctx, q, schema.Connection, schema.Hash, gz, schema.Source, schema.FetchedAt); err != nil {
		return fmt.Errorf("graphqlstore: writing schema: %w", err)
	}
	return nil
}

// DeleteSchema removes a connection's schema and every embedding built
// from it. A connection that is gone must not leave an index behind for
// a later connection of the same name to inherit.
func (s *Store) DeleteSchema(ctx context.Context, connection string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM graphql_operation_embeddings WHERE connection = $1`, connection); err != nil {
		return fmt.Errorf("graphqlstore: deleting embeddings: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM graphql_connection_schemas WHERE connection = $1`, connection); err != nil {
		return fmt.Errorf("graphqlstore: deleting schema: %w", err)
	}
	return nil
}

// LoadVectors returns the persisted embeddings for one connection's
// schema version, keyed by operation id.
func (s *Store) LoadVectors(ctx context.Context, connection, schemaHash string) (map[string][]float32, error) {
	const q = `SELECT operation_id, embedding
	             FROM graphql_operation_embeddings
	            WHERE connection = $1 AND schema_hash = $2`
	rows, err := s.db.QueryContext(ctx, q, connection, schemaHash)
	if err != nil {
		return nil, fmt.Errorf("graphqlstore: reading embeddings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]float32{}
	for rows.Next() {
		var (
			id  string
			vec pgvector.Vector
		)
		if err := rows.Scan(&id, &vec); err != nil {
			return nil, fmt.Errorf("graphqlstore: scanning embedding: %w", err)
		}
		out[id] = vec.Slice()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphqlstore: reading embeddings: %w", err)
	}
	return out, nil
}

// compress gzips SDL for storage. A published schema is largely
// repeated type and field syntax, so it compresses by roughly an order
// of magnitude; the point is that a row stays small enough to read on
// every connection reload.
func compress(sdl string) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(sdl)); err != nil {
		return nil, fmt.Errorf("graphqlstore: compressing schema: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("graphqlstore: compressing schema: %w", err)
	}
	return buf.Bytes(), nil
}

// decompress reads a stored schema back, bounded so a corrupted row
// cannot expand without limit.
func decompress(gz []byte) (string, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return "", fmt.Errorf("graphqlstore: reading stored schema: %w", err)
	}
	defer func() { _ = r.Close() }()
	// One byte past the bound so an oversized row is refused rather than
	// silently handed back cut in half, which would fail to parse later
	// with no mention of the cap that did it.
	sdl, err := io.ReadAll(io.LimitReader(r, maxSDLBytes+1))
	if err != nil {
		return "", fmt.Errorf("graphqlstore: reading stored schema: %w", err)
	}
	if len(sdl) > maxSDLBytes {
		return "", fmt.Errorf("graphqlstore: the stored schema exceeds %d bytes", maxSDLBytes)
	}
	return string(sdl), nil
}

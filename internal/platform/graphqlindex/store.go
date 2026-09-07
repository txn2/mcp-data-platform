package graphqlindex

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Store persists the per-operation embedding vectors in
// graphql_operation_embeddings. It is the write half of what
// internal/platform/graphqlstore reads at connection load time; the two
// address the same rows, keyed on (connection, schema_hash,
// operation_id).
type Store struct {
	db *sql.DB
}

// NewStore returns a store over the platform's database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ListVectors returns the persisted vectors for one connection's schema
// version, keyed by operation id.
func (s *Store) ListVectors(ctx context.Context, connection, schemaHash string) (map[string]indexjobs.Vector, error) {
	const q = `SELECT operation_id, text_hash, embedding, model, dim
	             FROM graphql_operation_embeddings
	            WHERE connection = $1 AND schema_hash = $2`
	rows, err := s.db.QueryContext(ctx, q, connection, schemaHash)
	if err != nil {
		return nil, fmt.Errorf("graphqlindex: listing vectors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]indexjobs.Vector)
	for rows.Next() {
		var (
			v   indexjobs.Vector
			vec pgvector.Vector
		)
		if err := rows.Scan(&v.ItemID, &v.TextHash, &vec, &v.Model, &v.Dim); err != nil {
			return nil, fmt.Errorf("graphqlindex: scanning vector: %w", err)
		}
		v.Embedding = vec.Slice()
		out[v.ItemID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphqlindex: listing vectors: %w", err)
	}
	return out, nil
}

// Replace swaps the whole vector set for one connection's schema
// version in a single transaction, so an operation the schema no longer
// exposes loses its row.
func (s *Store) Replace(ctx context.Context, connection, schemaHash string, rows []indexjobs.Vector) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphqlindex: replace begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	const del = `DELETE FROM graphql_operation_embeddings WHERE connection = $1 AND schema_hash = $2`
	if _, err := tx.ExecContext(ctx, del, connection, schemaHash); err != nil {
		return fmt.Errorf("graphqlindex: replace delete: %w", err)
	}
	if err := insertVectors(ctx, tx, connection, schemaHash, rows); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphqlindex: replace commit: %w", err)
	}
	return nil
}

// UpsertBatch writes one chunk in place without disturbing rows outside
// it.
func (s *Store) UpsertBatch(ctx context.Context, connection, schemaHash string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graphqlindex: upsert begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertVectors(ctx, tx, connection, schemaHash, rows); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graphqlindex: upsert commit: %w", err)
	}
	return nil
}

// insertVectors writes rows as an idempotent upsert inside tx.
func insertVectors(ctx context.Context, tx *sql.Tx, connection, schemaHash string, rows []indexjobs.Vector) error {
	const q = `INSERT INTO graphql_operation_embeddings
	                (connection, schema_hash, operation_id, text_hash, embedding, model, dim, updated_at)
	           VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	      ON CONFLICT (connection, schema_hash, operation_id) DO UPDATE
	              SET text_hash  = EXCLUDED.text_hash,
	                  embedding  = EXCLUDED.embedding,
	                  model      = EXCLUDED.model,
	                  dim        = EXCLUDED.dim,
	                  updated_at = NOW()`
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, q,
			connection, schemaHash, r.ItemID, r.TextHash,
			pgvector.NewVector(r.Embedding), r.Model, r.Dim); err != nil {
			return fmt.Errorf("graphqlindex: inserting vector %s: %w", r.ItemID, err)
		}
	}
	return nil
}

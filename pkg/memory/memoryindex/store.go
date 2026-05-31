package memoryindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Store reads and writes memory embedding state on the memory_records
// table for the indexjobs memory consumer. It is intentionally separate
// from the memory.Store interface: it touches only the embedding columns
// (embedding, embedding_model, embedding_text_hash) and is scoped to the
// backfill path, so it does not widen the request-path store contract.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errArchivedOrMissing is returned by GetContent when the record is gone
// or archived, so the Source can treat the unit as nothing to index.
var errArchivedOrMissing = errors.New("memoryindex: record archived or missing")

// GetContent returns the active record's content. A record that is
// archived or absent yields errArchivedOrMissing so the Source returns
// an empty item set (a clean "nothing to index" completion).
func (s *Store) GetContent(ctx context.Context, id string) (string, error) {
	const q = `SELECT content FROM memory_records WHERE id = $1 AND status <> 'archived'`
	var content string
	err := s.db.QueryRowContext(ctx, q, id).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errArchivedOrMissing
	}
	if err != nil {
		return "", fmt.Errorf("memoryindex: get content: %w", err)
	}
	return content, nil
}

// ListVectors returns the record's persisted embedding keyed by item id
// (the record id), for the worker's text-hash + model dedup pass. A
// record with no embedding yields an empty map, so the worker embeds it.
func (s *Store) ListVectors(ctx context.Context, id string) (map[string]indexjobs.Vector, error) {
	const q = `
		SELECT embedding, embedding_model, embedding_text_hash
		  FROM memory_records
		 WHERE id = $1 AND embedding IS NOT NULL`
	var (
		vec   pgvector.Vector
		model string
		hash  []byte
	)
	err := s.db.QueryRowContext(ctx, q, id).Scan(&vec, &model, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]indexjobs.Vector{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memoryindex: list vectors: %w", err)
	}
	embedding := vec.Slice()
	return map[string]indexjobs.Vector{
		id: {
			ItemID:    id,
			TextHash:  hash,
			Embedding: embedding,
			Model:     model,
			Dim:       len(embedding),
		},
	}, nil
}

// UpsertVectors writes the embedding back onto the record. The memory
// unit holds exactly one item (the record itself); a missing or empty
// row set is a no-op. The id predicate plus the single-row contract make
// Upsert and UpsertBatch identical for memory (there are no sibling rows
// to delete), so both delegate here.
func (s *Store) UpsertVectors(ctx context.Context, id string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	const q = `
		UPDATE memory_records
		   SET embedding           = $2,
		       embedding_model     = $3,
		       embedding_text_hash = $4,
		       updated_at          = NOW()
		 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q,
		id, pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
		return fmt.Errorf("memoryindex: upsert vectors: %w", err)
	}
	return nil
}

// FindGaps returns the ids of active records whose embedding is missing
// or was produced by a model other than the current provider's. Missing
// embeddings cover the embedder-outage case (a memory saved while the
// provider was down); the model mismatch covers a provider model swap.
// Both converge off the request path when the reconciler enqueues them.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `
		SELECT id
		  FROM memory_records
		 WHERE status <> 'archived'
		   AND (embedding IS NULL OR embedding_model IS DISTINCT FROM $1)`
	rows, err := s.db.QueryContext(ctx, q, currentModel)
	if err != nil {
		return nil, fmt.Errorf("memoryindex: find gaps: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("memoryindex: find gaps scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memoryindex: find gaps rows: %w", err)
	}
	return ids, nil
}

// Coverage returns the number of active records with an embedding
// (indexed) and the total number of active records (expected). The
// memory kind reports a real indexed/expected ratio because every active
// record is expected to carry a vector once converged.
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	const q = `
		SELECT
			COUNT(*) FILTER (WHERE embedding IS NOT NULL) AS indexed,
			COUNT(*)                                      AS expected
		  FROM memory_records
		 WHERE status <> 'archived'`
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("memoryindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

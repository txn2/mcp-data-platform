package callindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Store reads and writes the embedding state of call_records for this
// consumer. It is deliberately separate from the catalog's own store: it
// touches only the embedding columns and the text they are computed from, so
// the read-path contract does not widen to carry a backfill concern.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errNotIndexable is returned when a record is gone, or holds nothing worth
// embedding, so the Source can treat the unit as a clean nothing-to-index.
var errNotIndexable = errors.New("callindex: record missing or not indexable")

// hasTextExpr is what makes a record indexable at all: it said something, or it
// did something nameable. A record with neither is not a gap, it is nothing to
// embed.
const hasTextExpr = `(purpose <> '' OR statement <> '' OR path <> '' OR operation_id <> '')`

// GetText returns the text of one record to embed, composed by the catalog's
// own IndexText so the vector is computed from exactly the corpus the lexical
// arm matches. A record that no longer exists, failed, or has nothing to say
// yields errNotIndexable.
func (s *Store) GetText(ctx context.Context, id string) (string, error) {
	const q = `
		SELECT purpose, statement, method, path, operation_id
		  FROM call_records
		 WHERE id = $1 AND success`
	var rec callrecord.Record
	err := s.db.QueryRowContext(ctx, q, id).
		Scan(&rec.Purpose, &rec.Statement, &rec.Method, &rec.Path, &rec.OperationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotIndexable
	}
	if err != nil {
		return "", fmt.Errorf("callindex: get text: %w", err)
	}
	text := callrecord.IndexText(rec)
	if text == "" {
		return "", errNotIndexable
	}
	return text, nil
}

// ListVectors returns the record's persisted embedding keyed by item id (the
// record id), for the worker's text-hash and model dedup pass.
func (s *Store) ListVectors(ctx context.Context, id string) (map[string]indexjobs.Vector, error) {
	const q = `
		SELECT embedding, embedding_model, embedding_text_hash
		  FROM call_records
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
		return nil, fmt.Errorf("callindex: list vectors: %w", err)
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

// UpsertVectors writes the embedding back onto the record. A call record holds
// exactly one item, so there are no sibling rows to delete and Upsert and
// UpsertBatch are the same write.
func (s *Store) UpsertVectors(ctx context.Context, id string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	const q = `
		UPDATE call_records
		   SET embedding           = $2,
		       embedding_model     = $3,
		       embedding_text_hash = $4,
		       updated_at          = NOW()
		 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q,
		id, pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
		return fmt.Errorf("callindex: upsert vectors: %w", err)
	}
	return nil
}

// gapPredicate is what makes a record a gap: it succeeded, it has text worth
// embedding, and its vector is missing or was produced by another model. The
// first condition is what keeps the queue from filling with failed calls, which
// are never search results.
const gapPredicate = `success AND ` + hasTextExpr + ` AND
	(embedding IS NULL OR embedding_model IS DISTINCT FROM $1)`

// FindGaps returns the ids of records whose embedding is missing or stale.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	q := `SELECT id FROM call_records WHERE ` + gapPredicate
	rows, err := s.db.QueryContext(ctx, q, currentModel)
	if err != nil {
		return nil, fmt.Errorf("callindex: find gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("callindex: find gaps scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("callindex: find gaps rows: %w", err)
	}
	return ids, nil
}

// Coverage returns how many indexable records carry a vector, and how many
// there are. Both counts are over the same population the gap query walks, so a
// converged catalog reads as complete rather than as permanently short by the
// records that were never meant to be embedded.
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	q := `
		SELECT
			COUNT(*) FILTER (WHERE embedding IS NOT NULL) AS indexed,
			COUNT(*)                                      AS expected
		  FROM call_records
		 WHERE success AND ` + hasTextExpr
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("callindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

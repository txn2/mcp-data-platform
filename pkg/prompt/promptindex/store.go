package promptindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Store reads and writes prompt embedding state on the prompts table for the
// indexjobs prompts consumer. It is intentionally separate from prompt.Store:
// it touches only the embedding columns (embedding, embedding_model,
// embedding_text_hash) and is scoped to the backfill path, so it does not widen
// the request-path store contract. The request-path Store clears these columns
// when a prompt's indexed text changes; this Store writes them back.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errNotIndexable is returned by GetIndexText when the prompt is missing or no
// longer approved+enabled, so the Source treats the unit as nothing to index.
var errNotIndexable = errors.New("promptindex: prompt missing or not approved")

// GetIndexText returns the composed embed text for an approved, enabled prompt.
// A prompt that was deprecated, disabled, or deleted between enqueue and claim
// yields errNotIndexable so the Source returns an empty item set (a clean
// "nothing to index" completion). The composition is prompt.IndexText, the same
// one the request-path search ranks against.
func (s *Store) GetIndexText(ctx context.Context, id string) (string, error) {
	const q = `
		SELECT display_name, name, description, content, tags
		  FROM prompts
		 WHERE id = $1 AND status = 'approved' AND enabled = true`
	var p prompt.Prompt
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&p.DisplayName, &p.Name, &p.Description, &p.Content, pq.Array(&p.Tags))
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotIndexable
	}
	if err != nil {
		return "", fmt.Errorf("promptindex: get index text: %w", err)
	}
	return prompt.IndexText(&p), nil
}

// ListVectors returns the prompt's persisted embedding keyed by item id (the
// prompt id), for the worker's text-hash + model dedup pass. A prompt with no
// embedding yields an empty map, so the worker embeds it.
func (s *Store) ListVectors(ctx context.Context, id string) (map[string]indexjobs.Vector, error) {
	const q = `
		SELECT embedding, embedding_model, embedding_text_hash
		  FROM prompts
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
		return nil, fmt.Errorf("promptindex: list vectors: %w", err)
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

// UpsertVectors writes the embedding back onto the prompt. The prompt unit holds
// exactly one item (the prompt itself); a missing or empty row set is a no-op.
// updated_at is deliberately left untouched: a background embed is not a
// user-visible edit, so the prompt's "last modified" timestamp must not move.
func (s *Store) UpsertVectors(ctx context.Context, id string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	const q = `
		UPDATE prompts
		   SET embedding           = $2,
		       embedding_model     = $3,
		       embedding_text_hash = $4
		 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q,
		id, pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
		return fmt.Errorf("promptindex: upsert vectors: %w", err)
	}
	return nil
}

// FindGaps returns the ids of approved, enabled prompts whose embedding is
// missing or was produced by a model other than the current provider's. Missing
// embeddings cover a freshly approved prompt (and a content edit, which the
// request-path Update clears the embedding for); the model mismatch covers a
// provider model swap. Both converge off the request path when the reconciler
// enqueues them.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `
		SELECT id
		  FROM prompts
		 WHERE status = 'approved' AND enabled = true
		   AND (embedding IS NULL OR embedding_model IS DISTINCT FROM $1)`
	rows, err := s.db.QueryContext(ctx, q, currentModel)
	if err != nil {
		return nil, fmt.Errorf("promptindex: find gaps: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("promptindex: find gaps scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("promptindex: find gaps rows: %w", err)
	}
	return ids, nil
}

// Coverage returns the number of approved+enabled prompts with an embedding
// (indexed) and the total number of approved+enabled prompts (expected). Every
// approved prompt is expected to carry a vector once converged.
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	const q = `
		SELECT
			COUNT(*) FILTER (WHERE embedding IS NOT NULL) AS indexed,
			COUNT(*)                                      AS expected
		  FROM prompts
		 WHERE status = 'approved' AND enabled = true`
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("promptindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

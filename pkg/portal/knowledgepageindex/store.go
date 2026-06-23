package knowledgepageindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// Store reads and writes knowledge-page embedding state on the
// portal_knowledge_pages table for the indexjobs knowledge-pages consumer. It is
// intentionally separate from portal.KnowledgePageStore: it touches only the
// embedding columns and is scoped to the backfill path, so it does not widen the
// request-path store contract. The request-path Create/Update clears these
// columns when a page's indexed text changes; this Store writes them back.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errNotIndexable is returned by GetIndexText when the page is missing or
// soft-deleted, so the Source treats the unit as nothing to index.
var errNotIndexable = errors.New("knowledgepageindex: page missing or deleted")

// GetIndexText returns the composed embed text for a non-deleted page. A page
// soft-deleted between enqueue and claim yields errNotIndexable so the Source
// returns an empty item set. The composition is knowledgepage.IndexText,
// the same one the request-path search ranks against.
func (s *Store) GetIndexText(ctx context.Context, id string) (string, error) {
	const q = `SELECT title, body, tags FROM portal_knowledge_pages WHERE id = $1 AND deleted_at IS NULL`
	var (
		title, body string
		tagsJSON    []byte
	)
	err := s.db.QueryRowContext(ctx, q, id).Scan(&title, &body, &tagsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotIndexable
	}
	if err != nil {
		return "", fmt.Errorf("knowledgepageindex: get index text: %w", err)
	}
	var tags []string
	if len(tagsJSON) > 0 {
		if err := json.Unmarshal(tagsJSON, &tags); err != nil {
			return "", fmt.Errorf("knowledgepageindex: unmarshal tags: %w", err)
		}
	}
	return knowledgepage.IndexText(title, body, tags), nil
}

// ListVectors returns the page's persisted embedding keyed by item id (the page
// id), for the worker's text-hash + model dedup pass. A page with no embedding
// yields an empty map, so the worker embeds it.
func (s *Store) ListVectors(ctx context.Context, id string) (map[string]indexjobs.Vector, error) {
	const q = `SELECT embedding, embedding_model, embedding_text_hash FROM portal_knowledge_pages WHERE id = $1 AND embedding IS NOT NULL`
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
		return nil, fmt.Errorf("knowledgepageindex: list vectors: %w", err)
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

// UpsertVectors writes the embedding back onto the page. The page unit holds
// exactly one item; a missing or empty row set is a no-op. updated_at is left
// untouched: a background embed is not a user-visible edit, so the page's "last
// modified" timestamp must not move.
func (s *Store) UpsertVectors(ctx context.Context, id string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	const q = `UPDATE portal_knowledge_pages SET embedding = $2, embedding_model = $3, embedding_text_hash = $4 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q, id, pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
		return fmt.Errorf("knowledgepageindex: upsert vectors: %w", err)
	}
	return nil
}

// FindGaps returns the ids of non-deleted pages whose embedding is missing or
// was produced by a model other than the current provider's.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `SELECT id FROM portal_knowledge_pages
		WHERE deleted_at IS NULL AND (embedding IS NULL OR embedding_model IS DISTINCT FROM $1)`
	rows, err := s.db.QueryContext(ctx, q, currentModel)
	if err != nil {
		return nil, fmt.Errorf("knowledgepageindex: find gaps: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("knowledgepageindex: find gaps scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("knowledgepageindex: find gaps rows: %w", err)
	}
	return ids, nil
}

// Coverage returns the number of non-deleted pages with an embedding (indexed)
// and the total number of non-deleted pages (expected).
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	const q = `SELECT COUNT(*) FILTER (WHERE embedding IS NOT NULL) AS indexed, COUNT(*) AS expected
		FROM portal_knowledge_pages WHERE deleted_at IS NULL`
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("knowledgepageindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

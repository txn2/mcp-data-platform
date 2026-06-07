package collectionindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Store reads and writes collection embedding state on the portal_collections
// table for the indexjobs collections consumer. It touches only the embedding
// columns (embedding, embedding_model, embedding_text_hash) and the denormalized
// sections_text it reads, so it does not widen the request-path store contract.
// The request-path Update/SetSections clears the embedding when a collection's
// indexed text changes; this Store writes it back.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errNotIndexable is returned by GetIndexText when the collection is missing or
// soft-deleted, so the Source treats the unit as nothing to index.
var errNotIndexable = errors.New("collectionindex: collection missing or deleted")

// GetIndexText returns the composed embed text for a non-deleted collection. A
// collection soft-deleted between enqueue and claim yields errNotIndexable so
// the Source returns an empty item set. The composition is
// portal.CollectionIndexText, the same one the request-path search ranks
// against (name + description + sections_text).
func (s *Store) GetIndexText(ctx context.Context, id string) (string, error) {
	const q = `SELECT name, description, sections_text FROM portal_collections WHERE id = $1 AND deleted_at IS NULL`
	var name, description, sectionsText string
	err := s.db.QueryRowContext(ctx, q, id).Scan(&name, &description, &sectionsText)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotIndexable
	}
	if err != nil {
		return "", fmt.Errorf("collectionindex: get index text: %w", err)
	}
	return portal.CollectionIndexText(name, description, sectionsText), nil
}

// ListVectors returns the collection's persisted embedding keyed by item id (the
// collection id), for the worker's text-hash + model dedup pass. A collection
// with no embedding yields an empty map, so the worker embeds it.
func (s *Store) ListVectors(ctx context.Context, id string) (map[string]indexjobs.Vector, error) {
	const q = `SELECT embedding, embedding_model, embedding_text_hash FROM portal_collections WHERE id = $1 AND embedding IS NOT NULL`
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
		return nil, fmt.Errorf("collectionindex: list vectors: %w", err)
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

// UpsertVectors writes the embedding back onto the collection. updated_at is
// deliberately left untouched: a background embed is not a user-visible edit.
func (s *Store) UpsertVectors(ctx context.Context, id string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	const q = `UPDATE portal_collections SET embedding = $2, embedding_model = $3, embedding_text_hash = $4 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q, id, pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
		return fmt.Errorf("collectionindex: upsert vectors: %w", err)
	}
	return nil
}

// FindGaps returns the ids of non-deleted collections whose embedding is missing
// or was produced by a model other than the current provider's.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `SELECT id FROM portal_collections
		WHERE deleted_at IS NULL AND (embedding IS NULL OR embedding_model IS DISTINCT FROM $1)`
	rows, err := s.db.QueryContext(ctx, q, currentModel)
	if err != nil {
		return nil, fmt.Errorf("collectionindex: find gaps: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("collectionindex: find gaps scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collectionindex: find gaps rows: %w", err)
	}
	return ids, nil
}

// Coverage returns the number of non-deleted collections with an embedding
// (indexed) and the total number of non-deleted collections (expected).
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	const q = `SELECT COUNT(*) FILTER (WHERE embedding IS NOT NULL) AS indexed, COUNT(*) AS expected
		FROM portal_collections WHERE deleted_at IS NULL`
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("collectionindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

package scriptindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Store reads and writes script embedding state on the scripts table for the
// indexjobs scripts consumer. It is intentionally separate from the
// request-path script store: it touches only the embedding columns (embedding,
// embedding_model, embedding_text_hash) and is scoped to the backfill path, so
// it does not widen the store contract manage_script and the portal are built
// on. The request-path store clears these columns when a script's indexed text
// changes; this Store writes them back.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errNotIndexable is returned by GetIndexText when the script is missing or no
// longer enabled, so the Source treats the unit as nothing to index.
var errNotIndexable = errors.New("scriptindex: script missing or disabled")

// GetIndexText returns the composed embed text for an enabled script. Every
// enabled script is embedded regardless of status: search visibility is decided
// at query time (the store's own scope predicate and discoverable-status
// filter), so the index must cover what any caller can rank. A script disabled
// or deleted between enqueue and claim yields errNotIndexable so the Source
// returns an empty item set (a clean "nothing to index" completion).
//
// The composition is script.IndexText, the same one the discovery source shows
// a caller, and it reads only the description card: no source_code column is
// selected here, which is the storage-level expression of that rule.
func (s *Store) GetIndexText(ctx context.Context, id string) (string, error) {
	// Every column script.IndexText reads is selected here, and no other. A
	// field composed into the text but missing from this projection would leave
	// the worker hashing a DIFFERENT document from the one updateTx hashed on
	// the write, so the row's stored hash would never match and the script would
	// be re-embedded on every sweep, forever.
	const q = `
		SELECT display_name, name, description, category, tags, params,
		       status, superseded_by
		  FROM scripts
		 WHERE id = $1 AND enabled = true`
	var (
		sc         script.Script
		paramsJSON []byte
	)
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&sc.DisplayName, &sc.Name, &sc.Description, &sc.Category, pq.Array(&sc.Tags),
		&paramsJSON, &sc.Status, &sc.SupersededBy)
	// The WHERE clause admitted only an enabled row, and the execution note the
	// text ends with reads the field.
	sc.Enabled = true
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotIndexable
	}
	if err != nil {
		return "", fmt.Errorf("scriptindex: get index text: %w", err)
	}
	if err := json.Unmarshal(paramsJSON, &sc.Params); err != nil {
		return "", fmt.Errorf("scriptindex: unmarshal script params: %w", err)
	}
	return script.IndexText(&sc), nil
}

// ListVectors returns the script's persisted embedding keyed by item id (the
// script id), for the worker's text-hash + model dedup pass. A script with no
// embedding yields an empty map, so the worker embeds it.
func (s *Store) ListVectors(ctx context.Context, id string) (map[string]indexjobs.Vector, error) {
	const q = `
		SELECT embedding, embedding_model, embedding_text_hash
		  FROM scripts
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
		return nil, fmt.Errorf("scriptindex: list vectors: %w", err)
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

// UpsertVectors writes the embedding back onto the script. The script unit holds
// exactly one item (the script itself); a missing or empty row set is a no-op.
// updated_at is deliberately left untouched: a background embed is not a
// user-visible edit, so the script's "last modified" timestamp must not move —
// which matters more here than elsewhere, because the portal listing orders on
// it and a re-embed would otherwise reshuffle a person's scripts.
func (s *Store) UpsertVectors(ctx context.Context, id string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	const q = `
		UPDATE scripts
		   SET embedding           = $2,
		       embedding_model     = $3,
		       embedding_text_hash = $4
		 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q,
		id, pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
		return fmt.Errorf("scriptindex: upsert vectors: %w", err)
	}
	return nil
}

// FindGaps returns the ids of enabled scripts whose embedding is missing or was
// produced by a model other than the current provider's. Missing embeddings
// cover a freshly created script and an edit that moved the indexed text (the
// request-path write clears the vector), and the model mismatch covers a
// provider model swap. Both converge off the request path when the reconciler
// enqueues them.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `
		SELECT id
		  FROM scripts
		 WHERE enabled = true
		   AND (embedding IS NULL OR embedding_model IS DISTINCT FROM $1)`
	rows, err := s.db.QueryContext(ctx, q, currentModel)
	if err != nil {
		return nil, fmt.Errorf("scriptindex: find gaps: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scriptindex: find gaps scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scriptindex: find gaps rows: %w", err)
	}
	return ids, nil
}

// Coverage returns the number of enabled scripts with an embedding (indexed)
// and the total number of enabled scripts (expected). Every enabled script is
// expected to carry a vector once converged.
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	const q = `
		SELECT
			COUNT(*) FILTER (WHERE embedding IS NOT NULL) AS indexed,
			COUNT(*)                                      AS expected
		  FROM scripts
		 WHERE enabled = true`
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("scriptindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

package toolsindex

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Store persists tool embedding vectors (tool_embeddings) and the
// expected-count breadcrumb (index_sources) and answers the query-time
// cosine ranking. Backed by PostgreSQL + pgvector.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ListVectors returns every persisted vector for the source, keyed by
// tool name, for the worker's text-hash dedup pass.
func (s *Store) ListVectors(ctx context.Context, sourceID string) (map[string]indexjobs.Vector, error) {
	const q = `
		SELECT tool_name, text_hash, embedding, model, dim
		  FROM tool_embeddings
		 WHERE source_id = $1
	`
	rows, err := s.db.QueryContext(ctx, q, sourceID)
	if err != nil {
		return nil, fmt.Errorf("toolsindex: list vectors: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	out := make(map[string]indexjobs.Vector)
	for rows.Next() {
		var (
			v   indexjobs.Vector
			vec pgvector.Vector
		)
		if err := rows.Scan(&v.ItemID, &v.TextHash, &vec, &v.Model, &v.Dim); err != nil {
			return nil, fmt.Errorf("toolsindex: list vectors scan: %w", err)
		}
		v.Embedding = vec.Slice()
		out[v.ItemID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("toolsindex: list vectors rows: %w", err)
	}
	return out, nil
}

// Replace atomically swaps the full vector set for the source: it
// deletes every existing row for source_id and inserts the supplied
// set in one transaction, so a tool removed from the registry has its
// stale vector dropped.
func (s *Store) Replace(ctx context.Context, sourceID string, rows []indexjobs.Vector) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("toolsindex: replace begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM tool_embeddings WHERE source_id = $1`, sourceID); err != nil {
		return fmt.Errorf("toolsindex: replace delete: %w", err)
	}
	if err := insertVectors(ctx, tx, sourceID, rows); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("toolsindex: replace commit: %w", err)
	}
	return nil
}

// UpsertBatch inserts or updates the supplied rows in place without
// deleting rows outside the batch (incremental progress for the
// worker's per-chunk persistence).
func (s *Store) UpsertBatch(ctx context.Context, sourceID string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("toolsindex: upsert batch begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertVectors(ctx, tx, sourceID, rows); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("toolsindex: upsert batch commit: %w", err)
	}
	return nil
}

// insertVectors writes rows via an idempotent upsert inside tx. Shared
// by Replace (after its delete) and UpsertBatch.
func insertVectors(ctx context.Context, tx *sql.Tx, sourceID string, rows []indexjobs.Vector) error {
	const q = `
		INSERT INTO tool_embeddings
		    (source_id, tool_name, text_hash, embedding, model, dim, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (source_id, tool_name) DO UPDATE
		   SET text_hash  = EXCLUDED.text_hash,
		       embedding  = EXCLUDED.embedding,
		       model      = EXCLUDED.model,
		       dim        = EXCLUDED.dim,
		       updated_at = NOW()
	`
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, q,
			sourceID, r.ItemID, r.TextHash,
			pgvector.NewVector(r.Embedding), r.Model, r.Dim); err != nil {
			return fmt.Errorf("toolsindex: insert vector %s: %w", r.ItemID, err)
		}
	}
	return nil
}

// Coverage returns the number of indexed tool vectors across every
// source (one source today, "platform"). The tools kind stamps no
// expected count — it re-syncs the live registry every reconcile sweep
// (see FindGaps) — so only the indexed total is meaningful; the admin
// dashboard pairs it with the latest job status to show a sync
// indicator rather than an indexed/expected ratio.
func (s *Store) Coverage(ctx context.Context) (int, error) {
	var indexed int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_embeddings`).Scan(&indexed); err != nil {
		return 0, fmt.Errorf("toolsindex: coverage: %w", err)
	}
	return indexed, nil
}

// RankBySimilarity returns every indexed tool for the source ordered by
// cosine similarity to queryVec (most similar first). pgvector's `<=>`
// is the cosine-distance operator, so 1 - distance is the similarity.
// No LIMIT is applied: the corpus is small (low hundreds) and the
// caller filters by persona before capping, which must happen on the
// full ranked set to avoid a denied tool consuming a top-K slot.
func (s *Store) RankBySimilarity(ctx context.Context, sourceID string, queryVec []float32) ([]ScoredTool, error) {
	const q = `
		SELECT tool_name, 1 - (embedding <=> $1) AS score
		  FROM tool_embeddings
		 WHERE source_id = $2
		 ORDER BY embedding <=> $1
	`
	rows, err := s.db.QueryContext(ctx, q, pgvector.NewVector(queryVec), sourceID)
	if err != nil {
		return nil, fmt.Errorf("toolsindex: rank: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []ScoredTool
	for rows.Next() {
		var t ScoredTool
		if err := rows.Scan(&t.ToolName, &t.Score); err != nil {
			return nil, fmt.Errorf("toolsindex: rank scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("toolsindex: rank rows: %w", err)
	}
	return out, nil
}

package datasetindex

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Store owns the platform's copy of the catalog's dataset text
// (catalog_datasets) and the sweep marker (catalog_dataset_sync). It serves
// both sides of the consumer: the write path the indexjobs worker drives
// (Sync/StampSync, the vector reads and writes, gap detection) and the
// request-path ranking the search federation reads.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Sync materializes the enumerated catalog into catalog_datasets: every entry
// is inserted or updated in one transaction. Rows for datasets the enumeration
// did NOT return are left alone here and dropped by ReplaceVectors, which is
// the Sink's atomic-replace contract — keeping one delete path means a failed
// embed pass cannot leave the mirror pruned against a corpus that was never
// re-indexed.
//
// The embedding columns are deliberately untouched: the framework's text-hash
// dedup compares the stored hash against the freshly composed text in the same
// job, so a changed description is re-embedded without this write having to
// invalidate anything, and an unchanged one keeps a usable vector throughout.
func (s *Store) Sync(ctx context.Context, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("datasetindex: sync begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		INSERT INTO catalog_datasets (urn, name, description, tags, domain, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (urn) DO UPDATE
		   SET name        = EXCLUDED.name,
		       description = EXCLUDED.description,
		       tags        = EXCLUDED.tags,
		       domain      = EXCLUDED.domain,
		       updated_at  = NOW()
	`
	for _, e := range entries {
		tags := e.Tags
		if tags == nil {
			// pq.Array(nil) binds SQL NULL, which the NOT NULL column rejects.
			tags = []string{}
		}
		if _, err := tx.ExecContext(ctx, q, e.URN, e.Name, e.Description, pq.Array(tags), e.Domain); err != nil {
			return fmt.Errorf("datasetindex: sync upsert %s: %w", e.URN, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("datasetindex: sync commit: %w", err)
	}
	return nil
}

// StampSync records that the catalog was enumerated now. The single row is the
// schedule the gap sweep reads: without it an empty catalog (a legitimate
// state) would look permanently un-synced and re-enumerate on every reconciler
// tick.
func (s *Store) StampSync(ctx context.Context) error {
	const q = `
		INSERT INTO catalog_dataset_sync (id, synced_at) VALUES (TRUE, NOW())
		ON CONFLICT (id) DO UPDATE SET synced_at = NOW()
	`
	if _, err := s.db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("datasetindex: stamp sync: %w", err)
	}
	return nil
}

// ListVectors returns every mirrored dataset's persisted vector keyed by URN
// (the item id), for the worker's text-hash + model dedup pass. Rows with no
// embedding yet are omitted so the worker embeds them.
func (s *Store) ListVectors(ctx context.Context) (map[string]indexjobs.Vector, error) {
	const q = `
		SELECT urn, embedding_text_hash, embedding, embedding_model
		  FROM catalog_datasets
		 WHERE embedding IS NOT NULL
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("datasetindex: list vectors: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable

	out := make(map[string]indexjobs.Vector)
	for rows.Next() {
		var (
			v   indexjobs.Vector
			vec pgvector.Vector
		)
		if err := rows.Scan(&v.ItemID, &v.TextHash, &vec, &v.Model); err != nil {
			return nil, fmt.Errorf("datasetindex: list vectors scan: %w", err)
		}
		v.Embedding = vec.Slice()
		v.Dim = len(v.Embedding)
		out[v.ItemID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("datasetindex: list vectors rows: %w", err)
	}
	return out, nil
}

// UpsertVectors writes the embedding columns for the supplied rows onto their
// mirrored dataset. A row whose dataset vanished between the sync and the write
// updates nothing rather than resurrecting it (the UPDATE matches no row), so a
// concurrent prune is not undone.
func (s *Store) UpsertVectors(ctx context.Context, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("datasetindex: upsert vectors begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateVectors(ctx, tx, rows); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("datasetindex: upsert vectors commit: %w", err)
	}
	return nil
}

// ReplaceVectors is the Sink's atomic replace: it deletes every mirrored
// dataset outside the supplied set and writes the set's vectors, in one
// transaction. Deleting the ROW (not just its vector) is what prunes a dataset
// the catalog no longer returns — the row is the index entry, so a dropped
// dataset must not linger as a lexically-matchable hit whose fetch 404s.
//
// An empty set clears the mirror. That is the correct reading of "the catalog
// returned nothing": the Source fails the job on any enumeration error rather
// than reporting a partial corpus, so an empty set means an empty catalog.
func (s *Store) ReplaceVectors(ctx context.Context, rows []indexjobs.Vector) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("datasetindex: replace begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	urns := make([]string, 0, len(rows))
	for _, r := range rows {
		urns = append(urns, r.ItemID)
	}
	const del = `DELETE FROM catalog_datasets WHERE NOT (urn = ANY($1))`
	if len(urns) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_datasets`); err != nil {
			return fmt.Errorf("datasetindex: replace clear: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, del, pq.Array(urns)); err != nil {
		return fmt.Errorf("datasetindex: replace prune: %w", err)
	}
	if err := updateVectors(ctx, tx, rows); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("datasetindex: replace commit: %w", err)
	}
	return nil
}

// updateVectors writes each row's embedding columns inside tx. Shared by
// UpsertVectors (per-chunk progress) and ReplaceVectors (the final set).
func updateVectors(ctx context.Context, tx *sql.Tx, rows []indexjobs.Vector) error {
	const q = `
		UPDATE catalog_datasets
		   SET embedding = $2, embedding_model = $3, embedding_text_hash = $4
		 WHERE urn = $1
	`
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, q, r.ItemID,
			pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
			return fmt.Errorf("datasetindex: update vector %s: %w", r.ItemID, err)
		}
	}
	return nil
}

// NeedsSweep reports whether the corpus owes work: either the enumeration is
// older than interval (or has never run), or some mirrored dataset carries no
// vector or one produced by a model other than currentModel. It is the whole of
// gap detection for this kind — the corpus lives in DataHub, so "what is
// missing" cannot be answered by diffing two local tables, and the freshness
// half is what turns the reconciler's tick into the sweep schedule.
func (s *Store) NeedsSweep(ctx context.Context, currentModel string, interval time.Duration) (bool, error) {
	const q = `
		SELECT
			NOT EXISTS (SELECT 1 FROM catalog_dataset_sync
			             WHERE synced_at > NOW() - make_interval(secs => $1))
			OR EXISTS (SELECT 1 FROM catalog_datasets
			            WHERE embedding IS NULL OR embedding_model IS DISTINCT FROM $2)
	`
	var needs bool
	if err := s.db.QueryRowContext(ctx, q, interval.Seconds(), currentModel).Scan(&needs); err != nil {
		return false, fmt.Errorf("datasetindex: needs sweep: %w", err)
	}
	return needs, nil
}

// Coverage returns the mirrored datasets carrying a vector and the total
// mirrored, the two halves of the admin Indexing dashboard's ratio.
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	const q = `
		SELECT COUNT(*) FILTER (WHERE embedding IS NOT NULL), COUNT(*)
		  FROM catalog_datasets
	`
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("datasetindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

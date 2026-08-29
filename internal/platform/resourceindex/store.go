package resourceindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Store reads and writes resource index state on the resources table for the
// indexjobs resources consumer. It is intentionally separate from
// resource.Store: it touches only the index columns (content_text, embedding,
// embedding_model, embedding_text_hash) plus the metadata the indexed text is
// composed from, and is scoped to the backfill path, so it does not widen the
// request-path store contract. The request-path Update clears the embedding
// columns when a resource's indexed metadata changes; this Store writes them
// back.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errGone is returned by Load when the resource row no longer exists, so the
// Source can report the unit as gone rather than as unreadable.
var errGone = errors.New("resourceindex: resource row is gone")

// Row is the resource state the Source needs to compose the indexed text:
// Resource carries the indexed metadata fields plus the location and size of the
// blob whose text is extracted (only the columns this consumer selects are
// populated), and ContentText is the text a previous pass extracted (kept when a
// blob read fails transiently).
type Row struct {
	Resource    resource.Resource
	ContentText string
	// ContentSettled reports whether a previous pass already settled the content
	// question for this row (content_indexed_at is set). It is what lets the
	// Source skip a redundant write while still guaranteeing that a row which has
	// never settled gets stamped, including when the extracted text is empty.
	ContentSettled bool
}

// Load returns the indexable state of one resource. A resource deleted between
// enqueue and claim yields errGone.
func (s *Store) Load(ctx context.Context, id string) (Row, error) {
	const q = `SELECT display_name, description, path, filename, tags, mime_type, size_bytes, s3_key,
		content_text, content_indexed_at IS NOT NULL
		FROM resources WHERE id = $1`
	var r Row
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&r.Resource.DisplayName, &r.Resource.Description, &r.Resource.Path, &r.Resource.Filename,
		pq.Array(&r.Resource.Tags), &r.Resource.MIMEType, &r.Resource.SizeBytes, &r.Resource.S3Key,
		&r.ContentText, &r.ContentSettled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, errGone
	}
	if err != nil {
		return Row{}, fmt.Errorf("resourceindex: load resource: %w", err)
	}
	return r, nil
}

// SetContentText writes the text extracted from a resource's blob onto the row
// so the lexical index covers it, and stamps content_indexed_at to record that
// the content question is settled for this row. The two are written together
// because they must not diverge: content_indexed_at is what takes the row OUT of
// the gap query, so stamping it without the text (or leaving it NULL after
// writing the text) either strands the content unindexed or re-reads the blob
// forever.
//
// updated_at is deliberately left untouched: a background extraction is not a
// user-visible edit, so the resource's "last modified" timestamp must not move.
// A row deleted concurrently updates nothing, which is not an error.
func (s *Store) SetContentText(ctx context.Context, id, text string) error {
	const q = `UPDATE resources SET content_text = $2, content_indexed_at = NOW() WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q, id, text); err != nil {
		return fmt.Errorf("resourceindex: set content text: %w", err)
	}
	return nil
}

// ListVectors returns the resource's persisted embedding keyed by item id (the
// resource id), for the worker's text-hash + model dedup pass. A resource with
// no embedding yields an empty map, so the worker embeds it.
func (s *Store) ListVectors(ctx context.Context, id string) (map[string]indexjobs.Vector, error) {
	const q = `SELECT embedding, embedding_model, embedding_text_hash FROM resources
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
		return nil, fmt.Errorf("resourceindex: list vectors: %w", err)
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

// UpsertVectors writes the embedding back onto the resource. The resource unit
// holds exactly one item; a missing or empty row set is a no-op. updated_at is
// deliberately left untouched (see SetContentText).
func (s *Store) UpsertVectors(ctx context.Context, id string, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	r := rows[0]
	const q = `UPDATE resources SET embedding = $2, embedding_model = $3, embedding_text_hash = $4 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q, id, pgvector.NewVector(r.Embedding), r.Model, r.TextHash); err != nil {
		return fmt.Errorf("resourceindex: upsert vectors: %w", err)
	}
	return nil
}

// FindGaps returns the ids of resources whose index state is incomplete: the
// embedding is missing, it was produced by a model other than the current
// provider's, or the content pass has never settled (content_indexed_at IS
// NULL). Missing embeddings cover a freshly uploaded resource and a metadata
// edit (the request-path Update clears the embedding); the model mismatch covers
// a provider model swap.
//
// The content clause is what makes a failed extraction recoverable. A blob read
// that fails transiently still yields a valid metadata-only embedding, so
// without it the job would succeed, the row would stop being a gap, and the
// file's contents would never be indexed — while Coverage reported the resource
// fully indexed. The consumer stamps content_indexed_at only when it has either
// extracted the text or established there is nothing to extract, so a row whose
// content is still owed keeps coming back until it settles.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `SELECT id FROM resources
		WHERE embedding IS NULL OR embedding_model IS DISTINCT FROM $1 OR content_indexed_at IS NULL`
	rows, err := s.db.QueryContext(ctx, q, currentModel)
	if err != nil {
		return nil, fmt.Errorf("resourceindex: find gaps: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("resourceindex: find gaps scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resourceindex: find gaps rows: %w", err)
	}
	return ids, nil
}

// Coverage returns the number of fully indexed resources (a vector AND a settled
// content pass) and the total number of resources. It counts the same condition
// FindGaps excludes, so a resource whose content extraction is still owed reports
// as a gap rather than as covered — the failure mode this consumer is most likely
// to hit is a blob read that fails while the metadata embed succeeds, and
// coverage must not report that as done.
func (s *Store) Coverage(ctx context.Context) (indexed, expected int, err error) {
	const q = `SELECT COUNT(*) FILTER (WHERE embedding IS NOT NULL AND content_indexed_at IS NOT NULL) AS indexed,
		COUNT(*) AS expected FROM resources`
	if err := s.db.QueryRowContext(ctx, q).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("resourceindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

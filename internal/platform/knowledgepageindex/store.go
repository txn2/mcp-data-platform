package knowledgepageindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Store reads and writes knowledge-page embedding state for the indexjobs
// knowledge-pages consumer: the page's chunk vectors in
// portal_knowledge_page_embedding_chunks, plus the set-level index marker
// (embedding_model) on portal_knowledge_pages. It is intentionally separate from
// portal.KnowledgePageStore: it touches only indexing state and is scoped to the
// backfill path, so it does not widen the request-path store contract. The
// request-path Create/Update clears that state when a page's indexed text
// changes; this Store writes it back.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errNotIndexable is returned by GetContent when the page is missing or
// soft-deleted, so the Source treats the unit as nothing to index.
var errNotIndexable = errors.New("knowledgepageindex: page missing or deleted")

// indexablePage is the predicate for a page the consumer owes a vector set: live,
// and carrying at least one field the embed text is composed from. A page with no
// indexable text at all can never produce a chunk, so counting it as owed would
// make the reconciler re-enqueue it on every sweep forever. Shared by FindGaps and
// Coverage so the gap query and the coverage report cannot disagree about what
// "expected" means.
const indexablePage = `deleted_at IS NULL AND (title <> '' OR body <> '' OR tags <> '[]'::jsonb)`

// itemID names one chunk of a page within the indexjobs framework's flat item
// namespace: "<page id>:<chunk index>". The framework never parses it; only this
// package's Source and Sink interpret it, and chunkIndex is the inverse.
func itemID(pageID string, index int) string {
	return pageID + ":" + strconv.Itoa(index)
}

// chunkIndex recovers the chunk ordinal from an item id produced by itemID. A
// malformed id (a vector row written by an older revision, or an id from another
// kind) is rejected rather than silently mapped to chunk 0, which would collapse a
// page's chunk set onto one row.
func chunkIndex(pageID, id string) (int, error) {
	suffix, ok := strings.CutPrefix(id, pageID+":")
	if !ok {
		return 0, fmt.Errorf("knowledgepageindex: item id %q is not a chunk of page %q", id, pageID)
	}
	n, err := strconv.Atoi(suffix)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("knowledgepageindex: item id %q has no chunk index", id)
	}
	return n, nil
}

// Content is the indexed text of one page: exactly the fields
// knowledgepage.IndexChunks composes an embed text from.
type Content struct {
	Title string
	Body  string
	Tags  []string
}

// GetContent returns the indexed fields of a non-deleted page. A page
// soft-deleted between enqueue and claim yields errNotIndexable so the Source
// returns an empty item set. The Source composes the embed text from these
// fields with knowledgepage.IndexChunks, the same composition the request-path
// search ranks against.
func (s *Store) GetContent(ctx context.Context, id string) (Content, error) {
	const q = `SELECT title, body, tags FROM portal_knowledge_pages WHERE id = $1 AND deleted_at IS NULL`
	var (
		c        Content
		tagsJSON []byte
	)
	err := s.db.QueryRowContext(ctx, q, id).Scan(&c.Title, &c.Body, &tagsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Content{}, errNotIndexable
	}
	if err != nil {
		return Content{}, fmt.Errorf("knowledgepageindex: get content: %w", err)
	}
	if len(tagsJSON) > 0 {
		if err := json.Unmarshal(tagsJSON, &c.Tags); err != nil {
			return Content{}, fmt.Errorf("knowledgepageindex: unmarshal tags: %w", err)
		}
	}
	return c, nil
}

// ListVectors returns the page's persisted chunk vectors keyed by item id, for
// the worker's text-hash + model dedup pass. A page with no chunks yields an
// empty map, so the worker embeds every chunk. Per-chunk hashes are what make an
// edit to one section re-embed only the chunks whose text actually moved.
func (s *Store) ListVectors(ctx context.Context, pageID string) (map[string]indexjobs.Vector, error) {
	const q = `SELECT chunk_index, text_hash, embedding, model
		FROM portal_knowledge_page_embedding_chunks WHERE page_id = $1`
	rows, err := s.db.QueryContext(ctx, q, pageID)
	if err != nil {
		return nil, fmt.Errorf("knowledgepageindex: list vectors: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable

	out := map[string]indexjobs.Vector{}
	for rows.Next() {
		var (
			index int
			hash  []byte
			vec   pgvector.Vector
			model string
		)
		if err := rows.Scan(&index, &hash, &vec, &model); err != nil {
			return nil, fmt.Errorf("knowledgepageindex: list vectors scan: %w", err)
		}
		emb := vec.Slice()
		id := itemID(pageID, index)
		out[id] = indexjobs.Vector{ItemID: id, TextHash: hash, Embedding: emb, Model: model, Dim: len(emb)}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("knowledgepageindex: list vectors rows: %w", err)
	}
	return out, nil
}

// ReplaceVectors writes the page's chunk set atomically: every supplied row is
// upserted and any chunk outside the set is deleted, so a page that shrinks (an
// edit that removes a section) does not leave an orphan vector ranking against
// text the page no longer has. An empty row set deletes every chunk, which is how
// the worker clears a unit whose source is gone.
//
// The page's own updated_at is deliberately untouched here and in StampModel: a
// background embed is not a user-visible edit, so the page's "last modified"
// timestamp must not move.
func (s *Store) ReplaceVectors(ctx context.Context, pageID string, rows []indexjobs.Vector) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("knowledgepageindex: begin replace: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	keep := make([]int, 0, len(rows))
	for _, r := range rows {
		index, err := chunkIndex(pageID, r.ItemID)
		if err != nil {
			return err
		}
		if err := upsertChunk(ctx, tx, pageID, index, r); err != nil {
			return err
		}
		keep = append(keep, index)
	}
	if err := deleteChunksExcept(ctx, tx, pageID, keep); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("knowledgepageindex: commit replace: %w", err)
	}
	return nil
}

// UpsertVectors writes one batch of chunk vectors in place, leaving every chunk
// outside the batch alone, so a job that fails mid-pass leaves its completed
// chunks visible to the next attempt's dedup read.
func (s *Store) UpsertVectors(ctx context.Context, pageID string, rows []indexjobs.Vector) error {
	for _, r := range rows {
		index, err := chunkIndex(pageID, r.ItemID)
		if err != nil {
			return err
		}
		if err := upsertChunk(ctx, s.db, pageID, index, r); err != nil {
			return err
		}
	}
	return nil
}

// execer is the write surface upsertChunk needs, satisfied by both *sql.DB (the
// per-batch path) and *sql.Tx (the atomic replace).
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// upsertChunk writes one chunk row, replacing any vector previously stored at the
// same ordinal.
func upsertChunk(ctx context.Context, db execer, pageID string, index int, r indexjobs.Vector) error {
	const q = `INSERT INTO portal_knowledge_page_embedding_chunks
		(page_id, chunk_index, text_hash, embedding, model, dim, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (page_id, chunk_index) DO UPDATE SET
			text_hash = EXCLUDED.text_hash, embedding = EXCLUDED.embedding,
			model = EXCLUDED.model, dim = EXCLUDED.dim, updated_at = NOW()`
	if _, err := db.ExecContext(ctx, q, pageID, index, r.TextHash,
		pgvector.NewVector(r.Embedding), r.Model, len(r.Embedding)); err != nil {
		return fmt.Errorf("knowledgepageindex: upsert chunk: %w", err)
	}
	return nil
}

// deleteChunksExcept removes the page's chunks whose ordinal is not in keep. An
// empty keep deletes them all.
func deleteChunksExcept(ctx context.Context, tx *sql.Tx, pageID string, keep []int) error {
	const deleteAll = `DELETE FROM portal_knowledge_page_embedding_chunks WHERE page_id = $1`
	if len(keep) == 0 {
		if _, err := tx.ExecContext(ctx, deleteAll, pageID); err != nil {
			return fmt.Errorf("knowledgepageindex: delete chunks: %w", err)
		}
		return nil
	}
	// nosemgrep: semgrep.unbounded-make-slice-capacity -- capacity is the caller's own row count, not attacker-controlled input
	args := make([]any, 0, len(keep)+1)
	args = append(args, pageID)
	placeholders := make([]string, 0, len(keep))
	for i, index := range keep {
		placeholders = append(placeholders, "$"+strconv.Itoa(i+2))
		args = append(args, index)
	}
	// #nosec G201 -- the interpolated text is generated placeholders ($2, $3, ...);
	// every value is bound through args.
	q := fmt.Sprintf(`DELETE FROM portal_knowledge_page_embedding_chunks
		WHERE page_id = $1 AND chunk_index NOT IN (%s)`, strings.Join(placeholders, ", "))
	if _, err := tx.ExecContext(ctx, q, args...); err != nil { // #nosec G701 -- placeholders only
		return fmt.Errorf("knowledgepageindex: prune chunks: %w", err)
	}
	return nil
}

// StampModel records that the page's chunk set was produced by the given model.
// It is the set-level convergence marker the gap query reads: one row per page,
// so detecting "this page still owes work" does not have to count chunk rows or
// know how many chunks the page's current text would produce. A page with no
// indexable text converges here with zero chunks, which is what keeps it out of
// the reconciler's sweep instead of being re-enqueued forever.
func (s *Store) StampModel(ctx context.Context, pageID, model string) error {
	const q = `UPDATE portal_knowledge_pages SET embedding_model = $2 WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, q, pageID, model); err != nil {
		return fmt.Errorf("knowledgepageindex: stamp model: %w", err)
	}
	return nil
}

// FindGaps returns the ids of indexable pages whose chunk set is missing or was
// produced by a model other than the current provider's.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `SELECT id FROM portal_knowledge_pages
		WHERE ` + indexablePage + ` AND embedding_model IS DISTINCT FROM $1`
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

// Coverage returns the number of indexable pages whose chunk set is current
// (indexed) and the total number of indexable pages (expected), against the
// supplied provider model. Both halves apply the same indexable predicate as
// FindGaps, so a fully converged corpus reports 100% rather than a permanent
// shortfall from pages that can never carry a vector.
func (s *Store) Coverage(ctx context.Context, currentModel string) (indexed, expected int, err error) {
	const q = `SELECT COUNT(*) FILTER (WHERE embedding_model IS NOT DISTINCT FROM $1) AS indexed,
		COUNT(*) AS expected FROM portal_knowledge_pages WHERE ` + indexablePage
	if err := s.db.QueryRowContext(ctx, q, currentModel).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("knowledgepageindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

package scriptindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Store reads scripts and writes their chunk vectors for the indexjobs
// scripts consumer. It is separate from the request-path script store: it
// touches only script_embedding_chunks and the index_embedded_hash and
// index_model markers, and is scoped to the backfill path. The request-path
// store records index_text_hash on every save; when it moves, this Store
// rebuilds the script's chunks, reusing the ones whose text did not.
type Store struct {
	db *sql.DB
	// loaded is the hash of the text each script's chunks were cut from, by
	// script id, from LoadItems to Stamp in the same pass.
	loaded sync.Map
}

// NewStore returns a Store over the given database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// errNotIndexable is returned by GetIndexed when the script is missing or no
// longer enabled, so the Source treats the unit as nothing to index.
var errNotIndexable = errors.New("scriptindex: script missing or disabled")

// itemID is the item id of one chunk of a script.
func itemID(scriptID string, index int) string {
	return scriptID + ":" + strconv.Itoa(index)
}

// chunkIndex recovers the chunk ordinal from an item id produced by itemID. An
// id that is not a chunk of this script is refused rather than mapped to chunk
// 0, which would collapse the script's chunk set onto one row.
func chunkIndex(scriptID, id string) (int, error) {
	suffix, ok := strings.CutPrefix(id, scriptID+":")
	if !ok {
		return 0, fmt.Errorf("scriptindex: item id %q is not a chunk of script %q", id, scriptID)
	}
	n, err := strconv.Atoi(suffix)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("scriptindex: item id %q has no chunk index", id)
	}
	return n, nil
}

// GetIndexed returns the fields an enabled script is indexed on: everything
// script.IndexCorpus reads, and no other. A field composed into the text but
// missing here would leave the worker hashing a different document from the
// one the save hashed, so the script would be found owing an embedding on
// every sweep. A script disabled or deleted since it was enqueued yields
// errNotIndexable.
func (s *Store) GetIndexed(ctx context.Context, id string) (*script.Script, error) {
	const q = `
		SELECT display_name, name, description, category, tags, params,
		       status, superseded_by, library, source_code
		  FROM scripts
		 WHERE id = $1 AND enabled = true`
	var (
		sc         script.Script
		paramsJSON []byte
	)
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&sc.DisplayName, &sc.Name, &sc.Description, &sc.Category, pq.Array(&sc.Tags),
		&paramsJSON, &sc.Status, &sc.SupersededBy, &sc.Library, &sc.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotIndexable
	}
	if err != nil {
		return nil, fmt.Errorf("scriptindex: get indexed script: %w", err)
	}
	// The WHERE clause admitted only an enabled row, and the execution note the
	// card ends with reads the field.
	sc.Enabled = true
	if err := json.Unmarshal(paramsJSON, &sc.Params); err != nil {
		return nil, fmt.Errorf("scriptindex: unmarshal script params: %w", err)
	}
	return &sc, nil
}

// ListVectors returns the script's chunk vectors keyed by item id, for the
// worker's text-hash and model dedup pass. A script with no chunks yields an
// empty map, so every chunk is embedded.
func (s *Store) ListVectors(ctx context.Context, scriptID string) (map[string]indexjobs.Vector, error) {
	const q = `SELECT chunk_index, text_hash, embedding, model
		FROM script_embedding_chunks WHERE script_id = $1`
	rows, err := s.db.QueryContext(ctx, q, scriptID)
	if err != nil {
		return nil, fmt.Errorf("scriptindex: list vectors: %w", err)
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
			return nil, fmt.Errorf("scriptindex: list vectors scan: %w", err)
		}
		emb := vec.Slice()
		id := itemID(scriptID, index)
		out[id] = indexjobs.Vector{ItemID: id, TextHash: hash, Embedding: emb, Model: model, Dim: len(emb)}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scriptindex: list vectors rows: %w", err)
	}
	return out, nil
}

// ReplaceVectors writes the script's chunk set atomically: every supplied row
// is upserted and every other chunk deleted, so a script whose source shrank
// keeps no vector for code it no longer has. An empty set deletes every chunk,
// which is how a disabled script is cleared. The script's updated_at is left
// alone: a background embed is not an edit, and the portal listing orders on
// it.
func (s *Store) ReplaceVectors(ctx context.Context, scriptID string, rows []indexjobs.Vector) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("scriptindex: begin replace: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	keep := make([]int, 0, len(rows))
	for _, r := range rows {
		index, err := chunkIndex(scriptID, r.ItemID)
		if err != nil {
			return err
		}
		if err := upsertChunk(ctx, tx, scriptID, index, r); err != nil {
			return err
		}
		keep = append(keep, index)
	}
	const prune = `DELETE FROM script_embedding_chunks WHERE script_id = $1 AND NOT (chunk_index = ANY($2))`
	if _, err := tx.ExecContext(ctx, prune, scriptID, pq.Array(keep)); err != nil {
		return fmt.Errorf("scriptindex: prune chunks: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scriptindex: commit replace: %w", err)
	}
	return nil
}

// UpsertVectors writes one batch of chunk vectors in place, leaving every
// chunk outside the batch alone.
func (s *Store) UpsertVectors(ctx context.Context, scriptID string, rows []indexjobs.Vector) error {
	for _, r := range rows {
		index, err := chunkIndex(scriptID, r.ItemID)
		if err != nil {
			return err
		}
		if err := upsertChunk(ctx, s.db, scriptID, index, r); err != nil {
			return err
		}
	}
	return nil
}

// execer is the write surface upsertChunk needs, satisfied by *sql.DB and
// *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// upsertChunk writes one chunk row, replacing any vector at the same ordinal.
func upsertChunk(ctx context.Context, db execer, scriptID string, index int, r indexjobs.Vector) error {
	const q = `INSERT INTO script_embedding_chunks
		(script_id, chunk_index, text_hash, embedding, model, dim, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (script_id, chunk_index) DO UPDATE SET
			text_hash = EXCLUDED.text_hash, embedding = EXCLUDED.embedding,
			model = EXCLUDED.model, dim = EXCLUDED.dim, updated_at = NOW()`
	if _, err := db.ExecContext(ctx, q, scriptID, index, r.TextHash,
		pgvector.NewVector(r.Embedding), r.Model, len(r.Embedding)); err != nil {
		return fmt.Errorf("scriptindex: upsert chunk: %w", err)
	}
	return nil
}

// stampQuery records the model and the text a script's chunk set was built
// from. index_text_hash is set too where no save has written it yet (a script
// saved before 000177), so the two hashes agree once the script is built.
const stampQuery = `UPDATE scripts
	SET index_model = $2, index_embedded_hash = $3, index_text_hash = COALESCE(index_text_hash, $3)
	WHERE id = $1`

// Stamp records that the script's chunk set was built by model from the text
// LoadItems read for it. When LoadItems read nothing (the script was disabled
// or deleted), there is nothing built to record.
func (s *Store) Stamp(ctx context.Context, scriptID, model string) error {
	v, ok := s.loaded.LoadAndDelete(scriptID)
	if !ok {
		return nil
	}
	hash, ok := v.([]byte)
	if !ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, stampQuery, scriptID, model, hash); err != nil {
		return fmt.Errorf("scriptindex: stamp: %w", err)
	}
	return nil
}

// owing is the predicate of an enabled script whose chunk set is missing, was
// built from text it no longer has, or was built by a model other than $1.
// FindGaps and Coverage read the same one, so a converged corpus reports full
// coverage and a script still owed is both a gap and uncovered.
const owing = `(index_embedded_hash IS NULL
	OR index_embedded_hash IS DISTINCT FROM index_text_hash
	OR index_model IS DISTINCT FROM $1)`

// FindGaps returns the ids of enabled scripts owing an embedding. A save that
// lands while a script is being embedded leaves the hashes apart once that
// pass stamps, so the script is found here again.
func (s *Store) FindGaps(ctx context.Context, currentModel string) ([]string, error) {
	const q = `SELECT id FROM scripts WHERE enabled = true AND ` + owing
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

// Coverage returns the enabled scripts whose chunk set is current (indexed)
// and all enabled scripts (expected).
func (s *Store) Coverage(ctx context.Context, currentModel string) (indexed, expected int, err error) {
	const q = `SELECT COUNT(*) FILTER (WHERE NOT ` + owing + `) AS indexed, COUNT(*) AS expected
		FROM scripts WHERE enabled = true`
	if err := s.db.QueryRowContext(ctx, q, currentModel).Scan(&indexed, &expected); err != nil {
		return 0, 0, fmt.Errorf("scriptindex: coverage: %w", err)
	}
	return indexed, expected, nil
}

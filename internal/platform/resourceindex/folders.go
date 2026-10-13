package resourceindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// FolderKind is the source kind a folder's one search entry is indexed under
// (#2111). A folder holding files that are not indexed one by one is found by
// search as one thing: 4,775 archive files cost one embed, not 4,775.
const FolderKind = resource.FolderIndexKind

// folderLibraryWhere selects one folder of one library on resources; the
// global library's id is NULL there and ” in a folder key.
const folderLibraryWhere = `scope = $1 AND COALESCE(scope_id, '') = $2 AND path = $3`

// FolderSummary is what a folder's entry is composed from.
type FolderSummary struct {
	Path        string
	Description string
	Files       int
	MIMETypes   []string
	Oldest      time.Time
	Newest      time.Time
	Samples     []string
}

// Text is the summary the folder is embedded and matched on. It is built so
// that it changes when what the folder holds changes materially, not on every
// file: the count is rounded, the dates are months, and the sample names are
// the oldest files, which a backfill does not replace. A job whose text is
// unchanged costs a hash compare and no embed.
func (f FolderSummary) Text() string {
	lines := []string{"Folder " + f.Path}
	if f.Description != "" {
		lines = append(lines, f.Description)
	}
	lines = append(lines,
		fmt.Sprintf("%s not indexed one by one, of types %s", aboutFiles(f.Files), strings.Join(f.MIMETypes, ", ")),
		fmt.Sprintf("Added %s to %s", f.Oldest.UTC().Format("2006-01"), f.Newest.UTC().Format("2006-01")))
	if len(f.Samples) > 0 {
		lines = append(lines, "For example: "+strings.Join(f.Samples, ", "))
	}
	return strings.Join(lines, "\n")
}

// decimalBase is the base a count is rounded in, and thousandsGroup how many
// digits a comma separates.
const (
	decimalBase    = 10
	thousandsGroup = 3
)

// aboutFiles states a count to one significant figure past ten, so a folder a
// pipeline adds to every few minutes reads the same between steps.
func aboutFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	if n < decimalBase {
		return strconv.Itoa(n) + " files"
	}
	magnitude := math.Pow(decimalBase, math.Floor(math.Log10(float64(n))))
	rounded := int(math.Round(float64(n)/magnitude) * magnitude)
	return "about " + groupThousands(rounded) + " files"
}

func groupThousands(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%thousandsGroup == 0 {
			_ = b.WriteByte(',')
		}
		_, _ = b.WriteRune(r)
	}
	return b.String()
}

// LoadFolder summarizes the files of one folder that are not indexed
// themselves. A folder with none of them yields ok=false.
func (s *Store) LoadFolder(ctx context.Context, lib resource.ScopeFilter, path string) (FolderSummary, bool, error) {
	sum := FolderSummary{Path: path}
	var oldest, newest sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(created_at), MAX(created_at),
		COALESCE(array_agg(DISTINCT lower(split_part(mime_type, ';', 1)) ORDER BY lower(split_part(mime_type, ';', 1))), '{}')
		FROM resources WHERE index_policy = 'none' AND `+folderLibraryWhere,
		string(lib.Scope), lib.ScopeID, path,
	).Scan(&sum.Files, &oldest, &newest, pq.Array(&sum.MIMETypes))
	if err != nil {
		return sum, false, fmt.Errorf("resourceindex: summarize folder: %w", err)
	}
	if sum.Files == 0 {
		return sum, false, nil
	}
	sum.Oldest, sum.Newest = oldest.Time, newest.Time

	rows, err := s.db.QueryContext(ctx, `SELECT filename FROM resources
		WHERE index_policy = 'none' AND `+folderLibraryWhere+`
		ORDER BY created_at, filename LIMIT 3`, // the three oldest names stand for the folder
		string(lib.Scope), lib.ScopeID, path)
	if err != nil {
		return sum, false, fmt.Errorf("resourceindex: sample folder: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return sum, false, fmt.Errorf("resourceindex: sample folder scan: %w", err)
		}
		sum.Samples = append(sum.Samples, name)
	}
	if err := rows.Err(); err != nil {
		return sum, false, fmt.Errorf("resourceindex: sample folder rows: %w", err)
	}

	err = s.db.QueryRowContext(ctx, `SELECT description FROM resource_folders
		WHERE scope = $1 AND COALESCE(scope_id, '') = $2 AND path = $3`,
		string(lib.Scope), lib.ScopeID, path).Scan(&sum.Description)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sum, false, fmt.Errorf("resourceindex: folder description: %w", err)
	}
	return sum, true, nil
}

// SetFolderSummary records the folder's summary text, creating the folder's
// row when no writer recorded it, so the entry has somewhere to live.
func (s *Store) SetFolderSummary(ctx context.Context, lib resource.ScopeFilter, path, text string) error {
	scopeID := sql.NullString{String: lib.ScopeID, Valid: lib.ScopeID != ""}
	_, err := s.db.ExecContext(ctx, `INSERT INTO resource_folders (scope, scope_id, path, summary_text)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (scope, (COALESCE(scope_id, '')), path) DO UPDATE SET summary_text = EXCLUDED.summary_text`,
		string(lib.Scope), scopeID, path, text)
	if err != nil {
		return fmt.Errorf("resourceindex: set folder summary: %w", err)
	}
	return nil
}

// folderVectors reads one folder's stored entry for the worker's dedup.
func (s *Store) folderVectors(ctx context.Context, key string) (map[string]indexjobs.Vector, error) {
	lib, path, err := resource.ParseFolderKey(key)
	if err != nil {
		return nil, fmt.Errorf("resourceindex: %w", err)
	}
	var (
		vec   pgvector.Vector
		model string
		hash  []byte
	)
	err = s.db.QueryRowContext(ctx, `SELECT embedding, embedding_model, embedding_text_hash FROM resource_folders
		WHERE scope = $1 AND COALESCE(scope_id, '') = $2 AND path = $3 AND embedding IS NOT NULL`,
		string(lib.Scope), lib.ScopeID, path).Scan(&vec, &model, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]indexjobs.Vector{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resourceindex: folder vectors: %w", err)
	}
	embedding := vec.Slice()
	return map[string]indexjobs.Vector{key: {
		ItemID: key, TextHash: hash, Embedding: embedding, Model: model, Dim: len(embedding),
	}}, nil
}

// upsertFolderVector writes a folder's entry, or clears it when the folder no
// longer holds a file that is not indexed (an empty row set).
func (s *Store) upsertFolderVector(ctx context.Context, key string, rows []indexjobs.Vector) error {
	lib, path, err := resource.ParseFolderKey(key)
	if err != nil {
		return fmt.Errorf("resourceindex: %w", err)
	}
	if len(rows) == 0 {
		_, err = s.db.ExecContext(ctx, `UPDATE resource_folders
			SET embedding = NULL, embedding_model = '', embedding_text_hash = NULL, summary_text = ''
			WHERE scope = $1 AND COALESCE(scope_id, '') = $2 AND path = $3`,
			string(lib.Scope), lib.ScopeID, path)
	} else {
		r := rows[0]
		_, err = s.db.ExecContext(ctx, `UPDATE resource_folders
			SET embedding = $4, embedding_model = $5, embedding_text_hash = $6
			WHERE scope = $1 AND COALESCE(scope_id, '') = $2 AND path = $3`,
			string(lib.Scope), lib.ScopeID, path, pgvector.NewVector(r.Embedding), r.Model, r.TextHash)
	}
	if err != nil {
		return fmt.Errorf("resourceindex: upsert folder vector: %w", err)
	}
	return nil
}

// owedFoldersQuery is every folder whose entry is behind: one holding files
// that are not indexed with no entry under the current model or with its
// summary emptied by a description written since, and one with an entry that
// no longer holds any.
const owedFoldersQuery = `
	SELECT scope, COALESCE(scope_id, ''), path FROM resources WHERE index_policy = 'none'
	GROUP BY scope, COALESCE(scope_id, ''), path
	EXCEPT
	SELECT scope, COALESCE(scope_id, ''), path FROM resource_folders
	WHERE embedding IS NOT NULL AND embedding_model = $1 AND summary_text <> ''
	UNION
	SELECT f.scope, COALESCE(f.scope_id, ''), f.path FROM resource_folders f
	WHERE f.embedding IS NOT NULL AND NOT EXISTS (
		SELECT 1 FROM resources r WHERE r.index_policy = 'none'
		  AND r.scope = f.scope AND COALESCE(r.scope_id, '') = COALESCE(f.scope_id, '') AND r.path = f.path)`

// findFolderGaps lists the folder keys owed an index job.
func (s *Store) findFolderGaps(ctx context.Context, currentModel string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, owedFoldersQuery, currentModel)
	if err != nil {
		return nil, fmt.Errorf("resourceindex: find folder gaps: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var keys []string
	for rows.Next() {
		var lib resource.ScopeFilter
		var path string
		if err := rows.Scan(&lib.Scope, &lib.ScopeID, &path); err != nil {
			return nil, fmt.Errorf("resourceindex: find folder gaps scan: %w", err)
		}
		keys = append(keys, resource.FolderKey(lib, path))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resourceindex: find folder gaps rows: %w", err)
	}
	return keys, nil
}

// folderCoverageQuery counts the folders owed an entry and those that have one.
const folderCoverageQuery = `
	SELECT COUNT(*) FILTER (WHERE f.embedding IS NOT NULL), COUNT(*)
	FROM (SELECT DISTINCT scope, COALESCE(scope_id, '') AS sid, path FROM resources WHERE index_policy = 'none') o
	LEFT JOIN resource_folders f
	  ON f.scope = o.scope AND COALESCE(f.scope_id, '') = o.sid AND f.path = o.path`

// FolderSource loads one folder's summary as the one item it is indexed on.
type FolderSource struct{ store *Store }

// NewFolderSource returns the folder entry source over store.
func NewFolderSource(store *Store) *FolderSource { return &FolderSource{store: store} }

var _ indexjobs.Source = (*FolderSource)(nil)

// Kind returns the folder entry kind.
func (*FolderSource) Kind() string { return FolderKind }

// LoadItems summarizes the folder a key names. A folder that no longer holds
// a file that is not indexed yields no item, which clears its entry.
func (s *FolderSource) LoadItems(ctx context.Context, key string) ([]indexjobs.Item, error) {
	lib, path, err := resource.ParseFolderKey(key)
	if err != nil {
		return nil, fmt.Errorf("folder %q: %w", key, indexjobs.ErrSourceGone)
	}
	sum, ok, err := s.store.LoadFolder(ctx, lib, path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	text := sum.Text()
	if err := s.store.SetFolderSummary(ctx, lib, path, text); err != nil {
		return nil, err
	}
	return []indexjobs.Item{{ItemID: key, Text: text}}, nil
}

// OnSucceeded is a no-op.
func (*FolderSource) OnSucceeded(string) {}

// FolderSink stores a folder's entry on its resource_folders row.
type FolderSink struct {
	store        *Store
	currentModel string
}

// NewFolderSink returns the folder entry sink over store.
func NewFolderSink(store *Store, currentModel string) *FolderSink {
	return &FolderSink{store: store, currentModel: currentModel}
}

var (
	_ indexjobs.Sink             = (*FolderSink)(nil)
	_ indexjobs.CoverageReporter = (*FolderSink)(nil)
)

// Kind returns the folder entry kind.
func (*FolderSink) Kind() string { return FolderKind }

// ListExisting returns the folder's stored entry for the dedup pass.
func (s *FolderSink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.folderVectors(ctx, key.SourceID)
}

// Upsert writes or clears the folder's entry.
func (s *FolderSink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.upsertFolderVector(ctx, key.SourceID, rows)
}

// UpsertBatch writes the folder's entry; a folder is one item.
func (s *FolderSink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	if len(rows) == 0 {
		return nil
	}
	return s.store.upsertFolderVector(ctx, key.SourceID, rows)
}

// StampExpected is a no-op: a folder is one item.
func (*FolderSink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps lists the folders owed an entry or owing a removal.
func (s *FolderSink) FindGaps(ctx context.Context) ([]string, error) {
	return s.store.findFolderGaps(ctx, s.currentModel)
}

// Coverage counts the folders that have the entry they are owed.
func (s *FolderSink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	var indexed, expected int
	if err := s.store.db.QueryRowContext(ctx, folderCoverageQuery).Scan(&indexed, &expected); err != nil {
		return indexjobs.Coverage{}, fmt.Errorf("resourceindex: folder coverage: %w", err)
	}
	return indexjobs.Coverage{Indexed: indexed, Expected: expected, ExpectedKnown: true}, nil
}

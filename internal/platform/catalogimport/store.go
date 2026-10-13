package catalogimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Store keeps the import's runs and its mapping of DataHub URNs to catalog
// references in PostgreSQL.
type Store struct {
	db *sql.DB
}

// NewStore keeps runs and the mapping in db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

var _ Mapping = (*Store)(nil)

const selectMap = `SELECT urn, reference FROM catalog_import_map`

// Map returns every URN the import has recorded and what it became.
func (s *Store) Map(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, selectMap)
	if err != nil {
		return nil, fmt.Errorf("reading the import map: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var urn, ref string
		if err := rows.Scan(&urn, &ref); err != nil {
			return nil, fmt.Errorf("reading the import map: %w", err)
		}
		out[urn] = ref
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the import map: %w", err)
	}
	return out, nil
}

const upsertMap = `
INSERT INTO catalog_import_map (urn, reference) VALUES ($1, $2)
ON CONFLICT (urn) DO UPDATE SET reference = EXCLUDED.reference, imported_at = NOW()`

// Record keeps what a URN became.
func (s *Store) Record(ctx context.Context, urn, reference string) error {
	if _, err := s.db.ExecContext(ctx, upsertMap, urn, reference); err != nil {
		return fmt.Errorf("recording %s: %w", urn, err)
	}
	return nil
}

// Run is one run of the import as every replica reports it.
type Run struct {
	ID         string     `json:"id"`
	SourceURL  string     `json:"source_url"`
	StartedBy  string     `json:"started_by"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Running is true while the run holds the import's lock; a run with no
	// finished_at that does not was cut off when its replica stopped.
	Running bool    `json:"running"`
	Report  *Report `json:"report,omitempty"`
	Error   string  `json:"error,omitempty"`
}

const insertRun = `
INSERT INTO catalog_imports (source_url, started_by) VALUES ($1, $2)
RETURNING id::text, started_at`

// begin records a run's start.
func (s *Store) begin(ctx context.Context, sourceURL, by string) (Run, error) {
	r := Run{SourceURL: sourceURL, StartedBy: by, Running: true}
	if err := s.db.QueryRowContext(ctx, insertRun, sourceURL, by).Scan(&r.ID, &r.StartedAt); err != nil {
		return Run{}, fmt.Errorf("recording the import's start: %w", err)
	}
	return r, nil
}

const finishRun = `
UPDATE catalog_imports SET finished_at = NOW(), report = $2::jsonb, error = $3 WHERE id = $1::uuid`

// finish records a run's end.
func (s *Store) finish(ctx context.Context, id string, report *Report, failure string) error {
	if report == nil {
		report = newReport()
	}
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encoding the import's report: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, finishRun, id, string(body), failure); err != nil {
		return fmt.Errorf("recording the import's end: %w", err)
	}
	return nil
}

const selectRuns = `
SELECT id::text, source_url, started_by, started_at, finished_at, report::text, error
FROM catalog_imports ORDER BY started_at DESC LIMIT $1`

// Runs lists the latest runs, newest first. running says whether the lock is
// held, which only the newest unfinished run can be holding.
func (s *Store) Runs(ctx context.Context, limit int, running bool) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, selectRuns, limit)
	if err != nil {
		return nil, fmt.Errorf("reading the imports: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Run, 0)
	holder := running
	for rows.Next() {
		var r Run
		var finished sql.NullTime
		var report string
		if err := rows.Scan(&r.ID, &r.SourceURL, &r.StartedBy, &r.StartedAt, &finished, &report, &r.Error); err != nil {
			return nil, fmt.Errorf("reading the imports: %w", err)
		}
		if finished.Valid {
			r.FinishedAt = &finished.Time
			r.Report = &Report{}
			if err := json.Unmarshal([]byte(report), r.Report); err != nil {
				return nil, fmt.Errorf("reading the report of import %s: %w", r.ID, err)
			}
		} else {
			r.Running, holder = holder, false
			if !r.Running {
				r.Error = "the run stopped before it finished, when the replica running it stopped; start the import again"
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the imports: %w", err)
	}
	return out, nil
}

// Package recstore is the PostgreSQL store of managed-script recordings
// (internal/platform/scriptrec, migration 000166).
package recstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
)

// Compile-time interface verification.
var _ scriptrec.Store = (*Store)(nil)

// Store keeps recordings in script_recordings.
type Store struct {
	db *sql.DB
}

// New returns a Store over db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// columns is the column list every SELECT reads, mirrored by scan.
const columns = `run_id, COALESCE(script_id::text, ''), script_name, kind, recorded_by,
	COALESCE(version, 0), source_sha256, succeeded, reason, bytes, kept, created_at, data`

func scan(row interface{ Scan(...any) error }) (*scriptrec.Stored, error) {
	var s scriptrec.Stored
	err := row.Scan(&s.RunID, &s.ScriptID, &s.ScriptName, &s.Kind, &s.RecordedBy,
		&s.Version, &s.SourceSHA256, &s.Succeeded, &s.Reason, &s.Bytes, &s.Kept, &s.CreatedAt, &s.Data)
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by the caller, which names the read
	}
	return &s, nil
}

// Save stores a recording, replacing one under the same run id.
func (s *Store) Save(ctx context.Context, rec scriptrec.Stored) error {
	var version any
	if rec.Version > 0 {
		version = rec.Version
	}
	var scriptID any
	if rec.ScriptID != "" {
		scriptID = rec.ScriptID
	}
	var data any
	if rec.Data != nil {
		data = rec.Data
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO script_recordings (run_id, script_id, script_name, kind, recorded_by,
		                               version, source_sha256, succeeded, reason, bytes, data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (run_id) DO UPDATE SET
		    succeeded = EXCLUDED.succeeded, reason = EXCLUDED.reason,
		    bytes = EXCLUDED.bytes, data = EXCLUDED.data, created_at = NOW()`,
		rec.RunID, scriptID, rec.ScriptName, rec.Kind, rec.RecordedBy,
		version, rec.SourceSHA256, rec.Succeeded, rec.Reason, len(rec.Data), data)
	if err != nil {
		return fmt.Errorf("save script recording: %w", err)
	}
	return nil
}

// Get returns one recording.
func (s *Store) Get(ctx context.Context, runID string) (*scriptrec.Stored, error) {
	rec, err := scan(s.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM script_recordings WHERE run_id = $1`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, scriptrec.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get script recording: %w", err)
	}
	return rec, nil
}

// Recent returns the newest replayable recordings of the script's successful
// runs.
func (s *Store) Recent(ctx context.Context, scriptID string, limit int) ([]scriptrec.Stored, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM script_recordings
		WHERE script_id = $1 AND kind = 'run' AND succeeded AND data IS NOT NULL
		ORDER BY created_at DESC LIMIT $2`, scriptID, limit)
	if err != nil {
		return nil, fmt.Errorf("list script recordings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []scriptrec.Stored{}
	for rows.Next() {
		rec, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning script recording: %w", err)
		}
		out = append(out, *rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate script recordings: %w", err)
	}
	return out, nil
}

// Keep marks the recordings a script's latest version names in its tests.
func (s *Store) Keep(ctx context.Context, scriptID, author string, runIDs []string) error {
	if runIDs == nil {
		runIDs = []string{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("keep script recordings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE script_recordings SET script_id = $1
		 WHERE run_id = ANY($3) AND script_id IS NULL AND recorded_by = $2`,
		scriptID, author, pq.Array(runIDs)); err != nil {
		return fmt.Errorf("attach script recordings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE script_recordings SET kept = (run_id = ANY($2))
		 WHERE script_id = $1 AND kept <> (run_id = ANY($2))`,
		scriptID, pq.Array(runIDs)); err != nil {
		return fmt.Errorf("keep script recordings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("keep script recordings: %w", err)
	}
	return nil
}

// Purge deletes the recordings past retention that no test keeps.
func (s *Store) Purge(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM script_recordings
		 WHERE NOT kept AND created_at < NOW() - ($1 || ' seconds')::INTERVAL`,
		int(retention.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("purge script recordings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting purged script recordings: %w", err)
	}
	return n, nil
}

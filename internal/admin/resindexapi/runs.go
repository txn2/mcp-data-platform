package resindexapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcereclass"
)

// Runs keeps the last reclassification where every replica reads it, and
// refuses a second start while one runs.
type Runs interface {
	// Begin records a run starting and reports false when one is running.
	Begin(ctx context.Context) (started time.Time, ok bool, err error)
	// Finish records how the run ended.
	Finish(ctx context.Context, res resourcereclass.ReclassifyResult, failure string) error
	// Last reads the last run, nil when none has started.
	Last(ctx context.Context) (*Run, error)
}

// PostgresRuns is Runs over resource_reclassify_runs.
type PostgresRuns struct{ DB *sql.DB }

// Begin implements Runs. A run unfinished after an hour is taken to have
// stopped with its replica; the reclassifier's advisory lock still refuses a
// start while one truly runs.
func (p PostgresRuns) Begin(ctx context.Context) (time.Time, bool, error) {
	var started time.Time
	err := p.DB.QueryRowContext(ctx, `INSERT INTO resource_reclassify_runs (id, started_at)
		VALUES (1, NOW())
		ON CONFLICT (id) DO UPDATE SET started_at = NOW(), finished_at = NULL,
			examined = 0, changed = 0, cleared = 0, error = ''
		WHERE resource_reclassify_runs.finished_at IS NOT NULL
		   OR resource_reclassify_runs.started_at < NOW() - INTERVAL '1 hour'
		RETURNING started_at`).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("resindexapi: recording a reclassification: %w", err)
	}
	return started.UTC(), true, nil
}

// Finish implements Runs.
func (p PostgresRuns) Finish(ctx context.Context, res resourcereclass.ReclassifyResult, failure string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE resource_reclassify_runs
		SET finished_at = NOW(), examined = $1, changed = $2, cleared = $3, error = $4
		WHERE id = 1`, res.Examined, res.Changed, res.Cleared, failure)
	if err != nil {
		return fmt.Errorf("resindexapi: recording a reclassification's end: %w", err)
	}
	return nil
}

// Last implements Runs.
func (p PostgresRuns) Last(ctx context.Context) (*Run, error) {
	var (
		run      Run
		finished sql.NullTime
		res      resourcereclass.ReclassifyResult
	)
	err := p.DB.QueryRowContext(ctx, `SELECT started_at, finished_at, examined, changed, cleared, error
		FROM resource_reclassify_runs WHERE id = 1`).
		Scan(&run.StartedAt, &finished, &res.Examined, &res.Changed, &res.Cleared, &run.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no run has started
	}
	if err != nil {
		return nil, fmt.Errorf("resindexapi: reading the last reclassification: %w", err)
	}
	run.StartedAt = run.StartedAt.UTC()
	if finished.Valid {
		at := finished.Time.UTC()
		run.FinishedAt, run.Result = &at, &res
	}
	return &run, nil
}

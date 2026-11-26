package sitetypecheck

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Statuses of a check that has not finished.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
)

// maxStateBytes bounds the incremental state kept for an environment; a
// larger one is not kept and the next check runs whole.
const maxStateBytes = 32 << 20

// pollEvery is how often a replica looks for a queued check it was not woken
// for: one another replica queued.
const pollEvery = 30 * time.Second

// Check is one check of one release.
type Check struct {
	ID            int64      `json:"id"`
	SiteID        string     `json:"-"`
	ReleaseNumber int        `json:"release"`
	Environment   string     `json:"environment"`
	Tsconfig      string     `json:"tsconfig"`
	Status        string     `json:"status"`
	Result        *Result    `json:"result,omitempty"`
	RequestedBy   string     `json:"requested_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	// Ahead is how many queued checks run before this one, for a queued
	// check.
	Ahead int `json:"ahead,omitempty"`
}

// Source reads a release's source tree.
type Source func(ctx context.Context, siteID string, release int) (fs.FS, error)

// Queue holds the checks and runs them one at a time on each replica.
type Queue struct {
	db      *sql.DB
	checker *Checker
	source  Source
	timeout time.Duration
	wake    chan struct{}
}

// NewQueue builds the queue over db, running checks with checker on the
// release trees source reads.
func NewQueue(db *sql.DB, checker *Checker, source Source) *Queue {
	return &Queue{db: db, checker: checker, source: source, timeout: checker.cfg.Timeout, wake: make(chan struct{}, 1)}
}

// Request is one check to queue: a site's release, the environment whose
// incremental state it uses, the project file and who asked.
type Request struct {
	SiteID      string
	Release     int
	Environment string
	Tsconfig    string
	By          string
}

// Enqueue queues a check of a release and wakes this replica's worker.
func (q *Queue) Enqueue(ctx context.Context, req Request) (int64, error) {
	var id int64
	if err := q.db.QueryRowContext(ctx, `
		INSERT INTO site_typechecks (site_id, release_number, environment, tsconfig, requested_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, req.SiteID, req.Release, req.Environment, req.Tsconfig, req.By).Scan(&id); err != nil {
		return 0, fmt.Errorf("queueing a type check: %w", err)
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return id, nil
}

// checkColumns are the columns a Check is read from.
const checkColumns = `id, site_id, release_number, environment, tsconfig, status, result, requested_by, created_at, started_at, finished_at`

// Latest is the newest check of a release, nil when it has none.
func (q *Queue) Latest(ctx context.Context, siteID string, release int) (*Check, error) {
	row := q.db.QueryRowContext(ctx, `SELECT `+checkColumns+` FROM site_typechecks
		WHERE site_id = $1 AND release_number = $2 ORDER BY created_at DESC, id DESC LIMIT 1`, siteID, release)
	c, err := scanCheck(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no check is not an error
	}
	if err != nil {
		return nil, err
	}
	if c.Status == StatusQueued {
		_ = q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_typechecks
			WHERE status IN ('queued', 'running') AND (created_at, id) < ($1, $2)`, c.CreatedAt, c.ID).Scan(&c.Ahead)
	}
	return c, nil
}

// Summary is a release's newest check as a list shows it.
type Summary struct {
	Status string `json:"status"`
	Errors int    `json:"errors"`
}

// Summaries are the newest check of each of a site's releases, by number.
func (q *Queue) Summaries(ctx context.Context, siteID string) (map[int]Summary, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT DISTINCT ON (release_number) release_number, status,
		       CASE WHEN jsonb_typeof(result -> 'diagnostics') = 'array' THEN jsonb_array_length(result -> 'diagnostics') ELSE 0 END
		  FROM site_typechecks WHERE site_id = $1
		 ORDER BY release_number, created_at DESC, id DESC`, siteID)
	if err != nil {
		return nil, fmt.Errorf("reading type checks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]Summary{}
	for rows.Next() {
		var (
			n int
			s Summary
		)
		if err := rows.Scan(&n, &s.Status, &s.Errors); err != nil {
			return nil, fmt.Errorf("scanning a type check: %w", err)
		}
		out[n] = s
	}
	return out, rows.Err() //nolint:wrapcheck // iteration error of the read above
}

func scanCheck(row interface{ Scan(...any) error }) (*Check, error) {
	var (
		c   Check
		raw []byte
	)
	if err := row.Scan(&c.ID, &c.SiteID, &c.ReleaseNumber, &c.Environment, &c.Tsconfig, &c.Status, &raw,
		&c.RequestedBy, &c.CreatedAt, &c.StartedAt, &c.FinishedAt); err != nil {
		return nil, err //nolint:wrapcheck // the caller reads sql.ErrNoRows
	}
	if len(raw) > 0 {
		var r Result
		if json.Unmarshal(raw, &r) == nil {
			c.Result = &r
		}
	}
	return &c, nil
}

// Loop runs this replica's worker until stop closes or ctx ends: queued
// checks one at a time, woken by an enqueue here and polling for one another
// replica queued.
func (q *Queue) Loop(ctx context.Context, stop <-chan struct{}) {
	bgloop.Run(ctx, bgloop.Loop{
		Name: bgloop.NameSiteTypechecks, Every: pollEvery, Wake: q.wake, Stop: stop, SpanPerUnit: true, Immediate: true,
		Body: q.runQueued,
	})
}

// runQueued runs queued checks one at a time until none is left.
func (q *Queue) runQueued(ctx context.Context) error {
	for {
		c, err := q.claim(ctx)
		if err != nil || c == nil {
			return err
		}
		err = bgloop.Unit(ctx, bgloop.NameSiteTypechecks, func(ctx context.Context) error { return q.runOne(ctx, c) })
		if err != nil {
			slog.WarnContext(ctx, "site type check failed to record", "check", c.ID, "error", logsan.SanitizeForLog(err.Error()))
		}
	}
}

// claim takes the oldest queued check, or one whose replica stopped while
// running it.
func (q *Queue) claim(ctx context.Context) (*Check, error) {
	row := q.db.QueryRowContext(ctx, `
		UPDATE site_typechecks SET status = 'running', started_at = NOW(), claimed_until = NOW() + $1::interval
		 WHERE id = (SELECT id FROM site_typechecks
		              WHERE status = 'queued' OR (status = 'running' AND claimed_until < NOW())
		              ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING `+checkColumns, fmt.Sprintf("%d seconds", int((q.timeout+5*time.Minute).Seconds())))
	c, err := scanCheck(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing queued
	}
	if err != nil {
		return nil, fmt.Errorf("claiming a type check: %w", err)
	}
	return c, nil
}

// runOne runs a claimed check and records its result and the environment's
// state.
func (q *Queue) runOne(ctx context.Context, c *Check) error {
	src, err := q.source(ctx, c.SiteID, c.ReleaseNumber)
	if err != nil {
		return q.finish(ctx, c, Result{Status: StatusFailed, Message: "the release's files could not be read: " + err.Error()}, nil)
	}
	var state []byte
	_ = q.db.QueryRowContext(ctx, `SELECT tsbuildinfo FROM site_typecheck_state WHERE site_id = $1 AND environment = $2`,
		c.SiteID, c.Environment).Scan(&state)
	res, next := q.checker.Check(ctx, src, c.Tsconfig, state)
	logFinished(ctx, c, res)
	return q.finish(ctx, c, res, next)
}

// logFinished logs how a check ended and how long it spent compiling the
// compiler apart from the whole (#2833), which is what a deployment's CPU
// limit costs a check; a compilation cache that could not be used is a
// warning, since every check then compiles again.
func logFinished(ctx context.Context, c *Check, res Result) {
	attrs := []any{"check", c.ID, "site", c.SiteID, "status", res.Status, "duration_ms", res.DurationMS, "compile_ms", res.CompileMS}
	if res.CacheError != "" {
		slog.WarnContext(ctx, "site type check could not use the compilation cache and compiled without it",
			append(attrs, "error", logsan.SanitizeForLog(res.CacheError))...)
		return
	}
	slog.InfoContext(ctx, "site type check finished", attrs...)
}

// finish records a check's result, and the environment's new state.
func (q *Queue) finish(ctx context.Context, c *Check, res Result, state []byte) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("encoding a type check: %w", err)
	}
	if _, err := q.db.ExecContext(ctx, `
		UPDATE site_typechecks SET status = $2, result = $3, finished_at = NOW(), claimed_until = NULL WHERE id = $1`,
		c.ID, res.Status, raw); err != nil {
		return fmt.Errorf("recording a type check: %w", err)
	}
	if len(state) == 0 || len(state) > maxStateBytes {
		return nil
	}
	if _, err := q.db.ExecContext(ctx, `
		INSERT INTO site_typecheck_state (site_id, environment, tsbuildinfo) VALUES ($1, $2, $3)
		ON CONFLICT (site_id, environment) DO UPDATE SET tsbuildinfo = EXCLUDED.tsbuildinfo, updated_at = NOW()`,
		c.SiteID, c.Environment, state); err != nil {
		return fmt.Errorf("keeping the incremental state: %w", err)
	}
	return nil
}

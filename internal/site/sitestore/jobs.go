package sitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// NewJobID is a job's id.
func NewJobID() string { return "sjob_" + uuid.NewString() }

// CreateJob records a job as running.
func (s *Store) CreateJob(ctx context.Context, j *site.Job) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO site_jobs (id, site_id, kind, phase, environment, source, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING status, created_at, updated_at`,
		j.ID, j.SiteID, j.Kind, j.Phase, j.Environment, j.Source, j.CreatedBy).Scan(&j.Status, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	if j.Skipped == nil {
		j.Skipped = []site.Skipped{}
	}
	return nil
}

const jobColumns = `id, site_id, kind, status, phase, environment, source, done, total, pages, bytes, skipped_count, redirects, skipped,
	release_number, error, created_by, created_at, updated_at`

func scanJob(row interface{ Scan(...any) error }) (*site.Job, error) {
	j := &site.Job{}
	var skipped []byte
	err := row.Scan(&j.ID, &j.SiteID, &j.Kind, &j.Status, &j.Phase, &j.Environment, &j.Source, &j.Done, &j.Total, &j.Pages,
		&j.Bytes, &j.SkippedCount, &j.Redirects, &skipped, &j.Release, &j.Error, &j.CreatedBy, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, site.ErrNoJob
	}
	if err != nil {
		return nil, fmt.Errorf("read job: %w", err)
	}
	j.Skipped = []site.Skipped{}
	if err := json.Unmarshal(skipped, &j.Skipped); err != nil {
		return nil, fmt.Errorf("decode skipped addresses: %w", err)
	}
	return j, nil
}

// Job reads a job by id.
func (s *Store) Job(ctx context.Context, id string) (*site.Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM site_jobs WHERE id = $1`, id))
}

// ActiveJobs lists a site's running jobs, newest first.
func (s *Store) ActiveJobs(ctx context.Context, siteID string) ([]site.Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+jobColumns+` FROM site_jobs WHERE site_id = $1 AND status = $2 ORDER BY created_at DESC`, siteID, site.JobRunning)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	return out, nil
}

// ReportJob records progress, which is the job's heartbeat, and reads in the
// same statement whether it was asked to stop.
func (s *Store) ReportJob(ctx context.Context, id string, p site.JobProgress) (bool, error) {
	var stop bool
	err := s.db.QueryRowContext(ctx, `
		UPDATE site_jobs SET phase = $2, done = $3, total = $4, pages = $5, redirects = $6, bytes = $7, skipped_count = $8, updated_at = NOW()
		 WHERE id = $1 RETURNING stop_requested`, id, p.Phase, p.Done, p.Total, p.Pages, p.Redirects, p.Bytes, p.Skipped).Scan(&stop)
	if errors.Is(err, sql.ErrNoRows) {
		return false, site.ErrNoJob
	}
	if err != nil {
		return false, fmt.Errorf("report job: %w", err)
	}
	return stop, nil
}

// FinishJob records how a job ended.
func (s *Store) FinishJob(ctx context.Context, id string, r site.JobResult) error {
	skipped, err := json.Marshal(nonNilSkipped(r.Skipped))
	if err != nil {
		return fmt.Errorf("encode skipped addresses: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE site_jobs SET status = $2, release_number = $3, pages = $4, redirects = $5, skipped = $6, error = $7, updated_at = NOW()
		 WHERE id = $1`, id, r.Status, r.Release, r.Pages, r.Redirects, skipped, r.Error)
	if err != nil {
		return fmt.Errorf("finish job: %w", err)
	}
	return oneRow(res, site.ErrNoJob)
}

func nonNilSkipped(in []site.Skipped) []site.Skipped {
	if in == nil {
		return []site.Skipped{}
	}
	return in
}

// StopJob asks a running job to stop; the replica running it reads this at
// its next report.
func (s *Store) StopJob(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE site_jobs SET stop_requested = TRUE WHERE id = $1 AND status = $2`, id, site.JobRunning)
	if err != nil {
		return fmt.Errorf("stop job: %w", err)
	}
	return oneRow(res, site.ErrNoJob)
}

package whstore

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// maxRejections is how many rejected requests a source keeps.
const maxRejections = 50

// Count is a number of requests of one source with one outcome in one minute.
type Count struct {
	Source  string
	Minute  time.Time
	Outcome string
	Count   int64
}

// Rejection is one refused request: when, which outcome, and why. Never the
// body.
type Rejection struct {
	Source  string    `json:"-"`
	At      time.Time `json:"at"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason"`
}

// RecordCounts adds counts to the per-minute totals. Counts naming the same
// source, minute and outcome are summed before they are written, because one
// upsert cannot touch a row twice. A count for a source that
// no longer exists is dropped with the rest of that source's data rather than
// failing the batch.
func (s *Store) RecordCounts(ctx context.Context, counts []Count) error {
	if len(counts) == 0 {
		return nil
	}
	sources := make([]string, 0, len(counts))
	minutes := make([]time.Time, 0, len(counts))
	outcomes := make([]string, 0, len(counts))
	values := make([]int64, 0, len(counts))
	for _, c := range counts {
		sources = append(sources, c.Source)
		minutes = append(minutes, c.Minute.UTC().Truncate(time.Minute))
		outcomes = append(outcomes, c.Outcome)
		values = append(values, c.Count)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO webhook_request_counts (source, minute, outcome, count)
		 SELECT c.source, c.minute, c.outcome, SUM(c.count)
		   FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::bigint[]) AS c(source, minute, outcome, count)
		   JOIN webhook_sources ws ON ws.name = c.source
		  GROUP BY c.source, c.minute, c.outcome
		 ON CONFLICT (source, minute, outcome) DO UPDATE
		    SET count = webhook_request_counts.count + EXCLUDED.count`,
		pq.Array(sources), pq.Array(minutes), pq.Array(outcomes), pq.Array(values))
	if err != nil {
		return fmt.Errorf("recording webhook request counts: %w", err)
	}
	return nil
}

// RecordRejections keeps rejected requests, then trims each source named to
// its newest maxRejections.
func (s *Store) RecordRejections(ctx context.Context, rejections []Rejection) error {
	if len(rejections) == 0 {
		return nil
	}
	sources := make([]string, 0, len(rejections))
	ats := make([]time.Time, 0, len(rejections))
	outcomes := make([]string, 0, len(rejections))
	reasons := make([]string, 0, len(rejections))
	for _, r := range rejections {
		sources = append(sources, r.Source)
		ats = append(ats, r.At.UTC())
		outcomes = append(outcomes, r.Outcome)
		reasons = append(reasons, r.Reason)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recording webhook rejections: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO webhook_rejections (source, rejected_at, outcome, reason)
		 SELECT r.source, r.at, r.outcome, r.reason
		   FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::text[]) AS r(source, at, outcome, reason)
		   JOIN webhook_sources ws ON ws.name = r.source`,
		pq.Array(sources), pq.Array(ats), pq.Array(outcomes), pq.Array(reasons)); err != nil {
		return fmt.Errorf("recording webhook rejections: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM webhook_rejections w
		  WHERE w.source = ANY($1::text[])
		    AND w.id NOT IN (
		        SELECT k.id FROM webhook_rejections k
		         WHERE k.source = w.source
		         ORDER BY k.id DESC
		         LIMIT $2)`,
		pq.Array(sources), maxRejections); err != nil {
		return fmt.Errorf("trimming webhook rejections: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing webhook rejections: %w", err)
	}
	return nil
}

// PruneCounts deletes per-minute counts older than before.
func (s *Store) PruneCounts(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM webhook_request_counts WHERE minute < $1`, before.UTC()); err != nil {
		return fmt.Errorf("pruning webhook request counts: %w", err)
	}
	return nil
}

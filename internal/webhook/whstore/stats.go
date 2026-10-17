package whstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// maxRejections is how many rows of rejected requests a source keeps for each
// outcome. Kept per outcome so that no outcome can push another's out: a burst
// of rate_limited refusals from an authenticated sender must not delete the
// unauthorized rows that are the reason the list exists (#2001).
const maxRejections = 50

// rejectionRunGap is how close a rejection must be to the newest row of its
// source and outcome, with the same reason, to be added to that row rather
// than written as a new one.
const rejectionRunGap = time.Minute

// errRecordingRejections wraps every failure of a rejection write.
const errRecordingRejections = "recording webhook rejections: %w"

// Count is a number of requests of one source with one outcome, answered
// with one HTTP status, in one minute. Status 0 is a request whose sender went
// away before it was answered.
type Count struct {
	Source  string
	Minute  time.Time
	Outcome string
	Status  int
	Count   int64
}

// Rejection is a run of refused requests of one source with one outcome and
// reason: how many, the first and the latest. Never the body. A rejection the
// receiver records is a run of one.
type Rejection struct {
	Source string `json:"-"`
	// At is the latest rejection of the run.
	At time.Time `json:"at"`
	// FirstAt is the earliest. Equal to At for a single rejection.
	FirstAt time.Time `json:"first_at"`
	// Count is how many rejections the row stands for, at least one.
	Count   int64  `json:"count"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
	// RequestHash is the SHA-256 of the refused request's body, and EventIDs
	// the ids of its first events, when its events were read; empty for a
	// request refused before that. A rejection carrying ids is one request,
	// never folded into a run.
	RequestHash string   `json:"request_hash"`
	EventIDs    []string `json:"event_ids"`
}

// identified reports whether r names the events it refused.
func (r Rejection) identified() bool { return len(r.EventIDs) > 0 }

// RecordCounts adds counts to the per-minute totals: by outcome in
// webhook_request_counts, and by the HTTP status the sender was answered with
// in webhook_status_counts (#2150), in one transaction so a retried batch is
// never counted twice in one of them. Counts naming the same key are summed
// before they are written, because one upsert cannot touch a row twice. A
// count for a source that no longer exists is dropped with the rest of that
// source's data rather than failing the batch.
//
// The status is a table of its own so webhook_request_counts keeps the key a
// replica of the previous release writes it under during a rolling upgrade.
func (s *Store) RecordCounts(ctx context.Context, counts []Count) error {
	if len(counts) == 0 {
		return nil
	}
	sources := make([]string, 0, len(counts))
	minutes := make([]time.Time, 0, len(counts))
	outcomes := make([]string, 0, len(counts))
	statuses := make([]int64, 0, len(counts))
	values := make([]int64, 0, len(counts))
	for _, c := range counts {
		sources = append(sources, c.Source)
		minutes = append(minutes, c.Minute.UTC().Truncate(time.Minute))
		outcomes = append(outcomes, c.Outcome)
		statuses = append(statuses, int64(c.Status))
		values = append(values, c.Count)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recording webhook request counts: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO webhook_request_counts (source, minute, outcome, count)
		 SELECT c.source, c.minute, c.outcome, SUM(c.count)
		   FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::bigint[]) AS c(source, minute, outcome, count)
		   JOIN webhook_sources ws ON ws.name = c.source
		  GROUP BY c.source, c.minute, c.outcome
		 ON CONFLICT (source, minute, outcome) DO UPDATE
		    SET count = webhook_request_counts.count + EXCLUDED.count`,
		pq.Array(sources), pq.Array(minutes), pq.Array(outcomes), pq.Array(values)); err != nil {
		return fmt.Errorf("recording webhook request counts: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO webhook_status_counts (source, minute, http_status, count)
		 SELECT c.source, c.minute, c.status, SUM(c.count)
		   FROM unnest($1::text[], $2::timestamptz[], $3::smallint[], $4::bigint[]) AS c(source, minute, status, count)
		   JOIN webhook_sources ws ON ws.name = c.source
		  GROUP BY c.source, c.minute, c.status
		 ON CONFLICT (source, minute, http_status) DO UPDATE
		    SET count = webhook_status_counts.count + EXCLUDED.count`,
		pq.Array(sources), pq.Array(minutes), pq.Array(statuses), pq.Array(values)); err != nil {
		return fmt.Errorf("recording webhook status counts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("recording webhook request counts: %w", err)
	}
	return nil
}

// RecordRejections keeps rejected requests, then trims each source and outcome
// named to its newest maxRejections rows.
//
// The batch is first folded into runs (collapseRejections). Each run is added
// to the newest row of its source and outcome when that row has the same
// reason and its latest rejection is within rejectionRunGap of the run's
// first, and is written as a new row otherwise. A burst therefore costs one
// row, written once, when the database is least able to absorb many.
func (s *Store) RecordRejections(ctx context.Context, rejections []Rejection) error {
	runs := collapseRejections(rejections)
	if len(runs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(errRecordingRejections, err)
	}
	defer func() { _ = tx.Rollback() }()
	sources := make([]string, 0, len(runs))
	outcomes := make([]string, 0, len(runs))
	for _, r := range runs {
		if err := recordRun(ctx, tx, r); err != nil {
			return err
		}
		sources = append(sources, r.Source)
		outcomes = append(outcomes, r.Outcome)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM webhook_rejections w
		  USING unnest($1::text[], $2::text[]) AS t(source, outcome)
		  WHERE w.source = t.source AND w.outcome = t.outcome
		    AND w.id NOT IN (
		        SELECT k.id FROM webhook_rejections k
		         WHERE k.source = t.source AND k.outcome = t.outcome
		         ORDER BY k.id DESC
		         LIMIT $3)`,
		pq.Array(sources), pq.Array(outcomes), maxRejections); err != nil {
		return fmt.Errorf("trimming webhook rejections: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing webhook rejections: %w", err)
	}
	return nil
}

// recordRun adds one run to the newest row it continues, or writes it as a new
// row. A rejection that names its events is always a new row, and no run is
// added to one. A run for a source that no longer exists is dropped with the
// rest of that source's data.
func recordRun(ctx context.Context, tx *sql.Tx, r Rejection) error {
	if !r.identified() {
		res, err := tx.ExecContext(ctx,
			`UPDATE webhook_rejections
			    SET count = count + $4, rejected_at = GREATEST(rejected_at, $5)
			  WHERE id = (SELECT id FROM webhook_rejections
			               WHERE source = $1 AND outcome = $2
			               ORDER BY id DESC LIMIT 1)
			    AND reason = $3
			    AND rejected_at >= $6
			    AND cardinality(event_ids) = 0`,
			r.Source, r.Outcome, r.Reason, r.Count, r.At.UTC(), r.FirstAt.UTC().Add(-rejectionRunGap))
		if err != nil {
			return fmt.Errorf(errRecordingRejections, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf(errRecordingRejections, err)
		}
		if n > 0 {
			return nil
		}
	}
	ids := r.EventIDs
	if ids == nil {
		ids = []string{}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO webhook_rejections (source, first_at, rejected_at, count, outcome, reason, request_hash, event_ids)
		 SELECT ws.name, $2, $3, $4, $5, $6, $7, $8::text[] FROM webhook_sources ws WHERE ws.name = $1`,
		r.Source, r.FirstAt.UTC(), r.At.UTC(), r.Count, r.Outcome, r.Reason, r.RequestHash, pq.Array(ids)); err != nil {
		return fmt.Errorf(errRecordingRejections, err)
	}
	return nil
}

// collapseRejections folds a batch into runs: a rejection with the same
// source, outcome and reason as the run before it of that source and outcome,
// within rejectionRunGap of that run's latest, is added to it. A rejection
// that names its events is its own row and ends the run before it. The runs
// keep the order of their first rejection, so the newest row written for an
// outcome is the newest run.
//
// Only the newest maxRejections rejections of each source and outcome keep
// their ids: the rows written for older ones would be trimmed in the same
// transaction, so they fold like any other.
func collapseRejections(rejections []Rejection) []Rejection {
	type key struct{ source, outcome string }
	rejections = keepNewestIdentities(rejections)
	runs := make([]Rejection, 0, len(rejections))
	last := map[key]int{}
	for _, r := range rejections {
		if r.identified() {
			r.Count, r.FirstAt = max(r.Count, 1), r.At
			delete(last, key{r.Source, r.Outcome})
			runs = append(runs, r)
			continue
		}
		count := max(r.Count, 1)
		first := r.FirstAt
		if first.IsZero() || first.After(r.At) {
			first = r.At
		}
		k := key{r.Source, r.Outcome}
		if i, ok := last[k]; ok && runs[i].Reason == r.Reason && !first.After(runs[i].At.Add(rejectionRunGap)) {
			runs[i].Count += count
			if r.At.After(runs[i].At) {
				runs[i].At = r.At
			}
			if first.Before(runs[i].FirstAt) {
				runs[i].FirstAt = first
			}
			continue
		}
		r.Count, r.FirstAt = count, first
		last[k] = len(runs)
		runs = append(runs, r)
	}
	return runs
}

// keepNewestIdentities strips the request hash and event ids from every
// rejection but the newest maxRejections of its source and outcome, returning
// a copy when it changes any.
func keepNewestIdentities(rejections []Rejection) []Rejection {
	type key struct{ source, outcome string }
	seen := map[key]int{}
	var out []Rejection
	for i := len(rejections) - 1; i >= 0; i-- {
		r := rejections[i]
		if !r.identified() {
			continue
		}
		k := key{r.Source, r.Outcome}
		seen[k]++
		if seen[k] <= maxRejections {
			continue
		}
		if out == nil {
			out = append([]Rejection(nil), rejections...)
		}
		out[i].EventIDs, out[i].RequestHash = nil, ""
	}
	if out == nil {
		return rejections
	}
	return out
}

// PruneCounts deletes the telemetry older than before: request counts,
// acknowledgement latency, each receiver's minutes and the compaction runs.
func (s *Store) PruneCounts(ctx context.Context, before time.Time) error {
	for _, q := range []struct{ what, sql string }{
		{"request counts", `DELETE FROM webhook_request_counts WHERE minute < $1`},
		{"status counts", `DELETE FROM webhook_status_counts WHERE minute < $1`},
		{"acknowledgement latency", `DELETE FROM webhook_ack_latency WHERE minute < $1`},
		{"receiver minutes", `DELETE FROM webhook_replica_minutes WHERE minute < $1`},
		{"compaction runs", `DELETE FROM webhook_compactions WHERE compacted_at < $1`},
	} {
		if _, err := s.db.ExecContext(ctx, q.sql, before.UTC()); err != nil {
			return fmt.Errorf("pruning webhook %s: %w", q.what, err)
		}
	}
	return nil
}

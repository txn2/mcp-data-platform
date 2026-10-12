package whstore

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"time"

	"golang.org/x/sync/errgroup"
)

// maxOverviewRejections is how many rows of rejected requests the overview of
// every source returns for each outcome, newest first, across all sources.
const maxOverviewRejections = 50

// Summary is one source's line on the overview of every source: the counts
// and window aggregates Status reports, without the per-source rejections and
// the window positions only the source's own page shows.
type Summary struct {
	// LastHour and LastDay are request counts by outcome.
	LastHour map[string]int64
	LastDay  map[string]int64
	// LastSegmentAt is when the source last received an event; nil when no
	// window it still holds records one.
	LastSegmentAt *time.Time
	// Pending and Failing count windows owed a compaction, and those of them
	// whose last attempt failed.
	Pending   int
	Failing   int
	LastError string
	// AckLastHour and AckLastDay are how long the deliveries answered 202
	// waited for that answer.
	AckLastHour AckLatency
	AckLastDay  AckLatency
}

// VolumePoint is the number of requests of one source with one outcome in
// one bucket of the overview's series. At is the bucket's start.
type VolumePoint struct {
	Source  string
	At      time.Time
	Outcome string
	Count   int64
}

// Overview is what the overview of every source reads, in one pass over the
// control tables.
type Overview struct {
	// Summaries holds one entry per source that has counts or windows; a
	// source with neither has no entry.
	Summaries map[string]Summary
	// Volume is the request counts from Since, summed per bucket of Step,
	// oldest bucket first. A bucket with no requests has no point.
	Volume []VolumePoint
	// Rejections are the newest rejected requests of every source, or of the
	// source read, whose latest rejection is at or after Since.
	Rejections []Rejection
	// Since is the series' lower bound: a count is in the series when its
	// minute is at or after it, the bound LastHour and LastDay are read with.
	Since time.Time
}

// Overview reads the summary at now of every source, or of source alone when
// it is not empty, the request series over the span before now in buckets of
// step, and the newest rejections within that span. The five reads are
// independent and run concurrently.
func (s *Store) Overview(ctx context.Context, source string, now time.Time, span, step time.Duration) (Overview, error) {
	ov := Overview{Since: now.Add(-span).UTC()}
	counts := map[string]Summary{}
	windows := map[string]Summary{}
	var latency map[string]ackSpans
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return s.overviewCounts(gctx, source, now, counts) })
	g.Go(func() error { return s.overviewWindows(gctx, source, windows) })
	g.Go(func() (err error) {
		latency, err = s.readLatency(gctx, source, now)
		return err
	})
	g.Go(func() (err error) {
		ov.Volume, err = s.volume(gctx, source, ov.Since, step)
		return err
	})
	g.Go(func() (err error) {
		ov.Rejections, err = s.recentRejections(gctx, source, ov.Since)
		return err
	})
	if err := g.Wait(); err != nil {
		return Overview{}, err //nolint:wrapcheck // each read wraps its own error
	}
	ov.Summaries = mergeSummaries(counts, windows, latency)
	return ov, nil
}

// emptySummary is a summary whose count maps are empty rather than nil.
func emptySummary() Summary {
	return Summary{LastHour: map[string]int64{}, LastDay: map[string]int64{}}
}

// mergeSummaries joins the count half, the window half and the latency of
// each source's summary.
func mergeSummaries(counts, windows map[string]Summary, latency map[string]ackSpans) map[string]Summary {
	out := make(map[string]Summary, len(counts))
	maps.Copy(out, counts)
	for name, w := range windows {
		sum, ok := out[name]
		if !ok {
			sum = emptySummary()
		}
		sum.LastSegmentAt, sum.Pending, sum.Failing, sum.LastError = w.LastSegmentAt, w.Pending, w.Failing, w.LastError
		out[name] = sum
	}
	for name, l := range latency {
		sum, ok := out[name]
		if !ok {
			sum = emptySummary()
		}
		sum.AckLastHour, sum.AckLastDay = l.hour, l.day
		out[name] = sum
	}
	return out
}

// overviewCounts fills each source's last hour's and last day's counts, with
// the bounds readCounts uses for one source.
func (s *Store) overviewCounts(ctx context.Context, source string, now time.Time, out map[string]Summary) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, outcome,
		        COALESCE(SUM(count) FILTER (WHERE minute >= $1), 0),
		        SUM(count)
		   FROM webhook_request_counts
		  WHERE minute >= $2 AND ($3::text = '' OR source = $3::text)
		  GROUP BY source, outcome`,
		now.Add(-time.Hour).UTC(), now.Add(-24*time.Hour).UTC(), source)
	if err != nil {
		return fmt.Errorf("reading webhook request counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			source, outcome string
			hour, day       int64
		)
		if err := rows.Scan(&source, &outcome, &hour, &day); err != nil {
			return fmt.Errorf("scanning webhook request count: %w", err)
		}
		sum, ok := out[source]
		if !ok {
			sum = emptySummary()
		}
		if hour > 0 {
			sum.LastHour[outcome] = hour
		}
		sum.LastDay[outcome] = day
		out[source] = sum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating webhook request counts: %w", err)
	}
	return nil
}

// overviewWindows fills each source's window aggregates, as Status reads them
// for one source: the last error is the newest window's that has one.
func (s *Store) overviewWindows(ctx context.Context, source string, out map[string]Summary) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source,
		        MAX(last_segment_at),
		        COUNT(*) FILTER (WHERE generation <> compacted_generation),
		        COUNT(*) FILTER (WHERE generation <> compacted_generation AND last_error <> ''),
		        COALESCE((ARRAY_AGG(last_error ORDER BY window_start DESC) FILTER (WHERE last_error <> ''))[1], '')
		   FROM webhook_windows
		  WHERE expired_at IS NULL AND ($1::text = '' OR source = $1::text)
		  GROUP BY source`, source)
	if err != nil {
		return fmt.Errorf("reading webhook window status: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			source string
			sum    Summary
			last   sql.NullTime
		)
		if err := rows.Scan(&source, &last, &sum.Pending, &sum.Failing, &sum.LastError); err != nil {
			return fmt.Errorf("scanning webhook window status: %w", err)
		}
		sum.LastSegmentAt = timePtr(last)
		out[source] = sum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating webhook window status: %w", err)
	}
	return nil
}

// volume reads the request counts at or after since, summed per source,
// outcome and bucket of step. A bucket starts at a multiple of step since the
// Unix epoch, so the buckets of two reads line up.
func (s *Store) volume(ctx context.Context, source string, since time.Time, step time.Duration) ([]VolumePoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source,
		        to_timestamp((floor(extract(epoch FROM minute) / $2::integer) * $2::integer)::double precision) AS bucket,
		        outcome,
		        SUM(count)
		   FROM webhook_request_counts
		  WHERE minute >= $1 AND ($3::text = '' OR source = $3::text)
		  GROUP BY source, bucket, outcome
		  ORDER BY bucket, source, outcome`,
		since.UTC(), int(step.Seconds()), source)
	if err != nil {
		return nil, fmt.Errorf("reading webhook request volume: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]VolumePoint, 0)
	for rows.Next() {
		var p VolumePoint
		if err := rows.Scan(&p.Source, &p.At, &p.Outcome, &p.Count); err != nil {
			return nil, fmt.Errorf("scanning webhook request volume: %w", err)
		}
		p.At = p.At.UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook request volume: %w", err)
	}
	return out, nil
}

// recentRejections reads the newest rejected requests of every source, or of
// source alone when it is not empty, whose latest rejection is at or after
// since: the newest maxOverviewRejections rows of each outcome, so one
// outcome's volume never hides another's (#2001).
func (s *Store) recentRejections(ctx context.Context, source string, since time.Time) ([]Rejection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, first_at, rejected_at, count, outcome, reason, request_hash, event_ids FROM (
		     SELECT w.*, ROW_NUMBER() OVER (PARTITION BY outcome ORDER BY rejected_at DESC, id DESC) AS n
		       FROM webhook_rejections w
		      WHERE w.rejected_at >= $2 AND ($3::text = '' OR w.source = $3::text)) ranked
		  WHERE n <= $1
		  ORDER BY rejected_at DESC, id DESC`, maxOverviewRejections, since.UTC(), source)
	if err != nil {
		return nil, fmt.Errorf("reading webhook rejections: %w", err)
	}
	return scanRejections(rows)
}

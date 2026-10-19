package whstore

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// ReplicaMinute is one receiver's minute (#2150): the events it accepted, the
// events and bytes it wrote to object storage, and what its spool held at the
// minute's last report.
type ReplicaMinute struct {
	Replica          string    `json:"replica"`
	Minute           time.Time `json:"at"`
	ReceivedEvents   int64     `json:"received_events"`
	DrainedEvents    int64     `json:"drained_events"`
	DrainedBytes     int64     `json:"drained_bytes"`
	SpoolBytes       int64     `json:"spool_bytes"`
	SpoolEvents      int64     `json:"spool_events"`
	OldestAgeSeconds int64     `json:"oldest_age_seconds"`
}

// RecordReplicaMinutes adds each minute to the stored one: events and bytes
// summed, the spool's depth and age the greater of the two.
func (s *Store) RecordReplicaMinutes(ctx context.Context, minutes []ReplicaMinute) error {
	for _, m := range minutes {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO webhook_replica_minutes
			        (replica, minute, received_events, drained_events, drained_bytes, spool_bytes, spool_events, oldest_age_seconds)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (replica, minute) DO UPDATE SET
			    received_events = webhook_replica_minutes.received_events + EXCLUDED.received_events,
			    drained_events = webhook_replica_minutes.drained_events + EXCLUDED.drained_events,
			    drained_bytes = webhook_replica_minutes.drained_bytes + EXCLUDED.drained_bytes,
			    spool_bytes = GREATEST(webhook_replica_minutes.spool_bytes, EXCLUDED.spool_bytes),
			    spool_events = GREATEST(webhook_replica_minutes.spool_events, EXCLUDED.spool_events),
			    oldest_age_seconds = GREATEST(webhook_replica_minutes.oldest_age_seconds, EXCLUDED.oldest_age_seconds)`,
			m.Replica, m.Minute.UTC().Truncate(time.Minute), m.ReceivedEvents, m.DrainedEvents, m.DrainedBytes,
			m.SpoolBytes, m.SpoolEvents, m.OldestAgeSeconds); err != nil {
			return fmt.Errorf("recording a webhook receiver minute: %w", err)
		}
	}
	return nil
}

// CompactionRun is one compaction of one window (#2150).
type CompactionRun struct {
	Source      string    `json:"source"`
	WindowStart time.Time `json:"window_start"`
	CompactedAt time.Time `json:"at"`
	DurationMS  int64     `json:"duration_ms"`
	// LagSeconds is how long after the window ended the compaction finished.
	LagSeconds int64 `json:"lag_seconds"`
	Segments   int   `json:"segments"`
	Events     int64 `json:"events"`
	// InputBytes is the gzipped segments read, DecodedBytes the events held
	// in memory, OutputBytes the Parquet file written.
	InputBytes   int64 `json:"input_bytes"`
	DecodedBytes int64 `json:"decoded_bytes"`
	OutputBytes  int64 `json:"output_bytes"`
}

// RecordCompactionRun keeps one compaction's figures.
func (s *Store) RecordCompactionRun(ctx context.Context, c CompactionRun) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO webhook_compactions
		        (source, window_start, compacted_at, duration_ms, lag_seconds, segments, events,
		         input_bytes, decoded_bytes, output_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (source, window_start, compacted_at) DO NOTHING`,
		c.Source, c.WindowStart.UTC(), c.CompactedAt.UTC(), c.DurationMS, c.LagSeconds, c.Segments, c.Events,
		c.InputBytes, c.DecodedBytes, c.OutputBytes); err != nil {
		return fmt.Errorf("recording a webhook compaction: %w", err)
	}
	return nil
}

// RollupTelemetry folds the per-minute rows older than before into one row
// per hour, at the hour's first minute: request counts and the receivers'
// minutes summed (spool depth and age the greatest), acknowledgement latency
// bucket by bucket with the slowest delivery kept. Each table is folded in
// one statement, so a pass that stops part-way leaves every row counted once.
func (s *Store) RollupTelemetry(ctx context.Context, before time.Time) error {
	before = before.UTC().Truncate(time.Hour)
	statements := []struct{ what, sql string }{
		{"request counts", `WITH moved AS (
			DELETE FROM webhook_request_counts
			 WHERE minute < $1 AND minute <> date_bin('1 hour', minute, TIMESTAMPTZ 'epoch')
			RETURNING source, minute, outcome, count)
			INSERT INTO webhook_request_counts (source, minute, outcome, count)
			SELECT source, date_bin('1 hour', minute, TIMESTAMPTZ 'epoch'), outcome, SUM(count)
			  FROM moved GROUP BY 1, 2, 3
			ON CONFLICT (source, minute, outcome) DO UPDATE
			   SET count = webhook_request_counts.count + EXCLUDED.count`},
		{"status counts", `WITH moved AS (
			DELETE FROM webhook_status_counts
			 WHERE minute < $1 AND minute <> date_bin('1 hour', minute, TIMESTAMPTZ 'epoch')
			RETURNING source, minute, http_status, count)
			INSERT INTO webhook_status_counts (source, minute, http_status, count)
			SELECT source, date_bin('1 hour', minute, TIMESTAMPTZ 'epoch'), http_status, SUM(count)
			  FROM moved GROUP BY 1, 2, 3
			ON CONFLICT (source, minute, http_status) DO UPDATE
			   SET count = webhook_status_counts.count + EXCLUDED.count`},
		{"receiver minutes", `WITH moved AS (
			DELETE FROM webhook_replica_minutes
			 WHERE minute < $1 AND minute <> date_bin('1 hour', minute, TIMESTAMPTZ 'epoch')
			RETURNING *)
			INSERT INTO webhook_replica_minutes
			       (replica, minute, received_events, drained_events, drained_bytes, spool_bytes, spool_events, oldest_age_seconds)
			SELECT replica, date_bin('1 hour', minute, TIMESTAMPTZ 'epoch'), SUM(received_events), SUM(drained_events),
			       SUM(drained_bytes), MAX(spool_bytes), MAX(spool_events), MAX(oldest_age_seconds)
			  FROM moved GROUP BY 1, 2
			ON CONFLICT (replica, minute) DO UPDATE SET
			   received_events = webhook_replica_minutes.received_events + EXCLUDED.received_events,
			   drained_events = webhook_replica_minutes.drained_events + EXCLUDED.drained_events,
			   drained_bytes = webhook_replica_minutes.drained_bytes + EXCLUDED.drained_bytes,
			   spool_bytes = GREATEST(webhook_replica_minutes.spool_bytes, EXCLUDED.spool_bytes),
			   spool_events = GREATEST(webhook_replica_minutes.spool_events, EXCLUDED.spool_events),
			   oldest_age_seconds = GREATEST(webhook_replica_minutes.oldest_age_seconds, EXCLUDED.oldest_age_seconds)`},
		{"acknowledgement latency", `WITH moved AS (
			DELETE FROM webhook_ack_latency
			 WHERE minute < $1 AND minute <> date_bin('1 hour', minute, TIMESTAMPTZ 'epoch')
			RETURNING *),
			binned AS (SELECT *, date_bin('1 hour', minute, TIMESTAMPTZ 'epoch') AS hour FROM moved),
			counts AS (
			  SELECT source, hour, array_agg(n ORDER BY i) AS buckets
			    FROM (SELECT b.source, b.hour, u.i, SUM(u.n) AS n
			            FROM binned b, unnest(b.buckets) WITH ORDINALITY AS u(n, i)
			           GROUP BY b.source, b.hour, u.i) per_bucket
			   GROUP BY source, hour),
			sums AS (
			  SELECT source, hour, SUM(buffer_ms_sum) AS bs, SUM(mark_ms_sum) AS ms, SUM(put_ms_sum) AS ps
			    FROM binned GROUP BY source, hour),
			slowest AS (
			  SELECT DISTINCT ON (source, hour) source, hour, max_ms, slowest_at,
			         slowest_buffer_ms, slowest_mark_ms, slowest_put_ms
			    FROM binned ORDER BY source, hour, max_ms DESC)
			INSERT INTO webhook_ack_latency
			       (source, minute, buckets, max_ms, slowest_at, slowest_buffer_ms, slowest_mark_ms, slowest_put_ms,
			        buffer_ms_sum, mark_ms_sum, put_ms_sum)
			SELECT c.source, c.hour, c.buckets, sl.max_ms, sl.slowest_at, sl.slowest_buffer_ms, sl.slowest_mark_ms,
			       sl.slowest_put_ms, su.bs, su.ms, su.ps
			  FROM counts c
			  JOIN sums su ON su.source = c.source AND su.hour = c.hour
			  JOIN slowest sl ON sl.source = c.source AND sl.hour = c.hour
			ON CONFLICT (source, minute) DO UPDATE SET
			   buckets = ARRAY(SELECT COALESCE(t.a, 0) + COALESCE(t.b, 0)
			                     FROM unnest(webhook_ack_latency.buckets, EXCLUDED.buckets) WITH ORDINALITY AS t(a, b, i)
			                    ORDER BY t.i),
			   slowest_at = CASE WHEN EXCLUDED.max_ms > webhook_ack_latency.max_ms
			                     THEN EXCLUDED.slowest_at ELSE webhook_ack_latency.slowest_at END,
			   slowest_buffer_ms = CASE WHEN EXCLUDED.max_ms > webhook_ack_latency.max_ms
			                     THEN EXCLUDED.slowest_buffer_ms ELSE webhook_ack_latency.slowest_buffer_ms END,
			   slowest_mark_ms = CASE WHEN EXCLUDED.max_ms > webhook_ack_latency.max_ms
			                     THEN EXCLUDED.slowest_mark_ms ELSE webhook_ack_latency.slowest_mark_ms END,
			   slowest_put_ms = CASE WHEN EXCLUDED.max_ms > webhook_ack_latency.max_ms
			                     THEN EXCLUDED.slowest_put_ms ELSE webhook_ack_latency.slowest_put_ms END,
			   max_ms = GREATEST(webhook_ack_latency.max_ms, EXCLUDED.max_ms),
			   buffer_ms_sum = webhook_ack_latency.buffer_ms_sum + EXCLUDED.buffer_ms_sum,
			   mark_ms_sum = webhook_ack_latency.mark_ms_sum + EXCLUDED.mark_ms_sum,
			   put_ms_sum = webhook_ack_latency.put_ms_sum + EXCLUDED.put_ms_sum`},
	}
	for _, st := range statements {
		if _, err := s.db.ExecContext(ctx, st.sql, before); err != nil {
			return fmt.Errorf("rolling up webhook %s: %w", st.what, err)
		}
	}
	return nil
}

// minStepSeconds is the shortest bucket, in seconds, a series is read in.
const minStepSeconds = 60

// timeSpan is the bounds and bucket length a series is read over.
type timeSpan struct {
	from time.Time
	// until is the upper bound as a query argument, nil for none.
	until any
	step  int
}

// Telemetry reads the series from since until until in step-long buckets,
// for source or for every source when it is empty. The receivers' series are
// every replica's whatever the source; the storage is as it stands now.
func (s *Store) Telemetry(ctx context.Context, source string, since, until time.Time, step time.Duration) (Telemetry, error) {
	out := Telemetry{
		Statuses: []StatusPoint{}, Replicas: []ReplicaMinute{}, Ack: []AckPoint{},
		Compactions: []CompactionRun{}, Storage: []SourceStorage{},
	}
	stepSec := max(int(step.Seconds()), minStepSeconds)
	var err error
	span := timeSpan{from: since.UTC(), until: bound(until), step: stepSec}
	if out.Statuses, err = s.statusSeries(ctx, source, span); err != nil {
		return out, err
	}
	if out.Replicas, err = s.replicaSeries(ctx, span); err != nil {
		return out, err
	}
	if out.Ack, err = s.ackSeries(ctx, source, span); err != nil {
		return out, err
	}
	if out.Compactions, err = s.compactionRuns(ctx, source, span); err != nil {
		return out, err
	}
	if out.Storage, err = s.Storage(ctx, source); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) statusSeries(ctx context.Context, source string, sp timeSpan) ([]StatusPoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT to_timestamp((floor(extract(epoch FROM minute) / $2::integer) * $2::integer)::double precision) AS at, source, http_status, SUM(count)
		   FROM webhook_status_counts
		  WHERE minute >= $1 AND ($4::timestamptz IS NULL OR minute < $4) AND ($3::text = '' OR source = $3::text)
		  GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`, sp.from, sp.step, source, sp.until)
	if err != nil {
		return nil, fmt.Errorf("reading webhook statuses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []StatusPoint{}
	for rows.Next() {
		var p StatusPoint
		if err := rows.Scan(&p.At, &p.Source, &p.Status, &p.Count); err != nil {
			return nil, fmt.Errorf("scanning webhook statuses: %w", err)
		}
		p.At = p.At.UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading webhook statuses: %w", err)
	}
	return out, nil
}

func (s *Store) replicaSeries(ctx context.Context, sp timeSpan) ([]ReplicaMinute, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT to_timestamp((floor(extract(epoch FROM minute) / $2::integer) * $2::integer)::double precision) AS at, replica, SUM(received_events), SUM(drained_events),
		        SUM(drained_bytes), MAX(spool_bytes), MAX(spool_events), MAX(oldest_age_seconds)
		   FROM webhook_replica_minutes
		  WHERE minute >= $1 AND ($3::timestamptz IS NULL OR minute < $3)
		  GROUP BY 1, 2 ORDER BY 1, 2`, sp.from, sp.step, sp.until)
	if err != nil {
		return nil, fmt.Errorf("reading webhook receiver minutes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ReplicaMinute{}
	for rows.Next() {
		var m ReplicaMinute
		if err := rows.Scan(&m.Minute, &m.Replica, &m.ReceivedEvents, &m.DrainedEvents, &m.DrainedBytes,
			&m.SpoolBytes, &m.SpoolEvents, &m.OldestAgeSeconds); err != nil {
			return nil, fmt.Errorf("scanning webhook receiver minutes: %w", err)
		}
		m.Minute = m.Minute.UTC()
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading webhook receiver minutes: %w", err)
	}
	return out, nil
}

func (s *Store) ackSeries(ctx context.Context, source string, sp timeSpan) ([]AckPoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`WITH binned AS (
		   SELECT to_timestamp((floor(extract(epoch FROM minute) / $2::integer) * $2::integer)::double precision) AS at, source, buckets, max_ms,
		          buffer_ms_sum, mark_ms_sum, put_ms_sum
		     FROM webhook_ack_latency
		    WHERE minute >= $1 AND ($4::timestamptz IS NULL OR minute < $4) AND ($3::text = '' OR source = $3::text))
		 SELECT b.at, b.source, b.max_ms, b.bs, b.ms, b.ps, c.buckets
		   FROM (SELECT at, source, MAX(max_ms) AS max_ms, SUM(buffer_ms_sum) AS bs,
		                SUM(mark_ms_sum) AS ms, SUM(put_ms_sum) AS ps
		           FROM binned GROUP BY at, source) b
		   JOIN (SELECT at, source, array_agg(n ORDER BY i) AS buckets
		           FROM (SELECT x.at, x.source, u.i, SUM(u.n) AS n
		                   FROM binned x, unnest(x.buckets) WITH ORDINALITY AS u(n, i)
		                  GROUP BY x.at, x.source, u.i) per_bucket
		          GROUP BY at, source) c ON c.at = b.at AND c.source = b.source
		  ORDER BY b.at, b.source`, sp.from, sp.step, source, sp.until)
	if err != nil {
		return nil, fmt.Errorf("reading webhook acknowledgement series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []AckPoint{}
	for rows.Next() {
		var (
			p          AckPoint
			bs, ms, ps int64
			raw        pq.Int64Array
		)
		if err := rows.Scan(&p.At, &p.Source, &p.MaxMS, &bs, &ms, &ps, &raw); err != nil {
			return nil, fmt.Errorf("scanning webhook acknowledgement series: %w", err)
		}
		var buckets [ackBuckets]int64
		copy(buckets[:], raw)
		sum := summarize(buckets, &SlowAck{TotalMS: p.MaxMS})
		p.At, p.Count, p.P50MS, p.P99MS = p.At.UTC(), sum.Count, sum.P50MS, sum.P99MS
		if p.Count > 0 {
			n := float64(p.Count)
			p.BufferMS, p.MarkMS, p.PutMS = float64(bs)/n, float64(ms)/n, float64(ps)/n
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading webhook acknowledgement series: %w", err)
	}
	return out, nil
}

// maxCompactionRuns bounds the runs a page reads; a week of one-minute
// windows of every source would otherwise be unbounded.
const maxCompactionRuns = 2000

func (s *Store) compactionRuns(ctx context.Context, source string, sp timeSpan) ([]CompactionRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, window_start, compacted_at, duration_ms, lag_seconds, segments, events,
		        input_bytes, decoded_bytes, output_bytes
		   FROM webhook_compactions
		  WHERE compacted_at >= $1 AND ($4::timestamptz IS NULL OR compacted_at < $4) AND ($2::text = '' OR source = $2::text)
		  ORDER BY compacted_at DESC LIMIT $3`, sp.from, source, maxCompactionRuns, sp.until)
	if err != nil {
		return nil, fmt.Errorf("reading webhook compactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []CompactionRun{}
	for rows.Next() {
		var c CompactionRun
		if err := rows.Scan(&c.Source, &c.WindowStart, &c.CompactedAt, &c.DurationMS, &c.LagSeconds, &c.Segments,
			&c.Events, &c.InputBytes, &c.DecodedBytes, &c.OutputBytes); err != nil {
			return nil, fmt.Errorf("scanning webhook compactions: %w", err)
		}
		c.WindowStart, c.CompactedAt = c.WindowStart.UTC(), c.CompactedAt.UTC()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading webhook compactions: %w", err)
	}
	return out, nil
}

// Storage reads each source's objects, or source's alone when it is not
// empty.
func (s *Store) Storage(ctx context.Context, source string) ([]SourceStorage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, COALESCE(SUM(raw_bytes), 0), COALESCE(SUM(raw_segments), 0),
		        COALESCE(SUM(compacted_bytes) FILTER (WHERE compacted_at IS NOT NULL), 0),
		        COUNT(*) FILTER (WHERE compacted_at IS NOT NULL)
		   FROM webhook_windows
		  WHERE expired_at IS NULL AND ($1::text = '' OR source = $1::text)
		  GROUP BY source ORDER BY source`, source)
	if err != nil {
		return nil, fmt.Errorf("reading webhook storage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []SourceStorage{}
	for rows.Next() {
		var st SourceStorage
		if err := rows.Scan(&st.Source, &st.RawBytes, &st.RawSegments, &st.CompactedBytes, &st.CompactedWindows); err != nil {
			return nil, fmt.Errorf("scanning webhook storage: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading webhook storage: %w", err)
	}
	return out, nil
}

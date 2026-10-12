package whstore

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/lib/pq"
)

// ackBuckets is how many buckets an acknowledgement latency is counted in:
// one per bound of AckBound, and one for everything slower than the last.
const ackBuckets = 12

// AckBound is the upper bound, in milliseconds, of acknowledgement-latency
// bucket i, for i below ackBuckets-1. The last bucket has no upper bound.
func AckBound(i int) int64 {
	//nolint:revive // the bucket bounds are the table itself, named nowhere else
	bounds := [ackBuckets - 1]int64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000}
	return bounds[i]
}

// The percentiles AckLatency reports.
const (
	median          = 0.5
	ninetyNinthPerc = 0.99
)

// AckBucket is the bucket a latency of ms milliseconds is counted in.
func AckBucket(ms int64) int {
	for i := range ackBuckets - 1 {
		if ms <= AckBound(i) {
			return i
		}
	}
	return ackBuckets - 1
}

// SlowAck is the slowest acknowledged delivery of a span: when it was
// answered, how long the sender waited in all, and how long its events spent
// in the buffer, recording their window and writing their segment.
type SlowAck struct {
	At       time.Time `json:"at"`
	TotalMS  int64     `json:"total_ms"`
	BufferMS int64     `json:"buffer_ms"`
	MarkMS   int64     `json:"mark_ms"`
	PutMS    int64     `json:"put_ms"`
}

// Latency is the acknowledgement latency of one source's deliveries answered
// 202 in one minute: a count per bucket and the slowest of them.
type Latency struct {
	Source  string
	Minute  time.Time
	Buckets [ackBuckets]int64
	Slowest SlowAck
}

// Observe adds one acknowledged delivery to l.
func (l *Latency) Observe(s SlowAck) {
	l.Buckets[AckBucket(s.TotalMS)]++
	if s.TotalMS >= l.Slowest.TotalMS {
		l.Slowest = s
	}
}

// Merge adds o's deliveries to l.
func (l *Latency) Merge(o Latency) {
	for i, n := range o.Buckets {
		l.Buckets[i] += n
	}
	if o.Slowest.TotalMS > l.Slowest.TotalMS {
		l.Slowest = o.Slowest
	}
}

// AckLatency is a source's acknowledgement latency over a span. P50MS and
// P99MS are the upper bound of the bucket the percentile falls in, capped at
// MaxMS, so each is the most it could be: "p99_ms 250" means 99% of
// deliveries were answered in 250ms or less.
type AckLatency struct {
	Count int64 `json:"count"`
	P50MS int64 `json:"p50_ms"`
	P99MS int64 `json:"p99_ms"`
	MaxMS int64 `json:"max_ms"`
	// Slowest is the slowest delivery of the span, null when there was none.
	Slowest *SlowAck `json:"slowest"`
}

// percentile is the bound of the bucket rank q of the counted deliveries
// falls in, capped at most.
func percentile(buckets [ackBuckets]int64, count int64, q float64, most int64) int64 {
	if count == 0 {
		return 0
	}
	rank := int64(math.Ceil(q * float64(count)))
	var seen int64
	for i, n := range buckets {
		seen += n
		if seen >= rank {
			if i == ackBuckets-1 {
				return most
			}
			return min(AckBound(i), most)
		}
	}
	return most
}

// summarize turns bucket counts and the slowest delivery into an AckLatency.
func summarize(buckets [ackBuckets]int64, slowest *SlowAck) AckLatency {
	var count int64
	for _, n := range buckets {
		count += n
	}
	out := AckLatency{Count: count}
	if count == 0 || slowest == nil {
		return out
	}
	out.MaxMS = slowest.TotalMS
	out.P50MS = percentile(buckets, count, median, out.MaxMS)
	out.P99MS = percentile(buckets, count, ninetyNinthPerc, out.MaxMS)
	out.Slowest = slowest
	return out
}

// RecordLatency adds each minute's latency to the stored one: bucket counts
// summed, the slower of the two slowest kept. A minute of a source that no
// longer exists is dropped with the rest of that source's data.
func (s *Store) RecordLatency(ctx context.Context, latencies []Latency) error {
	for _, l := range latencies {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO webhook_ack_latency
			        (source, minute, buckets, max_ms, slowest_at, slowest_buffer_ms, slowest_mark_ms, slowest_put_ms)
			 SELECT ws.name, $2, $3::bigint[], $4, $5, $6, $7, $8 FROM webhook_sources ws WHERE ws.name = $1
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
			    max_ms = GREATEST(webhook_ack_latency.max_ms, EXCLUDED.max_ms)`,
			l.Source, l.Minute.UTC().Truncate(time.Minute), pq.Array(l.Buckets[:]), l.Slowest.TotalMS,
			l.Slowest.At.UTC(), l.Slowest.BufferMS, l.Slowest.MarkMS, l.Slowest.PutMS); err != nil {
			return fmt.Errorf("recording webhook acknowledgement latency: %w", err)
		}
	}
	return nil
}

// ackSpans is the last hour's and the last day's latency of one source.
type ackSpans struct {
	hour, day AckLatency
}

// readLatency reads the last hour's and the last day's acknowledgement
// latency of source, or of every source when source is empty, keyed by
// source.
func (s *Store) readLatency(ctx context.Context, source string, now time.Time) (map[string]ackSpans, error) {
	hourFrom, dayFrom := now.Add(-time.Hour).UTC(), now.Add(-24*time.Hour).UTC()
	hour, day, err := s.readLatencyBuckets(ctx, source, hourFrom, dayFrom)
	if err != nil {
		return nil, err
	}
	slowHour, slowDay, err := s.readSlowest(ctx, source, hourFrom, dayFrom)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ackSpans, len(day))
	for name, b := range day {
		var h [ackBuckets]int64
		if hb := hour[name]; hb != nil {
			h = *hb
		}
		out[name] = ackSpans{hour: summarize(h, slowHour[name]), day: summarize(*b, slowDay[name])}
	}
	return out, nil
}

// readLatencyBuckets sums each source's bucket counts over the last hour and
// the last day.
func (s *Store) readLatencyBuckets(ctx context.Context, source string, hourFrom, dayFrom time.Time,
) (hour, day map[string]*[ackBuckets]int64, err error) {
	hour, day = map[string]*[ackBuckets]int64{}, map[string]*[ackBuckets]int64{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT l.source, b.i,
		        COALESCE(SUM(b.n) FILTER (WHERE l.minute >= $1), 0),
		        SUM(b.n)
		   FROM webhook_ack_latency l, unnest(l.buckets) WITH ORDINALITY AS b(n, i)
		  WHERE l.minute >= $2 AND ($3::text = '' OR l.source = $3::text)
		  GROUP BY l.source, b.i`,
		hourFrom, dayFrom, source)
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook acknowledgement latency: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			name   string
			i      int
			hn, dn int64
		)
		if err := rows.Scan(&name, &i, &hn, &dn); err != nil {
			return nil, nil, fmt.Errorf("scanning webhook acknowledgement latency: %w", err)
		}
		if i < 1 || i > ackBuckets {
			continue
		}
		if day[name] == nil {
			day[name], hour[name] = &[ackBuckets]int64{}, &[ackBuckets]int64{}
		}
		day[name][i-1] = dn
		hour[name][i-1] = hn
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating webhook acknowledgement latency: %w", err)
	}
	return hour, day, nil
}

// readSlowest reads each source's slowest delivery of the last hour and of
// the last day.
func (s *Store) readSlowest(ctx context.Context, source string, hourFrom, dayFrom time.Time) (hour, day map[string]*SlowAck, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, minute >= $1, day_rank = 1, max_ms, slowest_at, slowest_buffer_ms, slowest_mark_ms, slowest_put_ms
		   FROM (SELECT l.*,
		                ROW_NUMBER() OVER (PARTITION BY source ORDER BY max_ms DESC, minute DESC) AS day_rank,
		                ROW_NUMBER() OVER (PARTITION BY source, minute >= $1 ORDER BY max_ms DESC, minute DESC) AS span_rank
		           FROM webhook_ack_latency l
		          WHERE minute >= $2 AND ($3::text = '' OR source = $3::text)) ranked
		  WHERE day_rank = 1 OR (span_rank = 1 AND minute >= $1)`,
		hourFrom, dayFrom, source)
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook slowest acknowledgements: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hour, day = map[string]*SlowAck{}, map[string]*SlowAck{}
	for rows.Next() {
		var (
			name            string
			inHour, dayBest bool
			a               SlowAck
			at              sql.NullTime
		)
		if err := rows.Scan(&name, &inHour, &dayBest, &a.TotalMS, &at, &a.BufferMS, &a.MarkMS, &a.PutMS); err != nil {
			return nil, nil, fmt.Errorf("scanning webhook slowest acknowledgement: %w", err)
		}
		a.At = at.Time.UTC()
		if dayBest {
			day[name] = &a
		}
		if inHour {
			if cur := hour[name]; cur == nil || a.TotalMS > cur.TotalMS {
				hour[name] = &a
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating webhook slowest acknowledgements: %w", err)
	}
	return hour, day, nil
}

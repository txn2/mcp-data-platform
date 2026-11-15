package sitetraffic

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// The grains a row is kept at.
const (
	GrainMinute = "minute"
	GrainHour   = "hour"
)

// Store keeps the shared rows in PostgreSQL.
type Store struct{ DB *sql.DB }

// Row is one row's key and counts, for a write.
type Row struct {
	Grain  string
	Bucket time.Time
	Key    Key
	Counts Counts
}

// ReplicaRow is one replica's row, for a write.
type ReplicaRow struct {
	Grain    string
	Bucket   time.Time
	Instance string
	Counts   Counts
}

const insertSiteRows = `INSERT INTO site_traffic (grain, bucket, kind, subject, environment, c2xx, c3xx, c4xx, c5xx, cother, l0, l1, l2, l3, l4, l5, l6, l7, l8, l9, l10, l11)
	SELECT * FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::text[], $5::text[], $6::bigint[], $7::bigint[], $8::bigint[], $9::bigint[], $10::bigint[], $11::bigint[], $12::bigint[], $13::bigint[], $14::bigint[], $15::bigint[], $16::bigint[], $17::bigint[], $18::bigint[], $19::bigint[], $20::bigint[], $21::bigint[], $22::bigint[])
	ON CONFLICT (grain, bucket, kind, subject, environment) DO UPDATE SET
		c2xx = site_traffic.c2xx + EXCLUDED.c2xx,
		c3xx = site_traffic.c3xx + EXCLUDED.c3xx,
		c4xx = site_traffic.c4xx + EXCLUDED.c4xx,
		c5xx = site_traffic.c5xx + EXCLUDED.c5xx,
		cother = site_traffic.cother + EXCLUDED.cother,
		l0 = site_traffic.l0 + EXCLUDED.l0,
		l1 = site_traffic.l1 + EXCLUDED.l1,
		l2 = site_traffic.l2 + EXCLUDED.l2,
		l3 = site_traffic.l3 + EXCLUDED.l3,
		l4 = site_traffic.l4 + EXCLUDED.l4,
		l5 = site_traffic.l5 + EXCLUDED.l5,
		l6 = site_traffic.l6 + EXCLUDED.l6,
		l7 = site_traffic.l7 + EXCLUDED.l7,
		l8 = site_traffic.l8 + EXCLUDED.l8,
		l9 = site_traffic.l9 + EXCLUDED.l9,
		l10 = site_traffic.l10 + EXCLUDED.l10,
		l11 = site_traffic.l11 + EXCLUDED.l11`

const insertReplicaRows = `INSERT INTO site_traffic_replicas (grain, bucket, instance, c2xx, c3xx, c4xx, c5xx, cother, l0, l1, l2, l3, l4, l5, l6, l7, l8, l9, l10, l11)
	SELECT * FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::bigint[], $5::bigint[], $6::bigint[], $7::bigint[], $8::bigint[], $9::bigint[], $10::bigint[], $11::bigint[], $12::bigint[], $13::bigint[], $14::bigint[], $15::bigint[], $16::bigint[], $17::bigint[], $18::bigint[], $19::bigint[], $20::bigint[])
	ON CONFLICT (grain, bucket, instance) DO UPDATE SET
		c2xx = site_traffic_replicas.c2xx + EXCLUDED.c2xx,
		c3xx = site_traffic_replicas.c3xx + EXCLUDED.c3xx,
		c4xx = site_traffic_replicas.c4xx + EXCLUDED.c4xx,
		c5xx = site_traffic_replicas.c5xx + EXCLUDED.c5xx,
		cother = site_traffic_replicas.cother + EXCLUDED.cother,
		l0 = site_traffic_replicas.l0 + EXCLUDED.l0,
		l1 = site_traffic_replicas.l1 + EXCLUDED.l1,
		l2 = site_traffic_replicas.l2 + EXCLUDED.l2,
		l3 = site_traffic_replicas.l3 + EXCLUDED.l3,
		l4 = site_traffic_replicas.l4 + EXCLUDED.l4,
		l5 = site_traffic_replicas.l5 + EXCLUDED.l5,
		l6 = site_traffic_replicas.l6 + EXCLUDED.l6,
		l7 = site_traffic_replicas.l7 + EXCLUDED.l7,
		l8 = site_traffic_replicas.l8 + EXCLUDED.l8,
		l9 = site_traffic_replicas.l9 + EXCLUDED.l9,
		l10 = site_traffic_replicas.l10 + EXCLUDED.l10,
		l11 = site_traffic_replicas.l11 + EXCLUDED.l11`

// countColumns is how many count columns a row has.
const countColumns = NumClasses + NumBuckets

// columnsOf is the count columns of rows as arrays, one per column.
func columnsOf(counts []Counts) []any {
	out := make([]any, countColumns)
	cols := make([][]int64, countColumns)
	for i := range cols {
		cols[i] = make([]int64, len(counts))
	}
	for r, c := range counts {
		for i, v := range c.Classes {
			cols[i][r] = v
		}
		for i, v := range c.Buckets {
			cols[NumClasses+i][r] = v
		}
	}
	for i := range cols {
		out[i] = pq.Array(cols[i])
	}
	return out
}

// Add adds rows and replica rows into the shared rows in one transaction: two
// statements however many rows. It reports whether the outcome is known: an
// error before the commit is false and nothing was written; an error from the
// commit itself is true, since whether it was written cannot be known.
func (s *Store) Add(ctx context.Context, rows []Row, replicas []ReplicaRow) (unknown bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin traffic flush: %w", err)
	}
	if len(rows) > 0 {
		grains, buckets, kinds, subjects, envs := make([]string, len(rows)), make([]time.Time, len(rows)), make([]string, len(rows)), make([]string, len(rows)), make([]string, len(rows))
		counts := make([]Counts, len(rows))
		for i, r := range rows {
			grains[i], buckets[i], kinds[i], subjects[i], envs[i], counts[i] = r.Grain, r.Bucket, r.Key.Kind, r.Key.Subject, r.Key.Environment, r.Counts
		}
		args := append([]any{pq.Array(grains), pq.Array(buckets), pq.Array(kinds), pq.Array(subjects), pq.Array(envs)}, columnsOf(counts)...)
		if _, err := tx.ExecContext(ctx, insertSiteRows, args...); err != nil {
			_ = tx.Rollback()
			return false, fmt.Errorf("add site traffic: %w", err)
		}
	}
	if len(replicas) > 0 {
		grains, buckets, instances := make([]string, len(replicas)), make([]time.Time, len(replicas)), make([]string, len(replicas))
		counts := make([]Counts, len(replicas))
		for i, r := range replicas {
			grains[i], buckets[i], instances[i], counts[i] = r.Grain, r.Bucket, r.Instance, r.Counts
		}
		args := append([]any{pq.Array(grains), pq.Array(buckets), pq.Array(instances)}, columnsOf(counts)...)
		if _, err := tx.ExecContext(ctx, insertReplicaRows, args...); err != nil {
			_ = tx.Rollback()
			return false, fmt.Errorf("add replica traffic: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return true, fmt.Errorf("commit traffic flush: %w", err)
	}
	return false, nil
}

// readTraffic wraps a failed read.
const readTraffic = "read traffic: %w"

const selectSeries = `SELECT date_bin($5::interval, bucket, TIMESTAMPTZ 'epoch') AS at, COALESCE(SUM(c2xx), 0), COALESCE(SUM(c3xx), 0), COALESCE(SUM(c4xx), 0), COALESCE(SUM(c5xx), 0), COALESCE(SUM(cother), 0), COALESCE(SUM(l0), 0), COALESCE(SUM(l1), 0), COALESCE(SUM(l2), 0), COALESCE(SUM(l3), 0), COALESCE(SUM(l4), 0), COALESCE(SUM(l5), 0), COALESCE(SUM(l6), 0), COALESCE(SUM(l7), 0), COALESCE(SUM(l8), 0), COALESCE(SUM(l9), 0), COALESCE(SUM(l10), 0), COALESCE(SUM(l11), 0)
	  FROM site_traffic
	 WHERE kind = $1 AND subject = $2 AND ($3 = '' OR environment = $3) AND grain = $4 AND bucket >= $6
	 GROUP BY at ORDER BY at`

const selectByEnvironment = `SELECT environment, COALESCE(SUM(c2xx), 0), COALESCE(SUM(c3xx), 0), COALESCE(SUM(c4xx), 0), COALESCE(SUM(c5xx), 0), COALESCE(SUM(cother), 0), COALESCE(SUM(l0), 0), COALESCE(SUM(l1), 0), COALESCE(SUM(l2), 0), COALESCE(SUM(l3), 0), COALESCE(SUM(l4), 0), COALESCE(SUM(l5), 0), COALESCE(SUM(l6), 0), COALESCE(SUM(l7), 0), COALESCE(SUM(l8), 0), COALESCE(SUM(l9), 0), COALESCE(SUM(l10), 0), COALESCE(SUM(l11), 0)
	  FROM site_traffic
	 WHERE kind = $1 AND subject = $2 AND grain = $3 AND bucket >= $4
	 GROUP BY environment ORDER BY environment`

const selectBySubject = `SELECT subject, environment, COALESCE(SUM(c2xx), 0), COALESCE(SUM(c3xx), 0), COALESCE(SUM(c4xx), 0), COALESCE(SUM(c5xx), 0), COALESCE(SUM(cother), 0), COALESCE(SUM(l0), 0), COALESCE(SUM(l1), 0), COALESCE(SUM(l2), 0), COALESCE(SUM(l3), 0), COALESCE(SUM(l4), 0), COALESCE(SUM(l5), 0), COALESCE(SUM(l6), 0), COALESCE(SUM(l7), 0), COALESCE(SUM(l8), 0), COALESCE(SUM(l9), 0), COALESCE(SUM(l10), 0), COALESCE(SUM(l11), 0)
	  FROM site_traffic
	 WHERE kind = $1 AND grain = 'hour' AND bucket >= $2
	 GROUP BY subject, environment`

const selectReplicas = `SELECT instance, COALESCE(SUM(c2xx), 0), COALESCE(SUM(c3xx), 0), COALESCE(SUM(c4xx), 0), COALESCE(SUM(c5xx), 0), COALESCE(SUM(cother), 0), COALESCE(SUM(l0), 0), COALESCE(SUM(l1), 0), COALESCE(SUM(l2), 0), COALESCE(SUM(l3), 0), COALESCE(SUM(l4), 0), COALESCE(SUM(l5), 0), COALESCE(SUM(l6), 0), COALESCE(SUM(l7), 0), COALESCE(SUM(l8), 0), COALESCE(SUM(l9), 0), COALESCE(SUM(l10), 0), COALESCE(SUM(l11), 0)
	  FROM site_traffic_replicas
	 WHERE grain = $1 AND bucket >= $2
	 GROUP BY instance ORDER BY instance`

// scanCounts reads the count columns after the leading ones.
func scanCounts(rows *sql.Rows, lead ...any) (Counts, error) {
	var c Counts
	dest := lead
	for i := range c.Classes {
		dest = append(dest, &c.Classes[i])
	}
	for i := range c.Buckets {
		dest = append(dest, &c.Buckets[i])
	}
	if err := rows.Scan(dest...); err != nil {
		return c, fmt.Errorf(readTraffic, err)
	}
	return c, nil
}

// Point is one step of a series.
type Point struct {
	At     time.Time
	Counts Counts
}

// SeriesQuery is one series to read: a key, its environment ("" for every
// one), the range's grain and step, and where it starts.
type SeriesQuery struct {
	Kind, Subject, Environment string
	Range                      Range
	Since                      time.Time
}

// Series is a key's counts over a range, in the range's steps.
func (s *Store) Series(ctx context.Context, q SeriesQuery) ([]Point, error) {
	rows, err := s.DB.QueryContext(ctx, selectSeries, q.Kind, q.Subject, q.Environment, q.Range.Grain,
		fmt.Sprintf("%d seconds", int64(q.Range.Step/time.Second)), q.Since)
	if err != nil {
		return nil, fmt.Errorf("read traffic series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Point{}
	for rows.Next() {
		var p Point
		if p.Counts, err = scanCounts(rows, &p.At); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read traffic series: %w", err)
	}
	return out, nil
}

// grouped reads a query whose first column names a group.
func (s *Store) grouped(ctx context.Context, query string, args ...any) (byName map[string]Counts, order []string, err error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf(readTraffic, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Counts{}
	order = []string{}
	for rows.Next() {
		var name string
		c, err := scanCounts(rows, &name)
		if err != nil {
			return nil, nil, err
		}
		out[name] = c
		order = append(order, name)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf(readTraffic, err)
	}
	return out, order, nil
}

// ByEnvironment is a key's counts from since by environment.
func (s *Store) ByEnvironment(ctx context.Context, kind, subject, grain string, since time.Time) (byEnv map[string]Counts, order []string, err error) {
	return s.grouped(ctx, selectByEnvironment, kind, subject, grain, since)
}

// BySubject is every subject's counts of kind from since, by environment, read
// from hour rows alone: at most 24 a site and environment for the Sites list's
// last 24 hours, however many requests they took.
func (s *Store) BySubject(ctx context.Context, kind string, since time.Time) (map[string]map[string]Counts, error) {
	rows, err := s.DB.QueryContext(ctx, selectBySubject, kind, since)
	if err != nil {
		return nil, fmt.Errorf(readTraffic, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]map[string]Counts{}
	for rows.Next() {
		var subject, env string
		c, err := scanCounts(rows, &subject, &env)
		if err != nil {
			return nil, err
		}
		if out[subject] == nil {
			out[subject] = map[string]Counts{}
		}
		out[subject][env] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(readTraffic, err)
	}
	return out, nil
}

// Replicas is each replica's counts across every site from since.
func (s *Store) Replicas(ctx context.Context, grain string, since time.Time) (byInstance map[string]Counts, order []string, err error) {
	return s.grouped(ctx, selectReplicas, grain, since)
}

const (
	sweepTraffic  = `DELETE FROM site_traffic WHERE (grain = 'minute' AND bucket < $1) OR (grain = 'hour' AND bucket < $2)`
	sweepReplicas = `DELETE FROM site_traffic_replicas WHERE (grain = 'minute' AND bucket < $1) OR (grain = 'hour' AND bucket < $2)`
)

// Sweep removes minute rows older than minuteBefore and hour rows older than
// hourBefore, and reports how many rows it removed.
func (s *Store) Sweep(ctx context.Context, minuteBefore, hourBefore time.Time) (int64, error) {
	var total int64
	for _, q := range []string{sweepTraffic, sweepReplicas} {
		res, err := s.DB.ExecContext(ctx, q, minuteBefore, hourBefore)
		if err != nil {
			return total, fmt.Errorf("sweep traffic: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

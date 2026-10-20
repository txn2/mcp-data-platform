// Package eprefusal counts the requests an endpoint refused before its
// handler ran (#2208): at the bot check, a rate limit, the body limit, the
// caller's credential, or a cross-origin preflight from an origin the binding
// does not list. An answered request is audited; a refused one was not, so a
// flood refused at the bot check, which is what an administrator looks for on
// a public endpoint, showed nowhere.
//
// A replica counts in memory and adds its counts to the database every few
// seconds, one row per endpoint, hour and reason: a flood moves a counter
// rather than writing a row per request, so counting refusals does not make
// refusing them cost more.
package eprefusal

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// The reasons a request is refused before its handler runs.
const (
	ReasonBotCheck        = "bot_check"
	ReasonRateLimit       = "rate_limit"
	ReasonCallerRateLimit = "caller_rate_limit"
	ReasonBodyLimit       = "body_limit"
	ReasonCredential      = "credential"
	ReasonOrigin          = "origin"
)

// Retention is how long the counts are kept.
const Retention = 30 * 24 * time.Hour

// DefaultFlush is how often a replica adds its counts.
const DefaultFlush = 10 * time.Second

// Count is how many requests to one endpoint were refused for one reason in
// one hour.
type Count struct {
	EndpointID string    `json:"-"`
	Hour       time.Time `json:"hour"`
	Reason     string    `json:"reason"`
	Count      int64     `json:"count"`
}

// Store keeps the counts.
type Store struct{ db *sql.DB }

// NewStore is the store over db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Add adds counts to the hours and reasons they are for, in one statement.
func (s *Store) Add(ctx context.Context, counts []Count) error {
	if len(counts) == 0 {
		return nil
	}
	ids, hours, reasons, ns := make([]string, len(counts)), make([]time.Time, len(counts)), make([]string, len(counts)), make([]int64, len(counts))
	for i, c := range counts {
		ids[i], hours[i], reasons[i], ns[i] = c.EndpointID, c.Hour, c.Reason, c.Count
	}
	// An endpoint deleted since the refusal has no row to count against; its
	// counts are dropped by the join rather than failing the batch.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO script_endpoint_refusals (endpoint_id, hour, reason, count)
		SELECT c.endpoint_id::uuid, c.hour, c.reason, c.n
		  FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::bigint[]) AS c(endpoint_id, hour, reason, n)
		  JOIN script_endpoints e ON e.id = c.endpoint_id::uuid
		ON CONFLICT (endpoint_id, hour, reason) DO UPDATE SET count = script_endpoint_refusals.count + EXCLUDED.count`,
		pq.Array(ids), pq.Array(hours), pq.Array(reasons), pq.Array(ns))
	if err != nil {
		return fmt.Errorf("adding endpoint refusals: %w", err)
	}
	return nil
}

// Since reads every endpoint's counts from since on, by hour and reason. An
// empty endpointID reads every endpoint's.
func (s *Store) Since(ctx context.Context, endpointID string, since time.Time) ([]Count, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT endpoint_id::text, hour, reason, count FROM script_endpoint_refusals
		 WHERE hour >= date_trunc('hour', $1::timestamptz) AND ($2 = '' OR endpoint_id::text = $2)
		 ORDER BY hour, reason`, since, endpointID)
	if err != nil {
		return nil, fmt.Errorf("reading endpoint refusals: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor; rows.Err below reports a failed read
	out := make([]Count, 0)
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.EndpointID, &c.Hour, &c.Reason, &c.Count); err != nil {
			return nil, fmt.Errorf("reading an endpoint refusal: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading endpoint refusals: %w", err)
	}
	return out, nil
}

// Sweep deletes the counts for hours before cutoff.
func (s *Store) Sweep(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM script_endpoint_refusals WHERE hour < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweeping endpoint refusals: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// adder is where a Counter adds its counts.
type adder interface {
	Add(ctx context.Context, counts []Count) error
}

// key is one counter.
type key struct {
	endpoint string
	hour     time.Time
	reason   string
}

// Counter counts this replica's refusals in memory and adds them to the store
// on its loop.
type Counter struct {
	store adder
	every time.Duration
	now   func() time.Time

	mu      sync.Mutex
	pending map[key]int64

	stop, done chan struct{}
}

// NewCounter counts into store, adding every flush (DefaultFlush when zero).
func NewCounter(store adder, flush time.Duration) *Counter {
	if flush <= 0 {
		flush = DefaultFlush
	}
	return &Counter{store: store, every: flush, now: time.Now, pending: map[key]int64{}}
}

// Refused counts one refused request. Nil-safe, and never blocks on the
// database.
func (c *Counter) Refused(endpointID, reason string) {
	if c == nil || endpointID == "" {
		return
	}
	k := key{endpoint: endpointID, hour: c.now().UTC().Truncate(time.Hour), reason: reason}
	c.mu.Lock()
	c.pending[k]++
	c.mu.Unlock()
}

// Flush adds what has been counted since the last flush. A batch the store
// refuses is put back, to be added on the next flush.
func (c *Counter) Flush(ctx context.Context) error {
	c.mu.Lock()
	batch := c.pending
	c.pending = map[key]int64{}
	c.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	counts := make([]Count, 0, len(batch))
	for k, n := range batch {
		counts = append(counts, Count{EndpointID: k.endpoint, Hour: k.hour, Reason: k.reason, Count: n})
	}
	if err := c.store.Add(ctx, counts); err != nil {
		c.mu.Lock()
		for k, n := range batch {
			c.pending[k] += n
		}
		c.mu.Unlock()
		return err //nolint:wrapcheck // the store names the write
	}
	return nil
}

// Start runs the flush loop, on a context ctx's ending does not stop: Stop
// does. A second call does nothing.
func (c *Counter) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop != nil {
		return nil
	}
	c.stop, c.done = make(chan struct{}), make(chan struct{})
	stop, done := c.stop, c.done
	go func() {
		defer close(done)
		bgloop.Run(context.WithoutCancel(ctx), bgloop.Loop{Name: bgloop.NameEndpointRefusals, Every: c.every, Stop: stop, Body: c.Flush})
	}()
	return nil
}

// Stop ends the loop and adds what is still counted, so a replica that stops
// loses no refusal it saw.
func (c *Counter) Stop(ctx context.Context) error {
	c.mu.Lock()
	stop, done := c.stop, c.done
	c.stop = nil
	c.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	if err := c.Flush(ctx); err != nil {
		slog.WarnContext(ctx, "endpoint refusals not recorded at shutdown", "error", logsan.SanitizeForLog(err.Error()))
	}
	return nil
}

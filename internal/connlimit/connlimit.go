// Package connlimit holds a connection to the number of calls its upstream
// can take at once (#2209): a connection's max_concurrent_calls, across every
// replica of the deployment.
//
// Some upstreams cannot serve parallel requests. Twelve runs of one script
// started together against such an endpoint all failed, and nothing on the
// platform could say "this upstream takes one request at a time". A limited
// connection's every request now takes a slot first and gives it back when
// its response has been read, whatever sent it: an interactive tool call, an
// export's page walk, a script's platform.call.
//
// A slot is a PostgreSQL session-level advisory lock, (connection, slot n),
// held on a pooled database connection for the length of the call, so the
// limit holds across replicas: an upstream that takes one request does not
// take three because the deployment runs three replicas. A call over the
// limit waits for a slot instead of failing, and the wait counts against the
// call's own deadline; a call whose deadline passes while it waits fails
// naming the limit and how long it waited. A deployment with no database
// holds the limit in this process alone.
package connlimit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// The wait between two looks for a free slot: short at first, since a slot
// held by a quick call frees quickly, and never longer than maxPoll, so a
// slot freed on another replica is taken within that.
const (
	firstPoll = 20 * time.Millisecond
	maxPoll   = 250 * time.Millisecond
	// releaseTimeout bounds giving a slot back, which runs after the call's
	// own context may have ended.
	releaseTimeout = 5 * time.Second
)

// keySpace is the first half of every slot's advisory-lock key. It is
// hashed from the connection, so two connections never share a slot, and it
// lives in the two-integer key form, a separate key space from the one
// bigint key the platform's other advisory locks take.
func keySpace(kind, connection string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte("connlimit\x00" + kind + "\x00" + connection))
	// #nosec G115 -- a hash folded into the lock's 32-bit key space: wrapping is the intent, and any value is a key
	return int32(h.Sum32())
}

// Limiter hands out a connection's slots.
type Limiter struct {
	db *sql.DB
	// local holds the limit in this process when there is no database.
	mu    sync.Mutex
	local map[string]chan struct{}
	// metrics records the calls in flight and each wait; nil records nothing.
	metrics func() *observability.Metrics
}

// New builds a Limiter over db. A nil db holds each limit in this process.
func New(db *sql.DB, metrics func() *observability.Metrics) *Limiter {
	if metrics == nil {
		metrics = func() *observability.Metrics { return nil }
	}
	return &Limiter{db: db, local: map[string]chan struct{}{}, metrics: metrics}
}

//nolint:gochecknoglobals // the one process-wide limiter, installed once the database is open, as outbound's recorder is
var installed atomic.Pointer[Limiter]

// Install makes l the limiter every limited transport takes its slots from.
// A transport built before Install takes them from it all the same: it reads
// the installed limiter on every request.
func Install(l *Limiter) { installed.Store(l) }

// Default is the installed limiter, or one that holds limits in this process
// until one is installed.
func Default() *Limiter {
	if l := installed.Load(); l != nil {
		return l
	}
	installed.CompareAndSwap(nil, New(nil, nil))
	return installed.Load()
}

// Slot is one held slot. Release gives it back; it is safe to call more than
// once.
type Slot struct {
	once    sync.Once
	release func()
}

// Release gives the slot back.
func (s *Slot) Release() {
	if s != nil {
		s.once.Do(s.release)
	}
}

// Acquire takes one of a connection's limit slots, waiting for one to free,
// until ctx ends. Waited is how long it waited.
func (l *Limiter) Acquire(ctx context.Context, kind, connection string, limit int) (*Slot, time.Duration, error) {
	start := time.Now()
	var (
		slot *Slot
		err  error
	)
	if l.db == nil {
		slot, err = l.acquireLocal(ctx, kind, connection, limit)
	} else {
		slot, err = l.acquireShared(ctx, kind, connection, limit)
	}
	waited := time.Since(start)
	if err != nil {
		return nil, waited, &WaitError{Connection: connection, Limit: limit, Waited: waited, Err: err}
	}
	m := l.metrics()
	m.RecordUpstreamSlotWait(ctx, kind, connection, waited)
	m.AddUpstreamInflight(ctx, kind, connection, 1)
	inner := slot.release
	slot.release = func() {
		inner()
		m.AddUpstreamInflight(context.Background(), kind, connection, -1)
	}
	return slot, waited, nil
}

// acquireShared takes a slot as an advisory lock, polling until one is free.
// A database connection is held only while a slot is: a waiting call holds
// none.
func (l *Limiter) acquireShared(ctx context.Context, kind, connection string, limit int) (*Slot, error) {
	space := keySpace(kind, connection)
	// One request's wait for a slot, bounded by the request's own context:
	// not a background loop, which is why it is not a bgloop.Run.
	for poll := firstPoll; ; poll = min(poll*2, maxPoll) {
		slot, err := l.tryShared(ctx, space, limit)
		if err != nil || slot != nil {
			return slot, err
		}
		if err := pause(ctx, poll); err != nil {
			return nil, err
		}
	}
}

// pause waits d, or until ctx ends, which it returns.
func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // WaitError names it
	case <-timer.C:
		return nil
	}
}

// tryShared takes the first free slot, or reports none free with a nil slot.
func (l *Limiter) tryShared(ctx context.Context, space int32, limit int) (*Slot, error) {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserving a database connection for the slot: %w", err)
	}
	for n := int32(0); int(n) < limit; n++ {
		var got bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, space, n).Scan(&got); err != nil {
			discard(conn)
			// A deadline reached during the query is the driver's cancel
			// error; the call's own end is what the caller is told.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr //nolint:wrapcheck // WaitError names it
			}
			return nil, fmt.Errorf("taking a slot: %w", err)
		}
		if got {
			return &Slot{release: func() { unlockShared(conn, space, n) }}, nil
		}
	}
	_ = conn.Close()
	return nil, nil //nolint:nilnil // no slot free is not a failure: the caller waits
}

// unlockShared gives a slot back and returns its database connection to the
// pool. A connection whose unlock failed may still hold the lock, so it is
// closed rather than pooled: a pooled connection holding a slot would hold it
// until the pool closed it.
func unlockShared(conn *sql.Conn, space, n int32) {
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	var released bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1, $2)`, space, n).Scan(&released); err != nil || !released {
		discard(conn)
		return
	}
	_ = conn.Close()
}

// discard closes conn and drops it from the pool, ending its session and with
// it any lock the session holds.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// acquireLocal takes a slot from this process's own count of the connection.
func (l *Limiter) acquireLocal(ctx context.Context, kind, connection string, limit int) (*Slot, error) {
	key := fmt.Sprintf("%s\x00%s\x00%d", kind, connection, limit)
	l.mu.Lock()
	sem, ok := l.local[key]
	if !ok {
		sem = make(chan struct{}, limit)
		l.local[key] = sem
	}
	l.mu.Unlock()
	select {
	case sem <- struct{}{}:
		return &Slot{release: func() { <-sem }}, nil
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck // WaitError names it
	}
}

// WaitError is a call that ended before a slot was free, or whose slot could
// not be taken. Its text names the limit and how long the call waited, which
// is what an author whose calls queue behind each other needs to read.
type WaitError struct {
	Connection string
	Limit      int
	Waited     time.Duration
	Err        error
}

func (e *WaitError) Error() string {
	waited := e.Waited.Round(time.Millisecond)
	if e.Waited >= time.Second {
		waited = e.Waited.Round(100 * time.Millisecond)
	}
	if errors.Is(e.Err, context.DeadlineExceeded) || errors.Is(e.Err, context.Canceled) {
		return fmt.Sprintf("waited %s for one of connection %q's %d call slots (max_concurrent_calls), and the call's time ran out first: %v",
			waited, e.Connection, e.Limit, e.Err)
	}
	return fmt.Sprintf("taking one of connection %q's %d call slots (max_concurrent_calls) failed after %s: %v",
		e.Connection, e.Limit, waited, e.Err)
}

func (e *WaitError) Unwrap() error { return e.Err }

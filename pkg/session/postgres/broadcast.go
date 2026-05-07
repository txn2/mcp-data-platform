// Package postgres provides PostgreSQL storage and pub/sub plumbing for
// the platform's session layer.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// DefaultNotifyChannel is the postgres LISTEN/NOTIFY channel name used
// by the platform for cross-replica MCP notification fan-out. Hard-coded
// rather than per-deployment so every replica that joins the same
// database immediately participates in the same event stream without
// extra config. The string is a postgres identifier (no quoting); keep
// it lowercase and ASCII.
const DefaultNotifyChannel = "mcp_notifications"

// listenerEventName maps pq.ListenerEventType to a human-readable
// label for logs. Centralized here so log consumers can grep events
// by name rather than the raw int value.
func listenerEventName(ev pq.ListenerEventType) string {
	switch ev {
	case pq.ListenerEventConnected:
		return "connected"
	case pq.ListenerEventDisconnected:
		return "disconnected"
	case pq.ListenerEventReconnected:
		return "reconnected"
	case pq.ListenerEventConnectionAttemptFailed:
		return "connection_attempt_failed"
	default:
		return fmt.Sprintf("unknown(%d)", int(ev))
	}
}

// listenerMinReconnect / listenerMaxReconnect bound the lib/pq
// Listener's exponential reconnect backoff. Min must be small enough
// that a transient connection drop reattaches before clients notice
// stale tool lists; max must be large enough that a sustained DB outage
// doesn't pin a CPU. These match the values lib/pq uses in its own
// usage examples and are well-exercised in production.
const (
	listenerMinReconnect = 10 * time.Second
	listenerMaxReconnect = time.Minute
)

// notifyPayload is the JSON body we attach to each pg_notify call.
// Mirrors session.Event but with explicit JSON tags so external
// consumers (psql, ops scripts) can read it. Keep the field set
// minimal; payloads >8 KB are rejected by postgres outright.
type notifyPayload struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// Broadcaster is a postgres-backed session.Broadcaster. Every active
// replica LISTENs on the same channel and re-publishes received
// notifications to its local in-memory subscribers, so a tool-list
// change on any replica fan-outs to all SSE long-poll clients across
// the cluster.
//
// Wire model:
//
//   - Publish encodes the Event as JSON and issues
//     `SELECT pg_notify($1, $2)`. No local short-circuit — every
//     replica (including the publisher) hears the event via its own
//     LISTEN connection, so behavior is symmetric and there is no
//     missed-delivery edge case when a publisher goes down between
//     local fan-out and the NOTIFY round-trip.
//   - The background goroutine reads pq.Listener.NotificationChannel()
//     and forwards each Notification to the embedded local
//     MemoryBroadcaster. Local SSE subscribers receive events exactly
//     as if a same-process Publish had fired.
type Broadcaster struct {
	listener *pq.Listener
	db       *sql.DB
	channel  string
	local    *session.MemoryBroadcaster
	done     chan struct{}
	closed   atomic.Bool
	logger   *slog.Logger
}

// NewBroadcaster builds a postgres-backed broadcaster bound to the
// given channel. dsn is the libpq connection string used by the
// dedicated LISTEN connection; db is the sql.DB used for outbound
// pg_notify calls (re-using the platform's connection pool keeps
// connection counts predictable).
//
// On success the returned broadcaster has already issued LISTEN and
// the background goroutine is running. Returns an error if LISTEN
// fails — typically a missing privilege on the role or a syntactically
// invalid channel name. Does NOT block on initial connectivity to
// postgres beyond the LISTEN command itself.
func NewBroadcaster(dsn string, db *sql.DB, channel string, logger *slog.Logger) (*Broadcaster, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if channel == "" {
		channel = DefaultNotifyChannel
	}
	listener := pq.NewListener(dsn, listenerMinReconnect, listenerMaxReconnect,
		func(ev pq.ListenerEventType, err error) {
			// Log all transitions, not just errors — operators need a
			// signal when the LISTEN connection drops or reconnects so
			// a flapping link is visible. lib/pq emits Connected at
			// startup, Disconnected on a drop, Reconnected on recovery,
			// ConnectionAttemptFailed for retries.
			//
			// Use Warn/Info directly rather than logger.Log so static
			// analysis (CodeQL go/clear-text-logging) sees an explicit
			// level binding instead of a dynamic-level Log call. The
			// underlying err here is the pq.Listener's transport error
			// (typically "pq: ..." or "dial tcp: ..." messages, not
			// the raw DSN); operators with strict requirements about
			// log content should run gateway logs through their
			// existing redaction pipeline regardless of this claim.
			if err != nil {
				logger.Warn("session/broadcast/postgres: listener event",
					"event", listenerEventName(ev), "error", err)
			} else {
				logger.Info("session/broadcast/postgres: listener event",
					"event", listenerEventName(ev))
			}
		})
	if err := listener.Listen(channel); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("listen %s: %w", channel, err)
	}
	b := &Broadcaster{
		listener: listener,
		db:       db,
		channel:  channel,
		local:    session.NewMemoryBroadcaster(logger),
		done:     make(chan struct{}),
		logger:   logger,
	}
	go b.run()
	return b, nil
}

// run is the background goroutine that forwards received notifications
// to local subscribers. Exits when Close is called (the listener
// channel is drained then the goroutine returns). Survives transient
// notification-channel disconnects — lib/pq buffers and reconnects
// internally, so we just keep reading.
func (b *Broadcaster) run() {
	defer close(b.done)
	ch := b.listener.NotificationChannel()
	for {
		n, ok := <-ch
		if !ok {
			return
		}
		if n == nil {
			// lib/pq sends nil on reconnect to signal "you may have
			// missed events". For tools/list_changed we don't care:
			// every client will catch up on its next list call.
			continue
		}
		b.dispatchPayload(n.Extra)
	}
}

// dispatchPayload decodes a single NOTIFY payload and forwards it to
// local subscribers. Extracted from run() so tests can drive the
// fan-out path without spinning up a real postgres listener.
func (b *Broadcaster) dispatchPayload(extra string) {
	var p notifyPayload
	if err := json.Unmarshal([]byte(extra), &p); err != nil {
		b.logger.Warn("session/broadcast/postgres: invalid payload",
			"channel", b.channel, "error", err)
		return
	}
	if err := b.local.Publish(context.Background(), session.Event{
		Method: p.Method,
		Params: p.Params,
	}); err != nil {
		b.logger.Warn("session/broadcast/postgres: local publish failed",
			"method", p.Method, "error", err)
	}
}

// Subscribe registers a local in-process SSE subscriber. The
// subscription receives every event NOTIFYed on the channel by any
// replica (including this one).
//
// Honors the Broadcaster contract: after Close starts, Subscribe
// returns a closed-immediate subscription. Without this short-circuit,
// the ~2s Close drain window between b.closed.CompareAndSwap and
// b.local.Close would let Subscribe hand back a live subscription
// while Publish already returns ErrBroadcasterClosed — a paired-
// backend asymmetry against MemoryBroadcaster, which gates Subscribe
// on its own closed flag in the same step.
func (b *Broadcaster) Subscribe(ctx context.Context, sessionID string) session.Subscription {
	if b.closed.Load() {
		// Build a closed-immediate sub via the local broadcaster.
		// b.local may not be closed yet (Close hasn't reached
		// local.Close), but we still want the same observable shape:
		// channel closed, Events() yields nothing.
		return closedSubscription()
	}
	return b.local.Subscribe(ctx, sessionID)
}

// closedSubscription returns a Subscription whose Events channel is
// already closed — the canonical "after Close" return shape.
func closedSubscription() session.Subscription {
	return &postgresClosedSub{}
}

// alreadyClosedEventChan is a single, persistent closed channel shared
// across every postgresClosedSub.Events() call. Caching avoids
// allocating-and-closing a fresh channel per call. Note this is a
// postgres-backend-specific optimization: MemoryBroadcaster's
// closed-immediate path allocates a fresh closed channel per
// Subscribe (each memorySub owns its own ch); the two backends agree
// on observable behavior (Events() yields no values; Close() is a
// no-op) but not on channel-pointer identity across calls.
var alreadyClosedEventChan = func() chan session.Event {
	c := make(chan session.Event)
	close(c)
	return c
}()

// postgresClosedSub is a minimal Subscription that delivers no events.
// Equivalent in shape to MemoryBroadcaster's closed-immediate sub,
// without dragging the memorySub internals across the package boundary.
type postgresClosedSub struct{}

// Events returns the package-level pre-closed channel so callers ranging
// over the result exit immediately. See alreadyClosedEventChan for the
// caching rationale.
func (*postgresClosedSub) Events() <-chan session.Event {
	return alreadyClosedEventChan
}

// Close is a no-op — the closed-immediate sub holds no per-instance
// resources to release.
func (*postgresClosedSub) Close() {}

// Publish encodes the event and issues pg_notify. Returns
// ErrBroadcasterClosed after Close. Surfacing pg_notify errors to
// callers is intentional — they pick whether to retry; we do not.
//
// Latency note: there is no local short-circuit. Even the publishing
// replica's local subscribers receive the event only after the
// pg_notify round-trip + LISTEN dispatch. Under normal postgres
// latency this is sub-millisecond; under DB pressure it can stretch
// into 100s of ms. The gateway's notifyDispatchTimeout (5s) bounds
// the worst case so a partitioned DB cannot leak goroutines, but
// callers expecting same-process delivery should use MemoryBroadcaster
// or accept the round-trip cost as a deliberate trade-off for
// cross-replica symmetry (no missed-delivery edge cases when a
// publisher dies between local fan-out and the NOTIFY round-trip).
//
// Listener-reconnect window: pg_notify uses the platform's shared
// db pool (outbound), but local subscribers receive events through
// the dedicated pq.Listener connection. During a Listener reconnect
// (Disconnected → ConnectionAttemptFailed* → Reconnected, up to
// listenerMaxReconnect = 60s), Publish can succeed while the
// publisher's own subscribers miss the event. lib/pq emits a nil
// notification on reconnect to signal "you may have missed events";
// for tools/list_changed this is harmless because the next list
// call catches up, but callers that need at-least-once delivery
// would need to track a sequence number across the gap.
func (b *Broadcaster) Publish(ctx context.Context, ev session.Event) error {
	if b.closed.Load() {
		return session.ErrBroadcasterClosed
	}
	body, err := json.Marshal(notifyPayload{Method: ev.Method, Params: ev.Params})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	// Postgres limits NOTIFY messages to 8000 bytes total — including
	// the channel name and per-message framing overhead, not just the
	// payload. We cap at 7500 bytes of payload to give comfortable
	// headroom for any channel name we might use, plus the small
	// per-message bookkeeping the server adds. Drop oversize payloads
	// with a clear error so the caller knows to shrink rather than
	// silently NOOPing.
	const maxNotifyBytes = 7500
	if len(body) > maxNotifyBytes {
		return fmt.Errorf("payload too large for pg_notify: %d bytes (max ~%d)", len(body), maxNotifyBytes)
	}
	if _, err := b.db.ExecContext(ctx, "SELECT pg_notify($1, $2)", b.channel, string(body)); err != nil {
		return fmt.Errorf("pg_notify: %w", err)
	}
	return nil
}

// Close stops the background goroutine, closes the local broadcaster
// (releasing every subscriber), and tears down the listen connection.
// Idempotent. Returns the first non-nil error encountered.
func (b *Broadcaster) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	var firstErr error
	// listener may be nil in tests that exercise the close-ordering
	// path without a real pq.Listener. Production always supplies one
	// (NewBroadcaster fails fast if listener.Listen errors), so the
	// nil guard is dead code in real deployments — but it lets tests
	// drive Close() end-to-end without faking the entire pq surface.
	if b.listener != nil {
		if err := b.listener.Close(); err != nil {
			firstErr = fmt.Errorf("close listener: %w", err)
		}
	}
	// Drain the run() goroutine — listener.Close closes the
	// notification channel which makes the loop exit. Bounded so a
	// pq.Listener that's mid-reconnect-sleep (up to listenerMaxReconnect
	// ~60s) can't pin Platform.Close past the orchestrator's container
	// grace period; if we time out the goroutine outlives this call,
	// but the local broadcaster is still cleaned up below.
	select {
	case <-b.done:
	case <-time.After(2 * time.Second):
		b.logger.Warn("session/broadcast/postgres: listener drain timed out — proceeding with shutdown")
	}
	if err := b.local.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close local: %w", err)
	}
	return firstErr
}

// SubscriberCount mirrors MemoryBroadcaster.SubscriberCount for tests.
func (b *Broadcaster) SubscriberCount() int { return b.local.SubscriberCount() }

// ensure interface compliance at compile time without exporting an
// unused symbol.
var _ session.Broadcaster = (*Broadcaster)(nil)

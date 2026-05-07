package session

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Event is a server-originated MCP notification (a JSON-RPC notification
// that has no id and expects no response). The broadcaster delivers
// these to every connected SSE long-poll subscriber.
//
// Method is the JSON-RPC method, e.g. "notifications/tools/list_changed".
// Params is the JSON-RPC params object — usually a small map with no
// payload (the method name itself is the signal). Pass nil when the
// notification carries no payload; the SSE writer will emit "{}".
type Event struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// Subscription is a per-subscriber stream of Events. Subscribers MUST
// call Close when they no longer want events; failing to do so would
// leak the underlying buffered channel and a slot in the broadcaster's
// fan-out map.
type Subscription interface {
	// Events returns the channel events are delivered on. The channel is
	// closed by Close, after which receives yield zero values.
	Events() <-chan Event
	// Close releases the subscription. Safe to call more than once.
	Close()
}

// Broadcaster fans out server-originated MCP notifications to every
// active SSE long-poll subscriber attached to the platform's
// session-aware HTTP handler.
//
// Implementations are concurrency-safe. Publish must not block on a
// slow subscriber — buffer-full subscribers are dropped (with a
// warning) so a single misbehaving downstream client cannot stall the
// event pipeline for everyone else.
type Broadcaster interface {
	// Subscribe registers a new subscriber and returns the subscription.
	// ctx cancellation closes the subscription automatically. The
	// sessionID is recorded for log attribution only — events are
	// fan-out broadcast to every subscriber regardless of sessionID.
	// (tools/list_changed today is a server-wide signal, so per-
	// session targeting is intentionally not implemented; if a future
	// notification needs per-session delivery, add an Event.SessionID
	// filter at Publish time and document the contract change here.)
	Subscribe(ctx context.Context, sessionID string) Subscription
	// Publish delivers ev to every active subscription. Best-effort:
	// returns nil even when individual subscribers are dropped due to
	// buffer overflow. The error path is reserved for unrecoverable
	// transport faults (e.g., the underlying postgres LISTEN connection
	// is closed) so callers can decide whether to retry or surface.
	Publish(ctx context.Context, ev Event) error
	// Close releases any background resources. After Close, Subscribe
	// returns a closed-immediate subscription and Publish returns
	// ErrBroadcasterClosed.
	Close() error
}

// ErrBroadcasterClosed is returned by Publish on a Broadcaster that has
// been Closed. Callers should drop the event and avoid retry loops.
var ErrBroadcasterClosed = errors.New("session: broadcaster closed")

// defaultEventBufferSize bounds the per-subscriber backlog. A single
// subscriber that stops draining cannot soak up unbounded memory;
// further events are dropped with slog.Warn so the operator can spot
// the slow client. 32 is enough to absorb a normal burst (gateway
// re-registers all upstream tools on startup) without growing memory
// noticeably under thousands of concurrent sessions.
const defaultEventBufferSize = 32

// MemoryBroadcaster is the in-memory Broadcaster implementation. It
// holds every active subscriber's channel under a single mutex and
// fan-outs each Publish synchronously across all of them. Suitable for
// single-replica deployments where all SSE long-poll clients hit the
// same process. Multi-replica deployments must wrap or compose this
// with a Postgres LISTEN/NOTIFY broadcaster (pkg/session/postgres).
type MemoryBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[*memorySub]struct{}
	closed      atomic.Bool
	logger      *slog.Logger
}

// NewMemoryBroadcaster builds a fresh in-memory broadcaster. Pass a
// non-nil logger to capture slow-subscriber warnings; nil falls back
// to slog.Default().
func NewMemoryBroadcaster(logger *slog.Logger) *MemoryBroadcaster {
	if logger == nil {
		logger = slog.Default()
	}
	return &MemoryBroadcaster{
		subscribers: make(map[*memorySub]struct{}),
		logger:      logger,
	}
}

// Subscribe registers a new subscriber. The returned Subscription is
// always non-nil even when the broadcaster is already closed — in that
// case the channel is closed immediately so callers' range loops exit
// without special-casing the closed state.
func (b *MemoryBroadcaster) Subscribe(ctx context.Context, sessionID string) Subscription {
	sub := &memorySub{
		ch:        make(chan Event, defaultEventBufferSize),
		done:      make(chan struct{}),
		sessionID: sessionID,
		broker:    b,
	}
	if b.closed.Load() {
		close(sub.ch)
		// Close sub.done in the same step so the Events()/Close()
		// observable contract is consistent: after Subscribe returns,
		// both channels are in their final closed state and a later
		// sub.Close() (whose CAS will fail because closedFlag is
		// already set) is a clean no-op rather than a partial cleanup.
		close(sub.done)
		sub.closedFlag.Store(true)
		return sub
	}
	// Re-check `closed` UNDER the lock to close the TOCTOU race
	// against Close: the atomic CAS in Close happens before Close
	// acquires b.mu and nils b.subscribers, so without this second
	// check a concurrent Close could leave us about to write into a
	// nil map and panic.
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		close(sub.ch)
		close(sub.done)
		sub.closedFlag.Store(true)
		return sub
	}
	b.subscribers[sub] = struct{}{}
	b.mu.Unlock()

	// Auto-close on ctx cancellation so HTTP handlers don't have to
	// wire an explicit defer Close. Selecting on sub.done as well
	// lets explicit Close() (with a non-cancelable ctx like
	// context.Background()) reap the goroutine — without the done
	// channel the goroutine would leak forever.
	go func() {
		select {
		case <-ctx.Done():
			sub.Close()
		case <-sub.done:
		}
	}()
	return sub
}

// Publish fans out ev to every active subscriber. Best-effort delivery
// per the Broadcaster contract — slow subscribers (full channel) are
// dropped with slog.Warn rather than blocking the publisher.
func (b *MemoryBroadcaster) Publish(_ context.Context, ev Event) error {
	if b.closed.Load() {
		return ErrBroadcasterClosed
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subscribers {
		select {
		case sub.ch <- ev:
		default:
			b.logger.Warn("session/broadcast: subscriber buffer full — event dropped",
				sessionIDKey, sub.sessionID, "method", ev.Method)
		}
	}
	return nil
}

// Close releases every subscriber's channel and marks the broadcaster
// as closed. Subsequent Publish calls return ErrBroadcasterClosed and
// subsequent Subscribe calls return a closed-immediate subscription.
// Safe to call more than once.
func (b *MemoryBroadcaster) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subscribers {
		sub.closeLocked()
	}
	b.subscribers = nil
	return nil
}

// SubscriberCount returns the number of active subscribers. Used by
// tests and by the postgres bridge to decide whether to bother
// republishing a received NOTIFY locally.
func (b *MemoryBroadcaster) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// memorySub is the MemoryBroadcaster-bound Subscription. Tracks the
// owning broker so Close can deregister itself; closedFlag avoids a
// double-close panic on the channel; done releases the ctx-watcher
// goroutine when Close is called explicitly.
type memorySub struct {
	ch         chan Event
	done       chan struct{}
	sessionID  string
	broker     *MemoryBroadcaster
	closedFlag atomic.Bool
}

// Events returns the receive channel.
func (s *memorySub) Events() <-chan Event { return s.ch }

// Close removes this subscription from the broker and closes the
// channel. Idempotent. Releases the ctx-watcher goroutine spawned in
// Subscribe by closing s.done.
func (s *memorySub) Close() {
	if !s.closedFlag.CompareAndSwap(false, true) {
		return
	}
	close(s.done)
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	delete(s.broker.subscribers, s)
	close(s.ch)
}

// closeLocked is Close without acquiring the broker lock. Used by
// Broadcaster.Close which already holds the lock.
func (s *memorySub) closeLocked() {
	if !s.closedFlag.CompareAndSwap(false, true) {
		return
	}
	close(s.done)
	close(s.ch)
}

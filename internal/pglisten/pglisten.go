// Package pglisten is the LISTEN half of a PostgreSQL LISTEN/NOTIFY wakeup:
// one goroutine holding a dedicated connection that wakes registered workers
// whenever a producer fires NOTIFY on the channel it watches.
//
// Every durable queue in this codebase is a table a worker polls, and every one
// of them wants the same thing on top of the poll: a producer's write should
// wake a worker now rather than at the next tick. The mechanism is identical
// across queues — only the channel name differs — so it lives here once, with
// the channel as a parameter, rather than being copied per queue. The
// notification delivery queue and the managed-script run queue both use it.
//
// A listener is an optimization, never a correctness requirement: every worker
// it wakes also polls, so a failed or absent listener degrades latency and
// nothing else.
package pglisten

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
)

// Notifier is the worker hook a listener fires on every received NOTIFY. A
// worker's Notify method implements it; the one-method interface keeps this
// package independent of the workers it wakes and keeps it testable with a
// fake.
type Notifier interface {
	Notify()
}

// logKeyChannel is the structured-logging key for the watched channel.
const logKeyChannel = "channel"

// backoffFloor / backoffCeiling bound pq.NewListener's exponential
// reconnect schedule.
const (
	backoffFloor   = 10 * time.Second
	backoffCeiling = time.Minute
)

// pqListener is the part of *pq.Listener a Listener drives, so the start and
// stop interleavings can be exercised without a server.
type pqListener interface {
	Listen(channel string) error
	Close() error
	NotificationChannel() <-chan *pq.Notification
}

// newPQListener opens pq's listener; tests replace it.
var newPQListener = func(dsn string, onEvent pq.EventCallbackType) pqListener {
	return pq.NewListener(dsn, backoffFloor, backoffCeiling, onEvent)
}

// Listener wakes its notifiers on every NOTIFY received on one channel.
type Listener struct {
	dsn       string
	channel   string
	notifiers []Notifier
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	started   atomic.Bool
	state     *bgloop.Listen

	// mu guards listener and stopped, so Stop reaches a connection Start is
	// still waiting on and Start never begins receiving after Stop.
	mu       sync.Mutex
	listener pqListener
	stopped  bool
}

// New constructs a Listener for the supplied DSN and channel. It does not
// connect until Start is called. Its connection reports on the pg_listen_*
// series as "listen_" + channel; the channels are the platform's own
// constants, which bounds the label.
func New(dsn, channel string, notifiers ...Notifier) *Listener {
	return &Listener{
		dsn: dsn, channel: channel, notifiers: notifiers, stopCh: make(chan struct{}),
		state: bgloop.NewListen(bgloop.ListenPrefix + channel),
	}
}

// Start opens the LISTEN connection and spawns the receive goroutine. An
// error means the woken workers degrade to their poll interval; the caller
// decides whether that is fatal (it should not be).
//
// pq's Listen waits until a connection exists, so against a database that
// never answers Start does not return on its own. Stop ends that wait: Start
// then returns net.ErrClosed, which is also what a Start after Stop returns.
func (l *Listener) Start(_ context.Context) error {
	if !l.started.CompareAndSwap(false, true) {
		return nil
	}
	pl := newPQListener(l.dsn, l.onEvent)
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		_ = pl.Close()
		return net.ErrClosed
	}
	l.listener = pl
	l.mu.Unlock()
	if err := pl.Listen(l.channel); err != nil {
		_ = pl.Close()
		l.mu.Lock()
		l.listener = nil
		l.mu.Unlock()
		l.started.Store(false)
		return err //nolint:wrapcheck // direct return so callers can errors.Is the underlying pq error
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return net.ErrClosed
	}
	l.wg.Add(1)
	go l.run(pl.NotificationChannel()) // #nosec G118 -- background goroutine bounded by stopCh
	return nil
}

// Stop closes the LISTEN connection, ends a Start still waiting for one, and
// waits for the receive goroutine. No connection event is logged after Stop
// returns. pq's own reconnect goroutine may outlive it by one backoff sleep
// before it sees the close and exits.
func (l *Listener) Stop() {
	l.stopOnce.Do(func() {
		l.mu.Lock()
		l.stopped = true
		close(l.stopCh)
		pl := l.listener
		l.mu.Unlock()
		if pl != nil {
			_ = pl.Close()
		}
	})
	l.wg.Wait()
}

func (l *Listener) run(ch <-chan *pq.Notification) {
	defer l.wg.Done()
	l.consume(ch)
}

// consume is the receive loop, taking the channel as a parameter so it can be
// driven without a live PostgreSQL connection.
func (l *Listener) consume(ch <-chan *pq.Notification) {
	// A nil notification signals a reconnect ("you may have missed
	// events"); either way wake every notifier to re-query.
	bgloop.Consume(context.Background(), bgloop.Events[*pq.Notification]{
		Name: l.state.Name(), C: ch, Stop: l.stopCh, SpanPerUnit: true,
		Body: func(_ context.Context, n *pq.Notification) error {
			if n != nil {
				// A nil is the reconnect signal, not a notification.
				l.state.Notified()
			}
			l.broadcast()
			return nil
		},
	})
}

func (l *Listener) broadcast() {
	for _, n := range l.notifiers {
		n.Notify()
	}
}

// onEvent logs pq.Listener lifecycle changes for operator visibility; the
// listener reconnects on its own.
func (l *Listener) onEvent(ev pq.ListenerEventType, err error) {
	select {
	case <-l.stopCh:
		return // pq's goroutine outlives Stop by a backoff sleep; it reports nothing more
	default:
	}
	l.state.Event(ev)
	switch ev {
	case pq.ListenerEventConnected:
		slog.Info("pglisten: listener connected", logKeyChannel, l.channel)
	case pq.ListenerEventDisconnected:
		slog.Warn("pglisten: listener disconnected", logKeyChannel, l.channel, "error", err)
	case pq.ListenerEventReconnected:
		slog.Info("pglisten: listener reconnected", logKeyChannel, l.channel)
	case pq.ListenerEventConnectionAttemptFailed:
		slog.Warn("pglisten: listener connect attempt failed", logKeyChannel, l.channel, "error", err)
	}
}

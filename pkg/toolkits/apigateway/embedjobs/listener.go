package embedjobs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
)

// notifier is the worker hook the listener notifies on every
// received NOTIFY. Worker.Notify implements this; declaring it
// as a one-method interface lets the listener be tested with a
// fake without pulling in the full Worker type.
type notifier interface {
	Notify()
}

// listenerBackoffMin / Max bound pq.NewListener's exponential
// reconnect schedule. Same constants the session broadcaster
// uses for the same reasons (fast first reconnect after a
// transient drop; cap the worst-case sleep on a long outage).
const (
	listenerBackoffMin = 10 * time.Second
	listenerBackoffMax = time.Minute
)

// Listener is the LISTEN-side of the LISTEN/NOTIFY adapter.
// Producers issue NOTIFY in Store.Enqueue; this goroutine
// receives the notifications and calls Worker.Notify so the
// worker drops out of its poll wait immediately.
//
// The listener is intentionally separate from the Worker: a
// pod running multiple Workers can share one Listener (saving
// a pq connection) by registering multiple notifiers. A pod
// running zero Workers but still receiving job notifications
// (read-only admin replicas) can run the listener alone.
type Listener struct {
	dsn       string
	channel   string
	notifiers []notifier
	listener  *pq.Listener
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	started   atomic.Bool
}

// NewListener constructs a Listener for the supplied DSN. The
// listener does not connect until Start is called; constructing
// it does no I/O.
func NewListener(dsn, channel string, notifiers ...notifier) *Listener {
	if channel == "" {
		channel = NotifyChannel
	}
	return &Listener{
		dsn:       dsn,
		channel:   channel,
		notifiers: notifiers,
		stopCh:    make(chan struct{}),
	}
}

// Start opens the LISTEN connection and spawns the receive
// goroutine. Returns nil on success. Errors here block startup
// because a missing notification path silently regresses
// embedding latency from "immediate" to "up to PollEvery."
func (l *Listener) Start(_ context.Context) error {
	if !l.started.CompareAndSwap(false, true) {
		return nil
	}
	pl := pq.NewListener(l.dsn, listenerBackoffMin, listenerBackoffMax, l.onEvent)
	if err := pl.Listen(l.channel); err != nil {
		_ = pl.Close()
		l.started.Store(false)
		return err //nolint:wrapcheck // direct return so callers can errors.Is the underlying pq error
	}
	l.listener = pl
	l.wg.Add(1)
	go l.run() //#nosec G118
	return nil
}

// Stop closes the LISTEN connection and waits for the receive
// goroutine to drain.
func (l *Listener) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopCh)
		if l.listener != nil {
			_ = l.listener.Close()
		}
	})
	l.wg.Wait()
}

func (l *Listener) run() {
	defer l.wg.Done()
	ch := l.listener.NotificationChannel()
	for {
		select {
		case <-l.stopCh:
			return
		case n := <-ch:
			if n == nil {
				// pq.Listener emits nil on a reconnect to
				// signal "you may have missed events." Wake
				// every notifier so they re-query the table.
				l.broadcast()
				continue
			}
			l.broadcast()
		}
	}
}

func (l *Listener) broadcast() {
	for _, n := range l.notifiers {
		n.Notify()
	}
}

// onEvent logs pq.Listener lifecycle changes. Non-fatal: the
// listener reconnects on its own; we just want operator
// visibility into "the LISTEN connection bounced 12 times this
// hour."
func (*Listener) onEvent(ev pq.ListenerEventType, err error) {
	switch ev {
	case pq.ListenerEventConnected:
		slog.Info("embedjobs: listener connected")
	case pq.ListenerEventDisconnected:
		slog.Warn("embedjobs: listener disconnected", "error", err)
	case pq.ListenerEventReconnected:
		slog.Info("embedjobs: listener reconnected")
	case pq.ListenerEventConnectionAttemptFailed:
		slog.Warn("embedjobs: listener connect attempt failed", "error", err)
	}
}

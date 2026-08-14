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
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
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

// Listener wakes its notifiers on every NOTIFY received on one channel.
type Listener struct {
	dsn       string
	channel   string
	notifiers []Notifier
	listener  *pq.Listener
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	started   atomic.Bool
}

// New constructs a Listener for the supplied DSN and channel. It does not
// connect until Start is called.
func New(dsn, channel string, notifiers ...Notifier) *Listener {
	return &Listener{dsn: dsn, channel: channel, notifiers: notifiers, stopCh: make(chan struct{})}
}

// Start opens the LISTEN connection and spawns the receive goroutine. An
// error means the woken workers degrade to their poll interval; the caller
// decides whether that is fatal (it should not be).
func (l *Listener) Start(_ context.Context) error {
	if !l.started.CompareAndSwap(false, true) {
		return nil
	}
	pl := pq.NewListener(l.dsn, backoffFloor, backoffCeiling, l.onEvent)
	if err := pl.Listen(l.channel); err != nil {
		_ = pl.Close()
		l.started.Store(false)
		return err //nolint:wrapcheck // direct return so callers can errors.Is the underlying pq error
	}
	l.listener = pl
	l.wg.Add(1)
	go l.run() // #nosec G118 -- background goroutine bounded by stopCh
	return nil
}

// Stop closes the LISTEN connection and waits for the receive goroutine.
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
	l.consume(l.listener.NotificationChannel())
}

// consume is the receive loop, taking the channel as a parameter so it can be
// driven without a live PostgreSQL connection.
func (l *Listener) consume(ch <-chan *pq.Notification) {
	for {
		select {
		case <-l.stopCh:
			return
		case <-ch:
			// A nil notification signals a reconnect ("you may have missed
			// events"); either way wake every notifier to re-query.
			l.broadcast()
		}
	}
}

func (l *Listener) broadcast() {
	for _, n := range l.notifiers {
		n.Notify()
	}
}

// onEvent logs pq.Listener lifecycle changes for operator visibility; the
// listener reconnects on its own.
func (l *Listener) onEvent(ev pq.ListenerEventType, err error) {
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

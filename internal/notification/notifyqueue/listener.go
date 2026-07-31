package notifyqueue

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
)

// notifier is the worker hook the listener fires on every received NOTIFY.
// notifyworker.Worker.Notify implements it; the one-method interface both keeps
// the listener testable with a fake and keeps this package from depending on
// the worker it wakes.
type notifier interface {
	Notify()
}

// listenerBackoffFloor / Ceiling bound pq.NewListener's exponential
// reconnect schedule.
const (
	listenerBackoffFloor   = 10 * time.Second
	listenerBackoffCeiling = time.Minute
)

// Listener is the LISTEN side of the queue's LISTEN/NOTIFY adapter.
// Enqueue fires NOTIFY; this goroutine receives it and wakes the worker so
// immediate notifications go out without waiting for the poll interval.
type Listener struct {
	dsn       string
	notifiers []notifier
	listener  *pq.Listener
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	started   atomic.Bool
}

// NewListener constructs a Listener for the supplied DSN. It does not
// connect until Start is called.
func NewListener(dsn string, notifiers ...notifier) *Listener {
	return &Listener{dsn: dsn, notifiers: notifiers, stopCh: make(chan struct{})}
}

// Start opens the LISTEN connection and spawns the receive goroutine. An
// error means delivery latency degrades to the worker's poll interval; the
// caller decides whether that is fatal.
func (l *Listener) Start(_ context.Context) error {
	if !l.started.CompareAndSwap(false, true) {
		return nil
	}
	pl := pq.NewListener(l.dsn, listenerBackoffFloor, listenerBackoffCeiling, l.onEvent)
	if err := pl.Listen(NotifyChannel); err != nil {
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
	ch := l.listener.NotificationChannel()
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
func (*Listener) onEvent(ev pq.ListenerEventType, err error) {
	switch ev {
	case pq.ListenerEventConnected:
		slog.Info("notification: listener connected")
	case pq.ListenerEventDisconnected:
		slog.Warn("notification: listener disconnected", "error", err)
	case pq.ListenerEventReconnected:
		slog.Info("notification: listener reconnected")
	case pq.ListenerEventConnectionAttemptFailed:
		slog.Warn("notification: listener connect attempt failed", "error", err)
	}
}

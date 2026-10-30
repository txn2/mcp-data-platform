package pglisten

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// countingNotifier records Notify calls.
type countingNotifier struct{ ch chan struct{} }

func (c *countingNotifier) Notify() {
	select {
	case c.ch <- struct{}{}:
	default:
	}
}

func TestPGListen_Broadcast(t *testing.T) {
	n1 := &countingNotifier{ch: make(chan struct{}, 1)}
	n2 := &countingNotifier{ch: make(chan struct{}, 1)}
	l := New("dsn-unused", "probe-channel", n1, n2)

	l.broadcast()

	for i, n := range []*countingNotifier{n1, n2} {
		select {
		case <-n.ch:
		case <-time.After(time.Second):
			t.Fatalf("notifier %d not woken", i)
		}
	}
}

func TestPGListen_StopWithoutStart(_ *testing.T) {
	l := New("dsn-unused", "probe-channel")
	l.Stop() // must not panic or hang
	l.Stop() // idempotent
}

func TestPGListen_OnEvent(_ *testing.T) {
	// Pure logging callback: exercise every lifecycle branch.
	l := New("dsn-unused", "probe-channel")
	for _, ev := range []pq.ListenerEventType{
		pq.ListenerEventConnected,
		pq.ListenerEventDisconnected,
		pq.ListenerEventReconnected,
		pq.ListenerEventConnectionAttemptFailed,
	} {
		l.onEvent(ev, errors.New("probe"))
	}
}

// TestPGListen_ConsumeWakesOnEveryNotification drives the receive loop without
// a live PostgreSQL connection: every notification, including the nil one that
// signals a reconnect, wakes the workers.
func TestPGListen_ConsumeWakesOnEveryNotification(t *testing.T) {
	// Room for both wakes: consume can broadcast twice before the first is
	// read, and a one-slot notifier drops the second (Notify never blocks).
	n := &countingNotifier{ch: make(chan struct{}, 2)}
	l := New("dsn-unused", "probe-channel", n)
	notifications := make(chan *pq.Notification, 2)
	notifications <- &pq.Notification{Channel: "probe-channel"}
	notifications <- nil

	done := make(chan struct{})
	go func() {
		l.consume(notifications)
		close(done)
	}()

	for i := range 2 {
		select {
		case <-n.ch:
		case <-time.After(time.Second):
			t.Fatalf("notification %d did not wake the notifier", i)
		}
	}
	l.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consume did not return after Stop")
	}
}

// TestPGListen_AReconnectIsNotANotification: the nil lib/pq sends on a
// reconnect wakes the workers but is not recorded as a notification, so a
// connection that only ever reconnects shows no notification at all.
func TestPGListen_AReconnectIsNotANotification(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	bgloop.SetDefaultMetrics(m)
	t.Cleanup(func() { bgloop.SetDefaultMetrics(nil) })

	n := &countingNotifier{ch: make(chan struct{}, 1)}
	l := New("dsn-unused", "probe-channel", n)
	notifications := make(chan *pq.Notification, 1)
	notifications <- nil
	done := make(chan struct{})
	go func() {
		l.consume(notifications)
		close(done)
	}()
	select {
	case <-n.ch:
	case <-time.After(time.Second):
		t.Fatal("the reconnect did not wake the notifier")
	}
	l.Stop()
	<-done

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	if strings.Contains(rec.Body.String(), "pg_listen_last_notification_age_seconds") {
		t.Error("a reconnect was recorded as a notification")
	}
}

// syncLog is a buffer the default logger writes to from pq's goroutine while
// the test reads it.
type syncLog struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	seen chan struct{}
}

func (s *syncLog) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bytes.Contains(p, []byte("connect attempt failed")) {
		select {
		case s.seen <- struct{}{}:
		default:
		}
	}
	return s.buf.Write(p) //nolint:wrapcheck // bytes.Buffer.Write never fails
}

func (s *syncLog) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// unreachableDSN names a port nothing listens on, so every connect fails.
const unreachableDSN = "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"

// TestPGListen_StopEndsAStartThatNeverConnected holds #2374: against a
// database that never answers, pq's Listen waits for a connection, and Stop
// ends that wait. Start returns net.ErrClosed, and a connection event pq
// reports after Stop is not logged.
func TestPGListen_StopEndsAStartThatNeverConnected(t *testing.T) {
	logs := &syncLog{seen: make(chan struct{}, 1)}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	l := New(unreachableDSN, "probe-channel")
	started := make(chan error, 1)
	go func() { started <- l.Start(context.Background()) }()

	select {
	case <-logs.seen:
	case <-time.After(10 * time.Second):
		t.Fatal("pq never reported a failed connect attempt")
	}
	l.Stop()
	select {
	case err := <-started:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Start = %v, want net.ErrClosed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start still waiting after Stop")
	}

	before := logs.String()
	l.onEvent(pq.ListenerEventConnectionAttemptFailed, errors.New("after stop"))
	l.onEvent(pq.ListenerEventConnected, nil)
	if after := logs.String(); after != before {
		t.Errorf("a connection event was logged after Stop:\n%s", strings.TrimPrefix(after, before))
	}
}

// TestPGListen_StartAfterStopIsRefused: a stopped listener never opens a
// connection.
func TestPGListen_StartAfterStopIsRefused(t *testing.T) {
	l := New(unreachableDSN, "probe-channel")
	l.Stop()
	if err := l.Start(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Start after Stop = %v, want net.ErrClosed", err)
	}
}

// fakePQ stands in for pq's listener: Listen returns once connected is closed
// (running onListen first, when set) or once Close is called.
type fakePQ struct {
	connected chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	notify    chan *pq.Notification
	onListen  func()
}

func newFakePQ() *fakePQ {
	return &fakePQ{connected: make(chan struct{}), closed: make(chan struct{}), notify: make(chan *pq.Notification, 1)}
}

func (f *fakePQ) Listen(string) error {
	if f.onListen != nil {
		f.onListen()
	}
	select {
	case <-f.connected:
		return nil
	case <-f.closed:
		return net.ErrClosed
	}
}

func (f *fakePQ) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakePQ) NotificationChannel() <-chan *pq.Notification { return f.notify }

// useFakePQ makes every Listener in the test open fake.
func useFakePQ(t *testing.T, fake *fakePQ) {
	t.Helper()
	prev := newPQListener
	newPQListener = func(string, pq.EventCallbackType) pqListener { return fake }
	t.Cleanup(func() { newPQListener = prev })
}

// TestPGListen_AConnectedListenerWakesItsNotifiersUntilStopped: once the
// connection is up, a notification wakes the workers, and Stop closes the
// connection and ends the receive loop.
func TestPGListen_AConnectedListenerWakesItsNotifiersUntilStopped(t *testing.T) {
	fake := newFakePQ()
	close(fake.connected)
	useFakePQ(t, fake)
	n := &countingNotifier{ch: make(chan struct{}, 1)}
	l := New("dsn-unused", "probe-channel", n)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start = %v", err)
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("a second Start = %v, want nil", err)
	}
	fake.notify <- &pq.Notification{Channel: "probe-channel"}
	select {
	case <-n.ch:
	case <-time.After(10 * time.Second):
		t.Fatal("the notification did not wake the notifier")
	}
	l.Stop()
	select {
	case <-fake.closed:
	default:
		t.Fatal("Stop left the connection open")
	}
}

// TestPGListen_AStopWhileTheConnectionComesUpStartsNoReceiver: a Stop that
// lands while Listen is returning leaves Start refusing, with nothing
// receiving and the connection closed.
func TestPGListen_AStopWhileTheConnectionComesUpStartsNoReceiver(t *testing.T) {
	fake := newFakePQ()
	useFakePQ(t, fake)
	l := New("dsn-unused", "probe-channel")
	fake.onListen = func() {
		l.Stop()
		close(fake.connected)
	}
	if err := l.Start(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Start = %v, want net.ErrClosed", err)
	}
	select {
	case <-fake.closed:
	default:
		t.Fatal("the connection Stop met was left open")
	}
}

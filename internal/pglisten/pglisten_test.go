package pglisten

import (
	"errors"
	"testing"
	"time"

	"github.com/lib/pq"
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
	n := &countingNotifier{ch: make(chan struct{}, 1)}
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

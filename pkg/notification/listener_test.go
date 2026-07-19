package notification

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

func TestListener_Broadcast(t *testing.T) {
	n1 := &countingNotifier{ch: make(chan struct{}, 1)}
	n2 := &countingNotifier{ch: make(chan struct{}, 1)}
	l := NewListener("dsn-unused", n1, n2)

	l.broadcast()

	for i, n := range []*countingNotifier{n1, n2} {
		select {
		case <-n.ch:
		case <-time.After(time.Second):
			t.Fatalf("notifier %d not woken", i)
		}
	}
}

func TestListener_StopWithoutStart(_ *testing.T) {
	l := NewListener("dsn-unused")
	l.Stop() // must not panic or hang
	l.Stop() // idempotent
}

func TestListener_OnEvent(_ *testing.T) {
	// Pure logging callback: exercise every lifecycle branch.
	l := NewListener("dsn-unused")
	for _, ev := range []pq.ListenerEventType{
		pq.ListenerEventConnected,
		pq.ListenerEventDisconnected,
		pq.ListenerEventReconnected,
		pq.ListenerEventConnectionAttemptFailed,
	} {
		l.onEvent(ev, errors.New("probe"))
	}
}

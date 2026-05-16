package embedjobs

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lib/pq"
)

// fakeNotifier counts Notify calls so tests can assert the
// listener fans events out to all registered subscribers.
type fakeNotifier struct {
	count atomic.Int32
}

func (n *fakeNotifier) Notify() { n.count.Add(1) }

// TestNewListener_DefaultsChannelWhenEmpty proves the
// constructor falls back to the package-level NotifyChannel
// constant when the caller passes the empty string.
func TestNewListener_DefaultsChannelWhenEmpty(t *testing.T) {
	t.Parallel()
	l := NewListener("postgres://example", "", &fakeNotifier{})
	if l.channel != NotifyChannel {
		t.Errorf("channel = %q; want %q", l.channel, NotifyChannel)
	}
}

// TestNewListener_HonorsExplicitChannel proves a non-empty
// channel argument overrides the default.
func TestNewListener_HonorsExplicitChannel(t *testing.T) {
	t.Parallel()
	l := NewListener("postgres://example", "alt_channel", &fakeNotifier{})
	if l.channel != "alt_channel" {
		t.Errorf("channel = %q; want %q", l.channel, "alt_channel")
	}
}

// TestListenerBroadcast_FansOutToAllNotifiers proves the
// LISTEN side calls Notify on every registered subscriber.
// Used when a single pod runs multiple Workers sharing one
// LISTEN connection.
func TestListenerBroadcast_FansOutToAllNotifiers(t *testing.T) {
	t.Parallel()
	n1, n2 := &fakeNotifier{}, &fakeNotifier{}
	l := NewListener("postgres://example", "", n1, n2)
	l.broadcast()
	if n1.count.Load() != 1 || n2.count.Load() != 1 {
		t.Errorf("notify counts: n1=%d n2=%d; want 1 each", n1.count.Load(), n2.count.Load())
	}
}

// TestListenerStop_BeforeStart proves Stop is safe on an
// un-started listener. The lifecycle wiring may call Stop
// during teardown even when Start failed; that path must not
// panic.
func TestListenerStop_BeforeStart(_ *testing.T) {
	l := NewListener("postgres://example", "", &fakeNotifier{})
	l.Stop()
}

// TestListenerStop_Idempotent proves repeated Stop calls are
// safe. The lifecycle's deferred stop + an explicit shutdown
// in another goroutine both end up here.
func TestListenerStop_Idempotent(_ *testing.T) {
	l := NewListener("postgres://example", "", &fakeNotifier{})
	l.Stop()
	l.Stop()
}

// TestListenerOnEvent_LogsLifecycleStates exercises the
// callback for each pq.ListenerEventType. The callback is
// log-only; this test asserts it does not panic on any of the
// known event types or on an unknown sentinel.
func TestListenerOnEvent_LogsLifecycleStates(_ *testing.T) {
	var l Listener
	l.onEvent(pq.ListenerEventConnected, nil)
	l.onEvent(pq.ListenerEventDisconnected, errors.New("dropped"))
	l.onEvent(pq.ListenerEventReconnected, nil)
	l.onEvent(pq.ListenerEventConnectionAttemptFailed, errors.New("refused"))
	// Sentinel for the default branch of the type switch.
	l.onEvent(pq.ListenerEventType(99), nil)
}

// TestWorker_NotifierContractCompiles ensures *Worker still
// satisfies the package-internal notifier interface so the
// Listener can take Workers as notifiers without reflection.
func TestListener_AcceptsWorkerAsNotifier(_ *testing.T) {
	var _ notifier = (*Worker)(nil)
}

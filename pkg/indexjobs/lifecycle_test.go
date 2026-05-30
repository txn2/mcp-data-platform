package indexjobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
)

// sweepStore records reaper sweeps and reconciler enqueues so the
// background-loop tests can assert the loop fired at least once. It
// embeds noopStore for the methods the loops do not drive.
type sweepStore struct {
	noopStore
	mu       sync.Mutex
	released atomic.Int32
	releaseN int
	releErr  error
	enqueued []Key
	enqErr   error
}

func (s *sweepStore) Enqueue(_ context.Context, k Key, _ Trigger) (bool, error) {
	if s.enqErr != nil {
		return false, s.enqErr
	}
	s.mu.Lock()
	s.enqueued = append(s.enqueued, k)
	s.mu.Unlock()
	return true, nil
}

func (s *sweepStore) ReleaseExpiredLeases(context.Context) (int, error) {
	s.released.Add(1)
	return s.releaseN, s.releErr
}

func (s *sweepStore) enqueuedKeys() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Key(nil), s.enqueued...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestReaper_SweepsOnStart(t *testing.T) {
	t.Parallel()
	store := &sweepStore{releaseN: 2}
	r := NewReaper(store, 10*time.Millisecond)
	r.Start(context.Background())
	defer r.Stop()
	waitFor(t, func() bool { return store.released.Load() >= 1 })
}

func TestReaper_SweepErrorDoesNotPanic(t *testing.T) {
	t.Parallel()
	store := &sweepStore{releErr: errors.New("db down")}
	r := NewReaper(store, 10*time.Millisecond)
	r.Start(context.Background())
	defer r.Stop()
	waitFor(t, func() bool { return store.released.Load() >= 1 })
}

func TestReconciler_EnqueuesGapsFromRegisteredSinks(t *testing.T) {
	t.Parallel()
	store := &sweepStore{}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"u1", "u2"}})
	rc := NewReconciler(store, reg, 10*time.Millisecond)
	rc.Start(context.Background())
	defer rc.Stop()
	waitFor(t, func() bool { return len(store.enqueuedKeys()) >= 2 })
	keys := store.enqueuedKeys()
	for _, k := range keys[:2] {
		if k.SourceKind != "k" {
			t.Errorf("enqueued kind = %q; want k", k.SourceKind)
		}
	}
}

func TestReconciler_StopIsIdempotent(t *testing.T) {
	t.Parallel()
	rc := NewReconciler(&sweepStore{}, NewRegistry(), time.Second)
	rc.Start(context.Background())
	rc.Stop()
	rc.Stop() // second Stop must not panic or block
}

func TestNewListener_DefaultsChannel(t *testing.T) {
	t.Parallel()
	l := NewListener("dsn", "", &Worker{wakeup: make(chan struct{}, 1)})
	if l.channel != NotifyChannel {
		t.Errorf("empty channel should default to %q; got %q", NotifyChannel, l.channel)
	}
}

func TestListener_BroadcastNotifiesAll(t *testing.T) {
	t.Parallel()
	w1 := &Worker{wakeup: make(chan struct{}, 1)}
	w2 := &Worker{wakeup: make(chan struct{}, 1)}
	l := NewListener("dsn", "ch", w1, w2)
	l.broadcast()
	for i, w := range []*Worker{w1, w2} {
		select {
		case <-w.wakeup:
		default:
			t.Errorf("worker %d was not notified by broadcast", i)
		}
	}
}

func TestComputeBackoffSeconds(t *testing.T) {
	t.Parallel()
	cases := map[int]int{0: 5, 1: 10, 2: 20, 3: 40, -1: 5}
	for attempts, want := range cases {
		if got := computeBackoffSeconds(attempts); got != want {
			t.Errorf("computeBackoffSeconds(%d) = %d; want %d", attempts, got, want)
		}
	}
	// Corrupt-column guard: a huge attempts value is capped, not a
	// multi-century backoff.
	if got := computeBackoffSeconds(1000); got != 5*(1<<maxBackoffShift) {
		t.Errorf("computeBackoffSeconds(1000) = %d; want capped", got)
	}
}

func TestListener_OnEventAllTypes(t *testing.T) {
	t.Parallel()
	var l Listener
	// Each event type just logs; assert none panic.
	l.onEvent(pq.ListenerEventConnected, nil)
	l.onEvent(pq.ListenerEventDisconnected, errors.New("drop"))
	l.onEvent(pq.ListenerEventReconnected, nil)
	l.onEvent(pq.ListenerEventConnectionAttemptFailed, errors.New("refused"))
}

func TestListener_StopWithoutStartIsSafe(t *testing.T) {
	t.Parallel()
	l := NewListener("dsn", "ch")
	l.Stop() // never started; must not panic or block
}

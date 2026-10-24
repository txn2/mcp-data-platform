package user

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeObserveStore records Observe calls and signals each on a channel so the
// async Directory.Observe goroutine can be awaited deterministically.
type fakeObserveStore struct {
	mu     sync.Mutex
	calls  [][3]string // email, first, last
	roles  [][]string  // the role set recorded alongside each call
	failN  int         // fail the first failN calls
	signal chan struct{}
}

func newFakeObserveStore() *fakeObserveStore {
	return &fakeObserveStore{signal: make(chan struct{}, 16)}
}

func (f *fakeObserveStore) Observe(_ context.Context, email, first, last string, roles []string) error {
	f.mu.Lock()
	f.calls = append(f.calls, [3]string{email, first, last})
	f.roles = append(f.roles, roles)
	fail := f.failN > 0
	if fail {
		f.failN--
	}
	f.mu.Unlock()
	f.signal <- struct{}{}
	if fail {
		return errors.New("boom")
	}
	return nil
}

func (f *fakeObserveStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// Unused Store methods (Directory only calls Observe).
func (*fakeObserveStore) Insert(context.Context, User) error                { return nil }
func (*fakeObserveStore) Get(context.Context, string) (*User, error)        { return nil, ErrNotFound }
func (*fakeObserveStore) List(context.Context, Filter) ([]User, int, error) { return nil, 0, nil }
func (*fakeObserveStore) Update(context.Context, string, Update) error      { return nil }
func (*fakeObserveStore) Delete(context.Context, string) error              { return nil }

func waitForSignal(t *testing.T, f *fakeObserveStore) {
	t.Helper()
	select {
	case <-f.signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async Observe")
	}
}

func TestDirectory_Observe_WritesOnce(t *testing.T) {
	f := newFakeObserveStore()
	d := NewDirectory(f)

	d.Observe("A@B.io", "Marcus", "Johnson", nil)
	waitForSignal(t, f)

	// Second call within the TTL is throttled — no new write.
	d.Observe("a@b.io", "Marcus", "Johnson", nil)
	time.Sleep(50 * time.Millisecond)

	if got := f.count(); got != 1 {
		t.Fatalf("expected exactly 1 write, got %d", got)
	}
	if f.calls[0][0] != "a@b.io" {
		t.Errorf("email not normalized: %q", f.calls[0][0])
	}
}

func TestDirectory_Observe_SkipsInvalidEmail(t *testing.T) {
	f := newFakeObserveStore()
	d := NewDirectory(f)

	d.Observe("", "X", "Y", nil)
	d.Observe("not-an-email", "X", "Y", nil)
	time.Sleep(50 * time.Millisecond)

	if got := f.count(); got != 0 {
		t.Fatalf("expected no writes for invalid emails, got %d", got)
	}
}

func TestDirectory_Observe_NoRetryStormOnError(t *testing.T) {
	f := newFakeObserveStore()
	f.failN = 1
	d := NewDirectory(f)

	// First write fails. The throttle entry is kept (not dropped), so a second
	// Observe within the TTL must NOT spawn an immediate retry — otherwise a
	// database outage would turn every authentication into a write storm.
	d.Observe("a@b.io", "Marcus", "Johnson", nil)
	waitForSignal(t, f)

	d.Observe("a@b.io", "Marcus", "Johnson", nil)
	time.Sleep(50 * time.Millisecond)

	if got := f.count(); got != 1 {
		t.Fatalf("expected exactly 1 write (no retry storm), got %d", got)
	}
}

func TestDirectory_Observe_SanitizesNames(t *testing.T) {
	f := newFakeObserveStore()
	d := NewDirectory(f)

	d.Observe("a@b.io", "  Mar\ncus\t", "Johnson\x00", nil)
	waitForSignal(t, f)

	if f.calls[0][1] != "Marcus" || f.calls[0][2] != "Johnson" {
		t.Fatalf("names not sanitized: %q / %q", f.calls[0][1], f.calls[0][2])
	}
}

func TestDirectory_PruneExpired(t *testing.T) {
	d := NewDirectory(newFakeObserveStore())
	d.ttl = 10 * time.Millisecond

	d.shouldWrite("old@b.io")
	time.Sleep(20 * time.Millisecond)
	d.pruneExpired(time.Now())

	d.mu.Lock()
	_, present := d.seen["old@b.io"]
	d.mu.Unlock()
	if present {
		t.Error("expected expired throttle entry to be pruned")
	}
}

func TestDirectory_Observe_NilSafe(_ *testing.T) {
	var d *Directory
	d.Observe("a@b.io", "X", "Y", nil) // must not panic

	d2 := &Directory{} // zero value, nil store
	d2.Observe("a@b.io", "X", "Y", nil)
}

// Record writes before it returns and reports the store's failure, and leaves
// the throttle alone so the next Observe still writes (#2279).
func TestDirectory_Record_WritesBeforeReturning(t *testing.T) {
	store := newFakeObserveStore()
	d := NewDirectory(store)
	if err := d.Record(context.Background(), " Jane@Example.com ", "Jane", "Doe", []string{"analyst"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if store.count() != 1 || store.calls[0][0] != "jane@example.com" {
		t.Fatalf("calls %v; want one write for jane@example.com before Record returned", store.calls)
	}
	<-store.signal
	d.Observe("jane@example.com", "Jane", "Doe", []string{"analyst"})
	waitForSignal(t, store)
	if store.count() != 2 {
		t.Errorf("Observe after Record wrote %d time(s) in all; Record must not throttle it", store.count())
	}

	store.failN = 1
	if err := d.Record(context.Background(), "jane@example.com", "", "", nil); err == nil {
		t.Error("a failed store write was not reported")
	}
	if err := d.Record(context.Background(), "not an address", "", "", nil); err == nil {
		t.Error("an invalid address was accepted")
	}
	var none *Directory
	if err := none.Record(context.Background(), "jane@example.com", "", "", nil); err != nil {
		t.Errorf("a nil directory: %v", err)
	}
}

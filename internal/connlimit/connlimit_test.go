package connlimit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLocal_HoldsTheLimitAndWaits is the in-process half: a third call on a
// limit of two waits until a slot is given back.
func TestLocal_HoldsTheLimitAndWaits(t *testing.T) {
	l := New(nil, nil)
	ctx := context.Background()
	a, _, err := l.Acquire(ctx, "graphql", "erp", 2)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := l.Acquire(ctx, "graphql", "erp", 2)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *Slot)
	go func() {
		s, _, err := l.Acquire(ctx, "graphql", "erp", 2)
		if err != nil {
			t.Error(err)
		}
		got <- s
	}()
	select {
	case <-got:
		t.Fatal("a third call took a slot while two were held")
	case <-time.After(50 * time.Millisecond):
	}
	a.Release()
	c := <-got
	b.Release()
	c.Release()
	c.Release() // giving a slot back twice is harmless

	// Another connection has slots of its own.
	other, _, err := l.Acquire(ctx, "graphql", "crm", 1)
	if err != nil {
		t.Fatal(err)
	}
	other.Release()
}

// TestWaitError_NamesTheLimitAndTheWait: a call whose time runs out while it
// waits says which limit held it and for how long.
func TestWaitError_NamesTheLimitAndTheWait(t *testing.T) {
	l := New(nil, nil)
	held, _, err := l.Acquire(context.Background(), "api", "billing", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, waited, err := l.Acquire(ctx, "api", "billing", 1)
	var we *WaitError
	if !errors.As(err, &we) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if waited < 30*time.Millisecond {
		t.Errorf("waited %v", waited)
	}
	for _, want := range []string{`connection "billing"'s 1 call slots (max_concurrent_calls)`, "the call's time ran out first", "waited "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err.Error(), want)
		}
	}
	other := (&WaitError{Connection: "c", Limit: 2, Waited: time.Second, Err: errors.New("db down")}).Error()
	if !strings.Contains(other, "failed after 1s: db down") {
		t.Errorf("a failure to take a slot = %q", other)
	}
}

func TestDefault_IsLocalUntilInstalled(t *testing.T) {
	prev := installed.Load()
	t.Cleanup(func() { installed.Store(prev) })
	installed.Store(nil)
	if d := Default(); d == nil || d.db != nil {
		t.Fatal("the default before an install holds limits in this process")
	}
	l := New(nil, nil)
	Install(l)
	if Default() != l {
		t.Fatal("Install did not take")
	}
}

// TestTransport_NeverSendsMoreThanTheLimit sends many requests at once
// through a limited client and counts what the upstream saw in flight.
func TestTransport_NeverSendsMoreThanTheLimit(t *testing.T) {
	var inFlight, most atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	client := &http.Client{Transport: Transport(http.DefaultTransport, "api", "one-at-a-time-"+t.Name(), 1)}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			resp, err := send(t, client, http.MethodGet, srv.URL)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			_, _ = io.ReadAll(resp.Body)
		})
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("the upstream saw %d calls at once; the limit is 1", most.Load())
	}
}

// TestTransport_TheWaitCountsAgainstTheCallsTimeout: the client's own
// timeout ends a call waiting for a slot, and the error says so.
func TestTransport_TheWaitCountsAgainstTheCallsTimeout(t *testing.T) {
	release, arrived := make(chan struct{}), make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	defer close(release)

	conn := "slow-" + t.Name()
	holder := &http.Client{Transport: Transport(http.DefaultTransport, "graphql", conn, 1)}
	go func() {
		resp, err := send(t, holder, http.MethodGet, srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-arrived // the first call is at the upstream, holding the slot

	waiter := &http.Client{Transport: Transport(http.DefaultTransport, "graphql", conn, 1), Timeout: 50 * time.Millisecond}
	resp, err := send(t, waiter, http.MethodGet, srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	// The client's timeout ends the wait, whichever of net/http's own timer
	// and the request's deadline reports it; what the caller reads is the
	// limit and the wait.
	if err == nil || !strings.Contains(err.Error(), "max_concurrent_calls") || !strings.Contains(err.Error(), "the call's time ran out first") {
		t.Fatalf("err = %v", err)
	}
}

// TestTransport_ASlotIsGivenBackOnEveryPath: an error, a bodiless answer and
// a body read to its end each give the slot back.
func TestTransport_ASlotIsGivenBackOnEveryPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.WriteString(w, "body")
	}))
	defer srv.Close()
	conn := "paths-" + t.Name()
	client := &http.Client{Transport: Transport(http.DefaultTransport, "api", conn, 1), Timeout: time.Second}

	head, err := send(t, client, http.MethodHead, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = head.Body.Close()
	read, err := send(t, client, http.MethodGet, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Read to EOF and not closed until the test ends: the slot is back
	// already, which the calls below need.
	defer func() { _ = read.Body.Close() }()
	_, _ = io.ReadAll(read.Body)
	if dead, err := send(t, client, http.MethodGet, "http://127.0.0.1:1"); err == nil {
		_ = dead.Body.Close()
		t.Fatal("expected a dial failure")
	}
	last, err := send(t, client, http.MethodGet, srv.URL)
	if err != nil {
		t.Fatalf("a slot was not given back: %v", err)
	}
	_ = last.Body.Close()
}

// send is one request through client, under the test's context.
func send(t *testing.T, client *http.Client, method, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending %s to %s: %w", method, url, err)
	}
	return resp, nil
}

func TestTransport_UnlimitedIsTheBase(t *testing.T) {
	base := http.DefaultTransport
	if Transport(base, "api", "c", 0) != base {
		t.Error("a limit of zero wraps nothing")
	}
	if _, ok := Transport(nil, "api", "c", 1).(*limited); !ok {
		t.Error("a nil base is the default transport, limited")
	}
}

func TestParse(t *testing.T) {
	for raw, want := range map[any]int{nil: 0, 3: 3, int64(4): 4, float64(5): 5} {
		if got, err := Parse(raw); err != nil || got != want {
			t.Errorf("Parse(%v) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []any{"1", 1.5, true} {
		if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "max_concurrent_calls must be a whole number") {
			t.Errorf("Parse(%v) = %v; want a refusal", raw, err)
		}
	}
}

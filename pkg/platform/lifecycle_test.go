package platform

import (
	"context"
	"errors"
	"testing"
)

const testLifecycleStopCount = 3

func TestLifecycle_StartAndStop(t *testing.T) {
	lc := NewLifecycle()

	var started, stopped bool
	lc.OnStart(func(_ context.Context) error {
		started = true
		return nil
	})
	lc.OnStop(func(_ context.Context) error {
		stopped = true
		return nil
	})

	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !started {
		t.Error("start callback not called")
	}
	if !lc.IsStarted() {
		t.Error("IsStarted() = false after Start()")
	}

	if err := lc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !stopped {
		t.Error("stop callback not called")
	}
	if lc.IsStarted() {
		t.Error("IsStarted() = true after Stop()")
	}
}

func TestLifecycle_StartAlreadyStarted(t *testing.T) {
	lc := NewLifecycle()
	_ = lc.Start(context.Background())

	err := lc.Start(context.Background())
	if err == nil {
		t.Error("Start() expected error for already started")
	}
}

func TestLifecycle_StopNotStarted(t *testing.T) {
	lc := NewLifecycle()
	err := lc.Stop(context.Background())
	if err != nil {
		t.Errorf("Stop() error = %v, expected nil for not started", err)
	}
}

func TestLifecycle_StartRollbackOnError(t *testing.T) {
	lc := NewLifecycle()

	var calls []string
	lc.OnStart(func(_ context.Context) error {
		calls = append(calls, "start1")
		return nil
	})
	lc.OnStop(func(_ context.Context) error {
		calls = append(calls, "stop1")
		return nil
	})
	lc.OnStart(func(_ context.Context) error {
		calls = append(calls, "start2")
		return errors.New("start2 failed")
	})
	lc.OnStop(func(_ context.Context) error {
		calls = append(calls, "stop2")
		return nil
	})

	err := lc.Start(context.Background())
	if err == nil {
		t.Error("Start() expected error")
	}

	// Should have called stop1 to rollback start1
	found := false
	for _, c := range calls {
		if c == "stop1" {
			found = true
		}
	}
	if !found {
		t.Error("expected rollback to call stop1")
	}
}

func TestLifecycle_StopInReverseOrder(t *testing.T) {
	lc := NewLifecycle()

	var order []int
	lc.OnStop(func(_ context.Context) error {
		order = append(order, 1)
		return nil
	})
	lc.OnStop(func(_ context.Context) error {
		order = append(order, 2)
		return nil
	})
	lc.OnStop(func(_ context.Context) error {
		order = append(order, 3)
		return nil
	})

	_ = lc.Start(context.Background())
	_ = lc.Stop(context.Background())

	// Should be in reverse order: 3, 2, 1
	expected := []int{testLifecycleStopCount, 2, 1}
	if len(order) != len(expected) {
		t.Fatalf("order length = %d, want %d", len(order), len(expected))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %d, want %d", i, order[i], v)
		}
	}
}

type mockComponent struct {
	started bool
	stopped bool
}

func (m *mockComponent) Start(_ context.Context) error {
	m.started = true
	return nil
}

func (m *mockComponent) Stop(_ context.Context) error {
	m.stopped = true
	return nil
}

func TestLifecycle_RegisterComponent(t *testing.T) {
	lc := NewLifecycle()
	comp := &mockComponent{}

	lc.RegisterComponent(comp)

	_ = lc.Start(context.Background())
	if !comp.started {
		t.Error("component not started")
	}

	_ = lc.Stop(context.Background())
	if !comp.stopped {
		t.Error("component not stopped")
	}
}

type mockCloser struct {
	closed bool
}

func (m *mockCloser) Close() error {
	m.closed = true
	return nil
}

func TestLifecycle_RegisterCloser(t *testing.T) {
	lc := NewLifecycle()
	closer := &mockCloser{}

	lc.RegisterCloser(closer)
	_ = lc.Start(context.Background())
	_ = lc.Stop(context.Background())

	if !closer.closed {
		t.Error("closer not closed")
	}
}

func TestLifecycle_RollbackWithStopError(t *testing.T) {
	lc := NewLifecycle()

	lc.OnStart(func(_ context.Context) error { return nil })
	lc.OnStop(func(_ context.Context) error {
		return errors.New("stop1 failed")
	})
	lc.OnStart(func(_ context.Context) error {
		return errors.New("start2 failed")
	})
	lc.OnStop(func(_ context.Context) error { return nil })

	err := lc.Start(context.Background())
	if err == nil {
		t.Fatal("Start() expected error")
	}
	// Rollback called stop1 which returned an error — should be logged, not panic.
	if lc.IsStarted() {
		t.Error("lifecycle should not be started after rollback")
	}
}

func TestLifecycle_RollbackSkipsNilStopCallback(t *testing.T) {
	lc := NewLifecycle()

	// Register start/stop pairs where one stop callback is nil.
	lc.OnStart(func(_ context.Context) error { return nil })
	lc.OnStop(nil) // nil stop callback — should be skipped in rollback

	lc.OnStart(func(_ context.Context) error { return nil })
	lc.OnStop(func(_ context.Context) error { return nil })

	// Third start fails → triggers rollback of callbacks 0 and 1.
	lc.OnStart(func(_ context.Context) error {
		return errors.New("start3 failed")
	})
	lc.OnStop(func(_ context.Context) error { return nil })

	err := lc.Start(context.Background())
	if err == nil {
		t.Fatal("Start() expected error")
	}
	if lc.IsStarted() {
		t.Error("lifecycle should not be started after rollback")
	}
}

// TestLifecycle_RollbackWithMismatchedRegistrations is the regression test
// for #755: a component registering only OnStart (or only OnStop) used to
// shift the positional pairing between the two callback slices, so rollback
// either indexed out of range or stopped the wrong component. With paired
// registration the rollback is structural.
func TestLifecycle_RollbackWithMismatchedRegistrations(t *testing.T) {
	t.Parallel()
	lc := NewLifecycle()

	// Start-only component (no stop half) shifts nothing anymore.
	lc.OnStart(func(_ context.Context) error { return nil })

	// Paired component whose stop must run during rollback.
	var pairedStopped bool
	lc.OnComponent(
		func(_ context.Context) error { return nil },
		func(_ context.Context) error { pairedStopped = true; return nil },
	)

	// Stop-only component (a Closer): closed during rollback too.
	var closerStopped bool
	lc.OnStop(func(_ context.Context) error { closerStopped = true; return nil })

	// This component's own stop must NOT run: its start failed.
	var failedStopped bool
	lc.OnComponent(
		func(_ context.Context) error { return errors.New("boom") },
		func(_ context.Context) error { failedStopped = true; return nil },
	)

	if err := lc.Start(context.Background()); err == nil {
		t.Fatal("Start() expected error")
	}
	if !pairedStopped {
		t.Error("paired component's stop must run during rollback")
	}
	if !closerStopped {
		t.Error("stop-only component must be torn down during rollback")
	}
	if failedStopped {
		t.Error("failed component's own stop must not run")
	}
	if lc.IsStarted() {
		t.Error("lifecycle should not be started after rollback")
	}
}

// TestLifecycle_OnComponentAfterStarted mirrors the late-wiring behavior of
// OnStart: the start half fires immediately, and the stop half still runs
// at shutdown.
func TestLifecycle_OnComponentAfterStarted(t *testing.T) {
	t.Parallel()
	lc := NewLifecycle()
	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	var started, stopped bool
	lc.OnComponent(
		func(_ context.Context) error { started = true; return nil },
		func(_ context.Context) error { stopped = true; return nil },
	)
	if !started {
		t.Error("late-registered start must fire immediately")
	}
	if err := lc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if !stopped {
		t.Error("late-registered stop must run at shutdown")
	}
}

func TestLifecycle_StopWithError(t *testing.T) {
	lc := NewLifecycle()

	lc.OnStop(func(_ context.Context) error {
		return errors.New("stop error")
	})

	_ = lc.Start(context.Background())
	err := lc.Stop(context.Background())
	if err == nil {
		t.Error("Stop() expected error when callback fails")
	}
}

// TestLifecycle_OnStartAfterStarted_FiresImmediately is the
// regression test for v1.62.0's "embed jobs never run" bug.
// Before the fix, OnStart called after Start silently dropped
// the callback. The fix invokes the callback immediately.
//
// Production trigger: internal/server/server.go calls
// platform.Start() inside platform.New's caller, then
// cmd/mcp-data-platform/main.go's startHTTPServer wires the
// embed-job worker via WireAPIGatewayEmbedJobsFromDB which
// registers OnStart. That callback must fire even though the
// lifecycle is already started.
func TestLifecycle_OnStartAfterStarted_FiresImmediately(t *testing.T) {
	t.Parallel()
	lc := NewLifecycle()
	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	called := false
	lc.OnStart(func(_ context.Context) error {
		called = true
		return nil
	})
	if !called {
		t.Fatal("late-registered OnStart callback was not invoked")
	}
}

// TestLifecycle_OnStartBeforeStarted_DeferredUntilStart proves
// the pre-Start path still defers (does not eagerly invoke).
func TestLifecycle_OnStartBeforeStarted_DeferredUntilStart(t *testing.T) {
	t.Parallel()
	lc := NewLifecycle()

	called := false
	lc.OnStart(func(_ context.Context) error {
		called = true
		return nil
	})
	if called {
		t.Fatal("pre-Start callback should not have fired yet")
	}
	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !called {
		t.Fatal("pre-Start callback never fired after Start")
	}
}

// TestLifecycle_OnStartAfterStarted_ErrorIsLogged confirms a
// failing late-registered callback does not panic or deadlock
// (it logs at warn level and returns to the caller).
func TestLifecycle_OnStartAfterStarted_ErrorIsLogged(t *testing.T) {
	t.Parallel()
	lc := NewLifecycle()
	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	lc.OnStart(func(_ context.Context) error {
		return errors.New("boom")
	})
}

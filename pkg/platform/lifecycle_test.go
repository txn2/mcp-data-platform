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

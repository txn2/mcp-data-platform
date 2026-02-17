package platform

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Lifecycle manages the startup and shutdown of platform components.
type Lifecycle struct {
	mu sync.Mutex

	startCallbacks []func(context.Context) error
	stopCallbacks  []func(context.Context) error

	started bool
}

// NewLifecycle creates a new lifecycle manager.
func NewLifecycle() *Lifecycle {
	return &Lifecycle{
		startCallbacks: make([]func(context.Context) error, 0),
		stopCallbacks:  make([]func(context.Context) error, 0),
	}
}

// OnStart registers a callback to run on startup.
func (l *Lifecycle) OnStart(callback func(context.Context) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.startCallbacks = append(l.startCallbacks, callback)
}

// OnStop registers a callback to run on shutdown.
func (l *Lifecycle) OnStop(callback func(context.Context) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopCallbacks = append(l.stopCallbacks, callback)
}

// Start runs all start callbacks.
func (l *Lifecycle) Start(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.started {
		return fmt.Errorf("lifecycle already started")
	}

	for i, cb := range l.startCallbacks {
		if err := cb(ctx); err != nil {
			l.rollback(ctx, i)
			return fmt.Errorf("start callback %d failed: %w", i, err)
		}
	}

	l.started = true
	return nil
}

// rollback stops already-started components in reverse order.
// Called when a start callback fails at index failedAt.
func (l *Lifecycle) rollback(ctx context.Context, failedAt int) {
	for j := failedAt - 1; j >= 0; j-- {
		if l.stopCallbacks[j] == nil {
			continue
		}
		if err := l.stopCallbacks[j](ctx); err != nil {
			slog.Warn("lifecycle rollback: stop callback failed",
				"callback", j, "error", err)
		}
	}
}

// Stop runs all stop callbacks in reverse order.
func (l *Lifecycle) Stop(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.started {
		return nil
	}

	var errs []error
	for i := len(l.stopCallbacks) - 1; i >= 0; i-- {
		if err := l.stopCallbacks[i](ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop callback %d: %w", i, err))
		}
	}

	l.started = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during shutdown: %v", errs)
	}
	return nil
}

// IsStarted returns whether the lifecycle has been started.
func (l *Lifecycle) IsStarted() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.started
}

// Component is something that can be started and stopped.
type Component interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// RegisterComponent registers a component with the lifecycle.
func (l *Lifecycle) RegisterComponent(c Component) {
	l.OnStart(c.Start)
	l.OnStop(c.Stop)
}

// Closer is something that can be closed.
type Closer interface {
	Close() error
}

// RegisterCloser registers a closer to be closed on shutdown.
func (l *Lifecycle) RegisterCloser(c Closer) {
	l.OnStop(func(_ context.Context) error {
		return c.Close()
	})
}

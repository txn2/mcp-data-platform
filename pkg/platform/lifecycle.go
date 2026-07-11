package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// component pairs a start callback with the stop callback that tears it
// down. Registering both halves atomically makes the pairing structural:
// Start, Stop, and rollback all iterate the same slice, so rollback can
// never stop the wrong component or index out of range when a component
// registers only one half.
type component struct {
	start func(context.Context) error
	stop  func(context.Context) error
}

// Lifecycle manages the startup and shutdown of platform components.
type Lifecycle struct {
	mu sync.Mutex

	components []component

	started bool
}

// NewLifecycle creates a new lifecycle manager.
func NewLifecycle() *Lifecycle {
	return &Lifecycle{}
}

// OnComponent registers a component's start and stop callbacks as one unit.
// Either callback may be nil. This is the preferred registration path: it
// keeps the start/stop pairing structural rather than relying on matched
// registration order across two lists.
//
// If the lifecycle has already been started, the start callback is invoked
// immediately with context.Background() and any error is logged. This
// handles late-wiring paths (toolkit setup that happens inside the HTTP
// server assembly, after platform.Start has already run) which otherwise
// silently never fire. The stop callback still runs at shutdown.
func (l *Lifecycle) OnComponent(start, stop func(context.Context) error) {
	l.mu.Lock()
	if !l.started {
		l.components = append(l.components, component{start: start, stop: stop})
		l.mu.Unlock()
		return
	}
	// Late registration: start already happened, run the callback now and
	// keep only the stop half for shutdown.
	l.components = append(l.components, component{stop: stop})
	l.mu.Unlock()
	if start == nil {
		return
	}
	if err := start(context.Background()); err != nil {
		slog.Warn("lifecycle: late-registered start callback failed", "error", err)
	}
}

// OnStart registers a callback to run on startup. Prefer OnComponent when
// the component also has a stop half.
func (l *Lifecycle) OnStart(callback func(context.Context) error) {
	l.OnComponent(callback, nil)
}

// OnStop registers a callback to run on shutdown. Prefer OnComponent when
// the component also has a start half.
func (l *Lifecycle) OnStop(callback func(context.Context) error) {
	l.OnComponent(nil, callback)
}

// Start runs all start callbacks in registration order. If one fails,
// already-started components are rolled back in reverse order.
func (l *Lifecycle) Start(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.started {
		return errors.New("lifecycle already started")
	}

	for i, c := range l.components {
		if c.start == nil {
			continue
		}
		if err := c.start(ctx); err != nil {
			l.rollback(ctx, i)
			return fmt.Errorf("start callback %d failed: %w", i, err)
		}
	}

	l.started = true
	return nil
}

// rollback tears down components registered before the one that failed, in
// reverse order. Stop-only components are torn down too: their resources
// were created eagerly at wiring time, and registration only scheduled the
// close.
func (l *Lifecycle) rollback(ctx context.Context, failedAt int) {
	for j := failedAt - 1; j >= 0; j-- {
		if l.components[j].stop == nil {
			continue
		}
		if err := l.components[j].stop(ctx); err != nil {
			slog.Warn("lifecycle rollback: stop callback failed",
				"callback", j, "error", err)
		}
	}
}

// Stop runs all stop callbacks in reverse registration order.
func (l *Lifecycle) Stop(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.started {
		return nil
	}

	var errs []error
	for i := len(l.components) - 1; i >= 0; i-- {
		if l.components[i].stop == nil {
			continue
		}
		if err := l.components[i].stop(ctx); err != nil {
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
	l.OnComponent(c.Start, c.Stop)
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

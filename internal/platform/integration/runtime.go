package integration

import (
	"context"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/pglisten"
)

// passEvery is the longest a pass waits with nothing announced: the timer of
// an operation is checked this often, and a run whose end was not announced
// (a replica stopped as it ended) is applied within it.
const passEvery = 30 * time.Second

// Runtime runs the passes on this replica: on every announcement on Channel
// and every passEvery.
type Runtime struct {
	Service *Service
	// DSN opens the LISTEN connection; empty polls only.
	DSN string

	wake     chan struct{}
	listener *pglisten.Listener
	cancel   context.CancelFunc
}

// Notify wakes the next pass; the listener calls it on every announcement.
func (r *Runtime) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Start listens for announcements and starts the passes.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil || r.Service == nil {
		return nil
	}
	r.wake = make(chan struct{}, 1)
	if r.DSN != "" {
		r.listener = pglisten.New(r.DSN, Channel, r)
		if err := r.listener.Start(ctx); err != nil {
			slog.WarnContext(ctx, "integrations: listening for changes failed; passes run every 30 seconds",
				"error", logsan.SanitizeForLog(err.Error()))
		}
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.cancel = cancel
	go bgloop.Run(loopCtx, bgloop.Loop{
		Name: bgloop.NameIntegrations, Every: passEvery, Immediate: true, Wake: r.wake,
		Body: func(ctx context.Context) error {
			err := r.Service.Pass(ctx)
			logFailure(ctx, err)
			return err
		},
	})
	return nil
}

// Stop ends the listener and the passes.
func (r *Runtime) Stop(context.Context) error {
	if r == nil {
		return nil
	}
	if r.listener != nil {
		r.listener.Stop()
	}
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

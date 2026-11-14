package callrun

import (
	"context"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/pglisten"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
)

// sweepEvery is how often counts past the longest window and failures past
// their week are removed.
const sweepEvery = time.Hour

// Sweeper removes what handlers no longer need (callhandler.Store).
type Sweeper interface {
	Sweep(ctx context.Context, now time.Time) error
}

// Runtime is the tool-call handlers' lifecycle on one replica: the index read
// before the first call and again on every change any replica announces, the
// watch writer, and the sweep.
type Runtime struct {
	Evaluator *Evaluator
	DSN       string
	Sweeper   Sweeper

	listener *pglisten.Listener
	cancel   context.CancelFunc
}

// Start reads the handlers, listens for changes, and starts the writer and
// the sweep. A failed read is logged: the index reads again in the
// background on the next call.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil || r.Evaluator == nil {
		return nil
	}
	e := r.Evaluator
	e.Start()
	if err := e.Index.Load(ctx); err != nil {
		slog.WarnContext(ctx, "call handlers: the first read of the handlers failed; the next call reads them",
			"error", logsan.SanitizeForLog(err.Error()))
	}
	if r.DSN != "" {
		r.listener = pglisten.New(r.DSN, callhandler.Channel, e.Index)
		if err := r.listener.Start(ctx); err != nil {
			slog.WarnContext(ctx, "call handlers: listening for changed handlers failed; each replica reads them every few seconds",
				"error", logsan.SanitizeForLog(err.Error()))
		}
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.cancel = cancel
	if e.Watches != nil {
		go e.Watches.Run(loopCtx)
	}
	if r.Sweeper != nil {
		go bgloop.Run(loopCtx, bgloop.Loop{Name: bgloop.NameCallHandlerSweep, Every: sweepEvery, Body: func(ctx context.Context) error {
			return r.Sweeper.Sweep(ctx, time.Now()) //nolint:wrapcheck // the store names the sweep
		}})
	}
	return nil
}

// Stop ends the listener, the writer and the sweep.
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

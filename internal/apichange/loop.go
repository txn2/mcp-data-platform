package apichange

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/advisorylock"
	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// LockKey is the advisory lock a pass holds, so one replica checks at a time.
const LockKey int64 = 4713210231

// pollInterval is how often the loop asks which specs are due. The interval a
// spec is checked at is the administrator's; this bounds how late it runs.
const pollInterval = time.Minute

// batch bounds the specs one pass checks; the rest are due on the next.
const batch = 20

// Loop checks every spec whose scheduled check has come (#2483).
type Loop struct {
	Refresher *Refresher
	DB        *sql.DB
	poll      time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// Start begins the loop; it checks at once.
func (l *Loop) Start(ctx context.Context) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		return
	}
	ctx, l.cancel = context.WithCancel(ctx)
	l.done = make(chan struct{})
	go func(done chan struct{}) {
		defer close(done)
		every := l.poll
		if every <= 0 {
			every = pollInterval
		}
		bgloop.Run(ctx, bgloop.Loop{Name: bgloop.NameAPISpecRefresh, Every: every, Immediate: true, Body: l.RunOnce})
	}(l.done)
}

// Stop ends the loop and waits for a pass in progress.
func (l *Loop) Stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	cancel, done := l.cancel, l.done
	l.cancel = nil
	l.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// RunOnce checks the due specs under the advisory lock, and forgets
// run-recorded callers past their retention. It returns bgloop.ErrSkipped
// when another replica holds the lock. A spec whose check fails is stamped
// with the failure and checked again one interval on; the pass goes on to
// the next.
func (l *Loop) RunOnce(ctx context.Context) error {
	release, ok, err := advisorylock.Try(ctx, l.DB, advisorylock.ID(LockKey))
	if err != nil {
		return err //nolint:wrapcheck // the lock package names itself
	}
	if !ok {
		return bgloop.ErrSkipped
	}
	defer release()
	due, err := l.Refresher.Store.Due(ctx, batch)
	if err != nil {
		return err //nolint:wrapcheck // the store says what it could not list
	}
	for _, k := range due {
		if _, err := l.Refresher.Check(ctx, k, TriggerSchedule, ""); err != nil && !errors.Is(err, context.Canceled) {
			slog.WarnContext(ctx, "api changes: a scheduled check failed",
				"catalog_id", logsan.SanitizeForLog(k.CatalogID), "spec", logsan.SanitizeForLog(k.SpecName),
				"error", logsan.SanitizeForLog(err.Error()))
		}
	}
	return PruneCallers(ctx, l.DB)
}

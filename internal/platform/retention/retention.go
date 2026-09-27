// Package retention removes what the platform writes and nothing else ever
// removes (#1904): soft-deleted portal items past their grace period, with
// their objects; archived memory records; producer rows whose file is gone;
// and GraphQL operation embeddings of a schema a connection no longer has.
// One pass mends rather than removes: it names the portal bucket on asset rows
// written with none, ahead of the purge, which could not otherwise delete
// their objects (#1931).
//
// Each sweep runs once a day under its own PostgreSQL advisory lock, so every
// replica may run the loop and only one of them deletes at a time, and one
// subsystem's sweep never waits on another's. The first pass runs at start
// rather than a day later, so a deployment that lowers a retention restarts
// into it.
package retention

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// DefaultInterval is how often the sweeps run.
const DefaultInterval = 24 * time.Hour

// Retention defaults, in days. A deleted portal item is kept a month so a
// mistaken delete can still be taken up with an administrator; the others
// match the call catalog's and the audit log's ninety days.
const (
	DefaultDeletedRetentionDays  = 30
	DefaultArchivedRetentionDays = 90
	DefaultProducerRetentionDays = 90
)

// Days resolves a configured retention: zero takes def, a negative value
// disables the sweep (reported as 0), and anything else is used as given.
func Days(configured, def int) int {
	switch {
	case configured == 0:
		return def
	case configured < 0:
		return 0
	default:
		return configured
	}
}

// unlockTimeout bounds the advisory unlock after a sweep, so a shutdown
// cannot hang on releasing it.
const unlockTimeout = 5 * time.Second

// Sweep is one removal the loop runs. LockKey is its advisory lock, distinct
// from every other maintenance lock in the platform. Run reports how many
// rows it removed.
type Sweep struct {
	Name    string
	LockKey int64
	Run     func(ctx context.Context) (int64, error)
}

// Loop runs a set of sweeps on an interval until Close.
type Loop struct {
	db       *sql.DB
	sweeps   []Sweep
	interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a loop over sweeps. A non-positive interval takes
// DefaultInterval.
func New(db *sql.DB, interval time.Duration, sweeps ...Sweep) *Loop {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Loop{db: db, sweeps: sweeps, interval: interval}
}

// Start runs every sweep now and then on the interval, in its own goroutine.
// A second call, a loop with no database and a loop with no sweeps do nothing.
func (l *Loop) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil || len(l.sweeps) == 0 || l.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.done = cancel, make(chan struct{})
	go func() {
		defer close(l.done)
		l.RunOnce(ctx)
		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.RunOnce(ctx)
			}
		}
	}()
}

// Close stops the loop and waits for a running pass to return.
func (l *Loop) Close() error {
	l.mu.Lock()
	cancel, done := l.cancel, l.done
	l.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

// RunOnce runs every sweep once, each under its lock. A sweep another replica
// is running is skipped, and one that fails is logged and does not stop the
// others.
func (l *Loop) RunOnce(ctx context.Context) {
	for _, s := range l.sweeps {
		if ctx.Err() != nil {
			return
		}
		removed, ran, err := l.runLocked(ctx, s)
		switch {
		case err != nil:
			slog.Warn("retention: sweep failed", "sweep", s.Name, "error", logsan.SanitizeForLog(err.Error()))
		case ran && removed > 0:
			slog.Info("retention: sweep removed rows", "sweep", s.Name, "removed", removed)
		}
	}
}

// runLocked runs one sweep while holding its advisory lock on a dedicated
// connection, since an advisory lock belongs to the session that took it.
// ran is false when another replica holds the lock.
func (l *Loop) runLocked(ctx context.Context, s Sweep) (removed int64, ran bool, err error) {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("acquiring a connection for the lock: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", s.LockKey).Scan(&got); err != nil {
		return 0, false, fmt.Errorf("taking the advisory lock: %w", err)
	}
	if !got {
		return 0, false, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		if _, uerr := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", s.LockKey); uerr != nil {
			slog.Warn("retention: advisory unlock failed", "sweep", s.Name, "error", logsan.SanitizeForLog(uerr.Error()))
		}
	}()
	removed, err = s.Run(ctx)
	return removed, true, err
}

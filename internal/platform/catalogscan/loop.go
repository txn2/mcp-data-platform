package catalogscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// LockKey is the advisory lock a scan holds, so replicas never scan at once.
const LockKey int64 = 4713210201

// pollInterval is how often the loop asks whether a scan is due. The
// interval itself is the operator's setting; this only bounds how late after
// it a scan starts.
const pollInterval = time.Minute

// unlockTimeout bounds the release of the lock after a scan.
const unlockTimeout = 5 * time.Second

// Loop runs a scan whenever the configured interval has passed since the
// last one any replica started, and whenever asked to.
type Loop struct {
	scanner  *Scanner
	settings SettingsReader
	db       *sql.DB
	poll     time.Duration
	trigger  chan struct{}
	after    func(context.Context)

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewLoop builds the loop.
func NewLoop(scanner *Scanner, settings SettingsReader, db *sql.DB) *Loop {
	return &Loop{scanner: scanner, settings: settings, db: db, poll: pollInterval, trigger: make(chan struct{}, 1)}
}

// AfterScan names what runs after each scan this replica finishes without
// error. Set it before Start.
func (l *Loop) AfterScan(fn func(context.Context)) {
	if l != nil {
		l.after = fn
	}
}

// Start begins the loop. It checks at once, so a deployment's first scan
// runs when it starts rather than an interval later.
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
	go l.run(ctx, l.done)
}

// Stop ends the loop and waits for a scan in progress to return.
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

const recordRequest = `
INSERT INTO catalog_scan_state (id, requested_at, requested_by) VALUES (TRUE, NOW(), $1)
ON CONFLICT (id) DO UPDATE SET requested_at = EXCLUDED.requested_at, requested_by = EXCLUDED.requested_by`

// recordStart marks when a whole scan started, before it reads anything.
const recordStart = `
INSERT INTO catalog_scan_state (id, started_at) VALUES (TRUE, NOW())
ON CONFLICT (id) DO UPDATE SET started_at = EXCLUDED.started_at`

// ScanNow asks for a scan regardless of the interval and returns at once. The
// request is recorded, so the replica that next takes the scan lock runs it
// even when another replica is scanning now; this replica also checks at once.
// A request made while one is already waiting is the same request.
func (l *Loop) ScanNow(ctx context.Context, by string) error {
	if _, err := l.db.ExecContext(ctx, recordRequest, by); err != nil {
		return fmt.Errorf("recording the scan request: %w", err)
	}
	select {
	case l.trigger <- struct{}{}:
	default:
	}
	return nil
}

func (l *Loop) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	bgloop.Run(ctx, bgloop.Loop{
		Name: bgloop.NameCatalogScan, Every: l.poll, Immediate: true, Wake: l.trigger, Body: l.runOnce,
	})
}

// runOnce scans when one is due, under the advisory lock. It returns
// bgloop.ErrSkipped when another replica holds the lock.
func (l *Loop) runOnce(ctx context.Context) error {
	ran, err := l.lockedScan(ctx)
	if errors.Is(err, bgloop.ErrSkipped) {
		return err
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.WarnContext(ctx, "catalog scan: run failed", "error", logsan.SanitizeForLog(err.Error()))
		}
		return err
	}
	if ran {
		slog.InfoContext(ctx, "catalog scan finished")
		if l.after != nil {
			l.after(ctx)
		}
	}
	return nil
}

// scanDue reads when the last whole scan started and when one was last
// requested; no row means neither has happened.
const scanDue = `SELECT started_at, requested_at FROM catalog_scan_state WHERE id`

// lockedScan takes the lock on a dedicated connection, since an advisory lock
// belongs to the session that took it, and scans when due. It returns
// bgloop.ErrSkipped when another replica holds the lock, and ran is false
// when no scan was due.
//
// A request made during the scan is served before the lock is released: the
// replica that asked was refused the lock, and would otherwise wait for its
// next poll to look again (#2218).
func (l *Loop) lockedScan(ctx context.Context) (ran bool, err error) {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("acquiring a connection for the lock: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", LockKey).Scan(&got); err != nil {
		return false, fmt.Errorf("taking the advisory lock: %w", err)
	}
	if !got {
		return false, bgloop.ErrSkipped
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		if _, uerr := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", LockKey); uerr != nil {
			slog.WarnContext(ctx, "catalog scan: advisory unlock failed", "error", logsan.SanitizeForLog(uerr.Error()))
		}
	}()
	for {
		due, err := l.due(ctx, conn)
		if err != nil || !due {
			return ran, err
		}
		if _, err := conn.ExecContext(ctx, recordStart); err != nil {
			return ran, fmt.Errorf("recording the scan's start: %w", err)
		}
		if _, err := l.scanner.Scan(ctx); err != nil {
			return ran, err
		}
		ran = true
	}
}

// due reports whether a scan is owed: none has run, the interval has passed
// since any replica last started a whole scan, or one was requested after it
// started.
// The times are read under the lock, so two replicas cannot both find the
// same scan due.
func (l *Loop) due(ctx context.Context, conn *sql.Conn) (bool, error) {
	set, err := l.settings.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("reading catalog settings: %w", err)
	}
	var last, requested sql.NullTime
	err = conn.QueryRowContext(ctx, scanDue).Scan(&last, &requested)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("reading the last scan: %w", err)
	}
	switch {
	case !last.Valid:
		return true, nil
	case requested.Valid && requested.Time.After(last.Time):
		return true, nil
	default:
		return time.Since(last.Time) >= set.Interval(), nil
	}
}

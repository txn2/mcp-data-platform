package callrecord

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// A catalog of every data-access call the platform makes grows for as long as
// the platform is used, and most of what it accumulates is noise: a query that
// ran, answered nothing anybody kept, and was never run again. Keeping those
// forever costs storage and slows every read that derives an outcome over the
// table.
//
// So the catalog is swept — but by what a record came to, not by its age alone.
// A record something was built from, one somebody promoted, one a reviewer
// declined, and one another session re-ran are all evidence, and evidence does
// not expire on a timer. What ages out is the draft nobody used.
//
// This is the same shape the audit store's retention takes, including the
// advisory lock: several replicas share one database and only one of them
// should be deleting.

const (
	// DefaultRetentionDays is how long a call that came to nothing is kept.
	// It is deliberately shorter than the year a script run is kept: a run is
	// a scheduled automation's refresh history, which people read, while an
	// unused query is a draft.
	DefaultRetentionDays = 90

	// sweepLockKey is the advisory-lock key this sweep runs under. It is
	// distinct from every other maintenance lock in the platform so one
	// subsystem's sweep never blocks another's.
	sweepLockKey = 4713210001

	// unlockTimeout bounds the advisory unlock after a sweep, so a shutdown
	// cannot hang on releasing it.
	unlockTimeout = 5 * time.Second
)

// RetentionDays resolves the configured retention, applying the default when
// unset. Zero or negative takes the default, matching every other retention
// this platform configures.
func RetentionDays(configured int) int {
	if configured <= 0 {
		return DefaultRetentionDays
	}
	return configured
}

// sweepQuery deletes the records that came to nothing.
//
// The four survival clauses are the whole rule, and each names a different kind
// of evidence: an asset or a capture cites it (the satisfied-by expression, the
// same one every read derives an outcome from), someone published it, someone
// declined it (so the queue does not offer it again), or another session found
// it and re-ran what it holds.
//
// #nosec G202 -- the only thing concatenated is this package's own satisfaction
// rule; every value the statement compares is bound as a parameter.
var sweepQuery = `
	DELETE FROM call_records r
	WHERE r.created_at < $2
	  AND r.promoted_urn = ''
	  AND r.rejected_at IS NULL
	  AND NOT EXISTS (SELECT 1 FROM call_record_reuse u WHERE u.call_record_id = r.id)
	  AND (` + satisfiedByCase("$1") + `) IS NULL`

// Cleanup removes the records older than the retention window that nothing
// came of, and reports how many it removed.
func (s *PostgresStore) Cleanup(ctx context.Context) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	res, err := s.db.ExecContext(ctx, sweepQuery, callReferencePrefix(), cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweeping expired call records: %w", err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // a driver that cannot count still swept
	}
	return removed, nil
}

// StartCleanupRoutine sweeps expired records on an interval until Close. It is
// started by the layer that assembles the catalog, so a deployment does not
// have to remember to run it.
func (s *PostgresStore) StartCleanupRoutine(interval time.Duration) {
	if s == nil || s.db == nil || s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepTick(ctx)
			}
		}
	}()
}

// Close stops the sweeper and waits for the tick in flight. It does not close
// the database handle, which the layer that opened it owns.
func (s *PostgresStore) Close() error {
	if s == nil || s.cancel == nil {
		return nil
	}
	s.cancel()
	<-s.done
	s.cancel = nil
	return nil
}

// sweepTick runs one sweep, under an advisory lock so that only one replica
// deletes per tick.
func (s *PostgresStore) sweepTick(ctx context.Context) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		slog.Warn("call catalog: acquire connection for the retention lock", "error", err)
		return
	}
	defer func() { _ = conn.Close() }()

	var acquired bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock($1)", sweepLockKey).Scan(&acquired); err != nil {
		slog.Warn("call catalog: try retention lock", "error", err)
		return
	}
	if !acquired {
		// Another replica is sweeping; this tick has nothing to do.
		return
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", sweepLockKey); err != nil {
			slog.Warn("call catalog: release retention lock", "error", err)
		}
	}()

	removed, err := s.Cleanup(ctx)
	if err != nil {
		slog.Warn("call catalog: sweep expired records", "error", err)
		return
	}
	if removed > 0 {
		slog.Info("call catalog: swept records that came to nothing",
			"removed", removed, "retention_days", s.retentionDays)
	}
}

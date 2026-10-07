package callrecord

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/script"
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
// A record an excluded persona made ages out at once rather than on the clock,
// and so does one a managed script run made. The recorder writes no more of
// either (see exclusion.go), and the ones written before the deployment
// declared that persona, or before this platform declined a run's calls, are
// noise it has already said it does not want — with the same evidence clauses
// standing, since a record something was built from is evidence whoever
// produced it. A persona marked as a service account is an excluded persona
// like one named in config, and is swept the same way (#1980).
//
// That backlog can be large: one deployment held 1.4 million records from a
// single automated caller before it was marked. So the sweep deletes in bounded
// batches, each its own statement and so its own transaction, rather than one
// DELETE holding a transaction and its locks across the whole table. A batch
// that commits stays committed if a later one fails or the process stops; the
// next sweep resumes from what is left.
//
// A record's vector is on its own row, so it goes with the row. Its indexing
// units do not: index_jobs keys a unit by source kind and id with no foreign
// key to the record, so each batch deletes the units of the records it removed
// in the same statement. Otherwise a pending unit would outlive its record,
// and the calls index would go on counting work for a record that is gone.
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

	// sweepBatchSize bounds how many records one statement of the sweep
	// removes. It is the same bound the index queue's own retention purges
	// by: large enough that a steady-state sweep is one statement, small
	// enough that one statement never holds a long transaction.
	sweepBatchSize = 5000
)

// IndexSourceKind is the index_jobs source kind a call record is indexed
// under. The calls index consumer serves this kind, and the sweep removes the
// units of this kind that belong to the records it deletes.
const IndexSourceKind = "calls"

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
// it and re-ran what it holds. They apply to both halves of the age test below:
// evidence is evidence whoever produced it.
//
// A record is old enough to sweep, or it was made by a persona the deployment
// has since declared to be machinery (#1614), or a managed script run made it
// (#1624). The second and third arms are what deal with the rows written before
// each rule existed: the recorder writes no more of them, and these go on the
// next sweep rather than sitting in the catalog and the embedding queue for the
// length of the retention window. A run's rows are matched by their principal
// (`script:<name>`, the only user id a run's calls are audited under), since
// the catalog stores no source column of its own.
//
// One execution removes at most $5 records, with the index units of kind $6
// that belong to them, and answers how many records it removed; Cleanup runs it
// until a batch comes back short.
//
// #nosec G202 -- the only thing concatenated is this package's own satisfaction
// rule; every value the statement compares is bound as a parameter.
var sweepQuery = `
	WITH doomed AS (
		SELECT r.id FROM call_records r
		WHERE (r.created_at < $2 OR lower(r.persona) = ANY($3) OR r.user_id LIKE $4)
		  AND r.promoted_urn = ''
		  AND r.rejected_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM call_record_reuse u WHERE u.call_record_id = r.id)
		  AND (` + satisfiedByCase("$1") + `) IS NULL
		LIMIT $5
	), gone AS (
		DELETE FROM call_records c USING doomed d WHERE c.id = d.id RETURNING c.id
	), units AS (
		DELETE FROM index_jobs j USING gone g
		WHERE j.source_kind = $6 AND j.source_id = g.id::text
	)
	SELECT COUNT(*) FROM gone`

// Cleanup removes the records nothing came of: those past the retention window,
// those an excluded persona made whenever it made them, and those a managed
// script run made. It reports how many it removed.
//
// The personas are read once, when the sweep starts, so every batch of one
// sweep applies the same rule. A cancellation between or during batches ends
// the sweep without an error: the batches already committed stand, and the
// next sweep resumes.
func (s *PostgresStore) Cleanup(ctx context.Context) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	personas := pq.Array(s.excluded.Personas())
	var total int64
	for ctx.Err() == nil {
		var removed int64
		err := s.db.QueryRowContext(ctx, sweepQuery, callReferencePrefix(), cutoff, personas,
			script.PrincipalPrefix+"%", sweepBatchSize, IndexSourceKind).Scan(&removed)
		if err != nil && ctx.Err() == nil {
			return total, fmt.Errorf("sweeping expired call records: %w", err)
		}
		total += removed
		if err != nil || removed < sweepBatchSize {
			break
		}
	}
	if total > 0 {
		s.top.forget()
	}
	return total, nil
}

// RequestSweep asks the running sweeper for a sweep now rather than at its
// next tick. The admin API calls it when a persona is saved as a service
// account, so the records that persona already wrote go promptly rather than
// up to a day later. It never blocks: a request made while one is already
// waiting is the same sweep.
func (s *PostgresStore) RequestSweep() {
	if s == nil || s.kick == nil {
		return
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
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

		// The first sweep runs now rather than one interval from now. A
		// deployment that has just declared a persona to be machinery restarts
		// to apply it, and the rows that persona already wrote are removed at
		// that restart instead of a day into it.
		s.sweepTick(ctx)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// An asked-for sweep that finds another replica holding the lock is
		// asked again until it gets it: the sweep in progress read its
		// personas when it started, so it may not remove the ones just marked.
		var retry <-chan time.Time
		asked := func() {
			retry = nil
			if !s.sweepTick(ctx) {
				retry = time.After(s.kickRetry)
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepTick(ctx)
			case <-s.kick:
				asked()
			case <-retry:
				asked()
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
// deletes per tick. It reports false when another replica held the lock, and
// true otherwise, a sweep that failed included.
func (s *PostgresStore) sweepTick(ctx context.Context) bool {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		slog.WarnContext(ctx, "call catalog: acquire connection for the retention lock", "error", err)
		return true
	}
	defer func() { _ = conn.Close() }()

	var acquired bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock($1)", sweepLockKey).Scan(&acquired); err != nil {
		slog.WarnContext(ctx, "call catalog: try retention lock", "error", err)
		return true
	}
	if !acquired {
		// Another replica is sweeping; this tick has nothing to do.
		return false
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", sweepLockKey); err != nil {
			slog.WarnContext(ctx, "call catalog: release retention lock", "error", err)
		}
	}()

	removed, err := s.Cleanup(ctx)
	if err != nil {
		slog.WarnContext(ctx, "call catalog: sweep expired records", "error", err)
		return true
	}
	if removed > 0 {
		slog.InfoContext(ctx, "call catalog: swept records that came to nothing",
			"removed", removed, "retention_days", s.retentionDays,
			"excluded_personas", len(s.excluded.Personas()))
	}
	return true
}

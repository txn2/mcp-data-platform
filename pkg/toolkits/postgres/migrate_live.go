package postgres

// The parts of manage_schema that hold a session on the target database: the
// apply lock, the ledger's creation, and each migration applied or reverted
// in its own transaction. The unit tests drive them through a fake session;
// the RealDB tests in schema_realdb_integration_test.go run them against
// PostgreSQL.

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// session is a pooled connection held for work that spans statements: an
// apply's advisory lock is held by its session. *pgxpool.Conn is one, through
// poolSession; the unit tests hold a fake.
type session interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (pgx.Tx, error)
	// Script runs SQL that may hold several statements, over the simple
	// protocol.
	Script(ctx context.Context, sql string) error
	Release()
}

// poolSession is a pooled connection as a session.
type poolSession struct{ *pgxpool.Conn }

// Script implements session.
func (p poolSession) Script(ctx context.Context, sql string) error {
	_, err := p.Conn.Conn().PgConn().Exec(ctx, sql).ReadAll()
	return err //nolint:wrapcheck // read by its type
}

// session takes a pooled connection, waiting at most pool_wait_timeout.
func (c *conn) session(ctx context.Context) (session, error) {
	if c.openSession != nil {
		return c.openSession(ctx)
	}
	pc, err := c.acquire(ctx, c.pool)
	if err != nil {
		return nil, err
	}
	return poolSession{pc}, nil
}

// script runs SQL that may hold several statements in tx, over the simple
// protocol. A transaction that runs scripts itself (a test's) is asked to.
func script(ctx context.Context, tx pgx.Tx, sql string) error {
	if s, ok := tx.(interface {
		Script(ctx context.Context, sql string) error
	}); ok {
		return s.Script(ctx, sql) //nolint:wrapcheck // read by its type
	}
	_, err := tx.Conn().PgConn().Exec(ctx, sql).ReadAll()
	return err //nolint:wrapcheck // read by its type
}

// ensureLedger creates the ledger on first use.
func (c *conn) ensureLedger(ctx context.Context, s session) error {
	ddl := "CREATE SCHEMA IF NOT EXISTS " + c.cfg.LedgerSchema + "; " +
		"CREATE TABLE IF NOT EXISTS " + c.ledger() + " (schema_name text NOT NULL, version integer NOT NULL, " +
		"name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now(), " +
		"applied_by text NOT NULL DEFAULT '', duration_ms bigint NOT NULL DEFAULT 0, PRIMARY KEY (schema_name, version))"
	return s.Script(ctx, ddl) //nolint:wrapcheck // read by its type
}

// prepareMigration bounds a migration's transaction and points it at its
// schema, creating the schema on first use. timeout zero is the connection's
// statement timeout.
func (c *conn) prepareMigration(ctx context.Context, tx pgx.Tx, schema string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = c.cfg.StatementTimeout
	}
	settings := "SELECT set_config('lock_timeout', $1, true), set_config('statement_timeout', $2, true)"
	if _, err := tx.Exec(ctx, settings, millis(migrationLockTimeout), millis(timeout)); err != nil {
		return fmt.Errorf("bounding the migration: %w", err)
	}
	if _, err := tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", schema); err != nil {
		return fmt.Errorf("setting the search path: %w", err)
	}
	return nil
}

// Apply applies the pending migrations in version order, each in a
// transaction of its own that also writes its ledger row, so a migration that
// fails leaves neither a partial change nor a row. An advisory lock in the
// target database is held for the whole apply, so two replicas or two callers
// never apply at once: the second waits, then finds nothing pending. A
// migration that is not transactional runs alone, outside a transaction.
//
// migrations is everything the platform holds for the schema; the ledger is
// read again under the lock and compared with it, and any drift stops the
// apply before a migration runs.
func (t *Toolkit) Apply(ctx context.Context, o ApplyOptions, migrations []Migration) ([]Migration, error) {
	s, err := t.lockedSession(ctx, o)
	if err != nil {
		return nil, err
	}
	defer s.pc.Release()
	pending, err := s.agreed(ctx, o, migrations, "applied")
	if err != nil {
		return nil, err
	}
	step := s.c.applyOne
	if o.MarkOnly {
		step = func(ctx context.Context, pc session, o ApplyOptions, m Migration) error {
			return s.c.recordApplied(ctx, pc, o, m, 0)
		}
	}
	return s.each(ctx, o, upTo(pending, o.UpTo), step)
}

// maintenanceVerbs are the statements RunMaintenance runs: PostgreSQL refuses
// VACUUM, and REINDEX CONCURRENTLY, in a transaction.
var maintenanceVerbs = []string{"ANALYZE", "VACUUM", "REINDEX"}

// RunMaintenance runs one ANALYZE, VACUUM or REINDEX statement outside a
// transaction on the connection's own pool (#2410), under lockTimeout and
// timeout (zero is the connection's statement_timeout). The reset a released
// connection runs undoes both settings.
func (t *Toolkit) RunMaintenance(ctx context.Context, connection, stmt string, lockTimeout, timeout time.Duration) error {
	c, ok := t.lookup(connection)
	verb, _, _ := strings.Cut(strings.TrimSpace(stmt), " ")
	if !ok || !slices.Contains(maintenanceVerbs, strings.ToUpper(verb)) {
		return &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf("%s: no maintenance of %q on connection %q", errPrefix, verb, connection)}
	}
	pc, err := c.session(ctx)
	if err != nil {
		return c.failure(ctx, err)
	}
	defer pc.Release()
	_, err = pc.Exec(ctx, "SELECT set_config('lock_timeout', $1, false), set_config('statement_timeout', $2, false)",
		millis(lockTimeout), millis(cmp.Or(timeout, c.cfg.StatementTimeout)))
	if err == nil {
		// The extended protocol runs one statement, never a list.
		_, err = pc.Exec(ctx, stmt)
	}
	observe(c.cfg.ConnectionName, err)
	if err != nil {
		return c.failure(ctx, err)
	}
	return nil
}

// Revert reverts applied migrations, newest first, down to and excluding
// toVersion, each with its down SQL in a transaction that also deletes its
// ledger row. toVersion below zero reverts only the latest.
func (t *Toolkit) Revert(ctx context.Context, o ApplyOptions, migrations []Migration, toVersion int) ([]Migration, error) {
	s, err := t.lockedSession(ctx, o)
	if err != nil {
		return nil, err
	}
	defer s.pc.Release()
	if _, err := s.agreed(ctx, o, migrations, "reverted"); err != nil {
		return nil, err
	}
	targets, err := revertMigrations(s.applied, migrations, toVersion)
	if err != nil {
		return nil, err
	}
	return s.each(ctx, o, targets, s.c.revertOne)
}

// lockedApply is a session holding the apply lock, and the ledger it read.
type lockedApply struct {
	c       *conn
	pc      session
	applied []Applied
}

// agreed reads the ledger under the lock and refuses to go on when it and the
// platform's migrations disagree; it returns what is pending.
func (s *lockedApply) agreed(ctx context.Context, o ApplyOptions, migrations []Migration, verb string) ([]Migration, error) {
	applied, err := s.c.readLedger(ctx, s.pc, o.Schema)
	if err != nil {
		return nil, s.c.failure(ctx, err)
	}
	s.applied = applied
	pending, drift := Compare(migrations, applied)
	if len(drift) > 0 {
		return nil, &RefusedError{Code: CodeSchemaDrift, msg: fmt.Sprintf(
			"%s: version %d: %s; nothing was %s", errPrefix, drift[0].Version, drift[0].Reason, verb)}
	}
	return pending, nil
}

// each runs step on each migration in order, telling OnDone, and stops at
// the first failure. A schema change empties the statement cache.
func (s *lockedApply) each(ctx context.Context, o ApplyOptions, ms []Migration,
	step func(context.Context, session, ApplyOptions, Migration) error,
) ([]Migration, error) {
	done := []Migration{}
	defer func() {
		if len(done) > 0 && s.c.cache != nil {
			s.c.cache.invalidate()
		}
	}()
	for _, m := range ms {
		start := time.Now()
		err := step(ctx, s.pc, o, m)
		if o.OnDone != nil {
			o.OnDone(m, time.Since(start), err)
		}
		if err != nil {
			return done, err
		}
		done = append(done, m)
	}
	return done, nil
}

// lockedSession takes a session, holds the apply lock, waiting applyLockWait
// for another apply to finish, and creates the ledger if it is missing.
func (t *Toolkit) lockedSession(ctx context.Context, o ApplyOptions) (*lockedApply, error) {
	c, err := t.schemaConn(o.Connection, o.Schema)
	if err != nil {
		return nil, err
	}
	if c.cfg.ReadOnly {
		return nil, &RefusedError{Code: CodeReadOnly, msg: fmt.Sprintf(
			"%s: connection %q is read_only and applies no migration", errPrefix, o.Connection)}
	}
	pc, err := c.session(ctx)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	// The released session's reset unlocks the advisory lock and undoes the
	// session-level timeout below.
	if err := c.takeApplyLock(ctx, pc, o); err != nil {
		pc.Release()
		return nil, err
	}
	return &lockedApply{c: c, pc: pc}, nil
}

// takeApplyLock waits for the apply lock and creates the ledger.
func (c *conn) takeApplyLock(ctx context.Context, pc session, o ApplyOptions) error {
	if _, err := pc.Exec(ctx, "SELECT set_config('statement_timeout', $1, false)", millis(applyLockWait)); err != nil {
		return c.failure(ctx, err)
	}
	if _, err := pc.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1), hashtext($2))", c.cfg.LedgerSchema, o.Schema); err != nil {
		if sqlState(err) == queryCanceled {
			return &FailedError{Code: CodeApplyLocked, Summary: fmt.Sprintf(
				"%s: another apply on connection %q schema %q held the migration lock for %s; try again when it finishes",
				errPrefix, o.Connection, o.Schema, applyLockWait)}
		}
		return c.failure(ctx, err)
	}
	if err := c.ensureLedger(ctx, pc); err != nil {
		return c.failure(ctx, err)
	}
	return nil
}

// execer runs a statement on a session or in a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// recordApplied writes a migration's ledger row.
func (c *conn) recordApplied(ctx context.Context, q execer, o ApplyOptions, m Migration, took time.Duration) error {
	_, err := q.Exec(ctx, "INSERT INTO "+c.ledger()+" (schema_name, version, name, checksum, applied_by, duration_ms) "+
		"VALUES ($1, $2, $3, $4, $5, $6)", o.Schema, m.Version, m.Name, m.Checksum, o.Actor, took.Milliseconds())
	return err //nolint:wrapcheck // read by its type
}

// applyOne applies one migration and writes its ledger row.
func (c *conn) applyOne(ctx context.Context, pc session, o ApplyOptions, m Migration) error {
	if err := checkMigrationSQL(m.Up, m.Transactional); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, refuse(err)))
	}
	start := time.Now()
	if !m.Transactional {
		if err := c.applyAlone(ctx, pc, o, m, start); err != nil {
			return migrationFailure(m.Version, c.failure(ctx, err))
		}
		return nil
	}
	tx, err := pc.Begin(ctx)
	if err != nil {
		return c.failure(ctx, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := c.prepareMigration(ctx, tx, o.Schema, o.Timeout); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	if err := script(ctx, tx, m.Up); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	if err := c.recordApplied(ctx, tx, o, m, time.Since(start)); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	return nil
}

// applyAlone runs a migration that cannot run in a transaction, then writes
// its ledger row.
func (c *conn) applyAlone(ctx context.Context, pc session, o ApplyOptions, m Migration, start time.Time) error {
	if err := c.prepareAlone(ctx, pc, o); err != nil {
		return err
	}
	if _, err := pc.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+o.Schema); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	if err := pc.Script(ctx, m.Up); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	return c.recordApplied(ctx, pc, o, m, time.Since(start))
}

// prepareAlone sets on the session what prepareMigration sets on a
// migration's transaction, for a migration run outside one.
func (c *conn) prepareAlone(ctx context.Context, pc session, o ApplyOptions) error {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = c.cfg.StatementTimeout
	}
	_, err := pc.Exec(ctx, "SELECT set_config('lock_timeout', $1, false), set_config('statement_timeout', $2, false), "+
		"set_config('search_path', $3, false)", millis(migrationLockTimeout), millis(timeout), o.Schema)
	return err //nolint:wrapcheck // read by its type
}

// revertOne reverts one migration and deletes its ledger row. A migration
// added with transactional=false is reverted the way it was applied (#2490):
// its down usually holds the statement that made it non-transactional (DROP
// INDEX CONCURRENTLY undoing CREATE INDEX CONCURRENTLY), which PostgreSQL
// refuses inside a transaction block.
func (c *conn) revertOne(ctx context.Context, pc session, o ApplyOptions, m Migration) error {
	if err := checkMigrationSQL(m.Down, m.Transactional); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, refuse(err)))
	}
	if !m.Transactional {
		if err := c.revertAlone(ctx, pc, o, m); err != nil {
			return migrationFailure(m.Version, c.failure(ctx, err))
		}
		return nil
	}
	tx, err := pc.Begin(ctx)
	if err != nil {
		return c.failure(ctx, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := c.prepareMigration(ctx, tx, o.Schema, o.Timeout); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	if err := script(ctx, tx, m.Down); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	if _, err := tx.Exec(ctx, "DELETE FROM "+c.ledger()+" WHERE schema_name = $1 AND version = $2", o.Schema, m.Version); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return migrationFailure(m.Version, c.failure(ctx, err))
	}
	return nil
}

// revertAlone runs a non-transactional migration's down on the session, then
// deletes its ledger row. Should the row's delete fail after the down ran, the
// ledger still lists the migration and a later down runs it again, which is
// why such a down is written with IF EXISTS.
func (c *conn) revertAlone(ctx context.Context, pc session, o ApplyOptions, m Migration) error {
	if err := c.prepareAlone(ctx, pc, o); err != nil {
		return err
	}
	if err := pc.Script(ctx, m.Down); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	_, err := pc.Exec(ctx, "DELETE FROM "+c.ledger()+" WHERE schema_name = $1 AND version = $2", o.Schema, m.Version)
	return err //nolint:wrapcheck // read by its type
}

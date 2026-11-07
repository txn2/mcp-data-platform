package postgres

// The plan a manage_schema plan reports: the pending migrations run in a
// transaction on a session of the target database, read and rolled back. The
// unit tests drive it through a fake session; the RealDB tests in
// schema_realdb_integration_test.go run it against PostgreSQL.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Lock is a relation lock a planned migration took.
type Lock struct {
	Relation string `json:"relation"`
	Mode     string `json:"mode"`
}

// Plan is what applying the pending migrations would do.
type Plan struct {
	// Migrations are the versions planned, in order.
	Migrations []int `json:"migrations"`
	// Changed are the relations and columns the migrations created or
	// altered, read from the system catalog rows the transaction wrote.
	Changed []string `json:"changed"`
	// Locks are the relation locks the transaction held at its end, with
	// their modes: the locks the apply takes.
	Locks []Lock `json:"locks"`
	// Unplanned are migrations that run outside a transaction, which cannot
	// be rolled back and so are not planned.
	Unplanned []int `json:"unplanned"`
}

// planChangesSQL reads the relations and columns whose catalog rows the
// current transaction wrote: what its DDL created or altered.
const planChangesSQL = `/* mcp-data-platform */ SELECT n.nspname || '.' || c.relname AS object FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.xmin::text = (txid_current() % 4294967296)::text AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
UNION
SELECT n.nspname || '.' || c.relname || '.' || a.attname FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE a.attnum > 0 AND a.xmin::text = (txid_current() % 4294967296)::text
AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
ORDER BY 1`

// planLocksSQL reads the relation locks this session holds, outside the
// system catalogs the reads themselves lock.
const planLocksSQL = `/* mcp-data-platform */ SELECT n.nspname || '.' || c.relname, l.mode FROM pg_locks l
JOIN pg_class c ON c.oid = l.relation JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE l.pid = pg_backend_pid() AND l.locktype = 'relation' AND l.granted
AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
ORDER BY 1, 2`

// PlanMigrations runs the pending migrations in one transaction, reads the
// objects they changed and the locks they took, and rolls back. PostgreSQL's
// DDL is transactional, so the plan is exactly what an apply would do,
// against the database as it is now.
func (t *Toolkit) PlanMigrations(ctx context.Context, connection, schema string, pending []Migration) (*Plan, error) {
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return nil, err
	}
	pc, err := c.session(ctx)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	defer pc.Release()
	tx, err := pc.Begin(ctx)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := c.prepareMigration(ctx, tx, schema, 0); err != nil {
		return nil, c.failure(ctx, err)
	}
	plan, err := c.runPlanned(ctx, tx, pending)
	if err != nil {
		return nil, err
	}
	if plan.Changed, err = readStrings(ctx, tx, planChangesSQL); err != nil {
		return nil, c.failure(ctx, err)
	}
	if plan.Locks, err = readLocks(ctx, tx); err != nil {
		return nil, c.failure(ctx, err)
	}
	return plan, nil
}

// runPlanned runs each pending migration that runs in a transaction, and
// lists the ones that do not as unplanned.
func (c *conn) runPlanned(ctx context.Context, tx pgx.Tx, pending []Migration) (*Plan, error) {
	plan := &Plan{Migrations: []int{}, Changed: []string{}, Locks: []Lock{}, Unplanned: []int{}}
	for _, m := range pending {
		if !m.Transactional {
			plan.Unplanned = append(plan.Unplanned, m.Version)
			continue
		}
		if err := checkMigrationSQL(m.Up, true); err != nil {
			return nil, migrationFailure(m.Version, refuse(err))
		}
		if err := script(ctx, tx, m.Up); err != nil {
			return nil, migrationFailure(m.Version, c.failure(ctx, err))
		}
		plan.Migrations = append(plan.Migrations, m.Version)
	}
	return plan, nil
}

// readStrings reads a one-column result.
func readStrings(ctx context.Context, tx pgx.Tx, sql string) ([]string, error) {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
		out = append(out, s)
	}
	return out, rows.Err() //nolint:wrapcheck // read by its type
}

// readLocks reads planLocksSQL.
func readLocks(ctx context.Context, tx pgx.Tx) ([]Lock, error) {
	rows, err := tx.Query(ctx, planLocksSQL)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	defer rows.Close()
	out := []Lock{}
	for rows.Next() {
		var l Lock
		if err := rows.Scan(&l.Relation, &l.Mode); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
		out = append(out, l)
	}
	return out, rows.Err() //nolint:wrapcheck // read by its type
}

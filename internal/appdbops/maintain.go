// Package appdbops is maintenance of an application database (#2410):
// ANALYZE, VACUUM and REINDEX, run outside a transaction as the connection's
// own login under a lock timeout. The admin API and the platform.maintain
// script binding call it; creating or dropping an index is refused, naming
// manage_schema.
package appdbops

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The maintenance commands.
const (
	CommandAnalyze    = "analyze"
	CommandVacuum     = "vacuum"
	CommandVacuumFull = "vacuum_full"
	CommandReindex    = "reindex"
)

// Commands are the maintenance commands, in the order they are documented.
var Commands = []string{CommandAnalyze, CommandVacuum, CommandVacuumFull, CommandReindex}

// DefaultLockTimeout is how long a maintenance statement waits for a lock
// before it gives up rather than queueing every statement behind it.
const DefaultLockTimeout = 5 * time.Second

// ErrSchemaChange refuses a command that would create or drop an index, or
// any other command: a change to the schema is a migration.
var ErrSchemaChange = errors.New("the maintenance commands are analyze, vacuum, vacuum_full and reindex; " +
	"creating or dropping an index is a schema change: add it as a migration with manage_schema")

// ErrTableRequired refuses vacuum_full and reindex of a whole database.
var ErrTableRequired = errors.New("vacuum_full and reindex act on one table; name it as table or schema.table")

// identPart is one part of a table name a maintenance command takes.
var identPart = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]{0,62}$`)

// Statement is the statement a maintenance command runs: ANALYZE, VACUUM
// with ANALYZE, VACUUM FULL (which holds an ACCESS EXCLUSIVE lock on the table
// while it rewrites it), or REINDEX TABLE CONCURRENTLY. table is optional for
// analyze and vacuum, which then act on the whole database.
func Statement(command, table string) (string, error) {
	target := ""
	if table != "" {
		parts := strings.Split(table, ".")
		if len(parts) > 2 {
			return "", fmt.Errorf("table %q is not a table or schema.table", table)
		}
		for _, p := range parts {
			if !identPart.MatchString(p) {
				return "", fmt.Errorf("table %q is not a table or schema.table", table)
			}
		}
		target = " " + pgx.Identifier(parts).Sanitize()
	}
	switch command {
	case CommandAnalyze:
		return "ANALYZE" + target, nil
	case CommandVacuum:
		return "VACUUM (ANALYZE)" + target, nil
	case CommandVacuumFull, CommandReindex:
		if target == "" {
			return "", ErrTableRequired
		}
		if command == CommandVacuumFull {
			return "VACUUM (FULL, ANALYZE)" + target, nil
		}
		return "REINDEX TABLE CONCURRENTLY" + target, nil
	}
	return "", ErrSchemaChange
}

// Maintainer runs one maintenance statement outside a transaction: the
// postgres toolkit's RunMaintenance.
type Maintainer interface {
	RunMaintenance(ctx context.Context, connection, stmt string, lockTimeout, timeout time.Duration) error
}

// Outcome is what a maintenance command did.
type Outcome struct {
	Connection string `json:"connection"`
	Command    string `json:"command"`
	Table      string `json:"table,omitempty"`
	// Statement is what ran.
	Statement  string `json:"statement"`
	DurationMS int64  `json:"duration_ms"`
}

// Request is one maintenance command on one connection.
type Request struct {
	Connection, Command string
	// Table is table or schema.table; analyze and vacuum act on the whole
	// database without one.
	Table string
	// LockTimeout is how long a lock is waited for (DefaultLockTimeout when
	// zero), Timeout how long the statement may run (the connection's
	// statement_timeout when zero).
	LockTimeout, Timeout time.Duration
}

// Maintain runs a maintenance command.
func Maintain(ctx context.Context, m Maintainer, req Request) (*Outcome, error) {
	stmt, err := Statement(req.Command, req.Table)
	if err != nil {
		return nil, err
	}
	lock := req.LockTimeout
	if lock <= 0 {
		lock = DefaultLockTimeout
	}
	start := time.Now()
	if err := m.RunMaintenance(ctx, req.Connection, stmt, lock, req.Timeout); err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's value-free summary is the text
	}
	return &Outcome{
		Connection: req.Connection, Command: req.Command, Table: req.Table, Statement: stmt,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

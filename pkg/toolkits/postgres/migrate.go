package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Migration is one versioned change to an application database's schema
// (#2404): the SQL that moves it up and, optionally, back down. The platform
// keeps it; the target's ledger records that it was applied.
type Migration struct {
	Connection string `json:"connection"`
	// Schema is the namespace the migration belongs to and runs in: its
	// search_path, created when the first migration applies.
	Schema  string `json:"schema"`
	Version int    `json:"version"`
	Name    string `json:"name"`
	Up      string `json:"up"`
	Down    string `json:"down,omitempty"`
	// Transactional is false for a migration holding a statement that cannot
	// run in a transaction (CREATE INDEX CONCURRENTLY), which runs alone.
	Transactional bool      `json:"transactional"`
	Checksum      string    `json:"checksum"`
	Author        string    `json:"author,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ChecksumOf is a migration's checksum: what it runs up and down, and whether
// it runs in a transaction.
func ChecksumOf(up, down string, transactional bool) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "up\x00%s\x00down\x00%s\x00tx\x00%t", up, down, transactional)
	return hex.EncodeToString(h.Sum(nil))
}

// Applied is one row of the target's ledger.
type Applied struct {
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	Checksum   string    `json:"checksum"`
	AppliedAt  time.Time `json:"applied_at"`
	AppliedBy  string    `json:"applied_by"`
	DurationMS int64     `json:"duration_ms"`
}

// schemaName is what a migration's namespace may be: an unquoted lowercase
// identifier, so it is written into search_path and CREATE SCHEMA as is.
var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// DefaultSchema is the namespace a migration belongs to when none is named.
const DefaultSchema = "public"

// Bounds on what an apply waits for.
const (
	// applyLockWait is how long an apply waits for another apply on the same
	// database to finish.
	applyLockWait = 30 * time.Second
	// migrationLockTimeout is how long one migration's statement waits for a
	// lock a running query holds, so a migration never queues behind a long
	// read while every later query queues behind it.
	migrationLockTimeout = 10 * time.Second
)

// ledger is the target's ledger table.
func (c *conn) ledger() string { return c.cfg.LedgerSchema + ".migrations" }

// ledgerReader runs the ledger's reads on a pool, a session or a transaction.
type ledgerReader interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// readLedger is what the ledger says was applied in schema, oldest first. A
// database with no ledger yet has applied nothing.
func (c *conn) readLedger(ctx context.Context, q ledgerReader, schema string) ([]Applied, error) {
	var exists bool
	if err := q.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", c.ledger()).Scan(&exists); err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	if !exists {
		return []Applied{}, nil
	}
	rows, err := q.Query(ctx, "SELECT version, name, checksum, applied_at, applied_by, duration_ms FROM "+
		c.ledger()+" WHERE schema_name = $1 ORDER BY version", schema)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	defer rows.Close()
	applied := []Applied{}
	for rows.Next() {
		var a Applied
		if err := rows.Scan(&a.Version, &a.Name, &a.Checksum, &a.AppliedAt, &a.AppliedBy, &a.DurationMS); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
		applied = append(applied, a)
	}
	return applied, rows.Err() //nolint:wrapcheck // read by its type
}

// Drift is a disagreement between the ledger and the migrations the platform
// holds, which stops an apply.
type Drift struct {
	Version int    `json:"version"`
	Reason  string `json:"reason"`
}

// Compare reads the ledger against the platform's migrations: the ones still
// to apply, in version order, and every disagreement. A migration the ledger
// holds with another checksum was changed after it was applied (or the
// ledger was edited by hand); a version the ledger holds that the platform
// does not was applied by something else.
func Compare(migrations []Migration, applied []Applied) (pending []Migration, drift []Drift) {
	byVersion := make(map[int]Migration, len(migrations))
	for _, m := range migrations {
		byVersion[m.Version] = m
	}
	done := make(map[int]bool, len(applied))
	latest := 0
	for _, a := range applied {
		done[a.Version] = true
		latest = max(latest, a.Version)
		m, ok := byVersion[a.Version]
		switch {
		case !ok:
			drift = append(drift, Drift{Version: a.Version, Reason: "the ledger records it as applied and the platform holds no such migration"})
		case m.Checksum != a.Checksum:
			drift = append(drift, Drift{Version: a.Version, Reason: "its checksum in the ledger differs from the migration the platform holds: " +
				"the applied migration or the ledger was changed, and a fix is a new migration"})
		}
	}
	pending = []Migration{}
	for _, m := range migrations {
		if done[m.Version] {
			continue
		}
		if m.Version < latest {
			drift = append(drift, Drift{Version: m.Version, Reason: fmt.Sprintf(
				"it is pending below version %d, which is applied; give it a version above the latest applied", latest)})
			continue
		}
		pending = append(pending, m)
	}
	slices.SortFunc(pending, func(a, b Migration) int { return a.Version - b.Version })
	return pending, drift
}

// LedgerStatus is what the ledger records, what is pending, and where the
// two disagree, for one connection and schema.
type LedgerStatus struct {
	Applied []Applied
	Pending []Migration
	Drift   []Drift
}

// Status reads the ledger and compares it with the platform's migrations.
func (t *Toolkit) Status(ctx context.Context, connection, schema string, migrations []Migration) (*LedgerStatus, error) {
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return nil, err
	}
	applied, err := c.readLedger(ctx, c.reader(), schema)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	pending, drift := Compare(migrations, applied)
	return &LedgerStatus{Applied: applied, Pending: pending, Drift: drift}, nil
}

// schemaConn is the connection a schema command acts on.
func (t *Toolkit) schemaConn(connection, schema string) (*conn, error) {
	c, ok := t.lookup(connection)
	if !ok {
		return nil, &RefusedError{Code: CodeUnknownConnection, msg: fmt.Sprintf(
			"%s: no application database named %q", errPrefix, connection)}
	}
	if !schemaName.MatchString(schema) {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf(
			"%s: schema %q is not a lowercase identifier", errPrefix, schema)}
	}
	if schema == c.cfg.LedgerSchema {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf(
			"%s: schema %q holds the migration ledger; name the application's own schema", errPrefix, schema)}
	}
	if strings.HasPrefix(schema, "pg_") || schema == "information_schema" {
		return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf(
			"%s: schema %q is PostgreSQL's own; name the application's schema", errPrefix, schema)}
	}
	return c, nil
}

func millis(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), decimal) }

// migrationError names the version a failure happened in.
type migrationError struct {
	version int
	err     error
}

func (e *migrationError) Error() string { return fmt.Sprintf("migration %d: %v", e.version, e.err) }
func (e *migrationError) Unwrap() error { return e.err }

// migrationFailure puts the version in a failure's value-free summary.
func migrationFailure(version int, err error) error {
	if f, ok := errors.AsType[*FailedError](err); ok {
		f.Summary = fmt.Sprintf("migration %d: %s", version, f.Summary)
		return f
	}
	return &migrationError{version: version, err: err}
}

// ApplyOptions is one apply or revert.
type ApplyOptions struct {
	Connection, Schema string
	// Actor is who applies, recorded in the ledger.
	Actor string
	// Timeout bounds each statement of a migration; zero is the
	// connection's statement timeout.
	Timeout time.Duration
	// OnDone is told each migration as it is applied or reverted, with how
	// long it took, or why it failed.
	OnDone func(m Migration, took time.Duration, err error)
	// UpTo stops an apply at this version; nil applies everything pending.
	UpTo *int
}

// revertMigrations are the migrations a revert undoes, newest first; one with
// no down SQL refuses the revert before anything runs.
func revertMigrations(applied []Applied, migrations []Migration, toVersion int) ([]Migration, error) {
	byVersion := make(map[int]Migration, len(migrations))
	for _, m := range migrations {
		byVersion[m.Version] = m
	}
	out := []Migration{}
	for _, v := range revertTargets(applied, toVersion) {
		m := byVersion[v]
		if m.Down == "" {
			return nil, &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf(
				"%s: migration %d has no down SQL, so it cannot be reverted; add a migration that undoes it", errPrefix, v)}
		}
		out = append(out, m)
	}
	return out, nil
}

// queryCanceled is the SQLSTATE a statement stopped by statement_timeout
// fails with.
const queryCanceled = "57014"

// Error codes a schema command is refused with.
const (
	CodeSchemaDrift = "schema_drift"
	CodeApplyLocked = "migration_locked"
)

// revertTargets are the applied versions a revert undoes, newest first.
func revertTargets(applied []Applied, toVersion int) []int {
	out := []int{}
	for _, a := range slices.Backward(applied) {
		v := a.Version
		if toVersion < 0 {
			return append(out, v)
		}
		if v > toVersion {
			out = append(out, v)
		}
	}
	return out
}

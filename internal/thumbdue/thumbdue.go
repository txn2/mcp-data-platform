// Package thumbdue is what the thumbnail stores share about the rows the
// renderer must look at (#2084). A row of resources, portal_assets or
// portal_collections carries thumbnail_due_at: when its owed rule next needs
// evaluating, NULL once the rule said it is owed nothing. A trigger sets it on
// every write the renderer did not make (migration 000189), and the stores
// read only due rows, through a partial index, so the cost of asking for work
// follows the rows that changed rather than the size of the library.
//
// What the trigger cannot see is the rule changing under rows nobody wrote: a
// new renderer generation, a changed source bound, a newly drawable type.
// Sweep marks a whole table due once per rule, in chunks, and records the rule
// it marked under in thumbnail_sweeps.
package thumbdue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// NotifyChannel is the channel the trigger notifies when a row becomes due.
const NotifyChannel = "thumbnail_due"

// A Table is one table the renderer draws from. Its value is the table name,
// and each carries the advisory lock its sweep runs under.
type Table string

// The tables the renderer draws from.
const (
	Resources   Table = "resources"
	Assets      Table = "portal_assets"
	Collections Table = "portal_collections"
)

// chunk is how many rows one statement of a sweep marks, so no statement
// holds row locks on more than this many rows at a time.
const chunk = 2000

// unlockTimeout bounds the advisory unlock after a sweep.
const unlockTimeout = 5 * time.Second

// The advisory locks the sweeps run under, one per table, so two replicas
// starting together sweep a table once.
const (
	lockResources   int64 = 4713210501
	lockAssets      int64 = 4713210502
	lockCollections int64 = 4713210503
)

// lockKey is the advisory lock a table's sweep runs under.
func (t Table) lockKey() (int64, error) {
	switch t {
	case Resources:
		return lockResources, nil
	case Assets:
		return lockAssets, nil
	case Collections:
		return lockCollections, nil
	}
	return 0, fmt.Errorf("thumbdue: %q is not a table the renderer draws from", string(t))
}

// Rule is the fingerprint of an owed rule: its SQL and the values bound into
// it, each as the driver sends it (a bound array is a pointer, whose address
// would change the fingerprint on every call). Two rules with the same
// fingerprint pass the same rows.
func Rule(query string, args ...any) string {
	h := sha256.New()
	_, _ = io.WriteString(h, query)
	for _, a := range args {
		if v, ok := a.(driver.Valuer); ok {
			if dv, err := v.Value(); err == nil {
				a = dv
			}
		}
		_, _ = fmt.Fprintf(h, "\x00%v", a)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Sweep marks every row of table due when rule is not the one it was last
// swept under, and records rule. It returns how many rows it marked: zero
// when the table was already swept under rule, or when another replica holds
// the sweep's lock (that replica records it). A row already due keeps its
// time; a claimed or held row is made due now and is still not claimed until
// its lease or hold passes.
func Sweep(ctx context.Context, db *sql.DB, table Table, rule string) (int64, error) {
	key, err := table.lockKey()
	if err != nil {
		return 0, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("thumbdue: acquiring a connection for the sweep: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		return 0, fmt.Errorf("thumbdue: taking the sweep lock: %w", err)
	}
	if !got {
		return 0, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
		defer cancel()
		if _, uerr := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key); uerr != nil {
			slog.WarnContext(ctx, "thumbnails: sweep unlock failed", "table", string(table), "error", logsan.SanitizeForLog(uerr.Error()))
		}
	}()
	return sweepLocked(ctx, conn, table, rule)
}

// sweepLocked is Sweep once its lock is held: mark the table unless it was
// swept under rule, then record rule.
func sweepLocked(ctx context.Context, conn *sql.Conn, table Table, rule string) (int64, error) {
	var recorded string
	err := conn.QueryRowContext(ctx, `SELECT rule FROM thumbnail_sweeps WHERE subject = $1`, string(table)).Scan(&recorded)
	if err != nil && err != sql.ErrNoRows { //nolint:errorlint // database/sql returns ErrNoRows unwrapped
		return 0, fmt.Errorf("thumbdue: reading the sweep record: %w", err)
	}
	if recorded == rule {
		return 0, nil
	}
	marked, err := markAll(ctx, conn, table)
	if err != nil {
		return marked, err
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO thumbnail_sweeps (subject, rule, swept_at) VALUES ($1, $2, now())
		ON CONFLICT (subject) DO UPDATE SET rule = EXCLUDED.rule, swept_at = EXCLUDED.swept_at`,
		string(table), rule); err != nil {
		return marked, fmt.Errorf("thumbdue: recording the sweep: %w", err)
	}
	return marked, nil
}

// markAll marks table due chunk rows at a time, walking its primary key.
func markAll(ctx context.Context, conn *sql.Conn, table Table) (int64, error) {
	query := markQuery(table)
	var marked int64
	after := ""
	for {
		var n int64
		var last sql.NullString
		// #nosec G701 -- table is one of the Table constants; every value is bound
		if err := conn.QueryRowContext(ctx, query, after, chunk).Scan(&n, &last); err != nil {
			return marked, fmt.Errorf("thumbdue: marking %s due: %w", string(table), err)
		}
		marked += n
		if n < chunk || !last.Valid {
			return marked, nil
		}
		after = last.String
	}
}

// markQuery marks the next chunk of rows after a key due, in primary-key
// order, and returns how many it marked and the last key.
func markQuery(table Table) string {
	return `WITH next AS (
			SELECT id FROM ` + string(table) + ` WHERE id > $1 ORDER BY id LIMIT $2
		), marked AS (
			UPDATE ` + string(table) + ` t
			   SET thumbnail_due_at = LEAST(COALESCE(t.thumbnail_due_at, now()), now())
			  FROM next WHERE t.id = next.id
			RETURNING t.id
		)
		SELECT count(*), max(id) FROM marked`
}

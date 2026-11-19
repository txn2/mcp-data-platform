// Package assetstatus is an asset's status (#2502): draft, published or
// deprecated, with an optional note a deprecated asset shows its readers, and
// the history of every change to an asset's status or verification (who,
// when, from, to).
//
// A status is informational. It changes no access: who can open an asset is
// decided by ownership and shares exactly as before, and nothing here reads a
// status to decide anything. A new asset has no status until someone sets one.
//
// The store writes portal_asset_status and portal_asset_mark_events; the asset
// store reads the status back beside every asset row it scans
// (internal/portal/portalstore). The routes are mounted by pkg/portal through
// its Mounts hook, and the asset toolkit writes through the same Store.
package assetstatus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// The kinds of change the history records.
const (
	KindStatus       = "status"
	KindVerification = "verification"
)

// maxHistory bounds one history read.
const maxHistory = 200

// ErrInvalid is returned for a change the store refuses before writing.
var ErrInvalid = errors.New("invalid status")

// Event is one recorded change to an asset's status or verification.
type Event struct {
	Kind  string    `json:"kind" example:"status"`
	From  string    `json:"from,omitempty" example:"draft"`
	To    string    `json:"to,omitempty" example:"published"`
	Note  string    `json:"note,omitempty"`
	Actor string    `json:"actor" example:"alice@example.com"`
	At    time.Time `json:"at"`
}

// Change is a status to set, its note and who sets it.
type Change struct {
	Status string
	Note   string
	Actor  string
}

// Normalize trims the change and validates it: a known status, a note within
// portaldomain.MaxMarkNoteLen, and an actor.
func (c Change) Normalize() (Change, error) {
	c.Status = strings.ToLower(strings.TrimSpace(c.Status))
	c.Note = strings.TrimSpace(c.Note)
	switch {
	case !portaldomain.ValidStatus(c.Status):
		return c, fmt.Errorf("status must be draft, published or deprecated: %w", ErrInvalid)
	case len(c.Note) > portaldomain.MaxMarkNoteLen:
		return c, fmt.Errorf("note is longer than %d characters: %w", portaldomain.MaxMarkNoteLen, ErrInvalid)
	case strings.TrimSpace(c.Actor) == "":
		return c, fmt.Errorf("no actor: %w", ErrInvalid)
	}
	return c, nil
}

// Store writes an asset's status and reads its history.
type Store interface {
	// Set sets the asset's status and records the change.
	Set(ctx context.Context, assetID string, c Change) error
	// Clear removes the asset's status, reporting whether it had one.
	Clear(ctx context.Context, assetID, actor string) (bool, error)
	// History is the asset's recorded changes, newest first.
	History(ctx context.Context, assetID string, limit int) ([]Event, error)
}

// Postgres is the PostgreSQL Store.
type Postgres struct{ db *sql.DB }

// New returns the PostgreSQL store over db.
func New(db *sql.DB) *Postgres { return &Postgres{db: db} }

// Execer is what RecordEvent writes through: a pool or a transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const insertEventSQL = `INSERT INTO portal_asset_mark_events (asset_id, kind, from_value, to_value, note, actor)
	VALUES ($1, $2, $3, $4, $5, $6)`

// RecordEvent records one change to an asset's marks. The verification store
// records through it, so the history is one table read one way.
func RecordEvent(ctx context.Context, ex Execer, assetID string, e Event) error {
	if _, err := ex.ExecContext(ctx, insertEventSQL, assetID, e.Kind, e.From, e.To, e.Note, e.Actor); err != nil {
		return fmt.Errorf("recording %s change: %w", e.Kind, err)
	}
	return nil
}

const (
	lockStatusSQL   = `SELECT status, note FROM portal_asset_status WHERE asset_id = $1 FOR UPDATE`
	upsertStatusSQL = `INSERT INTO portal_asset_status (asset_id, status, note, set_by, set_at)
	VALUES ($1, $2, $3, $4, NOW())
	ON CONFLICT (asset_id) DO UPDATE SET status = EXCLUDED.status, note = EXCLUDED.note,
		set_by = EXCLUDED.set_by, set_at = EXCLUDED.set_at`
	deleteStatusSQL = `DELETE FROM portal_asset_status WHERE asset_id = $1 RETURNING status`
	historySQL      = `SELECT kind, from_value, to_value, note, actor, at FROM portal_asset_mark_events
	WHERE asset_id = $1 ORDER BY at DESC, id DESC LIMIT $2`
)

// Set implements Store. Setting the status an asset already has with the same
// note writes nothing.
func (p *Postgres) Set(ctx context.Context, assetID string, c Change) error {
	c, err := c.Normalize()
	if err != nil {
		return err
	}
	return p.inTx(ctx, func(tx *sql.Tx) error {
		var prev, prevNote string
		err := tx.QueryRowContext(ctx, lockStatusSQL, assetID).Scan(&prev, &prevNote)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("reading status: %w", err)
		}
		if prev == c.Status && prevNote == c.Note {
			return nil
		}
		if _, err := tx.ExecContext(ctx, upsertStatusSQL, assetID, c.Status, c.Note, c.Actor); err != nil {
			return fmt.Errorf("setting status: %w", err)
		}
		return RecordEvent(ctx, tx, assetID, Event{Kind: KindStatus, From: prev, To: c.Status, Note: c.Note, Actor: c.Actor})
	})
}

// Clear implements Store.
func (p *Postgres) Clear(ctx context.Context, assetID, actor string) (bool, error) {
	var had bool
	err := p.inTx(ctx, func(tx *sql.Tx) error {
		var prev string
		err := tx.QueryRowContext(ctx, deleteStatusSQL, assetID).Scan(&prev)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("clearing status: %w", err)
		}
		had = true
		return RecordEvent(ctx, tx, assetID, Event{Kind: KindStatus, From: prev, Actor: actor})
	})
	return had, err
}

// History implements Store.
func (p *Postgres) History(ctx context.Context, assetID string, limit int) ([]Event, error) {
	if limit <= 0 || limit > maxHistory {
		limit = maxHistory
	}
	rows, err := p.db.QueryContext(ctx, historySQL, assetID, limit)
	if err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cleanup
	events := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Kind, &e.From, &e.To, &e.Note, &e.Actor, &e.At); err != nil {
			return nil, fmt.Errorf("scanning history: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating history: %w", err)
	}
	return events, nil
}

// inTx runs fn in a transaction, committing when it returns nil.
func (p *Postgres) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

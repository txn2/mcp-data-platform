// Package whimap is the imap input of a webhook source (#2247): the platform
// polls a mailbox and files each new message from an allowed, authenticated
// sender as one event, written exactly as the HTTP receiver writes a request's
// events. Compaction into Parquet, the source's table, retention and alerts
// then treat it like any other source. Attachments are written as managed
// resources the event names.
//
// Nothing here is a tool. The mail reaches a model or a script only through
// the source's table and the resources, like every webhook's data.
package whimap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Mark is where one folder of a source has been read to, and what its polls
// found.
type Mark struct {
	Source      string `json:"-"`
	Folder      string `json:"folder"`
	UIDValidity uint32 `json:"uid_validity"`
	LastUID     uint32 `json:"last_uid"`
	Stored      int64  `json:"stored"`
	// HiddenNotAllowed and HiddenUnauthenticated count the messages the
	// sender rules withheld. Nothing else of them is kept.
	HiddenNotAllowed      int64      `json:"hidden_not_allowed"`
	HiddenUnauthenticated int64      `json:"hidden_unauthenticated"`
	LastPolledAt          *time.Time `json:"last_polled_at,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
}

// Marks stores the marks in webhook_imap_marks.
type Marks struct {
	db *sql.DB
}

// NewMarks returns the store, nil without a database.
func NewMarks(db *sql.DB) *Marks {
	if db == nil {
		return nil
	}
	return &Marks{db: db}
}

// Get returns a folder's mark, zero when it has never been read.
func (m *Marks) Get(ctx context.Context, source, folder string) (Mark, error) {
	mk := Mark{Source: source, Folder: folder}
	var validity, last int64
	err := m.db.QueryRowContext(ctx,
		`SELECT uid_validity, last_uid, stored, hidden_not_allowed, hidden_unauthenticated, last_polled_at, last_error
		   FROM webhook_imap_marks WHERE source = $1 AND folder = $2`, source, folder).
		Scan(&validity, &last, &mk.Stored, &mk.HiddenNotAllowed, &mk.HiddenUnauthenticated, &mk.LastPolledAt, &mk.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return mk, nil
	}
	if err != nil {
		return mk, fmt.Errorf("reading the imap mark of %s %s: %w", source, folder, err)
	}
	mk.UIDValidity, mk.LastUID = uint32(validity), uint32(last) // #nosec G115 -- stored from uint32 values
	return mk, nil
}

// Put writes a folder's mark.
func (m *Marks) Put(ctx context.Context, mk Mark) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO webhook_imap_marks (source, folder, uid_validity, last_uid, stored, hidden_not_allowed,
		                                 hidden_unauthenticated, last_polled_at, last_error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (source, folder) DO UPDATE SET
		   uid_validity = EXCLUDED.uid_validity, last_uid = EXCLUDED.last_uid, stored = EXCLUDED.stored,
		   hidden_not_allowed = EXCLUDED.hidden_not_allowed, hidden_unauthenticated = EXCLUDED.hidden_unauthenticated,
		   last_polled_at = EXCLUDED.last_polled_at, last_error = EXCLUDED.last_error`,
		mk.Source, mk.Folder, int64(mk.UIDValidity), int64(mk.LastUID), mk.Stored, mk.HiddenNotAllowed,
		mk.HiddenUnauthenticated, mk.LastPolledAt, mk.LastError)
	if err != nil {
		return fmt.Errorf("writing the imap mark of %s %s: %w", mk.Source, mk.Folder, err)
	}
	return nil
}

// List returns a source's marks, by folder.
func (m *Marks) List(ctx context.Context, source string) ([]Mark, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT folder, uid_validity, last_uid, stored, hidden_not_allowed, hidden_unauthenticated, last_polled_at, last_error
		   FROM webhook_imap_marks WHERE source = $1 ORDER BY folder`, source)
	if err != nil {
		return nil, fmt.Errorf("listing the imap marks of %s: %w", source, err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup
	out := []Mark{}
	for rows.Next() {
		mk := Mark{Source: source}
		var validity, last int64
		if err := rows.Scan(&mk.Folder, &validity, &last, &mk.Stored, &mk.HiddenNotAllowed,
			&mk.HiddenUnauthenticated, &mk.LastPolledAt, &mk.LastError); err != nil {
			return nil, fmt.Errorf("scanning an imap mark: %w", err)
		}
		mk.UIDValidity, mk.LastUID = uint32(validity), uint32(last) // #nosec G115 -- stored from uint32 values
		out = append(out, mk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating imap marks: %w", err)
	}
	return out, nil
}

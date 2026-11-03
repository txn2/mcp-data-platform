// Package whpgstate keeps the state of each postgres webhook source's listener
// (#2408) in webhook_pg_listeners: which replica listens and since when, what
// it filed and refused, and its last failure, so a source's page on any
// replica shows how its listener is going. Nothing of a payload is kept.
package whpgstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// State is how a postgres source's listener stands: which replica listens,
// since when, what it filed and refused, and its last failure.
type State struct {
	// Replica is the replica listening, empty when none is.
	Replica        string     `json:"replica"`
	ListeningSince *time.Time `json:"listening_since,omitempty"`
	LastEventAt    *time.Time `json:"last_event_at,omitempty"`
	Delivered      int64      `json:"delivered"`
	Rejected       int64      `json:"rejected"`
	LastError      string     `json:"last_error,omitempty"`
	LastErrorAt    *time.Time `json:"last_error_at,omitempty"`
}

// States keeps each source's state in webhook_pg_listeners. A write for a
// source deleted meanwhile changes nothing: the row went with the source.
type States struct {
	db *sql.DB
}

// New returns the store, nil without a database.
func New(db *sql.DB) *States {
	if db == nil {
		return nil
	}
	return &States{db: db}
}

// errState wraps every failure of a state write or read.
const errState = "webhook postgres listener state: %w"

// exec runs a write that names a source by $1, ignoring a source that no
// longer exists.
func (s *States) exec(ctx context.Context, query string, args ...any) error {
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil && !isMissingSource(err) {
		return fmt.Errorf(errState, err)
	}
	return nil
}

// Listening records replica as the source's listener from since, clearing
// the last failure.
func (s *States) Listening(ctx context.Context, source, replica string, since time.Time) error {
	return s.exec(ctx, `INSERT INTO webhook_pg_listeners (source, replica, listening_since, last_error)
		VALUES ($1, $2, $3, '')
		ON CONFLICT (source) DO UPDATE SET replica = EXCLUDED.replica, listening_since = EXCLUDED.listening_since, last_error = ''`,
		source, replica, since)
}

// Stopped records that replica no longer listens, unless another replica
// took over meanwhile.
func (s *States) Stopped(ctx context.Context, source, replica string) error {
	return s.exec(ctx, `UPDATE webhook_pg_listeners SET replica = '', listening_since = NULL
		WHERE source = $1 AND replica = $2`, source, replica)
}

// Filed adds what one delivery filed and refused.
func (s *States) Filed(ctx context.Context, source string, delivered, rejected int64, at time.Time) error {
	return s.exec(ctx, `UPDATE webhook_pg_listeners
		SET delivered = delivered + $2, rejected = rejected + $3,
		    last_event_at = CASE WHEN $2 > 0 THEN $4 ELSE last_event_at END
		WHERE source = $1`, source, delivered, rejected, at)
}

// Failed records a listener's failure.
func (s *States) Failed(ctx context.Context, source, msg string, at time.Time) error {
	return s.exec(ctx, `INSERT INTO webhook_pg_listeners (source, last_error, last_error_at) VALUES ($1, $2, $3)
		ON CONFLICT (source) DO UPDATE SET last_error = EXCLUDED.last_error, last_error_at = EXCLUDED.last_error_at`,
		source, msg, at)
}

// Get returns a source's state, the zero value when no replica has listened.
func (s *States) Get(ctx context.Context, source string) (State, error) {
	var st State
	err := s.db.QueryRowContext(ctx, `SELECT replica, listening_since, last_event_at, delivered, rejected, last_error, last_error_at
		FROM webhook_pg_listeners WHERE source = $1`, source).
		Scan(&st.Replica, &st.ListeningSince, &st.LastEventAt, &st.Delivered, &st.Rejected, &st.LastError, &st.LastErrorAt)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf(errState, err)
	}
	return st, nil
}

// foreignKeyViolation is the SQLSTATE of a row naming a source that does not
// exist.
const foreignKeyViolation = "23503"

// isMissingSource reports a write refused because its source was deleted.
func isMissingSource(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == foreignKeyViolation
}

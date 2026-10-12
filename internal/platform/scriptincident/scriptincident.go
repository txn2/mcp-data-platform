// Package scriptincident holds each automation's failure incident (#2041):
// the failed scheduled run that opens it is the one its owner and the
// operator's channels are told of, the failed runs while it stays open are
// counted rather than sent, and the run that succeeds closes it and is told as
// the recovery. The rows are script_failure_incidents.
package scriptincident

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Opening is the failed run that may open a script's incident.
type Opening struct {
	ScriptID string
	RunID    string
	Cause    string
	// Error is the line of the run's error that names the failure: its last.
	Error    string
	FailedAt time.Time
}

// Incident is a script's failure incident as it stood when it closed.
type Incident struct {
	OpenedAt     time.Time
	OpeningRunID string
	OpeningCause string
	OpeningError string
	FailedRuns   int
	LastFailedAt time.Time
}

// Store holds each script's open incident.
type Store interface {
	// Open records a failed run, reporting whether it opened the incident
	// (true) or was counted on one already open (false).
	Open(ctx context.Context, o Opening) (bool, error)
	// Close ends the script's incident and returns it, or nil when none was
	// open.
	Close(ctx context.Context, scriptID string) (*Incident, error)
}

// postgresStore is script_failure_incidents.
type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates the store over db.
func NewPostgresStore(db *sql.DB) Store {
	return &postgresStore{db: db}
}

// openIncidentSQL inserts the incident or counts the run on the open one, in
// one statement. xmax is zero on the row version an INSERT wrote and set on
// the one an ON CONFLICT UPDATE wrote, which is what tells the two apart.
const openIncidentSQL = `
INSERT INTO script_failure_incidents
       (script_id, opened_at, opening_run_id, opening_cause, opening_error, failed_runs, last_failed_at)
VALUES ($1, $2, $3, $4, $5, 1, $2)
ON CONFLICT (script_id) DO UPDATE
   SET failed_runs    = script_failure_incidents.failed_runs + 1,
       last_failed_at = EXCLUDED.last_failed_at
RETURNING (xmax = 0)`

// Open implements Store.
func (s *postgresStore) Open(ctx context.Context, o Opening) (bool, error) {
	var opened bool
	if err := s.db.QueryRowContext(ctx, openIncidentSQL,
		o.ScriptID, o.FailedAt.UTC(), o.RunID, o.Cause, o.Error).Scan(&opened); err != nil {
		return false, fmt.Errorf("recording a failed automation run: %w", err)
	}
	return opened, nil
}

const closeIncidentSQL = `
DELETE FROM script_failure_incidents
 WHERE script_id = $1
RETURNING opened_at, opening_run_id, opening_cause, opening_error, failed_runs, last_failed_at`

// Close implements Store. The delete is the claim: of two replicas
// closing one script's incident, one is returned the row.
func (s *postgresStore) Close(ctx context.Context, scriptID string) (*Incident, error) {
	var in Incident
	err := s.db.QueryRowContext(ctx, closeIncidentSQL, scriptID).Scan(
		&in.OpenedAt, &in.OpeningRunID, &in.OpeningCause, &in.OpeningError, &in.FailedRuns, &in.LastFailedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no incident was open
	}
	if err != nil {
		return nil, fmt.Errorf("closing an automation's failure incident: %w", err)
	}
	return &in, nil
}

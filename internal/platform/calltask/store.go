package calltask

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Task is one call_tasks row.
type Task struct {
	ID            string
	ToolName      string
	UserID        string
	Status        string
	StatusMessage string
	// Result is the tool's result in its wire form, on a completed task.
	Result json.RawMessage
	// Error is the JSON-RPC error, on a failed task.
	Error           json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
	HeartbeatAt     time.Time
	CancelRequested bool
	ExpiresAt       time.Time
}

// Ending is how a call ended: its terminal status, the message the task
// carries, and the tool result or the JSON-RPC error, in wire form.
type Ending struct {
	Status  string
	Message string
	Result  json.RawMessage
	Error   json.RawMessage
}

// errTaskNotFound is a lookup for an id no row has.
var errTaskNotFound = errors.New("call task not found")

// Store is the call_tasks persistence the runner and the Source use.
type Store interface {
	// Create inserts a working task.
	Create(ctx context.Context, t Task) error
	// Get reads one task, or errTaskNotFound.
	Get(ctx context.Context, id string) (*Task, error)
	// Finish ends a working task; a task that already ended is left as it
	// is.
	Finish(ctx context.Context, id string, end Ending) error
	// RequestCancel records a request to stop a working task.
	RequestCancel(ctx context.Context, id string) error
	// Heartbeat stamps the working tasks ids name as alive, and returns the
	// ones among them a cancel was requested for.
	Heartbeat(ctx context.Context, ids []string) ([]string, error)
	// FailStale fails a working task whose heartbeat is older than before,
	// reporting whether it did.
	FailStale(ctx context.Context, id string, before time.Time, message string, rpcErr json.RawMessage) (bool, error)
}

// PostgresStore is the Store over the call_tasks table.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore returns the Store over db.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Create inserts a working task.
func (s *PostgresStore) Create(ctx context.Context, t Task) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO call_tasks (id, tool_name, user_id, status, status_message, created_at, updated_at, heartbeat_at, expires_at)
		VALUES ($1, $2, $3, 'working', $4, $5, $5, $5, $6)`,
		t.ID, t.ToolName, t.UserID, t.StatusMessage, t.CreatedAt, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("inserting call task: %w", err)
	}
	return nil
}

// Get reads one task.
func (s *PostgresStore) Get(ctx context.Context, id string) (*Task, error) {
	var t Task
	var result, rpcErr []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tool_name, user_id, status, status_message, result, error,
		       created_at, updated_at, heartbeat_at, cancel_requested_at IS NOT NULL, expires_at
		  FROM call_tasks
		 WHERE id = $1`, id).Scan(
		&t.ID, &t.ToolName, &t.UserID, &t.Status, &t.StatusMessage, &result, &rpcErr,
		&t.CreatedAt, &t.UpdatedAt, &t.HeartbeatAt, &t.CancelRequested, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading call task: %w", err)
	}
	t.Result, t.Error = result, rpcErr
	return &t, nil
}

// Finish ends a working task.
func (s *PostgresStore) Finish(ctx context.Context, id string, end Ending) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE call_tasks
		   SET status = $2, status_message = $3, result = $4, error = $5, updated_at = now()
		 WHERE id = $1 AND status = 'working'`,
		id, end.Status, end.Message, nullJSON(end.Result), nullJSON(end.Error))
	if err != nil {
		return fmt.Errorf("finishing call task: %w", err)
	}
	return nil
}

// RequestCancel records a request to stop a working task.
func (s *PostgresStore) RequestCancel(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE call_tasks
		   SET cancel_requested_at = COALESCE(cancel_requested_at, now()), updated_at = now()
		 WHERE id = $1 AND status = 'working'`, id)
	if err != nil {
		return fmt.Errorf("requesting a call task's cancel: %w", err)
	}
	return nil
}

// Heartbeat stamps working tasks as alive and returns the ones to cancel.
func (s *PostgresStore) Heartbeat(ctx context.Context, ids []string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE call_tasks
		   SET heartbeat_at = now()
		 WHERE id = ANY($1) AND status = 'working'
		RETURNING id, cancel_requested_at IS NOT NULL`, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("stamping call task heartbeats: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor; rows.Err below reports a failed read
	cancel := make([]string, 0)
	for rows.Next() {
		var id string
		var requested bool
		if err := rows.Scan(&id, &requested); err != nil {
			return nil, fmt.Errorf("reading a call task heartbeat: %w", err)
		}
		if requested {
			cancel = append(cancel, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading call task heartbeats: %w", err)
	}
	return cancel, nil
}

// FailStale fails a working task whose replica stopped reporting.
func (s *PostgresStore) FailStale(ctx context.Context, id string, before time.Time, message string, rpcErr json.RawMessage) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE call_tasks
		   SET status = 'failed', status_message = $3, error = $4, updated_at = now()
		 WHERE id = $1 AND status = 'working' AND heartbeat_at < $2`,
		id, before, message, nullJSON(rpcErr))
	if err != nil {
		return false, fmt.Errorf("failing a stale call task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("counting a stale call task: %w", err)
	}
	return n > 0, nil
}

// nullJSON stores an absent document as NULL.
func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

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
	// IdempotencyKey, Background and Progress are a background export's
	// (#2212): the key its caller reads it again by, the mark that it is one,
	// and how far its walk has come.
	IdempotencyKey string
	Background     bool
	Progress       json.RawMessage
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
	// CreateBackground inserts a working background export, or returns the
	// one already working for its caller, tool and key with created false.
	CreateBackground(ctx context.Context, t Task) (existing *Task, created bool, err error)
	// FindByKey reads the caller's latest task of tool under key, or
	// errTaskNotFound.
	FindByKey(ctx context.Context, userID, tool, key string) (*Task, error)
	// SetProgress records how far a working task has come.
	SetProgress(ctx context.Context, id string, progress json.RawMessage) error
	// EndedBackground reads the caller's background exports that ended after
	// since, newest first, at most limit.
	EndedBackground(ctx context.Context, userID string, since time.Time, limit int) ([]Task, error)
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
	return scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM call_tasks WHERE id = $1`, id))
}

// taskColumns are the columns scanTask reads, in its order.
const taskColumns = `id, tool_name, user_id, status, status_message, result, error,
		       created_at, updated_at, heartbeat_at, cancel_requested_at IS NOT NULL, expires_at,
		       COALESCE(idempotency_key, ''), background, progress`

// rowScanner is a *sql.Row or *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// scanTask reads one row of taskColumns.
func scanTask(row rowScanner) (*Task, error) {
	var t Task
	var result, rpcErr, progress []byte
	err := row.Scan(
		&t.ID, &t.ToolName, &t.UserID, &t.Status, &t.StatusMessage, &result, &rpcErr,
		&t.CreatedAt, &t.UpdatedAt, &t.HeartbeatAt, &t.CancelRequested, &t.ExpiresAt,
		&t.IdempotencyKey, &t.Background, &progress)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading call task: %w", err)
	}
	t.Result, t.Error, t.Progress = result, rpcErr, progress
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

// workingKeyIndex holds one working run per caller, tool and key (000206).
const workingKeyIndex = "idx_call_tasks_working_key"

// CreateBackground inserts a working background export, or returns the one
// already working under its caller, tool and key.
func (s *PostgresStore) CreateBackground(ctx context.Context, t Task) (*Task, bool, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO call_tasks (id, tool_name, user_id, status, status_message, created_at, updated_at,
		                        heartbeat_at, expires_at, idempotency_key, background)
		VALUES ($1, $2, $3, 'working', $4, $5, $5, $5, $6, $7, TRUE)`,
		t.ID, t.ToolName, t.UserID, t.StatusMessage, t.CreatedAt, t.ExpiresAt, t.IdempotencyKey)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && string(pqErr.Code) == pgUniqueViolation && pqErr.Constraint == workingKeyIndex {
		existing, findErr := s.FindByKey(ctx, t.UserID, t.ToolName, t.IdempotencyKey)
		if findErr != nil {
			return nil, false, findErr
		}
		return existing, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inserting background export: %w", err)
	}
	return &t, true, nil
}

// pgUniqueViolation is PostgreSQL's unique_violation code.
const pgUniqueViolation = "23505"

// FindByKey reads the caller's latest task of tool under key.
func (s *PostgresStore) FindByKey(ctx context.Context, userID, tool, key string) (*Task, error) {
	return scanTask(s.db.QueryRowContext(ctx, `
		SELECT `+taskColumns+` FROM call_tasks
		 WHERE user_id = $1 AND tool_name = $2 AND idempotency_key = $3
		 ORDER BY created_at DESC
		 LIMIT 1`, userID, tool, key))
}

// SetProgress records how far a working task has come.
func (s *PostgresStore) SetProgress(ctx context.Context, id string, progress json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE call_tasks SET progress = $2, updated_at = now()
		 WHERE id = $1 AND status = 'working'`, id, nullJSON(progress))
	if err != nil {
		return fmt.Errorf("recording a call task's progress: %w", err)
	}
	return nil
}

// EndedBackground reads the caller's background exports that ended after
// since.
func (s *PostgresStore) EndedBackground(ctx context.Context, userID string, since time.Time, limit int) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+taskColumns+` FROM call_tasks
		 WHERE user_id = $1 AND background AND status <> 'working' AND updated_at > $2
		 ORDER BY updated_at DESC
		 LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("reading ended background exports: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor; rows.Err below reports a failed read
	out := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading ended background exports: %w", err)
	}
	return out, nil
}

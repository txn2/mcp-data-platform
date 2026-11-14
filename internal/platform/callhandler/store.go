package callhandler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// ErrNotFound is a handler that does not exist.
var ErrNotFound = errors.New("no such call handler")

// MaxWindow is the longest window a count reaches back over; counts older
// than it are swept.
const MaxWindow = 31 * 24 * time.Hour

// FailureRetention is how long a failure is kept.
const FailureRetention = 7 * 24 * time.Hour

// handlerColumns are a handler's columns in the order scanHandler reads them.
const handlerColumns = `id, script_id, kind, connection, operations, personas, users, conditions, args,
	timeout_ms, on_failure, items_path, writes, protected, ord, enabled, created_by, updated_by, created_at, updated_at`

const (
	insertSQL = `INSERT INTO call_handlers (script_id, kind, connection, operations, personas, users, conditions, args,
		timeout_ms, on_failure, items_path, writes, protected, ord, enabled, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16)
		RETURNING id, created_at, updated_at`
	updateSQL = `UPDATE call_handlers SET connection = $2, operations = $3, personas = $4, users = $5, conditions = $6,
		args = $7, timeout_ms = $8, on_failure = $9, items_path = $10, writes = $11, protected = $12, ord = $13,
		updated_by = $14, updated_at = NOW()
		WHERE id = $1 RETURNING updated_at`
	getSQL       = `SELECT ` + handlerColumns + ` FROM call_handlers WHERE id = $1`
	byScriptSQL  = `SELECT ` + handlerColumns + ` FROM call_handlers WHERE script_id = $1 ORDER BY kind, ord, created_at`
	allSQL       = `SELECT ` + handlerColumns + ` FROM call_handlers ORDER BY kind, ord, created_at`
	enabledSQL   = `SELECT ` + handlerColumns + ` FROM call_handlers WHERE enabled ORDER BY ord, created_at`
	enableSQL    = `UPDATE call_handlers SET enabled = $2, updated_by = $3, updated_at = NOW() WHERE id = $1`
	deleteSQL    = `DELETE FROM call_handlers WHERE id = $1`
	countLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1 || ':' || $2, 0))`
	countAddSQL  = `INSERT INTO call_handler_counts (handler_id, key, call_id, amount) VALUES ($1, $2, $3, $4)`
	countSumSQL  = `SELECT COALESCE(SUM(amount), 0) FROM call_handler_counts
		WHERE handler_id = $1 AND key = $2 AND at > NOW() - make_interval(secs => $3)`
	releaseSQL     = `DELETE FROM call_handler_counts WHERE call_id = $1`
	sweepCountsSQL = `DELETE FROM call_handler_counts WHERE at < $1`
	insertFailSQL  = `INSERT INTO call_handler_failures (handler_id, version, user_email, call, prints, error, outcome, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	failuresSQL = `SELECT id, handler_id, version, user_email, call, prints, error, outcome, duration_ms, at
		FROM call_handler_failures WHERE handler_id = $1 ORDER BY at DESC LIMIT $2`
	sweepFailSQL = `DELETE FROM call_handler_failures WHERE at < $1`
)

// Store keeps handlers, their counts and their failures in PostgreSQL.
type Store struct {
	DB *sql.DB
}

// NewStore is the store over db, nil without one.
func NewStore(db *sql.DB) *Store {
	if db == nil {
		return nil
	}
	return &Store{DB: db}
}

// Insert writes a new handler, filling its id and times.
func (s *Store) Insert(ctx context.Context, h *Handler) error {
	enc, err := encodeJSON(h)
	if err != nil {
		return err
	}
	row := s.DB.QueryRowContext(ctx, insertSQL, h.ScriptID, h.Kind, h.Connection, pq.Array(h.Operations),
		pq.Array(h.Personas), pq.Array(h.Users), enc.cond, enc.args, h.TimeoutMS, h.OnFailure, h.ItemsPath, enc.writes,
		pq.Array(h.Protected), h.Order, h.Enabled, h.CreatedBy)
	if err := row.Scan(&h.ID, &h.CreatedAt, &h.UpdatedAt); err != nil {
		return fmt.Errorf("inserting call handler: %w", err)
	}
	h.UpdatedBy = h.CreatedBy
	return nil
}

// Update writes a handler's match and settings; its kind, script and state
// are not changed here.
func (s *Store) Update(ctx context.Context, h *Handler) error {
	enc, err := encodeJSON(h)
	if err != nil {
		return err
	}
	row := s.DB.QueryRowContext(ctx, updateSQL, h.ID, h.Connection, pq.Array(h.Operations), pq.Array(h.Personas),
		pq.Array(h.Users), enc.cond, enc.args, h.TimeoutMS, h.OnFailure, h.ItemsPath, enc.writes, pq.Array(h.Protected), h.Order, h.UpdatedBy)
	if err := row.Scan(&h.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("updating call handler: %w", err)
	}
	return nil
}

// Get is one handler.
func (s *Store) Get(ctx context.Context, id string) (*Handler, error) {
	rows, err := s.DB.QueryContext(ctx, getSQL, id)
	if err != nil {
		return nil, fmt.Errorf("reading call handler: %w", err)
	}
	list, err := scanHandlers(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// ByScript is every handler a script holds.
func (s *Store) ByScript(ctx context.Context, scriptID string) ([]Handler, error) {
	return s.query(ctx, byScriptSQL, scriptID)
}

// All is every handler, enabled or not.
func (s *Store) All(ctx context.Context) ([]Handler, error) {
	return s.query(ctx, allSQL)
}

// Enabled is every enabled handler: what an Index holds.
func (s *Store) Enabled(ctx context.Context) ([]Handler, error) {
	return s.query(ctx, enabledSQL)
}

func (s *Store) query(ctx context.Context, q string, args ...any) ([]Handler, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("reading call handlers: %w", err)
	}
	return scanHandlers(rows)
}

// SetEnabled turns a handler on or off.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool, by string) error {
	res, err := s.DB.ExecContext(ctx, enableSQL, id, enabled, by)
	if err != nil {
		return fmt.Errorf("turning call handler %s: %w", id, err)
	}
	return oneRow(res)
}

// Delete removes a handler, its counts and its failures.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		return fmt.Errorf("deleting call handler %s: %w", id, err)
	}
	return oneRow(res)
}

func oneRow(res sql.Result) error {
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// Tally is one count a policy makes: amount for CallID under the handler's
// Key, totalled over Window.
type Tally struct {
	HandlerID, Key, CallID string
	Amount                 int
	Window                 time.Duration
}

// Count adds the tally's amount for its call under the handler's key and
// returns the total over the window, this call included. The read and the
// write are one transaction under a lock on the key, so two replicas counting
// one key count one after the other.
func (s *Store) Count(ctx context.Context, t Tally) (int, error) {
	total, err := s.tally(ctx, t)
	if err != nil {
		return 0, fmt.Errorf("counting: %w", err)
	}
	return total, nil
}

func (s *Store) tally(ctx context.Context, t Tally) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err //nolint:wrapcheck // Count wraps it
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, countLockSQL, t.HandlerID, t.Key); err != nil {
		return 0, err //nolint:wrapcheck // Count wraps it
	}
	if t.Amount > 0 {
		if _, err := tx.ExecContext(ctx, countAddSQL, t.HandlerID, t.Key, t.CallID, t.Amount); err != nil {
			return 0, err //nolint:wrapcheck // Count wraps it
		}
	}
	var total int
	if err := tx.QueryRowContext(ctx, countSumSQL, t.HandlerID, t.Key, t.Window.Seconds()).Scan(&total); err != nil {
		return 0, err //nolint:wrapcheck // Count wraps it
	}
	return total, tx.Commit() //nolint:wrapcheck // Count wraps it
}

// Release removes what callID counted: a call the handlers did not let run
// is not counted against anyone.
func (s *Store) Release(ctx context.Context, callID string) error {
	if _, err := s.DB.ExecContext(ctx, releaseSQL, callID); err != nil {
		return fmt.Errorf("releasing counts: %w", err)
	}
	return nil
}

// Failure is what a policy or transform was given and printed when it failed.
type Failure struct {
	ID         string         `json:"id"`
	HandlerID  string         `json:"handler_id"`
	Version    int            `json:"version"`
	User       string         `json:"user"`
	Call       map[string]any `json:"call"`
	Prints     string         `json:"prints"`
	Error      string         `json:"error"`
	Outcome    string         `json:"outcome"`
	DurationMS int            `json:"duration_ms"`
	At         time.Time      `json:"at"`
}

// RecordFailure keeps one failure.
func (s *Store) RecordFailure(ctx context.Context, f Failure) error {
	call, err := json.Marshal(f.Call)
	if err != nil {
		return fmt.Errorf("encoding the failed call: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, insertFailSQL, f.HandlerID, f.Version, f.User, call, f.Prints, f.Error, f.Outcome, f.DurationMS); err != nil {
		return fmt.Errorf("recording a handler failure: %w", err)
	}
	return nil
}

// Failures is a handler's newest failures, at most limit.
func (s *Store) Failures(ctx context.Context, handlerID string, limit int) ([]Failure, error) {
	rows, err := s.DB.QueryContext(ctx, failuresSQL, handlerID, limit)
	if err != nil {
		return nil, fmt.Errorf("reading handler failures: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Failure{}
	for rows.Next() {
		var f Failure
		var call []byte
		if err := rows.Scan(&f.ID, &f.HandlerID, &f.Version, &f.User, &call, &f.Prints, &f.Error, &f.Outcome, &f.DurationMS, &f.At); err != nil {
			return nil, fmt.Errorf("reading handler failures: %w", err)
		}
		_ = json.Unmarshal(call, &f.Call)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading handler failures: %w", err)
	}
	return out, nil
}

// Sweep removes counts older than MaxWindow and failures older than
// FailureRetention, as of now.
func (s *Store) Sweep(ctx context.Context, now time.Time) error {
	if _, err := s.DB.ExecContext(ctx, sweepCountsSQL, now.Add(-MaxWindow)); err != nil {
		return fmt.Errorf("sweeping handler counts: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, sweepFailSQL, now.Add(-FailureRetention)); err != nil {
		return fmt.Errorf("sweeping handler failures: %w", err)
	}
	return nil
}

// encoded is a handler's JSON columns.
type encoded struct{ cond, args, writes []byte }

func encodeJSON(h *Handler) (encoded, error) {
	var e encoded
	conds := h.Conditions
	if conds == nil {
		conds = []Condition{}
	}
	var err error
	if e.cond, err = json.Marshal(conds); err != nil {
		return e, fmt.Errorf("encoding conditions: %w", err)
	}
	if e.args, err = json.Marshal(nonNilMap(h.Args)); err != nil {
		return e, fmt.Errorf("encoding args: %w", err)
	}
	w := h.Writes
	if w == nil {
		w = map[string]bool{}
	}
	if e.writes, err = json.Marshal(w); err != nil {
		return e, fmt.Errorf("encoding writes: %w", err)
	}
	return e, nil
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func scanHandlers(rows *sql.Rows) ([]Handler, error) {
	defer func() { _ = rows.Close() }()
	out := []Handler{}
	for rows.Next() {
		var h Handler
		var cond, args, writes []byte
		if err := rows.Scan(&h.ID, &h.ScriptID, &h.Kind, &h.Connection, pq.Array(&h.Operations), pq.Array(&h.Personas),
			pq.Array(&h.Users), &cond, &args, &h.TimeoutMS, &h.OnFailure, &h.ItemsPath, &writes, pq.Array(&h.Protected),
			&h.Order, &h.Enabled, &h.CreatedBy, &h.UpdatedBy, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, fmt.Errorf("reading call handlers: %w", err)
		}
		_ = json.Unmarshal(cond, &h.Conditions)
		_ = json.Unmarshal(args, &h.Args)
		_ = json.Unmarshal(writes, &h.Writes)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading call handlers: %w", err)
	}
	return out, nil
}

// Stat is one handler's last day on its calls, read from the audit rows of
// the calls it matched.
type Stat struct {
	Matched int     `json:"matched"`
	Denied  int     `json:"denied"`
	Held    int     `json:"held"`
	Errors  int     `json:"errors"`
	Queued  int     `json:"queued"`
	P50MS   float64 `json:"p50_ms"`
	P99MS   float64 `json:"p99_ms"`
}

// statsSQL is every handler's last 24 hours on one connection's calls, from
// the handlers each audit row records.
const statsSQL = `SELECT h->>'id',
	COUNT(*),
	COUNT(*) FILTER (WHERE h->>'outcome' = 'deny'),
	COUNT(*) FILTER (WHERE h->>'outcome' = 'require_approval'),
	COUNT(*) FILTER (WHERE h->>'outcome' = 'error'),
	COUNT(*) FILTER (WHERE h->>'outcome' = 'queued'),
	COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY (h->>'ms')::float8), 0),
	COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY (h->>'ms')::float8), 0)
	FROM audit_logs a, jsonb_array_elements(a.handlers) h
	WHERE a.handlers IS NOT NULL AND a.timestamp > NOW() - INTERVAL '24 hours' AND a.connection = $1
	GROUP BY h->>'id'`

// Stats is every handler's last day on a connection's calls, by handler id.
func (s *Store) Stats(ctx context.Context, connection string) (map[string]Stat, error) {
	rows, err := s.DB.QueryContext(ctx, statsSQL, connection)
	if err != nil {
		return nil, fmt.Errorf("reading handler counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Stat{}
	for rows.Next() {
		var id string
		var st Stat
		if err := rows.Scan(&id, &st.Matched, &st.Denied, &st.Held, &st.Errors, &st.Queued, &st.P50MS, &st.P99MS); err != nil {
			return nil, fmt.Errorf("reading handler counts: %w", err)
		}
		out[id] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading handler counts: %w", err)
	}
	return out, nil
}

// orderSQL sets one handler's place among its connection's transforms.
const orderSQL = `UPDATE call_handlers SET ord = $2, updated_by = $3, updated_at = NOW() WHERE id = $1 AND kind = 'transform'`

// SetOrder sets the order transforms run in: each id's place in ids.
func (s *Store) SetOrder(ctx context.Context, ids []string, by string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ordering transforms: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, orderSQL, id, i+1, by); err != nil {
			return fmt.Errorf("ordering transforms: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ordering transforms: %w", err)
	}
	return nil
}

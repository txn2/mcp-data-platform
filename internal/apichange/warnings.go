package apichange

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Warning kinds (#2486).
const (
	WarningDeprecation = "deprecation"
	WarningSunset      = "sunset"
	WarningRemoved     = "removed"
)

// maxDetail bounds a header value kept as a warning's detail.
const maxDetail = 120

// Warning is what an upstream said about one operation of a connection.
type Warning struct {
	Connection  string     `json:"connection"`
	Operation   string     `json:"operation"`
	Kind        string     `json:"kind"`
	Detail      string     `json:"detail,omitempty"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	SeenCount   int64      `json:"seen_count"`
	NotifiedAt  *time.Time `json:"notified_at,omitempty"`
}

// WarningNotifier sends a new warning to the channels an administrator chose.
type WarningNotifier interface {
	Warned(ctx context.Context, w Warning)
}

// Throttles: a replica writes a success at most once per successEvery for one
// operation, a warning it already wrote at most once per warnEvery, and a
// run's call of an operation once per callerEvery.
const (
	successEvery = time.Hour
	warnEvery    = 10 * time.Minute
	callerEvery  = 10 * time.Minute
	// maxWrites bounds the writes in flight; an answer past it is passed
	// over, so a burst of calls never queues work behind the upstream.
	maxWrites = 8
	// maxThrottleKeys is the size at which the throttle map is swept.
	maxThrottleKeys = 10000
	// writeTimeout bounds one write, apart from the call that caused it.
	writeTimeout = 5 * time.Second
)

// Warnings records upstream warnings from the answers the outbound chain
// hands it (upstreamwarn.Observer). Writes leave the request at once: each
// runs on its own goroutine, bounded, and throttled per replica so a busy
// operation costs a write an hour.
type Warnings struct {
	DB     *sql.DB
	Notify WarningNotifier

	now   func() time.Time
	slots chan struct{}

	mu   sync.Mutex
	last map[string]time.Time
}

// NewWarnings builds the recorder.
func NewWarnings(db *sql.DB, notify WarningNotifier) *Warnings {
	return &Warnings{DB: db, Notify: notify, slots: make(chan struct{}, maxWrites), last: map[string]time.Time{}}
}

func (w *Warnings) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

// due reports whether key has not been written within every, and marks it.
func (w *Warnings) due(key string, every time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.clock()
	if at, ok := w.last[key]; ok && now.Sub(at) < every {
		return false
	}
	if len(w.last) >= maxThrottleKeys {
		w.sweep(now)
	}
	w.last[key] = now
	return true
}

// sweep forgets the keys past the longest throttle window, which no longer
// hold anything back. Called with mu held, when the map reaches its bound:
// the callers' keys carry a run's session, so a replica that ran for months
// would otherwise keep one per run.
func (w *Warnings) sweep(now time.Time) {
	for k, at := range w.last {
		if now.Sub(at) >= successEvery {
			delete(w.last, k)
		}
	}
}

// Observe reads one answer: a 2xx records the operation's success, a
// Deprecation or Sunset header a warning, and a 404 or 410 a removal when the
// operation answered 2xx before.
func (w *Warnings) Observe(ctx context.Context, connection, operation string, status int, header http.Header) {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		if w.due("ok\x00"+connection+"\x00"+operation, successEvery) {
			w.write(ctx, func(ctx context.Context) error { return w.success(ctx, connection, operation) })
		}
	case status == http.StatusNotFound || status == http.StatusGone:
		if w.due(WarningRemoved+"\x00"+connection+"\x00"+operation, warnEvery) {
			detail := fmt.Sprintf("answered %d", status)
			w.write(ctx, func(ctx context.Context) error { return w.removal(ctx, connection, operation, detail) })
		}
	}
	w.observeHeaders(ctx, connection, operation, header)
}

// observeHeaders records a Deprecation or Sunset header an answer carried.
func (w *Warnings) observeHeaders(ctx context.Context, connection, operation string, header http.Header) {
	for kind, name := range map[string]string{WarningDeprecation: "Deprecation", WarningSunset: "Sunset"} {
		value := header.Get(name)
		if value == "" || !w.due(kind+"\x00"+connection+"\x00"+operation, warnEvery) {
			continue
		}
		detail := cleanDetail(value)
		w.write(ctx, func(ctx context.Context) error { return w.warn(ctx, connection, operation, kind, detail) })
	}
}

// cleanDetail is a header value as a warning keeps it.
func cleanDetail(v string) string {
	v = clean(v)
	if len(v) > maxDetail {
		v = strings.ToValidUTF8(v[:maxDetail], "")
	}
	return v
}

// write runs fn on its own goroutine when a slot is free.
func (w *Warnings) write(ctx context.Context, fn func(context.Context) error) {
	select {
	case w.slots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-w.slots }()
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
		defer cancel()
		if err := fn(wctx); err != nil {
			slog.WarnContext(wctx, "api warnings: recording an upstream answer failed",
				"error", logsan.SanitizeForLog(err.Error()))
		}
	}()
}

const successSQL = `INSERT INTO api_operation_successes (connection, operation, last_success_at)
	VALUES ($1, $2, NOW())
	ON CONFLICT (connection, operation) DO UPDATE SET last_success_at = NOW()`

func (w *Warnings) success(ctx context.Context, connection, operation string) error {
	if _, err := w.DB.ExecContext(ctx, successSQL, connection, operation); err != nil {
		return fmt.Errorf("recording the success of %s: %w", operation, err)
	}
	return nil
}

// removalSQL records a removal only for an operation that answered 2xx
// before: a 404 from one that never worked is a bad request, not a removal.
const removalSQL = `INSERT INTO api_operation_warnings (connection, operation, kind, detail)
	SELECT $1, $2, 'removed', $3
	WHERE EXISTS (SELECT 1 FROM api_operation_successes WHERE connection = $1 AND operation = $2)
	ON CONFLICT (connection, operation, kind) DO UPDATE SET
	    detail = EXCLUDED.detail, last_seen_at = NOW(),
	    seen_count = api_operation_warnings.seen_count + 1
	RETURNING (xmax = 0)`

func (w *Warnings) removal(ctx context.Context, connection, operation, detail string) error {
	return w.upsert(ctx, removalSQL, []any{connection, operation, detail}, Warning{
		Connection: connection, Operation: operation, Kind: WarningRemoved, Detail: detail,
	})
}

// warnSQL records a header warning. RETURNING (xmax = 0) is true only for the
// statement that inserted the row, which is the one that notifies, so a
// warning is sent once across replicas.
const warnSQL = `INSERT INTO api_operation_warnings (connection, operation, kind, detail)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (connection, operation, kind) DO UPDATE SET
	    detail = EXCLUDED.detail, last_seen_at = NOW(),
	    seen_count = api_operation_warnings.seen_count + 1
	RETURNING (xmax = 0)`

func (w *Warnings) warn(ctx context.Context, connection, operation, kind, detail string) error {
	return w.upsert(ctx, warnSQL, []any{connection, operation, kind, detail}, Warning{
		Connection: connection, Operation: operation, Kind: kind, Detail: detail,
	})
}

const notifiedSQL = `UPDATE api_operation_warnings SET notified_at = NOW()
	WHERE connection = $1 AND operation = $2 AND kind = $3`

// upsert writes a warning with stmt and notifies when the write created it.
func (w *Warnings) upsert(ctx context.Context, stmt string, args []any, wr Warning) error {
	var created bool
	err := w.DB.QueryRowContext(ctx, stmt, args...).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // a removal of an operation that never answered 2xx
	}
	if err != nil {
		return fmt.Errorf("recording a %s warning on %s: %w", wr.Kind, wr.Operation, err)
	}
	if !created {
		return nil
	}
	now := w.clock().UTC()
	wr.FirstSeenAt, wr.LastSeenAt, wr.SeenCount = now, now, 1
	if w.Notify != nil {
		w.Notify.Warned(ctx, wr)
	}
	if _, err := w.DB.ExecContext(ctx, notifiedSQL, wr.Connection, wr.Operation, wr.Kind); err != nil {
		return fmt.Errorf("stamping the %s warning on %s as sent: %w", wr.Kind, wr.Operation, err)
	}
	return nil
}

const listWarningsSQL = `SELECT connection, operation, kind, detail, first_seen_at, last_seen_at, seen_count, notified_at
	FROM api_operation_warnings WHERE connection = $1 ORDER BY last_seen_at DESC, operation, kind`

// ListWarnings returns a connection's warnings, the most recent first.
func ListWarnings(ctx context.Context, db *sql.DB, connection string) ([]Warning, error) {
	rows, err := db.QueryContext(ctx, listWarningsSQL, connection)
	if err != nil {
		return nil, fmt.Errorf("listing the warnings of %s: %w", connection, err)
	}
	defer func() { _ = rows.Close() }()
	out := []Warning{}
	for rows.Next() {
		var wr Warning
		var notified sql.NullTime
		if err := rows.Scan(&wr.Connection, &wr.Operation, &wr.Kind, &wr.Detail, &wr.FirstSeenAt,
			&wr.LastSeenAt, &wr.SeenCount, &notified); err != nil {
			return nil, fmt.Errorf("scanning a warning of %s: %w", connection, err)
		}
		if notified.Valid {
			wr.NotifiedAt = &notified.Time
		}
		out = append(out, wr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the warnings of %s: %w", connection, err)
	}
	return out, nil
}

// OperationKeyer names the catalog operation an API call addresses: its
// operation id, or "METHOD template", or "" when the catalog declares none
// (the API gateway toolkits).
type OperationKeyer func(ctx context.Context, connection, method, path, operationID string) string

// OperationKeyOf asks the API gateway toolkits all lists which catalog
// operation a call addresses; all is read on every call, since connections
// come and go while the platform runs.
func OperationKeyOf(all func() []registry.Toolkit) OperationKeyer {
	if all == nil {
		return nil
	}
	return func(ctx context.Context, connection, method, path, operationID string) string {
		for _, tk := range all() {
			k, ok := tk.(interface {
				OperationKey(ctx context.Context, connection, method, path, operationID string) string
			})
			if !ok {
				continue
			}
			if op := k.OperationKey(ctx, connection, method, path, operationID); op != "" {
				return op
			}
		}
		return ""
	}
}

// apiTools are the tools whose calls address an API operation.
var apiTools = map[string]bool{"api_invoke_endpoint": true, "api_export": true}

// Callers is an audit decorator recording which script called which API
// operation in a run (#2485). Only the run's script, the connection and the
// operation are kept, never the call's arguments.
type Callers struct {
	inner audit.Logger
	db    *sql.DB
	key   OperationKeyer
	w     *Warnings // its throttle and bounded writes
}

// NewCallers wraps inner.
func NewCallers(inner audit.Logger, db *sql.DB, key OperationKeyer) *Callers {
	return &Callers{inner: inner, db: db, key: key, w: NewWarnings(db, nil)}
}

// callerSQL records the script whose run made the call; the run id is the
// event's session, and a draft, which has no run row, records nothing.
const callerSQL = `INSERT INTO api_operation_callers (connection, operation, script_id, last_seen_at)
	SELECT $1, $2, r.script_id::text, NOW() FROM script_runs r WHERE r.id = $3
	ON CONFLICT (connection, operation, script_id) DO UPDATE SET last_seen_at = NOW()`

// Log writes the event, then records the operation a script run's API call
// addressed. The audit write's outcome is what is returned.
func (c *Callers) Log(ctx context.Context, event audit.Event) error {
	err := c.inner.Log(ctx, event)
	if !apiTools[event.ToolName] || !strings.HasPrefix(event.UserID, script.PrincipalPrefix) ||
		event.Connection == "" || event.SessionID == "" || c.key == nil {
		return err //nolint:wrapcheck // the audit store's error, unchanged
	}
	method, _ := event.Parameters["method"].(string)
	path, _ := event.Parameters["path"].(string)
	opID, _ := event.Parameters["operation_id"].(string)
	op := c.key(ctx, event.Connection, method, path, opID)
	if op == "" || !c.w.due("caller\x00"+event.Connection+"\x00"+op+"\x00"+event.SessionID, callerEvery) {
		return err //nolint:wrapcheck // the audit store's error, unchanged
	}
	conn, session := event.Connection, event.SessionID
	c.w.write(ctx, func(ctx context.Context) error {
		if _, werr := c.db.ExecContext(ctx, callerSQL, conn, op, session); werr != nil {
			return fmt.Errorf("recording a script's call of %s: %w", op, werr)
		}
		return nil
	})
	return err //nolint:wrapcheck // the audit store's error, unchanged
}

// Query passes through to the audit store.
func (c *Callers) Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	return c.inner.Query(ctx, filter) //nolint:wrapcheck // a pass-through
}

// Close passes through to the audit store.
func (c *Callers) Close() error { return c.inner.Close() } //nolint:wrapcheck // a pass-through

// pruneSQL forgets run-recorded callers not seen in the retention window.
const pruneSQL = `DELETE FROM api_operation_callers WHERE last_seen_at < NOW() - make_interval(days => $1)`

// callerRetentionDays is how long a run-recorded caller is listed after the
// last run that made the call.
const callerRetentionDays = 90

// PruneCallers forgets callers no run has made within the retention window.
func PruneCallers(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, pruneSQL, callerRetentionDays); err != nil {
		return fmt.Errorf("pruning api operation callers: %w", err)
	}
	return nil
}

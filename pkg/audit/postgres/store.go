// Package postgres provides PostgreSQL storage for audit logs.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// auditMaintenanceLockKey is the PostgreSQL advisory-lock key that
// serializes the audit maintenance tick across replicas. Multi-pod
// deployments race to acquire this lock at the start of each tick;
// exactly one wins and runs the partition rotation + retention DELETE,
// the rest skip until next tick. The value is FNV-1a of a stable
// namespace string so it does not collide with other advisory-lock
// users in the same database.
var auditMaintenanceLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("mcp-data-platform:audit_logs:maintenance"))
	return int64(h.Sum64()) //nolint:gosec // G115: postgres advisory lock keys are bigint; bit pattern preserved
}()

const (
	defaultRetentionDays = 90
	defaultQueryCapacity = 100
	maxQueryCapacity     = 10000
)

// SQL column names for the audit_logs table. Defined as constants so
// the same literal does not appear repeatedly across the column list,
// predicates, and ORDER BY clauses inside this package.
const (
	colTimestamp    = "timestamp"
	colUserID       = "user_id"
	colToolName     = "tool_name"
	colDurationMS   = "duration_ms"
	colPersona      = "persona"
	colConnection   = "connection"
	colSessionID    = "session_id"
	colToolkitKind  = "toolkit_kind"
	colErrorMessage = "error_message"
	colUserEmail    = "user_email"
	colSuccess      = "success"
	colSource       = "source"
)

// psq is the PostgreSQL statement builder with dollar placeholders.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// auditColumns lists columns returned by audit SELECT queries.
var auditColumns = []string{
	"id", colTimestamp, colDurationMS, "request_id", colSessionID,
	colUserID, colUserEmail, colPersona, colToolName, colToolkitKind,
	"toolkit_name", colConnection, "parameters", colSuccess, colErrorMessage,
	"response_chars", "request_chars", "content_blocks",
	"transport", "source", "enrichment_applied",
	"enrichment_tokens_full", "enrichment_tokens_dedup",
	"enrichment_mode", "enrichment_match_kind", "authorized",
}

// Store implements audit.Logger using PostgreSQL.
type Store struct {
	db            *sql.DB
	retentionDays int
	cancel        context.CancelFunc
	done          chan struct{}
}

// Config configures the PostgreSQL audit store.
type Config struct {
	RetentionDays int
}

// New creates a new PostgreSQL audit store.
func New(db *sql.DB, cfg Config) *Store {
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = defaultRetentionDays
	}
	return &Store{
		db:            db,
		retentionDays: cfg.RetentionDays,
	}
}

// Log records an audit event.
func (s *Store) Log(ctx context.Context, event audit.Event) error {
	params, err := json.Marshal(event.Parameters)
	if err != nil {
		params = []byte("{}")
	}

	query := `
		INSERT INTO audit_logs
		(id, timestamp, duration_ms, request_id, session_id, user_id, user_email, persona, tool_name, toolkit_kind, toolkit_name, connection, parameters, success, error_message, created_date, response_chars, request_chars, content_blocks, transport, source, enrichment_applied, enrichment_tokens_full, enrichment_tokens_dedup, enrichment_mode, enrichment_match_kind, authorized)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)
	`

	_, err = s.db.ExecContext(ctx, query,
		event.ID,
		event.Timestamp,
		event.DurationMS,
		event.RequestID,
		event.SessionID,
		event.UserID,
		event.UserEmail,
		event.Persona,
		event.ToolName,
		event.ToolkitKind,
		event.ToolkitName,
		event.Connection,
		params,
		event.Success,
		event.ErrorMessage,
		event.Timestamp.Format("2006-01-02"),
		event.ResponseChars,
		event.RequestChars,
		event.ContentBlocks,
		event.Transport,
		event.Source,
		event.EnrichmentApplied,
		event.EnrichmentTokensFull,
		event.EnrichmentTokensDedup,
		event.EnrichmentMode,
		event.EnrichmentMatchKind,
		event.Authorized,
	)
	if err != nil {
		return fmt.Errorf("inserting audit log: %w", err)
	}

	return nil
}

// applyAuditFilter adds filter conditions to a SELECT builder.
func applyAuditFilter(qb sq.SelectBuilder, filter audit.QueryFilter) sq.SelectBuilder {
	for _, eq := range []struct{ col, val string }{
		{"id", filter.ID},
		{colUserID, filter.UserID},
		{colSessionID, filter.SessionID},
		{colToolName, filter.ToolName},
		{colToolkitKind, filter.ToolkitKind},
		{colSource, filter.Source},
	} {
		if eq.val != "" {
			qb = qb.Where(sq.Eq{eq.col: eq.val})
		}
	}
	if filter.StartTime != nil {
		qb = qb.Where(sq.GtOrEq{colTimestamp: *filter.StartTime})
	}
	if filter.EndTime != nil {
		qb = qb.Where(sq.LtOrEq{colTimestamp: *filter.EndTime})
	}
	if filter.Success != nil {
		qb = qb.Where(sq.Eq{colSuccess: *filter.Success})
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		qb = qb.Where(sq.Or{
			sq.ILike{colUserID: like},
			sq.ILike{colToolName: like},
			sq.ILike{colToolkitKind: like},
			sq.ILike{colConnection: like},
			sq.ILike{colPersona: like},
			sq.ILike{colErrorMessage: like},
		})
	}
	return qb
}

// Query retrieves audit events matching the filter.
func (s *Store) Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	qb := applyAuditFilter(psq.Select(auditColumns...).From("audit_logs"), filter)

	orderCol := colTimestamp
	orderDir := "DESC"
	if filter.SortBy != "" && audit.ValidSortColumns[filter.SortBy] {
		orderCol = filter.SortBy
	}
	if filter.SortOrder == audit.SortAsc {
		orderDir = "ASC"
	}
	qb = qb.OrderBy(orderCol + " " + orderDir)
	if filter.Limit > 0 {
		qb = qb.Limit(uint64(filter.Limit))
	}
	if filter.Offset > 0 {
		qb = qb.Offset(uint64(filter.Offset))
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building audit query: %w", err)
	}

	return s.executeQuery(ctx, query, args, filter.Limit)
}

// Count returns the number of audit events matching the filter.
func (s *Store) Count(ctx context.Context, filter audit.QueryFilter) (int, error) {
	qb := applyAuditFilter(psq.Select("COUNT(*)").From("audit_logs"), filter)

	query, args, err := qb.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting audit logs: %w", err)
	}
	return count, nil
}

// Distinct returns sorted unique values for the given column, scoped by optional time range.
func (s *Store) Distinct(ctx context.Context, column string, startTime, endTime *time.Time) ([]string, error) {
	allowed := map[string]bool{
		colUserID:      true,
		colToolName:    true,
		colToolkitKind: true,
		colSource:      true,
	}
	if !allowed[column] {
		return nil, fmt.Errorf("distinct not supported for column %q", column)
	}

	qb := psq.Select("DISTINCT " + column).From("audit_logs").OrderBy(column)
	if startTime != nil {
		qb = qb.Where(sq.GtOrEq{colTimestamp: *startTime})
	}
	if endTime != nil {
		qb = qb.Where(sq.LtOrEq{colTimestamp: *endTime})
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building distinct query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying distinct %s: %w", column, err)
	}
	defer func() { _ = rows.Close() }()

	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scanning distinct %s: %w", column, err)
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating distinct %s: %w", column, err)
	}
	return values, nil
}

// DistinctPairs returns a mapping of col1 values to col2 values. When col2 is
// empty for a row, the row is skipped. This is used to map e.g. user_id to
// user_email for display labels.
func (s *Store) DistinctPairs(ctx context.Context, col1, col2 string, startTime, endTime *time.Time) (map[string]string, error) {
	allowed := map[string]bool{colUserID: true, colUserEmail: true}
	if !allowed[col1] || !allowed[col2] {
		return nil, fmt.Errorf("distinct pairs not supported for columns %q, %q", col1, col2)
	}

	qb := psq.Select("DISTINCT " + col1 + ", " + col2).From("audit_logs").
		Where(sq.NotEq{col2: ""}).OrderBy(col1)
	if startTime != nil {
		qb = qb.Where(sq.GtOrEq{colTimestamp: *startTime})
	}
	if endTime != nil {
		qb = qb.Where(sq.LtOrEq{colTimestamp: *endTime})
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building distinct pairs query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying distinct pairs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]string)
	for rows.Next() {
		var v1, v2 string
		if err := rows.Scan(&v1, &v2); err != nil {
			return nil, fmt.Errorf("scanning distinct pair: %w", err)
		}
		result[v1] = v2
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating distinct pairs: %w", err)
	}
	return result, nil
}

func (s *Store) executeQuery(ctx context.Context, query string, args []any, limit int) ([]audit.Event, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying audit logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	allocCap := defaultQueryCapacity
	if limit > 0 && limit <= maxQueryCapacity {
		allocCap = limit
	}
	events := make([]audit.Event, 0, allocCap)

	for rows.Next() {
		event, err := s.scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating audit log rows: %w", err)
	}

	return events, nil
}

func (*Store) scanEvent(rows *sql.Rows) (audit.Event, error) {
	var event audit.Event
	var params []byte

	err := rows.Scan(
		&event.ID,
		&event.Timestamp,
		&event.DurationMS,
		&event.RequestID,
		&event.SessionID,
		&event.UserID,
		&event.UserEmail,
		&event.Persona,
		&event.ToolName,
		&event.ToolkitKind,
		&event.ToolkitName,
		&event.Connection,
		&params,
		&event.Success,
		&event.ErrorMessage,
		&event.ResponseChars,
		&event.RequestChars,
		&event.ContentBlocks,
		&event.Transport,
		&event.Source,
		&event.EnrichmentApplied,
		&event.EnrichmentTokensFull,
		&event.EnrichmentTokensDedup,
		&event.EnrichmentMode,
		&event.EnrichmentMatchKind,
		&event.Authorized,
	)
	if err != nil {
		return event, fmt.Errorf("scanning audit log row: %w", err)
	}

	if len(params) > 0 {
		_ = json.Unmarshal(params, &event.Parameters)
	}

	return event, nil
}

// Close cancels the cleanup goroutine and waits for it to exit.
// It is safe to call Close even if StartCleanupRoutine was never called.
func (s *Store) Close() error {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	return nil
}

// Cleanup removes audit logs older than retention period.
func (s *Store) Cleanup(ctx context.Context) error {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	query := `DELETE FROM audit_logs WHERE timestamp < $1`
	_, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return fmt.Errorf("cleaning up audit logs: %w", err)
	}
	return nil
}

// partitionsAheadDefault controls how far in advance the cleanup loop creates
// monthly partitions. Two months of lookahead handles month-boundary timing
// even on systems whose ticker last fires several hours before midnight UTC.
const partitionsAheadDefault = 2

// slogKeyError is the structured-log key for an error value. Centralized so
// the literal does not repeat across the maintenance loop's warn-and-continue
// branches (each step logs its own error; the next step still runs).
const slogKeyError = "error"

// StartCleanupRoutine starts a background goroutine that periodically deletes
// old audit logs, creates upcoming monthly partitions, and drops partitions
// whose entire date range has aged past the retention window. The goroutine
// is stopped when Close is called.
//
// Partition rotation is best-effort: a failure to create or drop a partition
// is logged and skipped, never aborts the retention DELETE that runs on the
// same tick. This keeps audit_logs bounded even on a database where the
// partition operations have transient errors.
//
// In multi-replica deployments, only one pod per tick acquires the audit
// maintenance advisory lock and runs the steps; the rest skip silently
// until the next tick. The initial pre-tick ensure also runs under the
// lock so simultaneous pod restarts do not stampede on CREATE TABLE.
func (s *Store) StartCleanupRoutine(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	// Best-effort: ensure upcoming partitions exist before the first tick so
	// rows written between startup and the first tick land in named
	// partitions when their month is covered.
	s.runUnderMaintenanceLock(ctx, func(ctx context.Context) {
		if err := s.EnsureMonthlyPartitions(ctx, partitionsAheadDefault); err != nil {
			slog.Warn("audit cleanup: initial ensure partitions", slogKeyError, err)
		}
	})

	go func() {
		defer close(s.done)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runMaintenanceTick(ctx)
			}
		}
	}()
}

// runMaintenanceTick performs one round of partition rotation and retention
// cleanup, guarded by a PostgreSQL advisory lock so only one replica runs it
// per tick. Each step's error is logged and isolated so a failure in one does
// not skip the others; the retention DELETE is the critical step and must
// always run if reachable.
func (s *Store) runMaintenanceTick(ctx context.Context) {
	s.runUnderMaintenanceLock(ctx, func(ctx context.Context) {
		if err := s.EnsureMonthlyPartitions(ctx, partitionsAheadDefault); err != nil {
			slog.Warn("audit cleanup: ensure partitions", slogKeyError, err)
		}
		if err := s.Cleanup(ctx); err != nil {
			slog.Warn("audit cleanup: expired logs", slogKeyError, err)
		}
		if err := s.DropExpiredPartitions(ctx, s.retentionDays); err != nil {
			slog.Warn("audit cleanup: drop expired partitions", slogKeyError, err)
		}
	})
}

// unlockTimeout caps the time the deferred advisory_unlock call may take.
// The unlock runs on a detached context so it still fires when the parent
// context has been canceled by Close, but it must not block shutdown
// indefinitely if the database itself is unreachable.
const unlockTimeout = 5 * time.Second

// runUnderMaintenanceLock attempts to acquire the audit maintenance advisory
// lock and, if successful, invokes fn. If another replica already holds the
// lock or the database is unreachable, fn is not invoked and the call returns
// silently. The next tick will retry.
//
// The lock is taken on a dedicated connection so the acquire and release
// happen on the same PostgreSQL session (advisory locks are session-scoped).
// fn itself uses s.db, which may draw other connections from the pool; those
// queries are not blocked by the lock. The lock only prevents OTHER replicas
// from running maintenance concurrently.
//
// The unlock uses a detached context with a short timeout so a canceled
// parent context (e.g. Close called during fn) cannot leave the advisory
// lock held on the pooled connection.
func (s *Store) runUnderMaintenanceLock(ctx context.Context, fn func(context.Context)) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		slog.Warn("audit cleanup: acquire connection for lock", slogKeyError, err)
		return
	}
	defer func() { _ = conn.Close() }()

	var got bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock($1)", auditMaintenanceLockKey,
	).Scan(&got); err != nil {
		slog.Warn("audit cleanup: try advisory lock", slogKeyError, err)
		return
	}
	if !got {
		// Another replica holds the lock; skip this tick.
		return
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx,
			"SELECT pg_advisory_unlock($1)", auditMaintenanceLockKey,
		); err != nil {
			slog.Warn("audit cleanup: advisory unlock", slogKeyError, err)
		}
	}()

	fn(ctx)
}

// Verify interface compliance.
var _ audit.Logger = (*Store)(nil)

// Package postgres provides PostgreSQL storage for audit logs.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

const (
	defaultRetentionDays = 90
	defaultQueryCapacity = 100
	maxQueryCapacity     = 10000
)

// psq is the PostgreSQL statement builder with dollar placeholders.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// auditColumns lists columns returned by audit SELECT queries.
var auditColumns = []string{
	"id", "timestamp", "duration_ms", "request_id", "session_id",
	"user_id", "user_email", "persona", "tool_name", "toolkit_kind",
	"toolkit_name", "connection", "parameters", "success", "error_message",
	"response_chars", "request_chars", "content_blocks",
	"transport", "source", "enrichment_applied", "authorized",
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
		(id, timestamp, duration_ms, request_id, session_id, user_id, user_email, persona, tool_name, toolkit_kind, toolkit_name, connection, parameters, success, error_message, created_date, response_chars, request_chars, content_blocks, transport, source, enrichment_applied, authorized)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
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
		event.Authorized,
	)
	if err != nil {
		return fmt.Errorf("inserting audit log: %w", err)
	}

	return nil
}

// applyAuditFilter adds filter conditions to a SELECT builder.
func applyAuditFilter(qb sq.SelectBuilder, filter audit.QueryFilter) sq.SelectBuilder {
	if filter.ID != "" {
		qb = qb.Where(sq.Eq{"id": filter.ID})
	}
	if filter.StartTime != nil {
		qb = qb.Where(sq.GtOrEq{"timestamp": *filter.StartTime})
	}
	if filter.EndTime != nil {
		qb = qb.Where(sq.LtOrEq{"timestamp": *filter.EndTime})
	}
	if filter.UserID != "" {
		qb = qb.Where(sq.Eq{"user_id": filter.UserID})
	}
	if filter.SessionID != "" {
		qb = qb.Where(sq.Eq{"session_id": filter.SessionID})
	}
	if filter.ToolName != "" {
		qb = qb.Where(sq.Eq{"tool_name": filter.ToolName})
	}
	if filter.ToolkitKind != "" {
		qb = qb.Where(sq.Eq{"toolkit_kind": filter.ToolkitKind})
	}
	if filter.Success != nil {
		qb = qb.Where(sq.Eq{"success": *filter.Success})
	}
	return qb
}

// Query retrieves audit events matching the filter.
func (s *Store) Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	qb := applyAuditFilter(psq.Select(auditColumns...).From("audit_logs"), filter)
	qb = qb.OrderBy("timestamp DESC")
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

// StartCleanupRoutine starts a background goroutine that periodically deletes
// old audit logs. The goroutine is stopped when Close is called.
func (s *Store) StartCleanupRoutine(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.Cleanup(ctx)
			}
		}
	}()
}

// Verify interface compliance.
var _ audit.Logger = (*Store)(nil)

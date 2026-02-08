// Package postgres provides PostgreSQL storage for audit logs.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

const (
	defaultRetentionDays = 90
	defaultQueryCapacity = 100
)

// Store implements audit.Logger using PostgreSQL.
type Store struct {
	db            *sql.DB
	retentionDays int
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

// queryBuilder helps build parameterized queries.
type queryBuilder struct {
	conditions []string
	args       []any
	argNum     int
}

func newQueryBuilder() *queryBuilder {
	return &queryBuilder{argNum: 1}
}

func (b *queryBuilder) addCondition(column string, value any) {
	b.conditions = append(b.conditions, fmt.Sprintf("%s = $%d", column, b.argNum))
	b.args = append(b.args, value)
	b.argNum++
}

func (b *queryBuilder) addTimeCondition(column, op string, value time.Time) {
	b.conditions = append(b.conditions, fmt.Sprintf("%s %s $%d", column, op, b.argNum))
	b.args = append(b.args, value)
	b.argNum++
}

func (b *queryBuilder) addLimit(limit int) {
	b.args = append(b.args, limit)
	b.argNum++
}

func (b *queryBuilder) addOffset(offset int) {
	b.args = append(b.args, offset)
}

func (b *queryBuilder) whereClause() string {
	if len(b.conditions) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(b.conditions, " AND ")
}

func (b *queryBuilder) limitClause(limit int) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf(" LIMIT $%d", b.argNum)
}

func (b *queryBuilder) offsetClause(offset int) string {
	if offset <= 0 {
		return ""
	}
	return fmt.Sprintf(" OFFSET $%d", b.argNum)
}

// Query retrieves audit events matching the filter.
func (s *Store) Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	builder := newQueryBuilder()
	s.buildFilterConditions(builder, filter)

	query := s.buildSelectQuery(builder, filter)
	return s.executeQuery(ctx, query, builder.args, filter.Limit)
}

func (*Store) buildFilterConditions(b *queryBuilder, filter audit.QueryFilter) {
	if filter.StartTime != nil {
		b.addTimeCondition("timestamp", ">=", *filter.StartTime)
	}
	if filter.EndTime != nil {
		b.addTimeCondition("timestamp", "<=", *filter.EndTime)
	}
	if filter.UserID != "" {
		b.addCondition("user_id", filter.UserID)
	}
	if filter.SessionID != "" {
		b.addCondition("session_id", filter.SessionID)
	}
	if filter.ToolName != "" {
		b.addCondition("tool_name", filter.ToolName)
	}
	if filter.ToolkitKind != "" {
		b.addCondition("toolkit_kind", filter.ToolkitKind)
	}
	if filter.Success != nil {
		b.addCondition("success", *filter.Success)
	}
}

func (*Store) buildSelectQuery(b *queryBuilder, filter audit.QueryFilter) string {
	query := `
		SELECT id, timestamp, duration_ms, request_id, session_id, user_id, user_email, persona, tool_name, toolkit_kind, toolkit_name, connection, parameters, success, error_message, response_chars, request_chars, content_blocks, transport, source, enrichment_applied, authorized
		FROM audit_logs
	`
	query += b.whereClause()
	query += " ORDER BY timestamp DESC"

	if filter.Limit > 0 {
		query += b.limitClause(filter.Limit)
		b.addLimit(filter.Limit)
	}
	if filter.Offset > 0 {
		query += b.offsetClause(filter.Offset)
		b.addOffset(filter.Offset)
	}

	return query
}

func (s *Store) executeQuery(ctx context.Context, query string, args []any, limit int) ([]audit.Event, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying audit logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	capacity := limit
	if capacity <= 0 {
		capacity = defaultQueryCapacity
	}
	events := make([]audit.Event, 0, capacity)

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

// Close releases resources.
func (*Store) Close() error {
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

// StartCleanupRoutine starts a background routine to clean up old audit logs.
func (s *Store) StartCleanupRoutine(ctx context.Context, interval time.Duration) {
	go func() {
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

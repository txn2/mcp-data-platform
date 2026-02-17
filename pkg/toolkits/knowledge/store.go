package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
)

// psq is the PostgreSQL statement builder with dollar placeholders.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// InsightStore persists and queries captured insights.
type InsightStore interface {
	Insert(ctx context.Context, insight Insight) error
	Get(ctx context.Context, id string) (*Insight, error)
	List(ctx context.Context, filter InsightFilter) ([]Insight, int, error)
	UpdateStatus(ctx context.Context, id, status, reviewedBy, reviewNotes string) error
	Update(ctx context.Context, id string, updates InsightUpdate) error
	Stats(ctx context.Context, filter InsightFilter) (*InsightStats, error)
	MarkApplied(ctx context.Context, id, appliedBy, changesetRef string) error
	Supersede(ctx context.Context, entityURN string, excludeID string) (int, error)
}

// postgresStore implements InsightStore using PostgreSQL.
type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL insight store.
func NewPostgresStore(db *sql.DB) InsightStore {
	return &postgresStore{db: db}
}

// Insert persists an insight to the knowledge_insights table.
func (s *postgresStore) Insert(ctx context.Context, insight Insight) error {
	entityURNs, err := json.Marshal(insight.EntityURNs)
	if err != nil {
		return fmt.Errorf("marshaling entity_urns: %w", err)
	}

	relatedCols, err := json.Marshal(insight.RelatedColumns)
	if err != nil {
		return fmt.Errorf("marshaling related_columns: %w", err)
	}

	suggestedActions, err := json.Marshal(insight.SuggestedActions)
	if err != nil {
		return fmt.Errorf("marshaling suggested_actions: %w", err)
	}

	query := `
		INSERT INTO knowledge_insights
		(id, session_id, captured_by, persona, source, category, insight_text, confidence, entity_urns, related_columns, suggested_actions, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = s.db.ExecContext(ctx, query,
		insight.ID,
		insight.SessionID,
		insight.CapturedBy,
		insight.Persona,
		insight.Source,
		insight.Category,
		insight.InsightText,
		insight.Confidence,
		entityURNs,
		relatedCols,
		suggestedActions,
		insight.Status,
	)
	if err != nil {
		return fmt.Errorf("inserting insight: %w", err)
	}

	return nil
}

// Get retrieves a single insight by ID.
func (s *postgresStore) Get(ctx context.Context, id string) (*Insight, error) {
	query := `
		SELECT id, created_at, session_id, captured_by, persona, source, category,
		       insight_text, confidence, entity_urns, related_columns,
		       suggested_actions, status, reviewed_by, reviewed_at,
		       review_notes, applied_by, applied_at, changeset_ref
		FROM knowledge_insights WHERE id = $1
	`

	var insight Insight
	var entityURNs, relatedCols, suggestedActions []byte
	var reviewedAt, appliedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&insight.ID, &insight.CreatedAt, &insight.SessionID,
		&insight.CapturedBy, &insight.Persona, &insight.Source, &insight.Category,
		&insight.InsightText, &insight.Confidence, &entityURNs,
		&relatedCols, &suggestedActions, &insight.Status,
		&insight.ReviewedBy, &reviewedAt, &insight.ReviewNotes,
		&insight.AppliedBy, &appliedAt, &insight.ChangesetRef,
	)
	if err != nil {
		return nil, fmt.Errorf("querying insight: %w", err)
	}

	if reviewedAt.Valid {
		insight.ReviewedAt = &reviewedAt.Time
	}
	if appliedAt.Valid {
		insight.AppliedAt = &appliedAt.Time
	}

	if err := unmarshalInsightJSON(&insight, entityURNs, relatedCols, suggestedActions); err != nil {
		return nil, err
	}

	return &insight, nil
}

// unmarshalInsightJSON unmarshals JSON columns into Insight fields.
func unmarshalInsightJSON(insight *Insight, entityURNs, relatedCols, suggestedActions []byte) error {
	if err := json.Unmarshal(entityURNs, &insight.EntityURNs); err != nil {
		return fmt.Errorf("unmarshaling entity_urns: %w", err)
	}
	if err := json.Unmarshal(relatedCols, &insight.RelatedColumns); err != nil {
		return fmt.Errorf("unmarshaling related_columns: %w", err)
	}
	if err := json.Unmarshal(suggestedActions, &insight.SuggestedActions); err != nil {
		return fmt.Errorf("unmarshaling suggested_actions: %w", err)
	}
	return nil
}

// applyInsightFilter adds filter conditions to a SELECT builder.
func applyInsightFilter(qb sq.SelectBuilder, filter InsightFilter) sq.SelectBuilder {
	if filter.Status != "" {
		qb = qb.Where(sq.Eq{"status": filter.Status})
	}
	if filter.Category != "" {
		qb = qb.Where(sq.Eq{"category": filter.Category})
	}
	if filter.EntityURN != "" {
		qb = qb.Where(sq.Expr("entity_urns @> ?::jsonb", fmt.Sprintf(`[%q]`, filter.EntityURN)))
	}
	if filter.CapturedBy != "" {
		qb = qb.Where(sq.Eq{"captured_by": filter.CapturedBy})
	}
	if filter.Confidence != "" {
		qb = qb.Where(sq.Eq{"confidence": filter.Confidence})
	}
	if filter.Source != "" {
		qb = qb.Where(sq.Eq{"source": filter.Source})
	}
	if filter.Since != nil {
		qb = qb.Where(sq.GtOrEq{"created_at": *filter.Since})
	}
	if filter.Until != nil {
		qb = qb.Where(sq.LtOrEq{"created_at": *filter.Until})
	}
	return qb
}

// List returns insights matching the filter with pagination.
func (s *postgresStore) List(ctx context.Context, filter InsightFilter) ([]Insight, int, error) {
	// Count total matching rows.
	countQB := applyInsightFilter(psq.Select("COUNT(*)").From("knowledge_insights"), filter)
	countQuery, countArgs, err := countQB.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting insights: %w", err)
	}

	// Fetch paginated results.
	limit := filter.EffectiveLimit()
	selectQB := applyInsightFilter(psq.Select(
		"id", "created_at", "session_id", "captured_by", "persona", "source", "category",
		"insight_text", "confidence", "entity_urns", "related_columns",
		"suggested_actions", "status", "reviewed_by", "reviewed_at",
		"review_notes", "applied_by", "applied_at", "changeset_ref",
	).From("knowledge_insights"), filter).
		OrderBy("created_at DESC")
	if limit > 0 {
		selectQB = selectQB.Limit(uint64(limit))
	}
	if filter.Offset > 0 {
		selectQB = selectQB.Offset(uint64(filter.Offset))
	}

	selectQuery, selectArgs, err := selectQB.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building select query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying insights: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var insights []Insight
	for rows.Next() {
		insight, err := scanInsightRow(rows)
		if err != nil {
			return nil, 0, err
		}
		insights = append(insights, insight)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating insight rows: %w", err)
	}

	return insights, total, nil
}

// scanInsightRow scans a single row into an Insight, handling null times and JSON columns.
func scanInsightRow(rows *sql.Rows) (Insight, error) {
	var insight Insight
	var entityURNs, relatedCols, suggestedActions []byte
	var reviewedAt, appliedAt sql.NullTime

	if err := rows.Scan(
		&insight.ID, &insight.CreatedAt, &insight.SessionID,
		&insight.CapturedBy, &insight.Persona, &insight.Source, &insight.Category,
		&insight.InsightText, &insight.Confidence, &entityURNs,
		&relatedCols, &suggestedActions, &insight.Status,
		&insight.ReviewedBy, &reviewedAt, &insight.ReviewNotes,
		&insight.AppliedBy, &appliedAt, &insight.ChangesetRef,
	); err != nil {
		return insight, fmt.Errorf("scanning insight row: %w", err)
	}

	if reviewedAt.Valid {
		insight.ReviewedAt = &reviewedAt.Time
	}
	if appliedAt.Valid {
		insight.AppliedAt = &appliedAt.Time
	}

	if err := unmarshalInsightJSON(&insight, entityURNs, relatedCols, suggestedActions); err != nil {
		return insight, err
	}
	return insight, nil
}

// UpdateStatus transitions an insight's status with review metadata.
func (s *postgresStore) UpdateStatus(ctx context.Context, id, status, reviewedBy, reviewNotes string) error {
	query := `
		UPDATE knowledge_insights
		SET status = $1, reviewed_by = $2, reviewed_at = $3, review_notes = $4
		WHERE id = $5
	`

	result, err := s.db.ExecContext(ctx, query, status, reviewedBy, time.Now(), reviewNotes, id)
	if err != nil {
		return fmt.Errorf("updating insight status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("insight not found: %s", id)
	}

	return nil
}

// Update edits an insight's text, category, or confidence.
func (s *postgresStore) Update(ctx context.Context, id string, updates InsightUpdate) error {
	qb := psq.Update("knowledge_insights")

	hasUpdates := false
	if updates.InsightText != "" {
		qb = qb.Set("insight_text", updates.InsightText)
		hasUpdates = true
	}
	if updates.Category != "" {
		qb = qb.Set("category", updates.Category)
		hasUpdates = true
	}
	if updates.Confidence != "" {
		qb = qb.Set("confidence", updates.Confidence)
		hasUpdates = true
	}

	if !hasUpdates {
		return fmt.Errorf("no fields to update")
	}

	qb = qb.Where(sq.Eq{"id": id}).Where(sq.NotEq{"status": StatusApplied})

	query, args, err := qb.ToSql()
	if err != nil {
		return fmt.Errorf("building update query: %w", err)
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating insight: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("insight not found or already applied: %s", id)
	}

	return nil
}

// Stats returns aggregated statistics matching the filter.
func (s *postgresStore) Stats(ctx context.Context, filter InsightFilter) (*InsightStats, error) {
	stats := &InsightStats{
		ByCategory:   make(map[string]int),
		ByConfidence: make(map[string]int),
		ByStatus:     make(map[string]int),
	}

	if err := s.countGroupBy(ctx, filter, "status", stats.ByStatus); err != nil {
		return nil, fmt.Errorf("counting by status: %w", err)
	}
	stats.TotalPending = stats.ByStatus[StatusPending]

	if err := s.countGroupBy(ctx, filter, "category", stats.ByCategory); err != nil {
		return nil, fmt.Errorf("counting by category: %w", err)
	}

	if err := s.countGroupBy(ctx, filter, "confidence", stats.ByConfidence); err != nil {
		return nil, fmt.Errorf("counting by confidence: %w", err)
	}

	return stats, nil
}

// countGroupBy queries a group-by count and populates a map.
func (s *postgresStore) countGroupBy(ctx context.Context, filter InsightFilter, col string, dest map[string]int) error {
	qb := applyInsightFilter(
		psq.Select(col, "COUNT(*)").From("knowledge_insights"), filter,
	).GroupBy(col)

	query, args, err := qb.ToSql()
	if err != nil {
		return fmt.Errorf("building query: %w", err)
	}

	return s.queryCountMap(ctx, query, args, dest)
}

// queryCountMap executes a "SELECT key, COUNT(*)" query and populates a map.
func (s *postgresStore) queryCountMap(ctx context.Context, query string, args []any, result map[string]int) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("executing count query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return fmt.Errorf("scanning count row: %w", err)
		}
		result[key] = count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating count rows: %w", err)
	}
	return nil
}

// MarkApplied marks an insight as applied with changeset reference.
func (s *postgresStore) MarkApplied(ctx context.Context, id, appliedBy, changesetRef string) error {
	query := `
		UPDATE knowledge_insights
		SET status = $1, applied_by = $2, applied_at = $3, changeset_ref = $4
		WHERE id = $5
	`

	result, err := s.db.ExecContext(ctx, query, StatusApplied, appliedBy, time.Now(), changesetRef, id)
	if err != nil {
		return fmt.Errorf("marking insight as applied: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("insight not found: %s", id)
	}

	return nil
}

// Supersede marks pending insights for an entity as superseded, except excludeID.
func (s *postgresStore) Supersede(ctx context.Context, entityURN, excludeID string) (int, error) {
	query := `
		UPDATE knowledge_insights
		SET status = $1
		WHERE status = $2 AND entity_urns @> $3::jsonb AND id != $4
	`

	result, err := s.db.ExecContext(ctx, query, StatusSuperseded, StatusPending,
		fmt.Sprintf(`[%q]`, entityURN), excludeID)
	if err != nil {
		return 0, fmt.Errorf("superseding insights: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected: %w", err) //nolint:revive // shared error format
	}

	return int(rows), nil
}

// Verify interface compliance.
var _ InsightStore = (*postgresStore)(nil)

// NewNoopStore creates a no-op InsightStore for use when no database is available.
func NewNoopStore() InsightStore {
	return &noopStore{}
}

// noopStore is a no-op implementation of InsightStore.
// All methods are no-ops; Get returns "insight not found".
//
//nolint:revive // interface implementation methods on unexported type need no doc comments
type noopStore struct{}

func (*noopStore) Insert(_ context.Context, _ Insight) error { return nil } //nolint:revive // interface impl

func (*noopStore) Get(_ context.Context, _ string) (*Insight, error) { //nolint:revive // interface impl
	return nil, fmt.Errorf("insight not found")
}

func (*noopStore) List(_ context.Context, _ InsightFilter) ([]Insight, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*noopStore) UpdateStatus(_ context.Context, _, _, _, _ string) error   { return nil } //nolint:revive // interface impl
func (*noopStore) Update(_ context.Context, _ string, _ InsightUpdate) error { return nil } //nolint:revive // interface impl

func (*noopStore) Stats(_ context.Context, _ InsightFilter) (*InsightStats, error) { //nolint:revive // interface impl
	return &InsightStats{ByCategory: map[string]int{}, ByConfidence: map[string]int{}, ByStatus: map[string]int{}}, nil
}

func (*noopStore) MarkApplied(_ context.Context, _, _, _ string) error   { return nil }    //nolint:revive // interface impl
func (*noopStore) Supersede(_ context.Context, _, _ string) (int, error) { return 0, nil } //nolint:revive // interface impl

// Verify interface compliance.
var _ InsightStore = (*noopStore)(nil)

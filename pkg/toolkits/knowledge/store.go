package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
		(id, session_id, captured_by, persona, category, insight_text, confidence, entity_urns, related_columns, suggested_actions, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err = s.db.ExecContext(ctx, query,
		insight.ID,
		insight.SessionID,
		insight.CapturedBy,
		insight.Persona,
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
		SELECT id, created_at, session_id, captured_by, persona, category,
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
		&insight.CapturedBy, &insight.Persona, &insight.Category,
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

// List returns insights matching the filter with pagination.
func (s *postgresStore) List(ctx context.Context, filter InsightFilter) ([]Insight, int, error) {
	where, args := buildFilterWhere(filter)

	// Count total matching rows
	countQuery := "SELECT COUNT(*) FROM knowledge_insights" + where
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting insights: %w", err)
	}

	// Fetch paginated results
	limit := filter.EffectiveLimit()
	// #nosec G202 -- concatenation uses parameterized args ($N), not user input
	selectQuery := `
		SELECT id, created_at, session_id, captured_by, persona, category,
		       insight_text, confidence, entity_urns, related_columns,
		       suggested_actions, status, reviewed_by, reviewed_at,
		       review_notes, applied_by, applied_at, changeset_ref
		FROM knowledge_insights` + where +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d OFFSET %d", limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying insights: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var insights []Insight
	for rows.Next() {
		var insight Insight
		var entityURNs, relatedCols, suggestedActions []byte
		var reviewedAt, appliedAt sql.NullTime

		if err := rows.Scan(
			&insight.ID, &insight.CreatedAt, &insight.SessionID,
			&insight.CapturedBy, &insight.Persona, &insight.Category,
			&insight.InsightText, &insight.Confidence, &entityURNs,
			&relatedCols, &suggestedActions, &insight.Status,
			&insight.ReviewedBy, &reviewedAt, &insight.ReviewNotes,
			&insight.AppliedBy, &appliedAt, &insight.ChangesetRef,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning insight row: %w", err)
		}

		if reviewedAt.Valid {
			insight.ReviewedAt = &reviewedAt.Time
		}
		if appliedAt.Valid {
			insight.AppliedAt = &appliedAt.Time
		}

		if err := unmarshalInsightJSON(&insight, entityURNs, relatedCols, suggestedActions); err != nil {
			return nil, 0, err
		}
		insights = append(insights, insight)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating insight rows: %w", err)
	}

	return insights, total, nil
}

// buildFilterWhere builds a WHERE clause and args from an InsightFilter.
func buildFilterWhere(filter InsightFilter) (where string, args []any) {
	var conditions []string
	argN := 1

	if filter.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argN))
		args = append(args, filter.Status)
		argN++
	}
	if filter.Category != "" {
		conditions = append(conditions, fmt.Sprintf("category = $%d", argN))
		args = append(args, filter.Category)
		argN++
	}
	if filter.EntityURN != "" {
		conditions = append(conditions, fmt.Sprintf("entity_urns @> $%d::jsonb", argN))
		args = append(args, fmt.Sprintf(`[%q]`, filter.EntityURN))
		argN++
	}
	if filter.CapturedBy != "" {
		conditions = append(conditions, fmt.Sprintf("captured_by = $%d", argN))
		args = append(args, filter.CapturedBy)
		argN++
	}
	if filter.Confidence != "" {
		conditions = append(conditions, fmt.Sprintf("confidence = $%d", argN))
		args = append(args, filter.Confidence)
		argN++
	}
	if filter.Since != nil {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", argN))
		args = append(args, *filter.Since)
		argN++
	}
	if filter.Until != nil {
		conditions = append(conditions, fmt.Sprintf("created_at <= $%d", argN))
		args = append(args, *filter.Until)
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
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
	var setClauses []string
	var args []any
	argN := 1

	if updates.InsightText != "" {
		setClauses = append(setClauses, fmt.Sprintf("insight_text = $%d", argN))
		args = append(args, updates.InsightText)
		argN++
	}
	if updates.Category != "" {
		setClauses = append(setClauses, fmt.Sprintf("category = $%d", argN))
		args = append(args, updates.Category)
		argN++
	}
	if updates.Confidence != "" {
		setClauses = append(setClauses, fmt.Sprintf("confidence = $%d", argN))
		args = append(args, updates.Confidence)
		argN++
	}

	if len(setClauses) == 0 {
		return fmt.Errorf("no fields to update")
	}

	query := fmt.Sprintf( // #nosec G201 -- setClauses are hardcoded column names, not user input
		"UPDATE knowledge_insights SET %s WHERE id = $%d AND status != 'applied'",
		strings.Join(setClauses, ", "), argN,
	)
	args = append(args, id)

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
	where, args := buildFilterWhere(filter)

	stats := &InsightStats{
		ByCategory:   make(map[string]int),
		ByConfidence: make(map[string]int),
		ByStatus:     make(map[string]int),
	}

	// Count by status
	statusQuery := "SELECT status, COUNT(*) FROM knowledge_insights" + where + " GROUP BY status"
	if err := s.queryCountMap(ctx, statusQuery, args, stats.ByStatus); err != nil {
		return nil, fmt.Errorf("counting by status: %w", err)
	}
	stats.TotalPending = stats.ByStatus[StatusPending]

	// Count by category
	catQuery := "SELECT category, COUNT(*) FROM knowledge_insights" + where + " GROUP BY category"
	if err := s.queryCountMap(ctx, catQuery, args, stats.ByCategory); err != nil {
		return nil, fmt.Errorf("counting by category: %w", err)
	}

	// Count by confidence
	confQuery := "SELECT confidence, COUNT(*) FROM knowledge_insights" + where + " GROUP BY confidence"
	if err := s.queryCountMap(ctx, confQuery, args, stats.ByConfidence); err != nil {
		return nil, fmt.Errorf("counting by confidence: %w", err)
	}

	return stats, nil
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

package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// InsightStore persists captured insights.
type InsightStore interface {
	Insert(ctx context.Context, insight Insight) error
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

// Verify interface compliance.
var _ InsightStore = (*postgresStore)(nil)

// NewNoopStore creates a no-op InsightStore for use when no database is available.
func NewNoopStore() InsightStore {
	return &noopStore{}
}

// noopStore is a no-op implementation of InsightStore.
type noopStore struct{}

// Insert is a no-op.
func (*noopStore) Insert(_ context.Context, _ Insight) error {
	return nil
}

// Verify interface compliance.
var _ InsightStore = (*noopStore)(nil)

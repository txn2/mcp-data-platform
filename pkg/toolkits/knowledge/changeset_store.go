package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ChangesetStore persists and queries knowledge changesets.
type ChangesetStore interface {
	InsertChangeset(ctx context.Context, cs Changeset) error
	GetChangeset(ctx context.Context, id string) (*Changeset, error)
	ListChangesets(ctx context.Context, filter ChangesetFilter) ([]Changeset, int, error)
	RollbackChangeset(ctx context.Context, id, rolledBackBy string) error
}

// postgresChangesetStore implements ChangesetStore using PostgreSQL.
type postgresChangesetStore struct {
	db *sql.DB
}

// NewPostgresChangesetStore creates a new PostgreSQL changeset store.
func NewPostgresChangesetStore(db *sql.DB) ChangesetStore {
	return &postgresChangesetStore{db: db}
}

// InsertChangeset persists a changeset to the knowledge_changesets table.
func (s *postgresChangesetStore) InsertChangeset(ctx context.Context, cs Changeset) error {
	prevVal, err := json.Marshal(cs.PreviousValue)
	if err != nil {
		return fmt.Errorf("marshaling previous_value: %w", err)
	}

	newVal, err := json.Marshal(cs.NewValue)
	if err != nil {
		return fmt.Errorf("marshaling new_value: %w", err)
	}

	srcIDs, err := json.Marshal(cs.SourceInsightIDs)
	if err != nil {
		return fmt.Errorf("marshaling source_insight_ids: %w", err)
	}

	query := `
		INSERT INTO knowledge_changesets
		(id, target_urn, change_type, previous_value, new_value, source_insight_ids, approved_by, applied_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err = s.db.ExecContext(ctx, query,
		cs.ID, cs.TargetURN, cs.ChangeType, prevVal, newVal, srcIDs,
		cs.ApprovedBy, cs.AppliedBy,
	)
	if err != nil {
		return fmt.Errorf("inserting changeset: %w", err)
	}

	return nil
}

// GetChangeset retrieves a single changeset by ID.
func (s *postgresChangesetStore) GetChangeset(ctx context.Context, id string) (*Changeset, error) {
	query := `
		SELECT id, created_at, target_urn, change_type, previous_value, new_value,
		       source_insight_ids, approved_by, applied_by, rolled_back,
		       rolled_back_by, rolled_back_at
		FROM knowledge_changesets WHERE id = $1
	`

	var cs Changeset
	var prevVal, newVal, srcIDs []byte
	var rolledBackAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&cs.ID, &cs.CreatedAt, &cs.TargetURN, &cs.ChangeType,
		&prevVal, &newVal, &srcIDs,
		&cs.ApprovedBy, &cs.AppliedBy, &cs.RolledBack,
		&cs.RolledBackBy, &rolledBackAt,
	)
	if err != nil {
		return nil, fmt.Errorf("querying changeset: %w", err)
	}

	if rolledBackAt.Valid {
		cs.RolledBackAt = &rolledBackAt.Time
	}

	if err := unmarshalChangesetJSON(&cs, prevVal, newVal, srcIDs); err != nil {
		return nil, err
	}

	return &cs, nil
}

// unmarshalChangesetJSON unmarshals JSON columns into Changeset fields.
func unmarshalChangesetJSON(cs *Changeset, prevVal, newVal, srcIDs []byte) error {
	if err := json.Unmarshal(prevVal, &cs.PreviousValue); err != nil {
		return fmt.Errorf("unmarshaling previous_value: %w", err)
	}
	if err := json.Unmarshal(newVal, &cs.NewValue); err != nil {
		return fmt.Errorf("unmarshaling new_value: %w", err)
	}
	if err := json.Unmarshal(srcIDs, &cs.SourceInsightIDs); err != nil {
		return fmt.Errorf("unmarshaling source_insight_ids: %w", err)
	}
	return nil
}

// ListChangesets returns changesets matching the filter with pagination.
func (s *postgresChangesetStore) ListChangesets(ctx context.Context, filter ChangesetFilter) ([]Changeset, int, error) {
	where, args := buildChangesetFilterWhere(filter)

	countQuery := "SELECT COUNT(*) FROM knowledge_changesets" + where
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting changesets: %w", err)
	}

	limit := filter.EffectiveLimit()
	// #nosec G202 -- concatenation uses parameterized args ($N), not user input
	selectQuery := `
		SELECT id, created_at, target_urn, change_type, previous_value, new_value,
		       source_insight_ids, approved_by, applied_by, rolled_back,
		       rolled_back_by, rolled_back_at
		FROM knowledge_changesets` + where +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d OFFSET %d", limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying changesets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var changesets []Changeset
	for rows.Next() {
		var cs Changeset
		var prevVal, newVal, srcIDs []byte
		var rolledBackAt sql.NullTime

		if err := rows.Scan(
			&cs.ID, &cs.CreatedAt, &cs.TargetURN, &cs.ChangeType,
			&prevVal, &newVal, &srcIDs,
			&cs.ApprovedBy, &cs.AppliedBy, &cs.RolledBack,
			&cs.RolledBackBy, &rolledBackAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning changeset row: %w", err)
		}

		if rolledBackAt.Valid {
			cs.RolledBackAt = &rolledBackAt.Time
		}

		if err := unmarshalChangesetJSON(&cs, prevVal, newVal, srcIDs); err != nil {
			return nil, 0, err
		}
		changesets = append(changesets, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating changeset rows: %w", err)
	}

	return changesets, total, nil
}

// buildChangesetFilterWhere builds a WHERE clause from a ChangesetFilter.
func buildChangesetFilterWhere(filter ChangesetFilter) (where string, args []any) {
	var conditions []string
	argN := 1

	if filter.EntityURN != "" {
		conditions = append(conditions, fmt.Sprintf("target_urn = $%d", argN))
		args = append(args, filter.EntityURN)
		argN++
	}
	if filter.AppliedBy != "" {
		conditions = append(conditions, fmt.Sprintf("applied_by = $%d", argN))
		args = append(args, filter.AppliedBy)
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
		argN++
	}
	if filter.RolledBack != nil {
		conditions = append(conditions, fmt.Sprintf("rolled_back = $%d", argN))
		args = append(args, *filter.RolledBack)
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// RollbackChangeset marks a changeset as rolled back.
func (s *postgresChangesetStore) RollbackChangeset(ctx context.Context, id, rolledBackBy string) error {
	query := `
		UPDATE knowledge_changesets
		SET rolled_back = TRUE, rolled_back_by = $1, rolled_back_at = $2
		WHERE id = $3 AND rolled_back = FALSE
	`

	result, err := s.db.ExecContext(ctx, query, rolledBackBy, time.Now(), id)
	if err != nil {
		return fmt.Errorf("rolling back changeset: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("changeset not found or already rolled back: %s", id)
	}

	return nil
}

// Verify interface compliance.
var _ ChangesetStore = (*postgresChangesetStore)(nil)

// NewNoopChangesetStore creates a no-op ChangesetStore.
func NewNoopChangesetStore() ChangesetStore {
	return &noopChangesetStore{}
}

// noopChangesetStore is a no-op implementation of ChangesetStore.
// All methods are no-ops; GetChangeset returns "changeset not found".
//
//nolint:revive // interface implementation methods on unexported type need no doc comments
type noopChangesetStore struct{}

func (*noopChangesetStore) InsertChangeset(_ context.Context, _ Changeset) error { return nil } //nolint:revive // interface impl

func (*noopChangesetStore) GetChangeset(_ context.Context, _ string) (*Changeset, error) { //nolint:revive // interface impl
	return nil, fmt.Errorf("changeset not found")
}

func (*noopChangesetStore) ListChangesets(_ context.Context, _ ChangesetFilter) ([]Changeset, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*noopChangesetStore) RollbackChangeset(_ context.Context, _, _ string) error { return nil } //nolint:revive // interface impl

// Verify interface compliance.
var _ ChangesetStore = (*noopChangesetStore)(nil)

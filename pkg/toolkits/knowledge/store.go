package knowledge

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// psq is the PostgreSQL statement builder with dollar placeholders.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// SQL column names referenced from filter and update builders.
const (
	colAppliedBy = "applied_by"
	colCreatedAt = "created_at"
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
	// ReturnToReview sends an applied insight back to pending after its changeset
	// was rolled back (#1257), so the review queue surfaces it for a decision
	// again. The prior application is retained (applied_by, applied_at,
	// changeset_ref) and the rollback is recorded in the review fields, so the
	// next reviewer sees what was already tried. It returns whether the insight
	// was in the applied state and was transitioned: an insight in any other
	// state is left alone (returned == false, no error), so re-running a rollback
	// neither errors nor resurrects an insight somebody has since decided.
	ReturnToReview(ctx context.Context, id, rolledBackBy, changesetID string) (returned bool, err error)
	Supersede(ctx context.Context, entityURN string, excludeID string) (int, error)
}

// PendingReviewOf returns the review-queue summary platform_info shows as the
// review-debt nudge (#764): the pending count, the oldest pending capture and
// how many have waited over thirty days.
//
// It reads them off the store's Stats, which the memory-backed adapter answers
// in one pass over the active records rather than the group-by fan-out the
// dropped Postgres store issued.
func PendingReviewOf(ctx context.Context, store InsightStore) (*PendingReview, error) {
	stats, err := store.Stats(ctx, InsightFilter{Status: StatusPending})
	if err != nil {
		return nil, fmt.Errorf("pending review stats: %w", err)
	}
	return &PendingReview{
		TotalPending:    stats.TotalPending,
		OldestPendingAt: stats.OldestPendingAt,
		PendingOver30d:  stats.PendingOver30d,
	}, nil
}

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
	return nil, errors.New("insight not found")
}

func (*noopStore) List(_ context.Context, _ InsightFilter) ([]Insight, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*noopStore) UpdateStatus(_ context.Context, _, _, _, _ string) error   { return nil } //nolint:revive // interface impl
func (*noopStore) Update(_ context.Context, _ string, _ InsightUpdate) error { return nil } //nolint:revive // interface impl

func (*noopStore) Stats(_ context.Context, _ InsightFilter) (*InsightStats, error) { //nolint:revive // interface impl
	return &InsightStats{ByCategory: map[string]int{}, ByConfidence: map[string]int{}, ByStatus: map[string]int{}}, nil
}

func (*noopStore) MarkApplied(_ context.Context, _, _, _ string) error { return nil } //nolint:revive // interface impl

func (*noopStore) ReturnToReview(_ context.Context, _, _, _ string) (bool, error) { //nolint:revive // interface impl
	return false, nil
}

func (*noopStore) Supersede(_ context.Context, _, _ string) (int, error) { return 0, nil } //nolint:revive // interface impl

// Verify interface compliance.
var _ InsightStore = (*noopStore)(nil)

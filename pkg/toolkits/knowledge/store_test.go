package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- noopStore tests ---

func TestNoopStore_Insert(t *testing.T) {
	store := NewNoopStore()
	err := store.Insert(context.Background(), Insight{
		ID:          "test-id",
		Category:    "correction",
		InsightText: "This is a test insight",
		Status:      "pending",
	})
	assert.NoError(t, err)
}

func TestNoopStore_Get(t *testing.T) {
	store := NewNoopStore()
	insight, err := store.Get(context.Background(), "any-id")
	assert.Nil(t, insight)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insight not found")
}

func TestNoopStore_List(t *testing.T) {
	store := NewNoopStore()
	insights, total, err := store.List(context.Background(), InsightFilter{})
	assert.NoError(t, err)
	assert.Nil(t, insights)
	assert.Equal(t, 0, total)
}

func TestNoopStore_UpdateStatus(t *testing.T) {
	store := NewNoopStore()
	err := store.UpdateStatus(context.Background(), "id", "approved", "admin", "looks good")
	assert.NoError(t, err)
}

func TestNoopStore_Update(t *testing.T) {
	store := NewNoopStore()
	err := store.Update(context.Background(), "id", InsightUpdate{InsightText: "updated"})
	assert.NoError(t, err)
}

func TestNoopStore_Stats(t *testing.T) {
	store := NewNoopStore()
	stats, err := store.Stats(context.Background(), InsightFilter{})
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.NotNil(t, stats.ByCategory)
	assert.NotNil(t, stats.ByConfidence)
	assert.NotNil(t, stats.ByStatus)
}

func TestNoopStore_MarkApplied(t *testing.T) {
	store := NewNoopStore()
	err := store.MarkApplied(context.Background(), "id", "admin", "cs-1")
	assert.NoError(t, err)
}

func TestNoopStore_Supersede(t *testing.T) {
	store := NewNoopStore()
	count, err := store.Supersede(context.Background(), "urn:li:dataset:foo", "exclude-id")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

// --- PendingReviewOf tests (#764) ---

func TestPendingReviewOf(t *testing.T) {
	t.Run("reads the rollup off the store's Stats", func(t *testing.T) {
		oldest := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		store := &fullSpyStore{StatsResult: &InsightStats{
			TotalPending: 3, OldestPendingAt: &oldest, PendingOver30d: 2,
		}}

		review, err := PendingReviewOf(context.Background(), store)
		require.NoError(t, err)
		assert.Equal(t, 3, review.TotalPending) //nolint:revive // test value
		assert.Equal(t, &oldest, review.OldestPendingAt)
		assert.Equal(t, 2, review.PendingOver30d) //nolint:revive // test value
	})

	t.Run("an empty queue reports nothing pending and no oldest", func(t *testing.T) {
		review, err := PendingReviewOf(context.Background(), NewNoopStore())
		require.NoError(t, err)
		assert.Equal(t, 0, review.TotalPending)
		assert.Nil(t, review.OldestPendingAt)
	})

	t.Run("propagates the Stats error", func(t *testing.T) {
		store := &fullSpyStore{StatsErr: errors.New("boom")}
		review, err := PendingReviewOf(context.Background(), store)
		assert.Error(t, err)
		assert.Nil(t, review)
	})
}

package knowledgelayer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// stubInsightStore overrides Stats on the noop insight store so
// PendingReviewSummary can be tested without a database. Other InsightStore
// methods are inherited from the noop and are unused here.
type stubInsightStore struct {
	knowledgekit.InsightStore
	stats *knowledgekit.InsightStats
	err   error
}

func (s stubInsightStore) Stats(context.Context, knowledgekit.InsightFilter) (*knowledgekit.InsightStats, error) {
	return s.stats, s.err
}

func TestPendingReviewSummary(t *testing.T) {
	oldest := time.Now().Add(-94 * 24 * time.Hour)
	tests := []struct {
		name  string
		store knowledgekit.InsightStore
		want  *ReviewQueueInfo
	}{
		{name: "nil store returns nil", store: nil, want: nil},
		{
			name:  "stats error returns nil (orientation must not fail)",
			store: stubInsightStore{InsightStore: knowledgekit.NewNoopStore(), err: errors.New("db down")},
			want:  nil,
		},
		{
			name:  "empty queue returns nil",
			store: stubInsightStore{InsightStore: knowledgekit.NewNoopStore(), stats: &knowledgekit.InsightStats{TotalPending: 0}},
			want:  nil,
		},
		{
			name: "pending queue with staleness is summarized",
			store: stubInsightStore{InsightStore: knowledgekit.NewNoopStore(), stats: &knowledgekit.InsightStats{
				TotalPending:    6,
				OldestPendingAt: &oldest,
				PendingOver30d:  2,
			}},
			want: &ReviewQueueInfo{Pending: 6, OldestPendingAgeDays: 94, PendingOver30d: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build the layer handle over the injected insight store; the
			// db/embedding inputs are unused with apply disabled.
			handle, err := NewFromInsightStore(nil, tt.store, nil, Config{ToolkitName: "default"})
			require.NoError(t, err)
			got := handle.PendingReviewSummary(context.Background())
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want.Pending, got.Pending)
			assert.Equal(t, tt.want.PendingOver30d, got.PendingOver30d)
			assert.InDelta(t, tt.want.OldestPendingAgeDays, got.OldestPendingAgeDays, 1)
		})
	}
}

// TestPendingReviewSummary_NilHandle covers the disabled-knowledge path, where
// Platform holds a nil handle and orientation must still be built.
func TestPendingReviewSummary_NilHandle(t *testing.T) {
	var h *Handle
	assert.Nil(t, h.PendingReviewSummary(context.Background()))
}

//go:build integration

package knowledge

// Real-Postgres round-trip for the capture_insight -> recall_insight path.
// Captured insights persist as knowledge-dimension memory records through
// memoryInsightAdapter, and recall_insight reads them back via the adapter's
// lexical Search. The underlying memory store has its own RealDB test, but the
// insight<->record mapping (status mapping, owner/dimension scoping, the
// metadata round-trip) is adapter logic that nothing exercises against a real
// engine. This asserts capture -> Get -> List -> Search read-back on real
// Postgres.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/memory"
)

func TestMemoryInsightAdapter_RealDB_CaptureRecallRoundTrip(t *testing.T) {
	adapter := NewMemoryInsightAdapter(memory.NewPostgresStore(testdb.New(t)))
	ctx := context.Background()

	const owner = "analyst@example.com"
	ins := Insight{
		ID:          "ins_realdb_1",
		CapturedBy:  owner,
		Source:      "agent_discovery",
		Category:    "business_context",
		InsightText: "The transactions table is partitioned by transaction_date.",
		Confidence:  "high",
		Status:      StatusPending,
	}
	require.NoError(t, adapter.Insert(ctx, ins), "capture_insight write path")

	// Read-back by ID: the record-to-insight mapping must reproduce the fields.
	got, err := adapter.Get(ctx, ins.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, ins.ID, got.ID)
	assert.Equal(t, ins.InsightText, got.InsightText)
	assert.Equal(t, ins.Category, got.Category)
	assert.Equal(t, ins.Confidence, got.Confidence)
	assert.Equal(t, StatusPending, got.Status, "a freshly captured insight reads back as pending")

	// Read-back via List, scoped to the owner (knowledge dimension is enforced
	// by the adapter). The pending insight must appear.
	list, _, err := adapter.List(ctx, InsightFilter{CapturedBy: owner, Status: StatusPending})
	require.NoError(t, err)
	assert.True(t, containsInsight(list, ins.ID), "captured insight must appear in the owner's pending list")

	// Read-back via the recall_insight path: the adapter's lexical Search must
	// find it by a distinctive term, owner-scoped, against the real tsvector.
	searcher, ok := adapter.(InsightSearcher)
	require.True(t, ok, "memory-backed adapter implements InsightSearcher")
	scored, err := searcher.Search(ctx, InsightSearchQuery{
		QueryText:  "partitioned",
		CapturedBy: owner,
		Limit:      10,
	})
	require.NoError(t, err)
	found := false
	for _, s := range scored {
		if s.Insight.ID == ins.ID {
			found = true
			assert.Greater(t, s.Score, 0.0, "lexical match must score above zero")
		}
	}
	assert.True(t, found, "recall_insight must read back the captured insight via lexical search")

	// A different owner must not see it (owner scoping is enforced in SQL).
	other, _, err := adapter.List(ctx, InsightFilter{CapturedBy: "stranger@example.com", Status: StatusPending})
	require.NoError(t, err)
	assert.False(t, containsInsight(other, ins.ID), "another owner must not see the insight")
}

func containsInsight(insights []Insight, id string) bool {
	for _, in := range insights {
		if in.ID == id {
			return true
		}
	}
	return false
}

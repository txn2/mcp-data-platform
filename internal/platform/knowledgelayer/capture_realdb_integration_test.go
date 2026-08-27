//go:build integration

package knowledgelayer

// The headline criterion of #1517: a deployment that turns the memory toolkit
// off still captures knowledge.
//
// Migration 000031 moved capture into memory_records and dropped
// knowledge_insights, but New fell back to a store built entirely on the
// dropped table whenever the caller passed no memory store -- which is what
// Platform passes when memory.enabled is false. Every capture, read and review
// failed with `relation "knowledge_insights" does not exist`.
//
// A unit test could only show which store New selects. Only a real database
// shows whether what that store writes can be read back, which is the part
// that was broken.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

func TestCaptureWithoutAMemoryStore_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	// nil memStore is exactly what Platform.initKnowledge passes when
	// memory.enabled is false and a database is configured.
	h, err := New(db, nil, nil, Config{ToolkitName: "default"})
	require.NoError(t, err)
	require.NotNil(t, h)

	store := h.InsightStore()
	require.NotNil(t, store)

	insight := knowledgekit.Insight{
		ID:          "11111111-1111-1111-1111-111111111111",
		SessionID:   "sess-1517",
		CapturedBy:  "analyst@example.com",
		Persona:     "analyst",
		Source:      "user",
		Category:    "correction",
		InsightText: "The amount column is gross margin before returns.",
		Confidence:  "high",
		EntityURNs:  []string{"urn:li:dataset:(urn:li:dataPlatform:trino,db.schema.orders,PROD)"},
		Status:      knowledgekit.StatusPending,
	}
	require.NoError(t, store.Insert(ctx, insight), "capture must reach a table that exists")

	got, err := store.Get(ctx, insight.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, insight.InsightText, got.InsightText)
	assert.Equal(t, insight.CapturedBy, got.CapturedBy)
	assert.Equal(t, insight.EntityURNs, got.EntityURNs)

	// The review queue reads the same rows, which is the surface platform_info
	// and the review-queue alert report from.
	list, total, err := store.List(ctx, knowledgekit.InsightFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, insight.ID, list[0].ID)

	review, err := knowledgekit.PendingReviewOf(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, 1, review.TotalPending)
}

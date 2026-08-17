//go:build integration

package knowledge

// Real-Postgres test for the #633 Goal 3 sink router: a business_knowledge
// capture promoted via apply_knowledge lands in a canonical portal knowledge
// page, records a changeset, marks the source insights applied, and rolls back
// cleanly. It exercises the real assembled path (memory store -> insight adapter
// -> apply toolkit -> knowledgepage store + changeset store), not just fakes.
//
// It is also the capture -> apply -> rollback -> back-in-the-queue test #1257
// asks for: the rollback returns every source insight to pending, keeps the
// application it reverted, and bulk_review counts and itemizes them again.

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

func TestPageSink_RealDB_PromoteAndRollback(t *testing.T) {
	db := testdb.New(t)
	insightStore := NewMemoryInsightAdapter(memory.NewPostgresStore(db))
	csStore := NewPostgresChangesetStore(db)
	pageStore := knowledgepage.NewPostgresStoreSearcher(db)

	tk, err := New("test", insightStore)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, csStore, &NoopDataHubWriter{})
	tk.SetPageWriter(pageStore)

	ctx := ctxWithUser("admin@example.com", "sess", "admin")

	// Capture two business_knowledge insights (the provisional inbox drafts). The
	// first carries a DataHub reference that must survive promotion onto the page
	// (#664); both are promoted together so the rollback has to return every source
	// insight, not just the first (#1257).
	const refURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"
	require.NoError(t, insightStore.Insert(ctx, Insight{
		ID:          "ins-bk-1",
		CapturedBy:  "alice@example.com",
		InsightText: "Our fiscal year starts in February.",
		SinkClass:   memory.SinkBusinessKnowledge,
		Status:      StatusPending,
		EntityURNs:  []string{refURN},
	}))
	require.NoError(t, insightStore.Insert(ctx, Insight{
		ID:          "ins-bk-2",
		CapturedBy:  "alice@example.com",
		InsightText: "Q1 therefore runs February through April.",
		SinkClass:   memory.SinkBusinessKnowledge,
		Status:      StatusPending,
	}))

	// Promote them to a canonical knowledge page.
	res, _, err := tk.handleApplyKnowledge(ctx, &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action:     actionApply,
		Sink:       sinkKnowledgePage,
		InsightIDs: []string{"ins-bk-1", "ins-bk-2"},
		Page: &pagePromotionInput{
			Slug: "fiscal-calendar", Title: "Fiscal Calendar",
			Body: "# Fiscal Calendar\n\nOur fiscal year starts in February.",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "promote should succeed")
	out := parseJSONResult(t, res)
	assert.Equal(t, "created", out["action"])
	changesetID, _ := out["changeset_id"].(string)
	require.NotEmpty(t, changesetID)

	// Page exists and carries the origin sink-class tag.
	page, err := pageStore.GetBySlug(ctx, "fiscal-calendar")
	require.NoError(t, err)
	assert.Equal(t, "Fiscal Calendar", page.Title)
	assert.Contains(t, page.Tags, memory.SinkBusinessKnowledge)
	// Authorship is the acting user's email, not the opaque user id (#682).
	assert.Equal(t, "admin@example.com", page.CreatedEmail)
	assert.Equal(t, "admin@example.com", page.CreatedBy)

	// The insight's DataHub reference was carried onto the page (#664), against
	// the real table with its CHECK, FK, and unique-index constraints.
	refs, err := pageStore.ListEntityRefs(ctx, page.ID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, knowledgepage.RefTargetDataHub, refs[0].TargetType)
	assert.Equal(t, refURN, refs[0].EntityURN)
	assert.Equal(t, knowledgepage.RefSourcePromoted, refs[0].Source)

	// Changeset recorded with the kp: target.
	cs, err := csStore.GetChangeset(ctx, changesetID)
	require.NoError(t, err)
	assert.Equal(t, pageTargetPrefix+"fiscal-calendar", cs.TargetURN)
	assert.Equal(t, changeCreatePage, cs.ChangeType)

	// Source insights drained from the inbox (marked applied), so the review queue
	// is empty before the rollback.
	for _, id := range []string{"ins-bk-1", "ins-bk-2"} {
		applied, err := insightStore.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, StatusApplied, applied.Status, id)
	}
	assert.Equal(t, float64(0), bulkReviewTotal(ctx, t, tk), "applied insights are not in the queue")

	// Roll back: the page is removed and every source insight returns to the queue.
	rb, _, err := tk.handleApplyKnowledge(ctx, &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action: actionRollback, ChangesetID: changesetID, Confirm: true,
	})
	require.NoError(t, err)
	require.False(t, rb.IsError, "rollback should succeed")
	rbOut := parseJSONResult(t, rb)
	assert.ElementsMatch(t, []any{"ins-bk-1", "ins-bk-2"}, rbOut["insights_returned_to_review"])

	_, err = pageStore.GetBySlug(ctx, "fiscal-calendar")
	assert.ErrorIs(t, err, knowledgepage.ErrNotFound, "page should be soft-deleted after rollback")

	// Both insights are reviewable again, and each still carries the application
	// that was reverted so the next reviewer is not deciding blind (#1257).
	for _, id := range []string{"ins-bk-1", "ins-bk-2"} {
		returned, err := insightStore.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, StatusPending, returned.Status, id)
		assert.Equal(t, changesetID, returned.ChangesetRef, id)
		assert.Equal(t, "admin@example.com", returned.AppliedBy, id)
		require.NotNil(t, returned.AppliedAt, id)
		assert.Equal(t, RollbackReviewNote(changesetID), returned.ReviewNotes, id)
	}

	// The queue counts them and itemizes them: this is the surface a reviewer
	// enumerates, and before #1257 a rolled-back insight was in neither.
	assert.Equal(t, float64(2), bulkReviewTotal(ctx, t, tk))
	items, _, err := tk.handleBulkReview(ctx, applyKnowledgeInput{Itemize: true, Limit: MaxLimit})
	require.NoError(t, err)
	require.False(t, items.IsError)
	itemized := parseJSONResult(t, items)
	assert.Equal(t, float64(2), itemized["total_pending"])
	var listed []string
	for _, raw := range itemized["insights"].([]any) {
		listed = append(listed, raw.(map[string]any)["id"].(string))
	}
	assert.ElementsMatch(t, []string{"ins-bk-1", "ins-bk-2"}, listed)
}

// bulkReviewTotal reads the review queue's headline count through the tool.
func bulkReviewTotal(ctx context.Context, t *testing.T, tk *Toolkit) float64 {
	t.Helper()
	res, _, err := tk.handleBulkReview(ctx, applyKnowledgeInput{})
	require.NoError(t, err)
	require.False(t, res.IsError)
	total, ok := parseJSONResult(t, res)["total_pending"].(float64)
	require.True(t, ok, "total_pending should be a number")
	return total
}

//go:build integration

package knowledge

// Real-Postgres test for the #633 Goal 3 sink router: a business_knowledge
// capture promoted via apply_knowledge lands in a canonical portal knowledge
// page, records a changeset, marks the source insight applied, and rolls back
// cleanly. It exercises the real assembled path (memory store -> insight adapter
// -> apply toolkit -> knowledgepage store + changeset store), not just fakes.

import (
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

	// Capture a business_knowledge insight (the provisional inbox draft). It
	// carries a DataHub reference that must survive promotion onto the page (#664).
	const refURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"
	require.NoError(t, insightStore.Insert(ctx, Insight{
		ID:          "ins-bk-1",
		CapturedBy:  "alice@example.com",
		InsightText: "Our fiscal year starts in February.",
		SinkClass:   memory.SinkBusinessKnowledge,
		Status:      StatusPending,
		EntityURNs:  []string{refURN},
	}))

	// Promote it to a canonical knowledge page.
	res, _, err := tk.handleApplyKnowledge(ctx, &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action:     actionApply,
		Sink:       sinkKnowledgePage,
		InsightIDs: []string{"ins-bk-1"},
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

	// Source insight drained from the inbox (marked applied).
	applied, err := insightStore.Get(ctx, "ins-bk-1")
	require.NoError(t, err)
	assert.Equal(t, StatusApplied, applied.Status)

	// Roll back: the page is removed and the insight returns to rolled_back.
	rb, _, err := tk.handleApplyKnowledge(ctx, &mcp.CallToolRequest{}, applyKnowledgeInput{
		Action: actionRollback, ChangesetID: changesetID, Confirm: true,
	})
	require.NoError(t, err)
	require.False(t, rb.IsError, "rollback should succeed")

	_, err = pageStore.GetBySlug(ctx, "fiscal-calendar")
	assert.ErrorIs(t, err, knowledgepage.ErrNotFound, "page should be soft-deleted after rollback")

	rolledBack, err := insightStore.Get(ctx, "ins-bk-1")
	require.NoError(t, err)
	assert.Equal(t, StatusRolledBack, rolledBack.Status)
}

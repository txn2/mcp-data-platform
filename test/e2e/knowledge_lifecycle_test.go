//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	"github.com/txn2/mcp-data-platform/test/e2e/helpers"
)

const (
	entityA = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.e2e_test.test_orders,PROD)"
	entityB = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.e2e_test.legacy_users,PROD)"
)

// TestKnowledgeLifecycle validates the full knowledge capture lifecycle against
// real PostgreSQL: insert, query, JSONB containment, pagination, status
// transitions, changesets, and rollback.
func TestKnowledgeLifecycle(t *testing.T) {
	cfg := helpers.DefaultE2EConfig()

	ctx := context.Background()
	if err := helpers.WaitForPostgres(ctx, cfg.PostgresDSN, helpers.DefaultWaitConfig()); err != nil {
		t.Fatalf("postgres not ready: %v", err)
	}

	kdb := helpers.NewKnowledgeTestDB(t, cfg.PostgresDSN)
	defer func() {
		if err := kdb.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	}()
	kdb.TruncateKnowledgeTables(t)

	// IDs for the three insights we'll create
	const (
		insightA1 = "e2e-insight-a1"
		insightA2 = "e2e-insight-a2"
		insightB1 = "e2e-insight-b1"
	)

	// --- Insight CRUD ---

	t.Run("01_capture_insights", func(t *testing.T) {
		kdb.InsertTestInsight(t, insightA1, "correction",
			"The order_total column is pre-tax, not post-tax as documented",
			[]string{entityA})

		kdb.InsertTestInsight(t, insightA2, "business_context",
			"Orders table joins to customers via customer_id FK",
			[]string{entityA})

		kdb.InsertTestInsight(t, insightB1, "data_quality",
			"Legacy users table has many null email addresses",
			[]string{entityB})

		if got := kdb.CountRows(t, "knowledge_insights"); got != 3 {
			t.Fatalf("expected 3 rows, got %d", got)
		}
	})

	t.Run("02_get_insight", func(t *testing.T) {
		insight, err := kdb.InsightStore.Get(ctx, insightA1)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}

		if insight.ID != insightA1 {
			t.Errorf("ID: got %q, want %q", insight.ID, insightA1)
		}
		if insight.Category != "correction" {
			t.Errorf("Category: got %q, want %q", insight.Category, "correction")
		}
		if insight.Status != knowledge.StatusPending {
			t.Errorf("Status: got %q, want %q", insight.Status, knowledge.StatusPending)
		}
		if insight.CreatedAt.IsZero() {
			t.Error("CreatedAt is zero")
		}
		if len(insight.EntityURNs) != 1 || insight.EntityURNs[0] != entityA {
			t.Errorf("EntityURNs: got %v, want [%s]", insight.EntityURNs, entityA)
		}
		if insight.SessionID != "e2e-session-001" {
			t.Errorf("SessionID: got %q, want %q", insight.SessionID, "e2e-session-001")
		}
	})

	t.Run("03_get_nonexistent", func(t *testing.T) {
		_, err := kdb.InsightStore.Get(ctx, "does-not-exist")
		if err == nil {
			t.Fatal("expected error for nonexistent insight")
		}
	})

	t.Run("04_list_all", func(t *testing.T) {
		insights, total, err := kdb.InsightStore.List(ctx, knowledge.InsightFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 {
			t.Errorf("total: got %d, want 3", total)
		}
		if len(insights) != 3 {
			t.Errorf("len: got %d, want 3", len(insights))
		}
	})

	t.Run("05_list_by_entity_urn", func(t *testing.T) {
		// JSONB @> containment: only insights whose entity_urns contain entityA
		insights, total, err := kdb.InsightStore.List(ctx, knowledge.InsightFilter{
			EntityURN: entityA,
		})
		if err != nil {
			t.Fatalf("List by EntityURN: %v", err)
		}
		if total != 2 {
			t.Errorf("total: got %d, want 2", total)
		}
		if len(insights) != 2 {
			t.Errorf("len: got %d, want 2", len(insights))
		}
	})

	t.Run("06_list_by_category", func(t *testing.T) {
		insights, total, err := kdb.InsightStore.List(ctx, knowledge.InsightFilter{
			Category: "data_quality",
		})
		if err != nil {
			t.Fatalf("List by Category: %v", err)
		}
		if total != 1 {
			t.Errorf("total: got %d, want 1", total)
		}
		if len(insights) != 1 {
			t.Errorf("len: got %d, want 1", len(insights))
		}
		if len(insights) > 0 && insights[0].ID != insightB1 {
			t.Errorf("ID: got %q, want %q", insights[0].ID, insightB1)
		}
	})

	t.Run("07_list_pagination", func(t *testing.T) {
		// Page 1: limit 2
		page1, total, err := kdb.InsightStore.List(ctx, knowledge.InsightFilter{
			Limit: 2,
		})
		if err != nil {
			t.Fatalf("List page 1: %v", err)
		}
		if total != 3 {
			t.Errorf("total: got %d, want 3", total)
		}
		if len(page1) != 2 {
			t.Errorf("page 1 len: got %d, want 2", len(page1))
		}

		// Page 2: offset 2, limit 2
		page2, total2, err := kdb.InsightStore.List(ctx, knowledge.InsightFilter{
			Limit:  2,
			Offset: 2,
		})
		if err != nil {
			t.Fatalf("List page 2: %v", err)
		}
		if total2 != 3 {
			t.Errorf("total: got %d, want 3", total2)
		}
		if len(page2) != 1 {
			t.Errorf("page 2 len: got %d, want 1", len(page2))
		}

		// Ensure no overlap between pages
		if len(page1) > 0 && len(page2) > 0 && page1[0].ID == page2[0].ID {
			t.Error("page 1 and page 2 overlap")
		}
	})

	t.Run("08_stats", func(t *testing.T) {
		stats, err := kdb.InsightStore.Stats(ctx, knowledge.InsightFilter{})
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if stats.TotalPending != 3 {
			t.Errorf("TotalPending: got %d, want 3", stats.TotalPending)
		}
		if stats.ByStatus[knowledge.StatusPending] != 3 {
			t.Errorf("ByStatus[pending]: got %d, want 3", stats.ByStatus[knowledge.StatusPending])
		}
		if stats.ByCategory["correction"] != 1 {
			t.Errorf("ByCategory[correction]: got %d, want 1", stats.ByCategory["correction"])
		}
		if stats.ByCategory["business_context"] != 1 {
			t.Errorf("ByCategory[business_context]: got %d, want 1", stats.ByCategory["business_context"])
		}
		if stats.ByConfidence["medium"] != 3 {
			t.Errorf("ByConfidence[medium]: got %d, want 3", stats.ByConfidence["medium"])
		}
	})

	// --- Status transitions ---

	t.Run("09_approve_insights", func(t *testing.T) {
		err := kdb.InsightStore.UpdateStatus(ctx, insightA1, knowledge.StatusApproved, "reviewer-1", "looks correct")
		if err != nil {
			t.Fatalf("UpdateStatus A1: %v", err)
		}

		err = kdb.InsightStore.UpdateStatus(ctx, insightA2, knowledge.StatusApproved, "reviewer-1", "verified FK")
		if err != nil {
			t.Fatalf("UpdateStatus A2: %v", err)
		}

		// Verify
		a1, err := kdb.InsightStore.Get(ctx, insightA1)
		if err != nil {
			t.Fatalf("Get A1: %v", err)
		}
		if a1.Status != knowledge.StatusApproved {
			t.Errorf("Status: got %q, want %q", a1.Status, knowledge.StatusApproved)
		}
		if a1.ReviewedBy != "reviewer-1" {
			t.Errorf("ReviewedBy: got %q, want %q", a1.ReviewedBy, "reviewer-1")
		}
		if a1.ReviewedAt == nil {
			t.Error("ReviewedAt is nil after approval")
		}
		if a1.ReviewNotes != "looks correct" {
			t.Errorf("ReviewNotes: got %q, want %q", a1.ReviewNotes, "looks correct")
		}
	})

	t.Run("10_reject_insight", func(t *testing.T) {
		err := kdb.InsightStore.UpdateStatus(ctx, insightB1, knowledge.StatusRejected, "reviewer-1", "not actionable")
		if err != nil {
			t.Fatalf("UpdateStatus B1: %v", err)
		}

		b1, err := kdb.InsightStore.Get(ctx, insightB1)
		if err != nil {
			t.Fatalf("Get B1: %v", err)
		}
		if b1.Status != knowledge.StatusRejected {
			t.Errorf("Status: got %q, want %q", b1.Status, knowledge.StatusRejected)
		}
	})

	t.Run("11_update_insight", func(t *testing.T) {
		newText := "The order_total column is pre-tax (excluding sales tax and shipping)"
		err := kdb.InsightStore.Update(ctx, insightA1, knowledge.InsightUpdate{
			InsightText: newText,
		})
		if err != nil {
			t.Fatalf("Update A1: %v", err)
		}

		a1, err := kdb.InsightStore.Get(ctx, insightA1)
		if err != nil {
			t.Fatalf("Get A1: %v", err)
		}
		if a1.InsightText != newText {
			t.Errorf("InsightText: got %q, want %q", a1.InsightText, newText)
		}
	})

	t.Run("12_mark_applied", func(t *testing.T) {
		const changesetID = "e2e-changeset-001"
		err := kdb.InsightStore.MarkApplied(ctx, insightA1, "applier-1", changesetID)
		if err != nil {
			t.Fatalf("MarkApplied A1: %v", err)
		}

		a1, err := kdb.InsightStore.Get(ctx, insightA1)
		if err != nil {
			t.Fatalf("Get A1: %v", err)
		}
		if a1.Status != knowledge.StatusApplied {
			t.Errorf("Status: got %q, want %q", a1.Status, knowledge.StatusApplied)
		}
		if a1.AppliedBy != "applier-1" {
			t.Errorf("AppliedBy: got %q, want %q", a1.AppliedBy, "applier-1")
		}
		if a1.AppliedAt == nil {
			t.Error("AppliedAt is nil after MarkApplied")
		}
		if a1.ChangesetRef != changesetID {
			t.Errorf("ChangesetRef: got %q, want %q", a1.ChangesetRef, changesetID)
		}
	})

	// --- Changeset lifecycle ---

	const changesetID = "e2e-changeset-001"

	t.Run("13_create_changeset", func(t *testing.T) {
		cs := knowledge.Changeset{
			ID:               changesetID,
			TargetURN:        entityA,
			ChangeType:       "update_description",
			PreviousValue:    map[string]any{"description": "old description"},
			NewValue:         map[string]any{"description": "new description with pre-tax note"},
			SourceInsightIDs: []string{insightA1},
			ApprovedBy:       "reviewer-1",
			AppliedBy:        "applier-1",
		}
		err := kdb.ChangesetStore.InsertChangeset(ctx, cs)
		if err != nil {
			t.Fatalf("InsertChangeset: %v", err)
		}

		if got := kdb.CountRows(t, "knowledge_changesets"); got != 1 {
			t.Fatalf("expected 1 changeset row, got %d", got)
		}
	})

	t.Run("14_get_changeset", func(t *testing.T) {
		cs, err := kdb.ChangesetStore.GetChangeset(ctx, changesetID)
		if err != nil {
			t.Fatalf("GetChangeset: %v", err)
		}
		if cs.ID != changesetID {
			t.Errorf("ID: got %q, want %q", cs.ID, changesetID)
		}
		if cs.TargetURN != entityA {
			t.Errorf("TargetURN: got %q, want %q", cs.TargetURN, entityA)
		}
		if cs.ChangeType != "update_description" {
			t.Errorf("ChangeType: got %q, want %q", cs.ChangeType, "update_description")
		}
		if cs.RolledBack {
			t.Error("RolledBack should be false")
		}
		if cs.CreatedAt.IsZero() {
			t.Error("CreatedAt is zero")
		}
		if len(cs.SourceInsightIDs) != 1 || cs.SourceInsightIDs[0] != insightA1 {
			t.Errorf("SourceInsightIDs: got %v, want [%s]", cs.SourceInsightIDs, insightA1)
		}
		if cs.PreviousValue["description"] != "old description" {
			t.Errorf("PreviousValue[description]: got %v", cs.PreviousValue["description"])
		}
		if cs.NewValue["description"] != "new description with pre-tax note" {
			t.Errorf("NewValue[description]: got %v", cs.NewValue["description"])
		}
	})

	t.Run("15_list_changesets", func(t *testing.T) {
		changesets, total, err := kdb.ChangesetStore.ListChangesets(ctx, knowledge.ChangesetFilter{
			EntityURN: entityA,
		})
		if err != nil {
			t.Fatalf("ListChangesets: %v", err)
		}
		if total != 1 {
			t.Errorf("total: got %d, want 1", total)
		}
		if len(changesets) != 1 {
			t.Errorf("len: got %d, want 1", len(changesets))
		}

		// No changesets for entityB
		_, totalB, err := kdb.ChangesetStore.ListChangesets(ctx, knowledge.ChangesetFilter{
			EntityURN: entityB,
		})
		if err != nil {
			t.Fatalf("ListChangesets entityB: %v", err)
		}
		if totalB != 0 {
			t.Errorf("totalB: got %d, want 0", totalB)
		}
	})

	t.Run("16_rollback_changeset", func(t *testing.T) {
		err := kdb.ChangesetStore.RollbackChangeset(ctx, changesetID, "rollback-user")
		if err != nil {
			t.Fatalf("RollbackChangeset: %v", err)
		}

		cs, err := kdb.ChangesetStore.GetChangeset(ctx, changesetID)
		if err != nil {
			t.Fatalf("GetChangeset after rollback: %v", err)
		}
		if !cs.RolledBack {
			t.Error("RolledBack should be true")
		}
		if cs.RolledBackBy != "rollback-user" {
			t.Errorf("RolledBackBy: got %q, want %q", cs.RolledBackBy, "rollback-user")
		}
		if cs.RolledBackAt == nil {
			t.Error("RolledBackAt is nil after rollback")
		}
	})

	t.Run("17_double_rollback", func(t *testing.T) {
		err := kdb.ChangesetStore.RollbackChangeset(ctx, changesetID, "another-user")
		if err == nil {
			t.Fatal("expected error on double rollback")
		}
	})

	// --- Supersede ---

	t.Run("18_supersede", func(t *testing.T) {
		// Insert a new pending insight for entityA to be the "survivor"
		const survivorID = "e2e-insight-a3"
		kdb.InsertTestInsight(t, survivorID, "enhancement",
			"Orders table should include a currency_code column",
			[]string{entityA})

		// A2 is currently approved (not pending), so supersede should NOT touch it.
		// Only insightA2 won't be affected because it's approved.
		// But we need a pending insight for entityA to supersede.
		// Currently: A1=applied, A2=approved, B1=rejected, A3=pending
		// Supersede entityA excluding A3 should find 0 pending (A1 is applied, A2 is approved)
		count, err := kdb.InsightStore.Supersede(ctx, entityA, survivorID)
		if err != nil {
			t.Fatalf("Supersede: %v", err)
		}
		// No pending entityA insights besides the survivor
		if count != 0 {
			t.Errorf("superseded count: got %d, want 0", count)
		}

		// Add another pending insight for entityA, then supersede
		const extraID = "e2e-insight-a4"
		kdb.InsertTestInsight(t, extraID, "correction",
			"Another correction for orders table",
			[]string{entityA})

		// Now supersede entityA excluding survivorID should find extraID
		count, err = kdb.InsightStore.Supersede(ctx, entityA, survivorID)
		if err != nil {
			t.Fatalf("Supersede (second): %v", err)
		}
		if count != 1 {
			t.Errorf("superseded count: got %d, want 1", count)
		}

		// Verify the extra insight is now superseded
		extra, err := kdb.InsightStore.Get(ctx, extraID)
		if err != nil {
			t.Fatalf("Get extra: %v", err)
		}
		if extra.Status != knowledge.StatusSuperseded {
			t.Errorf("Status: got %q, want %q", extra.Status, knowledge.StatusSuperseded)
		}

		// Survivor should still be pending
		survivor, err := kdb.InsightStore.Get(ctx, survivorID)
		if err != nil {
			t.Fatalf("Get survivor: %v", err)
		}
		if survivor.Status != knowledge.StatusPending {
			t.Errorf("Survivor status: got %q, want %q", survivor.Status, knowledge.StatusPending)
		}
	})

	t.Run("19_final_stats", func(t *testing.T) {
		stats, err := kdb.InsightStore.Stats(ctx, knowledge.InsightFilter{})
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}

		// A1=applied, A2=approved, B1=rejected, A3=pending, A4=superseded
		if stats.ByStatus[knowledge.StatusApplied] != 1 {
			t.Errorf("ByStatus[applied]: got %d, want 1", stats.ByStatus[knowledge.StatusApplied])
		}
		if stats.ByStatus[knowledge.StatusApproved] != 1 {
			t.Errorf("ByStatus[approved]: got %d, want 1", stats.ByStatus[knowledge.StatusApproved])
		}
		if stats.ByStatus[knowledge.StatusRejected] != 1 {
			t.Errorf("ByStatus[rejected]: got %d, want 1", stats.ByStatus[knowledge.StatusRejected])
		}
		if stats.ByStatus[knowledge.StatusSuperseded] != 1 {
			t.Errorf("ByStatus[superseded]: got %d, want 1", stats.ByStatus[knowledge.StatusSuperseded])
		}
		if stats.TotalPending != 1 {
			t.Errorf("TotalPending: got %d, want 1", stats.TotalPending)
		}
	})
}

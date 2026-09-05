//go:build integration

package memory

// Real-Postgres round-trip test for the memory store. The write path marshals
// slice/map fields to JSONB (nil tolerated as JSON null) and only binds the
// embedding columns when an embedding is present, so a minimal record with no
// embedding must insert and read back cleanly against the real schema.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestMemoryStore_Insert_RealDB_RoundTrip(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	rec := Record{
		ID:        "mem_realdb_1",
		Content:   "The transactions table is partitioned by transaction_date.",
		Dimension: "knowledge",
		Category:  "business_context",
		Source:    "user",
		// EntityURNs/RelatedColumns/Metadata left nil (marshaled to JSON null/[]),
		// Embedding left nil (embedding columns omitted from the INSERT).
	}
	require.NoError(t, store.Insert(ctx, rec), "insert memory record with no embedding")

	got, err := store.Get(ctx, "mem_realdb_1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "mem_realdb_1", got.ID)
	assert.Equal(t, rec.Content, got.Content)
	assert.Equal(t, "knowledge", got.Dimension)
}

// TestMemoryStore_StatusUpdate_RealDB_ArchivedExcludedFromActive is the #579
// regression at the store level: archiving a record via Update (the path a
// rejected insight takes) must move the status COLUMN, so a status-filtered
// read (what memory_recall uses) excludes it, while an archived-status read
// still finds it (archived, not deleted).
func TestMemoryStore_StatusUpdate_RealDB_ArchivedExcludedFromActive(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	rec := Record{
		ID:        "mem_realdb_reject",
		Content:   "Insight that will be rejected.",
		Dimension: "knowledge",
		Category:  "business_context",
		Source:    "user",
		Status:    StatusActive,
	}
	require.NoError(t, store.Insert(ctx, rec))

	containsID := func(records []Record, id string) bool {
		for _, r := range records {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	activeList := func() []Record {
		recs, _, err := store.List(ctx, Filter{Dimension: "knowledge", Status: StatusActive, Limit: 50})
		require.NoError(t, err)
		return recs
	}

	// Before reject: the active-status list (what recall uses) contains it.
	require.True(t, containsID(activeList(), rec.ID), "record must be in the active list before reject")

	// Reject maps to archived; Update threads Status through to the column.
	require.NoError(t, store.Update(ctx, rec.ID, RecordUpdate{Status: StatusArchived}))

	got, err := store.Get(ctx, rec.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, StatusArchived, got.Status, "update must move the status column to archived")

	// After reject: the active-status list excludes it (recall no longer sees it).
	assert.False(t, containsID(activeList(), rec.ID), "archived insight must not appear in an active-status list")

	// An archived-status list still finds it (archived, not deleted).
	archived, _, err := store.List(ctx, Filter{Dimension: "knowledge", Status: StatusArchived, Limit: 50})
	require.NoError(t, err)
	assert.True(t, containsID(archived, rec.ID), "archived insight must remain visible under an archived-status list")
}

// TestMemoryStore_LexicalSearch_RealDB_Differentiates is the #578 regression:
// lexical ranking must differentiate two single-match records (an exact short
// match outranking a long single-mention) rather than collapsing both to the
// flat weight-D 0.1, and scores must stay within (0,1].
func TestMemoryStore_LexicalSearch_RealDB_Differentiates(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	exact := Record{ID: "lex_exact", Dimension: "knowledge", Status: StatusActive, Content: "revenue"}
	long := Record{
		ID: "lex_long", Dimension: "knowledge", Status: StatusActive,
		Content: "Quarterly revenue grew across every region this year compared with the prior period.",
	}
	require.NoError(t, store.Insert(ctx, exact))
	require.NoError(t, store.Insert(ctx, long))

	results, err := store.LexicalSearch(ctx, LexicalQuery{QueryText: "revenue", Dimension: "knowledge", Limit: 10})
	require.NoError(t, err)

	scores := map[string]float64{}
	for _, r := range results {
		scores[r.Record.ID] = r.Score
	}
	require.Contains(t, scores, exact.ID)
	require.Contains(t, scores, long.ID)

	// Substantially differentiated: the exact short match must outrank the long
	// single-mention by a clear margin. The flat-0.1 bug made these equal; a
	// too-weak normalization would make them nearly equal.
	assert.Greater(t, scores[exact.ID], 2*scores[long.ID],
		"exact match must rank well above a long single-mention, not collapse to a flat score")
	// Scores are bounded into (0,1) by the 32 normalization bit.
	for id, s := range scores {
		assert.Greater(t, s, 0.0, "score for %s must be positive", id)
		assert.Less(t, s, 1.0, "score for %s must be < 1", id)
	}
}

// TestMemoryStore_Supersede_RealDB_AdvancesInsightStatus is the #682 fix at the
// store level: superseding a reviewable insight must advance BOTH the lifecycle
// status column AND metadata.insight_status, so the insights read path (which
// filters on insight_status) stops surfacing the stale record. A non-insight
// record, which carries no insight_status, must be superseded without one being
// invented. Validates the jsonb_exists CASE that sqlmock cannot exercise.
func TestMemoryStore_Supersede_RealDB_AdvancesInsightStatus(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	insight := Record{
		ID:        "mem_realdb_supersede_insight",
		Content:   "Original business-knowledge insight to be superseded.",
		Dimension: "knowledge",
		Category:  "business_context",
		Source:    "user",
		Status:    StatusActive,
		Metadata:  map[string]any{MetaKeyInsightStatus: InsightStatusPending},
	}
	require.NoError(t, store.Insert(ctx, insight))

	pref := Record{
		ID:        "mem_realdb_supersede_pref",
		Content:   "A personal preference to be superseded (no insight_status).",
		Dimension: "preference",
		Category:  "general",
		Source:    "user",
		Status:    StatusActive,
	}
	require.NoError(t, store.Insert(ctx, pref))

	require.NoError(t, store.Supersede(ctx, insight.ID, "mem_successor"))
	require.NoError(t, store.Supersede(ctx, pref.ID, "mem_successor"))

	gotInsight, err := store.Get(ctx, insight.ID)
	require.NoError(t, err)
	require.NotNil(t, gotInsight)
	assert.Equal(t, StatusSuperseded, gotInsight.Status, "lifecycle status advanced")
	assert.Equal(t, InsightStatusSuperseded, gotInsight.Metadata[MetaKeyInsightStatus],
		"insight review status follows to superseded so the insights read path retracts it (#682)")
	assert.Equal(t, "mem_successor", gotInsight.Metadata["superseded_by"])

	gotPref, err := store.Get(ctx, pref.ID)
	require.NoError(t, err)
	require.NotNil(t, gotPref)
	assert.Equal(t, StatusSuperseded, gotPref.Status, "non-insight lifecycle status advanced")
	_, hasInsightStatus := gotPref.Metadata[MetaKeyInsightStatus]
	assert.False(t, hasInsightStatus, "a non-insight record must not be given an insight_status")
	assert.Equal(t, "mem_successor", gotPref.Metadata["superseded_by"])
}

// TestMemoryStore_EntityLookup_RealDB_PushPathGatesPending is the #745 acceptance
// test at the store level. The persona-scoped enrichment push path (createdBy == "")
// must exclude un-evaluated candidates regardless of how their pending state is
// encoded:
//   - live captures set metadata.insight_status = pending
//   - insights migrated from knowledge_insights carry no insight_status and record
//     their state under metadata.legacy_status (migration 000031)
//
// Grounded records (any status off pending) and non-insight memories (no marker)
// must still be pushed. The user-scoped path (createdBy set) is a caller reading
// their own memories and keeps their own pending candidates.
func TestMemoryStore_EntityLookup_RealDB_PushPathGatesPending(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	const (
		urn   = "urn:li:dataset:(urn:li:dataPlatform:trino,cat.sch.tbl,PROD)"
		owner = "owner@example.com"
		other = "analyst"
	)
	knowledgeRec := func(id, content string, meta map[string]any) Record {
		return Record{
			ID:         id,
			CreatedBy:  owner,
			Persona:    other,
			Dimension:  DimensionKnowledge,
			SinkClass:  SinkSchemaEntity,
			Content:    content,
			Category:   CategoryBusinessCtx,
			Confidence: ConfidenceHigh,
			Source:     SourceUser,
			Status:     StatusActive,
			EntityURNs: []string{urn},
			Metadata:   meta,
		}
	}

	// Two pending encodings that must be gated on the push path.
	pendingInsight := knowledgeRec("mem_745_pending_insight", "Pending live capture.",
		map[string]any{MetaKeyInsightStatus: InsightStatusPending})
	migratedPending := knowledgeRec("mem_745_pending_legacy", "Pending migrated insight.",
		map[string]any{MetaKeyLegacyStatus: InsightStatusPending})
	// Grounded records that must still be pushed.
	appliedInsight := knowledgeRec("mem_745_applied", "Grounded via apply.",
		map[string]any{MetaKeyInsightStatus: "applied"})
	migratedApproved := knowledgeRec("mem_745_legacy_approved", "Grounded migrated insight.",
		map[string]any{MetaKeyLegacyStatus: "approved"})
	// A migrated candidate later approved: UpdateStatus merges insight_status
	// without clearing the stale legacy_status='pending', so the record carries
	// BOTH keys. insight_status is authoritative (resolveInsightStatus precedence),
	// so this grounded insight must still be pushed. Gating on "either key pending"
	// would withhold it forever.
	migratedThenApproved := knowledgeRec("mem_745_legacy_then_approved", "Migrated, then approved.",
		map[string]any{MetaKeyInsightStatus: "approved", MetaKeyLegacyStatus: InsightStatusPending})
	// A non-insight preference (no marker) must never be gated by the insight logic.
	preference := Record{
		ID:         "mem_745_pref",
		CreatedBy:  owner,
		Persona:    other,
		Dimension:  DimensionPreference,
		SinkClass:  SinkPersonalPreference,
		Content:    "User prefers ISO dates.",
		Category:   CategoryGeneral,
		Confidence: ConfidenceHigh,
		Source:     SourceUser,
		Status:     StatusActive,
		EntityURNs: []string{urn},
	}
	for _, r := range []Record{
		pendingInsight, migratedPending, appliedInsight, migratedApproved, migratedThenApproved, preference,
	} {
		require.NoError(t, store.Insert(ctx, r))
	}

	ids := func(records []Record) map[string]bool {
		set := make(map[string]bool, len(records))
		for _, r := range records {
			set[r.ID] = true
		}
		return set
	}

	// Push path: persona-scoped, createdBy == "".
	pushed, err := store.EntityLookup(ctx, urn, other, "")
	require.NoError(t, err)
	got := ids(pushed)
	assert.False(t, got[pendingInsight.ID], "insight_status=pending candidate must not be pushed")
	assert.False(t, got[migratedPending.ID], "legacy_status=pending (migrated) candidate must not be pushed")
	assert.True(t, got[appliedInsight.ID], "grounded (applied) insight must be pushed")
	assert.True(t, got[migratedApproved.ID], "grounded (legacy approved) insight must be pushed")
	assert.True(t, got[migratedThenApproved.ID],
		"grounded insight with stale legacy_status=pending must be pushed (insight_status is authoritative)")
	assert.True(t, got[preference.ID], "non-insight memory must be pushed")

	// User-scoped path: createdBy set to the owner. The caller sees their own
	// candidates; the pending gate does not apply.
	own, err := store.EntityLookup(ctx, urn, other, owner)
	require.NoError(t, err)
	gotOwn := ids(own)
	assert.True(t, gotOwn[pendingInsight.ID], "owner sees their own insight_status=pending candidate")
	assert.True(t, gotOwn[migratedPending.ID], "owner sees their own legacy_status=pending candidate")
	assert.Len(t, own, 6, "user-scoped lookup returns all of the owner's entity-linked records")
}

// TestMemoryStore_SimilarActivePairs_RealDB exercises the #762 backstop query
// against real pgvector: the lateral self-join must pair a user's two
// near-identical ACTIVE records exactly once (mirror rows deduplicated, older
// first), and must not pair across owners, against superseded records, or
// across dissimilar content. sqlmock cannot validate any of this SQL.
func TestMemoryStore_SimilarActivePairs_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	// 768-dim embeddings: base and near are almost parallel (cosine ~0.995);
	// distinct is orthogonal to both.
	base := make([]float32, 768)
	base[0] = 1
	near := make([]float32, 768)
	near[0] = 1
	near[1] = 0.1
	distinct := make([]float32, 768)
	distinct[2] = 1

	rec := func(id, owner, status string, emb []float32) Record {
		return Record{
			ID: id, CreatedBy: owner, Dimension: DimensionKnowledge, SinkClass: SinkBusinessKnowledge,
			Content: "content for " + id, Category: CategoryBusinessCtx, Confidence: ConfidenceMedium,
			Source: SourceUser, Status: status, Embedding: emb, EmbeddingModel: "test",
		}
	}
	for _, r := range []Record{
		rec("mem_762_dup_a", "a@example.com", StatusActive, base),
		rec("mem_762_dup_b", "a@example.com", StatusActive, near),
		rec("mem_762_distinct", "a@example.com", StatusActive, distinct),
		rec("mem_762_superseded_twin", "a@example.com", StatusSuperseded, base),
		rec("mem_762_other_user_twin", "b@example.com", StatusActive, base),
	} {
		require.NoError(t, store.Insert(ctx, r))
	}

	finder, ok := any(store).(DuplicateFinder)
	require.True(t, ok, "postgres store must implement DuplicateFinder")

	pairs, err := finder.SimilarActivePairs(ctx, "a@example.com", 0.9, 10)
	require.NoError(t, err)
	require.Len(t, pairs, 1, "exactly one active pair must be found for the owner (no mirrors, no superseded)")
	assert.Equal(t, "mem_762_dup_a", pairs[0].Older.ID)
	assert.Equal(t, "mem_762_dup_b", pairs[0].Newer.ID)
	assert.Greater(t, pairs[0].Score, 0.9)

	// The owner scope is the per-user privacy boundary: user B has only one
	// active embedded record, so no pair — and never user A's content.
	pairsB, err := finder.SimilarActivePairs(ctx, "b@example.com", 0.9, 10)
	require.NoError(t, err)
	assert.Empty(t, pairsB, "another user's records must never appear in a caller's pair listing")
}

// TestStalenessWatcher_RealDB_VerifiesOnlyEntityLinkedRecords is #1625
// criterion 1. The watcher's batch query and its marks are SQL, so this is
// where the rule holds or does not: a record naming an entity is checked and
// stamped, a record naming none is left alone, and neither record's updated_at
// moves, because verification reads the catalog and changes nothing.
func TestStalenessWatcher_RealDB_VerifiesOnlyEntityLinkedRecords(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	linked := Record{
		ID:        "mem_stale_linked",
		Content:   "The daily_sales table is partitioned by transaction_date.",
		Dimension: DimensionKnowledge,
		SinkClass: SinkSchemaEntity,
		Category:  CategoryBusinessCtx,
		Source:    SourceUser,
		Status:    StatusActive,
		EntityURNs: []string{
			"urn:li:dataset:(urn:li:dataPlatform:trino,hive.retail.daily_sales,PROD)",
		},
	}
	unlinked := Record{
		ID:        "mem_stale_unlinked",
		Content:   "Revenue is reported net of refunds in every board deck.",
		Dimension: DimensionKnowledge,
		SinkClass: SinkBusinessKnowledge,
		Category:  CategoryBusinessCtx,
		Source:    SourceUser,
		Status:    StatusActive,
	}
	require.NoError(t, store.Insert(ctx, linked))
	require.NoError(t, store.Insert(ctx, unlinked))

	before := map[string]time.Time{
		linked.ID:   mustGet(t, store, linked.ID).UpdatedAt,
		unlinked.ID: mustGet(t, store, unlinked.ID).UpdatedAt,
	}

	w := NewStalenessWatcher(store, &mockSemanticProvider{}, StalenessConfig{})
	require.NoError(t, w.checkBatch(ctx))
	require.NoError(t, w.checkBatch(ctx))

	got := mustGet(t, store, linked.ID)
	require.NotNil(t, got.LastVerified, "an entity-linked record the watcher checked must be stamped verified")
	assert.Equal(t, StatusActive, got.Status)
	assert.WithinDuration(t, before[linked.ID], got.UpdatedAt, time.Millisecond,
		"verification must not re-date updated_at: it is the column readers take as the last content change (#1625)")

	got = mustGet(t, store, unlinked.ID)
	assert.Nil(t, got.LastVerified,
		"a record naming no entity is not something the watcher can verify; last_verified must stay null")
	assert.WithinDuration(t, before[unlinked.ID], got.UpdatedAt, time.Millisecond,
		"a record the watcher skipped must not be re-dated either")
}

// TestStalenessWatcher_RealDB_BatchIsCheckableRecords is #1625 criterion 3: a
// batch of N costs N catalog lookups. The entity-less records outnumber the
// batch and sort first (last_verified null), so a watcher that filtered in the
// Go loop rather than in the query would spend the whole batch on them and
// check nothing.
func TestStalenessWatcher_RealDB_BatchIsCheckableRecords(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	const (
		unlinkedCount = 8
		linkedCount   = 5
		batchSize     = 3
	)
	for i := range unlinkedCount {
		require.NoError(t, store.Insert(ctx, Record{
			ID:        fmt.Sprintf("mem_batch_unlinked_%d", i),
			Content:   fmt.Sprintf("Operational rule number %d that names no dataset.", i),
			Dimension: DimensionKnowledge,
			SinkClass: SinkOperationalRule,
			Category:  CategoryUsageGuidance,
			Source:    SourceUser,
			Status:    StatusActive,
		}))
	}
	for i := range linkedCount {
		require.NoError(t, store.Insert(ctx, Record{
			ID:        fmt.Sprintf("mem_batch_linked_%d", i),
			Content:   fmt.Sprintf("Table number %d is refreshed nightly at 02:00 UTC.", i),
			Dimension: DimensionKnowledge,
			SinkClass: SinkSchemaEntity,
			Category:  CategoryBusinessCtx,
			Source:    SourceUser,
			Status:    StatusActive,
			EntityURNs: []string{
				fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,hive.retail.t%d,PROD)", i),
			},
		}))
	}

	var lookups int
	sp := &mockSemanticProvider{
		tableCtxFn: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			lookups++
			return &semantic.TableContext{}, nil
		},
	}
	w := NewStalenessWatcher(store, sp, StalenessConfig{BatchSize: batchSize})
	require.NoError(t, w.checkBatch(ctx))

	assert.Equal(t, batchSize, lookups, "a batch of %d must cost %d catalog lookups", batchSize, batchSize)

	verified, _, err := store.List(ctx, Filter{Status: StatusActive, Limit: MaxLimit})
	require.NoError(t, err)
	var stamped int
	for _, rec := range verified {
		if rec.LastVerified == nil {
			continue
		}
		stamped++
		assert.NotEmpty(t, rec.EntityURNs, "record %s was stamped verified but names no entity", rec.ID)
	}
	assert.Equal(t, batchSize, stamped, "the watcher must stamp exactly the records it checked")
}

// mustGet reads one record back, failing the test when it is missing.
func mustGet(t *testing.T, store Store, id string) *Record {
	t.Helper()
	rec, err := store.Get(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, rec, "record %s must exist", id)
	return rec
}

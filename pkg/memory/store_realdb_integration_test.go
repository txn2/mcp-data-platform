//go:build integration

package memory

// Real-Postgres round-trip test for the memory store. The write path marshals
// slice/map fields to JSONB (nil tolerated as JSON null) and only binds the
// embedding columns when an embedding is present, so a minimal record with no
// embedding must insert and read back cleanly against the real schema.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
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

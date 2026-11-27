//go:build integration

package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestMemoryStore_LexicalSearch_RealDB_MatchesAnyWord is #2838's criterion for
// memory: ranked by words alone, a query no record holds every word of finds
// the records holding some of them, the one holding more first.
func TestMemoryStore_LexicalSearch_RealDB_MatchesAnyWord(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	for _, r := range []Record{
		{ID: "mem_outbound", Content: "Outbound orders are handed to a carrier."},
		{ID: "mem_count", Content: "A count per warehouse per day."},
		{ID: "mem_payroll", Content: "People on the payroll."},
	} {
		r.Dimension, r.Status = "knowledge", StatusActive
		require.NoError(t, store.Insert(ctx, r))
	}

	results, err := store.LexicalSearch(ctx, LexicalQuery{QueryText: "outbound orders count", Dimension: "knowledge", Limit: 10})
	require.NoError(t, err)
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.Record.ID)
	}
	require.Equal(t, []string{"mem_outbound", "mem_count"}, ids)
}

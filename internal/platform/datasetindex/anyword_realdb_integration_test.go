//go:build integration

package datasetindex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// TestCatalogIndexRealDB_LexicalMatchesAnyWord is #2838's criterion for the
// catalog: an intent no single dataset holds every word of finds the datasets
// holding some of them while the embedder cannot answer, the one holding more
// of them first.
func TestCatalogIndexRealDB_LexicalMatchesAnyWord(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()

	require.NoError(t, store.Sync(ctx, []Entry{
		{URN: "urn:outbound", Name: "ops.outbound_orders", Description: "Outbound orders handed to a carrier."},
		{URN: "urn:counts", Name: "ops.daily_counts", Description: "A count per warehouse per day."},
		{URN: "urn:hr", Name: "hr.employees", Description: "People on the payroll."},
	}))

	hits, err := store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "outbound orders count"})
	require.NoError(t, err)
	urns := make([]string, 0, len(hits))
	for _, h := range hits {
		urns = append(urns, h.URN)
	}
	require.Equal(t, []string{"urn:outbound", "urn:counts"}, urns)

	none, err := store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "how many of the"})
	require.NoError(t, err)
	require.Empty(t, none, "an intent of stop words alone matches nothing")
}

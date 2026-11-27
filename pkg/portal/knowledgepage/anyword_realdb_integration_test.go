//go:build integration

package knowledgepage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestPages_RealDB_LexicalMatchesAnyWord is #2838's criterion for knowledge
// pages: ranked by words alone, a query no page holds every word of finds the
// pages holding some of them, the one holding more first.
func TestPages_RealDB_LexicalMatchesAnyWord(t *testing.T) {
	store := &postgresStore{db: testdb.New(t)}
	ctx := context.Background()

	for _, p := range []Page{
		{Slug: "outbound", Title: "Outbound orders", Body: "Outbound orders are handed to a carrier."},
		{Slug: "tally", Title: "Warehouse tally", Body: "A count per warehouse per day."},
		{Slug: "payroll", Title: "Payroll", Body: "People on the payroll."},
	} {
		p.ID, p.CreatedBy, p.CreatedEmail, p.Tags = NewID(), "alice@example.com", "alice@example.com", []string{}
		require.NoError(t, store.Insert(ctx, p))
	}

	results, err := store.Search(ctx, SearchQuery{QueryText: "outbound orders count", Limit: 10})
	require.NoError(t, err)
	slugs := make([]string, 0, len(results))
	for _, r := range results {
		slugs = append(slugs, r.Page.Slug)
	}
	require.Equal(t, []string{"outbound", "tally"}, slugs)
}

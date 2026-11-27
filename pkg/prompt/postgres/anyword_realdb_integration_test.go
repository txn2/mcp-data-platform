//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// TestStore_SearchLexical_RealDB_MatchesAnyWord is #2838's criterion for
// prompts: ranked by words alone, a query no prompt holds every word of finds
// the prompts holding some of them, the one holding more first.
func TestStore_SearchLexical_RealDB_MatchesAnyWord(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	for name, content := range map[string]string{
		"outbound-orders": "Outbound orders are handed to a carrier.",
		"warehouse-tally": "A count per warehouse per day.",
		"payroll":         "People on the payroll.",
	} {
		require.NoError(t, store.Create(ctx, &prompt.Prompt{
			Name: name, Content: content, Scope: prompt.ScopeGlobal,
			Status: prompt.StatusApproved, Enabled: true, Source: prompt.SourceOperator,
		}))
	}

	results, err := store.Search(ctx, prompt.SearchQuery{QueryText: "outbound orders count", Limit: 10})
	require.NoError(t, err)
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Prompt.Name)
	}
	require.Equal(t, []string{"outbound-orders", "warehouse-tally"}, names)
}

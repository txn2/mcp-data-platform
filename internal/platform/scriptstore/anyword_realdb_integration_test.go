//go:build integration

package scriptstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_LexicalSearchMatchesAnyWord is #2838's criterion for managed
// scripts: ranked by words alone, an intent no script holds every word of
// finds the scripts holding some of them, the one holding more first.
func TestRealDB_LexicalSearchMatchesAnyWord(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	mk := func(name, display, desc string) *script.Script {
		sc := &script.Script{
			Name: name, DisplayName: display, Description: desc,
			Source: "def main():\n    print(1)\n", OwnerEmail: "jane@example.com", Enabled: true,
		}
		require.NoError(t, s.Create(ctx, sc, testAuthor))
		return sc
	}
	outbound := mk("outbound", "Outbound orders", "Lists outbound orders handed to a carrier")
	counts := mk("counts", "Warehouse tally", "A count per warehouse per day")
	mk("payroll", "Payroll", "People on the payroll")

	got, err := s.Search(ctx, script.SearchQuery{QueryText: "outbound orders count"})
	require.NoError(t, err)
	require.Equal(t, []string{outbound.ID, counts.ID}, scriptIDs(got))
}

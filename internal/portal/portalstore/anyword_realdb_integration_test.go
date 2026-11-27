//go:build integration

package portalstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestSearchAssetsLexical_RealDB_MatchesAnyWord is #2838's criterion for the
// ranked asset search (internal/sqlrank): ranked by words alone, a query no
// asset holds every word of finds the assets holding some of them, the one
// holding more first.
func TestSearchAssetsLexical_RealDB_MatchesAnyWord(t *testing.T) {
	store := &postgresAssetStore{db: testdb.New(t)}
	ctx := context.Background()

	for _, a := range []portaldomain.Asset{
		{ID: "asset_outbound", Name: "Outbound orders", Description: "Outbound orders handed to a carrier."},
		{ID: "asset_count", Name: "Warehouse tally", Description: "A count per warehouse per day."},
		{ID: "asset_payroll", Name: "Payroll", Description: "People on the payroll."},
	} {
		a.OwnerID, a.OwnerEmail, a.ContentType = realdbOwner, "u@example.com", "text/html"
		a.S3Bucket, a.S3Key, a.Tags, a.CurrentVersion = "portal-assets", a.ID, []string{}, 1
		require.NoError(t, store.Insert(ctx, a))
	}

	results, err := store.SearchAssets(ctx, portaldomain.AssetSearchQuery{
		QueryText: "outbound orders count", Owner: portaldomain.NewAssetOwner(realdbOwner, ""), Limit: 10,
	})
	require.NoError(t, err)
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.Asset.ID)
	}
	require.Equal(t, []string{"asset_outbound", "asset_count"}, ids)
}

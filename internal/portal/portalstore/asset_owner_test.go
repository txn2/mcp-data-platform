package portalstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// TestApplyAssetFilterOwnerArms renders the ownership predicate the listing
// applies. The address arm is what returns a managed script's output to the
// person who owns the script (#1551); the sentinel and the empty address must
// never reach a parameter, or every unattributed asset would answer an
// unauthenticated caller.
func TestApplyAssetFilterOwnerArms(t *testing.T) {
	tests := []struct {
		name      string
		owner     portaldomain.AssetOwner
		wantSQL   string
		wantArgs  []any
		wantNoRef bool
	}{
		{
			name:     "both identifiers",
			owner:    portaldomain.NewAssetOwner("u1", "Alice@example.com"),
			wantSQL:  "(owner_id = $1 OR LOWER(owner_email) = LOWER($2))",
			wantArgs: []any{"u1", "Alice@example.com"},
		},
		{
			name:     "an id with no address",
			owner:    portaldomain.NewAssetOwner("u1", ""),
			wantSQL:  "owner_id = $1",
			wantArgs: []any{"u1"},
		},
		{
			name:     "an address with no id",
			owner:    portaldomain.NewAssetOwner("", "alice@example.com"),
			wantSQL:  "LOWER(owner_email) = LOWER($1)",
			wantArgs: []any{"alice@example.com"},
		},
		{
			name:     "the anonymous sentinel is dropped from both",
			owner:    portaldomain.NewAssetOwner("u1", portaldomain.AnonymousOwner),
			wantSQL:  "owner_id = $1",
			wantArgs: []any{"u1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := applyAssetFilter(psq.Select("id").From("portal_assets"),
				portaldomain.AssetFilter{Owner: tt.owner})
			got, args, err := qb.ToSql()
			require.NoError(t, err)
			assert.Contains(t, got, tt.wantSQL)
			assert.Equal(t, tt.wantArgs, args)
		})
	}
}

// A filter naming nobody carries no ownership predicate at all: that is the
// administrator's all-owners listing, and every owner-scoped caller is refused
// before it reaches the store.
func TestApplyAssetFilterUnidentifiedOwnerAddsNoPredicate(t *testing.T) {
	got, args, err := applyAssetFilter(psq.Select("id").From("portal_assets"),
		portaldomain.AssetFilter{}).ToSql()
	require.NoError(t, err)
	assert.NotContains(t, got, "owner_id")
	assert.NotContains(t, got, "owner_email")
	assert.Empty(t, args)
}

// The ranked-search statements bind the same two arms, numbered from the first
// placeholder the caller has left.
func TestAssetSearchOwnerScope(t *testing.T) {
	scope, args := assetSearchOwnerScope(portaldomain.NewAssetOwner("u1", "alice@example.com"), 3)
	assert.Equal(t, "(owner_id = $3 OR LOWER(owner_email) = LOWER($4))", scope)
	assert.Equal(t, []any{"u1", "alice@example.com"}, args)

	scope, args = assetSearchOwnerScope(portaldomain.NewAssetOwner("u1", ""), 2)
	assert.Equal(t, "owner_id = $2", scope)
	assert.Equal(t, []any{"u1"}, args)

	scope, args = assetSearchOwnerScope(portaldomain.NewAssetOwner("", "alice@example.com"), 2)
	assert.Equal(t, "LOWER(owner_email) = LOWER($2)", scope)
	assert.Equal(t, []any{"alice@example.com"}, args)
}

// Both ranked statements have to carry the arm, not just the one the
// deployment happens to run: which arm runs is decided by whether an embedding
// provider is configured.
func TestRankedSearchStatementsCarryTheAddressArm(t *testing.T) {
	q := portaldomain.AssetSearchQuery{
		Embedding: []float32{0.1},
		QueryText: "revenue",
		Owner:     portaldomain.NewAssetOwner("u1", "alice@example.com"),
	}
	hybrid, hybridArgs := buildAssetHybridSearch(q)
	assert.Contains(t, hybrid, "(owner_id = $3 OR LOWER(owner_email) = LOWER($4))")
	require.Len(t, hybridArgs, 4)
	assert.Equal(t, "u1", hybridArgs[2])
	assert.Equal(t, "alice@example.com", hybridArgs[3])

	q.Embedding = nil
	lexical, lexicalArgs := buildAssetLexicalSearch(q)
	assert.Contains(t, lexical, "(owner_id = $2 OR LOWER(owner_email) = LOWER($3))")
	assert.Equal(t, []any{"revenue", "u1", "alice@example.com"}, lexicalArgs)
}

// A search for a caller with neither identifier never reaches the database: an
// empty owner scope would rank every asset on the platform.
func TestSearchAssetsRefusesAnUnidentifiedCaller(t *testing.T) {
	store := &postgresAssetStore{}
	scored, err := store.SearchAssets(t.Context(), portaldomain.AssetSearchQuery{QueryText: "revenue"})
	require.NoError(t, err)
	assert.Empty(t, scored)
}

// An identity naming nobody matches nothing rather than everything. The callers
// guard before they get here; this pins what happens if one ever does not.
func TestOwnerArmsOfAnUnidentifiedOwnerMatchNothing(t *testing.T) {
	got, args, err := assetOwnerPredicate(portaldomain.AssetOwner{}).ToSql()
	require.NoError(t, err)
	assert.Equal(t, "FALSE", got)
	assert.Empty(t, args)

	scope, scopeArgs := assetSearchOwnerScope(portaldomain.AssetOwner{}, 2)
	assert.Equal(t, "FALSE", scope)
	assert.Empty(t, scopeArgs)
}

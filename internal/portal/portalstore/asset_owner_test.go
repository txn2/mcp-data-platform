package portalstore

import (
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
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
		{
			// A run is judged on the address alone: its principal is
			// script:<name> and a script name is unique only within its owner
			// (#1579).
			name:     "an unattended caller binds no principal",
			owner:    portaldomain.NewAssetOwner("script:daily-sales", "alice@example.com").ActingFor("alice@example.com"),
			wantSQL:  "LOWER(owner_email) = LOWER($1)",
			wantArgs: []any{"alice@example.com"},
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
	scope, args := assetSearchScope(portaldomain.AssetSearchQuery{Owner: portaldomain.NewAssetOwner("u1", "alice@example.com")}, 3)
	assert.Equal(t, "(owner_id = $3 OR LOWER(owner_email) = LOWER($4))", scope)
	assert.Equal(t, []any{"u1", "alice@example.com"}, args)

	scope, args = assetSearchScope(portaldomain.AssetSearchQuery{Owner: portaldomain.NewAssetOwner("u1", "")}, 2)
	assert.Equal(t, "owner_id = $2", scope)
	assert.Equal(t, []any{"u1"}, args)

	scope, args = assetSearchScope(portaldomain.AssetSearchQuery{Owner: portaldomain.NewAssetOwner("", "alice@example.com")}, 2)
	assert.Equal(t, "LOWER(owner_email) = LOWER($2)", scope)
	assert.Equal(t, []any{"alice@example.com"}, args)

	// The two renderings of the same judgment must agree, or a run would rank
	// rows a listing would not return (#1579).
	scope, args = assetSearchScope(portaldomain.AssetSearchQuery{
		Owner: portaldomain.NewAssetOwner("script:daily-sales", "alice@example.com").
			ActingFor("alice@example.com"),
	}, 2)
	assert.Equal(t, "LOWER(owner_email) = LOWER($2)", scope)
	assert.Equal(t, []any{"alice@example.com"}, args)
}

// The listing predicate and the ranked-search statement are two renderings of
// one judgment, and Owns is a third. A row either belongs to a caller on all
// three or on none: the collision #1579 fixes was reachable through the search
// as well as the listing, so a fix in one rendering only would have left it.
func TestOwnerRenderingsAgree(t *testing.T) {
	owners := map[string]portaldomain.AssetOwner{
		"person": portaldomain.NewAssetOwner("u-alice", "alice@example.com"),
		"run":    portaldomain.NewAssetOwner("script:daily-sales", "alice@example.com").ActingFor("alice@example.com"),
	}
	for name, owner := range owners {
		t.Run(name, func(t *testing.T) {
			listing, listArgs, err := assetOwnerPredicate(owner).ToSql()
			require.NoError(t, err)
			// The listing is assembled by squirrel, which numbers its
			// placeholders when the whole statement is rendered; the search
			// numbers its own. Compare them in the same dialect.
			listing, err = sq.Dollar.ReplacePlaceholders(listing)
			require.NoError(t, err)
			search, searchArgs := assetSearchScope(portaldomain.AssetSearchQuery{Owner: owner}, 1)
			assert.Equal(t, listing, search,
				"the listing and the ranked search must render the same arm")
			assert.Equal(t, listArgs, searchArgs)
		})
	}

	// The producer scope is the third rendering, and the two statements have to
	// agree about it too: a run ranks and lists exactly the rows its own script
	// produced (#1579).
	producer := portaldomain.NewContentProducer(producedby.KindScript, "script-uuid")
	t.Run("run inventory", func(t *testing.T) {
		listing, listArgs, err := producedByPredicate(
			producedby.TargetAsset, "portal_assets.id", producer).ToSql()
		require.NoError(t, err)
		listing, err = sq.Dollar.ReplacePlaceholders(listing)
		require.NoError(t, err)
		search, searchArgs := assetSearchScope(
			portaldomain.AssetSearchQuery{ProducedBy: producer}, 1)
		assert.Equal(t, listing, search)
		assert.Equal(t, listArgs, searchArgs)
		assert.Equal(t, []any{producedby.TargetAsset, producedby.KindScript, "script-uuid"}, searchArgs)
	})

	// A query carrying both narrows to their intersection on both statements.
	// No surface in the tree sets both, but AssetSearchQuery is on the
	// supported surface, and a search returning MORE than the listing for one
	// query would be a widening nobody asked for.
	t.Run("owner and producer", func(t *testing.T) {
		owner := portaldomain.NewAssetOwner("u-alice", "alice@example.com")
		qb := applyAssetFilter(psq.Select("id").From("portal_assets"),
			portaldomain.AssetFilter{Owner: owner, ProducedBy: producer})
		listing, listArgs, err := qb.ToSql()
		require.NoError(t, err)
		search, searchArgs := assetSearchScope(
			portaldomain.AssetSearchQuery{Owner: owner, ProducedBy: producer}, 1)

		// The producer arm binds three values from $1, so the ownership arm
		// that follows it starts at $4. Each statement orders its own bindings;
		// what has to match is the set of values and the fact that both arms
		// are conjoined.
		assert.Contains(t, search, "content_producers")
		assert.Contains(t, search, "owner_id = $4")
		assert.Contains(t, search, "LOWER(owner_email) = LOWER($5)")
		assert.Contains(t, search, " AND ")
		assert.ElementsMatch(t, listArgs, searchArgs,
			"both statements bind the same values")
		assert.Contains(t, listing, "content_producers")
		assert.Contains(t, listing, "owner_id =")
		assert.Contains(t, listing, "LOWER(owner_email)")
	})
}

// The inventory is what a producer CREATED. A producer row is written for every
// version too, so without cp.created a script that wrote one version over
// somebody else's asset would carry that asset in its own listing from then on.
func TestProducedByPredicateMatchesOnlyWhatItCreated(t *testing.T) {
	got, _, err := producedByPredicate(producedby.TargetAsset, "portal_assets.id",
		portaldomain.NewContentProducer(producedby.KindScript, "script-uuid")).ToSql()
	require.NoError(t, err)
	assert.Contains(t, got, "AND cp.created")
}

// A run's enumeration is scoped by the producer alone: the row's own
// identifiers name a script only ambiguously (the principal) or not at all
// after a transfer (the address), so neither is an arm here.
func TestApplyAssetFilterProducerScope(t *testing.T) {
	got, args, err := applyAssetFilter(psq.Select("id").From("portal_assets"),
		portaldomain.AssetFilter{
			ProducedBy: portaldomain.NewContentProducer(producedby.KindScript, "script-uuid"),
		}).ToSql()
	require.NoError(t, err)
	assert.Contains(t, got, "EXISTS (SELECT 1 FROM content_producers cp")
	assert.Contains(t, got, "cp.target_id = portal_assets.id",
		"the id must be qualified or it binds to content_producers' own id")
	assert.NotContains(t, got, "owner_id =")
	assert.NotContains(t, got, "owner_email")
	assert.Equal(t, []any{producedby.TargetAsset, producedby.KindScript, "script-uuid"}, args)

	// A half-named producer scopes nothing rather than every row of its kind.
	_, args, err = applyAssetFilter(psq.Select("id").From("portal_assets"),
		portaldomain.AssetFilter{ProducedBy: portaldomain.ContentProducer{Kind: producedby.KindScript}}).ToSql()
	require.NoError(t, err)
	assert.Empty(t, args)
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

	scope, scopeArgs := assetSearchScope(portaldomain.AssetSearchQuery{}, 2)
	assert.Equal(t, "FALSE", scope)
	assert.Empty(t, scopeArgs)
}

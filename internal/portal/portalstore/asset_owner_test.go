package portalstore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/internal/sqlrank"
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

// A ranking reads the listing's own statement over the same filter, so the
// listing and the search cannot disagree about whose row a row is: the
// collision #1579 fixes was reachable through the search as well as the
// listing. Each shape the search is asked in renders the predicate the
// listing count does, with the same arguments.
func TestRankedCandidatesAreTheListing(t *testing.T) {
	producer := portaldomain.NewContentProducer(producedby.KindScript, "script-uuid")
	for name, filter := range map[string]portaldomain.AssetFilter{
		"person":             {Owner: portaldomain.NewAssetOwner("u-alice", "alice@example.com")},
		"run":                {Owner: portaldomain.NewAssetOwner("script:daily-sales", "alice@example.com").ActingFor("alice@example.com")},
		"run inventory":      {ProducedBy: producer},
		"owner and producer": {Owner: portaldomain.NewAssetOwner("u-alice", "alice@example.com"), ProducedBy: producer},
	} {
		t.Run(name, func(t *testing.T) {
			ranked, rankedArgs, err := rankedCandidates(filter)
			require.NoError(t, err)
			count, countArgs, err := buildAssetCount(filter)
			require.NoError(t, err)
			assert.Equal(t, whereOf(count), whereOf(ranked), "the ranking reads the listing's predicate")
			assert.Equal(t, countArgs, rankedArgs)
		})
	}
}

// whereOf is a statement's WHERE clause.
func whereOf(stmt string) string {
	_, where, _ := strings.Cut(stmt, " WHERE ")
	return where
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

// The ranked statement binds the owner's address beside its id, whichever
// mode runs: hybrid when an embedding provider is configured, lexical when
// not.
func TestRankedSearchStatementsCarryTheAddressArm(t *testing.T) {
	inner, args, err := rankedCandidates(portaldomain.AssetFilter{Owner: portaldomain.NewAssetOwner("u1", "alice@example.com")})
	require.NoError(t, err)
	for _, emb := range [][]float32{{0.1}, nil} {
		stmt, stmtArgs := sqlrank.BuildRanked(sqlrank.Ranking{Candidates: inner, Args: args, FTS: searchFTS, QueryText: "revenue", Embedding: emb})
		assert.Contains(t, stmt, "(owner_id = $1 OR LOWER(owner_email) = LOWER($2))")
		assert.Equal(t, []any{"u1", "alice@example.com", "revenue"}, stmtArgs[:3])
	}
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
}

// Every tag named and every metadata value must hold (#1848): one containment
// per axis, the single tag folded in with the repeated ones.
func TestApplyAssetFilterTagsAndMetadataAreANDed(t *testing.T) {
	got, args, err := applyAssetFilter(psq.Select("id").From("portal_assets"), portaldomain.AssetFilter{
		Tag: "report:sales", Tags: []string{"", "tenant:x"}, Metadata: map[string]string{"region": "west"},
	}).ToSql()
	require.NoError(t, err)
	assert.Contains(t, got, "tags @> $1::jsonb")
	assert.Contains(t, got, "portal_assets.metadata @> $2::jsonb")
	assert.Equal(t, []any{`["report:sales","tenant:x"]`, `{"region":"west"}`}, args)
}

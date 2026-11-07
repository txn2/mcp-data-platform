//go:build integration

package portalstore

import (
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
	"github.com/txn2/mcp-data-platform/internal/sqlrank"
)

// reachSQLSamples renders the statements the listings, ranked searches,
// facets and permissions assemble over a reach (#2500), each scope and both
// ranking modes, and the owner and producer searches, for the statement gate.
func reachSQLSamples() map[string]string {
	out := map[string]string{}
	vec := make([]float32, 768)
	owners := []string{"Owner@Example.com"}
	ranked := func(name string, inner string, args []any) {
		out[name+"/hybrid"], _ = sqlrank.BuildRanked(sqlrank.Ranking{Candidates: inner, Args: args, FTS: searchFTS, QueryText: "quarterly report", Embedding: vec, Limit: 10, Offset: 10})
		out[name+"/lexical"], _ = sqlrank.BuildRanked(sqlrank.Ranking{Candidates: inner, Args: args, FTS: searchFTS, QueryText: "quarterly report", Limit: 10})
	}
	for _, scope := range []portaldomain.Scope{portaldomain.ScopeMine, portaldomain.ScopeShared, portaldomain.ScopeAll} {
		r := portaldomain.NewReach("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com", "analyst", scope)
		name := string(scope)
		filter := portaldomain.AssetFilter{Reach: r, Owners: owners, ContentType: "text/csv", Tags: []string{"finance"}, Limit: 10}
		out["reach/assetCount/"+name], _, _ = buildAssetCount(filter)
		out["reach/assetSelect/"+name], _, _ = buildAssetSelect(filter)
		st, _ := buildFacets(filter)
		out["reach/tagFacet/"+name], out["reach/authorFacet/"+name] = st.tags, st.authors
		inner, args, _ := rankedCandidates(filter)
		ranked("reach/rankedAssets/"+name, inner, args)
		inner, args, _ = collectionCandidates(portaldomain.CollectionFilter{Reach: r, Owners: owners})
		ranked("reach/rankedCollections/"+name, inner, args)
		count, sel := (*postgresCollectionStore)(nil).buildListQueries(portaldomain.CollectionFilter{Reach: r, Owners: owners}, 10)
		out["reach/collectionCount/"+name], _, _ = count.ToSql()
		out["reach/collectionSelect/"+name], _, _ = sel.ToSql()
		out["reach/collectionAuthors/"+name], _, _ = buildCollectionAuthors(r)
	}
	for name, filter := range map[string]portaldomain.AssetFilter{
		"owner":  {Owner: portaldomain.NewAssetOwner("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com")},
		"idOnly": {Owner: portaldomain.NewAssetOwner("550e8400-e29b-41d4-a716-446655440444", "")},
		"script": {ProducedBy: portaldomain.NewContentProducer("script", "7c1f0e2b-5a44-4c19-9f3e-8b2d6a1c4e07")},
		"ids":    {IDs: []string{"a1", "a2"}, Limit: 2},
	} {
		inner, args, _ := rankedCandidates(filter)
		ranked("rankedAssets/"+name, inner, args)
	}
	inner, args, _ := collectionCandidates(portaldomain.CollectionFilter{OwnerID: "550e8400-e29b-41d4-a716-446655440444"})
	ranked("rankedCollections/owner", inner, args)
	out["reach/permissions"], _, _ = reachstore.BuildPermissions([]string{"a1", "a2"},
		portaldomain.NewReach("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com", "analyst", portaldomain.ScopeAll))
	out["reach/permissions/personaOnly"], _, _ = reachstore.BuildPermissions([]string{"a1"}, portaldomain.NewReach("", "", "analyst", portaldomain.ScopeAll))
	return out
}

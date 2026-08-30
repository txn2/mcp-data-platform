//go:build integration

package portalstore

import "github.com/txn2/mcp-data-platform/internal/portal/portaldomain"

// SQLSamples renders each statement this package assembles at run time, for the
// gate that hands store SQL to a real PostgreSQL to parse and plan (#1512).
//
// A statement built from a format string has no text in the source, so nothing
// could reach it: it is these builders, called here with representative inputs,
// that the gate prepares. Both target columns of the share statements are
// rendered, since a column name is exactly what the two renderings differ in.
//
// The file is integration-tagged, so it is absent from the default build.
func SQLSamples() map[string]string {
	// 768 is the width of the embedding columns (migration 000068 and its
	// successors). The gate prepares rather than executes, so the values do not
	// matter, but a vector of the declared width is what types $1.
	assetQuery := portaldomain.AssetSearchQuery{
		Embedding: make([]float32, 768),
		QueryText: "quarterly report",
		Owner: portaldomain.NewAssetOwner(
			"550e8400-e29b-41d4-a716-446655440444", "analyst@example.com"),
		Limit: 10,
	}
	// A caller with no address renders a different ownership arm, and an
	// arm nothing prepares is an arm the gate does not cover.
	assetQueryIDOnly := portaldomain.AssetSearchQuery{
		Embedding: make([]float32, 768),
		QueryText: "quarterly report",
		Owner:     portaldomain.NewAssetOwner("550e8400-e29b-41d4-a716-446655440444", ""),
		Limit:     10,
	}
	collectionQuery := portaldomain.CollectionSearchQuery{
		Embedding: make([]float32, 768),
		QueryText: "quarterly report",
		OwnerID:   "550e8400-e29b-41d4-a716-446655440444",
		Limit:     10,
	}

	// The listing's ownership arm is assembled, not written down, and it is the
	// arm a script's output is returned through, so both shapes are prepared.
	listFilter := portaldomain.AssetFilter{
		Owner: portaldomain.NewAssetOwner(
			"550e8400-e29b-41d4-a716-446655440444", "analyst@example.com"),
		ContentType: "text/csv", Tag: "script", Search: "revenue", Limit: 10, Offset: 20,
	}
	listFilterIDOnly := portaldomain.AssetFilter{
		Owner:            portaldomain.NewAssetOwner("550e8400-e29b-41d4-a716-446655440444", ""),
		ThumbnailPending: true,
	}
	assetCount, _, _ := buildAssetCount(listFilter)
	assetSelect, _, _ := buildAssetSelect(listFilter)
	assetCountIDOnly, _, _ := buildAssetCount(listFilterIDOnly)
	assetSelectIDOnly, _, _ := buildAssetSelect(listFilterIDOnly)

	assetHybrid, _ := buildAssetHybridSearch(assetQuery)
	assetHybridIDOnly, _ := buildAssetHybridSearch(assetQueryIDOnly)
	assetLexical, _ := buildAssetLexicalSearch(assetQuery)
	assetLexicalIDOnly, _ := buildAssetLexicalSearch(assetQueryIDOnly)
	collectionHybrid, _ := buildCollectionHybridSearch(collectionQuery)

	return map[string]string{
		"buildAssetCount":                 assetCount,
		"buildAssetCount/idOnly":          assetCountIDOnly,
		"buildAssetSelect":                assetSelect,
		"buildAssetSelect/idOnly":         assetSelectIDOnly,
		"buildAssetHybridSearch":          assetHybrid,
		"buildAssetHybridSearch/idOnly":   assetHybridIDOnly,
		"buildAssetLexicalSearch":         assetLexical,
		"buildAssetLexicalSearch/idOnly":  assetLexicalIDOnly,
		"buildCollectionHybridSearch":     collectionHybrid,
		"buildCollectionLexicalSearch":    buildCollectionLexicalSearch(collectionQuery),
		"buildActiveShareForTarget/asset": buildActiveShareForTarget(colAssetID),
		"buildActiveShareForTarget/coll":  buildActiveShareForTarget(colCollectionID),
		"buildShareSummaries/asset":       buildShareSummaries(colAssetID),
		"buildShareSummaries/collection":  buildShareSummaries(colCollectionID),
	}
}

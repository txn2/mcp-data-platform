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
		OwnerID:   "550e8400-e29b-41d4-a716-446655440444",
		Limit:     10,
	}
	collectionQuery := portaldomain.CollectionSearchQuery{
		Embedding: make([]float32, 768),
		QueryText: "quarterly report",
		OwnerID:   "550e8400-e29b-41d4-a716-446655440444",
		Limit:     10,
	}

	assetHybrid, _ := buildAssetHybridSearch(assetQuery)
	collectionHybrid, _ := buildCollectionHybridSearch(collectionQuery)

	return map[string]string{
		"buildAssetHybridSearch":          assetHybrid,
		"buildAssetLexicalSearch":         buildAssetLexicalSearch(assetQuery),
		"buildCollectionHybridSearch":     collectionHybrid,
		"buildCollectionLexicalSearch":    buildCollectionLexicalSearch(collectionQuery),
		"buildActiveShareForTarget/asset": buildActiveShareForTarget(colAssetID),
		"buildActiveShareForTarget/coll":  buildActiveShareForTarget(colCollectionID),
		"buildShareSummaries/asset":       buildShareSummaries(colAssetID),
		"buildShareSummaries/collection":  buildShareSummaries(colCollectionID),
	}
}

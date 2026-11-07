//go:build integration

package portalstore

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
)

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
	// The producer a managed-script run's own enumeration is scoped by. It
	// renders an EXISTS over content_producers, which no other sample reaches
	// (#1579).
	scriptProducerSample := portaldomain.NewContentProducer(
		producedby.KindScript, "7c1f0e2b-5a44-4c19-9f3e-8b2d6a1c4e07")

	// The listing's ownership arm is assembled, not written down, and it is the
	// arm a script's output is returned through, so both shapes are prepared.
	listFilter := portaldomain.AssetFilter{
		Owner: portaldomain.NewAssetOwner(
			"550e8400-e29b-41d4-a716-446655440444", "analyst@example.com"),
		ContentType: "text/csv", Tag: "script", Search: "revenue", Limit: 10, Offset: 20,
		Tags: []string{"report:sales"}, Metadata: map[string]string{"region": "west"},
	}
	listFilterIDOnly := portaldomain.AssetFilter{
		Owner: portaldomain.NewAssetOwner("550e8400-e29b-41d4-a716-446655440444", ""),
	}
	// The renderer's claim (#1787): the owed predicate under a locking
	// subquery, with the listing projection RETURNED.
	thumbnailClaim, _, _ := buildThumbnailClaim(1, time.Minute, 25)
	thumbnailBacklog, _, _ := buildThumbnailBacklog(1)
	thumbnailClear, _, _ := buildThumbnailClear(1, 1000)
	// A managed script run's own inventory joins content_producers rather than
	// reading either identifier on the row (#1579).
	listFilterScript := portaldomain.AssetFilter{
		ProducedBy: scriptProducerSample,
		Limit:      10,
	}
	listFilterBoth := portaldomain.AssetFilter{
		Owner: portaldomain.NewAssetOwner(
			"550e8400-e29b-41d4-a716-446655440444", "analyst@example.com"),
		ProducedBy: scriptProducerSample,
		Limit:      10,
	}
	assetCount, _, _ := buildAssetCount(listFilter)
	assetSelect, _, _ := buildAssetSelect(listFilter)
	assetCountIDOnly, _, _ := buildAssetCount(listFilterIDOnly)
	assetSelectIDOnly, _, _ := buildAssetSelect(listFilterIDOnly)
	assetCountScript, _, _ := buildAssetCount(listFilterScript)
	assetSelectScript, _, _ := buildAssetSelect(listFilterScript)
	assetCountBoth, _, _ := buildAssetCount(listFilterBoth)
	assetSelectBoth, _, _ := buildAssetSelect(listFilterBoth)

	// The collection listing's ownership arm is assembled the same way, and a
	// run's adds a second, conjoined comparison to it.
	collCount, collSelect := (*postgresCollectionStore)(nil).buildListQueries(
		portaldomain.CollectionFilter{
			OwnerID: "550e8400-e29b-41d4-a716-446655440444", Search: "revenue", Offset: 20,
		}, 10)
	collCountScript, collSelectScript := (*postgresCollectionStore)(nil).buildListQueries(
		portaldomain.CollectionFilter{ProducedBy: scriptProducerSample}, 10)
	collCountSQL, _, _ := collCount.ToSql()
	collSelectSQL, _, _ := collSelect.ToSql()
	collCountScriptSQL, _, _ := collCountScript.ToSql()
	collSelectScriptSQL, _, _ := collSelectScript.ToSql()

	samples := map[string]string{
		"buildThumbnailClaim":                      thumbnailClaim,
		"buildCollectionThumbnailClaim":            buildCollectionThumbnailClaim(),
		"buildThumbnailBacklog":                    thumbnailBacklog,
		"collectionThumbnailBacklog":               collectionThumbnailBacklog(),
		"buildThumbnailClear":                      thumbnailClear,
		"buildCollectionThumbnailClear":            buildCollectionThumbnailClear(),
		"recordCollectionThumbnailQuery":           recordCollectionThumbnailQuery,
		"buildAssetCount":                          assetCount,
		"buildAssetCount/idOnly":                   assetCountIDOnly,
		"buildAssetCount/script":                   assetCountScript,
		"buildAssetCount/ownerAndProducer":         assetCountBoth,
		"buildAssetSelect":                         assetSelect,
		"buildAssetSelect/idOnly":                  assetSelectIDOnly,
		"buildAssetSelect/script":                  assetSelectScript,
		"buildAssetSelect/ownerAndProducer":        assetSelectBoth,
		"buildCollectionListQueries/count":         collCountSQL,
		"buildCollectionListQueries/select":        collSelectSQL,
		"buildCollectionListQueries/count/script":  collCountScriptSQL,
		"buildCollectionListQueries/select/script": collSelectScriptSQL,
		"buildActiveShareForTarget/asset":          buildActiveShareForTarget(colAssetID),
		"buildActiveShareForTarget/coll":           buildActiveShareForTarget(colCollectionID),
		"buildShareSummaries/asset":                buildShareSummaries(colAssetID),
		"buildShareSummaries/collection":           buildShareSummaries(colCollectionID),
		// The shared-with-me page carries the provenance summary, which is
		// assembled rather than written down (#1623).
		"buildSharedWithUserSelect": buildSharedWithUserSelect(),
	}
	for name, stmt := range reachSQLSamples() {
		samples[name] = stmt
	}
	return samples
}

package capacity

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestClassify(t *testing.T) {
	l := Layout{PortalBucket: "portal-assets", PortalPrefix: "artifacts", ResourceBucket: "managed-resources"}
	for _, tc := range []struct {
		name, bucket, key, want string
		ok                      bool
	}{
		{"asset content", "portal-assets", "artifacts/u1/a1/content.html", observability.StoragePurposePortalAssets, true},
		{"asset version", "portal-assets", "artifacts/u1/a1/v2/content.csv", observability.StoragePurposePortalAssets, true},
		{"legacy asset", "portal-assets", "portal/u1/a1/content.md", observability.StoragePurposePortalAssets, true},
		{"trino export counts as an asset", "portal-assets", "artifacts/someone/a9/content.parquet", observability.StoragePurposePortalAssets, true},
		{"graphql export", "portal-assets", "artifacts/graphql_export/u1/a1/content.json", observability.StoragePurposeExports, true},
		{"api export", "portal-assets", "artifacts/api_export/u1/a1/content.csv", observability.StoragePurposeExports, true},
		{"script output", "portal-assets", "artifacts/scripts/s1/a1/r1/content.csv", observability.StoragePurposeScriptOutputs, true},
		{"script tile", "portal-assets", "artifacts/scripts/s1/tile.png", observability.StoragePurposeThumbnails, true},
		{"script dark tile", "portal-assets", "artifacts/scripts/s1/tile-dark.png", observability.StoragePurposeThumbnails, true},
		{"asset tile", "portal-assets", "artifacts/u1/a1/.thumbnail.png", observability.StoragePurposeThumbnails, true},
		{"asset dark tile", "portal-assets", "artifacts/u1/a1/.thumbnail_dark.png", observability.StoragePurposeThumbnails, true},
		{"legacy asset tile", "portal-assets", "portal/u1/a1/thumbnail.png", observability.StoragePurposeThumbnails, true},
		{"collection mosaic", "portal-assets", "artifacts/collections/c1/thumbnail.png", observability.StoragePurposeThumbnails, true},
		{"legacy collection mosaic", "portal-assets", "portal/collections/c1/thumbnail_dark.png", observability.StoragePurposeThumbnails, true},
		{"outside the portal prefix", "portal-assets", "elsewhere/x", "", false},
		{"resource", "managed-resources", "resources/global/global/r1/report.pdf", observability.StoragePurposeResources, true},
		{"resource named like a script tile", "managed-resources", "resources/global/global/r1/tile.png", observability.StoragePurposeResources, true},
		{"resource tile", "managed-resources", "resources/global/global/r1/.thumbnail.png", observability.StoragePurposeThumbnails, true},
		{"webhook segment", "managed-resources", "webhooks/src/raw/dt=2026-10-09/hour=01/minute=02/w-1.jsonl.gz", observability.StoragePurposeWebhooks, true},
		{"map archive", "managed-resources", "maps/regions/r1/b-1.pmtiles", observability.StoragePurposeMaps, true},
		{"unknown prefix in the resource bucket", "managed-resources", "other/x", "", false},
		{"another bucket", "someone-else", "artifacts/u1/a1/content.html", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := l.Classify(tc.bucket, tc.key)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestClassify_SharedBucket shows a deployment that points the portal and
// managed resources at one bucket: the resource prefixes win, the rest is the
// portal's.
func TestClassify_SharedBucket(t *testing.T) {
	l := Layout{PortalBucket: "one", PortalPrefix: "artifacts/", ResourceBucket: "one"}
	got, _ := l.Classify("one", "resources/global/global/r1/f.csv")
	assert.Equal(t, observability.StoragePurposeResources, got)
	got, _ = l.Classify("one", "artifacts/u/a/content.html")
	assert.Equal(t, observability.StoragePurposePortalAssets, got)
}

// TestClassify_EmptyPrefix shows a portal with no key prefix owns every key in
// its bucket.
func TestClassify_EmptyPrefix(t *testing.T) {
	l := Layout{PortalBucket: "p"}
	got, ok := l.Classify("p", "u/a/content.html")
	assert.True(t, ok)
	assert.Equal(t, observability.StoragePurposePortalAssets, got)
	assert.Equal(t, []BucketPrefix{{"p", ""}}, l.Prefixes(), "the whole bucket covers the legacy prefix")
}

// TestPrefixes_SharedBucketWithNoPortalPrefix lists the bucket once when the
// portal owns all of it and managed resources share it.
func TestPrefixes_SharedBucketWithNoPortalPrefix(t *testing.T) {
	assert.Equal(t, []BucketPrefix{{"one", ""}}, Layout{PortalBucket: "one", ResourceBucket: "one"}.Prefixes())
}

func TestPrefixes(t *testing.T) {
	l := Layout{PortalBucket: "p", PortalPrefix: "/artifacts/", ResourceBucket: "r"}
	assert.Equal(t, []BucketPrefix{
		{"p", "artifacts/"}, {"p", "portal/"}, {"r", "resources/"}, {"r", "webhooks/"}, {"r", "maps/"},
	}, l.Prefixes())
	assert.Empty(t, Layout{}.Prefixes())
}

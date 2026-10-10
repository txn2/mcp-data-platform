package capacity

import (
	"path"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// legacyPortalPrefix is where the portal wrote assets and collection tiles
// before s3_prefix was honored everywhere (#1903). Rows written then keep
// those keys, so the objects stay under it.
const legacyPortalPrefix = "portal/"

// markerPrefix holds the listing's start marker (scan.go): in a platform
// bucket, never a platform object.
const markerPrefix = "_mcp_platform_capacity/"

// The managed-resources bucket's top-level prefixes, one per purpose
// (pkg/resource BuildS3Key, internal/webhook/whlayout, internal/maps).
const (
	resourcesPrefix = "resources/"
	webhooksPrefix  = "webhooks/"
	mapsPrefix      = "maps/"
)

// The second path segment under the portal prefix that names a purpose; any
// other segment is an owner id, which is an asset.
const (
	segCollections   = "collections"
	segScripts       = "scripts"
	segGraphQLExport = "graphql_export"
	segAPIExport     = "api_export"
)

// Layout is where the platform keeps its objects: the portal's bucket and key
// prefix, and the managed-resources bucket (which also holds webhook segments
// and, unless a deployment points maps elsewhere, basemap archives).
type Layout struct {
	PortalBucket   string
	PortalPrefix   string
	ResourceBucket string
}

// Prefixes is every bucket and key prefix a full listing walks, each once.
func (l Layout) Prefixes() []BucketPrefix {
	var out []BucketPrefix
	seen := map[BucketPrefix]bool{}
	add := func(bucket, prefix string) {
		bp := BucketPrefix{Bucket: bucket, Prefix: prefix}
		if bucket != "" && !seen[bp] {
			seen[bp] = true
			out = append(out, bp)
		}
	}
	add(l.PortalBucket, l.portalPrefix())
	add(l.PortalBucket, legacyPortalPrefix)
	add(l.ResourceBucket, resourcesPrefix)
	add(l.ResourceBucket, webhooksPrefix)
	add(l.ResourceBucket, mapsPrefix)
	return withoutCovered(out)
}

// withoutCovered drops a prefix another prefix of the same bucket already
// covers (a portal with no key prefix lists the whole bucket, the legacy
// prefix and, on a shared bucket, the resource prefixes with it), so a
// listing meets each object once.
func withoutCovered(in []BucketPrefix) []BucketPrefix {
	out := make([]BucketPrefix, 0, len(in))
	for _, bp := range in {
		covered := false
		for _, other := range in {
			if other != bp && other.Bucket == bp.Bucket && strings.HasPrefix(bp.Prefix, other.Prefix) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, bp)
		}
	}
	return out
}

// BucketPrefix is one prefix of one bucket.
type BucketPrefix struct {
	Bucket string
	Prefix string
}

// portalPrefix is the configured prefix with exactly one trailing slash.
func (l Layout) portalPrefix() string {
	p := strings.Trim(l.PortalPrefix, "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// Classify names the purpose of the object at key in bucket by where it sits,
// the one rule the full listing and every put and delete share. ok is false
// for an object outside the platform's prefixes.
//
// A key says what an object is for only as far as its layout does. Inside the
// portal prefix a tile is told apart by its file name, a collection mosaic and
// a script's outputs by their second segment, and a GraphQL or API export by
// its directory; a Trino export is written in an asset's own shape, so it is
// counted with the portal's assets.
func (l Layout) Classify(bucket, key string) (purpose string, ok bool) {
	if strings.HasPrefix(key, markerPrefix) {
		return "", false
	}
	if bucket == l.ResourceBucket {
		switch {
		case strings.HasPrefix(key, resourcesPrefix):
			if isTile(key) {
				return observability.StoragePurposeThumbnails, true
			}
			return observability.StoragePurposeResources, true
		case strings.HasPrefix(key, webhooksPrefix):
			return observability.StoragePurposeWebhooks, true
		case strings.HasPrefix(key, mapsPrefix):
			return observability.StoragePurposeMaps, true
		}
	}
	if bucket != l.PortalBucket {
		return "", false
	}
	rest, ok := l.underPortal(key)
	if !ok {
		return "", false
	}
	return portalPurpose(rest), true
}

// underPortal is key with the portal prefix (or the legacy one) removed.
func (l Layout) underPortal(key string) (string, bool) {
	if p := l.portalPrefix(); p != "" && strings.HasPrefix(key, p) {
		return key[len(p):], true
	}
	if strings.HasPrefix(key, legacyPortalPrefix) {
		return key[len(legacyPortalPrefix):], true
	}
	if l.portalPrefix() == "" {
		return key, true
	}
	return "", false
}

// portalPurpose sorts a key below the portal prefix.
func portalPurpose(rest string) string {
	first, _, _ := strings.Cut(rest, "/")
	base := path.Base(rest)
	switch {
	case isTile(rest), base == legacyTile, base == legacyDarkTile:
		return observability.StoragePurposeThumbnails
	case first == segCollections:
		return observability.StoragePurposeThumbnails
	case first == segScripts && (base == scriptTile || base == scriptDarkTile):
		return observability.StoragePurposeThumbnails
	case first == segScripts:
		return observability.StoragePurposeScriptOutputs
	case first == segGraphQLExport, first == segAPIExport:
		return observability.StoragePurposeExports
	default:
		return observability.StoragePurposePortalAssets
	}
}

// The tile file names: the hidden pair written beside an asset's or a
// resource's content, the names assets used before them, and a script's flow
// tile (portaldomain, pkg/resource, internal/platform/scripttiles).
const (
	tile           = ".thumbnail.png"
	darkTile       = ".thumbnail_dark.png"
	legacyTile     = "thumbnail.png"
	legacyDarkTile = "thumbnail_dark.png"
	scriptTile     = "tile.png"
	scriptDarkTile = "tile-dark.png"
)

// isTile reports whether a key is a tile stored beside its content.
func isTile(key string) bool {
	base := path.Base(key)
	return base == tile || base == darkTile
}

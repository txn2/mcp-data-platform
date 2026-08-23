//go:build integration

package portalstore

// Real-Postgres tests for the thumbnail refresh queue's work list (#1431).
//
// The condition is composed SQL over columns sqlmock does not have: an ILIKE
// against an array of content-type fragments, a per-variant version comparison
// against current_version, and a basename extraction that has to tell
// ".thumbnail.png" from the "thumbnail.png" it is one character away from.
// Asserting the generated statement would pin the spelling, not the answer;
// these run it against the real schema and assert which rows come back.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const pendingOwner = "550e8400-e29b-41d4-a716-446655440333"

// thumbState is one asset's capture state: the keys recorded and the version
// each was taken from.
type thumbState struct {
	light        string
	dark         string
	lightVersion int
	darkVersion  int
}

// seedPendingAsset inserts an asset at the given current version with the given
// capture state. Insert does not carry thumbnail columns -- only a capture
// writes them -- so they are stamped afterwards, which is also the shape an
// upgraded row is in.
func seedPendingAsset(t *testing.T, db *sql.DB, store *postgresAssetStore, id, contentType string, size int64, version int, st thumbState) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.Insert(ctx, portaldomain.Asset{
		ID: id, OwnerID: pendingOwner, OwnerEmail: "u@example.com",
		Name: id, ContentType: contentType, S3Bucket: "portal-assets", S3Key: "k/" + id + "/content",
		SizeBytes: size, Tags: []string{}, CurrentVersion: version,
	}))
	_, err := db.ExecContext(ctx, `
		UPDATE portal_assets
		SET current_version = $1, thumbnail_s3_key = $2, thumbnail_dark_s3_key = $3,
		    thumbnail_version = $4, thumbnail_dark_version = $5
		WHERE id = $6`,
		version, st.light, st.dark, st.lightVersion, st.darkVersion, id)
	require.NoError(t, err)
}

// current is the capture state of an asset whose thumbnails are up to date.
func current(version int) thumbState {
	return thumbState{
		light:        "k/x/.thumbnail.png",
		dark:         "k/x/.thumbnail_dark.png",
		lightVersion: version,
		darkVersion:  version,
	}
}

func pendingIDs(t *testing.T, store *postgresAssetStore) []string {
	t.Helper()
	assets, _, err := store.List(context.Background(), portaldomain.AssetFilter{
		OwnerID: pendingOwner, ThumbnailPending: true,
	})
	require.NoError(t, err)
	return ids(assets)
}

// TestThumbnailPending_RealDB_OffersOnlyWhatNeedsCapturing is the headline
// criterion: an asset a script rewrote is on the list, and one whose capture is
// current is not.
func TestThumbnailPending_RealDB_OffersOnlyWhatNeedsCapturing(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_fresh", "text/html", 100, 4, current(4))
	// The state a version write leaves behind since #1431: the pointers survive,
	// so the asset still shows an image, and the version says it is behind.
	seedPendingAsset(t, db, store, "asset_rewritten", "text/html", 100, 5, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 4,
	})
	seedPendingAsset(t, db, store, "asset_never_captured", "text/html", 100, 1, thumbState{})

	assert.ElementsMatch(t, []string{"asset_rewritten", "asset_never_captured"}, pendingIDs(t, store))
}

// TestThumbnailPending_RealDB_SkipsWhatNoBrowserWillCapture pins the two
// exclusions. Offering either forever is what wedges a queue that hands work
// out in batches.
func TestThumbnailPending_RealDB_SkipsWhatNoBrowserWillCapture(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_pdf", "application/pdf", 100, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_huge", "text/html", maxThumbnailSourceBytes+1, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_at_limit", "text/html", maxThumbnailSourceBytes, 1, thumbState{})

	assert.Equal(t, []string{"asset_at_limit"}, pendingIDs(t, store),
		"the limit is inclusive: an asset exactly at it is still worth rendering")
}

// TestThumbnailPending_RealDB_DarkVariant pins that the dark half of the
// condition is asked only of the types that carry a dark capture, and that it
// is asked at all: a light pass that landed while the dark one threw leaves the
// asset with a current light image and no dark one.
func TestThumbnailPending_RealDB_DarkVariant(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_csv_dark_missing", "text/csv", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})
	seedPendingAsset(t, db, store, "asset_csv_dark_behind", "text/csv", 100, 3, thumbState{
		light: "k/x/.thumbnail.png", dark: "k/x/.thumbnail_dark.png", lightVersion: 3, darkVersion: 2,
	})
	// HTML carries its own colors: one capture serves both modes, so an empty
	// dark key is not a gap and must not put the asset on the list forever.
	seedPendingAsset(t, db, store, "asset_html_no_dark", "text/html", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})

	assert.ElementsMatch(t,
		[]string{"asset_csv_dark_missing", "asset_csv_dark_behind"},
		pendingIDs(t, store))
}

// TestThumbnailPending_RealDB_LegacyFilename covers the reason a capture that
// is current can still be pending: written under the visible filename, the
// object is read by Hive as CSV rows and blocks the asset from being registered
// as a table (#1327). The basename test has to tell it from the hidden name it
// is one character away from.
func TestThumbnailPending_RealDB_LegacyFilename(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_legacy_light", "text/csv", 100, 1, thumbState{
		light: "k/x/thumbnail.png", dark: "k/x/.thumbnail_dark.png", lightVersion: 1, darkVersion: 1,
	})
	seedPendingAsset(t, db, store, "asset_legacy_dark", "text/csv", 100, 1, thumbState{
		light: "k/x/.thumbnail.png", dark: "k/x/thumbnail_dark.png", lightVersion: 1, darkVersion: 1,
	})
	seedPendingAsset(t, db, store, "asset_hidden", "text/csv", 100, 1, current(1))

	assert.ElementsMatch(t,
		[]string{"asset_legacy_light", "asset_legacy_dark"}, pendingIDs(t, store))
}

// TestThumbnailPending_RealDB_JSONFamilies covers the fragment that admits both
// JSON families (#1432). One fragment matches "application/json" and every
// spelling of newline-delimited JSON, and both are drawn on the platform's own
// background, so both are asked for a dark variant as markdown and CSV are.
func TestThumbnailPending_RealDB_JSONFamilies(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_json", "application/json", 100, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_ndjson", "application/x-ndjson", 100, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_vendor_json", "application/vnd.acme.report+json", 100, 1, thumbState{})
	// A light capture that landed while the dark pass threw: the JSON families
	// carry a dark variant, so this is still a gap.
	seedPendingAsset(t, db, store, "asset_json_dark_missing", "application/json", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})
	seedPendingAsset(t, db, store, "asset_json_current", "application/json", 100, 1, current(1))

	assert.ElementsMatch(t,
		[]string{"asset_json", "asset_ndjson", "asset_vendor_json", "asset_json_dark_missing"},
		pendingIDs(t, store))
}

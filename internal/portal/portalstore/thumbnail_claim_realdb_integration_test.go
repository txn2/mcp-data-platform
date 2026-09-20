//go:build integration

package portalstore

// Real-Postgres tests for the renderer's claim: which assets the platform owes
// a tile, and that two replicas never take the same one (#1431, #1787).
//
// The condition is composed SQL over columns sqlmock does not have: an ILIKE
// against an array of content-type fragments, a per-variant version comparison
// against current_version, a basename extraction that has to tell
// ".thumbnail.png" from the "thumbnail.png" it is one character away from, and
// a lease taken under FOR UPDATE SKIP LOCKED. Asserting the generated statement
// would pin the spelling, not the answer; these run it against the real schema
// and assert which rows come back.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
)

const pendingOwner = "550e8400-e29b-41d4-a716-446655440333"

// testRenderer is the renderer generation these tests claim for.
const testRenderer = 1

// thumbState is one asset's capture state: the keys recorded, the version each
// was taken from, and whether an older renderer drew them. A tile is from the
// current renderer unless oldRenderer says otherwise, so each criterion below
// is decided by the condition it is about and not by the generation.
type thumbState struct {
	light        string
	dark         string
	lightVersion int
	darkVersion  int
	oldRenderer  bool
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
		    thumbnail_version = $4, thumbnail_dark_version = $5, thumbnail_renderer = $6
		WHERE id = $7`,
		version, st.light, st.dark, st.lightVersion, st.darkVersion, rendererOf(st), id)
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

func rendererOf(st thumbState) int {
	if st.oldRenderer {
		return 0
	}
	return testRenderer
}

// pendingIDs claims what the renderer is owed and then releases the leases, so
// a criterion can ask more than once.
func pendingIDs(t *testing.T, store *postgresAssetStore) []string {
	t.Helper()
	assets, err := store.ClaimThumbnailWork(context.Background(), testRenderer, time.Minute, 100)
	require.NoError(t, err)
	_, err = store.db.ExecContext(context.Background(), `UPDATE portal_assets SET thumbnail_claimed_until = NULL`)
	require.NoError(t, err)
	return ids(assets)
}

// TestThumbnailPending_RealDB_OffersOnlyWhatNeedsCapturing is the headline
// criterion: an asset a script rewrote is on the list, and one whose capture is
// current is not.
func TestThumbnailClaim_RealDB_OffersOnlyWhatNeedsCapturing(t *testing.T) {
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

// TestThumbnailClaim_RealDB_SkipsWhatNoBrowserWillCapture pins the two
// exclusions against a real PostgreSQL. Offering either forever is what wedges
// a queue that hands work out in batches.
//
// The size half of it is a CASE over the content type since #1794, because a
// PDF is held to a bound of its own: it is drawn from page one alone, and one
// letter page scanned at 300dpi is already past the bound every other family
// shares. This is where that expression meets a database.
func TestThumbnailClaim_RealDB_SkipsWhatNoBrowserWillCapture(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_zip", "application/zip", 100, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_huge", "text/html", thumbtypes.DefaultSourceLimit+1, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_at_limit", "text/html", thumbtypes.DefaultSourceLimit, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_pdf_mid", "application/pdf", thumbtypes.DefaultSourceLimit+1, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_pdf_at_limit", "application/pdf", thumbtypes.LargeSourceLimit, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_pdf_huge", "application/pdf", thumbtypes.LargeSourceLimit+1, 1, thumbState{})

	assert.ElementsMatch(t,
		[]string{"asset_at_limit", "asset_pdf_mid", "asset_pdf_at_limit"},
		pendingIDs(t, store),
		"both bounds are inclusive, and the raised one reaches the PDF family alone")
}

// TestThumbnailPending_RealDB_DarkVariant pins that the dark half of the
// condition is asked only of the types that carry a dark capture, and that it
// is asked at all: a light pass that landed while the dark one threw leaves the
// asset with a current light image and no dark one.
func TestThumbnailClaim_RealDB_DarkVariant(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_csv_dark_missing", "text/csv", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})
	seedPendingAsset(t, db, store, "asset_csv_dark_behind", "text/csv", 100, 3, thumbState{
		light: "k/x/.thumbnail.png", dark: "k/x/.thumbnail_dark.png", lightVersion: 3, darkVersion: 2,
	})
	// HTML is drawn in each scheme it styles itself for (#1789), so a light
	// tile alone leaves it owed the dark one.
	seedPendingAsset(t, db, store, "asset_html_dark_missing", "text/html", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})
	// A raster image is drawn as stored: one capture serves both modes, so an
	// empty dark key is not a gap and must not put the asset on the list forever.
	seedPendingAsset(t, db, store, "asset_png_no_dark", "image/png", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})
	// An SVG's type contains "xml", which IS themeable; it is still an SVG and
	// is drawn as stored. Reading it as XML owed it a dark tile nothing ever
	// draws, which under a server renderer is a document redrawn forever.
	seedPendingAsset(t, db, store, "asset_svg_no_dark", "image/svg+xml", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})

	assert.ElementsMatch(t,
		[]string{"asset_csv_dark_missing", "asset_csv_dark_behind", "asset_html_dark_missing"},
		pendingIDs(t, store))
}

// TestThumbnailPending_RealDB_LegacyFilename covers the reason a capture that
// is current can still be pending: written under the visible filename, the
// object is read by Hive as CSV rows and blocks the asset from being registered
// as a table (#1327). The basename test has to tell it from the hidden name it
// is one character away from.
func TestThumbnailClaim_RealDB_LegacyFilename(t *testing.T) {
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
func TestThumbnailClaim_RealDB_JSONFamilies(t *testing.T) {
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

// TestThumbnailPending_RealDB_FamiliesUnifiedWithResources covers the families
// this store had lost against the other three copies of the rule (#1568).
//
// A raster image asset was never offered although the capturer downscales one,
// and a plain-text asset was never offered although the capturer has prose CSS
// that draws it. Both were reachable only because the rule was written out four
// times; there is one Go definition now (internal/thumbtypes) and one browser
// definition, held together by a test.
func TestThumbnailClaim_RealDB_FamiliesUnifiedWithResources(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_png", "image/png", 100, 1, thumbState{})
	seedPendingAsset(t, db, store, "asset_text", "text/plain; charset=utf-8", 100, 1, thumbState{})
	// A raster image carries its own colors: one downscale serves both modes, so
	// an empty dark key is not a gap and must not offer it forever.
	seedPendingAsset(t, db, store, "asset_png_light_only", "image/jpeg", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})
	// Plain text is drawn on a forced background, so it is asked for both.
	seedPendingAsset(t, db, store, "asset_text_dark_missing", "text/plain", 100, 2, thumbState{
		light: "k/x/.thumbnail.png", lightVersion: 2,
	})
	seedPendingAsset(t, db, store, "asset_text_current", "text/plain", 100, 1, current(1))

	assert.ElementsMatch(t,
		[]string{"asset_png", "asset_text", "asset_text_dark_missing"},
		pendingIDs(t, store))
}

// TestThumbnailClaim_RealDB_AnOlderRendererIsOwedAgain pins the upgrade path:
// a tile the old capturer drew is current by version and still owed, so every
// existing tile is redrawn once, while its old image keeps serving.
func TestThumbnailClaim_RealDB_AnOlderRendererIsOwedAgain(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	old := current(3)
	old.oldRenderer = true
	seedPendingAsset(t, db, store, "asset_old_renderer", "text/html", 100, 3, old)
	seedPendingAsset(t, db, store, "asset_new_renderer", "text/html", 100, 3, current(3))

	assert.Equal(t, []string{"asset_old_renderer"}, pendingIDs(t, store))
}

// TestThumbnailClaim_RealDB_AFailureHoldsUntilTheDocumentChanges pins that a
// document the renderer could not draw is not retried at the version it
// failed on, and is again once the content moves on.
func TestThumbnailClaim_RealDB_AFailureHoldsUntilTheDocumentChanges(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}

	seedPendingAsset(t, db, store, "asset_failed_here", "text/html", 100, 2, thumbState{})
	seedPendingAsset(t, db, store, "asset_failed_before", "text/html", 100, 3, thumbState{})
	_, err := db.ExecContext(context.Background(), `
		UPDATE portal_assets SET thumbnail_failure = 'hung', thumbnail_failed_version = current_version WHERE id = 'asset_failed_here';
		UPDATE portal_assets SET thumbnail_failure = 'hung', thumbnail_failed_version = 2 WHERE id = 'asset_failed_before';`)
	require.NoError(t, err)

	assert.Equal(t, []string{"asset_failed_before"}, pendingIDs(t, store))
}

// TestThumbnailClaim_RealDB_ALeaseIsExclusiveUntilItLapses is what keeps two
// replicas from drawing the same document: a claimed row is not claimed again
// while its lease holds, and is once it lapses.
func TestThumbnailClaim_RealDB_ALeaseIsExclusiveUntilItLapses(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()
	seedPendingAsset(t, db, store, "asset_leased", "text/html", 100, 1, thumbState{})

	first, err := store.ClaimThumbnailWork(ctx, testRenderer, time.Minute, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"asset_leased"}, ids(first))

	second, err := store.ClaimThumbnailWork(ctx, testRenderer, time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, second, "a leased row was claimed a second time")

	_, err = db.ExecContext(ctx, `UPDATE portal_assets SET thumbnail_claimed_until = now() - interval '1 second'`)
	require.NoError(t, err)
	third, err := store.ClaimThumbnailWork(ctx, testRenderer, time.Minute, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"asset_leased"}, ids(third), "a lapsed lease must be claimable again")
}

// TestThumbnailClaim_RealDB_RecordingAResultEndsTheLease pins that a drawn
// tile is recorded without re-dating the asset, and that the write releases
// the lease so the asset's next change is picked up at once.
func TestThumbnailClaim_RealDB_RecordingAResultEndsTheLease(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()
	seedPendingAsset(t, db, store, "asset_drawn", "text/html", 100, 1, thumbState{})
	claimed, err := store.ClaimThumbnailWork(ctx, testRenderer, time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	before := claimed[0].UpdatedAt

	// HTML is drawn in both schemes (#1789), so a drawn tile is both variants.
	key, darkKey, version, renderer := "k/asset_drawn/.thumbnail.png", "k/asset_drawn/.thumbnail_dark.png", 1, testRenderer
	require.NoError(t, store.Update(ctx, "asset_drawn", portaldomain.AssetUpdate{
		ThumbnailS3Key: &key, ThumbnailVersion: &version, ThumbnailRenderer: &renderer,
		ThumbnailDarkS3Key: &darkKey, ThumbnailDarkVersion: &version,
		ReleaseThumbnailClaim: true,
	}))

	var leased sql.NullTime
	var updated time.Time
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT thumbnail_claimed_until, updated_at FROM portal_assets WHERE id = 'asset_drawn'`).Scan(&leased, &updated))
	assert.False(t, leased.Valid, "the lease survived the recorded result")
	assert.True(t, updated.Equal(before), "recording a tile re-dated the asset")
	assert.Empty(t, pendingIDs(t, store), "a drawn tile is still owed")
}

// TestThumbnailClaim_RealDB_ADeletedAssetIsNeverClaimed pins that the renderer
// never draws what the library no longer holds.
func TestThumbnailClaim_RealDB_ADeletedAssetIsNeverClaimed(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	seedPendingAsset(t, db, store, "asset_deleted", "text/html", 100, 1, thumbState{})
	_, err := db.ExecContext(context.Background(), `UPDATE portal_assets SET deleted_at = now() WHERE id = 'asset_deleted'`)
	require.NoError(t, err)

	assert.Empty(t, pendingIDs(t, store))
}

// TestCollectionThumbnailClaim_RealDB_AMosaicFollowsItsMembers pins the
// collection side of the claim (#1787): a mosaic is owed until it holds the
// tiles of its first members that have one, is owed again when one of them is
// redrawn, and is cleared once no member has a tile -- where the capturer drew
// a collection's tile once and kept it however wrong it became.
func TestCollectionThumbnailClaim_RealDB_AMosaicFollowsItsMembers(t *testing.T) {
	db := testdb.New(t)
	assets := &postgresAssetStore{db: db}
	colls, ok := NewPostgresCollectionStore(db, nil).(*postgresCollectionStore)
	require.True(t, ok)
	ctx := context.Background()

	seedPendingAsset(t, db, assets, "a_one", "text/html", 100, 1, current(1))
	seedPendingAsset(t, db, assets, "a_two", "text/html", 100, 2, current(2))
	seedPendingAsset(t, db, assets, "a_untiled", "text/html", 100, 1, thumbState{})
	for _, id := range []string{"c_mosaic", "c_empty"} {
		require.NoError(t, colls.Insert(ctx, portaldomain.Collection{
			ID: id, OwnerID: pendingOwner, OwnerEmail: "u@example.com", Name: id,
		}))
	}
	require.NoError(t, colls.SetSections(ctx, "c_mosaic", []portaldomain.CollectionSection{{
		ID: "s1", Items: []portaldomain.CollectionItem{
			{ID: "i1", AssetID: "a_untiled"}, {ID: "i2", AssetID: "a_one"}, {ID: "i3", AssetID: "a_two"},
		},
	}}))

	claim := func() map[string]string {
		work, err := colls.ClaimCollectionThumbnailWork(ctx, time.Minute, 100)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `UPDATE portal_collections SET thumbnail_claimed_until = NULL`)
		require.NoError(t, err)
		out := map[string]string{}
		for _, w := range work {
			out[w.ID] = w.Source
		}
		return out
	}
	updatedAt := func() time.Time {
		var at time.Time
		require.NoError(t, db.QueryRowContext(ctx, `SELECT updated_at FROM portal_collections WHERE id = 'c_mosaic'`).Scan(&at))
		return at
	}

	got := claim()
	assert.Equal(t, map[string]string{"c_mosaic": "a_one:1:1:1,a_two:2:2:1"}, got,
		"a member with no tile is skipped, and a collection with nothing to draw and no mosaic is not owed")

	before := updatedAt()
	require.NoError(t, colls.RecordCollectionThumbnail(ctx, "c_mosaic", "portal/collections/c_mosaic/thumbnail.png", got["c_mosaic"]))
	assert.True(t, updatedAt().Equal(before), "recording a mosaic re-dated the collection")
	assert.Empty(t, claim(), "a mosaic composed from its current source is not owed")

	_, err := db.ExecContext(ctx, `UPDATE portal_assets SET current_version = 3, thumbnail_version = 3 WHERE id = 'a_two'`)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"c_mosaic": "a_one:1:1:1,a_two:3:2:1"}, claim(), "a redrawn member makes the mosaic owed again")

	// The collection has a dark mosaic composed from its members' dark tiles,
	// so a member's new dark tile alone makes it owed too (#1789).
	require.NoError(t, colls.RecordCollectionThumbnail(ctx, "c_mosaic", "portal/collections/c_mosaic/thumbnail.png", "a_one:1:1:1,a_two:3:2:1"))
	_, err = db.ExecContext(ctx, `UPDATE portal_assets SET thumbnail_dark_version = 3 WHERE id = 'a_two'`)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"c_mosaic": "a_one:1:1:1,a_two:3:3:1"}, claim(), "a member's redrawn dark tile makes the mosaic owed again")

	require.NoError(t, colls.SetSections(ctx, "c_mosaic", nil))
	assert.Equal(t, map[string]string{"c_mosaic": ""}, claim(), "a mosaic whose members are gone is owed, with nothing to draw, so it is cleared")

	assert.Error(t, colls.RecordCollectionThumbnail(ctx, "c_missing", "", ""))
}

//go:build integration

package portalversions

// Real-Postgres tests for the asset version retention cap (#1421). The prune is
// a DELETE ... USING that joins portal_assets to exclude the key the asset row
// still points at, and its cutoff is arithmetic over the version numbers the
// same transaction assigns. sqlmock rubber-stamps both: only the real planner
// rejects an ambiguous RETURNING list, and only real rows show which version
// actually went.

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// recordingDeleter stands in for the portal's S3 client, recording every key the
// prune asked it to remove.
type recordingDeleter struct {
	mu      sync.Mutex
	deleted []string
	err     error
}

func (d *recordingDeleter) DeleteObject(_ context.Context, bucket, key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, bucket+"/"+key)
	return d.err
}

func (d *recordingDeleter) keys() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.deleted...)
}

// seedAsset inserts an asset to hang versions off and returns its id. The asset
// starts at current_version 1 with the flat content key an asset created before
// it had a second version carries, which is the key the prune's guard protects.
func seedAsset(t *testing.T, db *sql.DB, maxVersions *int) string {
	t.Helper()
	id := "asset_" + uuid.New().String()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO portal_assets
		(id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key,
		 size_bytes, tags, provenance, session_id, current_version, max_versions)
		VALUES ($1, $2, 'u@example.com', 'Dashboard', '', 'text/html', 'portal-assets', $3,
		        10, '[]', '{}', '', 1, $4)`,
		id, uuid.New().String(), flatKey(id), maxVersions)
	require.NoError(t, err)

	// Version 1 shares the asset's key, exactly as the pre-versioning rows do.
	_, err = db.ExecContext(context.Background(), `
		INSERT INTO portal_asset_versions
		(id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes, created_by, change_summary)
		VALUES ($1, $2, 1, $3, 'portal-assets', 'text/html', 10, 'u@example.com', 'initial')`,
		uuid.New().String(), id, flatKey(id))
	require.NoError(t, err)
	return id
}

func flatKey(assetID string) string {
	return "artifacts/owner/" + assetID + "/content.html"
}

func versionKey(assetID string, n int) string {
	return fmt.Sprintf("artifacts/owner/%s/v%d/content.html", assetID, n)
}

// writeVersion records one more version of an asset through the store, the way
// every real write path does.
func writeVersion(t *testing.T, store portaldomain.VersionStore, assetID string, n int) int {
	t.Helper()
	assigned, err := store.CreateVersion(context.Background(), portaldomain.AssetVersion{
		ID: uuid.New().String(), AssetID: assetID, S3Key: versionKey(assetID, n),
		S3Bucket: "portal-assets", ContentType: "text/html", SizeBytes: 20,
		CreatedBy: "u@example.com", ChangeSummary: fmt.Sprintf("write %d", n),
	})
	require.NoError(t, err)
	return assigned
}

// liveVersions returns the version numbers still recorded for an asset, oldest
// first.
func liveVersions(t *testing.T, db *sql.DB, assetID string) []int {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT version FROM portal_asset_versions WHERE asset_id = $1 ORDER BY version`, assetID)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck // test cleanup
	var out []int
	for rows.Next() {
		var v int
		require.NoError(t, rows.Scan(&v))
		out = append(out, v)
	}
	require.NoError(t, rows.Err())
	return out
}

func intPtr(n int) *int { return &n }

// TestAssetVersionPrune_RealDB_ConvergesOnTheCap is the headline acceptance
// criterion: an asset written over and over settles at the cap, and the version
// that goes is the oldest.
func TestAssetVersionPrune_RealDB_ConvergesOnTheCap(t *testing.T) {
	db := testdb.New(t)
	objects := &recordingDeleter{}
	store := NewPostgres(db, objects, intPtr(3))
	id := seedAsset(t, db, nil)

	for n := 2; n <= 8; n++ {
		assert.Equal(t, n, writeVersion(t, store, id, n))
	}

	assert.Equal(t, []int{6, 7, 8}, liveVersions(t, db, id),
		"the cap keeps the newest 3 and the oldest are the ones that go")
	assert.Contains(t, objects.keys(), "portal-assets/"+versionKey(id, 2),
		"a pruned version's content object goes with its row")
	assert.Contains(t, objects.keys(), "portal-assets/artifacts/owner/"+id+"/v2/.thumbnail.png",
		"and so do the thumbnails that sat beside it")
	assert.Contains(t, objects.keys(), "portal-assets/artifacts/owner/"+id+"/v2/.thumbnail_dark.png")
}

// TestAssetVersionPrune_RealDB_NeverDeletesTheLiveKey pins the guard. Version 1
// carries the flat key the asset row itself names until a second version moves
// the head; a prune must not take the live content with the row.
func TestAssetVersionPrune_RealDB_NeverDeletesTheLiveKey(t *testing.T) {
	db := testdb.New(t)
	objects := &recordingDeleter{}
	// A cap of 1 asks for the most aggressive prune there is.
	store := NewPostgres(db, objects, intPtr(1))
	id := seedAsset(t, db, nil)

	// The state the guard exists for: the asset row still names version 1's flat
	// key while version 1 sits at or below the cutoff. Probing the prune
	// directly puts the asset in it -- a real write moves the head first, which
	// is why the guard is otherwise unreachable and untested.
	pruned, err := pruneUnderHead(context.Background(), db, id, 2, 1)
	require.NoError(t, err)
	assert.Empty(t, pruned, "the key the asset row points at is never deleted")
	assert.Equal(t, []int{1}, liveVersions(t, db, id))

	// Now write a real second version. The head moves to v2, so v1's flat key is
	// no longer live and the cap of 1 may take it.
	writeVersion(t, store, id, 2)
	assert.Equal(t, []int{2}, liveVersions(t, db, id))
	// Both thumbnail spellings are attempted: a version captured before
	// thumbnails took hidden names still has PNGs under the old ones (#1327).
	assert.Equal(t, []string{
		"portal-assets/" + flatKey(id),
		"portal-assets/artifacts/owner/" + id + "/.thumbnail.png",
		"portal-assets/artifacts/owner/" + id + "/.thumbnail_dark.png",
		"portal-assets/artifacts/owner/" + id + "/thumbnail.png",
		"portal-assets/artifacts/owner/" + id + "/thumbnail_dark.png",
	}, objects.keys())

	// The surviving version is still readable and is the only entry.
	latest, err := store.GetLatest(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 2, latest.Version)
	assert.Equal(t, versionKey(id, 2), latest.S3Key)
}

// pruneUnderHead runs the prune outside CreateVersion so the guard can be
// exercised while the asset head still points at the row being considered --
// the state a normal write leaves behind only for version 1.
func pruneUnderHead(ctx context.Context, db *sql.DB, assetID string, latestVersion, keep int) ([]portaldomain.AssetVersion, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only probe
	pruned, err := prune(ctx, tx, assetID, latestVersion, keep)
	if err != nil {
		return nil, err
	}
	return pruned.removed, tx.Commit()
}

// TestAssetVersionPrune_RealDB_PerAssetOverride covers the three states of the
// column: unlimited, a cap of 1, and inheriting the deployment default.
func TestAssetVersionPrune_RealDB_PerAssetOverride(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	t.Run("0 keeps every version", func(t *testing.T) {
		objects := &recordingDeleter{}
		store := NewPostgres(db, objects, intPtr(2))
		id := seedAsset(t, db, intPtr(portaldomain.MaxVersionsUnlimited))
		for n := 2; n <= 5; n++ {
			writeVersion(t, store, id, n)
		}
		assert.Equal(t, []int{1, 2, 3, 4, 5}, liveVersions(t, db, id))
		assert.Empty(t, objects.keys(), "unlimited deletes no objects either")
	})

	t.Run("1 keeps only the current version", func(t *testing.T) {
		store := NewPostgres(db, &recordingDeleter{}, intPtr(100))
		id := seedAsset(t, db, intPtr(1))
		for n := 2; n <= 4; n++ {
			writeVersion(t, store, id, n)
		}
		assert.Equal(t, []int{4}, liveVersions(t, db, id))
		v, err := store.GetByVersion(ctx, id, 4)
		require.NoError(t, err)
		assert.Equal(t, versionKey(id, 4), v.S3Key, "the survivor is still readable")
	})

	t.Run("null inherits the deployment default", func(t *testing.T) {
		store := NewPostgres(db, &recordingDeleter{}, intPtr(2))
		id := seedAsset(t, db, nil)
		for n := 2; n <= 5; n++ {
			writeVersion(t, store, id, n)
		}
		assert.Equal(t, []int{4, 5}, liveVersions(t, db, id))
	})
}

// TestAssetVersionPrune_RealDB_ExistingHistoryTrimsAtTheNextWrite is the
// migration's stated behavior: nothing is retro-pruned, and an asset already
// over the cap converges when it is next written.
func TestAssetVersionPrune_RealDB_ExistingHistoryTrimsAtTheNextWrite(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, &recordingDeleter{}, intPtr(3))
	id := seedAsset(t, db, nil)

	// Ten versions accumulated before the cap existed, written straight to the
	// table the way the pre-#1421 store did.
	for n := 2; n <= 10; n++ {
		_, err := db.ExecContext(context.Background(), `
			INSERT INTO portal_asset_versions
			(id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes, created_by, change_summary)
			VALUES ($1, $2, $3, $4, 'portal-assets', 'text/html', 10, 'u@example.com', 'legacy')`,
			uuid.New().String(), id, n, versionKey(id, n))
		require.NoError(t, err)
	}
	_, err := db.ExecContext(context.Background(),
		`UPDATE portal_assets SET current_version = 10, s3_key = $2 WHERE id = $1`, id, versionKey(id, 10))
	require.NoError(t, err)
	require.Len(t, liveVersions(t, db, id), 10, "the migration itself trims nothing")

	writeVersion(t, store, id, 11)
	assert.Equal(t, []int{9, 10, 11}, liveVersions(t, db, id))
}

// TestAssetVersionPrune_RealDB_SharedKeyAcrossVersions is the managed-script
// shape against real rows: scriptexec keys an output object by RUN, so a run
// that exports a document and then publishes data into the same asset leaves two
// version rows at one key, and every version of such an asset shares one
// directory. Pruning the older row must take neither the object the newer row
// reads nor the thumbnail the current version serves.
func TestAssetVersionPrune_RealDB_SharedKeyAcrossVersions(t *testing.T) {
	db := testdb.New(t)
	objects := &recordingDeleter{}
	store := NewPostgres(db, objects, intPtr(2))
	id := seedAsset(t, db, nil)

	dir := "artifacts/scripts/s1/" + id + "/"
	runKey := dir + "run-7.html"
	// Versions 2 and 3 are the export and the data publish of one run: one key,
	// two rows. Version 4 is the next run.
	for _, k := range []string{runKey, runKey, dir + "run-8.html"} {
		_, err := db.ExecContext(context.Background(), `
			INSERT INTO portal_asset_versions
			(id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes, created_by, change_summary)
			VALUES ($1, $2, (SELECT COALESCE(MAX(version), 0) + 1 FROM portal_asset_versions WHERE asset_id = $2),
			        $3, 'portal-assets', 'text/html', 10, 'u@example.com', 'run output')`,
			uuid.New().String(), id, k)
		require.NoError(t, err)
	}
	_, err := db.ExecContext(context.Background(),
		`UPDATE portal_assets SET current_version = 4, s3_key = $2 WHERE id = $1`, id, dir+"run-8.html")
	require.NoError(t, err)

	// A cap of 2 at version 5 prunes versions 1, 2 and 3 -- and 2 and 3 are the
	// pair sharing runKey.
	writeVersion(t, store, id, 5)
	assert.Equal(t, []int{4, 5}, liveVersions(t, db, id))

	deleted := objects.keys()
	assert.NotContains(t, deleted, "portal-assets/"+dir+".thumbnail.png",
		"the current version's thumbnail lives in the same directory and must survive")
	assert.NotContains(t, deleted, "portal-assets/"+dir+".thumbnail_dark.png")
	assert.Contains(t, deleted, "portal-assets/"+runKey,
		"the shared key goes once both rows naming it are gone")
	assert.NotContains(t, deleted, "portal-assets/"+dir+"run-8.html",
		"version 4 survives and still names its object")
}

// TestAssetVersionPrune_RealDB_DeleteFailureDoesNotFailTheWrite pins the
// best-effort contract: the version the caller asked for has already committed,
// and an object that could not be removed must not turn into a failed save.
func TestAssetVersionPrune_RealDB_DeleteFailureDoesNotFailTheWrite(t *testing.T) {
	db := testdb.New(t)
	objects := &recordingDeleter{err: fmt.Errorf("storage unavailable")}
	store := NewPostgres(db, objects, intPtr(1))
	id := seedAsset(t, db, nil)

	assigned := writeVersion(t, store, id, 2)
	assert.Equal(t, 2, assigned)
	assert.Equal(t, []int{2}, liveVersions(t, db, id), "the row still went")
	assert.NotEmpty(t, objects.keys(), "the delete was attempted")
}

// TestAssetVersionPrune_RealDB_NoObjectClientStillTrimsTheTable covers
// database-only mode, where there are no objects to lose.
func TestAssetVersionPrune_RealDB_NoObjectClientStillTrimsTheTable(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, nil, intPtr(2))
	id := seedAsset(t, db, nil)

	for n := 2; n <= 5; n++ {
		writeVersion(t, store, id, n)
	}
	assert.Equal(t, []int{4, 5}, liveVersions(t, db, id))
}

// TestAssetVersionPrune_RealDB_NegativeOverrideIsRefusedByTheColumn pins the
// CHECK the migration adds: a negative cap cannot reach the column, whatever
// bypassed the entry points' validation.
func TestAssetVersionPrune_RealDB_NegativeOverrideIsRefusedByTheColumn(t *testing.T) {
	db := testdb.New(t)
	id := seedAsset(t, db, nil)

	_, err := db.ExecContext(context.Background(),
		`UPDATE portal_assets SET max_versions = -1 WHERE id = $1`, id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "portal_assets_max_versions_nonneg")
}

// TestAssetVersionWrite_RealDB_KeepsTheThumbnailPointers is the change #1431
// turns on: a version write used to blank both pointers, which sent an asset a
// managed script rewrites hourly back to the placeholder icon on every run. The
// row now keeps the capture it has -- one version behind is worth more than no
// image -- and the version columns are what say it has not caught up.
func TestAssetVersionWrite_RealDB_KeepsTheThumbnailPointers(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgres(db, &recordingDeleter{}, nil)
	id := seedAsset(t, db, nil)

	thumb := "artifacts/owner/" + id + "/.thumbnail.png"
	_, err := db.ExecContext(context.Background(), `
		UPDATE portal_assets
		SET thumbnail_s3_key = $1, thumbnail_version = 1
		WHERE id = $2`, thumb, id)
	require.NoError(t, err)

	writeVersion(t, store, id, 2)

	var key string
	var capturedAt, currentVersion int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT thumbnail_s3_key, thumbnail_version, current_version FROM portal_assets WHERE id = $1`, id).
		Scan(&key, &capturedAt, &currentVersion))
	assert.Equal(t, thumb, key, "the asset keeps serving the image it has")
	assert.Equal(t, 1, capturedAt)
	assert.Equal(t, 2, currentVersion,
		"and the row says the capture is a version behind, which is what the queue looks for")
}

// TestAssetVersionPrune_RealDB_KeepsTheThumbnailTheAssetPointsAt pins the other
// half. Now that a capture outlives the version it was taken from, the image an
// asset is serving can sit in a directory the prune is deleting; the retained
// set has to cover the key the asset row still names, not just what a surviving
// version's key derives.
func TestAssetVersionPrune_RealDB_KeepsTheThumbnailTheAssetPointsAt(t *testing.T) {
	db := testdb.New(t)
	objects := &recordingDeleter{}
	store := NewPostgres(db, objects, intPtr(1))
	id := seedAsset(t, db, nil)

	// A capture taken beside version 2's content, which version 3 then prunes.
	writeVersion(t, store, id, 2)
	thumb := "artifacts/owner/" + id + "/v2/.thumbnail.png"
	_, err := db.ExecContext(context.Background(), `
		UPDATE portal_assets SET thumbnail_s3_key = $1, thumbnail_version = 2 WHERE id = $2`, thumb, id)
	require.NoError(t, err)

	writeVersion(t, store, id, 3)

	assert.Equal(t, []int{3}, liveVersions(t, db, id), "the cap of 1 took version 2's row")
	assert.Contains(t, objects.keys(), "portal-assets/"+versionKey(id, 2),
		"its content object goes with it")
	assert.NotContains(t, objects.keys(), "portal-assets/"+thumb,
		"but not the image the asset row still points at")
}

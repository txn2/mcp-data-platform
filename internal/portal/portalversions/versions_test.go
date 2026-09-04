package portalversions

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// expectSurvivingKeys sets up the in-transaction read of the keys the versions
// that outlived the prune still own. It runs only when the prune removed a row.
func expectSurvivingKeys(mock sqlmock.Sqlmock, assetID string, keys ...string) {
	rows := sqlmock.NewRows([]string{"s3_key"})
	for _, k := range keys {
		rows.AddRow(k)
	}
	mock.ExpectQuery("SELECT DISTINCT s3_key FROM portal_asset_versions").
		WithArgs(assetID).
		WillReturnRows(rows)
	expectStoredThumbnails(mock, assetID, "", "")
}

// expectStoredThumbnails answers the read of the thumbnail keys the asset row
// points at, which the prune consults so it cannot delete the image the asset
// is currently serving. Every caller of expectSurvivingKeys gets the "no
// capture recorded" answer; a test about a stored pointer calls this itself
// after its own versions query.
func expectStoredThumbnails(mock sqlmock.Sqlmock, assetID, light, dark string) {
	mock.ExpectQuery("SELECT thumbnail_s3_key, thumbnail_dark_s3_key FROM portal_assets").
		WithArgs(assetID).
		WillReturnRows(sqlmock.NewRows([]string{"thumbnail_s3_key", "thumbnail_dark_s3_key"}).
			AddRow(light, dark))
}

// prunedRows is an empty result in the shape the prune's RETURNING clause
// produces.
func prunedRows(rows ...[]driver.Value) *sqlmock.Rows {
	r := sqlmock.NewRows([]string{"version", "s3_bucket", "s3_key"})
	for _, row := range rows {
		r.AddRow(row...)
	}
	return r
}

func TestPostgresVersionStoreCreateVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	version := portaldomain.AssetVersion{
		ID:            "v1",
		AssetID:       "abc123",
		Version:       2,
		S3Key:         "user1/abc123/v2/content.html",
		S3Bucket:      "portal",
		ContentType:   "text/html",
		SizeBytes:     2048,
		CreatedBy:     "user1",
		ChangeSummary: "Updated content",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_version, max_versions FROM portal_assets").
		WithArgs("abc123").
		WillReturnRows(sqlmock.NewRows([]string{"current_version", "max_versions"}).AddRow(1, nil))
	mock.ExpectExec("INSERT INTO portal_asset_versions").
		WithArgs(version.ID, version.AssetID, version.Version, version.S3Key, version.S3Bucket,
			version.ContentType, version.SizeBytes, version.CreatedBy, version.ChangeSummary).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets").
		WithArgs(version.Version, version.S3Key, version.ContentType, version.SizeBytes, version.AssetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// No override and no configured default, so the cap is
	// portaldomain.DefaultMaxVersions -- which version 2 is nowhere near, so no
	// prune statement is issued at all. Any DELETE here would fail the mock.
	mock.ExpectCommit()

	assignedVersion, err := store.CreateVersion(context.Background(), version)
	assert.NoError(t, err)
	assert.Equal(t, 2, assignedVersion)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreCreateVersionInsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_version, max_versions FROM portal_assets").
		WithArgs("").
		WillReturnRows(sqlmock.NewRows([]string{"current_version", "max_versions"}).AddRow(0, nil))
	mock.ExpectExec("INSERT INTO portal_asset_versions").
		WillReturnError(errors.New("db error"))
	mock.ExpectRollback()

	_, err = store.CreateVersion(context.Background(), portaldomain.AssetVersion{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreCreateVersionUpdateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_version, max_versions FROM portal_assets").
		WithArgs("").
		WillReturnRows(sqlmock.NewRows([]string{"current_version", "max_versions"}).AddRow(0, nil))
	mock.ExpectExec("INSERT INTO portal_asset_versions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets").
		WillReturnError(errors.New("db error"))
	mock.ExpectRollback()

	_, err = store.CreateVersion(context.Background(), portaldomain.AssetVersion{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating asset version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreCreateVersionBeginError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectBegin().WillReturnError(errors.New("db error"))

	_, err = store.CreateVersion(context.Background(), portaldomain.AssetVersion{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "beginning transaction")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreListByAsset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("abc123").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	dataRows := sqlmock.NewRows([]string{
		"id", "asset_id", "version", "s3_key", "s3_bucket", "content_type",
		"size_bytes", "created_by", "change_summary", "created_at",
	}).
		AddRow("v2", "abc123", 2, "key2", "portal", "text/html", int64(2048), "user1", "v2 changes", now).
		AddRow("v1", "abc123", 1, "key1", "portal", "text/html", int64(1024), "user1", "initial", now)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123", 10, 0).
		WillReturnRows(dataRows)

	versions, total, err := store.ListByAsset(context.Background(), "abc123", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, versions, 2)
	assert.Equal(t, 2, versions[0].Version)
	assert.Equal(t, 1, versions[1].Version)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreListByAssetCountError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("abc123").
		WillReturnError(errors.New("db error"))

	_, _, err = store.ListByAsset(context.Background(), "abc123", 10, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting versions")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreListByAssetQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("abc123").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123", 10, 0).
		WillReturnError(errors.New("db error"))

	_, _, err = store.ListByAsset(context.Background(), "abc123", 10, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying versions")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreListByAssetDefaults(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("abc123").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123", portaldomain.DefaultLimit, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "asset_id", "version", "s3_key", "s3_bucket", "content_type",
			"size_bytes", "created_by", "change_summary", "created_at",
		}))

	versions, total, err := store.ListByAsset(context.Background(), "abc123", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, versions)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreGetByVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "asset_id", "version", "s3_key", "s3_bucket", "content_type",
		"size_bytes", "created_by", "change_summary", "created_at",
	}).AddRow("v1", "abc123", 1, "key1", "portal", "text/html", int64(1024), "user1", "initial", now)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123", 1).
		WillReturnRows(rows)

	v, err := store.GetByVersion(context.Background(), "abc123", 1)
	require.NoError(t, err)
	assert.Equal(t, "v1", v.ID)
	assert.Equal(t, 1, v.Version)
	assert.Equal(t, "abc123", v.AssetID)
	assert.Equal(t, "initial", v.ChangeSummary)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreGetByVersionNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123", 99).
		WillReturnError(errors.New("sql: no rows in result set"))

	_, err = store.GetByVersion(context.Background(), "abc123", 99)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreGetLatest(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "asset_id", "version", "s3_key", "s3_bucket", "content_type",
		"size_bytes", "created_by", "change_summary", "created_at",
	}).AddRow("v3", "abc123", 3, "key3", "portal", "text/html", int64(4096), "user1", "latest changes", now)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123").
		WillReturnRows(rows)

	v, err := store.GetLatest(context.Background(), "abc123")
	require.NoError(t, err)
	assert.Equal(t, "v3", v.ID)
	assert.Equal(t, 3, v.Version)
	assert.Equal(t, "latest changes", v.ChangeSummary)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresVersionStoreGetLatestNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123").
		WillReturnError(errors.New("sql: no rows in result set"))

	_, err = store.GetLatest(context.Background(), "abc123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying latest version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Retention (#1421) ---

// countingDeleter records the keys a prune asked to remove.
type countingDeleter struct{ keys []string }

func (d *countingDeleter) DeleteObject(_ context.Context, bucket, key string) error {
	d.keys = append(d.keys, bucket+"/"+key)
	return nil
}

// failingDeleter refuses every delete, so the best-effort contract can be pinned.
type failingDeleter struct{ calls int }

func (d *failingDeleter) DeleteObject(_ context.Context, _, _ string) error {
	d.calls++
	return errors.New("storage unavailable")
}

// expectVersionWrite sets up the lock/insert/update the create path always runs,
// with maxVersions as the asset's stored override.
// expectCaptureTrim expects the statement that drops the captures belonging to
// the versions a prune just removed, in that prune's transaction (#1623).
func expectCaptureTrim(mock sqlmock.Sqlmock, assetID string, watermark int) {
	mock.ExpectExec("UPDATE portal_assets").
		WithArgs(assetID, watermark).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectVersionWrite(mock sqlmock.Sqlmock, v portaldomain.AssetVersion, currentVersion int, maxVersions any) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_version, max_versions FROM portal_assets").
		WithArgs(v.AssetID).
		WillReturnRows(sqlmock.NewRows([]string{"current_version", "max_versions"}).
			AddRow(currentVersion, maxVersions))
	mock.ExpectExec("INSERT INTO portal_asset_versions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets").WillReturnResult(sqlmock.NewResult(0, 1))
}

func retentionVersion() portaldomain.AssetVersion {
	return portaldomain.AssetVersion{
		ID: "v-new", AssetID: "abc123", S3Key: "artifacts/o/abc123/new/content.html",
		S3Bucket: "portal-assets", ContentType: "text/html", SizeBytes: 10,
		CreatedBy: "u@example.com", ChangeSummary: "write",
	}
}

// TestCreateVersion_UnlimitedRunsNoPrune pins that an asset asking to keep every
// version issues no DELETE at all -- not a DELETE that matches nothing.
func TestCreateVersion_UnlimitedRunsNoPrune(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	objects := &countingDeleter{}
	store := NewPostgres(db, objects, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 40, portaldomain.MaxVersionsUnlimited)
	mock.ExpectCommit()

	assigned, err := store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.Equal(t, 41, assigned)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, objects.keys)
}

// TestCreateVersion_PruneCutoffAndObjects pins the cutoff arithmetic the DELETE
// binds, and that every object a pruned version owned is deleted after commit.
func TestCreateVersion_PruneCutoffAndObjects(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	objects := &countingDeleter{}
	store := NewPostgres(db, objects, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 9, 3)
	// The version just assigned is 10, so a cap of 3 keeps 8, 9 and 10 and the
	// cutoff is 7.
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WithArgs(v.AssetID, 7).
		WillReturnRows(prunedRows(
			[]driver.Value{6, "portal-assets", "artifacts/o/abc123/v6/content.html"},
			[]driver.Value{7, "portal-assets", "artifacts/o/abc123/v7/content.html"},
		))
	expectCaptureTrim(mock, v.AssetID, 7)
	expectSurvivingKeys(mock, v.AssetID,
		"artifacts/o/abc123/v8/content.html",
		"artifacts/o/abc123/v9/content.html",
		v.S3Key)
	mock.ExpectCommit()

	_, err = store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
	// Both thumbnail spellings go: a version captured before thumbnails took
	// hidden names still has PNGs under the old ones (#1327).
	assert.Equal(t, []string{
		"portal-assets/artifacts/o/abc123/v6/content.html",
		"portal-assets/artifacts/o/abc123/v6/.thumbnail.png",
		"portal-assets/artifacts/o/abc123/v6/.thumbnail_dark.png",
		"portal-assets/artifacts/o/abc123/v6/thumbnail.png",
		"portal-assets/artifacts/o/abc123/v6/thumbnail_dark.png",
		"portal-assets/artifacts/o/abc123/v7/content.html",
		"portal-assets/artifacts/o/abc123/v7/.thumbnail.png",
		"portal-assets/artifacts/o/abc123/v7/.thumbnail_dark.png",
		"portal-assets/artifacts/o/abc123/v7/thumbnail.png",
		"portal-assets/artifacts/o/abc123/v7/thumbnail_dark.png",
	}, objects.keys)
}

// TestCreateVersion_PruneFailureRollsBackTheWrite pins that the prune is inside
// the transaction: a failed DELETE takes the version with it rather than leaving
// a committed version beside an unenforced cap.
func TestCreateVersion_PruneFailureRollsBackTheWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, &countingDeleter{}, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 200, nil)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnError(errors.New("deadlock detected"))
	mock.ExpectRollback()

	_, err = store.CreateVersion(context.Background(), v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pruning asset versions")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateVersion_ObjectDeleteFailureDoesNotFailTheWrite pins the best-effort
// contract: the version has committed, and an object that could not be removed
// is a storage-cleanup problem, not a failed save.
func TestCreateVersion_ObjectDeleteFailureDoesNotFailTheWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	objects := &failingDeleter{}
	store := NewPostgres(db, objects, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 100, 1)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{1, "portal-assets", "artifacts/o/abc123/v1/content.html"}))
	expectCaptureTrim(mock, v.AssetID, 100)
	expectSurvivingKeys(mock, v.AssetID, v.S3Key)
	mock.ExpectCommit()

	assigned, err := store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.Equal(t, 101, assigned)
	assert.Equal(t, 5, objects.calls,
		"content and both thumbnail spellings, light and dark, were each attempted")
}

// TestCreateVersion_NoObjectClientStillPrunes covers database-only mode.
func TestCreateVersion_NoObjectClientStillPrunes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 100, 1)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{1, "portal-assets", "k"}))
	expectCaptureTrim(mock, v.AssetID, 100)
	expectSurvivingKeys(mock, v.AssetID, v.S3Key)
	mock.ExpectCommit()

	_, err = store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateVersion_PruneScanError covers the per-row scan failure branch.
func TestCreateVersion_PruneScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, nil, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 100, 1)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{"not-a-number", "portal-assets", "k"}))
	mock.ExpectRollback()

	_, err = store.CreateVersion(context.Background(), v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scanning pruned version row")
}

// TestCreateVersion_KeepsAnObjectASurvivingVersionStillOwns is the managed-script
// shape: scriptexec keys an output object by RUN, not by version, so a run that
// exports a document and then publishes data into the same asset writes two
// version rows at one key. Pruning the older row must not delete the object the
// newer one is still reading from.
func TestCreateVersion_KeepsAnObjectASurvivingVersionStillOwns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	const sharedKey = "artifacts/scripts/s1/abc123/run-7.html"
	objects := &countingDeleter{}
	store := NewPostgres(db, objects, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 9, 2)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{7, "portal-assets", sharedKey}))
	expectCaptureTrim(mock, v.AssetID, 8)
	// Version 8 survives and names the same object version 7 did.
	expectSurvivingKeys(mock, v.AssetID, sharedKey, v.S3Key)
	mock.ExpectCommit()

	_, err = store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, objects.keys,
		"an object a surviving version still points at is never deleted")
}

// TestCreateVersion_KeepsTheCurrentThumbnailOfASharedDirectory is the second half
// of the same shape: every version of a script-written asset lives in one
// directory, so a pruned version's DERIVED thumbnail key is the current
// version's thumbnail key. Only the content object may go.
func TestCreateVersion_KeepsTheCurrentThumbnailOfASharedDirectory(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	const dir = "artifacts/scripts/s1/abc123/"
	objects := &countingDeleter{}
	store := NewPostgres(db, objects, nil, nil)
	v := retentionVersion()
	v.S3Key = dir + "run-9.html"

	expectVersionWrite(mock, v, 9, 1)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{9, "portal-assets", dir + "run-8.html"}))
	expectCaptureTrim(mock, v.AssetID, 9)
	expectSurvivingKeys(mock, v.AssetID, v.S3Key)
	mock.ExpectCommit()

	_, err = store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []string{"portal-assets/" + dir + "run-8.html"}, objects.keys,
		"the pruned content goes; the thumbnails the current version still serves stay")
}

// A capture now outlives the version it was taken from, so the prune has to
// know which object the asset row points at before it deletes anything in a
// pruned version's directory (#1431). It is a keeper, not a hint: an asset whose
// current image is a v8 capture and whose v8 row is being pruned loses the image
// it is serving if this is skipped.
func TestCreateVersion_KeepsTheThumbnailTheAssetRowPointsAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	objects := &countingDeleter{}
	store := NewPostgres(db, objects, nil, nil)
	v := retentionVersion()
	const prunedKey = "artifacts/o/abc123/v8/content.html"
	const liveThumb = "artifacts/o/abc123/v8/.thumbnail.png"

	expectVersionWrite(mock, v, 9, 1)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{8, "portal-assets", prunedKey}))
	expectCaptureTrim(mock, v.AssetID, 9)
	mock.ExpectQuery("SELECT DISTINCT s3_key FROM portal_asset_versions").
		WithArgs(v.AssetID).
		WillReturnRows(sqlmock.NewRows([]string{"s3_key"}).AddRow(v.S3Key))
	expectStoredThumbnails(mock, v.AssetID, liveThumb, "")
	mock.ExpectCommit()

	_, err = store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.NotContains(t, objects.keys, "portal-assets/"+liveThumb,
		"the image the asset is serving survives the prune of the version it was captured from")
	assert.Contains(t, objects.keys, "portal-assets/"+prunedKey)
}

// The read is inside the write's transaction, so it fails the write rather than
// leaving the prune to guess. Deleting objects the asset might still point at is
// the one outcome that cannot be undone.
func TestCreateVersion_StoredThumbnailReadFailureFailsTheWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	objects := &countingDeleter{}
	store := NewPostgres(db, objects, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 9, 1)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{8, "portal-assets", "artifacts/o/abc123/v8/content.html"}))
	expectCaptureTrim(mock, v.AssetID, 9)
	mock.ExpectQuery("SELECT DISTINCT s3_key FROM portal_asset_versions").
		WithArgs(v.AssetID).
		WillReturnRows(sqlmock.NewRows([]string{"s3_key"}).AddRow(v.S3Key))
	mock.ExpectQuery("SELECT thumbnail_s3_key, thumbnail_dark_s3_key FROM portal_assets").
		WithArgs(v.AssetID).
		WillReturnError(errors.New("connection reset"))
	mock.ExpectRollback()

	_, err = store.CreateVersion(context.Background(), v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading stored thumbnail keys")
	assert.Empty(t, objects.keys, "nothing is deleted when the transaction did not commit")
}

// TestCreateVersion_BelowTheCapIssuesNoStatement pins the common case: most
// assets never approach the cap, and a write that cannot prune anything must not
// pay for a DELETE that can never match.
func TestCreateVersion_BelowTheCapIssuesNoStatement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, &countingDeleter{}, nil, nil)
	v := retentionVersion()

	// Version 5 under a cap of 5: the oldest kept version is 1, so nothing is
	// past the cap. Any statement between the update and the commit fails here.
	expectVersionWrite(mock, v, 4, 5)
	mock.ExpectCommit()

	assigned, err := store.CreateVersion(context.Background(), v)
	require.NoError(t, err)
	assert.Equal(t, 5, assigned)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateVersion_SurvivingKeyReadFailureRollsBackTheWrite covers the error
// branch of the in-transaction read.
func TestCreateVersion_SurvivingKeyReadFailureRollsBackTheWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db, &countingDeleter{}, nil, nil)
	v := retentionVersion()

	expectVersionWrite(mock, v, 100, 1)
	mock.ExpectQuery("DELETE FROM portal_asset_versions").
		WillReturnRows(prunedRows([]driver.Value{1, "portal-assets", "k"}))
	expectCaptureTrim(mock, v.AssetID, 100)
	mock.ExpectQuery("SELECT DISTINCT s3_key FROM portal_asset_versions").
		WillReturnError(errors.New("connection reset"))
	mock.ExpectRollback()

	_, err = store.CreateVersion(context.Background(), v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading surviving version keys")
}

// A watermark below 2 leaves every capture: there is no version below it to
// have been pruned, and the origin capture is kept whatever the cap. The
// statement is never issued, which is what the absence of an expectation pins.
func TestTrimProvenanceCaptures_BelowTheOriginIssuesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	for _, watermark := range []int{-1, 0, 1} {
		require.NoError(t, TrimProvenanceCaptures(context.Background(), db, "abc123", watermark))
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTrimProvenanceCaptures_ReportsAFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectExec("UPDATE portal_assets").
		WithArgs("abc123", 7).
		WillReturnError(errors.New("connection reset"))

	err = TrimProvenanceCaptures(context.Background(), db, "abc123", 7)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trimming provenance captures")
}

// TestEffectiveCap resolves the asset override against the deployment default.
func TestEffectiveCap(t *testing.T) {
	five := 5
	assert.Equal(t, 7, (&store{platformMaxVersions: &five}).
		effectiveCap(sql.NullInt64{Int64: 7, Valid: true}), "the asset override wins")
	assert.Equal(t, 5, (&store{platformMaxVersions: &five}).
		effectiveCap(sql.NullInt64{}), "a NULL column inherits the deployment default")
	assert.Equal(t, portaldomain.DefaultMaxVersions, (&store{}).
		effectiveCap(sql.NullInt64{}), "with neither set, the platform default applies")
}

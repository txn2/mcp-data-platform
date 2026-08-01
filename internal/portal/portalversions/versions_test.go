package portalversions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func TestPostgresVersionStoreCreateVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgres(db)

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
	mock.ExpectQuery("SELECT current_version FROM portal_assets").
		WithArgs("abc123").
		WillReturnRows(sqlmock.NewRows([]string{"current_version"}).AddRow(1))
	mock.ExpectExec("INSERT INTO portal_asset_versions").
		WithArgs(version.ID, version.AssetID, version.Version, version.S3Key, version.S3Bucket,
			version.ContentType, version.SizeBytes, version.CreatedBy, version.ChangeSummary).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets").
		WithArgs(version.Version, version.S3Key, version.ContentType, version.SizeBytes, version.AssetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
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

	store := NewPostgres(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_version FROM portal_assets").
		WithArgs("").
		WillReturnRows(sqlmock.NewRows([]string{"current_version"}).AddRow(0))
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

	store := NewPostgres(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_version FROM portal_assets").
		WithArgs("").
		WillReturnRows(sqlmock.NewRows([]string{"current_version"}).AddRow(0))
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

	store := NewPostgres(db)

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

	store := NewPostgres(db)
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

	store := NewPostgres(db)

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

	store := NewPostgres(db)

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

	store := NewPostgres(db)

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

	store := NewPostgres(db)
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

	store := NewPostgres(db)

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

	store := NewPostgres(db)
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

	store := NewPostgres(db)

	mock.ExpectQuery("SELECT .+ FROM portal_asset_versions").
		WithArgs("abc123").
		WillReturnError(errors.New("sql: no rows in result set"))

	_, err = store.GetLatest(context.Background(), "abc123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying latest version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

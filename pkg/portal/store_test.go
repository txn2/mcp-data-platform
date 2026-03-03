package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- AssetStore tests ---

func TestPostgresAssetStoreInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	asset := Asset{
		ID:          "abc123",
		OwnerID:     "user1",
		Name:        "Test Dashboard",
		Description: "A test",
		ContentType: "text/html",
		S3Bucket:    "portal",
		S3Key:       "user1/abc123/content.html",
		SizeBytes:   1024,
		Tags:        []string{"dashboard"},
		Provenance:  Provenance{SessionID: "sess1"},
		SessionID:   "sess1",
	}

	mock.ExpectExec("INSERT INTO portal_assets").
		WithArgs(
			asset.ID, asset.OwnerID, asset.Name, asset.Description,
			asset.ContentType, asset.S3Bucket, asset.S3Key, asset.SizeBytes,
			sqlmock.AnyArg(), sqlmock.AnyArg(), asset.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Insert(context.Background(), asset)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreGet(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)
	now := time.Now()

	tags, _ := json.Marshal([]string{"report"})
	prov, _ := json.Marshal(Provenance{SessionID: "sess1"})

	rows := sqlmock.NewRows([]string{
		"id", "owner_id", "name", "description", "content_type", "s3_bucket", "s3_key",
		"size_bytes", "tags", "provenance", "session_id", "created_at", "updated_at", "deleted_at",
	}).AddRow(
		"abc123", "user1", "Test", "desc", "text/html", "portal", "key1",
		int64(512), tags, prov, "sess1", now, now, nil,
	)

	mock.ExpectQuery("SELECT .+ FROM portal_assets WHERE id").
		WithArgs("abc123").
		WillReturnRows(rows)

	asset, err := store.Get(context.Background(), "abc123")
	require.NoError(t, err)
	assert.Equal(t, "abc123", asset.ID)
	assert.Equal(t, "user1", asset.OwnerID)
	assert.Equal(t, []string{"report"}, asset.Tags)
	assert.Nil(t, asset.DeletedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreGetNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectQuery("SELECT .+ FROM portal_assets WHERE id").
		WithArgs("missing").
		WillReturnError(fmt.Errorf("sql: no rows in result set"))

	_, err = store.Get(context.Background(), "missing")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreList(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)
	now := time.Now()

	tags, _ := json.Marshal([]string{})
	prov, _ := json.Marshal(Provenance{})

	// Count query
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Select query
	dataRows := sqlmock.NewRows([]string{
		"id", "owner_id", "name", "description", "content_type", "s3_bucket", "s3_key",
		"size_bytes", "tags", "provenance", "session_id", "created_at", "updated_at", "deleted_at",
	}).AddRow(
		"abc123", "user1", "Test", "", "text/html", "portal", "key1",
		int64(100), tags, prov, "", now, now, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM portal_assets").WillReturnRows(dataRows)

	assets, total, err := store.List(context.Background(), AssetFilter{OwnerID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, assets, 1)
	assert.Equal(t, "abc123", assets[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreUpdate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "abc123", AssetUpdate{Name: "New Name"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreUpdateAllFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "abc123", AssetUpdate{
		Name:        "New",
		Description: "Desc",
		Tags:        []string{"tag1"},
		ContentType: "text/csv",
		S3Key:       "new/key",
		SizeBytes:   2048,
	})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreUpdateNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Update(context.Background(), "missing", AssetUpdate{Name: "x"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or deleted")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreUpdateNoFields(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)
	err = store.Update(context.Background(), "abc123", AssetUpdate{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no fields to update")
}

func TestPostgresAssetStoreSoftDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.SoftDelete(context.Background(), "abc123")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreSoftDeleteNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.SoftDelete(context.Background(), "missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- ShareStore tests ---

func TestPostgresShareStoreInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)
	expires := time.Now().Add(24 * time.Hour)

	share := Share{
		ID:        "share1",
		AssetID:   "abc123",
		Token:     "tok123",
		CreatedBy: "user1",
		ExpiresAt: &expires,
	}

	mock.ExpectExec("INSERT INTO portal_shares").
		WithArgs(share.ID, share.AssetID, share.Token, share.CreatedBy, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Insert(context.Background(), share)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreGetByToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "asset_id", "token", "created_by", "expires_at",
		"revoked", "access_count", "last_accessed_at", "created_at",
	}).AddRow("share1", "abc123", "tok123", "user1", nil, false, 5, now, now)

	mock.ExpectQuery("SELECT .+ FROM portal_shares WHERE token").
		WithArgs("tok123").
		WillReturnRows(rows)

	share, err := store.GetByToken(context.Background(), "tok123")
	require.NoError(t, err)
	assert.Equal(t, "share1", share.ID)
	assert.Equal(t, 5, share.AccessCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreListByAsset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "asset_id", "token", "created_by", "expires_at",
		"revoked", "access_count", "last_accessed_at", "created_at",
	}).AddRow("share1", "abc123", "tok1", "user1", nil, false, 0, nil, now)

	mock.ExpectQuery("SELECT .+ FROM portal_shares WHERE asset_id").
		WithArgs("abc123").
		WillReturnRows(rows)

	shares, err := store.ListByAsset(context.Background(), "abc123")
	require.NoError(t, err)
	assert.Len(t, shares, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreRevoke(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectExec("UPDATE portal_shares SET revoked").
		WithArgs("share1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Revoke(context.Background(), "share1")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreRevokeNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectExec("UPDATE portal_shares SET revoked").
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Revoke(context.Background(), "missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already revoked")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreIncrementAccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectExec("UPDATE portal_shares SET access_count").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.IncrementAccess(context.Background(), "share1")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Noop store tests ---

func TestNoopAssetStore(t *testing.T) {
	store := NewNoopAssetStore()
	ctx := context.Background()

	assert.NoError(t, store.Insert(ctx, Asset{}))

	_, err := store.Get(ctx, "any")
	assert.Error(t, err)

	assets, total, err := store.List(ctx, AssetFilter{})
	assert.NoError(t, err)
	assert.Nil(t, assets)
	assert.Equal(t, 0, total)

	assert.NoError(t, store.Update(ctx, "any", AssetUpdate{}))
	assert.NoError(t, store.SoftDelete(ctx, "any"))
}

func TestNoopShareStore(t *testing.T) {
	store := NewNoopShareStore()
	ctx := context.Background()

	assert.NoError(t, store.Insert(ctx, Share{}))

	_, err := store.GetByToken(ctx, "any")
	assert.Error(t, err)

	shares, err := store.ListByAsset(ctx, "any")
	assert.NoError(t, err)
	assert.Nil(t, shares)

	assert.NoError(t, store.Revoke(ctx, "any"))
	assert.NoError(t, store.IncrementAccess(ctx, "any"))
}

// --- Type tests ---

func TestAssetFilterEffectiveLimit(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int
	}{
		{"default", 0, defaultLimit},
		{"negative", -1, defaultLimit},
		{"small", 10, 10},
		{"max", maxLimit, maxLimit},
		{"over_max", maxLimit + 1, maxLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := AssetFilter{Limit: tt.limit}
			assert.Equal(t, tt.expected, f.EffectiveLimit())
		})
	}
}

// --- Validation tests ---

func TestValidateAssetName(t *testing.T) {
	assert.Error(t, ValidateAssetName(""))
	assert.NoError(t, ValidateAssetName("valid name"))

	longName := make([]byte, maxNameLength+1)
	for i := range longName {
		longName[i] = 'a'
	}
	assert.Error(t, ValidateAssetName(string(longName)))
}

func TestValidateContentType(t *testing.T) {
	assert.Error(t, ValidateContentType(""))
	assert.NoError(t, ValidateContentType("text/html"))
}

func TestValidateTags(t *testing.T) {
	assert.NoError(t, ValidateTags(nil))
	assert.NoError(t, ValidateTags([]string{"a", "b"}))

	tooMany := make([]string, maxTags+1)
	assert.Error(t, ValidateTags(tooMany))

	longTag := make([]byte, maxTagLength+1)
	for i := range longTag {
		longTag[i] = 'a'
	}
	assert.Error(t, ValidateTags([]string{string(longTag)}))
}

func TestValidateDescription(t *testing.T) {
	assert.NoError(t, ValidateDescription(""))
	assert.NoError(t, ValidateDescription("valid"))

	longDesc := make([]byte, maxDescriptionLength+1)
	for i := range longDesc {
		longDesc[i] = 'a'
	}
	assert.Error(t, ValidateDescription(string(longDesc)))
}

// --- Error path tests ---

func TestPostgresAssetStoreInsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("INSERT INTO portal_assets").
		WillReturnError(fmt.Errorf("db error"))

	err = store.Insert(context.Background(), Asset{
		Tags: []string{}, Provenance: Provenance{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting asset")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreListCountError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectQuery("SELECT COUNT").WillReturnError(fmt.Errorf("db error"))

	_, _, err = store.List(context.Background(), AssetFilter{OwnerID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "counting assets")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreListQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT .+ FROM portal_assets").WillReturnError(fmt.Errorf("db error"))

	_, _, err = store.List(context.Background(), AssetFilter{OwnerID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying assets")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreListWithOffset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	tags, _ := json.Marshal([]string{})
	prov, _ := json.Marshal(Provenance{})

	dataRows := sqlmock.NewRows([]string{
		"id", "owner_id", "name", "description", "content_type", "s3_bucket", "s3_key",
		"size_bytes", "tags", "provenance", "session_id", "created_at", "updated_at", "deleted_at",
	}).AddRow(
		"abc123", "user1", "Test", "", "text/html", "portal", "key1",
		int64(100), tags, prov, "", time.Now(), time.Now(), nil,
	)
	mock.ExpectQuery("SELECT .+ FROM portal_assets").WillReturnRows(dataRows)

	assets, _, err := store.List(context.Background(), AssetFilter{
		OwnerID: "user1", Offset: 10, Limit: 5,
	})
	require.NoError(t, err)
	assert.Len(t, assets, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreListFilterByTag(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT .+ FROM portal_assets").WillReturnRows(
		sqlmock.NewRows([]string{
			"id", "owner_id", "name", "description", "content_type", "s3_bucket", "s3_key",
			"size_bytes", "tags", "provenance", "session_id", "created_at", "updated_at", "deleted_at",
		}),
	)

	assets, _, err := store.List(context.Background(), AssetFilter{Tag: "dashboard"})
	require.NoError(t, err)
	assert.Empty(t, assets)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreListFilterByContentType(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT .+ FROM portal_assets").WillReturnRows(
		sqlmock.NewRows([]string{
			"id", "owner_id", "name", "description", "content_type", "s3_bucket", "s3_key",
			"size_bytes", "tags", "provenance", "session_id", "created_at", "updated_at", "deleted_at",
		}),
	)

	assets, _, err := store.List(context.Background(), AssetFilter{ContentType: "text/html"})
	require.NoError(t, err)
	assert.Empty(t, assets)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreUpdateExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets").
		WillReturnError(fmt.Errorf("db error"))

	err = store.Update(context.Background(), "abc123", AssetUpdate{Name: "x"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating asset")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreSoftDeleteExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	mock.ExpectExec("UPDATE portal_assets SET deleted_at").
		WillReturnError(fmt.Errorf("db error"))

	err = store.SoftDelete(context.Background(), "abc123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "soft-deleting asset")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreInsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectExec("INSERT INTO portal_shares").
		WillReturnError(fmt.Errorf("db error"))

	err = store.Insert(context.Background(), Share{ID: "s1", AssetID: "a1", Token: "t1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting share")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreListByAssetError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectQuery("SELECT .+ FROM portal_shares WHERE asset_id").
		WillReturnError(fmt.Errorf("db error"))

	_, err = store.ListByAsset(context.Background(), "abc123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying shares")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreRevokeExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectExec("UPDATE portal_shares SET revoked").
		WillReturnError(fmt.Errorf("db error"))

	err = store.Revoke(context.Background(), "share1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "revoking share")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreIncrementAccessError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectExec("UPDATE portal_shares SET access_count").
		WillReturnError(fmt.Errorf("db error"))

	err = store.IncrementAccess(context.Background(), "share1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "incrementing access count")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreGetByTokenNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectQuery("SELECT .+ FROM portal_shares WHERE token").
		WithArgs("missing").
		WillReturnError(fmt.Errorf("sql: no rows in result set"))

	_, err = store.GetByToken(context.Background(), "missing")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresShareStoreGetByTokenWithExpiration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)
	now := time.Now()
	expires := now.Add(24 * time.Hour)

	rows := sqlmock.NewRows([]string{
		"id", "asset_id", "token", "created_by", "expires_at",
		"revoked", "access_count", "last_accessed_at", "created_at",
	}).AddRow("share1", "abc123", "tok123", "user1", expires, false, 0, nil, now)

	mock.ExpectQuery("SELECT .+ FROM portal_shares WHERE token").
		WithArgs("tok123").
		WillReturnRows(rows)

	share, err := store.GetByToken(context.Background(), "tok123")
	require.NoError(t, err)
	assert.NotNil(t, share.ExpiresAt)
	assert.Nil(t, share.LastAccessedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAssetStoreGetWithDeletedAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)
	now := time.Now()
	deletedAt := now.Add(-1 * time.Hour)

	tags, _ := json.Marshal([]string{})
	prov, _ := json.Marshal(Provenance{})

	rows := sqlmock.NewRows([]string{
		"id", "owner_id", "name", "description", "content_type", "s3_bucket", "s3_key",
		"size_bytes", "tags", "provenance", "session_id", "created_at", "updated_at", "deleted_at",
	}).AddRow(
		"abc123", "user1", "Test", "desc", "text/html", "portal", "key1",
		int64(512), tags, prov, "sess1", now, now, deletedAt,
	)

	mock.ExpectQuery("SELECT .+ FROM portal_assets WHERE id").
		WithArgs("abc123").
		WillReturnRows(rows)

	asset, err := store.Get(context.Background(), "abc123")
	require.NoError(t, err)
	assert.NotNil(t, asset.DeletedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

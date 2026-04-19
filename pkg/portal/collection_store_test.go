package portal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CollectionStore tests ---

func TestPostgresCollectionStoreInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	coll := Collection{
		ID:             "coll1",
		OwnerID:        "user1",
		OwnerEmail:     "user1@example.com",
		Name:           "My Collection",
		Description:    "A test collection",
		ThumbnailS3Key: "thumb/key.png",
		Config:         CollectionConfig{ThumbnailSize: "large"},
	}

	mock.ExpectExec("INSERT INTO portal_collections").
		WithArgs(coll.ID, coll.OwnerID, coll.OwnerEmail, coll.Name, coll.Description, coll.ThumbnailS3Key, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Insert(context.Background(), coll)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreInsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	coll := Collection{
		ID:      "coll1",
		OwnerID: "user1",
		Name:    "Failing Collection",
	}

	mock.ExpectExec("INSERT INTO portal_collections").
		WithArgs(coll.ID, coll.OwnerID, coll.OwnerEmail, coll.Name, coll.Description, coll.ThumbnailS3Key, sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("unique constraint violation"))

	err = store.Insert(context.Background(), coll)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting collection")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreGet(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)
	now := time.Now()
	configJSON, _ := json.Marshal(CollectionConfig{ThumbnailSize: "medium"})

	// getHeader query
	headerRows := sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key", "config",
		"created_at", "updated_at", "deleted_at",
	}).AddRow("coll1", "user1", "user1@example.com", "My Collection", "desc", "thumb.png", configJSON, now, now, nil)

	mock.ExpectQuery("SELECT .+ FROM portal_collections WHERE id").
		WithArgs("coll1").
		WillReturnRows(headerRows)

	// getSections query
	sectionRows := sqlmock.NewRows([]string{
		"id", "collection_id", "title", "description", "position", "created_at",
	}).
		AddRow("sec1", "coll1", "Section One", "First section", 0, now).
		AddRow("sec2", "coll1", "Section Two", "Second section", 1, now)

	mock.ExpectQuery("SELECT .+ FROM portal_collection_sections").
		WithArgs("coll1").
		WillReturnRows(sectionRows)

	// getItemsBySections query
	itemRows := sqlmock.NewRows([]string{
		"id", "section_id", "asset_id", "position", "created_at",
		"name", "content_type", "thumbnail_s3_key", "description",
	}).
		AddRow("item1", "sec1", "asset1", 0, now, "Asset One", "text/html", "t1.png", "First asset").
		AddRow("item2", "sec2", "asset2", 0, now, "Asset Two", "image/svg+xml", "t2.png", "Second asset")

	mock.ExpectQuery("SELECT .+ FROM portal_collection_items").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(itemRows)

	coll, err := store.Get(context.Background(), "coll1")
	require.NoError(t, err)
	assert.Equal(t, "coll1", coll.ID)
	assert.Equal(t, "user1", coll.OwnerID)
	assert.Equal(t, "user1@example.com", coll.OwnerEmail)
	assert.Equal(t, "medium", coll.Config.ThumbnailSize)
	assert.Nil(t, coll.DeletedAt)
	require.Len(t, coll.Sections, 2)
	assert.Equal(t, "Section One", coll.Sections[0].Title)
	require.Len(t, coll.Sections[0].Items, 1)
	assert.Equal(t, "asset1", coll.Sections[0].Items[0].AssetID)
	assert.Equal(t, "Asset One", coll.Sections[0].Items[0].AssetName)
	require.Len(t, coll.Sections[1].Items, 1)
	assert.Equal(t, "asset2", coll.Sections[1].Items[0].AssetID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreGetNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectQuery("SELECT .+ FROM portal_collections WHERE id").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	_, err = store.Get(context.Background(), "missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying collection")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreList(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)
	now := time.Now()
	configJSON, _ := json.Marshal(CollectionConfig{})

	// COUNT query
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// SELECT query
	dataRows := sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key", "config",
		"created_at", "updated_at", "deleted_at",
	}).AddRow("coll1", "user1", "user1@example.com", "My Collection", "desc", "", configJSON, now, now, nil)

	mock.ExpectQuery("SELECT .+ FROM portal_collections").
		WillReturnRows(dataRows)

	// populateAssetTags query
	tagRows := sqlmock.NewRows([]string{"collection_id", "tags"}).
		AddRow("coll1", pq.Array([]string{"dashboard", "report"}))
	mock.ExpectQuery("SELECT cs.collection_id").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(tagRows)

	collections, total, err := store.List(context.Background(), CollectionFilter{OwnerID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, collections, 1)
	assert.Equal(t, "coll1", collections[0].ID)
	assert.Equal(t, []string{"dashboard", "report"}, collections[0].AssetTags)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreListEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	// COUNT query returns 0
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	// SELECT query returns empty
	mock.ExpectQuery("SELECT .+ FROM portal_collections").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key", "config",
			"created_at", "updated_at", "deleted_at",
		}))

	// populateAssetTags not called for empty slice — no mock needed

	collections, total, err := store.List(context.Background(), CollectionFilter{OwnerID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, collections)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreListWithSearch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)
	now := time.Now()
	configJSON, _ := json.Marshal(CollectionConfig{})

	// COUNT query with search filter
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// SELECT query
	dataRows := sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key", "config",
		"created_at", "updated_at", "deleted_at",
	}).AddRow("coll1", "user1", "user1@example.com", "Dashboard Collection", "has dashboards", "", configJSON, now, now, nil)

	mock.ExpectQuery("SELECT .+ FROM portal_collections").
		WillReturnRows(dataRows)

	// populateAssetTags
	mock.ExpectQuery("SELECT cs.collection_id").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"collection_id", "tags"}))

	collections, total, err := store.List(context.Background(), CollectionFilter{
		OwnerID: "user1",
		Search:  "dashboard",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, collections, 1)
	assert.Equal(t, "Dashboard Collection", collections[0].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections SET name").
		WithArgs("New Name", "New Description", sqlmock.AnyArg(), "coll1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "coll1", "New Name", "New Description")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdateNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections SET name").
		WithArgs("Name", "Desc", sqlmock.AnyArg(), "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Update(context.Background(), "missing", "Name", "Desc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdateConfig(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections SET config").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "coll1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.UpdateConfig(context.Background(), "coll1", CollectionConfig{ThumbnailSize: "small"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdateThumbnail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections SET thumbnail_s3_key").
		WithArgs("new/thumb.png", sqlmock.AnyArg(), "coll1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.UpdateThumbnail(context.Background(), "coll1", "new/thumb.png")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreSoftDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "coll1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.SoftDelete(context.Background(), "coll1")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreSoftDeleteNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.SoftDelete(context.Background(), "missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreSetSections(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	sections := []CollectionSection{
		{
			ID:          "sec1",
			Title:       "Section A",
			Description: "First",
			Items: []CollectionItem{
				{ID: "item1", AssetID: "asset1"},
				{ID: "item2", AssetID: "asset2"},
			},
		},
		{
			ID:          "sec2",
			Title:       "Section B",
			Description: "Second",
			Items: []CollectionItem{
				{ID: "item3", AssetID: "asset3"},
			},
		},
	}

	mock.ExpectBegin()

	// DELETE existing sections
	mock.ExpectExec("DELETE FROM portal_collection_sections").
		WithArgs("coll1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Prepare section insert
	mock.ExpectPrepare("INSERT INTO portal_collection_sections")

	// Prepare item insert
	mock.ExpectPrepare("INSERT INTO portal_collection_items")

	// Section 1 insert
	mock.ExpectExec("INSERT INTO portal_collection_sections").
		WithArgs("sec1", "coll1", "Section A", "First", 0).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Section 1 items
	mock.ExpectExec("INSERT INTO portal_collection_items").
		WithArgs("item1", "sec1", "asset1", 0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_collection_items").
		WithArgs("item2", "sec1", "asset2", 1).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Section 2 insert
	mock.ExpectExec("INSERT INTO portal_collection_sections").
		WithArgs("sec2", "coll1", "Section B", "Second", 1).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Section 2 items
	mock.ExpectExec("INSERT INTO portal_collection_items").
		WithArgs("item3", "sec2", "asset3", 0).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Touch updated_at
	mock.ExpectExec("UPDATE portal_collections SET updated_at").
		WithArgs(sqlmock.AnyArg(), "coll1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	err = store.SetSections(context.Background(), "coll1", sections)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStorePopulateAssetTags(t *testing.T) {
	// populateAssetTags is tested indirectly through List.
	// This test verifies that tags from the query are assigned to the correct collections.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)
	now := time.Now()
	configJSON, _ := json.Marshal(CollectionConfig{})

	// COUNT
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	// SELECT — two collections
	dataRows := sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key", "config",
		"created_at", "updated_at", "deleted_at",
	}).
		AddRow("c1", "u1", "u1@example.com", "Coll 1", "", "", configJSON, now, now, nil).
		AddRow("c2", "u1", "u1@example.com", "Coll 2", "", "", configJSON, now, now, nil)

	mock.ExpectQuery("SELECT .+ FROM portal_collections").
		WillReturnRows(dataRows)

	// populateAssetTags — only c1 has tags
	tagRows := sqlmock.NewRows([]string{"collection_id", "tags"}).
		AddRow("c1", pq.Array([]string{"chart", "svg"}))
	mock.ExpectQuery("SELECT cs.collection_id").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(tagRows)

	collections, _, err := store.List(context.Background(), CollectionFilter{OwnerID: "u1"})
	require.NoError(t, err)
	require.Len(t, collections, 2)
	assert.Equal(t, []string{"chart", "svg"}, collections[0].AssetTags)
	assert.Nil(t, collections[1].AssetTags) // c2 has no tags
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Noop CollectionStore ---

func TestNoopCollectionStore(t *testing.T) {
	store := NewNoopCollectionStore()

	ctx := context.Background()

	err := store.Insert(ctx, Collection{})
	assert.NoError(t, err)

	_, err = store.Get(ctx, "any")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection not found")

	collections, total, err := store.List(ctx, CollectionFilter{})
	assert.NoError(t, err)
	assert.Nil(t, collections)
	assert.Equal(t, 0, total)

	err = store.Update(ctx, "any", "name", "desc")
	assert.NoError(t, err)

	err = store.UpdateConfig(ctx, "any", CollectionConfig{})
	assert.NoError(t, err)

	err = store.UpdateThumbnail(ctx, "any", "key")
	assert.NoError(t, err)

	err = store.SoftDelete(ctx, "any")
	assert.NoError(t, err)

	err = store.SetSections(ctx, "any", nil)
	assert.NoError(t, err)
}

// --- CollectionFilter.EffectiveLimit ---

func TestCollectionFilterEffectiveLimit(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int
	}{
		{"default when zero", 0, defaultLimit},
		{"default when negative", -1, defaultLimit},
		{"normal value", 25, 25},
		{"capped at max", 500, maxLimit},
		{"exactly max", maxLimit, maxLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := CollectionFilter{Limit: tc.limit}
			assert.Equal(t, tc.expected, f.EffectiveLimit())
		})
	}
}

// --- AssetStore.GetByIDs ---

func TestGetByIDsEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)

	result, err := store.GetByIDs(context.Background(), []string{})
	require.NoError(t, err)
	assert.Empty(t, result)
	// No query should have been executed.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByIDsSuccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)
	now := time.Now()

	tags, _ := json.Marshal([]string{"tag1"})
	prov, _ := json.Marshal(Provenance{SessionID: "s1"})

	rows := sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description", "content_type", "s3_bucket", "s3_key",
		"thumbnail_s3_key", "size_bytes", "tags", "provenance", "session_id", "current_version",
		"created_at", "updated_at", "deleted_at", "idempotency_key",
	}).
		AddRow("a1", "u1", "u1@test.com", "Asset 1", "desc1", "text/html", "bucket", "k1",
			"", int64(100), tags, prov, "s1", 1, now, now, nil, "").
		AddRow("a2", "u1", "u1@test.com", "Asset 2", "desc2", "image/svg+xml", "bucket", "k2",
			"thumb.png", int64(200), tags, prov, "s1", 1, now, now, nil, "")

	mock.ExpectQuery("SELECT .+ FROM portal_assets WHERE id").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := store.GetByIDs(context.Background(), []string{"a1", "a2"})
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "Asset 1", result["a1"].Name)
	assert.Equal(t, "Asset 2", result["a2"].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- ShareStore collection-related tests ---

func TestGetUserCollectionPermission(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectQuery("SELECT permission FROM portal_shares").
		WithArgs("coll1", "user1", "user1@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"permission"}).AddRow("editor"))

	perm, err := store.GetUserCollectionPermission(context.Background(), "coll1", "user1", "user1@example.com")
	require.NoError(t, err)
	assert.Equal(t, PermissionEditor, perm)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserCollectionPermissionNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	mock.ExpectQuery("SELECT permission FROM portal_shares").
		WithArgs("coll1", "user1", "user1@example.com").
		WillReturnError(sql.ErrNoRows)

	_, err = store.GetUserCollectionPermission(context.Background(), "coll1", "user1", "user1@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying user collection permission")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListActiveCollectionShareSummaries(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)

	rows := sqlmock.NewRows([]string{"collection_id", "has_user_share", "has_public_link"}).
		AddRow("coll1", true, false).
		AddRow("coll2", false, true)

	mock.ExpectQuery("SELECT collection_id").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := store.ListActiveCollectionShareSummaries(context.Background(), []string{"coll1", "coll2"})
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.True(t, result["coll1"].HasUserShare)
	assert.False(t, result["coll1"].HasPublicLink)
	assert.False(t, result["coll2"].HasUserShare)
	assert.True(t, result["coll2"].HasPublicLink)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSharedCollectionsWithUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresShareStore(db)
	now := time.Now()
	configJSON, _ := json.Marshal(CollectionConfig{ThumbnailSize: "large"})

	// COUNT query
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("user2", "user2@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// SELECT query
	dataRows := sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description",
		"thumbnail_s3_key", "config", "created_at", "updated_at", "deleted_at",
		"share_id", "shared_by", "shared_at", "permission",
	}).AddRow(
		"coll1", "user1", "user1@example.com", "Shared Collection", "A shared collection",
		"thumb.png", configJSON, now, now, nil,
		"share1", "user1@example.com", now, "viewer",
	)

	mock.ExpectQuery("SELECT .+ FROM portal_shares").
		WithArgs("user2", "user2@example.com", 50, 0).
		WillReturnRows(dataRows)

	results, total, err := store.ListSharedCollectionsWithUser(context.Background(), "user2", "user2@example.com", 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, results, 1)
	assert.Equal(t, "coll1", results[0].Collection.ID)
	assert.Equal(t, "Shared Collection", results[0].Collection.Name)
	assert.Equal(t, "large", results[0].Collection.Config.ThumbnailSize)
	assert.Equal(t, "share1", results[0].ShareID)
	assert.Equal(t, "user1@example.com", results[0].SharedBy)
	assert.Equal(t, PermissionViewer, results[0].Permission)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdateConfigNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.UpdateConfig(context.Background(), "nonexistent", CollectionConfig{ThumbnailSize: "small"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdateConfigError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections").
		WillReturnError(fmt.Errorf("db error"))

	err = store.UpdateConfig(context.Background(), "coll1", CollectionConfig{ThumbnailSize: "small"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating config")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdateThumbnailNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.UpdateThumbnail(context.Background(), "nonexistent", "key.png")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreUpdateThumbnailError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections").
		WillReturnError(fmt.Errorf("db error"))

	err = store.UpdateThumbnail(context.Background(), "coll1", "key.png")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating thumbnail")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreSetSectionsBeginError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectBegin().WillReturnError(fmt.Errorf("begin error"))

	err = store.SetSections(context.Background(), "coll1", []CollectionSection{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "beginning transaction")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreSetSectionsDeleteError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresCollectionStore(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM portal_collection_sections").
		WillReturnError(fmt.Errorf("delete error"))
	mock.ExpectRollback()

	err = store.SetSections(context.Background(), "coll1", []CollectionSection{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting existing sections")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresCollectionStoreGetItemsBySectionsEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store, ok := NewPostgresCollectionStore(db).(*postgresCollectionStore)
	require.True(t, ok)

	// getItemsBySections with empty slice should return empty map without querying
	result, err := store.getItemsBySections(context.Background(), []string{})
	assert.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

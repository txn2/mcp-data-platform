package portalpurge

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newOneMock(t *testing.T, objects ObjectDeleter) (*Purger, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPurger(db, objects, "portal"), mock
}

func expectRowDeletes(mock sqlmock.Sqlmock, dependents int, final string, removed int64) {
	mock.ExpectBegin()
	for range dependents {
		mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(regexp.QuoteMeta(final)).WillReturnResult(sqlmock.NewResult(0, removed))
	mock.ExpectCommit()
}

// Delete permanently removes one deleted asset's objects first, then its
// rows, whatever its age.
func TestPurgeAssetRemovesObjectsThenRows(t *testing.T) {
	objs := &deletes{}
	p, mock := newOneMock(t, objs)
	mock.ExpectQuery(regexp.QuoteMeta(selectDeletedAssetQuery)).WithArgs("a1").
		WillReturnRows(sqlmock.NewRows(assetCols).
			AddRow("a1", "", "assets/a1/v2/content.html", "", "", pq.StringArray{""}, pq.StringArray{"assets/a1/v1/content.html"}))
	expectRowDeletes(mock, 4, purgeAssetsQuery, 1)

	require.NoError(t, p.PurgeAsset(context.Background(), "a1"))
	assert.Contains(t, objs.keys, "portal/assets/a1/v2/content.html")
	assert.Contains(t, objs.keys, "portal/assets/a1/v1/content.html")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPurgeAssetRefusals(t *testing.T) {
	p, mock := newOneMock(t, &deletes{fail: "k"})
	mock.ExpectQuery(regexp.QuoteMeta(selectDeletedAssetQuery)).WillReturnRows(sqlmock.NewRows(assetCols))
	require.ErrorIs(t, p.PurgeAsset(context.Background(), "live"), ErrNotInTrash)

	mock.ExpectQuery(regexp.QuoteMeta(selectDeletedAssetQuery)).
		WillReturnRows(sqlmock.NewRows(assetCols).AddRow("a1", "b", "k", "", "", pq.StringArray{}, pq.StringArray{}))
	require.ErrorIs(t, p.PurgeAsset(context.Background(), "a1"), ErrObjectsKept)

	mock.ExpectQuery(regexp.QuoteMeta(selectDeletedAssetQuery)).WillReturnError(errors.New("down"))
	require.Error(t, p.PurgeAsset(context.Background(), "a1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// An asset restored between its read and its delete keeps its row.
func TestPurgeAssetRestoredMeanwhile(t *testing.T) {
	p, mock := newOneMock(t, nil)
	mock.ExpectQuery(regexp.QuoteMeta(selectDeletedAssetQuery)).
		WillReturnRows(sqlmock.NewRows(assetCols).AddRow("a1", "", "", "", "", pq.StringArray{}, pq.StringArray{}))
	expectRowDeletes(mock, 4, purgeAssetsQuery, 0)
	require.ErrorIs(t, p.PurgeAsset(context.Background(), "a1"), ErrNotInTrash)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPurgeCollection(t *testing.T) {
	objs := &deletes{}
	p, mock := newOneMock(t, objs)
	mock.ExpectQuery(regexp.QuoteMeta(selectDeletedCollectionQuery)).WithArgs("c1").
		WillReturnRows(sqlmock.NewRows([]string{"thumbnail_s3_key"}).AddRow("collections/c1/thumb.png"))
	expectRowDeletes(mock, 2, purgeCollectionsQuery, 1)
	require.NoError(t, p.PurgeCollection(context.Background(), "c1"))
	assert.Len(t, objs.keys, 2, "the mosaic and its dark variant")

	mock.ExpectQuery(regexp.QuoteMeta(selectDeletedCollectionQuery)).WillReturnRows(sqlmock.NewRows([]string{"k"}))
	require.ErrorIs(t, p.PurgeCollection(context.Background(), "live"), ErrNotInTrash)

	p2, mock2 := newOneMock(t, &deletes{fail: "collections/c1/thumb.png"})
	mock2.ExpectQuery(regexp.QuoteMeta(selectDeletedCollectionQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"thumbnail_s3_key"}).AddRow("collections/c1/thumb.png"))
	require.ErrorIs(t, p2.PurgeCollection(context.Background(), "c1"), ErrObjectsKept)
	require.NoError(t, mock.ExpectationsWereMet())
	require.NoError(t, mock2.ExpectationsWereMet())
}

func TestRemovedOne(t *testing.T) {
	require.NoError(t, removedOne(1, nil))
	require.ErrorIs(t, removedOne(0, nil), ErrNotInTrash)
	boom := errors.New("boom")
	require.ErrorIs(t, removedOne(1, boom), boom)
}

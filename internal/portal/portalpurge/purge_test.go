package portalpurge

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deletes struct {
	keys []string
	fail string
}

func (d *deletes) DeleteObject(_ context.Context, bucket, key string) error {
	if key == d.fail {
		return errors.New("denied")
	}
	d.keys = append(d.keys, bucket+"/"+key)
	return nil
}

var (
	assetCols = []string{"id", "s3_bucket", "s3_key", "thumbnail_s3_key", "thumbnail_dark_s3_key", "vb", "vk"}
	collCols  = []string{"id", "thumbnail_s3_key"}
)

func expectAssetRows(mock sqlmock.Sqlmock, rows ...[]any) {
	r := sqlmock.NewRows(assetCols)
	for _, row := range rows {
		vals := make([]driver.Value, len(row))
		for i, v := range row {
			vals[i] = v
		}
		r.AddRow(vals...)
	}
	mock.ExpectQuery(regexp.QuoteMeta(selectPurgeableAssetsQuery)).WillReturnRows(r)
}

func expectEmptyTail(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(selectPurgeableCollectionsQuery)).WillReturnRows(sqlmock.NewRows(collCols))
	mock.ExpectExec(regexp.QuoteMeta(purgeThreadsQuery)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(purgeKnowledgePagesQuery)).WillReturnResult(sqlmock.NewResult(0, 0))
}

// The purge deletes an asset's objects before its rows, and an asset one of
// whose objects will not delete keeps its row.
func TestPurge_ObjectsThenRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expectAssetRows(mock,
		[]any{"a1", "b", "p/a1/v2/content.html", "", "", pq.StringArray{"b"}, pq.StringArray{"p/a1/v1/content.html"}},
		[]any{"a2", "b", "p/a2/content.html", "", "", pq.StringArray{}, pq.StringArray{}},
	)
	mock.ExpectBegin()
	for _, q := range []string{purgeAssetSharesQuery, purgeAssetItemsQuery, purgeAssetThreadsQuery, purgeAssetProducersQuery} {
		mock.ExpectExec(regexp.QuoteMeta(q)).WithArgs(pq.Array([]string{"a1"})).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(regexp.QuoteMeta(purgeAssetsQuery)).WithArgs(pq.Array([]string{"a1"}), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectEmptyTail(mock)

	objects := &deletes{fail: "p/a2/content.html"}
	res, err := NewPurger(db, objects, "b").Purge(context.Background(), time.Now())
	require.NoError(t, err)
	assert.Equal(t, PurgeResult{Assets: 1}, res)
	assert.Contains(t, objects.keys, "b/p/a1/v1/content.html")
	assert.Contains(t, objects.keys, "b/p/a1/v2/.thumbnail.png")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A collection's mosaic and its dark neighbor go before the row.
func TestPurge_Collections(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectAssetRows(mock)
	mock.ExpectQuery(regexp.QuoteMeta(selectPurgeableCollectionsQuery)).
		WillReturnRows(sqlmock.NewRows(collCols).AddRow("c1", "p/collections/c1/thumbnail.png").AddRow("c2", ""))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(purgeCollectionSharesQuery)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(purgeCollectionThreadsQuery)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(purgeCollectionsQuery)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta(purgeThreadsQuery)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(purgeKnowledgePagesQuery)).WillReturnResult(sqlmock.NewResult(0, 0))

	objects := &deletes{}
	res, err := NewPurger(db, objects, "b").Purge(context.Background(), time.Now())
	require.NoError(t, err)
	assert.Equal(t, 2, res.Collections)
	assert.ElementsMatch(t, []string{"b/p/collections/c1/thumbnail.png", "b/p/collections/c1/thumbnail_dark.png"}, objects.keys)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A full batch of threads is followed by another, until one comes back short.
func TestPurge_DrainsFullBatches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectAssetRows(mock)
	mock.ExpectQuery(regexp.QuoteMeta(selectPurgeableCollectionsQuery)).WillReturnRows(sqlmock.NewRows(collCols))
	mock.ExpectExec(regexp.QuoteMeta(purgeThreadsQuery)).WillReturnResult(sqlmock.NewResult(0, purgeBatch))
	mock.ExpectExec(regexp.QuoteMeta(purgeThreadsQuery)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta(purgeKnowledgePagesQuery)).WillReturnResult(sqlmock.NewResult(0, 0))

	res, err := NewPurger(db, nil, "b").Purge(context.Background(), time.Now())
	require.NoError(t, err)
	assert.Equal(t, purgeBatch+3, res.Threads)
	assert.Equal(t, purgeBatch+3, res.Total())
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Each failure stops the sweep and is returned.
func TestPurge_Failures(t *testing.T) {
	boom := errors.New("down")
	one := func(mock sqlmock.Sqlmock) {
		expectAssetRows(mock, []any{"a1", "b", "k", "", "", pq.StringArray{}, pq.StringArray{}})
	}
	for name, setup := range map[string]func(sqlmock.Sqlmock){
		"listing assets": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(selectPurgeableAssetsQuery)).WillReturnError(boom)
		},
		"scanning an asset": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(selectPurgeableAssetsQuery)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a1"))
		},
		"beginning": func(m sqlmock.Sqlmock) { one(m); m.ExpectBegin().WillReturnError(boom) },
		"a dependent": func(m sqlmock.Sqlmock) {
			one(m)
			m.ExpectBegin()
			m.ExpectExec(regexp.QuoteMeta(purgeAssetSharesQuery)).WillReturnError(boom)
		},
		"the rows": func(m sqlmock.Sqlmock) {
			one(m)
			m.ExpectBegin()
			for _, q := range []string{purgeAssetSharesQuery, purgeAssetItemsQuery, purgeAssetThreadsQuery, purgeAssetProducersQuery} {
				m.ExpectExec(regexp.QuoteMeta(q)).WillReturnResult(sqlmock.NewResult(0, 0))
			}
			m.ExpectExec(regexp.QuoteMeta(purgeAssetsQuery)).WillReturnError(boom)
		},
		"committing": func(m sqlmock.Sqlmock) {
			one(m)
			m.ExpectBegin()
			for _, q := range []string{purgeAssetSharesQuery, purgeAssetItemsQuery, purgeAssetThreadsQuery, purgeAssetProducersQuery, purgeAssetsQuery} {
				m.ExpectExec(regexp.QuoteMeta(q)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			m.ExpectCommit().WillReturnError(boom)
		},
		"listing collections": func(m sqlmock.Sqlmock) {
			expectAssetRows(m)
			m.ExpectQuery(regexp.QuoteMeta(selectPurgeableCollectionsQuery)).WillReturnError(boom)
		},
		"scanning a collection": func(m sqlmock.Sqlmock) {
			expectAssetRows(m)
			m.ExpectQuery(regexp.QuoteMeta(selectPurgeableCollectionsQuery)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("c1"))
		},
		"threads": func(m sqlmock.Sqlmock) {
			expectAssetRows(m)
			m.ExpectQuery(regexp.QuoteMeta(selectPurgeableCollectionsQuery)).WillReturnRows(sqlmock.NewRows(collCols))
			m.ExpectExec(regexp.QuoteMeta(purgeThreadsQuery)).WillReturnError(boom)
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			setup(mock)
			_, err = NewPurger(db, &deletes{}, "b").Purge(context.Background(), time.Now())
			assert.Error(t, err)
		})
	}
}

func TestAssetObjectsAreEachNamedOnce(t *testing.T) {
	got := assetObjects(assetRow{"b", "p/a/content.md", "p/a/.thumbnail.png", "", []string{"b"}, []string{"p/a/content.md", "p/a/v2/content.md"}})
	seen := map[storedObject]bool{}
	for _, o := range got {
		assert.False(t, seen[o], "duplicate %v", o)
		seen[o] = true
	}
	assert.True(t, seen[storedObject{"b", "p/a/content.md"}])
}

package portalpurge

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/aws/smithy-go"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deletes struct {
	keys []string
	fail string
	// errs answers a delete of bucket+"/"+key with the error it holds.
	errs map[string]error
}

func (d *deletes) DeleteObject(_ context.Context, bucket, key string) error {
	if key == d.fail {
		return errors.New("denied")
	}
	if err := d.errs[bucket+"/"+key]; err != nil {
		return err
	}
	d.keys = append(d.keys, bucket+"/"+key)
	return nil
}

// s3Error is a delete refused the way the S3 client reports it: an API error
// with the store's code, wrapped by each layer above.
func s3Error(code string) error {
	return fmt.Errorf("s3 delete: failed to delete object: %w",
		&smithy.GenericAPIError{Code: code, Message: "refused"})
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
	got := assetObjects(assetRow{"b", "p/a/content.md", "p/a/.thumbnail.png", "", []string{"b"}, []string{"p/a/content.md", "p/a/v2/content.md"}}, "b")
	seen := map[storedObject]bool{}
	for _, o := range got {
		assert.False(t, seen[o], "duplicate %v", o)
		seen[o] = true
	}
	assert.True(t, seen[storedObject{"b", "p/a/content.md"}])
}

// A row written before rows recorded a bucket (#1931) names its objects in
// the portal bucket, the asset row and each version row alike.
func TestAssetObjectsReadAnEmptyBucketAsThePortalBucket(t *testing.T) {
	key := "4acf7d36/b06163ce/content.html"
	got := assetObjects(assetRow{"", key, "", "", []string{""}, []string{key}}, "portal")
	require.NotEmpty(t, got)
	for _, o := range got {
		assert.Equal(t, "portal", o.bucket, o.key)
	}
	assert.Contains(t, got, storedObject{"portal", key})
}

// A delete the store answers with the object or its bucket already gone lets
// the row go; one it refuses for any other reason keeps it (#1931).
func TestPurge_AnObjectAlreadyGoneLetsTheRowGo(t *testing.T) {
	const key = "legacy/a1/content.html"
	for name, tc := range map[string]struct {
		bucket  string
		err     error
		removed bool
	}{
		"no such key":                         {"portal", s3Error("NoSuchKey"), true},
		"no such bucket, not the portal's":    {"retired", s3Error("NoSuchBucket"), true},
		"no such bucket, the portal's own":    {"portal", s3Error("NoSuchBucket"), false},
		"access denied":                       {"portal", s3Error("AccessDenied"), false},
		"a failure that is not the store's":   {"portal", errors.New("connection reset"), false},
		"no such key, in an empty-bucket row": {"", s3Error("NoSuchKey"), true},
	} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			expectAssetRows(mock, []any{"a1", tc.bucket, key, "", "", pq.StringArray{}, pq.StringArray{}})
			if tc.removed {
				mock.ExpectBegin()
				for _, q := range []string{purgeAssetSharesQuery, purgeAssetItemsQuery, purgeAssetThreadsQuery, purgeAssetProducersQuery} {
					mock.ExpectExec(regexp.QuoteMeta(q)).WillReturnResult(sqlmock.NewResult(0, 0))
				}
				mock.ExpectExec(regexp.QuoteMeta(purgeAssetsQuery)).WithArgs(pq.Array([]string{"a1"}), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			expectEmptyTail(mock)

			bucket := tc.bucket
			if bucket == "" {
				bucket = "portal"
			}
			objects := &deletes{errs: map[string]error{bucket + "/" + key: tc.err}}
			res, err := NewPurger(db, objects, "portal").Purge(context.Background(), time.Now())
			require.NoError(t, err)
			assert.Equal(t, tc.removed, res.Assets == 1, "row removed")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

package portalstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
)

// The statements themselves are held to PostgreSQL by the real-database suite
// (thumbnail_claim_realdb_integration_test.go) and the SQL gate. What is held
// here is the Go around them: what is bound where, what comes back, and what a
// failure at each step reports.

var errClaimDB = errors.New("db down")

func claimAssets(t *testing.T, db *sql.DB) *postgresAssetStore {
	t.Helper()
	s, ok := NewPostgresAssetStore(db, nil).(*postgresAssetStore)
	require.True(t, ok)
	return s
}

func claimCollections(t *testing.T, db *sql.DB) *postgresCollectionStore {
	t.Helper()
	s, ok := NewPostgresCollectionStore(db, nil).(*postgresCollectionStore)
	require.True(t, ok)
	return s
}

func TestBuildThumbnailClaim_BindsTheLeaseFirstAndTheLimitLast(t *testing.T) {
	stmt, args, err := buildThumbnailClaim(1, 2*time.Minute, 4)
	require.NoError(t, err)
	require.NotEmpty(t, args)
	assert.InDelta(t, 120.0, args[0], 0, "the lease is $1, in seconds")
	assert.Equal(t, 4, args[len(args)-1], "the limit is the last placeholder")
	assert.Contains(t, stmt, "FOR UPDATE SKIP LOCKED")
	assert.Contains(t, stmt, "RETURNING "+strings.Join(assetListColumns(), ", "))
	assert.NotContains(t, stmt, "?", "every placeholder is numbered")
}

func TestClaimThumbnailWork_ReturnsTheClaimedAssets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	rows := sqlmock.NewRows(assetSearchCols)
	addAssetRow(rows, "a1", "Deck")
	addAssetRow(rows, "a2", "Report")
	mock.ExpectQuery("UPDATE portal_assets SET thumbnail_claimed_until").WillReturnRows(rows)

	got, err := claimAssets(t, db).ClaimThumbnailWork(context.Background(), 1, time.Minute, 4)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a1", got[0].ID)
	assert.Equal(t, "Report", got[1].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimThumbnailWork_NothingIsClaimedForNoRoom(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	got, err := claimAssets(t, db).ClaimThumbnailWork(context.Background(), 1, time.Minute, 0)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet(), "no statement runs")
}

func TestClaimThumbnailWork_EachFailureIsReported(t *testing.T) {
	for _, tt := range []struct {
		name   string
		expect func(sqlmock.Sqlmock)
		want   string
	}{
		{"the claim", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("UPDATE portal_assets").WillReturnError(errClaimDB)
		}, "claiming thumbnail work"},
		{"a row", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("UPDATE portal_assets").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a1"))
		}, "scanning claimed asset"},
		{"the cursor", func(m sqlmock.Sqlmock) {
			rows := sqlmock.NewRows(assetSearchCols)
			addAssetRow(rows, "a1", "Deck")
			m.ExpectQuery("UPDATE portal_assets").WillReturnRows(rows.RowError(0, errClaimDB))
		}, "reading claimed assets"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close() //nolint:errcheck // test cleanup
			tt.expect(mock)

			_, err = claimAssets(t, db).ClaimThumbnailWork(context.Background(), 1, time.Minute, 4)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestClaimCollectionThumbnailWork_ReturnsEachWithItsSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("WITH member_tiles").
		WithArgs(120.0, 4).
		WillReturnRows(sqlmock.NewRows([]string{"id", "thumbnail_s3_key", "source"}).
			AddRow("c1", "", "a1:2:1,a2:1:1").
			AddRow("c2", "portal/collections/c2/thumbnail.png", ""))

	got, err := claimCollections(t, db).ClaimCollectionThumbnailWork(context.Background(), 2*time.Minute, 4)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a1:2:1,a2:1:1", got[0].Source)
	assert.Equal(t, "portal/collections/c2/thumbnail.png", got[1].ThumbnailS3Key)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimCollectionThumbnailWork_Failures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := claimCollections(t, db)

	got, err := store.ClaimCollectionThumbnailWork(context.Background(), time.Minute, 0)
	require.NoError(t, err)
	assert.Nil(t, got, "no room claims nothing")

	mock.ExpectQuery("WITH member_tiles").WillReturnError(errClaimDB)
	_, err = store.ClaimCollectionThumbnailWork(context.Background(), time.Minute, 4)
	require.ErrorContains(t, err, "claiming collection thumbnail work")

	mock.ExpectQuery("WITH member_tiles").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("c1"))
	_, err = store.ClaimCollectionThumbnailWork(context.Background(), time.Minute, 4)
	require.ErrorContains(t, err, "scanning claimed collection")

	mock.ExpectQuery("WITH member_tiles").WillReturnRows(
		sqlmock.NewRows([]string{"id", "thumbnail_s3_key", "source"}).AddRow("c1", "", "").RowError(0, errClaimDB))
	_, err = store.ClaimCollectionThumbnailWork(context.Background(), time.Minute, 4)
	require.ErrorContains(t, err, "reading claimed collections")
}

// Recording a mosaic ends the lease and leaves the collection's date alone.
func TestRecordCollectionThumbnail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := claimCollections(t, db)

	mock.ExpectExec("UPDATE portal_collections\\s+SET thumbnail_s3_key = \\$1, thumbnail_source = \\$2, thumbnail_claimed_until = NULL").
		WithArgs("portal/collections/c1/thumbnail.png", "a1:2:1", "c1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.RecordCollectionThumbnail(context.Background(), "c1", "portal/collections/c1/thumbnail.png", "a1:2:1"))

	mock.ExpectExec("UPDATE portal_collections").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorContains(t, store.RecordCollectionThumbnail(context.Background(), "gone", "", ""), "not found")

	mock.ExpectExec("UPDATE portal_collections").WillReturnError(errClaimDB)
	require.ErrorContains(t, store.RecordCollectionThumbnail(context.Background(), "c1", "", ""), "recording collection thumbnail")

	assert.NotContains(t, recordCollectionThumbnailQuery, "updated_at")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The source bound is per family, and the raise reaches only the family that
// earned it: a scanned PDF passes the default bound in a couple of pages, and
// every other family is still laid out in full to be drawn (#1794).
func TestBuildThumbnailClaim_BoundsTheSourceSizePerFamily(t *testing.T) {
	stmt, args, err := buildThumbnailClaim(1, time.Minute, 4)
	require.NoError(t, err)

	// squirrel renumbers the "?" the expression is written with, so the
	// statement is matched on the shape either side of the placeholder.
	assert.Regexp(t,
		`size_bytes <= CASE WHEN content_type ILIKE ANY\(\$\d+\) THEN 33554432::bigint ELSE 1048576::bigint END`,
		stmt)

	large, err := pq.Array(thumbtypes.ILikePatterns(thumbtypes.LargeSourceFamilies)).Value()
	require.NoError(t, err)
	var bound bool
	for _, a := range args {
		v, ok := a.(driver.Valuer)
		if !ok {
			continue
		}
		got, verr := v.Value()
		require.NoError(t, verr)
		if got == large {
			bound = true
		}
	}
	assert.True(t, bound, "the large-source families are bound to the CASE's placeholder")
}

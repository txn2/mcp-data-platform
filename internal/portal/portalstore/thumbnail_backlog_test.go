package portalstore

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetThumbnailBacklog(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	query, _, err := buildThumbnailBacklog(1)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(query), "SELECT"))
	store := &postgresAssetStore{db: db}

	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnRows(sqlmock.NewRows([]string{"p", "w"}).AddRow(3, 1))
	pending, waiting, err := store.ThumbnailBacklog(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), pending)
	assert.Equal(t, int64(1), waiting)

	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnError(errors.New("down"))
	_, _, err = store.ThumbnailBacklog(context.Background(), 1)
	assert.ErrorContains(t, err, "counting thumbnail work")
}

// TestCollectionThumbnailBacklog_SharesTheClaimsConditions: the backlog and
// the claim read one mosaic source and one owed condition (#1897).
func TestCollectionThumbnailBacklog_SharesTheClaimsConditions(t *testing.T) {
	claim, backlog := buildCollectionThumbnailClaim(), collectionThumbnailBacklog()
	for _, part := range []string{collectionMosaicSources(), collectionMosaicOwed} {
		assert.Contains(t, claim, part)
		assert.Contains(t, backlog, part)
	}

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}
	mock.ExpectQuery(regexp.QuoteMeta(backlog)).WillReturnRows(sqlmock.NewRows([]string{"p", "w"}).AddRow(2, 0))
	pending, waiting, err := store.CollectionThumbnailBacklog(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), pending)
	assert.Zero(t, waiting)

	mock.ExpectQuery(regexp.QuoteMeta(backlog)).WillReturnError(errors.New("down"))
	_, _, err = store.CollectionThumbnailBacklog(context.Background())
	assert.ErrorContains(t, err, "counting collection thumbnail work")
}

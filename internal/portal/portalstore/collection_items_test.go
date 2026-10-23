package portalstore

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// The collection store answers the add-to-collection picker (#2260) by
// handing its own database to internal/portal/collitems.
func TestCollectionStore_AnswersTheItemAppender(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test
	store, ok := NewPostgresCollectionStore(db, nil).(portaldomain.CollectionItemAppender)
	require.True(t, ok)

	mock.ExpectQuery("FROM portal_collection_sections s").
		WillReturnRows(sqlmock.NewRows([]string{"collection_id", "id", "title", "count", "has"}).AddRow("c1", "s1", "Summary", 1, true))
	got, err := store.SectionSummaries(context.Background(), []string{"c1"}, "a1")
	require.NoError(t, err)
	assert.True(t, got["c1"][0].HasAsset)

	mock.ExpectBegin().WillReturnError(assert.AnError)
	_, err = store.AppendItem(context.Background(), "c1", "s1", "a1")
	assert.ErrorContains(t, err, "beginning transaction")
	require.NoError(t, mock.ExpectationsWereMet())
}

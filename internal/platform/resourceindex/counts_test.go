package resourceindex

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCounts(t *testing.T) {
	store, mock := newDB(t)
	mock.ExpectQuery("SELECT\\s+COUNT\\(\\*\\) FILTER \\(WHERE index_policy = 'full'\\)").
		WillReturnRows(sqlmock.NewRows([]string{"full", "metadata", "none", "explicit", "bytes", "folders"}).
			AddRow(800, 15622, 6485, 1500, int64(1024), 3))
	c, err := store.Counts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, PolicyCounts{Full: 800, Metadata: 15622, None: 6485, Explicit: 1500, ContentBytes: 1024, Folders: 3}, c)

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db down"))
	_, err = store.Counts(context.Background())
	assert.Error(t, err)
}

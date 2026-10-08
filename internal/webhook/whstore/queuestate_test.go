package whstore

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactionState(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(compactionStateQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"p", "r", "w", "o"}).AddRow(5, 1, 3, 120.0))
	st, err := s.CompactionState(context.Background())
	require.NoError(t, err)
	assert.Equal(t, CompactionState{Pending: 5, Running: 1, Waiting: 3, OldestOwed: 2 * time.Minute}, st)

	mock.ExpectQuery(regexp.QuoteMeta(compactionStateQuery)).WillReturnError(errors.New("down"))
	_, err = s.CompactionState(context.Background())
	assert.ErrorContains(t, err, "compaction backlog")
}

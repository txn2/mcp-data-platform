package scriptstore

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

func TestRunQueueState_ScansTheCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery(regexp.QuoteMeta(runQueueStateQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"p", "r", "w", "o"}).AddRow(3, 1, 2, 12.5))
	st, err := New(db).RunQueueState(context.Background())
	require.NoError(t, err)
	assert.Equal(t, QueueState{Pending: 3, Running: 1, Waiting: 2, OldestDue: 12500 * time.Millisecond}, st)

	mock.ExpectQuery(regexp.QuoteMeta(runQueueStateQuery)).WillReturnError(errors.New("down"))
	_, err = New(db).RunQueueState(context.Background())
	assert.ErrorContains(t, err, "reading the run queue state")
}

func TestDueScheduleState_ScansTheCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery(regexp.QuoteMeta(dueScheduleStateQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"n", "o"}).AddRow(2, 60.0))
	due, oldest, err := New(db).DueScheduleState(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), due)
	assert.Equal(t, time.Minute, oldest)

	mock.ExpectQuery(regexp.QuoteMeta(dueScheduleStateQuery)).WillReturnError(errors.New("down"))
	_, _, err = New(db).DueScheduleState(context.Background())
	assert.ErrorContains(t, err, "reading the due schedules")
}

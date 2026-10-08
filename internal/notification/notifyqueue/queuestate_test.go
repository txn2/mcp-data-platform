package notifyqueue

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

func TestQueueState_ReportsBothKindsWithZerosForAnEmptyOne(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(queueStateQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "p", "s", "w", "o"}).AddRow("email", 4, 1, 2, 30.0))
	got, err := s.QueueState(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []KindState{
		{Kind: KindEmail, Pending: 4, Sending: 1, Waiting: 2, OldestDue: 30 * time.Second},
		{Kind: KindChannel},
	}, got)
}

func TestQueueState_Errors(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(queueStateQuery)).WillReturnError(errors.New("down"))
	_, err := s.QueueState(context.Background())
	assert.ErrorContains(t, err, "reading the notification queue state")

	mock.ExpectQuery(regexp.QuoteMeta(queueStateQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "p", "s", "w", "o"}).AddRow("email", "x", 1, 2, 30.0))
	_, err = s.QueueState(context.Background())
	assert.ErrorContains(t, err, "scanning")

	mock.ExpectQuery(regexp.QuoteMeta(queueStateQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "p", "s", "w", "o"}).AddRow("email", 1, 1, 2, 30.0).RowError(0, errors.New("cut")))
	_, err = s.QueueState(context.Background())
	assert.Error(t, err)
}

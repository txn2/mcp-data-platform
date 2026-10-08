package scripttiles

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBacklog(t *testing.T) {
	s, mock := newStore(t)
	mock.ExpectQuery(regexp.QuoteMeta(backlogSQL)).WithArgs(3).
		WillReturnRows(sqlmock.NewRows([]string{"p", "w"}).AddRow(4, 1))
	pending, waiting, err := s.Backlog(context.Background(), 3)
	require.NoError(t, err)
	assert.Equal(t, int64(4), pending)
	assert.Equal(t, int64(1), waiting)

	mock.ExpectQuery(regexp.QuoteMeta(backlogSQL)).WillReturnError(errors.New("down"))
	_, _, err = s.Backlog(context.Background(), 3)
	assert.ErrorContains(t, err, "counting script tiles owed")
}

package resource

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCountMounting(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := &postgresStore{db: db}
	mock.ExpectQuery("JOIN site_media sm").WithArgs("res_1", 50).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	n, err := s.CountMounting(context.Background(), "res_1", 50)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	mock.ExpectQuery("JOIN site_media sm").WillReturnError(errors.New("down"))
	_, err = s.CountMounting(context.Background(), "res_1", 50)
	require.ErrorContains(t, err, "sites serving a file")
}

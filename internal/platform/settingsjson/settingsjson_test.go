package settingsjson

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type value struct {
	Channels []string `json:"channels"`
}

func TestSection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := New[value](db, "storage")
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = \$1`).
		WithArgs("storage").WillReturnError(sql.ErrNoRows)
	_, err = s.Get(ctx)
	require.ErrorIs(t, err, ErrNotFound)

	mock.ExpectQuery(`SELECT value, updated_by, updated_at FROM platform_settings`).
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow([]byte(`{"channels":["ops"]}`), "admin", at))
	got, err := s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, Stored[value]{Value: value{Channels: []string{"ops"}}, UpdatedBy: "admin", UpdatedAt: at}, got)

	mock.ExpectQuery(`SELECT value`).
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow([]byte(`not json`), "admin", at))
	_, err = s.Get(ctx)
	require.Error(t, err)

	mock.ExpectQuery(`SELECT value`).WillReturnError(errors.New("down"))
	_, err = s.Get(ctx)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)

	mock.ExpectExec(`INSERT INTO platform_settings`).
		WithArgs("storage", []byte(`{"channels":["ops"]}`), "admin").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Set(ctx, value{Channels: []string{"ops"}}, "admin"))
	mock.ExpectExec(`INSERT INTO platform_settings`).WillReturnError(errors.New("down"))
	require.Error(t, s.Set(ctx, value{}, "admin"))
	require.NoError(t, mock.ExpectationsWereMet())

	bad := New[chan int](db, "x")
	require.Error(t, bad.Set(ctx, make(chan int), "admin"), "a value JSON cannot encode is refused")
}

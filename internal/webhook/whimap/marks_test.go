package whimap

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

func TestMarks(t *testing.T) {
	assert.Nil(t, NewMarks(nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test
	m := NewMarks(db)
	ctx := context.Background()
	cols := []string{"uid_validity", "last_uid", "stored", "hidden_not_allowed", "hidden_unauthenticated", "last_polled_at", "last_error"}
	now := time.Now()

	mock.ExpectQuery("FROM webhook_imap_marks WHERE source").WillReturnError(sql.ErrNoRows)
	got, err := m.Get(ctx, "s", "Platform")
	require.NoError(t, err)
	assert.Equal(t, Mark{Source: "s", Folder: "Platform"}, got, "a folder never read starts at zero")

	mock.ExpectQuery("FROM webhook_imap_marks WHERE source").WillReturnRows(sqlmock.NewRows(cols).AddRow(7, 42, 3, 1, 2, now, "boom"))
	got, err = m.Get(ctx, "s", "Platform")
	require.NoError(t, err)
	assert.EqualValues(t, 42, got.LastUID)
	assert.EqualValues(t, 2, got.HiddenUnauthenticated)
	assert.Equal(t, "boom", got.LastError)

	mock.ExpectQuery("FROM webhook_imap_marks WHERE source").WillReturnError(errors.New("db down"))
	_, err = m.Get(ctx, "s", "Platform")
	assert.ErrorContains(t, err, "reading the imap mark")

	mock.ExpectExec("INSERT INTO webhook_imap_marks").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, m.Put(ctx, got))
	mock.ExpectExec("INSERT INTO webhook_imap_marks").WillReturnError(errors.New("db down"))
	assert.ErrorContains(t, m.Put(ctx, got), "writing the imap mark")

	mock.ExpectQuery("ORDER BY folder").WillReturnRows(sqlmock.NewRows(append([]string{"folder"}, cols...)).AddRow("Platform", 7, 42, 3, 1, 2, nil, ""))
	list, err := m.List(ctx, "s")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Nil(t, list[0].LastPolledAt)

	mock.ExpectQuery("ORDER BY folder").WillReturnError(errors.New("db down"))
	_, err = m.List(ctx, "s")
	assert.ErrorContains(t, err, "listing the imap marks")

	mock.ExpectQuery("ORDER BY folder").WillReturnRows(sqlmock.NewRows([]string{"folder"}).AddRow("x"))
	_, err = m.List(ctx, "s")
	assert.ErrorContains(t, err, "scanning an imap mark")

	mock.ExpectQuery("ORDER BY folder").WillReturnRows(sqlmock.NewRows(append([]string{"folder"}, cols...)).
		AddRow("Platform", 7, 42, 3, 1, 2, nil, "").RowError(0, errors.New("db down")))
	_, err = m.List(ctx, "s")
	assert.ErrorContains(t, err, "iterating imap marks")

	mb := NewMailbox(m)
	mock.ExpectQuery("ORDER BY folder").WillReturnRows(sqlmock.NewRows(append([]string{"folder"}, cols...)))
	marks, err := mb.Marks(ctx, "s")
	require.NoError(t, err)
	assert.Empty(t, marks)
	require.NoError(t, mock.ExpectationsWereMet())
}

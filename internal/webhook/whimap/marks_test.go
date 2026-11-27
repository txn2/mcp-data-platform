package whimap

import (
	"context"
	"database/sql"
	"encoding/json"
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

	mb := NewMailbox(m, nil)
	none, err := mb.Repair(context.Background(), "s")
	require.NoError(t, err)
	assert.Nil(t, none, "no store, no repair")
	mock.ExpectQuery("ORDER BY folder").WillReturnRows(sqlmock.NewRows(append([]string{"folder"}, cols...)))
	marks, err := mb.Marks(ctx, "s")
	require.NoError(t, err)
	assert.Empty(t, marks)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepairsStore(t *testing.T) {
	assert.Nil(t, NewRepairs(nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test
	r := NewRepairs(db)
	ctx := context.Background()

	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnError(sql.ErrNoRows)
	done, err := r.Repaired(ctx, "inbox")
	require.NoError(t, err)
	assert.False(t, done)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnRows(sqlmock.NewRows([]string{"one"}).AddRow(1))
	done, err = r.Repaired(ctx, "inbox")
	require.NoError(t, err)
	assert.True(t, done)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnError(errors.New("down"))
	_, err = r.Repaired(ctx, "inbox")
	require.Error(t, err)

	rep := RepairReport{Moved: []FolderMove{{From: "a", To: "b"}}, Removed: []RemovedFolder{{Folder: "c", Files: 3}}, Kept: []string{}}
	mock.ExpectExec("INSERT INTO webhook_imap_repairs").WithArgs("inbox", 1, 1, 3, 0, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, r.RecordRepair(ctx, "inbox", rep))
	mock.ExpectExec("INSERT INTO webhook_imap_repairs").WillReturnError(errors.New("down"))
	require.Error(t, r.RecordRepair(ctx, "inbox", rep))

	cols := []string{"repaired_at", "moved", "removed", "removed_files", "kept", "report"}
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnError(sql.ErrNoRows)
	none, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Nil(t, none)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(time.Now(), 1, 1, 3, 0, []byte(`{"moved":[{"from":"a","to":"b"}],"removed":[{"folder":"c","files":3}],"kept":[]}`)))
	got, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, rep, got.Report)
	long := RepairReport{Kept: make([]string, 150)}
	for i := range long.Kept {
		long.Kept[i] = "k"
	}
	body, err := json.Marshal(long)
	require.NoError(t, err)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnRows(sqlmock.NewRows(cols).AddRow(time.Now(), 0, 0, 0, 150, body))
	got, err = r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, 150, got.Kept, "the count is the whole")
	assert.Len(t, got.Report.Kept, repairListLimit, "the list read is the first hundred")
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnRows(sqlmock.NewRows(cols).AddRow(time.Now(), 0, 0, 0, 0, []byte(`{`)))
	_, err = r.Get(ctx, "inbox")
	require.Error(t, err)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnError(errors.New("down"))
	_, err = r.Get(ctx, "inbox")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

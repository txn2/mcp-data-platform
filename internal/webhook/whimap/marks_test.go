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

	mock.ExpectQuery("SELECT version FROM webhook_imap_repairs WHERE source").WillReturnError(sql.ErrNoRows)
	version, err := r.RepairedVersion(ctx, "inbox")
	require.NoError(t, err)
	assert.Zero(t, version)
	mock.ExpectQuery("SELECT version FROM webhook_imap_repairs WHERE source").WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(1))
	version, err = r.RepairedVersion(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, 1, version)
	mock.ExpectQuery("SELECT version FROM webhook_imap_repairs WHERE source").WillReturnError(errors.New("down"))
	_, err = r.RepairedVersion(ctx, "inbox")
	require.Error(t, err)

	rep := RepairReport{
		Moved: []FolderMove{{From: "a", To: "b"}}, Removed: []RemovedFolder{{Folder: "c", Files: 3}},
		Collapsed: []RemovedFolder{{Folder: "d", Files: 2}, {Folder: "e", Files: 1}}, Kept: []string{},
	}
	mock.ExpectExec("INSERT INTO webhook_imap_repairs").WithArgs("inbox", 2, 1, 1, 3, 2, 3, 0, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, r.RecordRepair(ctx, "inbox", 2, rep))
	mock.ExpectExec("INSERT INTO webhook_imap_repairs").WillReturnError(errors.New("down"))
	require.Error(t, r.RecordRepair(ctx, "inbox", 2, rep))

	cols := []string{"repaired_at", "moved", "removed", "removed_files", "collapsed", "collapsed_files", "kept", "report"}
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnError(sql.ErrNoRows)
	none, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Nil(t, none)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(time.Now(), 1, 1, 3, 2, 3, 0, []byte(`{"moved":[{"from":"a","to":"b"}],"removed":[{"folder":"c","files":3}],"collapsed":[{"folder":"d","files":2},{"folder":"e","files":1}],"kept":[]}`)))
	got, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, rep, got.Report)
	assert.Equal(t, 2, got.Collapsed)
	assert.Equal(t, 3, got.CollapsedFiles)
	// A report version 1 wrote has no collapsed list: read as empty, not null.
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(time.Now(), 1, 0, 0, 0, 0, 0, []byte(`{"moved":[{"from":"a","to":"b"}],"removed":[],"kept":[]}`)))
	got, err = r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.NotNil(t, got.Report.Collapsed)
	assert.Empty(t, got.Report.Collapsed)
	long := RepairReport{Kept: make([]string, 150)}
	for i := range long.Kept {
		long.Kept[i] = "k"
	}
	body, err := json.Marshal(long)
	require.NoError(t, err)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnRows(sqlmock.NewRows(cols).AddRow(time.Now(), 0, 0, 0, 0, 0, 150, body))
	got, err = r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, 150, got.Kept, "the count is the whole")
	assert.Len(t, got.Report.Kept, repairListLimit, "the list read is the first hundred")
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnRows(sqlmock.NewRows(cols).AddRow(time.Now(), 0, 0, 0, 0, 0, 0, []byte(`{`)))
	_, err = r.Get(ctx, "inbox")
	require.Error(t, err)
	mock.ExpectQuery("FROM webhook_imap_repairs WHERE source").WillReturnError(errors.New("down"))
	_, err = r.Get(ctx, "inbox")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

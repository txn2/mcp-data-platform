package assetstatus

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMock(t *testing.T) (*Postgres, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func TestChangeNormalize(t *testing.T) {
	cases := map[string]struct {
		in      Change
		want    Change
		wantErr string
	}{
		"trims and lowers": {
			in:   Change{Status: "  Deprecated ", Note: "  replaced by Q4 ", Actor: "a@x"},
			want: Change{Status: "deprecated", Note: "replaced by Q4", Actor: "a@x"},
		},
		"unknown status":  {in: Change{Status: "archived", Actor: "a@x"}, wantErr: "draft, published or deprecated"},
		"none is not set": {in: Change{Status: "none", Actor: "a@x"}, wantErr: "draft, published or deprecated"},
		"note too long":   {in: Change{Status: "draft", Note: strings.Repeat("n", 501), Actor: "a@x"}, wantErr: "longer than 500"},
		"no actor":        {in: Change{Status: "draft", Actor: "  "}, wantErr: "no actor"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := tc.in.Normalize()
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalid)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSetWritesTheStatusAndRecordsTheChange(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(lockStatusSQL)).WithArgs("a1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "note"}).AddRow("draft", ""))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_status")).
		WithArgs("a1", "deprecated", "see Q4", "alice@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).
		WithArgs("a1", KindStatus, "draft", "deprecated", "see Q4", "alice@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := store.Set(context.Background(), "a1", Change{Status: "Deprecated", Note: "see Q4", Actor: "alice@example.com"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetWithNoPriorStatusRecordsFromEmpty(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(lockStatusSQL)).WithArgs("a1").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_status")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).
		WithArgs("a1", KindStatus, "", "draft", "", "alice@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Set(context.Background(), "a1", Change{Status: "draft", Actor: "alice@example.com"}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetTheSameStatusWritesNothing(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(lockStatusSQL)).WithArgs("a1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "note"}).AddRow("published", "final"))
	mock.ExpectCommit()

	require.NoError(t, store.Set(context.Background(), "a1", Change{Status: "published", Note: "final", Actor: "a@x"}))
	require.NoError(t, mock.ExpectationsWereMet(), "no upsert and no event for an unchanged status")
}

func TestSetRefusesAnInvalidChangeBeforeTouchingTheDatabase(t *testing.T) {
	store, mock := newMock(t)
	err := store.Set(context.Background(), "a1", Change{Status: "gone", Actor: "a@x"})
	require.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetRollsBackOnAFailedWrite(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(lockStatusSQL)).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	err := store.Set(context.Background(), "a1", Change{Status: "draft", Actor: "a@x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading status")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(lockStatusSQL)).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_status")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	err = store.Set(context.Background(), "a1", Change{Status: "draft", Actor: "a@x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "setting status")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetReportsBeginAndCommitFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("no conn"))
	err := store.Set(context.Background(), "a1", Change{Status: "draft", Actor: "a@x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting transaction")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(lockStatusSQL)).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_status")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
	err = store.Set(context.Background(), "a1", Change{Status: "draft", Actor: "a@x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "committing")
}

func TestClearRemovesTheStatusAndRecordsIt(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteStatusSQL)).WithArgs("a1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("deprecated"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).
		WithArgs("a1", KindStatus, "deprecated", "", "", "bob@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	had, err := store.Clear(context.Background(), "a1", "bob@example.com")
	require.NoError(t, err)
	assert.True(t, had)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClearWithNoStatusRecordsNothing(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteStatusSQL)).WithArgs("a1").WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	had, err := store.Clear(context.Background(), "a1", "bob@example.com")
	require.NoError(t, err)
	assert.False(t, had)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClearReportsAFailedDelete(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(deleteStatusSQL)).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err := store.Clear(context.Background(), "a1", "bob@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clearing status")
}

func TestHistoryReadsNewestFirstAndClampsTheLimit(t *testing.T) {
	at := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		asked, sent int
	}{{0, maxHistory}, {-3, maxHistory}, {10_000, maxHistory}, {25, 25}} {
		store, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_mark_events")).WithArgs("a1", tc.sent).
			WillReturnRows(sqlmock.NewRows([]string{"kind", "from_value", "to_value", "note", "actor", "at"}).
				AddRow(KindVerification, "", "verified", "checked", "carol@example.com", at).
				AddRow(KindStatus, "draft", "published", "", "alice@example.com", at.Add(-time.Hour)))
		events, err := store.History(context.Background(), "a1", tc.asked)
		require.NoError(t, err)
		require.Len(t, events, 2)
		assert.Equal(t, Event{Kind: KindVerification, To: "verified", Note: "checked", Actor: "carol@example.com", At: at}, events[0])
		assert.Equal(t, "published", events[1].To)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestHistoryEmptyIsAnEmptyList(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_mark_events")).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "from_value", "to_value", "note", "actor", "at"}))
	events, err := store.History(context.Background(), "a1", 5)
	require.NoError(t, err)
	assert.NotNil(t, events)
	assert.Empty(t, events)
}

func TestHistoryReportsQueryAndScanFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_mark_events")).WillReturnError(errors.New("boom"))
	_, err := store.History(context.Background(), "a1", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading history")

	store, mock = newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_mark_events")).
		WillReturnRows(sqlmock.NewRows([]string{"kind"}).AddRow("status"))
	_, err = store.History(context.Background(), "a1", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scanning history")

	store, mock = newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_mark_events")).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "from_value", "to_value", "note", "actor", "at"}).
			AddRow("status", "", "draft", "", "a", time.Now()).RowError(0, errors.New("row broke")))
	_, err = store.History(context.Background(), "a1", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iterating history")
}

func TestRecordEventNamesTheKindOnFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_mark_events")).WillReturnError(errors.New("boom"))
	err = RecordEvent(context.Background(), db, "a1", Event{Kind: KindVerification, To: "verified", Actor: "c"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recording verification change")
}

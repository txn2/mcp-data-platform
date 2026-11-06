package threads

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

var eventColumns = []string{
	"id", "thread_id", "event_type", "author_id", "author_email", "body", "rating",
	"parent_event_id", "metadata", "created_at", "edited_at", "deleted_at",
}

func lockRows(eventType string, deleted bool) *sqlmock.Rows {
	var deletedAt any
	if deleted {
		deletedAt = time.Now()
	}
	return sqlmock.NewRows([]string{"event_type", "body", "metadata", "deleted_at"}).
		AddRow(eventType, "old text", []byte(`{"mentions":["a@x.io"]}`), deletedAt)
}

func TestGetEvent(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()
	mock.ExpectQuery("FROM portal_thread_events WHERE thread_id = \\$1 AND id = \\$2").
		WithArgs("thr_1", "evt_1").
		WillReturnRows(sqlmock.NewRows(eventColumns).
			AddRow("evt_1", "thr_1", EventTypeComment, "u1", "u1@x.io", nil, nil, nil, []byte(`{}`), now, nil, now))
	got, err := store.GetEvent(context.Background(), "thr_1", "evt_1")
	require.NoError(t, err)
	assert.Empty(t, got.Body, "a deleted message reads without its text")
	require.NotNil(t, got.DeletedAt)

	mock.ExpectQuery("FROM portal_thread_events").WillReturnError(sql.ErrNoRows)
	_, err = store.GetEvent(context.Background(), "thr_1", "missing")
	assert.ErrorIs(t, err, ErrEventNotFound)

	mock.ExpectQuery("FROM portal_thread_events").WillReturnError(errors.New("boom"))
	_, err = store.GetEvent(context.Background(), "thr_1", "x")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrEventNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEditEvent_KeepsThePreviousText(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type, body, metadata, deleted_at FROM portal_thread_events").
		WithArgs("thr_1", "evt_1").WillReturnRows(lockRows(EventTypeComment, false))
	mock.ExpectExec("INSERT INTO portal_thread_event_revisions").
		WithArgs("evt_1", "old text", []byte(`{"mentions":["a@x.io"]}`), "u1@x.io").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("UPDATE portal_thread_events SET body").
		WithArgs("thr_1", "evt_1", sql.NullString{String: "new text", Valid: true}, []byte(`{}`)).
		WillReturnRows(sqlmock.NewRows(eventColumns).
			AddRow("evt_1", "thr_1", EventTypeComment, "u1", "u1@x.io", "new text", nil, nil, []byte(`{}`), now, now, nil))
	mock.ExpectCommit()

	got, err := store.EditEvent(context.Background(), EventEdit{
		ThreadID: "thr_1", EventID: "evt_1", Body: "new text", EditorEmail: "u1@x.io",
	})
	require.NoError(t, err)
	assert.Equal(t, "new text", got.Body)
	require.NotNil(t, got.EditedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEditEvent_Refusals(t *testing.T) {
	cases := []struct {
		name string
		rows *sqlmock.Rows
		err  error
		want error
	}{
		{"decision", lockRows(EventTypeApproval, false), nil, ErrEventNotEditable},
		{"deleted", lockRows(EventTypeComment, true), nil, ErrEventDeleted},
		{"missing", nil, sql.ErrNoRows, ErrEventNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := newThreadStoreMock(t)
			mock.ExpectBegin()
			q := mock.ExpectQuery("SELECT event_type")
			if tc.rows != nil {
				q.WillReturnRows(tc.rows)
			} else {
				q.WillReturnError(tc.err)
			}
			mock.ExpectRollback()
			_, err := store.EditEvent(context.Background(), EventEdit{ThreadID: "t", EventID: "e", Body: "x"})
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestEditEvent_StoreFailures(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("no tx"))
	_, err := store.EditEvent(context.Background(), EventEdit{})
	assert.Error(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.EditEvent(context.Background(), EventEdit{})
	assert.Error(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnRows(lockRows(EventTypeComment, false))
	mock.ExpectExec("INSERT INTO portal_thread_event_revisions").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.EditEvent(context.Background(), EventEdit{})
	assert.ErrorContains(t, err, "previous text")

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnRows(lockRows(EventTypeComment, false))
	mock.ExpectExec("INSERT INTO portal_thread_event_revisions").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("UPDATE portal_thread_events").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.EditEvent(context.Background(), EventEdit{})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteEvent(t *testing.T) {
	for _, lone := range []bool{true, false} {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT event_type").WillReturnRows(lockRows(EventTypeComment, false))
		mock.ExpectExec("UPDATE portal_thread_events SET deleted_at").
			WithArgs("thr_1", "evt_1", "mod@x.io").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT \\(SELECT id FROM portal_thread_events").
			WithArgs("thr_1", "evt_1", EventTypeStatusChange, EventTypeResolution, EventTypeVisibilityChange).
			WillReturnRows(sqlmock.NewRows([]string{"lone"}).AddRow(lone))
		if lone {
			mock.ExpectExec("UPDATE portal_threads SET deleted_at").WithArgs("thr_1").
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectCommit()
		got, err := store.DeleteEvent(context.Background(), "thr_1", "evt_1", "mod@x.io")
		require.NoError(t, err)
		assert.Equal(t, lone, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestDeleteEvent_Failures(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("no tx"))
	_, err := store.DeleteEvent(context.Background(), "t", "e", "a")
	assert.Error(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnRows(lockRows(EventTypeRating, false))
	mock.ExpectRollback()
	_, err = store.DeleteEvent(context.Background(), "t", "e", "a")
	assert.ErrorIs(t, err, ErrEventNotEditable)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnRows(lockRows(EventTypeComment, false))
	mock.ExpectExec("UPDATE portal_thread_events").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.DeleteEvent(context.Background(), "t", "e", "a")
	assert.Error(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnRows(lockRows(EventTypeComment, false))
	mock.ExpectExec("UPDATE portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\(SELECT id").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.DeleteEvent(context.Background(), "t", "e", "a")
	assert.ErrorContains(t, err, "replies")

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnRows(lockRows(EventTypeComment, false))
	mock.ExpectExec("UPDATE portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\(SELECT id").WillReturnRows(sqlmock.NewRows([]string{"lone"}).AddRow(true))
	mock.ExpectExec("UPDATE portal_threads").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err = store.DeleteEvent(context.Background(), "t", "e", "a")
	assert.ErrorContains(t, err, "deleting the thread")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestParticipants(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectQuery("UNION").WithArgs("thr_1").
		WillReturnRows(sqlmock.NewRows([]string{"addr"}).AddRow("b@x.io").AddRow(" ").AddRow("c@x.io"))
	got, err := store.Participants(context.Background(), "thr_1")
	require.NoError(t, err)
	assert.Equal(t, []string{"b@x.io", "c@x.io"}, got)

	mock.ExpectQuery("UNION").WillReturnRows(sqlmock.NewRows([]string{"addr"}))
	got, err = store.Participants(context.Background(), "thr_2")
	require.NoError(t, err)
	assert.NotNil(t, got, "no participants is an empty list, never null")

	mock.ExpectQuery("UNION").WillReturnError(errors.New("boom"))
	_, err = store.Participants(context.Background(), "thr_3")
	assert.Error(t, err)

	mock.ExpectQuery("UNION").WillReturnRows(sqlmock.NewRows([]string{"addr"}).AddRow(nil))
	_, err = store.Participants(context.Background(), "thr_4")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

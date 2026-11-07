package threads

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var undoEventColumns = []string{"event_type", "author_id", "author_email", "created_at", "metadata"}

func undoRequest(now time.Time) StatusUndo {
	return StatusUndo{ThreadID: "thr_1", EventID: "evt_1", ActorID: "u1", ActorEmail: "U1@x.io", Now: now}
}

func expectUndoEvent(mock sqlmock.Sqlmock, kind, authorID string, created time.Time, metadata string) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type, author_id, author_email, created_at, metadata FROM portal_thread_events").
		WithArgs("thr_1", "evt_1").
		WillReturnRows(sqlmock.NewRows(undoEventColumns).AddRow(kind, authorID, "u1@x.io", created, []byte(metadata)))
}

const ackChange = `{"old_status":"open","new_status":"acknowledged"}`

// TestUndoStatusChange_RestoresAndRemoves: the status the change replaced is
// restored and the line it wrote is removed, in one transaction.
func TestUndoStatusChange_RestoresAndRemoves(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()
	expectUndoEvent(mock, EventTypeStatusChange, "u1", now.Add(-time.Minute), ackChange)
	mock.ExpectQuery("SELECT t.status, EXISTS").WithArgs("thr_1", "evt_1", sqlmock.AnyArg(), "u1", "U1@x.io").
		WillReturnRows(sqlmock.NewRows([]string{"status", "later"}).AddRow("acknowledged", false))
	mock.ExpectExec("UPDATE portal_threads SET status = \\$2").WithArgs("thr_1", "open").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM portal_thread_events").WithArgs("thr_1", "evt_1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	restored, err := store.UndoStatusChange(context.Background(), undoRequest(now))
	require.NoError(t, err)
	assert.Equal(t, "open", restored)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUndoStatusChange_Refusals: each condition refuses, and nothing is
// changed.
func TestUndoStatusChange_Refusals(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		kind     string
		author   string
		created  time.Time
		metadata string
		status   string
		later    bool
		want     error
	}{
		{"a comment", EventTypeComment, "u1", now, ackChange, "", false, ErrUndoNotStatus},
		{"another person's change", EventTypeStatusChange, "u2", now, ackChange, "", false, nil},
		{"past the window", EventTypeResolution, "u1", now.Add(-11 * time.Minute), ackChange, "", false, ErrUndoExpired},
		{"metadata naming no status", EventTypeStatusChange, "u1", now, `{}`, "", false, ErrUndoNotStatus},
		{"someone else added since", EventTypeStatusChange, "u1", now, ackChange, "acknowledged", true, ErrUndoSuperseded},
		{"a later change replaced it", EventTypeStatusChange, "u1", now, ackChange, "resolved", false, ErrUndoSuperseded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, mock := newThreadStoreMock(t)
			mock.ExpectBegin()
			email := "u1@x.io"
			if c.author != "u1" {
				email = "u2@x.io"
			}
			mock.ExpectQuery("SELECT event_type, author_id, author_email").
				WillReturnRows(sqlmock.NewRows(undoEventColumns).AddRow(c.kind, c.author, email, c.created, []byte(c.metadata)))
			if c.status != "" {
				mock.ExpectQuery("SELECT t.status, EXISTS").
					WillReturnRows(sqlmock.NewRows([]string{"status", "later"}).AddRow(c.status, c.later))
			}
			mock.ExpectRollback()
			_, err := store.UndoStatusChange(context.Background(), undoRequest(now))
			want := c.want
			if want == nil {
				want = ErrUndoNotAuthor
			}
			require.ErrorIs(t, err, want)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUndoStatusChange_Failures(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_type").WillReturnRows(sqlmock.NewRows(undoEventColumns))
	mock.ExpectRollback()
	_, err := store.UndoStatusChange(context.Background(), undoRequest(time.Now()))
	require.ErrorIs(t, err, ErrEventNotFound)

	store, mock = newThreadStoreMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("down"))
	_, err = store.UndoStatusChange(context.Background(), undoRequest(time.Now()))
	require.Error(t, err)

	store, mock = newThreadStoreMock(t)
	now := time.Now()
	expectUndoEvent(mock, EventTypeStatusChange, "u1", now, ackChange)
	mock.ExpectQuery("SELECT t.status, EXISTS").WillReturnRows(sqlmock.NewRows([]string{"status", "later"}))
	mock.ExpectRollback()
	_, err = store.UndoStatusChange(context.Background(), undoRequest(now))
	require.ErrorIs(t, err, ErrEventNotFound, "a deleted thread")

	store, mock = newThreadStoreMock(t)
	expectUndoEvent(mock, EventTypeStatusChange, "u1", now, ackChange)
	mock.ExpectQuery("SELECT t.status, EXISTS").WillReturnRows(sqlmock.NewRows([]string{"status", "later"}).AddRow("acknowledged", false))
	mock.ExpectExec("UPDATE portal_threads").WillReturnError(errors.New("down"))
	mock.ExpectRollback()
	_, err = store.UndoStatusChange(context.Background(), undoRequest(now))
	require.Error(t, err)

	assert.True(t, sameActor("", "A@x.io", "z", "a@x.io"), "the address matches whatever its case")
	assert.False(t, sameActor("", "", "z", ""))
}

// The PostgreSQL store is what the undo route reaches through the interface.
var _ StatusUndoer = (*postgresThreadStore)(nil)

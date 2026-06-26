package threads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newThreadStoreMock(t *testing.T) (*postgresThreadStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &postgresThreadStore{db: db}, mock
}

func TestSearchThreads(t *testing.T) {
	t.Run("matches owner threads and maps rows", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		now := time.Now()
		rows := sqlmock.NewRows(threadColumnNames).
			AddRow("thr_1", "correction", "asset", nil, nil, nil, nil, nil, nil,
				"amount is gross", "u1", "a@example.com", "open", false, "none", nil, now, now, nil)
		mock.ExpectQuery("SELECT .* FROM portal_threads").
			WithArgs("a@example.com", "%amount%", 5).
			WillReturnRows(rows)

		got, err := store.SearchThreads(context.Background(), "a@example.com", "amount", 5)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "thr_1", got[0].ID)
		assert.Equal(t, "a@example.com", got[0].AuthorEmail)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty owner or blank intent never queries", func(t *testing.T) {
		store, _ := newThreadStoreMock(t)
		got, err := store.SearchThreads(context.Background(), "", "amount", 5)
		require.NoError(t, err)
		assert.Nil(t, got)
		got, err = store.SearchThreads(context.Background(), "a@example.com", "   ", 5)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("non-positive limit defaults", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectQuery("SELECT .* FROM portal_threads").
			WithArgs("a@example.com", "%x%", defaultThreadSearchLimit).
			WillReturnRows(sqlmock.NewRows(threadColumnNames))
		got, err := store.SearchThreads(context.Background(), "a@example.com", "x", 0)
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestThreadStoreCreateThread(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO portal_threads").
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	mock.ExpectExec("INSERT INTO portal_thread_events").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	thread := Thread{
		ID: "thr_1", Kind: ThreadKindCorrection, TargetType: targetTypeAsset, AssetID: "asset_1",
		AuthorID: "u1", AuthorEmail: "u1@example.com",
	}
	first := ThreadEvent{ID: "evt_1", ThreadID: "thr_1", EventType: EventTypeComment, AuthorID: "u1", AuthorEmail: "u1@example.com", Body: "wrong term"}

	got, err := store.CreateThread(context.Background(), thread, first)
	require.NoError(t, err)
	assert.Equal(t, "thr_1", got.ID)
	assert.Equal(t, now, got.CreatedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreCreateThreadRollbackOnEventError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO portal_threads").
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	mock.ExpectExec("INSERT INTO portal_thread_events").
		WillReturnError(fmt.Errorf("boom"))
	mock.ExpectRollback()

	_, err := store.CreateThread(context.Background(), Thread{ID: "t", TargetType: targetTypeStandalone, AuthorID: "u", AuthorEmail: "u@x"}, ThreadEvent{ID: "e", ThreadID: "t", EventType: EventTypeComment, AuthorID: "u", AuthorEmail: "u@x"})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreListThreads(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("FROM portal_threads t").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "kind", "target_type", "asset_id", "collection_id", "prompt_id", "knowledge_page_id", "anchor", "target_version",
			"title", "author_id", "author_email", "status", "requires_resolution", "validation_state", "insight_id",
			"created_at", "updated_at", "deleted_at",
			"event_count", "last_event_at", "last_event_type",
		}).AddRow(
			"thr_1", ThreadKindComment, targetTypeAsset, "asset_1", nil, nil, nil, []byte(`{"type":"text_quote","exact":"x"}`), 3,
			"title", "u1", "u1@example.com", ThreadStatusOpen, false, "none", nil,
			now, now, nil,
			2, now, EventTypeComment,
		))

	out, total, err := store.ListThreads(context.Background(), ThreadFilter{TargetType: targetTypeAsset, AssetID: "asset_1"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, out, 1)
	assert.Equal(t, 2, out[0].EventCount)
	assert.Equal(t, EventTypeComment, out[0].LastEventType)
	assert.Equal(t, 3, out[0].TargetVersion)
	assert.JSONEq(t, `{"type":"text_quote","exact":"x"}`, string(out[0].Anchor))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreGetThread(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()

	mock.ExpectQuery("FROM portal_threads WHERE id").
		WithArgs("thr_1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "kind", "target_type", "asset_id", "collection_id", "prompt_id", "knowledge_page_id", "anchor", "target_version",
			"title", "author_id", "author_email", "status", "requires_resolution", "validation_state", "insight_id",
			"created_at", "updated_at", "deleted_at",
		}).AddRow(
			"thr_1", ThreadKindQuestion, targetTypeStandalone, nil, nil, nil, nil, nil, nil,
			"", "u1", "u1@example.com", ThreadStatusOpen, false, "none", nil,
			now, now, nil,
		))

	got, err := store.GetThread(context.Background(), "thr_1")
	require.NoError(t, err)
	assert.Equal(t, targetTypeStandalone, got.TargetType)
	assert.Empty(t, got.AssetID)
	assert.Nil(t, got.Anchor)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreListEvents(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	now := time.Now()
	rating := 5

	mock.ExpectQuery("FROM portal_thread_events WHERE thread_id").
		WithArgs("thr_1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "thread_id", "event_type", "author_id", "author_email", "body", "rating", "parent_event_id", "metadata", "created_at",
		}).AddRow("evt_1", "thr_1", EventTypeRating, "u1", "u1@example.com", "great", rating, nil, []byte(`{}`), now))

	events, err := store.ListEvents(context.Background(), "thr_1")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Rating)
	assert.Equal(t, 5, *events[0].Rating)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreAppendEvent(t *testing.T) {
	store, mock := newThreadStoreMock(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_threads SET updated_at").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	got, err := store.AppendEvent(context.Background(), ThreadEvent{ID: "evt_2", ThreadID: "thr_1", EventType: EventTypeComment, AuthorID: "u1", AuthorEmail: "u1@example.com", Body: "reply"})
	require.NoError(t, err)
	assert.Equal(t, "evt_2", got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreUpdateThreadWithStatusChange(t *testing.T) {
	store, mock := newThreadStoreMock(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM portal_threads").
		WithArgs("thr_1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(ThreadStatusOpen))
	mock.ExpectExec("UPDATE portal_threads").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resolved := ThreadStatusResolved
	err := store.UpdateThread(context.Background(), "thr_1", ThreadUpdate{Status: &resolved}, "u1", "u1@example.com")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreUpdateThreadNoStatusChangeSkipsEvent(t *testing.T) {
	store, mock := newThreadStoreMock(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM portal_threads").
		WithArgs("thr_1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(ThreadStatusOpen))
	mock.ExpectExec("UPDATE portal_threads").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	rr := true
	err := store.UpdateThread(context.Background(), "thr_1", ThreadUpdate{RequiresResolution: &rr}, "u1", "u1@example.com")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreSoftDeleteThread(t *testing.T) {
	store, mock := newThreadStoreMock(t)

	mock.ExpectExec("UPDATE portal_threads SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "thr_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.SoftDeleteThread(context.Background(), "thr_1"))

	mock.ExpectExec("UPDATE portal_threads SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	require.Error(t, store.SoftDeleteThread(context.Background(), "missing"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreCreateThreadInsertError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO portal_threads").WillReturnError(fmt.Errorf("boom"))
	mock.ExpectRollback()
	_, err := store.CreateThread(context.Background(), Thread{ID: "t", TargetType: targetTypeStandalone, AuthorID: "u", AuthorEmail: "u@x"}, ThreadEvent{ID: "e", ThreadID: "t", EventType: EventTypeComment, AuthorID: "u", AuthorEmail: "u@x"})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreAppendEventRollbackOnTouchError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_threads SET updated_at").WillReturnError(fmt.Errorf("boom"))
	mock.ExpectRollback()
	_, err := store.AppendEvent(context.Background(), ThreadEvent{ID: "e", ThreadID: "t", EventType: EventTypeComment, AuthorID: "u", AuthorEmail: "u@x"})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreUpdateThreadLoadError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM portal_threads").WithArgs("t").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	s := ThreadStatusResolved
	err := store.UpdateThread(context.Background(), "t", ThreadUpdate{Status: &s}, "u", "u@x")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreListThreadsCountError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(fmt.Errorf("boom"))
	_, _, err := store.ListThreads(context.Background(), ThreadFilter{TargetType: targetTypeStandalone})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreGetThreadError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectQuery("FROM portal_threads WHERE id").WithArgs("missing").WillReturnError(sql.ErrNoRows)
	_, err := store.GetThread(context.Background(), "missing")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreListEventsError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectQuery("FROM portal_thread_events WHERE thread_id").WithArgs("t").WillReturnError(fmt.Errorf("boom"))
	_, err := store.ListEvents(context.Background(), "t")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreCountOpenByTargets(t *testing.T) {
	store, mock := newThreadStoreMock(t)

	got, err := store.CountOpenByTargets(context.Background(), targetTypeAsset, nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	mock.ExpectQuery("FROM portal_threads").
		WillReturnRows(sqlmock.NewRows([]string{"asset_id", "count"}).AddRow("asset_1", 2).AddRow("asset_2", 5))
	got, err = store.CountOpenByTargets(context.Background(), targetTypeAsset, []string{"asset_1", "asset_2"})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"asset_1": 2, "asset_2": 5}, got)

	_, err = store.CountOpenByTargets(context.Background(), "bogus", []string{"x"})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreCountOpenByTargetsQueryError(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectQuery("FROM portal_threads").WillReturnError(fmt.Errorf("boom"))
	_, err := store.CountOpenByTargets(context.Background(), targetTypeCollection, []string{"c"})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTargetColumn(t *testing.T) {
	for tt, col := range map[string]string{targetTypeAsset: "asset_id", targetTypeCollection: "collection_id", targetTypePrompt: "prompt_id"} {
		got, err := targetColumn(tt)
		require.NoError(t, err)
		assert.Equal(t, col, got)
	}
	_, err := targetColumn(targetTypeStandalone)
	assert.Error(t, err)
}

func TestThreadStoreLinkInsight(t *testing.T) {
	store, mock := newThreadStoreMock(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE portal_threads SET insight_id").
		WithArgs("ins_1", ThreadStatusResolved, "thr_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_threads SET insight_id").
		WithArgs("ins_1", ThreadStatusResolved, "thr_missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	linked, err := store.LinkInsight(context.Background(), []string{"thr_1", "thr_missing"}, "ins_1", "u1", "u1@example.com")
	require.NoError(t, err)
	// thr_1 linked (1 row affected); thr_missing skipped (0 rows affected).
	assert.Equal(t, []string{"thr_1"}, linked)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreLinkInsightNoop(t *testing.T) {
	store, _ := newThreadStoreMock(t)
	linked, err := store.LinkInsight(context.Background(), nil, "ins_1", "u", "e")
	require.NoError(t, err)
	assert.Nil(t, linked)
	linked, err = store.LinkInsight(context.Background(), []string{"t"}, "", "u", "e")
	require.NoError(t, err)
	assert.Nil(t, linked)
}

func TestThreadStoreLinkInsightErrors(t *testing.T) {
	t.Run("begin error", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin().WillReturnError(errors.New("begin boom"))
		_, err := store.LinkInsight(context.Background(), []string{"t1"}, "ins_1", "u", "e")
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update exec error", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE portal_threads SET insight_id").
			WithArgs("ins_1", ThreadStatusResolved, "t1").
			WillReturnError(errors.New("exec boom"))
		mock.ExpectRollback()
		_, err := store.LinkInsight(context.Background(), []string{"t1"}, "ins_1", "u", "e")
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE portal_threads SET insight_id").
			WithArgs("ins_1", ThreadStatusResolved, "t1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit boom"))
		_, err := store.LinkInsight(context.Background(), []string{"t1"}, "ins_1", "u", "e")
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestThreadStoreLinkInsightDeduplicates(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin()
	// A repeated id must produce exactly ONE update + one event, not two.
	mock.ExpectExec("UPDATE portal_threads SET insight_id").
		WithArgs("ins_1", ThreadStatusResolved, "thr_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	linked, err := store.LinkInsight(context.Background(), []string{"thr_1", "thr_1"}, "ins_1", "u1", "u1@example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"thr_1"}, linked)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreRequestValidationErrors(t *testing.T) {
	t.Run("begin error", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin().WillReturnError(errors.New("begin boom"))
		require.Error(t, store.RequestValidation(context.Background(), "t1", "u", "e"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE portal_threads SET validation_state").
			WithArgs(ValidationStatePending, "t1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit boom"))
		require.Error(t, store.RequestValidation(context.Background(), "t1", "u", "e"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestThreadStoreRequestValidation(t *testing.T) {
	store, mock := newThreadStoreMock(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE portal_threads SET validation_state").
		WithArgs(ValidationStatePending, "thr_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.RequestValidation(context.Background(), "thr_1", "u1", "u1@example.com"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreRespondValidation(t *testing.T) {
	t.Run("validated leaves status unchanged", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE portal_threads SET validation_state = \\$1, updated_at").
			WithArgs(ValidationStateValidated, "thr_1", ValidationStatePending).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		err := store.RespondValidation(context.Background(), "thr_1",
			ValidationResponse{Result: ValidationStateValidated}, "sme", "sme@example.com")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("disputed re-opens the thread", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		// Disputing sets validation_state AND status=open in the same update.
		mock.ExpectExec("UPDATE portal_threads SET validation_state = \\$1, status = \\$2").
			WithArgs(ValidationStateDisputed, ThreadStatusOpen, "thr_1", ValidationStatePending).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		err := store.RespondValidation(context.Background(), "thr_1",
			ValidationResponse{Result: ValidationStateDisputed, Reason: "still wrong"}, "sme", "sme@example.com")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE portal_threads SET validation_state").
			WithArgs(ValidationStateValidated, "missing", ValidationStatePending).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()
		err := store.RespondValidation(context.Background(), "missing",
			ValidationResponse{Result: ValidationStateValidated}, "sme", "sme@example.com")
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin error", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin().WillReturnError(errors.New("begin boom"))
		require.Error(t, store.RespondValidation(context.Background(), "t1",
			ValidationResponse{Result: ValidationStateValidated}, "sme", "sme@example.com"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		store, mock := newThreadStoreMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE portal_threads SET validation_state").
			WithArgs(ValidationStateValidated, "t1", ValidationStatePending).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO portal_thread_events").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit boom"))
		require.Error(t, store.RespondValidation(context.Background(), "t1",
			ValidationResponse{Result: ValidationStateValidated}, "sme", "sme@example.com"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestThreadStoreCountSignoffs(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectQuery("SELECT COUNT\\(DISTINCT e.author_id\\)").
		WithArgs("asset_1", EventTypeApproval).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	n, err := store.CountSignoffs(context.Background(), "asset", "asset_1")
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestThreadStoreCountSignoffsBadTarget(t *testing.T) {
	store, _ := newThreadStoreMock(t)
	_, err := store.CountSignoffs(context.Background(), "bogus", "x")
	require.Error(t, err)
}

func TestValidationResultMetadata(t *testing.T) {
	assert.JSONEq(t, `{"result":"validated"}`,
		string(validationResultMetadata(ValidationResponse{Result: ValidationStateValidated})))
	assert.JSONEq(t, `{"result":"disputed","reason":"no"}`,
		string(validationResultMetadata(ValidationResponse{Result: ValidationStateDisputed, Reason: "no"})))
}

func TestThreadStoreRequestValidationNotFound(t *testing.T) {
	store, mock := newThreadStoreMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE portal_threads SET validation_state").
		WithArgs(ValidationStatePending, "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	require.Error(t, store.RequestValidation(context.Background(), "missing", "u1", "u1@example.com"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInsightLinkedMetadata(t *testing.T) {
	var m map[string]string
	require.NoError(t, json.Unmarshal(insightLinkedMetadata("ins_9"), &m))
	assert.Equal(t, "ins_9", m["insight_id"])
}

func TestNewThreadEventID(t *testing.T) {
	id := NewThreadEventID()
	assert.Contains(t, id, "evt_")
	assert.NotEqual(t, id, NewThreadEventID())
}

// --- pure helpers ---

func TestStatusAndValidationDefaults(t *testing.T) {
	assert.Equal(t, ThreadStatusOpen, statusOrDefault(""))
	assert.Equal(t, ThreadStatusAnswered, statusOrDefault(ThreadStatusAnswered))
	assert.Equal(t, "none", validationOrDefault(""))
	assert.Equal(t, "validated", validationOrDefault("validated"))
}

func TestValidThreadValidationState(t *testing.T) {
	for _, s := range []string{ValidationStateNone, ValidationStatePending, ValidationStateValidated, ValidationStateDisputed} {
		assert.True(t, ValidThreadValidationState(s), s)
	}
	assert.False(t, ValidThreadValidationState("bogus"))
}

func TestValidThreadKind(t *testing.T) {
	for _, k := range []string{ThreadKindComment, ThreadKindQuestion, ThreadKindCorrection, ThreadKindRating, ThreadKindApproval, ThreadKindRejection, ThreadKindSuggestion} {
		assert.True(t, ValidThreadKind(k), k)
	}
	assert.False(t, ValidThreadKind("bogus"))
}

func TestValidThreadStatus(t *testing.T) {
	for _, s := range []string{ThreadStatusOpen, ThreadStatusAnswered, ThreadStatusResolved, ThreadStatusWontFix, ThreadStatusAcknowledged} {
		assert.True(t, ValidThreadStatus(s), s)
	}
	assert.False(t, ValidThreadStatus("bogus"))
}

func TestDeriveFirstEventType(t *testing.T) {
	assert.Equal(t, EventTypeRating, DeriveFirstEventType(ThreadKindRating))
	assert.Equal(t, EventTypeApproval, DeriveFirstEventType(ThreadKindApproval))
	assert.Equal(t, EventTypeRejection, DeriveFirstEventType(ThreadKindRejection))
	assert.Equal(t, EventTypeComment, DeriveFirstEventType(ThreadKindCorrection))
	assert.Equal(t, EventTypeComment, DeriveFirstEventType(ThreadKindQuestion))
}

func TestStatusChangeEventType(t *testing.T) {
	assert.Equal(t, EventTypeResolution, statusChangeEventType(ThreadStatusResolved))
	assert.Equal(t, EventTypeResolution, statusChangeEventType(ThreadStatusWontFix))
	assert.Equal(t, EventTypeStatusChange, statusChangeEventType(ThreadStatusAnswered))
}

func TestStatusChangeMetadata(t *testing.T) {
	var m map[string]string
	require.NoError(t, json.Unmarshal(statusChangeMetadata("open", "resolved"), &m))
	assert.Equal(t, "open", m["old_status"])
	assert.Equal(t, "resolved", m["new_status"])
}

func TestThreadFilterEffectiveLimit(t *testing.T) {
	assert.Equal(t, defaultThreadLimit, (&ThreadFilter{}).EffectiveLimit())
	assert.Equal(t, 10, (&ThreadFilter{Limit: 10}).EffectiveLimit())
	assert.Equal(t, maxThreadLimit, (&ThreadFilter{Limit: 9999}).EffectiveLimit())
}

func TestAliasedThreadColumns(t *testing.T) {
	got := aliasedThreadColumns("t")
	assert.Contains(t, got, "t.id")
	assert.Contains(t, got, "t.deleted_at")
}

func TestNullHelpers(t *testing.T) {
	assert.False(t, nullString("").Valid)
	assert.True(t, nullString("x").Valid)
	assert.False(t, nullInt(0).Valid)
	assert.True(t, nullInt(3).Valid)
	assert.False(t, nullIntPtr(nil).Valid)
	n := 4
	assert.True(t, nullIntPtr(&n).Valid)
	assert.Nil(t, nullJSON(nil))
	assert.NotNil(t, nullJSON(json.RawMessage(`{}`)))
	assert.Equal(t, []byte("{}"), metadataOrEmpty(nil))
	assert.Equal(t, []byte(`{"a":1}`), metadataOrEmpty(json.RawMessage(`{"a":1}`)))
}

func TestNewThreadID(t *testing.T) {
	id := NewThreadID("thr")
	assert.Contains(t, id, "thr_")
	assert.NotEqual(t, id, NewThreadID("thr"))
}

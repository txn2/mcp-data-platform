package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

const (
	testTTL            = 30 * time.Minute
	pgTestSessID       = "sess-123"
	pgTestCleanupCount = 3
)

var selectColumns = []string{
	"id", "user_id", "created_at", "last_active_at", "expires_at", "state",
}

func newTestSession() *session.Session {
	now := time.Now().UTC()
	return &session.Session{
		ID:           pgTestSessID,
		UserID:       "user-abc",
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(testTTL),
		State:        map[string]any{"key": "value"},
	}
}

func TestNew(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	assert.Equal(t, testTTL, store.ttl)
	assert.Equal(t, db, store.db)
}

func TestCreate_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	sess := newTestSession()

	stateJSON, err := json.Marshal(sess.State)
	require.NoError(t, err)

	mock.ExpectExec("INSERT INTO sessions").WithArgs(
		sess.ID, sess.UserID, sess.CreatedAt, sess.LastActiveAt, sess.ExpiresAt, stateJSON,
	).WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Create(context.Background(), sess)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	sess := newTestSession()

	mock.ExpectExec("INSERT INTO sessions").
		WillReturnError(errors.New("connection refused"))

	err = store.Create(context.Background(), sess)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting session")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	sess := newTestSession()

	stateJSON, err := json.Marshal(sess.State)
	require.NoError(t, err)

	rows := sqlmock.NewRows(selectColumns).AddRow(
		sess.ID, sess.UserID, sess.CreatedAt, sess.LastActiveAt, sess.ExpiresAt, stateJSON,
	)
	mock.ExpectQuery("SELECT .+ FROM sessions").WithArgs(pgTestSessID).WillReturnRows(rows)

	got, err := store.Get(context.Background(), pgTestSessID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, pgTestSessID, got.ID)
	assert.Equal(t, "user-abc", got.UserID)
	assert.Equal(t, "value", got.State["key"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	rows := sqlmock.NewRows(selectColumns)
	mock.ExpectQuery("SELECT .+ FROM sessions").WithArgs("nonexistent").WillReturnRows(rows)

	got, err := store.Get(context.Background(), "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectQuery("SELECT .+ FROM sessions").
		WillReturnError(errors.New("db unavailable"))

	got, err := store.Get(context.Background(), pgTestSessID)
	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "scanning session")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTouch_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectExec("UPDATE sessions").WithArgs(pgTestSessID, "1800 seconds").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Touch(context.Background(), pgTestSessID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTouch_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectExec("UPDATE sessions").
		WillReturnError(errors.New("connection lost"))

	err = store.Touch(context.Background(), pgTestSessID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "touching session")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectExec("DELETE FROM sessions WHERE id").WithArgs(pgTestSessID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Delete(context.Background(), pgTestSessID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectExec("DELETE FROM sessions WHERE id").
		WillReturnError(errors.New("delete failed"))

	err = store.Delete(context.Background(), pgTestSessID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting session")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	sess := newTestSession()

	stateJSON, err := json.Marshal(sess.State)
	require.NoError(t, err)

	rows := sqlmock.NewRows(selectColumns).
		AddRow(sess.ID, sess.UserID, sess.CreatedAt, sess.LastActiveAt, sess.ExpiresAt, stateJSON).
		AddRow("sess-456", "user-def", sess.CreatedAt, sess.LastActiveAt, sess.ExpiresAt, []byte("{}"))
	mock.ExpectQuery("SELECT .+ FROM sessions").WillReturnRows(rows)

	sessions, err := store.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
	assert.Equal(t, pgTestSessID, sessions[0].ID)
	assert.Equal(t, "sess-456", sessions[1].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectQuery("SELECT .+ FROM sessions").
		WillReturnError(errors.New("db unavailable"))

	sessions, err := store.List(context.Background())
	assert.Error(t, err)
	assert.Nil(t, sessions)
	assert.Contains(t, err.Error(), "listing sessions")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateState_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	state := map[string]any{"enrichment_dedup": map[string]any{"table1": "2025-01-01"}}
	stateJSON, err := json.Marshal(state)
	require.NoError(t, err)

	mock.ExpectExec("UPDATE sessions").WithArgs(pgTestSessID, stateJSON).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.UpdateState(context.Background(), pgTestSessID, state)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateState_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectExec("UPDATE sessions").
		WillReturnError(errors.New("update failed"))

	err = store.UpdateState(context.Background(), pgTestSessID, map[string]any{"k": "v"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "updating session state")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanup_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectExec("DELETE FROM sessions WHERE expires_at").
		WillReturnResult(sqlmock.NewResult(0, pgTestCleanupCount))

	err = store.Cleanup(context.Background())
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanup_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.ExpectExec("DELETE FROM sessions WHERE expires_at").
		WillReturnError(errors.New("cleanup failed"))

	err = store.Cleanup(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cleaning up sessions")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestClose_NilCancel_NoPanic(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	assert.NoError(t, store.Close())
}

func TestClose_StopsCleanupRoutine(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})

	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("DELETE FROM sessions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM sessions").
		WillReturnResult(sqlmock.NewResult(0, 0))

	store.StartCleanupRoutine(10 * time.Millisecond)

	time.Sleep(50 * time.Millisecond)

	assert.NoError(t, store.Close())
}

func TestInterfaceCompliance(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	var _ session.Store = store
}

func TestGet_EmptyState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{TTL: testTTL})
	now := time.Now().UTC()

	rows := sqlmock.NewRows(selectColumns).AddRow(
		"sess-1", "user-1", now, now, now.Add(testTTL), []byte{},
	)
	mock.ExpectQuery("SELECT .+ FROM sessions").WithArgs("sess-1").WillReturnRows(rows)

	got, err := store.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.NotNil(t, got.State, "State should be initialized even with empty JSON")
	assert.NoError(t, mock.ExpectationsWereMet())
}

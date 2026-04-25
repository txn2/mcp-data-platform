package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPKCEState(connection string, age time.Duration) *PKCEState {
	return &PKCEState{
		connection:   connection,
		codeVerifier: "v-" + connection,
		startedBy:    "alice@example.com",
		createdAt:    time.Now().Add(-age),
		returnURL:    "/portal",
		redirectURI:  "https://platform.example.com/api/v1/admin/oauth/callback",
	}
}

func TestMemoryPKCEStore_PutAndTake(t *testing.T) {
	s := newMemoryPKCEStore(0) // GC disabled — test only the explicit path
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	require.NoError(t, s.Put(ctx, "abc", newTestPKCEState("vendor", 0)))
	got, err := s.Take(ctx, "abc")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "vendor", got.connection)

	// Take is single-shot — second call returns the not-found sentinel.
	got, err = s.Take(ctx, "abc")
	assert.ErrorIs(t, err, ErrPKCEStateNotFound)
	assert.Nil(t, got)
}

func TestMemoryPKCEStore_TakeUnknownReturnsNotFound(t *testing.T) {
	s := newMemoryPKCEStore(0)
	t.Cleanup(func() { _ = s.Close() })
	got, err := s.Take(context.Background(), "no-such-state")
	assert.ErrorIs(t, err, ErrPKCEStateNotFound)
	assert.Nil(t, got)
}

func TestMemoryPKCEStore_OpportunisticGCOnPut(t *testing.T) {
	s := newMemoryPKCEStore(0) // background GC off; we exercise put-side sweep
	t.Cleanup(func() { _ = s.Close() })

	stale := newTestPKCEState("stale", pkceTTL+time.Minute) // older than TTL
	require.NoError(t, s.Put(context.Background(), "stale-state", stale))

	// Triggering another Put runs gcLocked which evicts the stale entry.
	require.NoError(t, s.Put(context.Background(), "fresh", newTestPKCEState("fresh", 0)))

	got, err := s.Take(context.Background(), "stale-state")
	assert.ErrorIs(t, err, ErrPKCEStateNotFound)
	assert.Nil(t, got, "stale entry should have been GC'd")
}

func TestMemoryPKCEStore_BackgroundGC(t *testing.T) {
	s := newMemoryPKCEStore(50 * time.Millisecond)
	t.Cleanup(func() { _ = s.Close() })

	stale := newTestPKCEState("stale", pkceTTL+time.Minute)
	require.NoError(t, s.Put(context.Background(), "stranded", stale))

	// Wait > one GC tick and then verify the stranded entry is gone
	// without anyone calling Take.
	time.Sleep(120 * time.Millisecond)
	got, err := s.Take(context.Background(), "stranded")
	assert.ErrorIs(t, err, ErrPKCEStateNotFound)
	assert.Nil(t, got, "background GC should have swept the stranded entry")
}

func TestMemoryPKCEStore_CloseIsIdempotent(t *testing.T) {
	s := newMemoryPKCEStore(10 * time.Millisecond)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close()) // does not panic / does not double-close
}

// TestPostgresPKCEStore_Take verifies the DELETE … RETURNING path.
func TestPostgresPKCEStore_Take_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows := sqlmock.NewRows([]string{"connection", "code_verifier", "started_by", "return_url", "redirect_uri", "created_at"}).
		AddRow("vendor", "verifier-x", "alice@example.com", "/portal", "https://x/cb", time.Now())
	mock.ExpectQuery("DELETE FROM oauth_pkce_states").
		WithArgs("state-1").
		WillReturnRows(rows)

	s := &PostgresPKCEStore{db: db, stopCh: make(chan struct{})}
	got, err := s.Take(context.Background(), "state-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "vendor", got.connection)
	assert.Equal(t, "verifier-x", got.codeVerifier)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresPKCEStore_Take_NotFoundReturnsSentinel(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Empty rowset with no error → Scan returns sql.ErrNoRows, which
	// the store maps to ErrPKCEStateNotFound.
	mock.ExpectQuery("DELETE FROM oauth_pkce_states").
		WithArgs("missing").
		WillReturnRows(sqlmock.NewRows([]string{
			"connection", "code_verifier", "started_by",
			"return_url", "redirect_uri", "created_at",
		}))

	s := &PostgresPKCEStore{db: db, stopCh: make(chan struct{})}
	got, err := s.Take(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrPKCEStateNotFound)
	assert.Nil(t, got)
}

func TestPostgresPKCEStore_Put_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO oauth_pkce_states").
		WithArgs("state-2", "vendor", "verifier-x", "alice@example.com",
			"/portal", "https://x/cb", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &PostgresPKCEStore{db: db, stopCh: make(chan struct{})}
	err = s.Put(context.Background(), "state-2", &PKCEState{
		connection:   "vendor",
		codeVerifier: "verifier-x",
		startedBy:    "alice@example.com",
		createdAt:    time.Now(),
		returnURL:    "/portal",
		redirectURI:  "https://x/cb",
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresPKCEStore_Put_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO oauth_pkce_states").WillReturnError(errors.New("conn refused"))
	s := &PostgresPKCEStore{db: db, stopCh: make(chan struct{})}
	err = s.Put(context.Background(), "x", newTestPKCEState("v", 0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkce_store: put")
}

func TestPostgresPKCEStore_CloseIsIdempotent(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	s := &PostgresPKCEStore{db: db, stopCh: make(chan struct{})}
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
}

func TestNoopCloseLog_IgnoresNilErr(_ *testing.T) {
	noopCloseLog(nil) // does not panic
}

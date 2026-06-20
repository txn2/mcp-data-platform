package pkcestore

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestState(connection string, age time.Duration) *State {
	return &State{
		Connection:   connection,
		CodeVerifier: "v-" + connection,
		StartedBy:    "alice@example.com",
		CreatedAt:    time.Now().Add(-age),
		ReturnURL:    "/portal",
		RedirectURI:  "https://platform.example.com/api/v1/admin/oauth/callback",
	}
}

func TestMemoryStore_PutAndTake(t *testing.T) {
	s := newMemoryStoreWithInterval(0) // GC disabled — test only the explicit path
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	require.NoError(t, s.Put(ctx, "abc", newTestState("vendor", 0)))
	got, err := s.Take(ctx, "abc")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "vendor", got.Connection)

	// Take is single-shot — second call returns the not-found sentinel.
	got, err = s.Take(ctx, "abc")
	assert.ErrorIs(t, err, ErrStateNotFound)
	assert.Nil(t, got)
}

func TestMemoryStore_TakeUnknownReturnsNotFound(t *testing.T) {
	s := newMemoryStoreWithInterval(0)
	t.Cleanup(func() { _ = s.Close() })
	got, err := s.Take(context.Background(), "no-such-state")
	assert.ErrorIs(t, err, ErrStateNotFound)
	assert.Nil(t, got)
}

func TestMemoryStore_OpportunisticGCOnPut(t *testing.T) {
	s := newMemoryStoreWithInterval(0) // background GC off; we exercise put-side sweep
	t.Cleanup(func() { _ = s.Close() })

	stale := newTestState("stale", TTL+time.Minute) // older than TTL
	require.NoError(t, s.Put(context.Background(), "stale-state", stale))

	// Triggering another Put runs gcLocked which evicts the stale entry.
	require.NoError(t, s.Put(context.Background(), "fresh", newTestState("fresh", 0)))

	got, err := s.Take(context.Background(), "stale-state")
	assert.ErrorIs(t, err, ErrStateNotFound)
	assert.Nil(t, got, "stale entry should have been GC'd")
}

func TestMemoryStore_BackgroundGC(t *testing.T) {
	s := newMemoryStoreWithInterval(20 * time.Millisecond)
	t.Cleanup(func() { _ = s.Close() })

	stale := newTestState("stale", TTL+time.Minute)
	require.NoError(t, s.Put(context.Background(), "stranded", stale))

	// Poll up to a generous timeout so slow CI runners don't flake.
	// The background GC sweeps every 20ms; we give it 5s before failing.
	assert.Eventually(t, func() bool {
		s.mu.Lock()
		_, present := s.states["stranded"]
		s.mu.Unlock()
		return !present
	}, 5*time.Second, 50*time.Millisecond, "background GC should have swept the stranded entry")
}

func TestMemoryStore_CloseIsIdempotent(t *testing.T) {
	s := newMemoryStoreWithInterval(10 * time.Millisecond)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close()) // does not panic / does not double-close
}

// TestPostgresStore_Take_Success verifies the DELETE … RETURNING path.
func TestPostgresStore_Take_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows := sqlmock.NewRows([]string{"connection", "connection_kind", "code_verifier", "started_by", "return_url", "redirect_uri", "created_at"}).
		AddRow("vendor", "mcp", "verifier-x", "alice@example.com", "/portal", "https://x/cb", time.Now())
	mock.ExpectQuery("DELETE FROM oauth_pkce_states").
		WithArgs("state-1").
		WillReturnRows(rows)

	s := &PostgresStore{db: db, enc: passThroughEncryptor{}, stopCh: make(chan struct{})}
	got, err := s.Take(context.Background(), "state-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "vendor", got.Connection)
	assert.Equal(t, "verifier-x", got.CodeVerifier)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Take_NotFoundReturnsSentinel(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Empty rowset with no error → Scan returns sql.ErrNoRows, which
	// the store maps to ErrStateNotFound.
	mock.ExpectQuery("DELETE FROM oauth_pkce_states").
		WithArgs("missing").
		WillReturnRows(sqlmock.NewRows([]string{
			"connection", "connection_kind", "code_verifier", "started_by",
			"return_url", "redirect_uri", "created_at",
		}))

	s := &PostgresStore{db: db, enc: passThroughEncryptor{}, stopCh: make(chan struct{})}
	got, err := s.Take(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrStateNotFound)
	assert.Nil(t, got)
}

func TestPostgresStore_Put_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO oauth_pkce_states").
		WithArgs("state-2", "vendor", "mcp", "verifier-x", "alice@example.com",
			"/portal", "https://x/cb", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &PostgresStore{db: db, enc: passThroughEncryptor{}, stopCh: make(chan struct{})}
	err = s.Put(context.Background(), "state-2", &State{
		Connection:   "vendor",
		CodeVerifier: "verifier-x",
		StartedBy:    "alice@example.com",
		CreatedAt:    time.Now(),
		ReturnURL:    "/portal",
		RedirectURI:  "https://x/cb",
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Put_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO oauth_pkce_states").WillReturnError(errors.New("conn refused"))
	s := &PostgresStore{db: db, enc: passThroughEncryptor{}, stopCh: make(chan struct{})}
	err = s.Put(context.Background(), "x", newTestState("v", 0))
	assert.ErrorIs(t, err, ErrStorePut)
}

// TestPostgresStore_Put_CollisionRejected proves the collision guard:
// when ON CONFLICT DO NOTHING reports zero rows affected, Put returns
// ErrStateCollision rather than silently overwriting.
func TestPostgresStore_Put_CollisionRejected(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO oauth_pkce_states").
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows affected = collision
	s := &PostgresStore{db: db, enc: passThroughEncryptor{}, stopCh: make(chan struct{})}
	err = s.Put(context.Background(), "duplicate-state", newTestState("v", 0))
	assert.ErrorIs(t, err, ErrStateCollision)
}

// TestPostgresStore_EncryptsCodeVerifierAtRest verifies the column goes
// through the encryptor on Put and is reversed on Take. Uses a trivial
// reverse-bytes "encryption" so we can read intermediate state.
func TestPostgresStore_EncryptsCodeVerifierAtRest(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	enc := reverseEncryptor{}
	mock.ExpectExec("INSERT INTO oauth_pkce_states").
		WithArgs("s1", "vendor", "mcp", reverse("verifier-x"), "alice", "/p", "https://x", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("DELETE FROM oauth_pkce_states").
		WithArgs("s1").
		WillReturnRows(sqlmock.NewRows([]string{
			"connection", "connection_kind", "code_verifier", "started_by", "return_url", "redirect_uri", "created_at",
		}).AddRow("vendor", "mcp", reverse("verifier-x"), "alice", "/p", "https://x", time.Now()))

	s := &PostgresStore{db: db, enc: enc, stopCh: make(chan struct{})}
	require.NoError(t, s.Put(context.Background(), "s1", &State{
		Connection:   "vendor",
		CodeVerifier: "verifier-x",
		StartedBy:    "alice",
		CreatedAt:    time.Now(),
		ReturnURL:    "/p",
		RedirectURI:  "https://x",
	}))
	got, err := s.Take(context.Background(), "s1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "verifier-x", got.CodeVerifier, "encryptor round-trip should restore plaintext")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// reverseEncryptor is a deterministic stand-in for the platform's
// FieldEncryptor: byte-reverse on Encrypt, byte-reverse on Decrypt.
// Lets the test verify ciphertext is what was sent to the DB.
type reverseEncryptor struct{}

func (reverseEncryptor) Encrypt(s string) (string, error) { return reverse(s), nil }
func (reverseEncryptor) Decrypt(s string) (string, error) { return reverse(s), nil }

func reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func TestPostgresStore_CloseIsIdempotent(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	s := &PostgresStore{db: db, enc: passThroughEncryptor{}, stopCh: make(chan struct{})}
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
}

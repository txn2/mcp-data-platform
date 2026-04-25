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
	s := memoryPKCEStoreWithInterval(0) // GC disabled — test only the explicit path
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
	s := memoryPKCEStoreWithInterval(0)
	t.Cleanup(func() { _ = s.Close() })
	got, err := s.Take(context.Background(), "no-such-state")
	assert.ErrorIs(t, err, ErrPKCEStateNotFound)
	assert.Nil(t, got)
}

func TestMemoryPKCEStore_OpportunisticGCOnPut(t *testing.T) {
	s := memoryPKCEStoreWithInterval(0) // background GC off; we exercise put-side sweep
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
	s := memoryPKCEStoreWithInterval(20 * time.Millisecond)
	t.Cleanup(func() { _ = s.Close() })

	stale := newTestPKCEState("stale", pkceTTL+time.Minute)
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

func TestMemoryPKCEStore_CloseIsIdempotent(t *testing.T) {
	s := memoryPKCEStoreWithInterval(10 * time.Millisecond)
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

	s := &PostgresPKCEStore{db: db, enc: passThroughPKCEEncryptor{}, stopCh: make(chan struct{})}
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

	s := &PostgresPKCEStore{db: db, enc: passThroughPKCEEncryptor{}, stopCh: make(chan struct{})}
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

	s := &PostgresPKCEStore{db: db, enc: passThroughPKCEEncryptor{}, stopCh: make(chan struct{})}
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
	s := &PostgresPKCEStore{db: db, enc: passThroughPKCEEncryptor{}, stopCh: make(chan struct{})}
	err = s.Put(context.Background(), "x", newTestPKCEState("v", 0))
	assert.ErrorIs(t, err, ErrPKCEStorePut)
}

// TestPostgresPKCEStore_Put_CollisionRejected proves the collision
// guard: when ON CONFLICT DO NOTHING reports zero rows affected, Put
// returns ErrPKCEStateCollision rather than silently overwriting.
func TestPostgresPKCEStore_Put_CollisionRejected(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO oauth_pkce_states").
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows affected = collision
	s := &PostgresPKCEStore{db: db, enc: passThroughPKCEEncryptor{}, stopCh: make(chan struct{})}
	err = s.Put(context.Background(), "duplicate-state", newTestPKCEState("v", 0))
	assert.ErrorIs(t, err, ErrPKCEStateCollision)
}

// TestPostgresPKCEStore_EncryptsCodeVerifierAtRest verifies the column
// goes through the encryptor on Put and is reversed on Take. Uses a
// trivial reverse-bytes "encryption" so we can read intermediate state.
func TestPostgresPKCEStore_EncryptsCodeVerifierAtRest(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	enc := reverseEncryptor{}
	mock.ExpectExec("INSERT INTO oauth_pkce_states").
		WithArgs("s1", "vendor", reverse("verifier-x"), "alice", "/p", "https://x", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("DELETE FROM oauth_pkce_states").
		WithArgs("s1").
		WillReturnRows(sqlmock.NewRows([]string{
			"connection", "code_verifier", "started_by", "return_url", "redirect_uri", "created_at",
		}).AddRow("vendor", reverse("verifier-x"), "alice", "/p", "https://x", time.Now()))

	s := &PostgresPKCEStore{db: db, enc: enc, stopCh: make(chan struct{})}
	require.NoError(t, s.Put(context.Background(), "s1", &PKCEState{
		connection:   "vendor",
		codeVerifier: "verifier-x",
		startedBy:    "alice",
		createdAt:    time.Now(),
		returnURL:    "/p",
		redirectURI:  "https://x",
	}))
	got, err := s.Take(context.Background(), "s1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "verifier-x", got.codeVerifier, "encryptor round-trip should restore plaintext")
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

func TestPostgresPKCEStore_CloseIsIdempotent(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	s := &PostgresPKCEStore{db: db, enc: passThroughPKCEEncryptor{}, stopCh: make(chan struct{})}
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
}

package oauthserver

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/pkg/oauth"
	oauthpostgres "github.com/txn2/mcp-data-platform/pkg/oauth/postgres"
)

const testSigningKey = "0123456789abcdef0123456789abcdef" // 32 bytes, satisfies HMAC minimum.

// TestMain fails the package if any test leaks a goroutine. The OAuth server and
// Postgres store both run cleanup tickers, so this guards the shutdown contract:
// every Handle a test builds must be fully torn down (see stop).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// stop fully tears down a Handle in tests: it closes the store (database path)
// and cancels the in-memory cleanup routine (single-replica path), mirroring how
// platform wires Close and StateStoreCleanup into shutdown. Without both, the
// ticker goroutines outlive the test and TestMain's goleak check fails.
func stop(t *testing.T, h *Handle) {
	t.Helper()
	assert.NoError(t, h.Close())
	if cancel := h.StateStoreCleanup(); cancel != nil {
		cancel()
	}
}

// failingStorage wraps a real storage but forces CreateClient to fail, exercising
// the client pre-registration error path without a live database.
type failingStorage struct {
	oauth.Storage
}

func (failingStorage) CreateClient(context.Context, *oauth.Client) error {
	return errors.New("create client boom")
}

func TestResolveStorage_Memory(t *testing.T) {
	storage, pg := Config{}.resolveStorage()
	assert.Nil(t, pg, "no database → no Postgres store")
	_, ok := storage.(*oauth.MemoryStorage)
	assert.True(t, ok, "no database → in-memory storage, got %T", storage)
}

func TestResolveStorage_Database(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock close error is inconsequential in tests.

	storage, pg := Config{DB: db}.resolveStorage()
	require.NotNil(t, pg, "database present → Postgres store selected")
	// The Postgres store is both the storage and the returned typed handle.
	got, ok := storage.(*oauthpostgres.Store)
	require.True(t, ok, "database path returns a *oauthpostgres.Store, got %T", storage)
	assert.Same(t, pg, got)
}

func TestResolveStorage_InjectedOverridesDB(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock close error is inconsequential in tests.

	injected := oauth.NewMemoryStorage()
	storage, pg := Config{DB: db, Storage: injected}.resolveStorage()
	assert.Nil(t, pg, "injected storage → no Postgres store owned")
	assert.Same(t, injected, storage, "injected storage used verbatim")
}

func TestNew_MemoryPath_StartsCleanupRoutine(t *testing.T) {
	handle, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
	})
	require.NoError(t, err)
	require.NotNil(t, handle)

	// Memory path exposes a cleanup cancel (for the caller's lifecycle) and owns
	// no store-closer.
	assert.NotNil(t, handle.StateStoreCleanup(), "memory state store starts a cancelable cleanup routine")
	assert.Nil(t, handle.storeCloser, "memory path owns no store-closer")
	require.NotNil(t, handle.Server())

	// Close is a no-op on the memory path; the cleanup routine is canceled via
	// StateStoreCleanup.
	stop(t, handle)
}

func TestNew_PreRegistersClientWithBcryptHash(t *testing.T) {
	// Inject a storage we hold a reference to so we can read the client back;
	// the server exposes no storage accessor.
	storage := oauth.NewMemoryStorage()
	handle, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		Storage:    storage,
		Clients: []Client{{
			ID:           "acme-client",
			Secret:       "s3cr3t",
			RedirectURIs: []string{"https://acme.example.com/callback"},
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, handle.Server())

	// The pre-registered client's secret must be stored as a bcrypt hash, not
	// plaintext, so it matches the shape the token endpoint verifies against.
	client, err := storage.GetClient(context.Background(), "acme-client")
	require.NoError(t, err)
	assert.NotEqual(t, "s3cr3t", client.ClientSecret)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(client.ClientSecret), []byte("s3cr3t")))
	assert.True(t, client.RequirePKCE)
	assert.Equal(t, []string{"authorization_code", "refresh_token"}, client.GrantTypes)
	assert.Equal(t, []string{"https://acme.example.com/callback"}, client.RedirectURIs)

	stop(t, handle)
}

func TestNew_DatabasePath_OwnsStoreCloser(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock close error is inconsequential in tests.

	handle, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		DB:         db,
	})
	require.NoError(t, err)
	require.NotNil(t, handle)

	// Database path owns a store-closer and uses the shared store for state,
	// so no in-memory cleanup routine is started.
	assert.NotNil(t, handle.storeCloser, "database path owns the Postgres store-closer")
	assert.Nil(t, handle.StateStoreCleanup(), "database path uses the shared store, no memory cleanup")

	// Close delegates to the Postgres store's Close (stops its cleanup routine).
	assert.NoError(t, handle.Close())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNew_UpstreamConfigured(t *testing.T) {
	handle, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		Upstream: &Upstream{
			Issuer:       "https://keycloak.example.com",
			ClientID:     "mcp",
			ClientSecret: "kc-secret",
			RedirectURI:  "https://auth.example.com/oauth/callback",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, handle.Server())
	stop(t, handle)
}

func TestNew_ClientHashingError(t *testing.T) {
	// A secret longer than bcrypt's 72-byte input limit forces a hashing error,
	// exercising the pre-registration failure path.
	longSecret := make([]byte, 100)
	for i := range longSecret {
		longSecret[i] = 'a'
	}
	_, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		Clients:    []Client{{ID: "big", Secret: string(longSecret)}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hashing client secret for big")
}

func TestNew_ClientCreateError(t *testing.T) {
	_, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		Storage:    failingStorage{oauth.NewMemoryStorage()},
		Clients:    []Client{{ID: "acme", Secret: "s3cr3t"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating client acme")
}

func TestNew_ServerConstructionError(t *testing.T) {
	// DCR enabled with an invalid regex pattern fails NewServer's DCR service.
	_, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		DCR: DCR{
			Enabled:                 true,
			AllowedRedirectPatterns: []string{"("},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating OAuth server")
}

func TestNew_DatabasePath_ClosesStoreOnError(t *testing.T) {
	// The Postgres store's cleanup ticker starts before the failure-prone
	// assembly steps. When assembly fails, New must close the store so its
	// goroutine does not outlive the failed construction. goleak asserts no
	// goroutine leaks past the failed New.
	defer goleak.VerifyNone(t)

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock close error is inconsequential in tests.

	// Invalid DCR regex fails NewServer, which runs after pgStore's cleanup
	// routine has already started.
	handle, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		DB:         db,
		DCR:        DCR{Enabled: true, AllowedRedirectPatterns: []string{"("}},
	})
	require.Error(t, err)
	assert.Nil(t, handle, "failed construction returns no handle")
}

func TestNew_DCRDefaultDenyStillConstructs(t *testing.T) {
	// DCR enabled without patterns and without allow-all is valid but denies all
	// registrations; New must still construct the server (and warn at boot).
	handle, err := New(context.Background(), Config{
		Issuer:     "https://auth.example.com",
		SigningKey: []byte(testSigningKey),
		DCR:        DCR{Enabled: true},
	})
	require.NoError(t, err)
	require.NotNil(t, handle.Server())
	stop(t, handle)
}

func TestHandle_NilSafe(t *testing.T) {
	var h *Handle
	assert.Nil(t, h.Server())
	assert.Nil(t, h.StateStoreCleanup())
	assert.NoError(t, h.Close())
}

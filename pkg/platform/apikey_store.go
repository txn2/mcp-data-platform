package platform

import (
	"database/sql"

	"github.com/txn2/mcp-data-platform/internal/platform/apikeystore"
)

// The database-managed API key store lives in internal/platform/apikeystore.
// These aliases keep the facade's published names — the admin API names
// APIKeyStore and APIKeyDefinition through this package — pointing at the
// moved implementation, so the extraction is invisible to callers.
type (
	// APIKeyDefinition is a database-managed API key.
	APIKeyDefinition = apikeystore.Definition
	// APIKeyStore manages API key persistence.
	APIKeyStore = apikeystore.Store
	// PostgresAPIKeyStore is the PostgreSQL-backed API key store.
	PostgresAPIKeyStore = apikeystore.PostgresStore
	// NoopAPIKeyStore is the store a deployment without a database holds.
	NoopAPIKeyStore = apikeystore.NoopStore
)

// ErrAPIKeyNotFound is returned when an API key does not exist in the database.
var ErrAPIKeyNotFound = apikeystore.ErrNotFound

// NewPostgresAPIKeyStore creates a new PostgreSQL-backed API key store.
func NewPostgresAPIKeyStore(db *sql.DB) *PostgresAPIKeyStore { return apikeystore.NewPostgres(db) }

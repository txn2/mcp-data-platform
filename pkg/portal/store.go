package portal

import (
	"database/sql"

	"github.com/txn2/mcp-data-platform/internal/portal/portalnoop"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/portal/portalversions"
)

// JSON field names reused across handler payloads. They match the SQL column
// names the store writes, which is why they are spelled the same; reusing one
// constant per name avoids duplicate literals (goconst) without implying a
// stricter relationship than equality of the underlying string.
const (
	colName        = "name"
	colDescription = "description"
	colTags        = "tags"
)

// HTTP / domain-level string constants reused across handlers.
const (
	mimeTypePNG = "image/png"

	statusUpdated  = "updated"
	statusRevoked  = "revoked"
	statusDeleted  = "deleted"
	statusReverted = "reverted"

	// keyVersion is the literal "version" used as the JSON response
	// field name and as the argument to r.PathValue when extracting
	// the {version} segment. Route registration strings still embed
	// the literal because Go's http.ServeMux pattern parser doesn't
	// accept runtime concatenation cleanly — this constant exists so
	// PathValue and JSON-key sites don't drift apart.
	keyVersion = "version"

	thumbSizeLarge = "large"
)

// NewPostgresAssetStore creates a new PostgreSQL asset store.
func NewPostgresAssetStore(db *sql.DB) AssetStore { return portalstore.NewPostgresAssetStore(db) }

// NewPostgresShareStore creates a new PostgreSQL share store.
func NewPostgresShareStore(db *sql.DB) ShareStore { return portalstore.NewPostgresShareStore(db) }

// NewPostgresVersionStore creates a new PostgreSQL asset version store.
func NewPostgresVersionStore(db *sql.DB) VersionStore {
	return portalversions.NewPostgres(db)
}

// NewPostgresCollectionStore creates a new PostgreSQL collection store.
func NewPostgresCollectionStore(db *sql.DB) CollectionStore {
	return portalstore.NewPostgresCollectionStore(db)
}

// NewNoopAssetStore creates a no-op AssetStore for use when no database is available.
func NewNoopAssetStore() AssetStore { return portalnoop.NewAssetStore() }

// NewNoopShareStore creates a no-op ShareStore for use when no database is available.
func NewNoopShareStore() ShareStore { return portalnoop.NewShareStore() }

// NewNoopVersionStore creates a no-op VersionStore for use when no database is available.
func NewNoopVersionStore() VersionStore { return portalnoop.NewVersionStore() }

// NewNoopCollectionStore creates a no-op CollectionStore for use when no database is available.
func NewNoopCollectionStore() CollectionStore { return portalnoop.NewCollectionStore() }

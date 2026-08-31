package portal

import (
	"database/sql"

	"github.com/txn2/mcp-data-platform/internal/portal/portalnoop"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/portal/portalversions"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
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

// NewPostgresAssetStore creates a new PostgreSQL asset store. producers records
// what produced each asset (#1569) and may be nil, which records nothing. Pass
// indexjobs.WithProducer to have asset writes enqueue their own index job
// instead of waiting for the reconciler's next sweep.
func NewPostgresAssetStore(db *sql.DB, producers producedby.Store, opts ...indexjobs.StoreOption) AssetStore {
	return portalstore.NewPostgresAssetStore(db, producers, opts...)
}

// NewPostgresShareStore creates a new PostgreSQL share store.
func NewPostgresShareStore(db *sql.DB) ShareStore { return portalstore.NewPostgresShareStore(db) }

// NewPostgresVersionStore creates a new PostgreSQL asset version store.
// s3Client deletes the objects of versions the retention cap prunes and may be
// nil in database-only mode, where the prune still trims the table; maxVersions
// is the deployment default a per-asset override supersedes, nil selecting the
// platform default of 100; producers records what produced each version and may
// be nil.
func NewPostgresVersionStore(db *sql.DB, s3Client S3Client, maxVersions *int, producers producedby.Store) VersionStore {
	return portalversions.NewPostgres(db, s3Client, maxVersions, producers)
}

// NewPostgresCollectionStore creates a new PostgreSQL collection store. Pass
// indexjobs.WithProducer to have collection writes enqueue their own index
// job instead of waiting for the reconciler's next sweep.
func NewPostgresCollectionStore(db *sql.DB, opts ...indexjobs.StoreOption) CollectionStore {
	return portalstore.NewPostgresCollectionStore(db, opts...)
}

// NewNoopAssetStore creates a no-op AssetStore for use when no database is available.
func NewNoopAssetStore() AssetStore { return portalnoop.NewAssetStore() }

// NewNoopShareStore creates a no-op ShareStore for use when no database is available.
func NewNoopShareStore() ShareStore { return portalnoop.NewShareStore() }

// NewNoopVersionStore creates a no-op VersionStore for use when no database is available.
func NewNoopVersionStore() VersionStore { return portalnoop.NewVersionStore() }

// NewNoopCollectionStore creates a no-op CollectionStore for use when no database is available.
func NewNoopCollectionStore() CollectionStore { return portalnoop.NewCollectionStore() }

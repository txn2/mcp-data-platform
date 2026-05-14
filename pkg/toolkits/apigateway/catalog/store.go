package catalog

import "context"

// Store is the persistence interface for catalogs and their
// component specs. Implementations are expected to enforce
// the (name, version) uniqueness and the spec-name uniqueness
// within a catalog at the storage layer; ValidateID /
// ValidateSpecName / ValidateSourceKind handle input shape
// upstream.
type Store interface {
	// Catalog header CRUD.
	CreateCatalog(ctx context.Context, c Catalog) error
	GetCatalog(ctx context.Context, id string) (*Catalog, error)
	ListCatalogs(ctx context.Context) ([]Catalog, error)
	UpdateCatalog(ctx context.Context, id string, u Update) error
	DeleteCatalog(ctx context.Context, id string) error

	// Component specs within a catalog. UpsertSpec creates the row
	// when (catalog_id, spec_name) is new and replaces content +
	// source metadata when it already exists. The portal "Edit"
	// flow uses the same write path as "Add".
	UpsertSpec(ctx context.Context, catalogID string, spec SpecEntry) error
	GetSpec(ctx context.Context, catalogID, specName string) (*SpecEntry, error)
	ListSpecs(ctx context.Context, catalogID string) ([]SpecEntry, error)
	DeleteSpec(ctx context.Context, catalogID, specName string) error

	// ReferencingConnections returns the (kind, name) of every
	// connection_instances row whose config JSONB has
	// catalog_id == catalogID. The admin handler calls this before
	// DeleteCatalog to refuse with a clear "still referenced by ..."
	// error instead of failing the FK at delete time. (There is no
	// SQL FK from connection_instances → api_catalogs because the
	// reference lives inside the JSONB, not in its own column.)
	ReferencingConnections(ctx context.Context, catalogID string) ([]ConnectionRef, error)
}

// ConnectionRef identifies a connection_instances row by its
// composite key.
type ConnectionRef struct {
	Kind string
	Name string
}

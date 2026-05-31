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

	// UpsertOperationEmbeddings replaces every embedding row for
	// (catalog_id, spec_name) with the supplied vectors. Atomic —
	// either all rows for the spec are present afterward or none
	// are (no partial state visible to ranking reads). Used by
	// catalog_handler at spec-write time so semantic ranking can
	// read pre-computed vectors at connection registration without
	// hitting the embedding provider on the request path.
	UpsertOperationEmbeddings(ctx context.Context, catalogID, specName string, rows []OperationEmbedding) error

	// UpsertOperationEmbeddingsBatch inserts or updates the
	// supplied rows in place. Unlike UpsertOperationEmbeddings,
	// this method does NOT delete absent rows: rows for operations
	// not in the batch survive untouched. Used by the embed-jobs
	// worker for per-chunk incremental persistence so a job that
	// fails on chunk N leaves chunks 0..N-1 visible to the next
	// attempt's dedup pass via ListOperationEmbeddings.
	//
	// Atomic per call: the upsert runs inside one transaction so
	// a concurrent ranking read either sees the prior set or the
	// prior set plus this chunk, never a half-written chunk.
	// Returns ErrNotFound when the referenced spec does not
	// exist (FK violation on insert).
	UpsertOperationEmbeddingsBatch(ctx context.Context, catalogID, specName string, rows []OperationEmbedding) error

	// ListOperationEmbeddings returns every embedding row for
	// (catalog_id, spec_name) so the toolkit can populate its
	// per-connection vector map at registration time without
	// re-embedding. Empty slice (not error) when the spec has no
	// vectors yet.
	ListOperationEmbeddings(ctx context.Context, catalogID, specName string) ([]OperationEmbedding, error)

	// DeleteOperationEmbeddings removes every embedding row for
	// (catalog_id, spec_name). Called by the admin reembed
	// endpoint before recomputing — the spec FK cascade handles
	// spec-deletion cleanup so callers do not need to invoke this
	// separately on spec delete.
	DeleteOperationEmbeddings(ctx context.Context, catalogID, specName string) error

	// SetOperationCount updates api_catalog_specs.operation_count
	// on a single row. The embedding worker calls this after a
	// successful Upsert so the reconciler's gap predicate (which
	// compares operation_count against the embedding row count)
	// sees a fully-indexed spec on the next sweep. Without this
	// hook, specs whose operation_count column was never stamped
	// (legacy rows from before migration 000045) would re-enqueue
	// on every reconciler tick forever.
	//
	// Returns ErrNotFound when (catalogID, specName) does not
	// exist. The worker treats that as best-effort and logs only.
	SetOperationCount(ctx context.Context, catalogID, specName string, count int) error

	// ListEmbeddingGaps returns the (catalog_id, spec_name) pairs
	// whose operation_count does not equal the number of persisted
	// rows in api_catalog_operation_embeddings. It is the api-catalog
	// gap query the indexjobs reconciler drives via the catalog
	// Sink's FindGaps: a spec written before the embedder was
	// configured, vectors lost to an outage, or any other mismatch
	// is returned so a reconciler job converges it. Specs with
	// operation_count = 0 and no vectors are NOT a gap (there is
	// genuinely nothing to embed).
	ListEmbeddingGaps(ctx context.Context) ([]SpecKey, error)

	// EmbeddingCoverage returns the system-wide indexed and expected
	// operation-vector totals across every catalog and spec: indexed
	// is the row count of api_catalog_operation_embeddings, expected
	// is the sum of api_catalog_specs.operation_count. The admin
	// Indexing dashboard renders the ratio as the api-catalog kind's
	// coverage (the catalog Sink's CoverageReporter wraps this).
	EmbeddingCoverage(ctx context.Context) (indexed, expected int, err error)
}

// SpecKey identifies one component spec by its composite key. The
// indexjobs reconciler maps each gap to an index_jobs source_id via
// the api-catalog encoding.
type SpecKey struct {
	CatalogID string
	SpecName  string
}

// ConnectionRef identifies a connection_instances row by its
// composite key.
type ConnectionRef struct {
	Kind string
	Name string
}

// OperationEmbedding is one persisted embedding row. OperationID
// is the synthesized id buildOperationIndex assigns to each path/
// method pair (the spec's operationId when set, "METHOD path"
// otherwise). TextHash is the SHA-256 of the source text fed to
// the embedding provider — used at upsert time to skip recomputing
// vectors whose text did not change between two spec refreshes.
// Model and Dim record the provider identity and vector
// dimensionality at write time so a deployment that swaps models
// has a row-level breadcrumb that the cached vectors no longer
// match the current provider's output.
type OperationEmbedding struct {
	OperationID string
	TextHash    []byte
	Embedding   []float32
	Model       string
	Dim         int
}

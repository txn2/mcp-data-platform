package query

import "context"

// Provider provides query execution context for metadata entities.
// Trino implements this. Future engines (Spark, Presto) can too.
type Provider interface {
	// Name returns the provider name.
	Name() string

	// ResolveTable converts a URN to a query table identifier.
	ResolveTable(ctx context.Context, urn string) (*TableIdentifier, error)

	// GetTableAvailability checks if a table is queryable.
	GetTableAvailability(ctx context.Context, urn string) (*TableAvailability, error)

	// GetQueryExamples returns sample queries for a table.
	GetQueryExamples(ctx context.Context, urn string) ([]Example, error)

	// GetExecutionContext returns context for querying multiple tables.
	GetExecutionContext(ctx context.Context, urns []string) (*ExecutionContext, error)

	// GetTableSchema returns the schema of a table.
	GetTableSchema(ctx context.Context, table TableIdentifier) (*TableSchema, error)

	// Close releases resources.
	Close() error
}

// Executor can execute queries against the query engine.
type Executor interface {
	// Execute runs a query and returns results.
	Execute(ctx context.Context, sql string, limit int) (*Result, error)

	// Describe returns information about a table.
	Describe(ctx context.Context, table TableIdentifier) (*TableSchema, error)
}

// CatalogBrowser enumerates the catalog/schema/table namespace of the query
// engine. It is an optional capability layered on top of Provider (not every
// provider can browse), consumed by argument autocompletion for the
// schema:// and availability:// resource templates. The returned names are the
// raw engine identifiers; callers apply their own persona/connection filtering.
type CatalogBrowser interface {
	// ListCatalogs returns the catalog names visible to the engine connection.
	ListCatalogs(ctx context.Context) ([]string, error)
	// ListSchemas returns the schema names in a catalog.
	ListSchemas(ctx context.Context, catalog string) ([]string, error)
	// ListTables returns the table names in a catalog schema.
	ListTables(ctx context.Context, catalog, schema string) ([]string, error)
}

// CatalogBrowserFrom reports the catalog-browse capability of p, returning the
// innermost provider that implements it. It unwraps any decorator chain so a
// wrapped provider still exposes the capability; browse lookups are namespace
// listings (not per-table context), so reaching the underlying provider
// directly is correct. ok is false when no provider in the chain can browse.
func CatalogBrowserFrom(p Provider) (CatalogBrowser, bool) {
	inner := p
	for {
		if b, ok := inner.(CatalogBrowser); ok {
			return b, true
		}
		u, ok := inner.(interface{ Unwrap() Provider })
		if !ok {
			return nil, false
		}
		inner = u.Unwrap()
	}
}

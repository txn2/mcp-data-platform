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
	GetQueryExamples(ctx context.Context, urn string) ([]QueryExample, error)

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
	Execute(ctx context.Context, sql string, limit int) (*QueryResult, error)

	// Describe returns information about a table.
	Describe(ctx context.Context, table TableIdentifier) (*TableSchema, error)
}

# Providers Reference

Providers abstract access to semantic metadata and query execution.

## Semantic Provider

The semantic provider interface abstracts access to a metadata catalog. With a database, the platform's own [catalog](../server/catalog.md) implements it.

### Interface

```go
type Provider interface {
    Name() string

    // Table context
    GetTableContext(ctx context.Context, table TableIdentifier) (*TableContext, error)
    GetColumnContext(ctx context.Context, column ColumnIdentifier) (*ColumnContext, error)
    GetColumnsContext(ctx context.Context, table TableIdentifier) (map[string]*ColumnContext, error)

    // Lineage
    GetLineage(ctx context.Context, table TableIdentifier, direction LineageDirection, maxDepth int) (*LineageInfo, error)

    // Search
    SearchTables(ctx context.Context, filter SearchFilter) ([]TableSearchResult, error)

    // Glossary
    GetGlossaryTerm(ctx context.Context, urn string) (*GlossaryTerm, error)

    Close() error
}
```

### Types

```go
type TableIdentifier struct {
    Catalog string
    Schema  string
    Table   string
}

type TableContext struct {
    Description  string
    Owners       []Owner
    Tags         []string
    Domain       *Domain
    GlossaryTerms []GlossaryTermRef
    QualityScore *float64
    Deprecation  *Deprecation
    CustomProperties map[string]string
}

type Owner struct {
    Name  string
    Type  string // "user" or "group"
    Email string
}

type Domain struct {
    URN  string
    Name string
}

type Deprecation struct {
    Deprecated  bool
    Note        string
    Replacement string
    DeprecatedAt *time.Time
}

type ColumnContext struct {
    Name          string
    Description   string
    Tags          []string
    GlossaryTerms []GlossaryTermRef
    IsPrimaryKey  bool
    IsForeignKey  bool
}

type SearchFilter struct {
    Query    string
    Type     string // "dataset", "dashboard", etc.
    Platform string
    Domain   string
    Tags     []string
    Limit    int
}

type TableSearchResult struct {
    URN         string
    Name        string
    Description string
    Platform    string
    Type        string
}

type LineageDirection string

const (
    LineageUpstream   LineageDirection = "upstream"
    LineageDownstream LineageDirection = "downstream"
)

type LineageInfo struct {
    Entities      []LineageEntity
    Relationships []LineageRelationship
}
```

### Implementations

**Platform catalog** (`internal/platform/catalogsemantic`): the provider the
platform builds from its database. It is internal: a library user gets it by
giving the platform a database, and replaces it with `WithSemanticProvider`.

**No-op Provider** (`pkg/semantic/noop.go`):

```go
import "github.com/txn2/mcp-data-platform/pkg/semantic"

provider := semantic.NewNoopProvider()
```

**Cached Provider** (`pkg/semantic/cache.go`):

```go
import "github.com/txn2/mcp-data-platform/pkg/semantic"

cached := semantic.NewCachedProvider(baseProvider, semantic.CacheConfig{
    TTL: 5 * time.Minute,
})
```

A by-URN read that reports `semantic.ErrNotFound` is cached for the same TTL as
one that returns an entity, and replayed as that error: a catalog that holds no
entity for a reference has answered, and the answer costs the catalog the same
to produce. Most tables a deployment queries are not in its catalog, so without
this every enriched tool call re-reads every one of them. A read that fails for
any other reason (a catalog that could not be reached, a rejected credential) is
not cached, since it says nothing about what the catalog holds.

---

## Query Provider

The query provider interface abstracts access to query engines like Trino.

### Interface

```go
type Provider interface {
    Name() string

    // Table resolution
    ResolveTable(ctx context.Context, urn string) (*TableIdentifier, error)

    // Availability
    GetTableAvailability(ctx context.Context, urn string) (*TableAvailability, error)

    // Examples
    GetQueryExamples(ctx context.Context, urn string) ([]QueryExample, error)

    // Execution context
    GetExecutionContext(ctx context.Context, urns []string) (*ExecutionContext, error)

    // Schema
    GetTableSchema(ctx context.Context, table TableIdentifier) (*TableSchema, error)

    Close() error
}
```

### Types

```go
type TableIdentifier struct {
    Catalog string
    Schema  string
    Table   string
}

type TableAvailability struct {
    Queryable       bool
    Connection      string
    TableIdentifier *TableIdentifier
    SampleQuery     string
    RowCount        *int64
    RowCountNote    string
    Reason          string // If not queryable
}

type QueryExample struct {
    Query          string
    Description    string
    User           string
    ExecutedAt     *time.Time
    ExecutionCount int
}

type ExecutionContext struct {
    Tables      map[string]*TableAvailability
    Connections []string
}

type TableSchema struct {
    Columns      []ColumnSchema
    PrimaryKeys  []string
    Partitioning []string
}

type ColumnSchema struct {
    Name        string
    Type        string
    Nullable    bool
    Comment     string
    IsPartition bool
}
```

### Implementations

**Trino Adapter** (`pkg/query/trino/adapter.go`):

```go
import "github.com/txn2/mcp-data-platform/pkg/query/trino"

provider, err := trino.NewAdapter(client)
```

**No-op Provider** (`pkg/query/noop.go`):

```go
import "github.com/txn2/mcp-data-platform/pkg/query"

provider := query.NewNoopProvider()
```

---

## Provider Configuration

### Via YAML

```yaml
semantic:
  provider: noop         # Optional: turn the platform catalog off as the semantic layer

query:
  provider: trino
  instance: primary
```

### Via Code

```go
import (
    "github.com/txn2/mcp-data-platform/pkg/platform"
)

// The platform catalog, from the configured database
p, _ := platform.New(platform.WithConfig(cfg))

// A custom provider
customProvider := mypackage.NewCustomSemanticProvider(...)
p, _ := platform.New(
    platform.WithConfig(cfg),
    platform.WithSemanticProvider(customProvider),
)
```

## Implementing Custom Providers

### Custom Semantic Provider

```go
package myprovider

import (
    "context"
    "github.com/txn2/mcp-data-platform/pkg/semantic"
)

type Provider struct {
    client *MyMetadataClient
}

func New(endpoint string) (*Provider, error) {
    client, err := NewMyMetadataClient(endpoint)
    if err != nil {
        return nil, err
    }
    return &Provider{client: client}, nil
}

func (p *Provider) Name() string {
    return "my-semantic-provider"
}

func (p *Provider) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
    // Implement lookup against your metadata system
    meta, err := p.client.GetTableMeta(ctx, table.Catalog, table.Schema, table.Table)
    if err != nil {
        return nil, err
    }

    return &semantic.TableContext{
        Description:  meta.Description,
        Owners:       convertOwners(meta.Owners),
        Tags:         meta.Tags,
        QualityScore: meta.Quality,
    }, nil
}

// Implement remaining interface methods...

func (p *Provider) Close() error {
    return p.client.Close()
}
```

### Custom Query Provider

```go
package myprovider

import (
    "context"
    "github.com/txn2/mcp-data-platform/pkg/query"
)

type Provider struct {
    client *MyQueryClient
}

func (p *Provider) GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error) {
    // Parse URN and check if table exists
    table, err := p.resolveURN(urn)
    if err != nil {
        return &query.TableAvailability{
            Queryable: false,
            Reason:    "Could not resolve URN",
        }, nil
    }

    exists, err := p.client.TableExists(ctx, table)
    if err != nil {
        return nil, err
    }

    if !exists {
        return &query.TableAvailability{
            Queryable: false,
            Reason:    "Table not found",
        }, nil
    }

    return &query.TableAvailability{
        Queryable:       true,
        Connection:      p.Name(),
        TableIdentifier: table,
        SampleQuery:     fmt.Sprintf("SELECT * FROM %s LIMIT 10", table.FullName()),
    }, nil
}

// Implement remaining interface methods...
```

---

## Error Handling

Providers should return `nil` results (not errors) for expected "not found" cases:

```go
func (p *Provider) GetTableContext(ctx context.Context, table TableIdentifier) (*TableContext, error) {
    meta, err := p.client.GetMeta(ctx, table)
    if err == ErrNotFound {
        return nil, nil // Not an error, just no metadata
    }
    if err != nil {
        return nil, err // Actual error
    }
    return convertMeta(meta), nil
}
```

This allows the enrichment middleware to gracefully skip enrichment when metadata isn't available.

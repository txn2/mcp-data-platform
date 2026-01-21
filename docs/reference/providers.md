# Providers Reference

Providers abstract access to external services for semantic metadata, query execution, and storage operations.

## Semantic Provider

The semantic provider interface abstracts access to metadata catalogs like DataHub.

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

**DataHub Adapter** (`pkg/semantic/datahub/adapter.go`):

```go
import "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"

provider, err := datahub.NewAdapter(client)
```

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

## Storage Provider

The storage provider interface abstracts access to object storage like S3.

### Interface

```go
type Provider interface {
    Name() string

    // Dataset availability
    GetDatasetAvailability(ctx context.Context, urn string) (*DatasetAvailability, error)

    // Object info
    GetObjectInfo(ctx context.Context, bucket, key string) (*ObjectInfo, error)

    Close() error
}
```

### Types

```go
type DatasetAvailability struct {
    Available      bool
    Connection     string
    Bucket         string
    Prefix         string
    ObjectCount    *int
    TotalSizeBytes *int64
    LastModified   *time.Time
    Format         string
    Reason         string // If not available
}

type ObjectInfo struct {
    Bucket       string
    Key          string
    Size         int64
    ContentType  string
    LastModified time.Time
    ETag         string
    Metadata     map[string]string
}
```

### Implementations

**S3 Adapter** (`pkg/storage/s3/adapter.go`):

```go
import "github.com/txn2/mcp-data-platform/pkg/storage/s3"

provider, err := s3.NewAdapter(client)
```

**No-op Provider** (`pkg/storage/noop.go`):

```go
import "github.com/txn2/mcp-data-platform/pkg/storage"

provider := storage.NewNoopProvider()
```

---

## Provider Configuration

### Via YAML

```yaml
semantic:
  provider: datahub      # Provider type
  instance: primary      # Toolkit instance name
  cache:
    enabled: true
    ttl: 5m

query:
  provider: trino
  instance: primary

storage:
  provider: s3
  instance: primary
```

### Via Code

```go
import (
    "github.com/txn2/mcp-data-platform/pkg/platform"
    "github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Use built-in provider from toolkit
p, _ := platform.New(
    platform.WithDataHubToolkit("primary", datahubCfg),
    platform.WithSemanticProvider("datahub", "primary"),
)

// Use custom provider
customProvider := mypackage.NewCustomSemanticProvider(...)
p, _ := platform.New(
    platform.WithCustomSemanticProvider(customProvider),
)
```

---

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

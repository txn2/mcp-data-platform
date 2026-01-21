# Go Library

The platform is designed for customization. Import it as a library and build what you need.

## When to Use

The YAML-configured server works for standard setups. Use the library when you need to:

- **Add tools** - Your domain-specific operations
- **Swap providers** - Different semantic layer, different query engine
- **Write middleware** - Custom auth, logging, rate limiting
- **Embed it** - MCP inside a larger application

## Packages

| Package | What it does |
|---------|--------------|
| `pkg/platform` | Main entry point, orchestration |
| `pkg/toolkits/*` | Trino, DataHub, S3 adapters |
| `pkg/semantic` | Semantic provider interface |
| `pkg/query` | Query provider interface |
| `pkg/middleware` | Request/response processing |
| `pkg/persona` | Role-based tool filtering |
| `pkg/auth` | OIDC and API key validation |

## Key Interfaces

**Platform** - wire everything together:

```go
p, err := platform.New(
    platform.WithDataHubToolkit("primary", datahubCfg),
    platform.WithTrinoToolkit("primary", trinoCfg),
)
```

**Providers** - swap implementations:

```go
type SemanticProvider interface {
    GetTableContext(ctx context.Context, table TableIdentifier) (*TableContext, error)
    SearchTables(ctx context.Context, filter SearchFilter) ([]TableSearchResult, error)
}
```

**Middleware** - intercept requests:

```go
type Middleware func(next Handler) Handler
```

## Minimal Example

DataHub only:

```go
package main

import (
    "log"
    "os"
    "github.com/txn2/mcp-data-platform/pkg/platform"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
)

func main() {
    p, err := platform.New(
        platform.WithServerName("my-data-platform"),
        platform.WithDataHubToolkit("primary", datahub.Config{
            URL:   os.Getenv("DATAHUB_URL"),
            Token: os.Getenv("DATAHUB_TOKEN"),
        }),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer p.Close()
    p.Run()
}
```

## Next Steps

- [Quick Start](quickstart.md) - Working examples
- [Architecture](architecture.md) - How it fits together
- [Extensibility](extensibility.md) - Add your own components

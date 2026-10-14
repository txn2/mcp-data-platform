---
description: Build custom MCP servers with the Go library. Add tools, swap providers, write middleware. Full control over the data platform.
---

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
| `pkg/toolkits/*` | Trino, S3, gateway, search and knowledge toolkits |
| `pkg/catalog` | The platform catalog's model and reference form |
| `pkg/semantic` | Semantic provider interface |
| `pkg/query` | Query provider interface |
| `pkg/middleware` | Request/response processing |
| `pkg/persona` | Role-based tool filtering |
| `pkg/auth` | OIDC and API key validation |

## Key Interfaces

**Platform** - wire everything together:

```go
cfg, err := platform.LoadConfig("platform.yaml")
if err != nil {
    log.Fatal(err)
}
p, err := platform.New(platform.WithConfig(cfg))
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

```go
package main

import (
    "context"
    "log"

    "github.com/txn2/mcp-data-platform/pkg/platform"
)

func main() {
    cfg, err := platform.LoadConfig("platform.yaml")
    if err != nil {
        log.Fatal(err)
    }
    p, err := platform.New(platform.WithConfig(cfg))
    if err != nil {
        log.Fatal(err)
    }
    defer p.Close()
    if err := p.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
    // p.MCPServer() is the *mcp.Server to serve over the transport you choose.
}
```

With `database.dsn` set, the platform catalog is the semantic provider;
`platform.WithSemanticProvider` replaces it with your own implementation.

## Next Steps

- [Quick Start](quickstart.md) - Working examples
- [Architecture](architecture.md) - How it fits together
- [Extensibility](extensibility.md) - Add your own components

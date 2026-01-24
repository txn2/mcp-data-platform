# Extensibility

mcp-data-platform is designed for extension. You can add custom toolkits, providers, and middleware to build specialized MCP servers.

## Custom Toolkits

Create a toolkit that wraps your domain-specific tools.

### Toolkit Interface

```go
type Toolkit interface {
    Kind() string
    Name() string
    RegisterTools(s *mcp.Server)
    Tools() []string
    SetSemanticProvider(provider semantic.Provider)
    SetQueryProvider(provider query.Provider)
    Close() error
}
```

### Example: Custom Toolkit

```go
package mytoolkit

import (
    "context"
    "encoding/json"
    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/txn2/mcp-data-platform/pkg/query"
    "github.com/txn2/mcp-data-platform/pkg/semantic"
)

type Config struct {
    Endpoint string
    APIKey   string
}

type Toolkit struct {
    name   string
    config Config
    client *MyClient

    semanticProvider semantic.Provider
    queryProvider    query.Provider
}

func New(name string, cfg Config) (*Toolkit, error) {
    client, err := NewMyClient(cfg.Endpoint, cfg.APIKey)
    if err != nil {
        return nil, err
    }

    return &Toolkit{
        name:   name,
        config: cfg,
        client: client,
    }, nil
}

func (t *Toolkit) Kind() string { return "mytoolkit" }
func (t *Toolkit) Name() string { return t.name }

func (t *Toolkit) RegisterTools(s *mcp.Server) {
    s.AddTool(mcp.Tool{
        Name:        "mytoolkit_do_something",
        Description: "Does something useful",
        InputSchema: mcp.ToolInputSchema{
            Type: "object",
            Properties: map[string]any{
                "input": map[string]any{
                    "type":        "string",
                    "description": "The input to process",
                },
            },
            Required: []string{"input"},
        },
    }, t.handleDoSomething)
}

func (t *Toolkit) handleDoSomething(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    // Parse arguments
    var args struct {
        Input string `json:"input"`
    }
    if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
        return mcp.NewToolResultError(err.Error()), nil
    }

    // Do the work
    result, err := t.client.DoSomething(ctx, args.Input)
    if err != nil {
        return mcp.NewToolResultError(err.Error()), nil
    }

    return mcp.NewToolResultText(result), nil
}

func (t *Toolkit) Tools() []string {
    return []string{"mytoolkit_do_something"}
}

func (t *Toolkit) SetSemanticProvider(p semantic.Provider) { t.semanticProvider = p }
func (t *Toolkit) SetQueryProvider(p query.Provider)       { t.queryProvider = p }

func (t *Toolkit) Close() error {
    return t.client.Close()
}
```

### Register Custom Toolkit

```go
import (
    "github.com/txn2/mcp-data-platform/pkg/platform"
    "mycompany/mytoolkit"
)

func main() {
    myTk, _ := mytoolkit.New("primary", mytoolkit.Config{
        Endpoint: "https://api.example.com",
        APIKey:   os.Getenv("MY_API_KEY"),
    })

    p, _ := platform.New(
        platform.WithServerName("custom-platform"),
        platform.WithCustomToolkit(myTk),
    )

    p.Run()
}
```

## Custom Providers

Create custom semantic, query, or storage providers.

### Semantic Provider

```go
package mysemanticprovider

import (
    "context"
    "github.com/txn2/mcp-data-platform/pkg/semantic"
)

type Provider struct {
    client *MyMetadataClient
}

func New(endpoint, token string) (*Provider, error) {
    client, err := NewMyMetadataClient(endpoint, token)
    if err != nil {
        return nil, err
    }
    return &Provider{client: client}, nil
}

func (p *Provider) Name() string {
    return "my-semantic-provider"
}

func (p *Provider) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
    // Look up metadata from your system
    metadata, err := p.client.GetMetadata(ctx, table.Catalog, table.Schema, table.Table)
    if err != nil {
        return nil, err
    }

    return &semantic.TableContext{
        Description:  metadata.Description,
        Owners:       convertOwners(metadata.Owners),
        Tags:         metadata.Tags,
        Domain:       convertDomain(metadata.Domain),
        QualityScore: metadata.QualityScore,
    }, nil
}

func (p *Provider) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
    results, err := p.client.Search(ctx, filter.Query, filter.Limit)
    if err != nil {
        return nil, err
    }

    var out []semantic.TableSearchResult
    for _, r := range results {
        out = append(out, semantic.TableSearchResult{
            URN:         r.URN,
            Name:        r.Name,
            Description: r.Description,
            Platform:    r.Platform,
        })
    }
    return out, nil
}

// Implement other interface methods...

func (p *Provider) Close() error {
    return p.client.Close()
}
```

### Register Custom Provider

```go
import (
    "github.com/txn2/mcp-data-platform/pkg/platform"
    "mycompany/mysemanticprovider"
)

func main() {
    semanticProvider, _ := mysemanticprovider.New(
        "https://metadata.example.com",
        os.Getenv("METADATA_TOKEN"),
    )

    p, _ := platform.New(
        platform.WithServerName("custom-platform"),
        platform.WithTrinoToolkit("primary", trinoCfg),
        platform.WithCustomSemanticProvider(semanticProvider),
        platform.WithEnrichment(platform.EnrichmentConfig{
            TrinoSemanticEnrichment: true,
        }),
    )

    p.Run()
}
```

## Custom Middleware

Add request processing logic at the MCP protocol level.

### Middleware Interface

MCP middleware intercepts requests at the protocol level:

```go
// MCP middleware signature from the go-sdk
type Middleware func(next MethodHandler) MethodHandler
type MethodHandler func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error)
```

### Example: Logging Middleware

```go
package mymiddleware

import (
    "context"
    "log"
    "time"

    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/txn2/mcp-data-platform/pkg/middleware"
)

func LoggingMiddleware(logger *log.Logger) mcp.Middleware {
    return func(next mcp.MethodHandler) mcp.MethodHandler {
        return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
            // Only log tools/call requests
            if method != "tools/call" {
                return next(ctx, method, req)
            }

            start := time.Now()

            // Get platform context (set by MCPToolCallMiddleware)
            pc := middleware.GetPlatformContext(ctx)

            logger.Printf("Starting tool call: %s (user: %s)",
                pc.ToolName,
                pc.UserID,
            )

            // Call next handler
            result, err := next(ctx, method, req)

            // Log completion
            duration := time.Since(start)
            if err != nil {
                logger.Printf("Tool call failed: %s (duration: %v, error: %v)",
                    pc.ToolName, duration, err)
            } else {
                logger.Printf("Tool call completed: %s (duration: %v)",
                    pc.ToolName, duration)
            }

            return result, err
        }
    }
}
```

### Example: Rate Limiting Middleware

```go
package mymiddleware

import (
    "context"
    "sync"
    "time"

    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/txn2/mcp-data-platform/pkg/middleware"
)

type RateLimiter struct {
    mu       sync.Mutex
    requests map[string][]time.Time
    limit    int
    window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
    return &RateLimiter{
        requests: make(map[string][]time.Time),
        limit:    limit,
        window:   window,
    }
}

func (r *RateLimiter) Middleware() mcp.Middleware {
    return func(next mcp.MethodHandler) mcp.MethodHandler {
        return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
            // Only rate limit tools/call
            if method != "tools/call" {
                return next(ctx, method, req)
            }

            pc := middleware.GetPlatformContext(ctx)
            userID := pc.UserID

            if !r.allow(userID) {
                return middleware.NewToolResultError("rate limit exceeded"), nil
            }

            return next(ctx, method, req)
        }
    }
}

func (r *RateLimiter) allow(userID string) bool {
    r.mu.Lock()
    defer r.mu.Unlock()

    now := time.Now()
    cutoff := now.Add(-r.window)

    // Clean old requests
    var valid []time.Time
    for _, t := range r.requests[userID] {
        if t.After(cutoff) {
            valid = append(valid, t)
        }
    }
    r.requests[userID] = valid

    // Check limit
    if len(valid) >= r.limit {
        return false
    }

    // Record request
    r.requests[userID] = append(r.requests[userID], now)
    return true
}
```

### Register Custom Middleware

Register middleware on the MCP server using `AddReceivingMiddleware()`:

```go
import (
    "log"
    "os"
    "time"

    "github.com/txn2/mcp-data-platform/pkg/platform"
    "mycompany/mymiddleware"
)

func main() {
    logger := log.New(os.Stdout, "[MCP] ", log.LstdFlags)
    rateLimiter := mymiddleware.NewRateLimiter(100, time.Minute)

    p, _ := platform.New(
        platform.WithServerName("custom-platform"),
        platform.WithTrinoToolkit("primary", trinoCfg),
    )

    // Add custom middleware to the MCP server
    p.Server().AddReceivingMiddleware(mymiddleware.LoggingMiddleware(logger))
    p.Server().AddReceivingMiddleware(rateLimiter.Middleware())

    p.Run()
}
```

## Custom Tool Handlers

Override or wrap existing tool handlers.

```go
import (
    "context"
    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/txn2/mcp-data-platform/pkg/platform"
)

func main() {
    p, _ := platform.New(
        platform.WithServerName("custom-platform"),
        platform.WithTrinoToolkit("primary", trinoCfg),
    )

    // Override a tool handler
    p.Server().AddTool(mcp.Tool{
        Name:        "trino_query",
        Description: "Execute SQL query with custom validation",
        // ... schema
    }, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        // Custom validation
        if !validateQuery(req) {
            return mcp.NewToolResultError("query validation failed"), nil
        }

        // Delegate to original handler
        return p.Toolkits().Get("trino", "primary").HandleQuery(ctx, req)
    })

    p.Run()
}
```

## Extension Points Summary

| Extension | Interface | Use Case |
|-----------|-----------|----------|
| Custom Toolkit | `Toolkit` | Add new tool categories |
| Semantic Provider | `semantic.Provider` | Custom metadata source |
| Query Provider | `query.Provider` | Custom query engine |
| Storage Provider | `storage.Provider` | Custom storage backend |
| Middleware | `middleware.Middleware` | Request/response processing |
| Tool Handler | MCP handler func | Override tool behavior |

## Best Practices

**Keep toolkits focused:**
Each toolkit should have a clear purpose. Don't mix unrelated tools in one toolkit.

**Make providers cacheable:**
Implement caching at the provider level or wrap with `semantic.CachedProvider`.

**Middleware order matters:**
Order middleware from outermost (runs first) to innermost. Auth should run before authz.

**Handle errors gracefully:**
Return tool errors via `mcp.NewToolResultError()` rather than Go errors for expected failures.

**Clean up resources:**
Implement `Close()` on all custom components and ensure the platform calls them.

## Next Steps

- [Providers Reference](../reference/providers.md) - Provider interface details
- [Middleware Reference](../reference/middleware.md) - Middleware patterns
- [Architecture](architecture.md) - System design

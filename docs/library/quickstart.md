# Library Quick Start

Build a custom MCP server using the mcp-data-platform library.

## Prerequisites

- Go 1.24+
- Access to Trino, DataHub, or S3 (at least one)

## Create a New Project

```bash
mkdir my-mcp-server
cd my-mcp-server
go mod init my-mcp-server
```

## Add Dependencies

```bash
go get github.com/txn2/mcp-data-platform
```

## Basic Server

Create `main.go`:

```go
package main

import (
    "log"
    "os"

    "github.com/txn2/mcp-data-platform/pkg/platform"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

func main() {
    // Create platform with Trino toolkit
    p, err := platform.New(
        platform.WithServerName("my-mcp-server"),
        platform.WithTrinoToolkit("primary", trino.Config{
            Host:    getEnv("TRINO_HOST", "localhost"),
            Port:    8080,
            User:    getEnv("TRINO_USER", "admin"),
            Catalog: "memory",
            Schema:  "default",
        }),
    )
    if err != nil {
        log.Fatalf("Failed to create platform: %v", err)
    }
    defer p.Close()

    // Run the server (stdio transport by default)
    if err := p.Run(); err != nil {
        log.Fatalf("Server error: %v", err)
    }
}

func getEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}
```

## Build and Run

```bash
go build -o my-mcp-server
./my-mcp-server
```

## Add to Claude Code

```bash
claude mcp add my-mcp-server -- ./my-mcp-server
```

## Adding Multiple Toolkits

```go
package main

import (
    "log"
    "os"

    "github.com/txn2/mcp-data-platform/pkg/platform"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

func main() {
    p, err := platform.New(
        platform.WithServerName("data-platform"),

        // Trino toolkit
        platform.WithTrinoToolkit("primary", trino.Config{
            Host:    os.Getenv("TRINO_HOST"),
            Port:    443,
            User:    os.Getenv("TRINO_USER"),
            SSL:     true,
            Catalog: "hive",
        }),

        // DataHub toolkit
        platform.WithDataHubToolkit("primary", datahub.Config{
            URL:   os.Getenv("DATAHUB_URL"),
            Token: os.Getenv("DATAHUB_TOKEN"),
        }),

        // S3 toolkit
        platform.WithS3Toolkit("primary", s3.Config{
            Region:   "us-east-1",
            ReadOnly: true,
        }),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer p.Close()

    if err := p.Run(); err != nil {
        log.Fatal(err)
    }
}
```

## Enabling Cross-Injection

```go
p, err := platform.New(
    platform.WithServerName("data-platform"),

    // Toolkits
    platform.WithTrinoToolkit("primary", trinoCfg),
    platform.WithDataHubToolkit("primary", datahubCfg),

    // Set up providers for cross-injection
    platform.WithSemanticProvider("datahub", "primary"),
    platform.WithQueryProvider("trino", "primary"),

    // Enable enrichment
    platform.WithEnrichment(platform.EnrichmentConfig{
        TrinoSemanticEnrichment:  true,
        DataHubQueryEnrichment:   true,
    }),
)
```

## Adding Authentication

```go
import "github.com/txn2/mcp-data-platform/pkg/auth"

p, err := platform.New(
    platform.WithServerName("secure-platform"),

    // API key authentication
    platform.WithAPIKeyAuth([]auth.APIKeyConfig{
        {
            Key:   os.Getenv("API_KEY_ADMIN"),
            Name:  "admin",
            Roles: []string{"admin"},
        },
        {
            Key:   os.Getenv("API_KEY_ANALYST"),
            Name:  "analyst",
            Roles: []string{"analyst"},
        },
    }),

    // Toolkits
    platform.WithTrinoToolkit("primary", trinoCfg),
)
```

## Adding Personas

```go
import "github.com/txn2/mcp-data-platform/pkg/persona"

p, err := platform.New(
    platform.WithServerName("persona-platform"),

    // Define personas
    platform.WithPersona("analyst", persona.Persona{
        DisplayName: "Data Analyst",
        Roles:       []string{"analyst"},
        Tools: persona.ToolRules{
            Allow: []string{"trino_*", "datahub_*"},
            Deny:  []string{"*_delete_*"},
        },
    }),

    platform.WithPersona("admin", persona.Persona{
        DisplayName: "Administrator",
        Roles:       []string{"admin"},
        Tools: persona.ToolRules{
            Allow: []string{"*"},
        },
        Priority: 100,
    }),

    platform.WithDefaultPersona("analyst"),

    // Toolkits
    platform.WithTrinoToolkit("primary", trinoCfg),
)
```

## Using SSE Transport

```go
p, err := platform.New(
    platform.WithServerName("sse-platform"),
    platform.WithTransport("sse"),
    platform.WithAddress(":8080"),

    platform.WithTrinoToolkit("primary", trinoCfg),
)
```

## Loading from YAML Config

```go
import "github.com/txn2/mcp-data-platform/pkg/platform"

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

    if err := p.Run(); err != nil {
        log.Fatal(err)
    }
}
```

## Complete Example

```go
package main

import (
    "log"
    "os"

    "github.com/txn2/mcp-data-platform/pkg/auth"
    "github.com/txn2/mcp-data-platform/pkg/persona"
    "github.com/txn2/mcp-data-platform/pkg/platform"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
    "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

func main() {
    p, err := platform.New(
        // Server config
        platform.WithServerName("enterprise-data-platform"),
        platform.WithTransport("stdio"),

        // Authentication
        platform.WithAPIKeyAuth([]auth.APIKeyConfig{
            {Key: os.Getenv("API_KEY"), Name: "default", Roles: []string{"analyst"}},
        }),

        // Personas
        platform.WithPersona("analyst", persona.Persona{
            DisplayName: "Data Analyst",
            Roles:       []string{"analyst"},
            Tools: persona.ToolRules{
                Allow: []string{"trino_*", "datahub_*", "s3_list_*", "s3_get_*"},
                Deny:  []string{"s3_put_*", "s3_delete_*"},
            },
        }),
        platform.WithDefaultPersona("analyst"),

        // Toolkits
        platform.WithTrinoToolkit("primary", trino.Config{
            Host:    os.Getenv("TRINO_HOST"),
            Port:    443,
            User:    os.Getenv("TRINO_USER"),
            SSL:     true,
            Catalog: "hive",
        }),
        platform.WithDataHubToolkit("primary", datahub.Config{
            URL:   os.Getenv("DATAHUB_URL"),
            Token: os.Getenv("DATAHUB_TOKEN"),
        }),
        platform.WithS3Toolkit("primary", s3.Config{
            Region:   "us-east-1",
            ReadOnly: true,
        }),

        // Cross-injection
        platform.WithSemanticProvider("datahub", "primary"),
        platform.WithQueryProvider("trino", "primary"),
        platform.WithStorageProvider("s3", "primary"),
        platform.WithEnrichment(platform.EnrichmentConfig{
            TrinoSemanticEnrichment:  true,
            DataHubQueryEnrichment:   true,
            S3SemanticEnrichment:     true,
        }),
    )
    if err != nil {
        log.Fatalf("Failed to create platform: %v", err)
    }
    defer p.Close()

    log.Println("Starting enterprise data platform...")
    if err := p.Run(); err != nil {
        log.Fatalf("Platform error: %v", err)
    }
}
```

## Next Steps

- [Architecture](architecture.md) - Understand the system design
- [Extensibility](extensibility.md) - Add custom components

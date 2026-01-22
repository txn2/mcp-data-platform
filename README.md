# txn2/mcp-data-platform

[![GitHub license](https://img.shields.io/github/license/txn2/mcp-data-platform.svg)](https://github.com/txn2/mcp-data-platform/blob/main/LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/txn2/mcp-data-platform.svg)](https://pkg.go.dev/github.com/txn2/mcp-data-platform)
[![codecov](https://codecov.io/gh/txn2/mcp-data-platform/graph/badge.svg)](https://codecov.io/gh/txn2/mcp-data-platform)
[![Go Report Card](https://goreportcard.com/badge/github.com/txn2/mcp-data-platform)](https://goreportcard.com/report/github.com/txn2/mcp-data-platform)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/txn2/mcp-data-platform/badge)](https://scorecard.dev/viewer/?uri=github.com/txn2/mcp-data-platform)
[![SLSA 3](https://slsa.dev/images/gh-badge-level3.svg)](https://slsa.dev)

A semantic data platform MCP server that composes multiple data tools with **bidirectional cross-injection** - tool responses automatically include critical context from other services.

## Features

- **Semantic-First Data Access**: All data queries include business context from DataHub
- **Bidirectional Cross-Injection**:
  - Trino results enriched with DataHub metadata (owners, tags, deprecation)
  - DataHub searches include query availability from Trino
  - S3 listings enriched with matching DataHub datasets
  - DataHub S3 searches include storage availability
- **Enterprise Security**: Fail-closed authentication, TLS enforcement, prompt injection protection
- **OAuth 2.1 Authentication**: OIDC, API keys, PKCE, Dynamic Client Registration
- **Role-Based Personas**: Tool filtering with wildcard patterns (allow/deny rules)
- **Comprehensive Audit Logging**: PostgreSQL-backed audit trail
- **Middleware Architecture**: Extensible request/response processing

> **Security Architecture**: For a detailed analysis of securing internal MCP servers, see [MCP Defense: A Case Study in AI Security](https://imti.co/mcp-defense/).
 
## Architecture

```mermaid
graph LR
    subgraph "MCP Data Platform"
        DataHub[DataHub<br/>Semantic Metadata]
        Platform[Platform<br/>Bridge]
        Trino[Trino<br/>Query Engine]
        S3[S3<br/>Object Storage]

        DataHub <-->|"enrichment"| Platform
        Platform <-->|"enrichment"| Trino
        Platform <-->|"enrichment"| S3
    end

    Client([MCP Client]) --> Platform
    Platform --> Client
```

**Cross-Injection Flow:**
- **Trino → DataHub**: Query results include owners, tags, glossary terms, deprecation warnings
- **DataHub → Trino**: Search results include query availability and sample SQL
- **S3 → DataHub**: Object listings include matching dataset metadata from DataHub
- **DataHub → S3**: Search results for S3 datasets include storage availability

## Security

mcp-data-platform implements a **fail-closed** security model designed for enterprise deployments. See [MCP Defense: A Case Study in AI Security](https://imti.co/mcp-defense/) for the security architecture rationale.

### Security Features

| Feature | Description |
|---------|-------------|
| **Fail-Closed Authentication** | Missing or invalid credentials deny access (never bypass) |
| **Required JWT Claims** | Tokens must include `sub` and `exp` claims |
| **TLS for SSE Transport** | Configurable TLS with warnings for plaintext connections |
| **Prompt Injection Protection** | Metadata sanitization prevents injection attacks |
| **Read-Only Mode** | Trino and S3 toolkits support enforced read-only access |
| **Default-Deny Personas** | Users without explicit persona assignment have no tool access |
| **Cryptographic Request IDs** | Request tracing uses secure random identifiers |

### Transport Security

| Transport | Authentication | TLS |
|-----------|---------------|-----|
| **stdio** | Not required (local execution) | N/A |
| **SSE** | Required (Bearer token or API key) | Strongly recommended |

### SSE Security Configuration

```yaml
server:
  transport: sse
  address: ":8443"
  tls:
    enabled: true
    cert_file: /path/to/cert.pem
    key_file: /path/to/key.pem

auth:
  allow_anonymous: false  # Default: authentication required
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"

personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_query", "datahub_search"]
        deny: ["*_delete_*", "*_drop_*"]
  default_persona: analyst  # Required: no implicit access
```

## Installation

### Go Install

```bash
go install github.com/txn2/mcp-data-platform/cmd/mcp-data-platform@latest
```

### From Source

```bash
git clone https://github.com/txn2/mcp-data-platform.git
cd mcp-data-platform
go build -o mcp-data-platform ./cmd/mcp-data-platform
```

## Quick Start

### Standalone Server

```bash
# Run with stdio transport (default)
./mcp-data-platform

# Run with configuration file
./mcp-data-platform --config configs/platform.yaml

# Run with SSE transport
./mcp-data-platform --transport sse --address :8080
```

### Claude Code CLI

```bash
claude mcp add mcp-data-platform -- mcp-data-platform
```

### Claude Desktop (Local)

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "mcp-data-platform": {
      "command": "mcp-data-platform",
      "args": ["--config", "/path/to/platform.yaml"]
    }
  }
}
```

### Claude Desktop (Remote with OAuth)

For connecting Claude Desktop to a remote MCP server with Keycloak authentication:

1. **Configure the MCP server** with OAuth and upstream IdP:

```yaml
server:
  transport: sse
  address: ":8080"

oauth:
  enabled: true
  issuer: "https://mcp.example.com"
  clients:
    - id: "claude-desktop"
      secret: "${CLAUDE_CLIENT_SECRET}"
      redirect_uris:
        - "http://localhost"
        - "http://127.0.0.1"
  upstream:
    issuer: "https://keycloak.example.com/realms/your-realm"
    client_id: "mcp-data-platform"
    client_secret: "${KEYCLOAK_CLIENT_SECRET}"
    redirect_uri: "https://mcp.example.com/oauth/callback"
```

2. **In Claude Desktop**, add the server with OAuth credentials:
   - **URL**: `https://mcp.example.com`
   - **Client ID**: `claude-desktop`
   - **Client Secret**: (the secret you configured)

When you connect, Claude Desktop will open your browser for Keycloak login, then automatically complete the OAuth flow.

See [OAuth 2.1 Server documentation](https://txn2.github.io/mcp-data-platform/auth/oauth-server/) for complete setup instructions.

## Configuration

Create a `platform.yaml` configuration file:

```yaml
server:
  name: mcp-data-platform
  transport: stdio

auth:
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"
  api_keys:
    enabled: true
    keys:
      - key: "${API_KEY_ADMIN}"
        name: "admin"
        roles: ["admin"]

personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_*", "datahub_*"]
        deny: ["*_delete_*"]
    admin:
      display_name: "Administrator"
      roles: ["admin"]
      tools:
        allow: ["*"]
  default_persona: analyst

semantic:
  provider: datahub
  cache:
    enabled: true
    ttl: 5m

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

database:
  dsn: "${DATABASE_URL}"
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string for audit logs | - |
| `API_KEY_ADMIN` | Admin API key (if using API key auth) | - |

## Core Packages

| Package | Description |
|---------|-------------|
| `pkg/platform` | Main platform facade and configuration |
| `pkg/auth` | OIDC and API key authentication |
| `pkg/oauth` | OAuth 2.1 server with DCR and PKCE |
| `pkg/persona` | Role-based personas and tool filtering |
| `pkg/semantic` | Semantic metadata provider abstraction |
| `pkg/query` | Query execution provider abstraction |
| `pkg/middleware` | Request/response middleware chain |
| `pkg/registry` | Toolkit registration and management |
| `pkg/audit` | Audit logging with PostgreSQL storage |
| `pkg/tuning` | Prompts, hints, and operational rules |
| `pkg/storage` | S3-compatible storage provider abstraction |
| `pkg/toolkits` | Toolkit implementations (Trino, DataHub, S3) |
| `pkg/client` | Platform client utilities |

## Development

```bash
# Run tests with race detection
go test -race ./...

# Run linter
golangci-lint run ./...

# Run security scan
gosec ./...

# Build
go build -o mcp-data-platform ./cmd/mcp-data-platform
```

## Library Usage

The platform can be imported and used as a library:

```go
import (
    "github.com/txn2/mcp-data-platform/pkg/platform"
)

// Load configuration
cfg, err := platform.LoadConfig("platform.yaml")
if err != nil {
    log.Fatal(err)
}

// Create platform
p, err := platform.New(platform.WithConfig(cfg))
if err != nil {
    log.Fatal(err)
}
defer p.Close()

// Start the platform
if err := p.Start(ctx); err != nil {
    log.Fatal(err)
}

// Access the MCP server
mcpServer := p.MCPServer()
```

## Contributing

We welcome contributions for bug fixes, tests, and documentation. Please ensure:

1. All tests pass (`go test -race ./...`)
2. Code is formatted (`gofmt`)
3. Linter passes (`golangci-lint run ./...`)
4. Security scan passes (`gosec ./...`)

## License

[Apache License 2.0](LICENSE)

---

Open source by [Craig Johnston](https://twitter.com/cjimti), sponsored by [Deasil Works, Inc.](https://deasil.works/)

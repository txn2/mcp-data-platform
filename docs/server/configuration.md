# Configuration

mcp-data-platform uses YAML configuration with environment variable expansion. Variables in the format `${VAR_NAME}` are replaced with their environment values at load time.

## Configuration File

Create a `platform.yaml` file:

```yaml
server:
  name: mcp-data-platform
  transport: stdio

toolkits:
  trino:
    primary:
      host: trino.example.com
      port: 443
      user: ${TRINO_USER}
      password: ${TRINO_PASSWORD}
      ssl: true
      catalog: hive
      schema: default

  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}

  s3:
    primary:
      region: us-east-1
      access_key_id: ${AWS_ACCESS_KEY_ID}
      secret_access_key: ${AWS_SECRET_ACCESS_KEY}

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  s3_semantic_enrichment: true
```

## Server Configuration

```yaml
server:
  name: mcp-data-platform      # Server name reported to clients
  transport: stdio             # stdio or sse
  address: ":8080"             # Listen address for SSE
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | `mcp-data-platform` | Server name in MCP handshake |
| `transport` | string | `stdio` | Transport protocol: `stdio` or `sse` |
| `address` | string | `:8080` | Listen address for SSE transport |
| `tls.enabled` | bool | `false` | Enable TLS for SSE transport |
| `tls.cert_file` | string | - | Path to TLS certificate |
| `tls.key_file` | string | - | Path to TLS private key |

!!! warning "SSE Transport Security"
    When using SSE transport without TLS, a warning is logged. For production deployments, always enable TLS to encrypt credentials in transit.

## Authentication Configuration

```yaml
auth:
  allow_anonymous: false       # Require authentication (default)
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"
    audience: "mcp-data-platform"
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"
    clock_skew_seconds: 30     # Allowed clock drift
    max_token_age: 24h         # Reject tokens older than this
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: "admin"
        roles: ["admin"]
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allow_anonymous` | bool | `false` | Allow unauthenticated requests |
| `oidc.enabled` | bool | `false` | Enable OIDC authentication |
| `oidc.issuer` | string | - | OIDC issuer URL |
| `oidc.client_id` | string | - | OAuth client ID |
| `oidc.audience` | string | - | Expected token audience |
| `oidc.role_claim_path` | string | `roles` | Path to roles in token claims |
| `oidc.role_prefix` | string | - | Filter roles to those with this prefix |
| `oidc.clock_skew_seconds` | int | `30` | Allowed clock skew for time claims |
| `oidc.max_token_age` | duration | `0` | Max token age (0 = no limit) |
| `api_keys.enabled` | bool | `false` | Enable API key authentication |
| `api_keys.keys` | array | - | List of API key configurations |

!!! note "Fail-Closed Security"
    Authentication follows a fail-closed model. Missing tokens, invalid signatures, expired tokens, or missing required claims (`sub`, `exp`) all result in denied access.

## Toolkit Configuration

### Trino

```yaml
toolkits:
  trino:
    primary:                   # Instance name (can be any identifier)
      host: trino.example.com
      port: 443
      user: analyst
      password: ${TRINO_PASSWORD}
      catalog: hive
      schema: default
      ssl: true
      ssl_verify: true
      timeout: 120s
      default_limit: 1000
      max_limit: 10000
      read_only: false
      connection_name: primary
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | **required** | Trino coordinator hostname |
| `port` | int | 8080 (443 if SSL) | Trino coordinator port |
| `user` | string | **required** | Trino username |
| `password` | string | - | Trino password (if auth enabled) |
| `catalog` | string | - | Default catalog |
| `schema` | string | - | Default schema |
| `ssl` | bool | `false` | Enable SSL/TLS |
| `ssl_verify` | bool | `true` | Verify SSL certificates |
| `timeout` | duration | `120s` | Query timeout |
| `default_limit` | int | `1000` | Default row limit for queries |
| `max_limit` | int | `10000` | Maximum allowed row limit |
| `read_only` | bool | `false` | Restrict to read-only queries |
| `connection_name` | string | instance name | Display name for this connection |

### DataHub

```yaml
toolkits:
  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}
      timeout: 30s
      default_limit: 10
      max_limit: 100
      max_lineage_depth: 5
      connection_name: primary
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `url` | string | **required** | DataHub GMS URL |
| `token` | string | - | DataHub access token |
| `timeout` | duration | `30s` | API request timeout |
| `default_limit` | int | `10` | Default search result limit |
| `max_limit` | int | `100` | Maximum search result limit |
| `max_lineage_depth` | int | `5` | Maximum lineage traversal depth |
| `connection_name` | string | instance name | Display name for this connection |

### S3

```yaml
toolkits:
  s3:
    primary:
      region: us-east-1
      endpoint: ""                    # Custom endpoint for MinIO, etc.
      access_key_id: ${AWS_ACCESS_KEY_ID}
      secret_access_key: ${AWS_SECRET_ACCESS_KEY}
      session_token: ""
      profile: ""                     # AWS profile name
      use_path_style: false           # Use path-style URLs
      timeout: 30s
      disable_ssl: false
      read_only: true                 # Restrict to read operations
      max_get_size: 10485760          # 10MB
      max_put_size: 104857600         # 100MB
      connection_name: primary
      bucket_prefix: ""               # Filter to buckets with this prefix
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `region` | string | `us-east-1` | AWS region |
| `endpoint` | string | - | Custom S3 endpoint (for MinIO, etc.) |
| `access_key_id` | string | - | AWS access key ID |
| `secret_access_key` | string | - | AWS secret access key |
| `session_token` | string | - | AWS session token (for temporary creds) |
| `profile` | string | - | AWS credentials profile name |
| `use_path_style` | bool | `false` | Use path-style S3 URLs |
| `timeout` | duration | `30s` | Request timeout |
| `disable_ssl` | bool | `false` | Disable SSL (for local testing) |
| `read_only` | bool | `false` | Restrict to read operations |
| `max_get_size` | int64 | `10485760` | Max bytes to read from objects |
| `max_put_size` | int64 | `104857600` | Max bytes to write to objects |
| `connection_name` | string | instance name | Display name for this connection |
| `bucket_prefix` | string | - | Only show buckets with this prefix |

## Cross-Injection Configuration

```yaml
injection:
  trino_semantic_enrichment: true    # Add DataHub context to Trino results
  datahub_query_enrichment: true     # Add Trino availability to DataHub results
  s3_semantic_enrichment: true       # Add DataHub context to S3 results
  datahub_storage_enrichment: true   # Add S3 availability to DataHub results
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trino_semantic_enrichment` | bool | `false` | Enrich Trino results with DataHub metadata |
| `datahub_query_enrichment` | bool | `false` | Add query availability to DataHub search results |
| `s3_semantic_enrichment` | bool | `false` | Enrich S3 results with DataHub metadata |
| `datahub_storage_enrichment` | bool | `false` | Add S3 availability to DataHub results |

## Semantic and Query Provider Configuration

Specify which toolkit instance provides semantic metadata and query execution:

```yaml
semantic:
  provider: datahub           # Provider type: datahub or noop
  instance: primary           # Which DataHub instance to use
  cache:
    enabled: true
    ttl: 5m

query:
  provider: trino             # Provider type: trino or noop
  instance: primary           # Which Trino instance to use

storage:
  provider: s3                # Provider type: s3 or noop
  instance: primary           # Which S3 instance to use
```

## Environment Variables

Common environment variables:

| Variable | Description |
|----------|-------------|
| `TRINO_USER` | Trino username |
| `TRINO_PASSWORD` | Trino password |
| `DATAHUB_TOKEN` | DataHub access token |
| `AWS_ACCESS_KEY_ID` | AWS access key |
| `AWS_SECRET_ACCESS_KEY` | AWS secret key |
| `AWS_SESSION_TOKEN` | AWS session token |
| `DATABASE_URL` | PostgreSQL connection string (for audit/OAuth) |

## Complete Example

```yaml
server:
  name: mcp-data-platform
  transport: stdio

toolkits:
  trino:
    primary:
      host: trino.example.com
      port: 443
      user: ${TRINO_USER}
      password: ${TRINO_PASSWORD}
      ssl: true
      catalog: hive
      schema: default
      default_limit: 1000
      max_limit: 10000

  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}
      default_limit: 10
      max_limit: 100

  s3:
    primary:
      region: us-east-1
      read_only: true

semantic:
  provider: datahub
  instance: primary
  cache:
    enabled: true
    ttl: 5m

query:
  provider: trino
  instance: primary

storage:
  provider: s3
  instance: primary

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  s3_semantic_enrichment: true

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

database:
  dsn: ${DATABASE_URL}

personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_query", "trino_explain", "datahub_*"]
        deny: ["*_delete_*"]
  default_persona: analyst
```

## Persona Configuration

Personas define tool access based on user roles. The security model follows a **default-deny** approach.

```yaml
personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst", "data_engineer"]
      tools:
        allow: ["trino_*", "datahub_*"]
        deny: ["*_delete_*", "*_drop_*"]
    admin:
      display_name: "Administrator"
      roles: ["admin"]
      tools:
        allow: ["*"]
  default_persona: analyst
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `definitions` | map | - | Named persona configurations |
| `definitions.<name>.display_name` | string | - | Human-readable name |
| `definitions.<name>.roles` | array | - | Roles that map to this persona |
| `definitions.<name>.tools.allow` | array | `[]` | Allowed tool patterns |
| `definitions.<name>.tools.deny` | array | `[]` | Denied tool patterns |
| `default_persona` | string | - | Persona for users without role match |

!!! warning "Default-Deny Security"
    Users without a resolved persona have **no tool access**. The built-in default persona denies all tools. You must define explicit personas with tool access for your users.

## Next Steps

- [Tools](tools.md) - Available tools and parameters
- [Multi-Provider](multi-provider.md) - Configure multiple instances
- [Authentication](../auth/overview.md) - Add authentication
- [Personas](../personas/overview.md) - Role-based access control

# Configuration

mcp-data-platform uses YAML configuration with environment variable expansion. Variables in the format `${VAR_NAME}` are replaced with their environment values at load time.

## How Configuration Works

The platform has two configuration modes that control how settings are stored and whether they can be changed at runtime:

**File mode** (default): Configuration is loaded from a YAML file at startup and is read-only. This is the simplest deployment — no database required.

**Database mode**: Adding `database.dsn` unlocks persistent platform features (audit logging, knowledge capture, session externalization). Setting `config_store.mode: database` additionally enables runtime configuration mutations through the admin API.

| What you configure | What it unlocks |
|--------------------|-----------------|
| YAML file only | Read-only config, in-memory sessions, no audit |
| `database.dsn` | Audit logging, knowledge capture, OAuth persistence, database-backed sessions |
| `database.dsn` + `config_store.mode: database` | All of the above, plus runtime config mutations via admin API |
| `database.dsn` + `admin.enabled: true` | REST endpoints for system health, config, personas, auth keys, audit |

See [Operating Modes](operating-modes.md) for the full comparison and [Admin API](admin-api.md) for the REST endpoints.

## Configuration File

Create a `platform.yaml` file:

```yaml
apiVersion: v1

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

## Config Versioning

Every configuration file should include an `apiVersion` field as the first key. This enables safe schema evolution with deprecation warnings and migration tooling.

```yaml
apiVersion: v1

server:
  name: mcp-data-platform
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiVersion` | string | `v1` | Config schema version. Omitting defaults to `v1` for backward compatibility. |

**Supported versions**: `v1` (current)

### Version Lifecycle

- **current**: Actively supported, no warnings
- **deprecated**: Still works, emits a warning at startup with migration guidance
- **removed**: Rejected at startup with an error pointing to the migration tool

### Migration Tool

Migrate config files to the latest version:

```bash
# From file to stdout
mcp-data-platform migrate-config --config platform.yaml

# From stdin to file
cat platform.yaml | mcp-data-platform migrate-config --output migrated.yaml

# Specify target version
mcp-data-platform migrate-config --config platform.yaml --target-version v1
```

The migration tool preserves `${VAR}` environment variable references.

## Server Configuration

```yaml
server:
  name: mcp-data-platform      # Server name reported to clients
  transport: stdio             # stdio or http
  address: ":8080"             # Listen address for HTTP transports
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | `mcp-data-platform` | Server name in MCP handshake |
| `transport` | string | `stdio` | Transport protocol: `stdio` or `http` (`sse` accepted for backward compatibility) |
| `address` | string | `:8080` | Listen address for HTTP transports |
| `tls.enabled` | bool | `false` | Enable TLS for HTTP transport |
| `tls.cert_file` | string | - | Path to TLS certificate |
| `tls.key_file` | string | - | Path to TLS private key |

!!! warning "HTTP Transport Security"
    When using HTTP transport without TLS, a warning is logged. For production deployments, always enable TLS to encrypt credentials in transit.

### Streamable HTTP Configuration

The HTTP transport serves both legacy SSE (`/sse`, `/message`) and Streamable HTTP (`/`) endpoints. Streamable HTTP session behavior is configured under `server.streamable`:

```yaml
server:
  streamable:
    session_timeout: 30m
    stateless: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `session_timeout` | duration | `30m` | How long an idle session persists before cleanup |
| `stateless` | bool | `false` | Disable session tracking (no `Mcp-Session-Id` validation) |

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

## Database Configuration

The `database` block configures the PostgreSQL connection used by audit logging, knowledge capture, session externalization, OAuth persistence, and (optionally) the config store.

```yaml
database:
  dsn: ${DATABASE_URL}
  max_open_conns: 25
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dsn` | string | - | PostgreSQL connection string |
| `max_open_conns` | int | `25` | Maximum open database connections |

!!! note "What the database unlocks"
    Setting `dsn` enables audit logging, knowledge capture, session externalization, and OAuth persistence. Without it, these features degrade to in-memory or noop implementations.

## Config Store

The `config_store` block controls where platform configuration is persisted. By default, configuration is loaded from the YAML file and is read-only. Setting mode to `database` enables runtime config mutations via the admin API.

```yaml
config_store:
  mode: file      # "file" (default) or "database"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | string | `file` | Config storage mode: `file` or `database` |

**`file` mode**: Configuration loaded from YAML at startup. Read-only. Admin API mutation endpoints (config import, persona CRUD, auth key CRUD) return `409 Conflict`. This is the default and requires no database.

**`database` mode**: Configuration persisted to PostgreSQL `config_versions` table. Requires `database.dsn` to be configured. Supports import, export, history, and runtime mutations via the admin API. On startup, bootstrap fields (`server`, `database`, `auth`, `admin`, `config_store`, `apiVersion`) are always loaded from the YAML file and override database values.

See [Operating Modes](operating-modes.md) for the full comparison of deployment configurations.

## Tool Visibility Configuration

The `tools` block controls which tools appear in `tools/list` responses. This is a **visibility filter** for reducing LLM token usage — it hides tools from discovery but does not affect authorization. Persona-level tool filtering (see [Tool Filtering](../personas/tool-filtering.md)) remains the security boundary for `tools/call`.

```yaml
tools:
  allow:
    - "trino_*"
    - "datahub_*"
  deny:
    - "*_delete_*"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tools.allow` | array | `[]` | Tool name patterns to include in `tools/list` |
| `tools.deny` | array | `[]` | Tool name patterns to exclude from `tools/list` |

**Semantics:**

- No patterns configured: all tools visible (default)
- Allow only: only matching tools appear
- Deny only: all tools appear except denied
- Both: allow patterns are evaluated first, then deny removes from that set

Patterns use `filepath.Match` syntax — `*` matches any sequence of non-separator characters. For example, `trino_*` matches `trino_query` and `trino_describe_table`.

!!! tip "When to use this"
    Deployments that only use a subset of toolkits (e.g., only Trino) can hide unused tools to save tokens. A full tool list is 25-32 tools; filtering to `trino_*` reduces it to 7.

!!! warning "Not a security boundary"
    Tool visibility filtering only affects `tools/list` responses. A user who knows a tool name can still call it via `tools/call` if their persona allows it. Use persona tool filtering for access control.

## Admin API Configuration

The `admin` block enables and configures the REST API for system health, configuration management, persona CRUD, auth key management, and audit queries.

```yaml
admin:
  enabled: true
  portal: true
  persona: admin
  path_prefix: /api/v1/admin
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable admin REST API |
| `portal` | bool | `false` | Enable the admin web portal UI |
| `persona` | string | `admin` | Persona required for admin access |
| `path_prefix` | string | `/api/v1/admin` | URL prefix for admin endpoints |

!!! note "HTTP transport required"
    The admin API is served over HTTP. It is not available when running in `stdio` transport mode.

The admin portal provides a web-based dashboard for audit log exploration, tool execution testing, and system monitoring. When enabled, it is served at the admin path prefix (e.g., `/api/v1/admin/`). See [Admin API](admin-api.md) for the full endpoint reference.

## Audit Configuration

The `audit` block controls audit logging of MCP tool calls. Audit events are written asynchronously to PostgreSQL.

```yaml
audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable audit logging |
| `log_tool_calls` | bool | `false` | Log MCP tool call events |
| `retention_days` | int | `90` | Days to retain audit events |

!!! note "Requires database"
    Audit logging requires `database.dsn` to be configured. Both `enabled` and `log_tool_calls` must be `true` for tool call events to be recorded.

See [Audit Logging](audit.md) for query examples and retention details.

## Session Configuration

The `sessions` block controls how MCP session state is stored. In-memory sessions are lost on restart; database-backed sessions survive restarts and support multi-replica deployments.

```yaml
sessions:
  store: database
  ttl: 30m
  idle_timeout: 30m
  cleanup_interval: 1m
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `store` | string | `memory` | Backend: `memory` or `database` |
| `ttl` | duration | streamable `session_timeout` | Session lifetime |
| `idle_timeout` | duration | streamable `session_timeout` | Idle eviction threshold |
| `cleanup_interval` | duration | `1m` | Cleanup routine interval |

!!! note "Requires database"
    The `database` store requires `database.dsn` to be configured.

See [Session Externalization](session-externalization.md) for architecture details and multi-replica considerations.

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

  # Session metadata deduplication (avoids repeating metadata for same table)
  session_dedup:
    enabled: true             # Default: true
    mode: reference           # reference (default), summary, none
    entry_ttl: 5m             # Defaults to semantic.cache.ttl
    session_timeout: 30m      # Defaults to server.streamable.session_timeout
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trino_semantic_enrichment` | bool | `false` | Enrich Trino results with DataHub metadata |
| `datahub_query_enrichment` | bool | `false` | Add query availability to DataHub search results |
| `s3_semantic_enrichment` | bool | `false` | Enrich S3 results with DataHub metadata |
| `datahub_storage_enrichment` | bool | `false` | Add S3 availability to DataHub results |
| `session_dedup.enabled` | bool | `true` | Whether session dedup is active |
| `session_dedup.mode` | string | `reference` | Repeat query content: `reference`, `summary`, `none` |
| `session_dedup.entry_ttl` | duration | semantic cache TTL | How long a table stays "already sent" |
| `session_dedup.session_timeout` | duration | streamable session timeout | Idle session cleanup interval |

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

## Knowledge Capture Configuration

Knowledge capture records domain knowledge shared during AI sessions and provides a workflow for applying approved insights to the DataHub catalog. See [Knowledge Capture](../knowledge/overview.md) for the full feature documentation.

```yaml
knowledge:
  enabled: true
  apply:
    enabled: true
    datahub_connection: primary
    require_confirmation: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable knowledge capture toolkit (`capture_insight` tool) |
| `apply.enabled` | bool | `false` | Enable the `apply_knowledge` tool for admin review and catalog write-back |
| `apply.datahub_connection` | string | - | DataHub instance name for write-back operations |
| `apply.require_confirmation` | bool | `false` | Require explicit `confirm: true` on apply actions |

!!! note "Prerequisites"
    Knowledge capture requires `database.dsn` to be configured. The `apply_knowledge` tool requires the admin persona.

## MCP Apps Configuration

MCP Apps provide interactive UI components that enhance tool results. The platform provides the infrastructure; you provide the HTML/JS/CSS apps.

```yaml
mcpapps:
  enabled: true
  apps:
    query_results:
      enabled: true
      assets_path: "/etc/mcp-apps/query-results"
      tools:
        - trino_query
      csp:
        resource_domains:
          - "https://cdn.jsdelivr.net"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable MCP Apps infrastructure |
| `apps` | map | - | Named app configurations |
| `apps.<name>.enabled` | bool | `true` | Enable this app |
| `apps.<name>.assets_path` | string | **required** | Absolute path to app directory |
| `apps.<name>.tools` | array | **required** | Tools this app enhances |
| `apps.<name>.csp.resource_domains` | array | - | Allowed CDN origins |

See [MCP Apps Configuration](../mcpapps/configuration.md) for complete options.

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
apiVersion: v1

server:
  name: mcp-data-platform
  transport: http
  address: ":8080"

database:
  dsn: ${DATABASE_URL}

config_store:
  mode: database

admin:
  enabled: true
  portal: true
  persona: admin

# Hide unused tools from tools/list to save LLM tokens
tools:
  allow:
    - "trino_*"
    - "datahub_*"
    - "capture_insight"
  deny:
    - "*_delete_*"

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

sessions:
  store: database
  ttl: 30m
  idle_timeout: 30m
  cleanup_interval: 1m

auth:
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: "admin"
        roles: ["admin"]

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

personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_query", "trino_explain", "datahub_*"]
        deny: ["*_delete_*"]
    admin:
      display_name: "Administrator"
      roles: ["admin"]
      tools:
        allow: ["*"]
  default_persona: analyst
```

## Next Steps

- [Operating Modes](operating-modes.md) - Standalone, file + DB, and bootstrap + DB config modes
- [Admin API](admin-api.md) - REST endpoints for system, config, personas, auth keys, audit
- [Tools](tools.md) - Available tools and parameters
- [Multi-Provider](multi-provider.md) - Configure multiple instances
- [Authentication](../auth/overview.md) - Add authentication
- [Personas](../personas/overview.md) - Role-based access control
- [MCP Apps](../mcpapps/overview.md) - Interactive UI for tool results

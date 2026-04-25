# Configuration

mcp-data-platform uses YAML configuration with environment variable expansion. Variables in the format `${VAR_NAME}` are replaced with their environment values at load time.

## How Configuration Works

**File mode** (default): Configuration is loaded from a YAML file at startup. This is the simplest deployment — no database required.

**File + database**: Adding `database.dsn` unlocks persistent platform features (audit logging, knowledge capture, session externalization). When a database is available, individual config entries stored in the `config_entries` table override file defaults for whitelisted keys. Changes made via the admin API take effect immediately without restart. File defaults are preserved and used as fallback when database entries are deleted.

| What you configure | What it unlocks |
|--------------------|-----------------|
| YAML file only | Read-only config, in-memory sessions, no audit |
| `database.dsn` | Audit logging, knowledge capture, OAuth persistence, database-backed sessions, per-key config overrides via admin API |
| `database.dsn` + `admin.enabled: true` | REST endpoints for system health, config entries CRUD, personas, auth keys, audit |

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
  column_context_filtering: true     # Only include SQL-referenced columns (default: true)
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

### Prompts

The platform registers MCP prompts at three levels:

1. **Auto-registered `platform-overview`** — Built dynamically from `server.description` and enabled toolkits. Lists what the platform can do based on which toolkits (DataHub, Trino, S3, Portal, Knowledge) are configured.

2. **Operator-configured prompts** — Defined in `server.prompts`. Support typed arguments with `{placeholder}` substitution in content.

3. **Workflow prompts** — Registered automatically when required toolkits are present. Provide guided multi-step workflows (e.g., `explore-available-data`, `create-interactive-dashboard`, `create-a-report`, `trace-data-lineage`).

Operator-configured prompts override any auto-registered prompt with the same name. Toolkits (Portal, Knowledge) may also register their own prompts via the `PromptDescriber` interface.

```yaml
server:
  description: "ACME Corp analytics platform"
  prompts:
    - name: routing_rules
      description: "How to route queries between systems"
      content: |
        Before querying, determine if you need ENTITY STATE or ANALYTICS...
    - name: explore-topic
      description: "Explore data about a specific topic"
      content: "Find all datasets related to {topic} and summarize key metrics."
      arguments:
        - name: topic
          description: "The topic to explore"
          required: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `server.prompts[].name` | string | required | Prompt name |
| `server.prompts[].description` | string | - | Prompt description |
| `server.prompts[].content` | string | required | Prompt content (supports `{arg_name}` placeholders) |
| `server.prompts[].arguments` | array | `[]` | Typed arguments for the prompt |
| `server.prompts[].arguments[].name` | string | required | Argument name (maps to `{name}` in content) |
| `server.prompts[].arguments[].description` | string | - | Argument description shown to clients |
| `server.prompts[].arguments[].required` | bool | `false` | Whether the argument is required |

**Built-in workflow prompts:**

| Prompt | Required Toolkits | Description |
|--------|-------------------|-------------|
| `explore-available-data` | DataHub | Discover datasets about a topic |
| `create-interactive-dashboard` | DataHub, Trino, Portal | Full workflow: discover, query, visualize, save |
| `create-a-report` | DataHub, Trino | Discover data, query it, produce a Markdown report |
| `trace-data-lineage` | DataHub | Trace upstream/downstream lineage for a dataset |

All registered prompts (platform + toolkit) are included in the `platform_info` tool response and visible in the platform-info app's Prompts tab.

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

### Browser Sessions (OIDC Login for Portal UI)

When both `auth.oidc` and `auth.browser_session` are enabled, the portal UI offers SSO login via the configured OIDC provider. The flow uses authorization code with PKCE and stores the session in an HMAC-SHA256 signed JWT cookie.

```yaml
auth:
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"
    client_secret: "${OIDC_CLIENT_SECRET}"
    audience: "mcp-data-platform"
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"
    scopes: [openid, profile, email]
  browser_session:
    enabled: true
    signing_key: "${SESSION_SIGNING_KEY}"  # openssl rand -base64 32
    ttl: 8h
    secure: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `browser_session.enabled` | bool | `false` | Enable cookie-based browser sessions |
| `browser_session.signing_key` | string | - | Base64-encoded HMAC key (32+ bytes) |
| `browser_session.ttl` | duration | `8h` | Session lifetime |
| `browser_session.secure` | bool | `true` | HTTPS-only cookies (set `false` for local dev) |
| `browser_session.cookie_name` | string | `mcp_session` | Cookie name |
| `browser_session.domain` | string | - | Cookie domain restriction |

The portal UI automatically detects OIDC availability and shows an SSO button. API key authentication remains as a fallback. MCP protocol clients are unaffected — browser sessions only apply to the portal HTTP endpoints.

!!! warning "Session Limitations"
    Sessions are stateless (no server-side store). Individual sessions cannot be revoked. Rotating `signing_key` invalidates all active sessions. Users must re-authenticate after TTL expires.

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

When a database is available (`database.dsn` is set), the platform uses a granular key/value config store. Individual config entries in the `config_entries` table override file defaults for whitelisted keys. Changes made via the admin API take effect immediately (hot-reload) without restart. Deleting a database entry restores the file default for that key.

**Whitelisted keys (phase 1):**

| Key | Description |
|-----|-------------|
| `server.description` | Platform description shown in `platform-overview` prompt and `platform_info` tool |
| `server.agent_instructions` | Custom instructions appended to agent system prompts |

Only whitelisted keys can be set via the admin API. Attempting to set a non-whitelisted key returns `400 Bad Request`.

```yaml
config_store:
  mode: file      # Deprecated — ignored. Presence is accepted for backward compatibility.
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `config_store.mode` | string | `file` | **Deprecated.** Ignored at runtime. The config entries system activates automatically when `database.dsn` is set. Accepted without error for backward compatibility. |

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

Patterns use `filepath.Match` syntax — `*` matches any sequence of non-separator characters. For example, `trino_*` matches `trino_query`, `trino_execute`, and `trino_describe_table`.

!!! tip "When to use this"
    Deployments that only use a subset of toolkits (e.g., only Trino) can hide unused tools to save tokens. A full tool list is 26-33 tools; filtering to `trino_*` reduces it to 8.

!!! warning "Not a security boundary"
    Tool visibility filtering only affects `tools/list` responses. A user who knows a tool name can still call it via `tools/call` if their persona allows it. Use persona tool filtering for access control.

## Admin API Configuration

The `admin` block enables and configures the REST API for system health, configuration management, persona CRUD, auth key management, and audit queries.

```yaml
admin:
  enabled: true
  persona: admin
  path_prefix: /api/v1/admin
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable admin REST API |
| `persona` | string | `admin` | Persona required for admin access |
| `path_prefix` | string | `/api/v1/admin` | URL prefix for admin endpoints |

!!! note "HTTP transport required"
    The admin API is served over HTTP. It is not available when running in `stdio` transport mode.

The admin portal provides a web-based dashboard for audit log exploration, tool execution testing, and system monitoring. Enable with `portal.enabled: true`. When enabled, it is served at `/portal/`. See [Admin API](admin-api.md) for the full endpoint reference and [Admin Portal](admin-portal.md) for the visual guide.

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
      read_only: true
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
| `read_only` | bool | `false` | Restrict to read operations (disables write tools) |

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

### MCP Gateway

The `mcp` toolkit kind proxies upstream MCP servers and re-exposes their
tools as `<connection_name>__<remote_tool>`. **Connections are managed
exclusively through the admin portal** — no per-instance config goes in
`platform.yaml`. The only YAML knob is `enabled`, which turns the kind on.

```yaml
toolkits:
  mcp:
    enabled: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Register the gateway toolkit kind. When false, mcp connections in `connection_instances` are ignored. |

**Required environment for the OAuth + at-rest encryption path:**

| Variable | Required for | Notes |
|----------|--------------|-------|
| `ENCRYPTION_KEY` | Encrypted credentials in `connection_instances`, `gateway_oauth_tokens`, and `oauth_pkce_states` | 32-byte AES-256 key, base64-encoded. Without it, sensitive fields are stored in plaintext and the platform logs a warning. Required for any production gateway deployment. |
| `DATABASE_URL` | OAuth `authorization_code` grant (refresh-token persistence) and multi-replica deployments | Without a database, OAuth tokens live in process memory only and don't survive restarts. Multi-replica deployments additionally need this so PKCE state is shared across pods. |

See [Gateway Toolkit](gateway.md) for the connection-config reference,
auth modes (`none`/`bearer`/`api_key`/`oauth`), OAuth grant types
(`client_credentials` and `authorization_code` + PKCE), and the
cross-enrichment rule schema.

## Cross-Injection Configuration

```yaml
injection:
  trino_semantic_enrichment: true    # Add DataHub context to Trino results
  datahub_query_enrichment: true     # Add Trino availability to DataHub results
  s3_semantic_enrichment: true       # Add DataHub context to S3 results
  datahub_storage_enrichment: true   # Add S3 availability to DataHub results
  unwrap_json: true               # Auto-unwrap single-row VARCHAR-of-JSON (default: true)
  column_context_filtering: true     # Only include SQL-referenced columns (default: true)

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
| `unwrap_json` | bool | `true` | Auto-unwrap single-row VARCHAR-of-JSON results |
| `column_context_filtering` | bool | `true` | Limit column enrichment to SQL-referenced columns |
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
| `definitions.<name>.context.description_prefix` | string | - | Prepended to platform description |
| `definitions.<name>.context.description_override` | string | - | Replaces platform description entirely |
| `definitions.<name>.context.agent_instructions_suffix` | string | - | Appended to platform agent instructions |
| `definitions.<name>.context.agent_instructions_override` | string | - | Replaces platform agent instructions entirely |
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

## Memory Layer Configuration

The memory layer provides persistent memory for agent and analyst sessions with vector search, cross-injection, and staleness detection. See [Memory Layer](../memory/overview.md) for the full feature documentation.

```yaml
memory:
  enabled: true
  embedding:
    provider: ollama
    ollama:
      url: "http://localhost:11434"
      model: "nomic-embed-text"
      timeout: 30s
  staleness:
    enabled: true
    interval: 15m
    batch_size: 50
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` (when database available) | Enable memory layer. Set `false` to explicitly disable. |
| `embedding.provider` | string | `noop` | Embedding provider: `ollama` or `noop` |
| `embedding.ollama.url` | string | `http://localhost:11434` | Ollama API base URL |
| `embedding.ollama.model` | string | `nomic-embed-text` | Ollama embedding model (768-dim) |
| `embedding.ollama.timeout` | duration | `30s` | Embedding API timeout |
| `staleness.enabled` | bool | `false` | Enable background staleness watcher |
| `staleness.interval` | duration | `15m` | Staleness check interval |
| `staleness.batch_size` | int | `50` | Records per check cycle |

!!! note "Prerequisites"
    Memory requires `database.dsn` to be configured and the pgvector PostgreSQL extension installed. Memory tools are opt-in per persona (`memory_*` in `tools.allow`).

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
        - trino_execute
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

## Resource Templates Configuration

Resource templates expose platform data as browseable, parameterized MCP resources using RFC 6570 URI templates.

```yaml
resources:
  enabled: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable resource templates |

When enabled, the platform registers these resource templates:

- `schema://{catalog}.{schema}/{table}` — Table schema with column types and descriptions
- `glossary://{term}` — Glossary term definitions
- `availability://{catalog}.{schema}/{table}` — Query availability and row counts

Clients that support resource browsing (e.g., Claude Desktop) will show these as navigable resources alongside tools.

## Custom Resources Configuration

Custom resources let you expose arbitrary static content as named MCP resources — brand assets, operational limits, environment docs, or any structured blob that agents can read by URI. They are registered whenever `resources.custom` is non-empty, independent of `resources.enabled`.

```yaml
resources:
  custom:
    - uri: "brand://theme"
      name: "Brand Theme"
      description: "Primary brand colors and site URL"
      mime_type: "application/json"
      content: |
        {
          "colors": {"primary": "#FF6B35", "secondary": "#004E89"},
          "url": "https://example.com"
        }

    - uri: "brand://logo"
      name: "Brand Logo SVG"
      mime_type: "image/svg+xml"
      content_file: "/etc/platform/logo.svg"
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `uri` | string | Yes | Unique resource URI (e.g., `brand://theme`, `docs://limits`) |
| `name` | string | Yes | Human-readable name shown in `resources/list` |
| `description` | string | No | Optional description for MCP clients |
| `mime_type` | string | Yes | MIME type (e.g., `application/json`, `image/svg+xml`, `text/plain`) |
| `content` | string | One of | Inline content (text, JSON, SVG, etc.) |
| `content_file` | string | One of | Absolute path to a file; read on every request (supports hot-reload) |

`content` and `content_file` are mutually exclusive. Invalid entries (missing required fields, both or neither content fields set) are skipped with a warning at startup; valid entries in the same list are still registered.

## Progress Notifications Configuration

Progress notifications send granular updates to MCP clients during long-running Trino queries. The client must include `_meta.progressToken` in the request to receive updates.

```yaml
progress:
  enabled: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable progress notifications |

When enabled, Trino query execution sends progress updates including rows scanned, bytes processed, and query stage information. Clients that don't send a `progressToken` receive no notifications (zero overhead).

## Client Logging Configuration

Client logging sends server-to-client log messages via the MCP `logging/setLevel` protocol. Messages include enrichment decisions, timing data, and platform diagnostics.

```yaml
client_logging:
  enabled: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable client logging |

Zero overhead if the client hasn't subscribed via `logging/setLevel`. When active, log messages report semantic cache hits/misses, enrichment timing, and cross-injection decisions.

## Elicitation Configuration

Elicitation requests user confirmation before potentially expensive or sensitive operations. Requires client-side elicitation support (e.g., Claude Desktop). Gracefully degrades to a no-op if the client doesn't support elicitation.

```yaml
elicitation:
  enabled: true
  cost_estimation:
    enabled: true
    row_threshold: 1000000
  pii_consent:
    enabled: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable elicitation |
| `cost_estimation.enabled` | bool | `false` | Prompt before expensive queries |
| `cost_estimation.row_threshold` | int | `1000000` | Row count threshold from `EXPLAIN` IO estimates |
| `pii_consent.enabled` | bool | `false` | Prompt when query accesses PII-tagged columns |

!!! note "Client support required"
    Elicitation uses the MCP `elicitation/create` capability. Clients that don't support elicitation will not receive prompts — queries proceed without confirmation.

## Icons Configuration

Icons add visual metadata to tools, resources, and prompts in MCP list responses. Upstream toolkits (Trino, DataHub, S3) provide default icons; this configuration overrides or extends them.

```yaml
icons:
  enabled: true
  tools:
    trino_query:
      src: "https://example.com/custom-trino.svg"
      mime_type: "image/svg+xml"
  resources:
    "schema://{catalog}.{schema}/{table}":
      src: "https://example.com/schema.svg"
  prompts:
    knowledge_capture:
      src: "https://example.com/knowledge.svg"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable icon injection middleware |
| `tools` | map | - | Icon overrides keyed by tool name |
| `resources` | map | - | Icon overrides keyed by resource URI |
| `prompts` | map | - | Icon overrides keyed by prompt name |
| `*.src` | string | - | Icon source URL |
| `*.mime_type` | string | - | Icon MIME type (e.g., `image/svg+xml`) |

!!! tip "Default icons"
    Each upstream toolkit provides a default icon for all its tools. You only need this configuration if you want to customize or override those defaults.

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

portal:
  enabled: true

admin:
  enabled: true
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
  unwrap_json: true
  column_context_filtering: true

resources:
  enabled: true

progress:
  enabled: true

client_logging:
  enabled: true

elicitation:
  enabled: true
  cost_estimation:
    enabled: true
    row_threshold: 1000000

personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_query", "trino_execute", "trino_explain", "datahub_*"]
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
- [Middleware Reference](../reference/middleware.md) - Request processing chain details

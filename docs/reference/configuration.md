# Configuration Reference

Complete reference for all configuration options in `platform.yaml`.

## Configuration File Format

Configuration uses YAML with environment variable expansion:

```yaml
# Environment variables: ${VAR_NAME}
password: ${DB_PASSWORD}

# Nested structures
server:
  name: my-server
  transport: stdio
```

## Server Configuration

```yaml
server:
  name: "ACME Corp Data Platform"
  version: "1.0.0"
  description: |
    Use this MCP server for all questions about ACME Corp, including X Widget sales,
    Thing Mart inventory, customer analytics, and financial reporting. This is the
    authoritative source for ACME business data.
  tags:
    - "ACME Corp"
    - "X Widget"
    - "Thing Mart"
    - "sales"
    - "inventory"
    - "customers"
  agent_instructions: |
    Prices are in cents - divide by 100.
    Always filter mode = 'live'.
  prompts:
    - name: routing_rules
      description: "How to route queries between systems"
      content: |
        Before querying, determine if you need ENTITY STATE or ANALYTICS...
    - name: data_dictionary
      description: "Key business terms and definitions"
      content: |
        - ARR: Annual Recurring Revenue
        - MRR: Monthly Recurring Revenue
    - name: explore-topic
      description: "Explore data about a specific topic"
      content: "Find all datasets related to {topic} and summarize key metrics."
      arguments:
        - name: topic
          description: "The topic to explore"
          required: true
  transport: stdio
  address: ":8080"
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `server.name` | string | `mcp-data-platform` | Platform identity (e.g., "ACME Corp Data Platform") - helps agents identify which business this MCP serves |
| `server.version` | string | `1.0.0` | Server version |
| `server.description` | string | - | Explains when to use this MCP - what business, products, or domains it covers. Agents use this to route questions to the right MCP server. |
| `server.tags` | array | `[]` | Keywords for discovery: company names, product names, business domains. Agents match these against user questions. |
| `server.agent_instructions` | string | - | Operational guidance: data conventions, required filters, unit conversions. Returned in `platform_info` response. |
| `server.prompts` | array | `[]` | Platform-level MCP prompts registered via `prompts/list`. Operator-defined prompts override auto-registered workflow prompts with the same name. |
| `server.prompts[].name` | string | required | Prompt name |
| `server.prompts[].description` | string | - | Prompt description |
| `server.prompts[].content` | string | required | Prompt content returned by `prompts/get`. Supports `{arg_name}` placeholders substituted from arguments. |
| `server.prompts[].arguments` | array | `[]` | Typed arguments for the prompt |
| `server.prompts[].arguments[].name` | string | required | Argument name (used as `{name}` placeholder in content) |
| `server.prompts[].arguments[].description` | string | - | Argument description shown to clients |
| `server.prompts[].arguments[].required` | bool | `false` | Whether the argument is required |
| `server.transport` | string | `stdio` | Transport: `stdio`, `http` (`sse` accepted for backward compatibility) |
| `server.address` | string | `:8080` | Listen address for HTTP transports |
| `server.streamable.session_timeout` | duration | `30m` | How long an idle Streamable HTTP session persists before cleanup |
| `server.streamable.stateless` | bool | `false` | Disable session tracking (no `Mcp-Session-Id` validation) |
| `server.tls.enabled` | bool | `false` | Enable TLS |
| `server.tls.cert_file` | string | - | TLS certificate path |
| `server.tls.key_file` | string | - | TLS private key path |
| `server.shutdown.grace_period` | duration | `25s` | Max time to drain in-flight requests during shutdown |
| `server.shutdown.pre_shutdown_delay` | duration | `2s` | Sleep before draining for load balancer deregistration |

### Session Externalization

```yaml
sessions:
  store: memory
  ttl: 30m
  idle_timeout: 30m
  cleanup_interval: 1m
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `sessions.store` | string | `memory` | Session store backend: `memory` or `database` |
| `sessions.ttl` | duration | `streamable.session_timeout` | Session lifetime |
| `sessions.idle_timeout` | duration | `streamable.session_timeout` | Idle session eviction timeout |
| `sessions.cleanup_interval` | duration | `1m` | How often the cleanup routine removes expired sessions |

When `sessions.store` is `database`, the platform forces `server.streamable.stateless: true` and manages sessions in PostgreSQL. This enables zero-downtime restarts and horizontal scaling. Requires `database.dsn` to be configured.

When `sessions.store` is `memory` (default), the SDK manages sessions internally with no behavior change from previous versions.

## Authentication Configuration

### OIDC

```yaml
auth:
  oidc:
    enabled: true
    issuer: "https://auth.example.com"
    client_id: "mcp-data-platform"
    audience: "mcp-data-platform"
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `auth.oidc.enabled` | bool | `false` | Enable OIDC authentication |
| `auth.oidc.issuer` | string | - | OIDC issuer URL |
| `auth.oidc.client_id` | string | - | Expected client ID |
| `auth.oidc.audience` | string | client_id | Expected audience claim |
| `auth.oidc.role_claim_path` | string | - | JSON path to roles in token |
| `auth.oidc.role_prefix` | string | - | Prefix to filter/strip from roles |

### API Keys

```yaml
auth:
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: "admin"
        roles: ["admin"]
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `auth.api_keys.enabled` | bool | `false` | Enable API key authentication |
| `auth.api_keys.keys` | array | `[]` | List of API key definitions |
| `auth.api_keys.keys[].key` | string | - | The API key value |
| `auth.api_keys.keys[].name` | string | - | Key identifier |
| `auth.api_keys.keys[].roles` | array | `[]` | Roles assigned to this key |

## OAuth Server Configuration

!!! warning "Use OIDC or API Keys Instead"
    The built-in OAuth server adds complexity. For most deployments, OIDC with an existing identity provider or API keys are simpler and more secure.

```yaml
oauth:
  enabled: false
  issuer: "https://mcp.example.com"
  signing_key: "${OAUTH_SIGNING_KEY}"  # Generate: openssl rand -base64 32
  dcr:
    enabled: false  # Keep disabled - security risk
    allowed_redirect_patterns:
      - "http://localhost:*"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `oauth.enabled` | bool | `false` | Enable OAuth 2.1 server |
| `oauth.issuer` | string | - | OAuth issuer URL (your MCP server's public URL) |
| `oauth.signing_key` | string | auto-generated | HMAC key for JWT access tokens. Required for production. |
| `oauth.dcr.enabled` | bool | `false` | Enable Dynamic Client Registration (**not recommended**) |
| `oauth.dcr.allowed_redirect_patterns` | array | `[]` | Allowed redirect URI patterns |

### Browser Sessions

Enables cookie-based browser authentication for the portal UI using OIDC authorization code flow with PKCE. Requires `auth.oidc` to be enabled.

```yaml
auth:
  browser_session:
    enabled: true
    cookie_name: "mcp_session"      # Cookie name (default: mcp_session)
    signing_key: "${SESSION_KEY}"    # base64-encoded 32+ byte HMAC key
    ttl: 8h                         # Session lifetime (default: 8h)
    secure: true                    # HTTPS-only cookies (default: true)
    domain: ""                      # Cookie domain (empty = current host)
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `auth.browser_session.enabled` | bool | `false` | Enable browser session authentication |
| `auth.browser_session.cookie_name` | string | `mcp_session` | Session cookie name |
| `auth.browser_session.signing_key` | string | - | Base64-encoded HMAC-SHA256 key (32+ bytes). Generate: `openssl rand -base64 32` |
| `auth.browser_session.ttl` | duration | `8h` | Session cookie lifetime |
| `auth.browser_session.secure` | bool | `true` | Set `Secure` flag on cookies (disable only for local dev) |
| `auth.browser_session.domain` | string | - | Cookie domain restriction (empty = current host only) |

When enabled, the platform registers three HTTP endpoints:

- `GET /portal/auth/login` — Initiates OIDC authorization code flow with PKCE
- `GET /portal/auth/callback` — Processes the OIDC callback, creates session cookie
- `GET /portal/auth/logout` — Clears session cookie and redirects to OIDC end_session

The OIDC `client_secret` and `scopes` fields from the `auth.oidc` config are used for the browser session flow.

!!! note "Session Limitations"
    Sessions are stateless JWT cookies signed with HMAC-SHA256. This means:

    - **No individual session revocation** — disabled users remain authenticated until cookie expires
    - **No key rotation support** — rotating `signing_key` invalidates all active sessions immediately
    - **No session refresh** — users must re-authenticate after TTL expires (default: 8h)

    For deployments requiring immediate revocation, consider shorter TTL values.

## Database Configuration

```yaml
database:
  dsn: "postgres://user:pass@localhost/platform"
  max_open_conns: 25
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `database.dsn` | string | - | PostgreSQL connection string |
| `database.max_open_conns` | int | `25` | Maximum open connections |

## Personas Configuration

```yaml
personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      description: "Read-only data access"
      roles: ["analyst"]
      tools:
        allow: ["trino_*", "datahub_*"]
        deny: ["*_delete_*"]
      context:
        description_prefix: "You are helping a data analyst."
        agent_instructions_suffix: "Prefer aggregations for large tables."
      priority: 10

  default_persona: analyst

  role_mapping:
    oidc_to_persona:
      "realm_analyst": "analyst"
    user_personas:
      "admin@example.com": "admin"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `personas.definitions` | map | `{}` | Persona definitions keyed by name |
| `personas.definitions.<name>.display_name` | string | - | Human-readable name |
| `personas.definitions.<name>.description` | string | - | Persona description |
| `personas.definitions.<name>.roles` | array | `[]` | Roles that map to this persona |
| `personas.definitions.<name>.tools.allow` | array | `["*"]` | Tool allow patterns |
| `personas.definitions.<name>.tools.deny` | array | `[]` | Tool deny patterns |
| `personas.definitions.<name>.context.description_prefix` | string | - | Prepended to platform description in `platform_info` |
| `personas.definitions.<name>.context.description_override` | string | - | Replaces platform description entirely (takes precedence over prefix) |
| `personas.definitions.<name>.context.agent_instructions_suffix` | string | - | Appended to platform agent instructions in `platform_info` |
| `personas.definitions.<name>.context.agent_instructions_override` | string | - | Replaces platform agent instructions entirely (takes precedence over suffix) |
| `personas.definitions.<name>.priority` | int | `0` | Selection priority |
| `personas.default_persona` | string | `default` | Default persona name |
| `personas.role_mapping.oidc_to_persona` | map | `{}` | OIDC role to persona mapping |
| `personas.role_mapping.user_personas` | map | `{}` | User-specific persona mapping |

## Toolkits Configuration

### Trino

```yaml
toolkits:
  trino:
    <instance_name>:
      host: "trino.example.com"
      port: 443
      user: "analyst"
      password: ${TRINO_PASSWORD}
      catalog: "hive"
      schema: "default"
      ssl: true
      ssl_verify: true
      timeout: 120s
      default_limit: 1000
      max_limit: 10000
      read_only: false
      connection_name: "Production"
      descriptions:
        trino_query: "Execute SQL with automatic semantic enrichment from DataHub"
        trino_describe_table: "Get table schema with DataHub context — the richest single-call way to understand a table"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `host` | string | **required** | Trino coordinator hostname |
| `port` | int | 8080/443 | Trino coordinator port |
| `user` | string | **required** | Trino username |
| `password` | string | - | Trino password |
| `catalog` | string | - | Default catalog |
| `schema` | string | - | Default schema |
| `ssl` | bool | `false` | Enable SSL/TLS |
| `ssl_verify` | bool | `true` | Verify SSL certificates |
| `timeout` | duration | `120s` | Query timeout |
| `default_limit` | int | `1000` | Default row limit |
| `max_limit` | int | `10000` | Maximum row limit |
| `read_only` | bool | `false` | Restrict to read-only queries |
| `connection_name` | string | instance name | Display name |
| `descriptions` | map | `{}` | Override tool descriptions (key: tool name, value: description text) |

### DataHub

```yaml
toolkits:
  datahub:
    <instance_name>:
      url: "https://datahub.example.com"
      token: ${DATAHUB_TOKEN}
      timeout: 30s
      default_limit: 10
      max_limit: 100
      max_lineage_depth: 5
      connection_name: "Primary Catalog"
      debug: false
      read_only: true
      descriptions:
        datahub_search: "Search the data catalog for datasets and dashboards"
        datahub_get_entity: "Get full metadata for a catalog entity by URN"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `url` | string | **required** | DataHub GMS URL |
| `token` | string | - | DataHub access token |
| `timeout` | duration | `30s` | API request timeout |
| `default_limit` | int | `10` | Default search limit |
| `max_limit` | int | `100` | Maximum search limit |
| `max_lineage_depth` | int | `5` | Maximum lineage depth |
| `connection_name` | string | instance name | Display name |
| `debug` | bool | `false` | Enable debug logging for GraphQL operations |
| `read_only` | bool | `false` | Restrict to read operations (disables `datahub_create`, `datahub_update`, `datahub_delete`) |
| `descriptions` | map | `{}` | Override tool descriptions (key: tool name, value: description text) |

### S3

```yaml
toolkits:
  s3:
    <instance_name>:
      region: "us-east-1"
      endpoint: ""
      access_key_id: ${AWS_ACCESS_KEY_ID}
      secret_access_key: ${AWS_SECRET_ACCESS_KEY}
      session_token: ""
      profile: ""
      use_path_style: false
      timeout: 30s
      disable_ssl: false
      read_only: true
      max_get_size: 10485760
      max_put_size: 104857600
      connection_name: "Data Lake"
      bucket_prefix: ""
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `region` | string | `us-east-1` | AWS region |
| `endpoint` | string | - | Custom S3 endpoint |
| `access_key_id` | string | - | AWS access key ID |
| `secret_access_key` | string | - | AWS secret access key |
| `session_token` | string | - | AWS session token |
| `profile` | string | - | AWS profile name |
| `use_path_style` | bool | `false` | Use path-style URLs |
| `timeout` | duration | `30s` | Request timeout |
| `disable_ssl` | bool | `false` | Disable SSL |
| `read_only` | bool | `false` | Restrict to read operations |
| `max_get_size` | int64 | `10485760` | Max bytes to read (10MB) |
| `max_put_size` | int64 | `104857600` | Max bytes to write (100MB) |
| `connection_name` | string | instance name | Display name |
| `bucket_prefix` | string | - | Filter buckets by prefix |

## Provider Configuration

```yaml
semantic:
  provider: datahub
  instance: primary
  cache:
    enabled: true
    ttl: 5m
  lineage:
    enabled: true
    max_hops: 2
    inherit:
      - glossary_terms
      - descriptions
      - tags
    prefer_column_lineage: true
    conflict_resolution: nearest
    cache_ttl: 10m
    timeout: 5s
    column_transforms:
      - target_pattern: "elasticsearch.*.rxtxmsg.payload.*"
        strip_prefix: "rxtxmsg.payload."
  urn_mapping:
    platform: postgres
    catalog_mapping:
      rdbms: warehouse

query:
  provider: trino
  instance: primary

storage:
  provider: s3
  instance: primary
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `semantic.provider` | string | - | Provider type: `datahub`, `noop` |
| `semantic.instance` | string | - | Toolkit instance name |
| `semantic.cache.enabled` | bool | `false` | Enable caching |
| `semantic.cache.ttl` | duration | `5m` | Cache TTL |
| `semantic.lineage.enabled` | bool | `false` | Enable lineage-aware semantic enrichment |
| `semantic.lineage.max_hops` | int | `2` | Maximum lineage hops to traverse |
| `semantic.lineage.inherit` | array | `[]` | Metadata to inherit: `glossary_terms`, `descriptions`, `tags` |
| `semantic.lineage.prefer_column_lineage` | bool | `false` | Use fine-grained column lineage when available |
| `semantic.lineage.conflict_resolution` | string | `nearest` | Conflict resolution: `nearest`, `all` |
| `semantic.lineage.cache_ttl` | duration | `10m` | Lineage cache TTL |
| `semantic.lineage.timeout` | duration | `5s` | Lineage lookup timeout |
| `semantic.lineage.column_transforms` | array | `[]` | Column path transforms for nested structures |
| `semantic.urn_mapping.platform` | string | `trino` | Platform name for DataHub URNs |
| `semantic.urn_mapping.catalog_mapping` | map | `{}` | Map Trino catalogs to DataHub catalogs |
| `query.provider` | string | - | Provider type: `trino`, `noop` |
| `query.instance` | string | - | Toolkit instance name |
| `query.urn_mapping.catalog_mapping` | map | `{}` | Map DataHub catalogs to Trino catalogs (reverse) |
| `storage.provider` | string | - | Provider type: `s3`, `noop` |
| `storage.instance` | string | - | Toolkit instance name |

### URN Mapping

When Trino catalog or platform names differ from DataHub metadata, configure URN mapping:

```yaml
semantic:
  provider: datahub
  instance: primary
  urn_mapping:
    # DataHub platform (e.g., postgres, mysql, trino)
    platform: postgres
    # Map Trino catalogs to DataHub catalogs
    catalog_mapping:
      rdbms: warehouse      # Trino "rdbms" → DataHub "warehouse"
      iceberg: datalake     # Trino "iceberg" → DataHub "datalake"

query:
  provider: trino
  instance: primary
  urn_mapping:
    # Reverse mapping: DataHub catalogs to Trino catalogs
    catalog_mapping:
      warehouse: rdbms      # DataHub "warehouse" → Trino "rdbms"
      datalake: iceberg     # DataHub "datalake" → Trino "iceberg"
```

This translates URNs during lookup:

| Direction | Example |
|-----------|---------|
| Trino → DataHub | `rdbms.public.users` → `urn:li:dataset:(urn:li:dataPlatform:postgres,warehouse.public.users,PROD)` |
| DataHub → Trino | `warehouse.public.users` in URN → `rdbms.public.users` for querying |

### Lineage-Aware Semantic Enrichment

When columns lack metadata in the target table, lineage traversal can inherit metadata from upstream sources:

```yaml
semantic:
  provider: datahub
  instance: primary
  lineage:
    enabled: true
    max_hops: 2
    inherit:
      - glossary_terms
      - descriptions
      - tags
    prefer_column_lineage: true
    conflict_resolution: nearest
    cache_ttl: 10m
    timeout: 5s
```

**Inheritance order:**
1. Column's own metadata (always preferred)
2. Fine-grained column lineage (if `prefer_column_lineage: true`)
3. Table-level upstream lineage

**Column transforms** handle nested structures where column paths differ between source and target:

```yaml
semantic:
  lineage:
    column_transforms:
      - target_pattern: "elasticsearch.*.rxtxmsg.payload.*"
        strip_prefix: "rxtxmsg.payload."
      - target_pattern: "elasticsearch.*.rxtxmsg.header.*"
        strip_prefix: "rxtxmsg.header."
```

This maps `elasticsearch.index.rxtxmsg.payload.field_name` to lookup `field_name` in upstream sources.

## Tool Visibility Configuration

Reduce LLM token usage by hiding tools from `tools/list` responses. This is a visibility optimization, not a security boundary — persona-level tool filtering continues to gate `tools/call`.

```yaml
tools:
  allow:
    - "trino_*"
    - "datahub_*"
  deny:
    - "*_delete_*"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.allow` | array | `[]` | Tool name patterns to include in `tools/list` |
| `tools.deny` | array | `[]` | Tool name patterns to exclude from `tools/list` |
| `tools.description_overrides` | map | `{}` | Override tool descriptions in `tools/list` (config wins over built-in defaults) |

No patterns configured means all tools are visible. When both are set, allow is evaluated first, then deny removes from the result. Patterns use `filepath.Match` syntax (`*` matches any sequence of characters).

**Description Overrides**: Built-in overrides for `trino_query` and `trino_execute` guide agents to call `datahub_search` before writing SQL. Use `description_overrides` to customize these or add overrides for other tools. Config values take precedence over built-in defaults.

## Elicitation Configuration

Elicitation prompts users for confirmation before expensive queries or when accessing PII-tagged columns. Requires client-side elicitation support (MCP `elicitation/create` capability). Gracefully degrades to a no-op if the client doesn't support it.

```yaml
elicitation:
  enabled: true
  cost_estimation:
    enabled: true
    row_threshold: 1000000
  pii_consent:
    enabled: true
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `elicitation.enabled` | bool | `false` | Master switch for elicitation |
| `elicitation.cost_estimation.enabled` | bool | `false` | Prompt when EXPLAIN IO estimates exceed the row threshold |
| `elicitation.cost_estimation.row_threshold` | int | `1000000` | Row estimate threshold for cost prompts |
| `elicitation.pii_consent.enabled` | bool | `false` | Prompt when query accesses columns tagged as PII/sensitive |

Elicitation is implemented as Trino toolkit middleware. When a user declines the prompt, the tool call returns an informational message instead of executing the query.

## Icons Configuration

Override or add icons to MCP `tools/list`, `resources/templates/list`, and `prompts/list` responses. Upstream toolkits (Trino, DataHub, S3) provide default icons; this configuration overrides them.

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `icons.enabled` | bool | `false` | Enable icon injection middleware |
| `icons.tools` | map | `{}` | Tool name to icon mapping |
| `icons.resources` | map | `{}` | Resource template URI to icon mapping |
| `icons.prompts` | map | `{}` | Prompt name to icon mapping |
| `icons.*.src` | string | - | Icon source URI (HTTPS URL or data URI) |
| `icons.*.mime_type` | string | - | Icon MIME type (e.g., `image/svg+xml`) |

## Resource Links Configuration

DataHub search results and entity responses automatically include MCP resource links when resource templates are enabled. These links allow clients to navigate directly to related schema, glossary, and availability resources.

```yaml
resources:
  enabled: true
```

When `resources.enabled: true`, DataHub tools include links to:

- `schema://{catalog}.{schema}/{table}` — table schema details
- `glossary://{term}` — glossary term definitions
- `availability://{catalog}.{schema}/{table}` — query availability status

## Custom Resources Configuration

Expose arbitrary static content as named MCP resources. Registered whenever `resources.custom` is non-empty, independent of `resources.enabled`.

```yaml
resources:
  custom:
    - uri: "brand://theme"
      name: "Brand Theme"
      description: "Primary brand colors and site URL"
      mime_type: "application/json"
      content: |
        {"colors": {"primary": "#FF6B35"}, "url": "https://example.com"}
    - uri: "brand://logo"
      name: "Brand Logo SVG"
      mime_type: "image/svg+xml"
      content_file: "/etc/platform/logo.svg"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `resources.custom[].uri` | string | — | Unique resource URI (required) |
| `resources.custom[].name` | string | — | Display name in `resources/list` (required) |
| `resources.custom[].description` | string | `""` | Optional description |
| `resources.custom[].mime_type` | string | — | MIME type, e.g. `application/json` (required) |
| `resources.custom[].content` | string | — | Inline content; mutually exclusive with `content_file` |
| `resources.custom[].content_file` | string | — | File path; read on every request for hot-reload |

## Portal Configuration

The asset portal persists AI-generated artifacts (JSX dashboards, HTML reports, SVG charts) to S3 with PostgreSQL metadata tracking.

```yaml
portal:
  enabled: true
  title: "ACME Data Platform"                    # Sidebar/branding title
  logo: https://example.com/logo.svg             # Logo URL (fallback for both themes)
  logo_light: https://example.com/logo-light.svg # Logo for light theme
  logo_dark: https://example.com/logo-dark.svg   # Logo for dark theme
  s3_connection: primary        # S3 toolkit instance for artifact storage
  s3_bucket: portal-artifacts   # Bucket for artifact content
  s3_prefix: "artifacts/"       # Key prefix within the bucket
  public_base_url: "https://portal.example.com"  # Base URL for portal links
  max_content_size: 10485760    # Max artifact size in bytes (default: 10MB)
  implementor:                                   # Optional implementor brand (left zone of public viewer header)
    name: "ACME Corp"
    logo: "https://acme.com/logo.svg"
    url: "https://acme.com"
  export:                                        # trino_export configuration
    enabled: true                                # auto-enabled when portal + trino are configured
    max_rows: 100000                             # hard row cap per export
    max_bytes: 104857600                         # hard byte cap (100 MB)
    default_timeout: "5m"                        # default query timeout
    max_timeout: "10m"                           # maximum allowed timeout
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `portal.enabled` | bool | `false` | Enable the portal SPA frontend and artifact API |
| `portal.title` | string | `MCP Data Platform` | Sidebar/branding title text |
| `portal.logo` | string | `""` | URL to logo image (used for both themes if no theme-specific logo is set) |
| `portal.logo_light` | string | `""` | URL to logo for light theme (overrides `logo`) |
| `portal.logo_dark` | string | `""` | URL to logo for dark theme (overrides `logo`) |
| `portal.s3_connection` | string | - | Name of the S3 toolkit instance to use for artifact storage |
| `portal.s3_bucket` | string | `portal-assets` | S3 bucket for storing artifact content |
| `portal.s3_prefix` | string | `artifacts/` | Key prefix within the bucket |
| `portal.public_base_url` | string | `""` | Base URL for portal links returned in `save_artifact` responses |
| `portal.max_content_size` | int | `10485760` | Maximum artifact size in bytes (10 MB) |
| `portal.implementor.name` | string | `""` | Implementor display name shown in the left zone of the public viewer header |
| `portal.implementor.logo` | string | `""` | URL to implementor SVG logo (fetched once at startup, max 1 MB) |
| `portal.implementor.url` | string | `""` | Clickable link wrapping the implementor name and logo |
| `portal.export.enabled` | bool | auto | Enable `trino_export` tool. Auto-enabled when portal and Trino are both configured. Set `false` to disable. |
| `portal.export.max_rows` | int | `100000` | Hard row cap for exports |
| `portal.export.max_bytes` | int | `104857600` | Hard byte cap for formatted output (100 MB) |
| `portal.export.default_timeout` | string | `"5m"` | Default query timeout for exports |
| `portal.export.max_timeout` | string | `"10m"` | Maximum allowed query timeout for exports |

### Share Creation API

When creating a share via `POST /api/v1/portal/assets/{id}/shares`, the request body accepts:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `expires_in` | string | - | Duration string (e.g., `"24h"`, `"72h"`) |
| `shared_with_user_id` | string | - | Target user ID for private shares |
| `shared_with_email` | string | - | Target email for private shares |
| `hide_expiration` | bool | `false` | Hide the expiration countdown in the public viewer |
| `notice_text` | string\|null | `"Proprietary & Confidential. Only share with authorized viewers."` | Custom notice text for the public viewer. Omit or `null` for the default. Set to `""` to hide the notice entirely. Max 500 characters. |

### Public Viewer

The public viewer (`/portal/view/{token}`) renders shared artifacts with:

- **Light/dark mode** — respects system `prefers-color-scheme`, toggle button persists choice to `localStorage`
- **Expiration notice** — shows relative time until expiry (hidden when `hide_expiration` is true or no expiry set)
- **Notice text** — configurable per-share via `notice_text`. Defaults to "Proprietary & Confidential. Only share with authorized viewers." Set to `""` at share creation to hide entirely.

!!! note "Prerequisites"
    Portal requires `database.dsn` to be configured for metadata storage, and at least one S3 toolkit instance for artifact content storage.

## Admin API Configuration

```yaml
admin:
  enabled: true
  persona: admin
  path_prefix: /api/v1/admin
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `admin.enabled` | bool | `false` | Enable admin REST API |
| `admin.persona` | string | `admin` | Persona required for admin access |
| `admin.path_prefix` | string | `/api/v1/admin` | URL prefix for admin endpoints |

## Injection Configuration

```yaml
injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  s3_semantic_enrichment: true
  datahub_storage_enrichment: true
  unwrap_json: true
  column_context_filtering: true
  search_schema_preview: true
  schema_preview_max_columns: 15
  session_dedup:
    enabled: true
    mode: reference
    entry_ttl: 5m
    session_timeout: 30m
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `trino_semantic_enrichment` | bool | `false` | Enrich Trino with DataHub |
| `datahub_query_enrichment` | bool | `false` | Enrich DataHub with Trino |
| `s3_semantic_enrichment` | bool | `false` | Enrich S3 with DataHub |
| `datahub_storage_enrichment` | bool | `false` | Enrich DataHub with S3 |
| `unwrap_json` | bool | `true` | Auto-unwrap single-row VARCHAR-of-JSON results (e.g. from raw_query) |
| `column_context_filtering` | bool | `true` | Limit column enrichment to SQL-referenced columns |
| `search_schema_preview` | bool | `true` | Add column preview to search query_context |
| `schema_preview_max_columns` | int | `15` | Max columns per entity in schema preview |

**Session Dedup** (`injection.session_dedup`):

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Whether session dedup is active |
| `mode` | string | `reference` | Content for repeat queries: `reference`, `summary`, `none` |
| `entry_ttl` | duration | semantic cache TTL | How long a table stays "already sent" |
| `session_timeout` | duration | streamable session timeout | Idle session cleanup interval |

See [Session Metadata Deduplication](../cross-enrichment/overview.md#session-metadata-deduplication) for detailed behavior and JSON examples.

## Tuning Configuration

```yaml
tuning:
  rules:
    require_datahub_check: true
    warn_on_deprecated: true
    quality_threshold: 0.7
  prompts_dir: "/etc/mcp/prompts"
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `tuning.rules.require_datahub_check` | bool | `false` | Static hint for query tools (superseded by workflow gating) |
| `tuning.rules.warn_on_deprecated` | bool | `false` | Warn on deprecated tables |
| `tuning.rules.quality_threshold` | float | `0.7` | Minimum quality score |
| `tuning.prompts_dir` | string | - | Directory for prompt resources |

## Workflow Gating Configuration

Session-aware enforcement that agents call DataHub discovery tools before running Trino queries. Unlike the static `require_datahub_check` rule (which fires on every query), workflow gating tracks discovery per session and only warns when discovery hasn't occurred.

```yaml
workflow:
  require_discovery_before_query: true
  # discovery_tools: []             # Defaults to all datahub_* tools
  # query_tools: []                 # Defaults to trino_query, trino_execute
  # warning_message: ""             # Custom warning (default: built-in REQUIRED message)
  escalation:
    after_warnings: 3               # Switch to escalated message after N warnings
    # escalation_message: ""        # Custom escalation (use {count} for warning number)
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `workflow.require_discovery_before_query` | bool | `false` | Enable session-aware workflow gating |
| `workflow.discovery_tools` | array | all `datahub_*` tools | Tool names that count as discovery |
| `workflow.query_tools` | array | `trino_query`, `trino_execute` | Tool names gated by discovery |
| `workflow.warning_message` | string | built-in | Message prepended to query results when no discovery has occurred |
| `workflow.escalation.after_warnings` | int | `3` | Number of standard warnings before escalation |
| `workflow.escalation.escalation_message` | string | built-in | Escalated message (supports `{count}` placeholder) |

When enabled, a standard warning is prepended to the first N query results (where N = `after_warnings`). After the threshold, an escalated message replaces the standard warning. Once any discovery tool is called, warnings reset and stop until the next session.

Built-in description overrides for `trino_query` and `trino_execute` (in `tools/list`) complement workflow gating by guiding agents to call `datahub_search` at tool-discovery time.

## Audit Configuration

Audit logging requires a PostgreSQL database. See [Audit Logging](../server/audit.md) for full documentation including schema, query examples, and troubleshooting.

```yaml
database:
  dsn: "postgres://user:pass@localhost/platform"

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `database.dsn` | string | - | PostgreSQL connection string. Required for audit logging. |
| `audit.enabled` | bool | `false` | Master switch for audit logging. |
| `audit.log_tool_calls` | bool | `false` | Log every `tools/call` request. Both this and `enabled` must be `true`. |
| `audit.retention_days` | int | `90` | Days to keep audit logs before automatic cleanup. |

If `audit.enabled` is `true` but no database is configured, the platform logs a warning and falls back to a no-op logger.

## Knowledge Configuration

Knowledge capture records domain knowledge shared during AI sessions and provides a governance workflow for applying approved insights to DataHub. See [Knowledge Capture](../knowledge/overview.md) for full documentation.

```yaml
knowledge:
  enabled: true
  apply:
    enabled: true
    datahub_connection: primary
    require_confirmation: true
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `knowledge.enabled` | bool | `false` | Enable the knowledge capture toolkit and `capture_insight` tool |
| `knowledge.apply.enabled` | bool | `false` | Enable the `apply_knowledge` tool for admin review and catalog write-back |
| `knowledge.apply.datahub_connection` | string | - | DataHub instance name for write-back operations |
| `knowledge.apply.require_confirmation` | bool | `false` | When true, the `apply` action requires `confirm: true` in the request |

!!! note "Prerequisites"
    Knowledge capture requires `database.dsn` to be configured. The `apply_knowledge` tool requires the admin persona.

## MCP Apps Configuration

MCP Apps provide interactive UI panels rendered in the MCP host alongside tool results. The built-in `platform-info` app is embedded in the binary and registers automatically — no configuration required.

```yaml
mcpapps:
  # enabled defaults to true; set false to disable all MCP Apps
  enabled: true
  apps:
    # platform-info is built-in; only branding overrides are needed
    platform-info:
      config:
        brand_name: "ACME Data Platform"
        brand_url: "https://data.acme.com"
        logo_svg: "<svg ...>"

    # Custom app example (assets_path required for non-built-in apps)
    query_results:
      enabled: true
      assets_path: "/etc/mcp-apps/query-results"
      tools:
        - trino_query
        - trino_execute
      csp:
        resource_domains:
          - "https://cdn.jsdelivr.net"
      config:
        maxRows: 1000
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `mcpapps.enabled` | bool | `true` | Master switch; set `false` to disable all MCP Apps |
| `mcpapps.apps.<name>.enabled` | bool | `true` | Enable/disable this individual app |
| `mcpapps.apps.<name>.assets_path` | string | - | Absolute path to HTML/JS/CSS directory. Required for custom apps; omit for `platform-info` to use the embedded HTML |
| `mcpapps.apps.<name>.entry_point` | string | `index.html` | HTML entry point filename |
| `mcpapps.apps.<name>.resource_uri` | string | `ui://<name>` | MCP resource URI for this app |
| `mcpapps.apps.<name>.tools` | []string | - | Tool names that cause this app to be surfaced |
| `mcpapps.apps.<name>.csp.resource_domains` | []string | - | Additional allowed origins for `<script>`/`<link>` |
| `mcpapps.apps.<name>.csp.connect_domains` | []string | - | Additional allowed `fetch`/XHR origins |
| `mcpapps.apps.<name>.config` | object | - | Arbitrary config injected into the app as `<script id="app-config">` JSON |

See [MCP Apps Configuration](../mcpapps/configuration.md) for full documentation.

## Complete Example

```yaml
server:
  name: mcp-data-platform
  version: "1.0.0"
  description: |
    Enterprise data platform providing unified access to analytics data.
    Includes semantic enrichment from DataHub and query execution via Trino.
  transport: stdio

admin:
  enabled: true
  portal: true

tools:
  allow:
    - "trino_*"
    - "datahub_*"
    - "capture_insight"
  deny:
    - "*_delete_*"

auth:
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_SERVICE}
        name: "service"
        roles: ["service"]

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
      priority: 100
  default_persona: analyst

toolkits:
  trino:
    primary:
      host: trino.example.com
      port: 443
      user: ${TRINO_USER}
      password: ${TRINO_PASSWORD}
      ssl: true
      catalog: hive
  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}
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
  search_schema_preview: true
  schema_preview_max_columns: 15
  session_dedup:
    enabled: true
    mode: reference

resources:
  enabled: true

workflow:
  require_discovery_before_query: true
  escalation:
    after_warnings: 3

elicitation:
  enabled: true
  cost_estimation:
    enabled: true
    row_threshold: 1000000

knowledge:
  enabled: true
  apply:
    enabled: true
    datahub_connection: primary
    require_confirmation: true

portal:
  enabled: true
  s3_connection: primary
  s3_bucket: portal-artifacts
  s3_prefix: "artifacts/"
  public_base_url: "https://portal.example.com"

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

database:
  dsn: ${DATABASE_URL}
```

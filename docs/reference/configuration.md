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
| `server.prompts` | array | `[]` | Platform-level MCP prompts registered via `prompts/list` |
| `server.prompts[].name` | string | required | Prompt name |
| `server.prompts[].description` | string | - | Prompt description |
| `server.prompts[].content` | string | required | Prompt content returned by `prompts/get` |
| `server.transport` | string | `stdio` | Transport: `stdio`, `http` (`sse` accepted for backward compat) |
| `server.address` | string | `:8080` | Listen address for HTTP transport |
| `server.streamable.session_timeout` | duration | `30m` | How long an idle Streamable HTTP session persists before cleanup |
| `server.streamable.stateless` | bool | `false` | Disable session tracking (no `Mcp-Session-Id` validation) |
| `server.tls.enabled` | bool | `false` | Enable TLS |
| `server.tls.cert_file` | string | - | TLS certificate path |
| `server.tls.key_file` | string | - | TLS private key path |

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
      prompts:
        system_prefix: "You are helping a data analyst."
        system_suffix: ""
        instructions: ""
      hints:
        trino_query: "Prefer aggregations for large tables"
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
| `personas.definitions.<name>.prompts.system_prefix` | string | - | System prompt prefix |
| `personas.definitions.<name>.prompts.system_suffix` | string | - | System prompt suffix |
| `personas.definitions.<name>.prompts.instructions` | string | - | Additional instructions |
| `personas.definitions.<name>.hints` | map | `{}` | Tool-specific hints |
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

## Injection Configuration

```yaml
injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  s3_semantic_enrichment: true
  datahub_storage_enrichment: true
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `trino_semantic_enrichment` | bool | `false` | Enrich Trino with DataHub |
| `datahub_query_enrichment` | bool | `false` | Enrich DataHub with Trino |
| `s3_semantic_enrichment` | bool | `false` | Enrich S3 with DataHub |
| `datahub_storage_enrichment` | bool | `false` | Enrich DataHub with S3 |

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
| `tuning.rules.require_datahub_check` | bool | `false` | Require DataHub lookup before query |
| `tuning.rules.warn_on_deprecated` | bool | `false` | Warn on deprecated tables |
| `tuning.rules.quality_threshold` | float | `0.7` | Minimum quality score |
| `tuning.prompts_dir` | string | - | Directory for prompt resources |

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

## Complete Example

```yaml
server:
  name: mcp-data-platform
  version: "1.0.0"
  description: |
    Enterprise data platform providing unified access to analytics data.
    Includes semantic enrichment from DataHub and query execution via Trino.
  transport: stdio

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

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

database:
  dsn: ${DATABASE_URL}
```

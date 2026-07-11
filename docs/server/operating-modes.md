---
description: Two operating modes for mcp-data-platform: standalone (no database) and database-backed. Feature availability comparison and example configurations.
---

# Operating Modes

mcp-data-platform has two operating modes, determined solely by whether `database.dsn` is set. Without a database the platform runs standalone with read-only file config; with a database the config becomes database-backed and mutable, and the persistence-dependent features (audit, knowledge, sessions, OAuth, MCP gateway) activate. There is no separate mode switch; store selection follows database presence.

## Mode Comparison

| Aspect | Standalone (no database) | Database-backed |
|--------|--------------------------|-----------------|
| `database.dsn` | empty | set |
| `config_mode` (from `system/info`) | `file` | `database` |
| Config source | YAML file only | YAML bootstrap + database |
| Config mutations (persona/auth-key CRUD, config import) | blocked | enabled |
| Knowledge tools | hidden (not registered) | registered |
| Knowledge admin API | 409 Conflict | available |
| Audit logging | noop (silent) | PostgreSQL |
| Audit admin API | 409 Conflict | available |
| Sessions | memory | database |
| OAuth (downstream clients) | available (memory store) | available (DB store) |
| MCP gateway connections | not loaded (no DB to read from) | loaded; full feature set |
| Gateway OAuth `client_credentials` | n/a | tokens encrypted in DB |
| Gateway OAuth `authorization_code` | n/a | encrypted refresh tokens persist across restarts |

### MCP gateway requirements

The gateway toolkit (kind `mcp`, see [Gateway Toolkit](gateway.md))
requires a database for its full feature set:

- **Connections** are stored in `connection_instances` rows and managed
  through the admin portal — not in the YAML file. Without a database
  there are no rows to load, and the gateway has no upstreams to proxy.
- **OAuth `authorization_code` + PKCE** persists access and refresh
  tokens in `gateway_oauth_tokens`, encrypted at rest with
  `ENCRYPTION_KEY`. Without persistence, the operator would have to
  re-authorize after every restart — defeating the purpose of the grant.
- **Multi-replica deployments** additionally need the database for the
  PKCE state store. The default in-memory store is single-replica only:
  `oauth-start` may land on replica A while the upstream's redirect
  lands the callback on replica B, and an in-memory store wouldn't see
  the cross-replica state. The platform automatically uses the
  Postgres-backed PKCE store when a database is configured.

For production gateway deployments, run in database-backed mode with
`ENCRYPTION_KEY` set.

## Standalone (No Database)

The lightest deployment. No external database required. Suitable for local development, single-user environments, or when external databases are not available.

All configuration comes from the YAML file. Features that require persistence (audit logging, knowledge capture) run in noop mode and their tools are hidden from `tools/list`. The admin API is available but database-dependent endpoints return `409 Conflict` with an explanation.

```yaml
server:
  name: mcp-data-platform
  transport: stdio

toolkits:
  datahub:
    enabled: true
    instances:
      primary:
        url: https://datahub.example.com
        token: ${DATAHUB_TOKEN}
    default: primary

  trino:
    enabled: true
    instances:
      primary:
        host: trino.example.com
        port: 443
        user: ${TRINO_USER}
        password: ${TRINO_PASSWORD}
        ssl: true
    default: primary

semantic:
  provider: datahub
  instance: primary

query:
  provider: trino
  instance: primary

enrichment:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  column_context_filtering: true   # Only enrich columns referenced in SQL (default: true)

personas:
  analyst:
    display_name: "Data Analyst"
    roles: ["analyst"]
    tools:
      allow: ["trino_*", "datahub_*"]
  default_persona: analyst
```

The `system/info` endpoint reports:

```json
{
  "config_mode": "file",
  "features": {
    "audit": false,
    "knowledge": false,
    "database": false,
    "oauth": false,
    "admin": true
  }
}
```

## Database-Backed

The production mode. Add a PostgreSQL database and the config becomes database-backed and mutable: audit logs, knowledge capture, session externalization, and the MCP gateway all activate, and the admin API can mutate personas, auth keys, and config entries at runtime (persisted to the database). The YAML file always bootstraps the base config; a subset of fields (below) is authoritative from YAML on every boot and overrides database values.

There is a single runtime mode here. How much you put in YAML versus manage through the admin API is an authoring choice, not a separate mode:

- **Full config in YAML**: keep the complete configuration in the YAML file, managed through your deployment pipeline (Git, CI/CD, ConfigMaps). The admin mutation API is still available but typically unused. Most teams start here.
- **Bootstrap-minimal**: keep only connection details in YAML (server, database, auth, admin) and manage the rest through the admin API, which persists to PostgreSQL with versioning. Use this when you need to modify personas, auth keys, or import configs without restarting.

```yaml
server:
  name: mcp-data-platform
  transport: http
  address: ":8080"

database:
  dsn: ${DATABASE_URL}

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

knowledge:
  enabled: true
  apply:
    enabled: true
    datahub_connection: primary
    require_confirmation: true

admin:
  enabled: true
  persona: admin
  path_prefix: /api/v1/admin

auth:
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: admin
        roles: ["admin"]

toolkits:
  datahub:
    enabled: true
    instances:
      primary:
        url: https://datahub.example.com
        token: ${DATAHUB_TOKEN}
    default: primary

  trino:
    enabled: true
    instances:
      primary:
        host: trino.example.com
        port: 443
        user: ${TRINO_USER}
        password: ${TRINO_PASSWORD}
        ssl: true
    default: primary

semantic:
  provider: datahub
  instance: primary

query:
  provider: trino
  instance: primary

enrichment:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  column_context_filtering: true   # Only enrich columns referenced in SQL (default: true)

personas:
  analyst:
    display_name: "Data Analyst"
    roles: ["analyst"]
    tools:
      allow: ["trino_*", "datahub_*"]
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
  default_persona: analyst
```

On first boot with an empty database, the platform seeds the config store with the bootstrap YAML. Subsequent boots load from the database and merge the bootstrap fields on top.

Bootstrap fields that always come from YAML (never overridden by database):

- `apiVersion`
- `server`
- `database`
- `auth`
- `admin`

The `system/info` endpoint reports:

```json
{
  "config_mode": "database",
  "features": {
    "audit": true,
    "knowledge": true,
    "database": true,
    "oauth": false,
    "admin": true
  }
}
```

## Which Mode Should I Use?

**Local development or single-user**: Use Standalone. No database setup required. Add a database later when you need audit logs, knowledge capture, or the MCP gateway.

**Production**: Use database-backed mode. Whether you author the full config in YAML (managed through your deployment pipeline) or bootstrap-minimal and manage the rest through the admin API is an operational preference; both run the same runtime mode.

## Feature Degradation

When a feature is enabled in configuration but the required infrastructure is unavailable, the platform degrades gracefully:

- **Knowledge tools**: Not registered in `tools/list`. Admin API returns `409 Conflict` explaining the requirement.
- **Audit logging**: Uses a noop logger (events are silently discarded). Admin API returns `409 Conflict`.
- **Sessions**: Fall back to in-memory store (state lost on restart).
- **OAuth**: Falls back to in-memory storage (clients lost on restart).

The `GET /api/v1/admin/system/info` endpoint always reflects the actual runtime state, not just what is enabled in config.

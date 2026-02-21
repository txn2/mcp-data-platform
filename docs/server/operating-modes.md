---
description: Three operating modes for mcp-data-platform — standalone, full-config file with database, bootstrap with database config. Feature availability comparison and example configurations.
---

# Operating Modes

mcp-data-platform supports three operating modes based on available infrastructure. The mode is determined by two configuration values: whether `database.dsn` is set and the `config_store.mode` setting.

## Mode Comparison

| Aspect | Standalone | File + Database | Bootstrap + DB Config |
|--------|-----------|-----------------|----------------------|
| `database.dsn` | empty | set | set |
| `config_store.mode` | `file` (default) | `file` (default) | `database` |
| Config source | YAML file only | YAML file only | Bootstrap YAML + DB |
| Config mutations | blocked | blocked | enabled |
| Knowledge tools | hidden (not registered) | registered | registered |
| Knowledge admin API | 409 Conflict | available | available |
| Audit logging | noop (silent) | PostgreSQL | PostgreSQL |
| Audit admin API | 409 Conflict | available | available |
| Sessions | memory | database | database |
| OAuth | available (memory store) | available (DB store) | available (DB store) |
| Persona/auth key CRUD | read-only | read-only | enabled |
| Config import | blocked | blocked | enabled |

## Standalone (No Database)

The lightest deployment. No external database required. Suitable for local development, single-user environments, or when external databases are not available.

All configuration comes from the YAML file. Features that require persistence (audit logging, knowledge capture) run in noop mode and their tools are hidden from `tools/list`. The admin API is available but database-dependent endpoints return `409 Conflict` with an explanation.

```yaml
server:
  name: mcp-data-platform
  transport: stdio

toolkits:
  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}

  trino:
    primary:
      host: trino.example.com
      port: 443
      user: ${TRINO_USER}
      password: ${TRINO_PASSWORD}
      ssl: true

semantic:
  provider: datahub
  instance: primary

query:
  provider: trino
  instance: primary

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  column_context_filtering: true   # Only enrich columns referenced in SQL (default: true)

personas:
  definitions:
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

## Full-Config File + Database

The production default. Complete configuration in YAML with a PostgreSQL database for persistence. Audit logs, knowledge capture, and session externalization are all available. Configuration is immutable at runtime - restart the server to apply changes.

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
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}

  trino:
    primary:
      host: trino.example.com
      port: 443
      user: ${TRINO_USER}
      password: ${TRINO_PASSWORD}
      ssl: true

semantic:
  provider: datahub
  instance: primary

query:
  provider: trino
  instance: primary

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  column_context_filtering: true   # Only enrich columns referenced in SQL (default: true)

personas:
  definitions:
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

The `system/info` endpoint reports:

```json
{
  "config_mode": "file",
  "features": {
    "audit": true,
    "knowledge": true,
    "database": true,
    "oauth": false,
    "admin": true
  }
}
```

## Bootstrap + Database Config

Minimal YAML provides connection details (server, database, auth, admin). Full configuration is stored in PostgreSQL with versioning. The admin API enables runtime mutations for personas, auth keys, and config import. Bootstrap fields always override database values on restart.

```yaml
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
  persona: admin
  path_prefix: /api/v1/admin

auth:
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: admin
        roles: ["admin"]
```

On first boot with an empty database, the platform seeds the config store with the bootstrap YAML. Subsequent boots load from the database and merge bootstrap fields on top.

Bootstrap fields that always come from YAML (never overridden by database):

- `apiVersion`
- `server`
- `database`
- `auth`
- `admin`
- `config_store`

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

**Local development or single-user**: Start with Standalone. No database setup required. Add a database later when you need audit logs or knowledge capture.

**Production with static config**: Use File + Database. Full feature set with config managed through your deployment pipeline (Git, CI/CD, ConfigMaps). Most teams start here.

**Production with runtime config**: Use Bootstrap + Database Config when you need to modify personas, auth keys, or import configs without restarting. The admin API becomes a control plane for the platform.

## Feature Degradation

When a feature is enabled in configuration but the required infrastructure is unavailable, the platform degrades gracefully:

- **Knowledge tools**: Not registered in `tools/list`. Admin API returns `409 Conflict` explaining the requirement.
- **Audit logging**: Uses a noop logger (events are silently discarded). Admin API returns `409 Conflict`.
- **Sessions**: Fall back to in-memory store (state lost on restart).
- **OAuth**: Falls back to in-memory storage (clients lost on restart).

The `GET /api/v1/admin/system/info` endpoint always reflects the actual runtime state, not just what is enabled in config.

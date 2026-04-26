---
description: REST API reference for the admin interface. System info, config management, personas, auth keys, audit, and knowledge endpoints. Operating mode behavior and authentication.
---

# Admin API

The Admin REST API provides HTTP endpoints for managing the platform outside the MCP protocol. All endpoints are mounted under a configurable path prefix (default: `/api/v1/admin`).

## Authentication

All admin endpoints require authentication. Pass credentials as either:

- `X-API-Key: <key>` header
- `Authorization: Bearer <token>` header

The authenticated user must resolve to the configured admin persona (set via `admin.persona` in config). Requests without valid credentials receive `401 Unauthorized`. Authenticated users whose persona does not match the admin persona receive `401 Unauthorized` (the admin API is invisible to non-admin users by design).

## Interactive API Documentation (Swagger UI)

When the admin API is enabled, an interactive Swagger UI is served at:

```
GET /api/v1/admin/docs/index.html
```

The OpenAPI specification is auto-generated from source code annotations using [swaggo/swag](https://github.com/swaggo/swag). The raw spec is available at `/api/v1/admin/docs/doc.json`. To regenerate after code changes:

```bash
make swagger
```

## Configuration

```yaml
portal:
  enabled: true               # Enable portal SPA + API
  title: "ACME Data Platform" # Sidebar/branding title
  logo: https://example.com/logo.svg
  logo_light: https://example.com/logo-for-light-bg.svg
  logo_dark: https://example.com/logo-for-dark-bg.svg

admin:
  enabled: true
  persona: admin              # Persona required for admin access
  path_prefix: /api/v1/admin  # URL prefix for all admin endpoints
```

**Admin configuration:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `admin.enabled` | bool | `false` | Enable admin REST API |
| `admin.persona` | string | `admin` | Persona required for admin access |
| `admin.path_prefix` | string | `/api/v1/admin` | URL prefix for admin endpoints |

**Portal/branding configuration:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `portal.enabled` | bool | `false` | Enable portal SPA + API |
| `portal.title` | string | `MCP Data Platform` | Sidebar/branding title text |
| `portal.logo` | string | `""` | Logo URL (fallback for both themes) |
| `portal.logo_light` | string | `""` | Logo URL for light theme |
| `portal.logo_dark` | string | `""` | Logo URL for dark theme |

## Admin Portal

When `portal.enabled: true`, an interactive web dashboard is served at `/portal/`. The portal provides:

- **Dashboard**: Real-time platform health with activity timelines, top tools/users, performance percentiles, and error monitoring
- **Tools**: Connection overview, tool inventory with descriptions, and interactive tool execution with semantic enrichment display
- **Audit Log**: Searchable event log with detail drawer showing full request metadata and parameters
- **Knowledge**: Insight statistics, governance workflow with approve/reject actions, and changeset tracking

![Admin Portal Dashboard](../images/screenshots/light/admin-admin-dashboard-light.webp#only-light)![Admin Portal Dashboard](../images/screenshots/dark/admin-admin-dashboard-dark.webp#only-dark)

The portal requires authentication — access it with the same credentials used for admin API requests. In production builds, the service worker (`mockServiceWorker.js`) is stripped automatically.

See the [Admin Portal guide](admin-portal.md) for a complete visual walkthrough.

## Error Format

All errors follow [RFC 9457 Problem Details](https://www.rfc-editor.org/rfc/rfc9457):

```json
{
  "type": "about:blank",
  "title": "Conflict",
  "status": 409,
  "detail": "knowledge is enabled but not available without database configuration"
}
```

## Operating Mode Behavior

Some endpoints are only available in certain [operating modes](operating-modes.md). When a feature is enabled in config but unavailable at runtime (e.g., no database), endpoints return `409 Conflict` with an explanation. When a feature is disabled in config, endpoints return `404 Not Found`.

| Endpoint Group | Standalone (no DB) | File + DB |
|---------------|-------------------|-----------|
| System | available | available |
| Config (read) | available | available |
| Config entries (CRUD) | 409 | available (whitelisted keys only) |
| Config changelog | 409 | available |
| Personas (read) | available | available |
| Personas (write) | 409 | available |
| Auth keys (read) | available | available |
| Auth keys (write) | 409 | available |
| Audit | 409 (if enabled) | available |
| Knowledge | 409 (if enabled) | available |

## System Endpoints

### Get System Info

```
GET /api/v1/admin/system/info
```

Returns platform identity, version, runtime feature availability, and config mode.

**Response:**

```json
{
  "name": "mcp-data-platform",
  "version": "0.17.0",
  "description": "Semantic data platform",
  "transport": "http",
  "config_mode": "file",
  "portal_title": "ACME Data Platform",
  "portal_logo": "https://example.com/logo.svg",
  "portal_logo_light": "https://example.com/logo-for-light-bg.svg",
  "portal_logo_dark": "https://example.com/logo-for-dark-bg.svg",
  "features": {
    "audit": true,
    "oauth": false,
    "knowledge": true,
    "admin": true,
    "database": true
  },
  "toolkit_count": 3,
  "persona_count": 2
}
```

Feature booleans reflect **runtime availability**, not config values. For example, `knowledge` is `false` when enabled in config but no database is configured.

### List Tools

```
GET /api/v1/admin/tools
```

Returns all registered tools across all toolkits.

**Response:**

```json
{
  "tools": [
    {
      "name": "trino_query",
      "toolkit": "prod",
      "kind": "trino",
      "connection": "prod-trino"
    }
  ],
  "total": 1
}
```

### List Connections

```
GET /api/v1/admin/connections
```

Returns all toolkit connections with their tools.

**Response:**

```json
{
  "connections": [
    {
      "kind": "trino",
      "name": "prod",
      "connection": "prod-trino",
      "tools": ["trino_query", "trino_describe_table"]
    }
  ],
  "total": 1
}
```

## Config Endpoints

### Get Config

```
GET /api/v1/admin/config
```

Returns the current configuration as JSON with sensitive values redacted.

**Response:**

```json
{
  "server": {
    "name": "mcp-data-platform",
    "transport": "http",
    "address": ":8080"
  },
  "auth": {
    "api_keys": {
      "enabled": true,
      "keys": [
        {
          "name": "admin",
          "key": "***REDACTED***",
          "roles": ["admin"]
        }
      ]
    }
  },
  "toolkits": [
    {
      "kind": "trino",
      "name": "prod",
      "config": {
        "host": "trino.example.com",
        "port": 8080,
        "password": "***REDACTED***"
      }
    }
  ]
}
```

### Get Config Mode

```
GET /api/v1/admin/config/mode
```

Returns the current config store mode.

**Response:**

```json
{
  "mode": "file",
  "read_only": true
}
```

### Export Config

```
GET /api/v1/admin/config/export
```

Returns the current configuration as downloadable YAML. Sensitive values are redacted by default.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `secrets` | string | Set to `true` to include sensitive values |

**Response** (`Content-Type: application/x-yaml`):

```yaml
server:
  name: mcp-data-platform
  transport: http
  address: ":8080"
auth:
  api_keys:
    enabled: true
    keys:
      - name: admin
        key: "***REDACTED***"
        roles: [admin]
```

### Effective Config

```
GET /api/v1/admin/config/effective
```

Returns the merged view of all whitelisted config keys: database overrides where present, file defaults otherwise. Each entry includes a `source` field indicating whether the value comes from the file or a database override.

**Response:**

```json
[
  {
    "key": "server.agent_instructions",
    "value": "You are an AI assistant...",
    "source": "file"
  },
  {
    "key": "server.description",
    "value": "ACME Corp analytics platform",
    "source": "database",
    "updated_by": "admin@example.com",
    "updated_at": "2025-01-15T14:30:00Z"
  }
]
```

### List Config Entries

```
GET /api/v1/admin/config/entries
```

Returns all config entries stored in the database. Each entry represents a per-key override of the file default.

**Response:**

```json
[
  {
    "key": "server.description",
    "value": "ACME Corp analytics platform",
    "updated_by": "admin@example.com",
    "updated_at": "2025-01-15T14:30:00Z"
  }
]
```

### Get Config Entry

```
GET /api/v1/admin/config/entries/{key}
```

Returns a single config entry by key. Returns `404 Not Found` if the key has no database override.

**Response:**

```json
{
  "key": "server.description",
  "value": "ACME Corp analytics platform",
  "updated_by": "admin@example.com",
  "updated_at": "2025-01-15T14:30:00Z"
}
```

### Set Config Entry

```
PUT /api/v1/admin/config/entries/{key}
```

Sets a config entry for a whitelisted key. The change takes effect immediately (hot-reload) without restart. Requires a database connection. Returns `400 Bad Request` for non-whitelisted keys and `409 Conflict` when no database is configured.

**Whitelisted keys (phase 1):** `server.description`, `server.agent_instructions`

**Request Body:**

```json
{
  "value": "ACME Corp analytics platform"
}
```

**Response:**

```json
{
  "key": "server.description",
  "value": "ACME Corp analytics platform",
  "updated_by": "admin@example.com",
  "updated_at": "2025-01-15T14:30:00Z"
}
```

**Status Codes:** `200 OK`, `400 Bad Request` (non-whitelisted key), `409 Conflict` (no database)

### Delete Config Entry

```
DELETE /api/v1/admin/config/entries/{key}
```

Removes a database override for a key, restoring the file default. Returns `404 Not Found` if no override exists.

**Response:** `204 No Content` (no body)

### Config Changelog

```
GET /api/v1/admin/config/changelog
```

Returns an audit log of config entry changes (creates, updates, deletes).

**Response:**

```json
[
  {
    "key": "server.description",
    "action": "set",
    "value": "ACME Corp analytics platform",
    "changed_by": "admin@example.com",
    "changed_at": "2025-01-15T14:30:00Z"
  }
]
```

## Persona Endpoints

### List Personas

```
GET /api/v1/admin/personas
```

Returns all configured personas with tool counts.

**Response:**

```json
{
  "personas": [
    {
      "name": "analyst",
      "display_name": "Data Analyst",
      "description": "Read-only data access",
      "roles": ["analyst"],
      "tool_count": 15
    }
  ],
  "total": 1
}
```

### Get Persona

```
GET /api/v1/admin/personas/{name}
```

Returns a single persona with resolved tool list.

**Response:**

```json
{
  "name": "analyst",
  "display_name": "Data Analyst",
  "description": "Read-only data access",
  "roles": ["analyst"],
  "priority": 0,
  "allow_tools": ["trino_*", "datahub_*"],
  "deny_tools": ["*_delete_*"],
  "tools": ["trino_query", "trino_describe_table", "datahub_search"],
  "description_prefix": "You are helping a data analyst.",
  "agent_instructions_suffix": "Prefer aggregations for large tables."
}
```

### Create Persona

```
POST /api/v1/admin/personas
```

Creates a new persona. Only available in `database` config mode.

**Request Body:**

```json
{
  "name": "viewer",
  "display_name": "Data Viewer",
  "description": "Read-only access to DataHub",
  "roles": ["viewer"],
  "allow_tools": ["datahub_*"],
  "deny_tools": []
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Unique identifier |
| `display_name` | string | yes | Human-readable name |
| `description` | string | no | Description |
| `roles` | array | no | Roles that map to this persona |
| `allow_tools` | array | no | Tool allow patterns |
| `deny_tools` | array | no | Tool deny patterns |
| `priority` | int | no | Resolution priority (higher wins) |

**Response** (`201 Created`):

```json
{
  "name": "viewer",
  "display_name": "Data Viewer",
  "description": "Read-only access to DataHub",
  "roles": ["viewer"],
  "priority": 0,
  "allow_tools": ["datahub_*"],
  "deny_tools": [],
  "tools": ["datahub_search", "datahub_get_entity", "datahub_get_schema", "datahub_get_lineage", "datahub_browse"],
  "source": "database"
}
```

### Update Persona

```
PUT /api/v1/admin/personas/{name}
```

Updates an existing persona. Only available in `database` config mode.

**Request Body:** Same as Create (except `name` is taken from the URL path).

**Response** (`200 OK`): Returns the updated persona detail (same structure as Create response).

### Delete Persona

```
DELETE /api/v1/admin/personas/{name}
```

Deletes a persona. Only available in `database` config mode. Cannot delete the admin persona.

**Response** (`200 OK`):

```json
{
  "message": "persona deleted",
  "name": "viewer"
}
```

**Status Codes:** `200 OK`, `404 Not Found`, `409 Conflict` (admin persona)

## Auth Key Endpoints

### List Auth Keys

```
GET /api/v1/admin/auth/keys
```

Returns all API keys (key values are never exposed, only names and roles).

**Response:**

```json
{
  "keys": [
    {
      "name": "admin",
      "roles": ["admin"],
      "source": "file"
    },
    {
      "name": "ci-pipeline",
      "email": "ci@example.com",
      "description": "CI/CD pipeline integration",
      "roles": ["analyst"],
      "expires_at": "2026-07-15T00:00:00Z",
      "source": "database"
    },
    {
      "name": "expired-key",
      "email": "legacy@example.com",
      "description": "Decommissioned service key",
      "roles": ["viewer"],
      "expires_at": "2026-03-31T00:00:00Z",
      "expired": true,
      "source": "database"
    }
  ],
  "total": 3
}
```

### Create Auth Key

```
POST /api/v1/admin/auth/keys
```

Generates a new API key. Only available in `database` config mode. The key value is returned only once.

**Request Body:**

```json
{
  "name": "ci-pipeline",
  "email": "ci@example.com",
  "description": "CI/CD pipeline integration",
  "roles": ["analyst"],
  "expires_in": "720h"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Unique key name |
| `email` | string | no | Owner email |
| `description` | string | no | Description |
| `roles` | array | yes | Roles to assign |
| `expires_in` | string | no | Duration until expiry (e.g., `24h`, `720h`, `8760h`). Omit for no expiration. |

**Response** (`201 Created`):

```json
{
  "name": "ci-pipeline",
  "email": "ci@example.com",
  "description": "CI/CD pipeline integration",
  "key": "mdp_a1b2c3d4e5f6...",
  "roles": ["analyst"],
  "expires_at": "2026-05-18T14:30:00Z",
  "warning": "Store this key securely. It will not be shown again."
}
```

**Status Codes:** `201 Created`, `400 Bad Request`, `409 Conflict` (name exists or file mode)

### Delete Auth Key

```
DELETE /api/v1/admin/auth/keys/{name}
```

Deletes an API key. Only available in `database` config mode.

**Response** (`200 OK`):

```json
{
  "message": "key deleted",
  "name": "ci-pipeline"
}
```

## Audit Endpoints

Audit endpoints require `audit.enabled: true` and a configured database. Without a database, endpoints return `409 Conflict`. See [Audit Logging](audit.md) for the audit system overview.

### List Audit Events

```
GET /api/v1/admin/audit/events
```

Returns paginated audit events with optional filtering.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `user_id` | string | Filter by user ID |
| `tool_name` | string | Filter by tool name |
| `session_id` | string | Filter by MCP session ID |
| `success` | boolean | Filter by success/failure |
| `start_time` | RFC 3339 | Events after this time |
| `end_time` | RFC 3339 | Events before this time |
| `page` | integer | Page number, 1-based (default: 1) |
| `per_page` | integer | Results per page (default: 50) |

**Response:**

```json
{
  "data": [
    {
      "id": "evt_a1b2c3d4e5f6",
      "timestamp": "2026-04-15T10:41:18Z",
      "duration_ms": 143,
      "request_id": "req_x9y8z7",
      "session_id": "sess_abc123",
      "user_id": "user-uuid-1234",
      "user_email": "marcus.johnson@example.com",
      "persona": "data-engineer",
      "tool_name": "datahub_get_schema",
      "toolkit_kind": "datahub",
      "toolkit_name": "acme-catalog",
      "connection": "acme-catalog",
      "parameters": {
        "urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)"
      },
      "success": true,
      "response_chars": 2450,
      "request_chars": 120,
      "content_blocks": 2,
      "transport": "http",
      "source": "mcp",
      "enrichment_applied": true,
      "enrichment_tokens_full": 850,
      "enrichment_tokens_dedup": 350,
      "enrichment_mode": "summary",
      "authorized": true
    }
  ],
  "total": 196,
  "page": 1,
  "per_page": 50
}
```

### Get Audit Event

```
GET /api/v1/admin/audit/events/{id}
```

Returns a single audit event by ID.

**Response:** Same structure as a single item from the list response (the event object without the pagination wrapper).

```json
{
  "id": "evt_a1b2c3d4e5f6",
  "timestamp": "2026-04-15T10:41:18Z",
  "duration_ms": 143,
  "request_id": "req_x9y8z7",
  "session_id": "sess_abc123",
  "user_id": "user-uuid-1234",
  "user_email": "marcus.johnson@example.com",
  "persona": "data-engineer",
  "tool_name": "datahub_get_schema",
  "toolkit_kind": "datahub",
  "toolkit_name": "acme-catalog",
  "connection": "acme-catalog",
  "parameters": {
    "urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)"
  },
  "success": true,
  "response_chars": 2450,
  "request_chars": 120,
  "content_blocks": 2,
  "transport": "http",
  "source": "mcp",
  "enrichment_applied": true,
  "enrichment_tokens_full": 850,
  "enrichment_tokens_dedup": 350,
  "enrichment_mode": "summary",
  "authorized": true
}
```

### Get Audit Stats

```
GET /api/v1/admin/audit/stats
```

Returns aggregate counts for total, successful, and failed events. Supports the same time and filter parameters as list.

**Response:**

```json
{
  "total": 1500,
  "success": 1423,
  "failures": 77
}
```

### Audit Metrics: Overview

```
GET /api/v1/admin/audit/metrics/overview
```

Returns aggregated audit metrics including tool, user, and toolkit breakdowns, timeseries data, and performance statistics.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `start_time` | RFC 3339 | Start of time range |
| `end_time` | RFC 3339 | End of time range |

**Response:**

```json
{
  "total_calls": 196,
  "success_count": 186,
  "failure_count": 10,
  "success_rate": 0.949,
  "unique_users": 12,
  "unique_tools": 12,
  "enrichment_rate": 0.85,
  "avg_duration_ms": 522,
  "p50_duration_ms": 320,
  "p95_duration_ms": 1450,
  "p99_duration_ms": 2400,
  "avg_response_chars": 1850,
  "by_tool": [
    {"name": "trino_query", "count": 65},
    {"name": "datahub_search", "count": 48},
    {"name": "trino_describe_table", "count": 32}
  ],
  "by_user": [
    {"name": "marcus.johnson@example.com", "count": 45},
    {"name": "lisa.chang@example.com", "count": 38}
  ],
  "timeseries": [
    {"timestamp": "2026-04-15T08:00:00Z", "total": 12, "errors": 1},
    {"timestamp": "2026-04-15T09:00:00Z", "total": 28, "errors": 2}
  ],
  "recent_errors": [
    {
      "id": "evt_err001",
      "timestamp": "2026-04-15T10:41:18Z",
      "user_email": "marcus.johnson@example.com",
      "tool_name": "trino_query",
      "error_message": "Query exceeded timeout of 30 seconds",
      "duration_ms": 30012
    }
  ]
}
```

### Audit Metrics: Enrichment

```
GET /api/v1/admin/audit/metrics/enrichment
```

Returns enrichment statistics: how often enrichment is applied, which modes are used, and token savings from deduplication.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `start_time` | RFC 3339 | Start of time range |
| `end_time` | RFC 3339 | End of time range |

**Response:**

```json
{
  "total_calls": 1500,
  "enriched_calls": 1200,
  "enrichment_rate": 0.80,
  "full_count": 800,
  "summary_count": 300,
  "reference_count": 100,
  "none_count": 0,
  "total_tokens_full": 450000,
  "total_tokens_dedup": 120000,
  "tokens_saved": 330000,
  "avg_tokens_full": 375.0,
  "avg_tokens_dedup": 100.0,
  "unique_sessions": 45
}
```

| Field | Type | Description |
|-------|------|-------------|
| `total_calls` | int | Total tool calls in the time range |
| `enriched_calls` | int | Calls where enrichment was applied |
| `enrichment_rate` | float | Fraction of calls that were enriched (0.0–1.0) |
| `full_count` | int | Calls using `full` enrichment mode |
| `summary_count` | int | Calls using `summary` dedup mode |
| `reference_count` | int | Calls using `reference` dedup mode |
| `none_count` | int | Calls using `none` dedup mode |
| `total_tokens_full` | int64 | Sum of full enrichment tokens |
| `total_tokens_dedup` | int64 | Sum of dedup enrichment tokens |
| `tokens_saved` | int64 | Estimated tokens saved by deduplication |
| `avg_tokens_full` | float | Average tokens per full enrichment |
| `avg_tokens_dedup` | float | Average tokens per dedup enrichment |
| `unique_sessions` | int | Distinct sessions in the time range |

### Audit Metrics: Discovery Patterns

```
GET /api/v1/admin/audit/metrics/discovery
```

Returns session-level discovery patterns: how often users explore the catalog (DataHub) before querying (Trino), and which discovery tools are most popular.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `start_time` | RFC 3339 | Start of time range |
| `end_time` | RFC 3339 | End of time range |

**Response:**

```json
{
  "total_sessions": 100,
  "discovery_sessions": 75,
  "query_sessions": 80,
  "discovery_before_query": 60,
  "discovery_rate": 0.75,
  "query_without_discovery": 20,
  "top_discovery_tools": [
    {"name": "datahub_search", "count": 150},
    {"name": "datahub_get_schema", "count": 90}
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `total_sessions` | int | Total sessions in the time range |
| `discovery_sessions` | int | Sessions that used DataHub tools |
| `query_sessions` | int | Sessions that used Trino tools |
| `discovery_before_query` | int | Sessions where DataHub was used before Trino |
| `discovery_rate` | float | Fraction of sessions that used discovery (0.0–1.0) |
| `query_without_discovery` | int | Sessions that queried Trino without using DataHub first |
| `top_discovery_tools` | array | Most-used discovery tools, sorted by count |

## Connection Instance Endpoints

Connection instance endpoints manage database-backed toolkit connections. These endpoints require a database connection. Read endpoints are always available; write endpoints require database config mode.

### List Connection Instances

```
GET /api/v1/admin/connection-instances
```

Returns all database-managed connection instances ordered by kind and name.

**Response:**

```json
[
  {
    "kind": "trino",
    "name": "prod",
    "config": {"host": "trino.example.com", "port": 8080},
    "description": "Production Trino cluster",
    "created_by": "admin@example.com",
    "updated_at": "2025-01-15T14:30:00Z"
  }
]
```

### Get Connection Instance

```
GET /api/v1/admin/connection-instances/{kind}/{name}
```

Returns a single connection instance by toolkit kind and instance name.

**Response:**

```json
{
  "kind": "trino",
  "name": "prod",
  "config": {"host": "trino.example.com", "port": 8080, "catalog": "hive"},
  "description": "Production Trino cluster",
  "created_by": "admin@example.com",
  "updated_at": "2026-01-15T14:30:00Z"
}
```

### Create or Update Connection Instance

```
PUT /api/v1/admin/connection-instances/{kind}/{name}
```

Creates or updates a database-managed connection instance. Only available in database config mode.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `kind` | Toolkit kind: `trino`, `datahub`, or `s3` |
| `name` | Instance name |

**Request Body:**

```json
{
  "config": {"host": "trino.example.com", "port": 8080},
  "description": "Production Trino cluster"
}
```

**Response** (`200 OK`):

```json
{
  "kind": "trino",
  "name": "prod",
  "config": {"host": "trino.example.com", "port": 8080},
  "description": "Production Trino cluster",
  "created_by": "admin@example.com",
  "updated_at": "2026-04-15T14:30:00Z"
}
```

### Delete Connection Instance

```
DELETE /api/v1/admin/connection-instances/{kind}/{name}
```

Deletes a database-managed connection instance. Only available in database config mode.

**Response:** `204 No Content` (no body)

## Gateway Endpoints

Gateway connections (kind `mcp`) proxy upstream MCP servers and re-expose
their tools as `<connection_name>__<remote_tool>`. They use the standard
[Connection Instance](#connection-instance-endpoints) CRUD endpoints with
`kind=mcp`, plus the gateway-specific endpoints below for testing,
re-discovery, OAuth flow, and cross-enrichment rules.

See [Gateway Toolkit](gateway.md) for the full feature reference.

### Test Connection

```
POST /api/v1/admin/gateway/connections/{name}/test
```

Dials the upstream MCP and returns its tool list **without saving** the
connection. Used by the admin UI to validate credentials before persisting.

The body is the same shape as `PUT /connection-instances/mcp/{name}`. The
`[REDACTED]` placeholder is honored for sensitive fields so an existing
connection can be re-tested without re-entering secrets.

**Response (`200 OK`):**

```json
{
  "healthy": true,
  "tools": [
    {"name": "echo", "local_name": "vendor__echo", "description": "Echo input message"},
    {"name": "add",  "local_name": "vendor__add",  "description": "Sum two integers"},
    {"name": "now",  "local_name": "vendor__now",  "description": "Return current UTC time"}
  ]
}
```

`tools[].local_name` is what the proxied tool will surface as in
`tools/list` once the connection is persisted (`<connection_name>__<remote_tool>`).
On failure, the response carries `{"healthy": false, "error": "..."}`.

### Refresh Connection

```
POST /api/v1/admin/gateway/connections/{name}/refresh
```

Re-dials a stored gateway connection and re-registers its tool catalog on
the live MCP server. Use after an upstream changes its tool set.

**Response (`200 OK`):**

```json
{
  "healthy": true,
  "tools": ["echo", "add", "now"]
}
```

The `tools` array here is just the remote tool names (no `local_name`
because they're already registered with their gateway-prefixed names on
the live server). On failure, `{"healthy": false, "error": "..."}`.

### Begin OAuth Authorization-Code Flow

```
POST /api/v1/admin/gateway/connections/{name}/oauth-start
```

For connections configured with `auth_mode=oauth` and
`oauth_grant=authorization_code`. Generates a PKCE verifier + state token,
records them in the platform's PKCE state store (in-memory by default,
Postgres-backed when a database is configured for multi-replica safety),
and returns the upstream's authorization URL.

The admin UI opens the returned `authorization_url` in a new tab. After
the operator authenticates with the upstream provider, the upstream
redirects to `/api/v1/admin/oauth/callback` (below) with the auth code
and state.

**Request Body** (optional):

```json
{
  "return_url": "/portal/admin/connections"
}
```

**Response (`200 OK`):**

```json
{
  "authorization_url": "https://login.example.com/authorize?response_type=code&client_id=...&code_challenge_method=S256&code_challenge=...&state=...&redirect_uri=https%3A%2F%2Fplatform.example.com%2Fapi%2Fv1%2Fadmin%2Foauth%2Fcallback",
  "state": "U9U-U5mpXbvIbRKOKUX2pGlx9KC3uUeqERo1e-kUcdc",
  "redirect_uri": "https://platform.example.com/api/v1/admin/oauth/callback",
  "expires_at": "2026-04-25T20:51:01Z"
}
```

**Errors:**

- `404 Not Found` — connection does not exist
- `409 Conflict` — connection is not configured for `authorization_code` OAuth

### OAuth Callback (public)

```
GET /api/v1/admin/oauth/callback?code=...&state=...
```

**Public endpoint** — does not require an admin auth header. The upstream
OAuth provider redirects the operator's browser here after sign-in. The
state token (carried in the query string) authenticates the callback by
matching the prior `oauth-start` record.

The handler exchanges `code` for tokens at the upstream's token endpoint,
encrypts them at rest in `gateway_oauth_tokens` (AES-256-GCM via the
platform's field encryptor when `ENCRYPTION_KEY` is set), and redirects
the browser to the original `return_url` (or `/portal/admin/connections`
by default).

On error (missing/expired state, upstream error, token exchange failure)
the handler renders an HTML error page so a stranded browser tab still
gives a useful message.

**Response:** `302 Found` on success; `400 Bad Request` (HTML) on error.

### List Enrichment Rules

```
GET /api/v1/admin/gateway/connections/{name}/enrichment-rules
```

Lists cross-enrichment rules attached to a gateway connection. Optionally
filter by tool name.

**Query Parameters:**

| Parameter | Description |
|-----------|-------------|
| `tool` | Filter to rules whose `tool_name` matches |
| `enabled` | `true` to return only enabled rules |

**Response (`200 OK`):**

```json
[
  {
    "id": "01j3z7n7d6y2g3xq4y9k7m9c8q",
    "connection_name": "vendor",
    "tool_name": "vendor__get_contact",
    "when_predicate": {"kind": "response_contains", "paths": ["$.email"]},
    "enrich_action": {
      "source": "trino",
      "operation": "query",
      "parameters": {
        "connection": "warehouse",
        "sql_template": "SELECT lifetime_value, last_order_at FROM mart.customers WHERE email = :email",
        "email": "$.response.email"
      }
    },
    "merge_strategy": {"kind": "path", "path": "warehouse_signals"},
    "description": "Attach lifetime value + last-order date when the proxied response carries an email",
    "enabled": true,
    "created_by": "admin@example.com",
    "created_at": "2026-04-15T14:30:00Z",
    "updated_at": "2026-04-15T14:30:00Z"
  }
]
```

### Get Enrichment Rule

```
GET /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}
```

Returns a single rule by its server-assigned id. Same shape as a single
element of the list response above.

### Create Enrichment Rule

```
POST /api/v1/admin/gateway/connections/{name}/enrichment-rules
```

Creates a new rule on the named connection. Returns the persisted rule
with its server-assigned `id`.

### Update Enrichment Rule

```
PUT /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}
```

Replaces an existing rule. The `id` is server-assigned at create time.

### Delete Enrichment Rule

```
DELETE /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}
```

**Response:** `204 No Content`

### Dry-Run Enrichment Rule

```
POST /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}/dry-run
```

Evaluates a rule against a sample tool call **without side effects** —
runs the `when` predicate, evaluates JSONPath bindings, executes the
read-only source operation, and returns what the merged response would
look like. Used by the admin UI's rule editor preview pane.

**Request Body:**

```json
{
  "args": {"contact_id": "C-1234"},
  "response": {"email": "ada@example.com", "name": "Ada Lovelace"},
  "user": {"id": "u_123", "email": "alice@example.com"}
}
```

**Response (`200 OK`):**

```json
{
  "response": {
    "email": "ada@example.com",
    "name": "Ada Lovelace",
    "warehouse_signals": [
      {"lifetime_value": 4250.0, "last_order_at": "2026-03-19T10:14:22Z"}
    ]
  },
  "warnings": [],
  "fired": [
    {
      "rule_id": "01j3z7n7d6y2g3xq4y9k7m9c8q",
      "matched": true,
      "duration_ms": 142
    }
  ]
}
```

`response` is the merged result the proxied tool would have returned if
the rule had fired live. `warnings` carries any non-fatal binding /
source errors. `fired` contains a per-rule trace (only the dry-run rule
in this case; the live engine's same shape carries every rule that
evaluated against the call).

## Knowledge Endpoints

Knowledge endpoints require `knowledge.enabled: true` and a configured database. Without a database, endpoints return `409 Conflict`. For the full knowledge API reference, see [Knowledge Admin API](../knowledge/admin-api.md).

**Endpoint summary:**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/knowledge/insights` | List insights with filtering |
| `GET` | `/knowledge/insights/stats` | Insight statistics |
| `GET` | `/knowledge/insights/{id}` | Get single insight |
| `PUT` | `/knowledge/insights/{id}` | Update insight text/category |
| `PUT` | `/knowledge/insights/{id}/status` | Approve or reject |
| `GET` | `/knowledge/changesets` | List changesets |
| `GET` | `/knowledge/changesets/{id}` | Get single changeset |
| `POST` | `/knowledge/changesets/{id}/rollback` | Rollback changes |

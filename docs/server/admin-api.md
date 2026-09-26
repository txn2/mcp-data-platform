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

### What the reference covers

The spec is not admin-only. Alongside the `/api/v1/admin/*` control plane it
documents the caller-facing surfaces a non-MCP client actually calls: the
[API gateway](api-gateway.md#rest-gateway-for-non-mcp-clients) data plane under
the `Gateway` tag (`POST /api/v1/gateway/{connection}/invoke` and
`/invoke-raw`, with their request body, the platform-versus-upstream status
split and the retry rules), the portal and resource routes, the DataHub catalog
surface, and the table registrations. Operations are grouped into a **User API**
and an **Admin API** heading by `x-tagGroups`.

Two gates hold this together, because both failure modes were silent. A route
registered on a mux but carrying no annotations is caught by
`TestAdminCatalogRouteParity`, which reads every registration pattern in the
source tree and refuses one it cannot resolve statically rather than skipping
it. A tag used by an operation but missing from `scripts/swagger-tag-groups.py`
is caught by `TestSwaggerTagsAreDescribedAndGrouped`, since such a tag renders
as an untitled bucket outside both headings.

### The landing section

`info.description` is the reference's introduction, and ReDoc splits it on its
top-level headings into navigation entries of their own: Getting started (the
base URL and one complete request with its response), Authentication, Calling a
connection through the gateway, Conventions (paging, the RFC 9457 error body,
the platform-versus-upstream status split, and the `X-Platform-Instance`
response header), and where the MCP surface is for a reader who wanted that one.

It is authored in `internal/apidocs/introduction.md` and injected by
`scripts/swagger-tag-groups.py` alongside the tag descriptions, because prose of
that length does not belong in a Go comment. The same script gives each
`securityDefinitions` entry its description, so an operation's Authorizations
block says what each credential is and where one is obtained rather than naming
two schemes.

### The base URL the document names

`@host` in the annotations is `localhost:8080`, and the embedded document keeps
it: that is the loopback address the platform-admin self-connection reaches the
platform on. The copy served over HTTP names the origin it was served from
instead, since a reader always fetches the document over the origin it
describes. A `Host` that is not a host is ignored rather than reflected.

### Reading it as reference material

The same document is also rendered with ReDoc, whose three-column layout suits
reading over trying calls out. Swagger UI keeps the "Try it out" button for an
operator holding an API key. Both are reachable from the portal's admin
navigation, and the caller-facing [operation browser](../portal/apis.md) links
to the `Gateway` tag beside the call it hands you.

ReDoc is themed from the portal's own palette
(`ui/src/pages/api-reference/redocTheme.ts`), with one stylesheet
(`redoc.css`) for the components ReDoc styles with a literal rather than a
theme key. What holds it is a contrast sweep in
`ui/e2e/interactive/api-reference.spec.ts`: it walks every element of the
rendered document on both themes, resolves the background each one's text
actually sits on, and fails on any pair below WCAG AA for its size. A renderer
the portal embeds but does not style is checked over its whole surface or not at
all, so which routes the mock fixture carries is decided by which constructs the
served document contains.

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
| `portal.logo_email` | string | `""` | Raster PNG logo URL for notification emails (mail clients strip SVG); unset renders the brand wordmark alone |

## Admin Portal

When `portal.enabled: true`, an interactive web dashboard is served at `/portal/`. The portal provides:

- **Dashboard**: Real-time platform health with activity timelines, top tools/users, performance percentiles, and error monitoring
- **Tools**: Connection overview, tool inventory with descriptions, and interactive tool execution with semantic enrichment display
- **Audit Log**: Searchable event log with detail drawer showing full request metadata and parameters
- **Knowledge**: Insight statistics, governance workflow with approve/reject actions, and changeset tracking

![Admin Portal Dashboard](../images/screenshots/light/admin-admin-dashboard-light.webp#only-light)![Admin Portal Dashboard](../images/screenshots/dark/admin-admin-dashboard-dark.webp#only-dark)

The portal requires authentication — access it with the same credentials used for admin API requests. In production builds, the service worker (`mockServiceWorker.js`) is stripped automatically.

See the [Admin Portal guide](../portal/index.md) for a complete visual walkthrough.

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

### Request Body Validation

Write endpoints decode JSON request bodies strictly: an unknown or mis-named
field is rejected with `400 Bad Request` naming the offending field, rather than
being silently dropped. This prevents a typo from changing behavior: for
authorization-defining resources such as personas, a dropped `deny_tools` field
would otherwise create a more permissive persona than intended.

```json
{
  "type": "about:blank",
  "title": "Bad Request",
  "status": 400,
  "detail": "unknown field \"tools\""
}
```

For example, `POST /personas` with the nested config shape
`{"tools": {"allow": [...]}}` is rejected because the documented fields are the
flat `allow_tools`/`deny_tools`; likewise `POST /tools/call` with `arguments`
(instead of the documented `parameters`) is rejected rather than executing the
tool with empty parameters.

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
    },
    {
      "kind": "mcp",
      "name": "vendor-crm",
      "connection": "vendor-crm",
      "tools": ["vendor-crm__search"],
      "health": {
        "reachable": false,
        "last_success": "2026-06-09T12:00:00Z",
        "last_error": "dial tcp: connection refused"
      }
    }
  ],
  "total": 2
}
```

The optional `health` object reports a gateway (`mcp` kind) upstream's runtime
reachability: `reachable` (live session with no failed last call),
`last_success` (RFC3339 UTC time of the last successful forwarded call, omitted
when none), and `last_error` (most recent call or connect failure). It is
present only for connection kinds that hold a live session and is the same
state the `list_connections` MCP tool reports, so the admin UI and the tool
never show conflicting health for the same connection. Kinds without
reachability tracking omit the field.

## Index Jobs Endpoints

Cross-kind embedding-index health for every consumer of the shared `index_jobs` queue (`pkg/indexjobs`). These back the admin portal's **Indexing** dashboard. They degrade gracefully when no queue is wired (no database or no configured embedding provider): the read endpoints return the provider status with an empty `kinds` list rather than an error.

### Index Jobs Summary

```
GET /api/v1/admin/index-jobs
```

Returns embedding-provider health plus one rollup row per registered kind: a plain-language health `verdict`, per-state unit counts, the number of units with open failures, last activity, and coverage where derivable.

**Response:**

```json
{
  "provider": { "kind": "ollama", "model": "nomic-embed-text", "dimension": 768, "status": "ok" },
  "kinds": [
    {
      "kind": "api_catalog",
      "verdict": "degraded",
      "pending": 0, "running": 0, "succeeded": 6, "failed": 2, "retrying": 0,
      "last_activity": "2026-05-30T12:00:00Z",
      "coverage": { "indexed": 142, "expected": 168, "expected_known": true }
    },
    {
      "kind": "tools",
      "verdict": "indexing",
      "pending": 0, "running": 1, "succeeded": 1, "failed": 0, "retrying": 0,
      "last_activity": "2026-05-30T12:02:00Z",
      "coverage": { "indexed": 87, "expected": 0, "expected_known": false }
    }
  ]
}
```

`verdict` is one of `healthy`, `indexing`, or `degraded`, derived server-side from the same counts and coverage the response carries so the lead health word and the detail metrics can never disagree. `healthy` is the single resting state for any fully-indexed, quiescent, failure-free kind, whether or not it has job history (a kind whose vectors were seeded outside the queue simply has a null `last_activity`); recency is read from `last_activity`, not the verdict.

`indexing` requires queued work that is getting somewhere. A failed unit is re-queued, so a kind failing every unit the same way always carries a pending count, and the pending count alone would report a permanent total failure as a pass in flight. Two signals can argue that a kind is progressing, and either one is enough: more units resting on a success than carrying an open failure, or more persisted vectors than broken units (this second one survives a full re-enqueue, where an embedding-model swap makes every unit a gap at once and no unit rests on a success). Only when neither holds does queued work read `degraded`.

`pending`/`running`/`succeeded` are per-unit latest-status counts ("N units whose last run was X"), not job counts. `failed` is different in kind: it is the number of distinct units carrying an open failed job (`status='failed'` and not yet resolved), the same population `GET /api/v1/admin/index-jobs/failures` enumerates (that endpoint bounds how many it returns), so the count and the failure list can never disagree about what is open. A unit under retry is counted under both `pending` and `failed`; `failed` drops to zero once every failure is superseded by a later success or dismissed. `retrying` is the pending jobs waiting out a retry backoff (at least one attempt behind them), a subset of `pending`, counted over the whole table; the dashboard's Retry backoff panel shows it beside the first page of those jobs.

`coverage.expected_known` is `true` for the current kinds, so all render a real indexed/expected ratio. api-catalog's expected comes from its stamped `operation_count`; the tools kind writes its complete registered set atomically on each index, so its indexed vector count is also its expected count (reported as both halves of the ratio). A kind reports `false` only when it has no expected total to show, in which case the dashboard shows an indexed-only state.

### Index Jobs List

```
GET /api/v1/admin/index-jobs/jobs?kind=&status=&source_id=&retrying=&limit=
```

Returns `index_jobs` rows newest first. All filters are optional; an omitted `kind` lists across every kind. `status` must be one of `pending`, `running`, `succeeded`, `failed`. `retrying=true` narrows to pending jobs with at least one attempt behind them, the jobs waiting out a retry backoff. `limit` defaults to 50 and is capped at 500.

A page of this list is the newest rows, so during a backlog it holds only pending jobs. The dashboard therefore asks it narrow questions (`status=running` for In flight, `retrying=true` for Retry backoff) and reads throughput and latency from the `indexjob_*` metrics (see [Observability](observability.md#background-indexing)), not from a page of rows.

`trigger` reports what produced the row: `write` (the consumer's own write path enqueued it when the source row was created or its indexed text changed), `reconciler` (the periodic gap sweep), or `manual_retry` (the re-index escape hatch, which skips the worker's text-hash dedup).

**Response:**

```json
{
  "jobs": [
    {
      "id": 105, "source_kind": "api_catalog", "source_id": "github|v3",
      "trigger": "reconciler", "status": "pending", "attempts": 2,
      "last_error": "embed batch: provider timeout after 30s",
      "next_run_at": "2026-05-30T12:03:30Z", "items_done": 0
    }
  ]
}
```

### Re-index

```
POST /api/v1/admin/index-jobs/reindex
```

Enqueues manual-retry jobs. With a `source_id` it targets exactly that unit (the failure-triage Retry button); without, it re-enqueues every unit the kind currently reports as out of sync. Enqueue is idempotent — a unit with an open job is not double-queued.

**Request:**

```json
{ "kind": "api_catalog", "source_id": "github|v3" }
```

**Response (`202 Accepted`):**

```json
{ "status": "queued", "enqueued": ["github|v3"], "count": 1 }
```

Returns `404` for an unregistered kind and `409` when no queue is wired.

### Failure Triage

```
GET /api/v1/admin/index-jobs/failures?kind=&limit=
```

Returns the units with open (unresolved) failures, one entry per unit, most-recently-failed first. A unit leaves this set automatically once a later job for the same `(source_kind, source_id)` succeeds (`Complete` resolves superseded failures), or when an operator dismisses it. Both filters are optional; an omitted `kind` lists across every kind. `limit` defaults to 50 and is capped at 500.

**Response:**

```json
{
  "failures": [
    {
      "source_kind": "api_catalog", "source_id": "acme|v1",
      "latest_job_id": 106, "last_error": "embed batch: provider timeout after 30s on spec \"acme\"",
      "attempts": 5, "occurrences": 2,
      "first_failed_at": "2026-05-30T10:00:00Z", "last_failed_at": "2026-05-30T11:00:00Z",
      "last_succeeded_at": "2026-05-29T09:00:00Z",
      "parked_until": "2026-05-30T11:30:00Z"
    }
  ]
}
```

`occurrences` is how many open failed rows the unit has (`>1` means it failed, was retried, and failed again without an intervening success). `last_succeeded_at` is omitted when the unit has never succeeded, so the dashboard can distinguish a unit that used to work from one that never has.

`parked_until` is present only while the periodic gap sweep is deferring the unit, and says when it will be re-queued. Each open failed row is five exhausted worker attempts, so a unit that reaches three of them is failing deterministically rather than transiently; the sweep then backs off instead of enqueueing a fresh job every `ReconcilerInterval`. The delay starts at 30 minutes, doubles with each further failure, and is capped at 6 hours.

The deferral is a delay, never a block. It applies to the automatic sweep alone: a write to the source row and the re-index escape hatch below both enqueue immediately, and because the cap is a bound rather than a terminal state, a unit whose cause has cleared recovers on its own without an operator touching it. That matters because this counter cannot distinguish a permanently unindexable input from a provider outage long enough to exhaust every unit in the corpus, and freezing the whole index behind the second case would be worse than the re-queue loop the deferral exists to stop.

### Dismiss Failure

```
POST /api/v1/admin/index-jobs/dismiss
```

Resolves every open failed job for one unit, clearing it from the failure-triage surface. This is the explicit fallback for a failure that will never be superseded (for example a removed consumer's leftover rows, which auto-resolve cannot reach because no future job for the kind will succeed). Idempotent: dismissing an already-clean unit returns `200` with `resolved: 0`.

**Request:**

```json
{ "kind": "api_catalog", "source_id": "acme|v1" }
```

**Response (`200 OK`):**

```json
{ "status": "resolved", "resolved": 2 }
```

Returns `400` when `kind` or `source_id` is missing and `409` when no queue is wired.

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

Sets a config entry for a whitelisted key. The change takes effect immediately on every replica, without restart: the platform resolves these keys from the store on each read rather than caching them per process. Requires a database connection. Returns `400 Bad Request` for non-whitelisted keys and `409 Conflict` when no database is configured.

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
  "allow_tools": ["*"],
  "deny_tools": ["*_delete_*"],
  "tools": ["datahub_browse", "fetch", "search", "trino_describe_table", "trino_query"],
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
  "allow_tools": ["platform_info", "search", "fetch", "datahub_*"],
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

Some tools must be granted together — `search` with `fetch`, and `memory_capture`
or `apply_knowledge` with `search`. A write that breaks a pair still succeeds
(the check is advisory, not a gate) and the server logs a warning naming the
persona, the missing tool, and the fix. See
[Some tools are a unit](../personas/overview.md#some-tools-are-a-unit).

**Response** (`201 Created`):

```json
{
  "name": "viewer",
  "display_name": "Data Viewer",
  "description": "Read-only access to DataHub",
  "roles": ["viewer"],
  "priority": 0,
  "allow_tools": ["platform_info", "search", "fetch", "datahub_*"],
  "deny_tools": [],
  "tools": ["datahub_browse", "datahub_get_lineage", "fetch", "search"],
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

These routes require the admin persona and accept any credential that carries it, an API key included: a service key whose roles reach that persona lists, issues and revokes keys, including a key bound to a person, which then authenticates as that person. The self-service routes under `/api/v1/portal/api-keys` are the ones that refuse an API-key request, since they act on the strength of the caller being signed in. See [API Key Authentication](../auth/api-keys.md) for what that means for the roles a key is given.

Every replica of a deployment answers these routes, and authenticates API keys, from the one key store in the database. A key created through one replica is listed by, and authenticates on, every replica as soon as the create returns; a deleted key is refused by every replica as soon as the delete returns, and its name can be used again at once. A replica confirms a database key against the store on each request that presents it, so a deleted key is never accepted because a replica had not yet heard of the delete. When the store cannot be read, the key routes answer `500 Internal Server Error` rather than a listing that may be out of date, and a database key is refused rather than accepted unconfirmed. Keys declared in the configuration file are unaffected by the store.

### List Auth Keys

```
GET /api/v1/admin/auth/keys
```

Returns all API keys (key values are never exposed, only names and roles). Each key carries `persona`, the persona its roles reach by the rules the MCP authorizer applies (an explicit `personas.role_mapping.oidc_to_persona` entry, then the personas' `roles`), or `no_persona: true` when they reach none. A key flagged `no_persona` authenticates and lists no tools; the portal's API Keys page badges it **No persona**.

**Response:**

```json
{
  "keys": [
    {
      "name": "admin",
      "roles": ["admin"],
      "source": "file",
      "persona": "admin"
    },
    {
      "name": "ci-pipeline",
      "email": "ci@example.com",
      "description": "CI/CD pipeline integration",
      "roles": ["analyst"],
      "expires_at": "2026-07-15T00:00:00Z",
      "source": "database",
      "persona": "analyst"
    },
    {
      "name": "expired-key",
      "email": "legacy@example.com",
      "description": "Decommissioned service key",
      "roles": ["viewer"],
      "expires_at": "2026-03-31T00:00:00Z",
      "expired": true,
      "source": "database",
      "no_persona": true
    }
  ],
  "total": 3
}
```

**Status Codes:** `200 OK`, `500 Internal Server Error` (key store unreadable)

### Create Auth Key

```
POST /api/v1/admin/auth/keys
```

Generates a new API key: 64 hexadecimal characters, the encoding of 32 random bytes. Only available in `database` config mode. The key value is returned only once; the store keeps its bcrypt hash.

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
| `user_email` | string | no | Issue the key against this person's account: it authenticates as them, with their user id and address. The platform must have seen them sign in, or the create is refused naming that. Omit for the standalone service key. |
| `roles` | array | yes, unless `user_email` is set | Roles to assign. On a bound key, omitting them makes the key carry whatever roles that person holds, read on every request; giving them replaces the person's roles on that key and is used verbatim. |
| `expires_in` | string | no | Duration until expiry (e.g., `24h`, `720h`, `8760h`). Omit for no expiration. |

**Response** (`201 Created`):

```json
{
  "name": "ci-pipeline",
  "email": "ci@example.com",
  "description": "CI/CD pipeline integration",
  "key": "3f9a1c07e2b84d56a0c3e1f7b9d2468ace13579bdf02468ace13579bdf024681",
  "roles": ["analyst"],
  "expires_at": "2026-05-18T14:30:00Z",
  "warning": "Store this key securely. It will not be shown again.",
  "persona": "analyst"
}
```

A key whose roles reach no persona is still created, since the persona carrying them may be defined afterward, and the response names the problem in `warnings` in place of `persona`:

```json
{
  "name": "ci-pipeline",
  "key": "3f9a1c07e2b84d56a0c3e1f7b9d2468ace13579bdf02468ace13579bdf024681",
  "roles": ["finance"],
  "warning": "Store this key securely. It will not be shown again.",
  "warnings": [
    "No persona carries any of the roles \"finance\", so this key authenticates and lists no tools. Roles the personas carry: \"admin\", \"analyst\", \"dp_finance\". \"finance\" is the name of a persona, not one of its roles; that persona carries \"dp_finance\"."
  ]
}
```

The portal shows each warning in the banner that shows the new key.

**Status Codes:** `201 Created`, `400 Bad Request`, `409 Conflict` (a key with the name exists, whichever replica created it; of two creates of one name at once, one answers `201` and the other `409`), `500 Internal Server Error` (key store unreadable), `405 Method Not Allowed` (file config mode)

### Delete Auth Key

```
DELETE /api/v1/admin/auth/keys/{name}
```

Deletes an API key. Only available in `database` config mode. A key declared in the configuration file cannot be deleted here; a key that is both in the file and in the store loses its store half and keeps authenticating with the file's value.

**Response** (`200 OK`):

```json
{
  "status": "deleted"
}
```

**Status Codes:** `200 OK`, `404 Not Found` (no key with the name, including one already deleted through another replica), `409 Conflict` (the key is declared only in the configuration file), `500 Internal Server Error` (key store unreadable), `405 Method Not Allowed` (file config mode)

## User Endpoints

The known-users directory (#614) records people (first name, last name, email)
so the portal share picker can resolve and suggest names. It is not an
authorization layer and grants no access. Endpoints require a database;
without one they are not registered. Anyone who authenticates is upserted into
the directory automatically (admin-entered names take precedence — a sign-in
only fills blank name fields). Write endpoints require `database` config mode.

The portal exposes a read-only list of the same directory to any authenticated
user at `GET /api/v1/portal/users` (the share picker), returning only email and
name fields.

### List Users

```
GET /api/v1/admin/users
```

Returns directory users. Optional query params: `q` (case-insensitive match on
email or name), `limit`, `offset`.

**Response:**

```json
{
  "users": [
    {
      "email": "marcus.johnson@example.com",
      "first_name": "Marcus",
      "last_name": "Johnson",
      "source": "auth",
      "confirmed": true,
      "last_seen_at": "2026-06-12T14:30:00Z",
      "created_at": "2026-06-01T09:00:00Z",
      "updated_at": "2026-06-12T14:30:00Z"
    },
    {
      "email": "dana.lee@example.com",
      "first_name": "Dana",
      "last_name": "Lee",
      "source": "admin",
      "confirmed": false,
      "added_by": "admin@example.com",
      "created_at": "2026-06-10T11:00:00Z",
      "updated_at": "2026-06-10T11:00:00Z"
    }
  ],
  "total": 2
}
```

`source` is `auth` (recorded from a real session) or `admin` (pre-added).
`confirmed` is `true` once the person has been seen via a real authenticated
session.

### Add User

```
POST /api/v1/admin/users
```

Pre-adds a person by email so they are selectable for sharing before they have
ever signed in. Only available in `database` config mode.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `email` | string | yes | Email address (normalized to lowercase) |
| `first_name` | string | no | First name |
| `last_name` | string | no | Last name |

**Status Codes:** `201 Created`, `400 Bad Request`, `409 Conflict` (email exists)

### Update User

```
PUT /api/v1/admin/users/{email}
```

Edits a person's first and/or last name. Omitted fields are left unchanged.
Only available in `database` config mode.

**Status Codes:** `200 OK`, `400 Bad Request`, `404 Not Found`

### Delete User

```
DELETE /api/v1/admin/users/{email}
```

Removes a directory entry. Only available in `database` config mode.

**Status Codes:** `200 OK`, `404 Not Found`

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
| `user_id` | string | Filter by the principal that made the call: an OIDC subject, `apikey:<name>`, or `script:<name>`. See [Who made the call](audit.md#who-made-the-call) |
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

## Session Endpoints

Session endpoints read sessions back from the audit log. A session is not a stored row: it is every audit event sharing one `session_id`, so these endpoints reach as far back as audit retention and keep working after the live session record has expired. They are read-only and require a database; without one the routes are not registered. See [Audit Logging](audit.md#sessions-read-back-from-the-log) for the model.

### List Sessions

```
GET /api/v1/admin/sessions
```

Returns sessions ordered by most recent activity.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `user_id` | string | Filter by the principal that made the call: an OIDC subject, `apikey:<name>`, or `script:<name>`. See [Who made the call](audit.md#who-made-the-call) |
| `kind` | string | Filter by session id origin: `agent` (`dps_`), `portal` (`dpp_`), `script` (`dpx_`), `transport` (everything else) |
| `start_time` | RFC 3339 | Sessions with activity after this time |
| `end_time` | RFC 3339 | Sessions with activity before this time |
| `has_assets` | boolean | Only sessions that saved at least one asset |
| `has_failures` | boolean | Only sessions with at least one failed call |
| `page` | integer | Page number, 1-based (default: 1) |
| `per_page` | integer | Results per page (default: 25, max: 200) |

A time range bounds the session's events, so a session is listed when any of its calls falls inside the window, and its counts describe the calls inside it.

**Response:**

```json
{
  "data": [
    {
      "session_id": "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b",
      "kind": "agent",
      "user_id": "user-uuid-1234",
      "user_email": "marcus.johnson@example.com",
      "persona": "data-engineer",
      "started_at": "2026-04-15T10:38:02Z",
      "last_active_at": "2026-04-15T10:52:47Z",
      "call_count": 5,
      "failure_count": 1,
      "tools": ["save_asset", "search", "trino_query"],
      "connections": ["acme-warehouse"],
      "asset_count": 1,
      "insight_count": 0
    }
  ],
  "total": 42,
  "page": 1,
  "per_page": 25
}
```

`persona` is the persona the handle was minted under while the live session record still exists, and the persona of the session's first event once it has expired.

### Get Session

```
GET /api/v1/admin/sessions/{id}
```

Returns one session: the summary above, plus what it produced and a page of its call timeline. Returns `404` when the audit log holds no call for that id — a session with no calls is not a session.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `page` | integer | Timeline page number, 1-based (default: 1) |
| `per_page` | integer | Timeline entries per page (default: 25, max: 200) |

**Response:**

```json
{
  "session_id": "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b",
  "kind": "agent",
  "user_id": "user-uuid-1234",
  "user_email": "marcus.johnson@example.com",
  "persona": "data-engineer",
  "started_at": "2026-04-15T10:38:02Z",
  "last_active_at": "2026-04-15T10:52:47Z",
  "call_count": 5,
  "failure_count": 1,
  "tools": ["save_asset", "search", "trino_query"],
  "connections": ["acme-warehouse"],
  "asset_count": 1,
  "insight_count": 0,
  "assets": [
    {
      "id": "ast_7c1e",
      "name": "Q3 revenue by region",
      "content_type": "text/csv",
      "created_at": "2026-04-15T10:52:47Z"
    }
  ],
  "insights": [],
  "timeline": [
    {
      "event_id": "evt_a1b2c3d4e5f6",
      "timestamp": "2026-04-15T10:38:02Z",
      "tool_name": "trino_query",
      "purpose": "Summing Q3 revenue by region for the board deck.",
      "toolkit_kind": "trino",
      "connection": "acme-warehouse",
      "success": true,
      "duration_ms": 143
    }
  ],
  "timeline_total": 5
}
```

`timeline` is ordered oldest first and `timeline_total` is the session's full call count, so a caller pages without a second request for the total. `event_id` addresses the same row [Get Audit Event](#get-audit-event) returns.

## Notification Endpoints

Notification endpoints expose the email-delivery history the notification queue leaves behind, so an admin can answer whether notification emails are reaching people and what happened to the ones that did not. They are read-only and require a database; without one the routes are not registered.

The queue purges resolved rows on a retention schedule (30 days by default), so these endpoints report recent history rather than a complete archive. `retention_days` on the stats response states the effective window.

Each user reads their own rows through the self-scoped `GET /api/v1/portal/notifications`, which takes no recipient parameter and omits the delivery error text below.

### List Notifications

```
GET /api/v1/admin/notifications
```

Returns paginated queue rows, newest first.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `recipient` | string | Filter by recipient email. Accepts the display-name form; normalized to the bare address before matching |
| `status` | string | Filter by status: `pending`, `sending`, `sent`, `failed` |
| `category` | string | Filter by category: `share`, `comment`, `mention` |
| `page` | int | Page number, 1-based (default: 1) |
| `per_page` | int | Results per page (default: 50, max: 200) |

**Response:**

```json
{
  "data": [
    {
      "id": 5121,
      "recipient": "marcus.johnson@example.com",
      "category": "share",
      "subject": "lisa.chang@example.com shared the asset \"Q3 Revenue by Region\" with you",
      "digest": false,
      "status": "failed",
      "attempts": 5,
      "last_error": "dial tcp 10.24.0.31:587: connect: connection refused",
      "item_title": "Q3 Revenue by Region",
      "actor": "lisa.chang@example.com",
      "scheduled_for": "2026-07-29T14:02:00Z",
      "created_at": "2026-07-29T14:01:00Z"
    }
  ],
  "total": 133,
  "page": 1,
  "per_page": 50
}
```

`subject` is the same line the recipient's email carried, so a reported message can be matched to its row.

### Get Notification Stats

```
GET /api/v1/admin/notifications/stats
```

Returns per-status counts and the retention window they cover. The `recipient` and `category` filters apply; `status` does not, so a list narrowed to failures still shows how many rows sent.

**Response:**

```json
{
  "pending": 2,
  "sending": 0,
  "sent": 128,
  "failed": 3,
  "total": 133,
  "retention_days": 30
}
```

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

A connection the platform configuration file declares is refused with `409
Conflict`, the same as the delete below. A stored record for such a connection
reached the running process but was discarded at the next restart: the merge
that folds stored connections into the toolkit config skips a name the file
already declares, so the file's config came back and the record went on
describing a state nothing was running.

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

#### Values are stored literally

A config sent here is stored exactly as it arrives. `${VAR}` expansion is a
feature of the platform's configuration FILE, which is expanded before it is
parsed; nothing expands a value written through this API. A config carrying an
unexpanded placeholder is therefore refused with `400 Bad Request` naming every
key it sits on:

```json
{
  "detail": "invalid connection config: password=\"${TRINO_PASSWORD}\", user=\"${TRINO_USER}\": a ${...} placeholder is stored literally on a database-managed connection, which the platform expands only in its configuration file, so this connection would be created and then fail every call. Send the resolved value, or declare this connection in the platform configuration file where ${VAR} is expanded"
}
```

Before that refusal existed, such a connection was created, listed and read back
looking correct, and every query against it failed at DSN construction with
`net/url: invalid userinfo`. Nested values are checked too, so a placeholder in
an api connection's `static_headers` is named as `static_headers.X-Subscription`.

### Test a Connection Instance

```
POST /api/v1/admin/connection-instances/{kind}/{name}/test
```

Opens the connection and asks its upstream one harmless question, then reports
what came back. It persists nothing and changes nothing.

What each kind asks:

| Kind | The question |
|------|--------------|
| `trino` | `SELECT 1` against the coordinator; on a scratch connection that accepts writes, also a call of each partition procedure on a table that does not exist (see below) |
| `s3` | a bucket listing |
| `graphql` | an introspection query at the endpoint |
| `api` | `GET /` at the base URL, through the connection's own client and credential |
| `mcp` | a `tools/list` over the live upstream session |

**Response** (`200 OK`) — the connection answered:

```json
{
  "kind": "trino",
  "name": "prod",
  "ok": true,
  "detail": "the query engine answered SELECT 1"
}
```

**Response** (`503 Service Unavailable`) — it did not:

```json
{
  "kind": "trino",
  "name": "prod",
  "ok": false,
  "detail": "connection \"prod\" could not be opened",
  "error": "invalid DSN: parse \"https://user:pass@trino.example.com:443/hive/public\": net/url: invalid userinfo"
}
```

`detail` says what answered on a success and what was attempted on a failure;
`error` carries the upstream's own words. A success is deliberately specific,
because a bare `ok: true` against the wrong credential reads the same as one
against the right credential.

`409 Conflict` is a third answer, and a different one: no toolkit of that kind
runs in this process, so there is nothing here to open the connection with. The
connection may well be serving on another replica, where the same call will
answer for it.

A connection saved through another replica is tested as soon as its save
returns. This replica takes it on from the connection store first, rather than
waiting for the other replica's announcement, which is what a tool call naming
it does too.

On a Trino connection that accepts writes and names a `scratch:` target, the
test also checks what [webhook sources](webhooks.md) need of that catalog. It
calls `sync_partition_metadata`, `register_partition` and
`unregister_partition` on a table that does not exist. Trino checks access
control before a procedure runs and the Hive connector checks
`hive.allow-register-partition-procedure` before it looks up the table, so
*"Table ... not found"* is the passing answer and nothing is changed. Every
missing setting is named in one `503`:

```json
{
  "kind": "trino",
  "name": "scratch",
  "ok": false,
  "detail": "the query engine answered SELECT 1, but webhook sources cannot run on this scratch connection: the Trino user of connection scratch may not EXECUTE scratch.system.sync_partition_metadata; a catalog rule does not grant procedures, so add a procedures rule for that user on the catalog's system schema (see docs/server/scratch-catalog.md#access-control); ...",
  "error": "... Access Denied: Cannot execute procedure scratch.system.sync_partition_metadata; ..."
}
```

The complete setup is on [Scratch Catalog](scratch-catalog.md).

### Delete Connection Instance

```
DELETE /api/v1/admin/connection-instances/{kind}/{name}
```

Deletes a database-managed connection instance. Only available in database config mode.

**Response:** `204 No Content` (no body)

A connection the platform configuration file declares is refused with `409
Conflict` naming the file as its owner. The configuration file is the only place
such a connection can be removed: deleting the stored row would drop it from
every live toolkit of its kind, on this replica and on every peer the removal
broadcasts to, until each of them restarts and the file put it back.

The stored row is not evidence of who owns the connection. The platform seeds a
credential-free `connection_instances` row for every file-configured connection
so that `mcp:connection:(kind,name)` knowledge-page references resolve, which
means a file connection and a database connection both have one. The list
endpoint below reports the distinction as `file_declared`.

### List Connection Kinds

```
GET /api/v1/admin/connection-kinds
GET /api/v1/admin/connection-kinds/{kind}
```

Returns each kind a connection can be created under, with the JSON Schema of
the `config` object `PUT /connection-instances/{kind}/{name}` takes. A
connection's config is a freeform object on the wire, so without this the only
way to learn a key's name is to read the deployment's own configuration file,
which an operator or an agent without cluster access cannot do.

**Response:**

```json
[
  {
    "kind": "trino",
    "config_schema": {
      "type": "object",
      "required": ["host"],
      "properties": {
        "host": {"type": "string", "description": "Trino coordinator hostname. Required."},
        "read_only": {"type": "boolean", "description": "Refuse write-class statements on this connection. ..."}
      }
    },
    "note": "These are the keys this kind reads. A key not named here is stored and ignored, ..."
  }
]
```

Read the `note` as part of the answer. The schema names the keys a kind reads,
and nothing more: a key it does not name is stored and ignored rather than
refused, a value is stored literally, and a config the schema admits is not
thereby a connection that works. The test endpoint above is what answers that
last question.

### List Effective Connections

```
GET /api/v1/admin/connection-instances/effective
```

Returns the merged view of the connections the platform is actually serving:
every live toolkit connection, overlaid with its stored instance where one
exists, with secrets redacted. Unlike the other connection endpoints this one is
available in file config mode as well, because it reads the running toolkits
rather than the store alone.

**Response:**

```json
[
  {
    "kind": "trino",
    "name": "prod",
    "connection": "prod",
    "description": "Production Trino cluster",
    "source": "both",
    "file_declared": true,
    "tools": ["trino_query", "trino_describe_table"],
    "config": {"host": "trino.example.com", "port": 8080},
    "created_by": "system",
    "updated_at": "2026-01-15T14:30:00Z"
  }
]
```

| Field | Description |
|-------|-------------|
| `source` | `file` when only a live toolkit contributes the connection, `database` when only a stored row does, `both` when each does |
| `file_declared` | `true` when the platform configuration file declares the connection. `source` cannot answer this: the backfilled row makes a file-configured connection report `both` as well. A `file_declared` connection cannot be deleted through this API |

### Cross-replica hot-reload

Connection create, update, and delete take effect on the handling replica
immediately (no restart) and are broadcast to peer replicas over the reload bus
so a multi-replica deployment does not serve stale connections. The reconcile
mechanics (find every live toolkit of the kind, then remove/add the connection)
are shared by the admin hot-reload path and the peer subscriber
(`pkg/connreconcile`) so both apply changes identically.

The broadcast carries the operation so peers apply it safely:

- **Create/update**: peers read the connection store for the new config and
  re-materialize it. If a peer's store read transiently fails, that peer keeps
  its currently-live config in place (it is not dropped over a database blip)
  and a later reload re-materializes it.
- **Delete**: peers remove the connection from their live toolkits **without a
  store read**, so a transient store failure on a peer can never leave a deleted
  connection callable there.

Neither removal applies to a connection the receiving replica's configuration
file declares. A replica that predates the delete refusal above can still
broadcast one, and an upsert whose store read comes back empty removes on the
same reasoning; the file is unaffected by anything the store did, so such a
connection keeps serving. An upsert that finds a row is applied normally, so a
stored override still reaches every replica.

During a rolling upgrade, a delete broadcast from an older replica that predates
the operation tag falls back to the read-and-decide path on newer replicas.

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

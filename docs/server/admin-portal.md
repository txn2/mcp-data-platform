---
description: Admin Portal web dashboard for monitoring, auditing, tool exploration, knowledge governance, and platform configuration. Visual guide with screenshots.
---

# Admin Portal

The Admin Portal is an interactive web dashboard for managing and monitoring the platform. Enable it with `portal.enabled: true` in your configuration.

```yaml
portal:
  enabled: true
  title: "ACME Data Platform"
  logo: https://example.com/logo.svg
  logo_light: https://example.com/logo-for-light-bg.svg
  logo_dark: https://example.com/logo-for-dark-bg.svg

admin:
  enabled: true
  persona: admin
```

The portal is served at `/portal/`. Authentication is required — use the same credentials as the [Admin API](admin-api.md). The sidebar is divided into **User** pages (see [User Portal](portal-user.md)) and **Admin** pages (described here).

### Branding

Customize the sidebar title and logo via `portal.title`, `portal.logo`, `portal.logo_light`, and `portal.logo_dark`. The portal picks the theme-appropriate logo automatically:

- **Light theme**: `logo_light` → `logo` → built-in default
- **Dark theme**: `logo_dark` → `logo` → built-in default

The resolved logo is also used as the browser favicon. A built-in activity icon is used when no logo is configured. Logos should be square SVGs for best results.

### Public Viewer Branding

Shared artifact links (the public viewer at `/portal/view/{token}`) display a two-zone header. The **right zone** shows the platform brand (`portal.title` and `portal.logo`). The **left zone** is an optional implementor brand for the organization deploying the platform:

```yaml
portal:
  implementor:
    name: "ACME Corp"                    # Display name (left zone of public viewer header)
    logo: "https://acme.com/logo.svg"    # URL to SVG logo (fetched once at startup, max 1 MB)
    url: "https://acme.com"              # Clickable link wrapping name + logo
```

All three fields are optional. When omitted, the left zone is hidden and only the platform brand appears. The logo URL must point to an SVG file; it is fetched at server startup and inlined into the HTML.

### Public Viewer Features

The public viewer includes:

- **Light/dark mode** — Defaults to the system `prefers-color-scheme` setting. A toggle button in the header allows switching; the choice is persisted to `localStorage`.
- **Expiration notice** — When the share has an expiration, a notice bar shows the relative time remaining (e.g., "This page expires in 6 hours"). Hidden when the share has no expiry or `hide_expiration` was set at share creation.
- **Notice text** — Configurable per-share via `notice_text`. Defaults to "Proprietary & Confidential. Only share with authorized viewers." Set to `""` to hide the notice entirely.

The `hide_expiration` and `notice_text` fields are set per-share when creating a share via the API:

```json
{"expires_in": "24h", "hide_expiration": true, "notice_text": "Internal use only."}
```

## Dashboard

The Dashboard is the admin home page, providing a real-time overview of platform health across configurable time ranges (1h, 6h, 24h, 7d).

![Dashboard](../images/screenshots/light/admin-admin-dashboard-light.webp#only-light)![Dashboard](../images/screenshots/dark/admin-admin-dashboard-dark.webp#only-dark)

The dashboard includes:

- **System info bar** — Platform name, version, transport, config mode, and enabled features (Audit, Knowledge, OAuth)
- **Summary cards** — Total calls, success rate, average duration, unique users, unique tools, enrichment rate, and error count
- **Activity timeline** — Tool call volume over time (green) with error overlay (red)
- **Top Tools / Top Users** — Horizontal bar charts showing the most active tools and users
- **Performance** — Response time percentiles (P50, P95, P99) and average response size
- **Recent Errors** — Clickable error list with detail drawer
- **Knowledge Insights** — Summary statistics and category breakdown with pending review queue
- **Connections** — All configured toolkit connections with tool counts

## Indexing

The **Indexing** tab of the Dashboard (alongside MCP, API Gateway, Health, and Events) is an admin-only, cross-kind view of embedding-index health for every consumer of the shared `index_jobs` queue (`pkg/indexjobs`): api-catalog operation vectors, tool descriptors, and any future consumer, which gets visibility here for free the moment it registers. Embedding work runs off the request path, so a provider outage, a model dimension mismatch, or repeated retries can silently degrade `ranking=semantic`/`hybrid` to lexical with only a log line as signal; this tab is the single place to answer whether indexing is healthy, what is covered, what failed, and why.

It is system-wide and admin-only by platform convention (operators see all indexing; it is not a per-persona capability). All data is real `index_jobs` and vector-table state — no mocked dimensions. The page polls every 5 seconds so it reflects work as the worker, reconciler, and reaper complete it.

The tab includes:

- **Provider health banner** — The embedding provider's kind, model, and dimension, or a clear degraded state (noop / unconfigured) since a bad provider makes the whole index meaningless and pauses indexing.
- **Per-kind health cards (summary-first)** — Each kind leads with one plain health **verdict** computed server-side: **Up to date** (the single resting state for a fully-indexed, quiescent, failure-free kind), **Indexing…** (work in flight), or **Degraded** (an open failure or a coverage shortfall). Equivalent states look identical: every up-to-date kind shows the same green badge and the same `N / M · 100%` coverage bar (api-catalog's expected comes from `operation_count`; tools writes its complete registered set atomically, so its indexed count is also its expected), and a recency line ("last indexed <relative>", or "fully indexed" for a kind seeded outside the queue, never "never"). The per-unit job-state breakdown ("units by last run") is shown only when there is active work or an open failure, so an up-to-date card is not cluttered with an all-zero or stale stat row. A **Re-index** button re-enqueues every out-of-sync unit of the kind.
- **Throughput timeline** — Completed jobs over time (d3 area), so an operator can see indexing keeping up or stalling.
- **Embed latency** — Per-kind started-to-completed duration (p50 with a p95 marker), surfacing slow passes such as the CPU-only embedder case.
- **In flight** — Running jobs with worker id, lease countdown, and items-done progress for long passes.
- **Retry backoff** — Pending jobs that already failed once, with attempt count and next run time.
- **Failure triage (self-resolving)** — Units with open failures, grouped by error signature. Each unit shows first-seen / last-seen timestamps, occurrence and attempt counts, and "last succeeded" context, with an expandable drill-in to the un-redacted error and the underlying job id. A failure auto-resolves (leaves the panel) once a later job for the same unit succeeds; **Retry** re-enqueues the unit and the card clears when it next succeeds, and **Dismiss** is the explicit fallback that resolves a failure (such as a removed consumer's leftover rows) that no future success will supersede.
- **Jobs drill-down** — A filterable table (by kind and status) of recent jobs with trigger, attempts, last update, and error. Routine timer-driven reconciler successes for a unit (which every replica re-runs on its own schedule) are collapsed into a single "synced ×N" row so they do not drown the table.

The existing per-catalog embedding badges in the API Catalogs panel remain; this tab is the cross-kind superset.

## Tools

The Tools page is a master-detail view. The list on the left groups every registered tool by connection (Trino, DataHub, S3, platform, and gateway-proxied MCP) with search filtering; selecting a tool opens its detail across five tabs.

### Overview

![Tools Overview](../images/screenshots/light/admin-admin-tools-overview-light.webp#only-light)![Tools Overview](../images/screenshots/dark/admin-admin-tools-overview-dark.webp#only-dark)

The Overview tab shows the selected tool's description (with an inline override editor), toolkit kind, connection, title, the JSON input schema, and per-persona access — which personas can call the tool and the rule that decided it.

### Try It

![Tools Try It](../images/screenshots/light/admin-admin-tools-tryit-light.webp#only-light)![Tools Try It](../images/screenshots/dark/admin-admin-tools-tryit-dark.webp#only-dark)

An interactive execution environment for the selected tool:

- **Dynamic parameter form** — Auto-generated from the tool's JSON schema with type-appropriate inputs (text areas for SQL, number fields for limits, dropdowns for enums)
- **Result display** — Rendered markdown tables for structured data, with a Raw toggle for JSON output
- **Execution history** — Timestamped log of tool calls with duration, status, and replay capability

### Activity

![Tools Activity](../images/screenshots/light/admin-admin-tools-activity-light.webp#only-light)![Tools Activity](../images/screenshots/dark/admin-admin-tools-activity-dark.webp#only-dark)

Aggregated call volume, success rate, and average duration for the selected tool over the recent window, with a deep link to the audit log filtered to this tool.

### Enrichment

![Tools Enrichment](../images/screenshots/light/admin-admin-tools-enrichment-light.webp#only-light)![Tools Enrichment](../images/screenshots/dark/admin-admin-tools-enrichment-dark.webp#only-dark)

Shown for gateway-proxied (MCP) tools with a connection. Lists the cross-injection enrichment rules attached to the tool — each rule's predicate, action source and operation, merge strategy, and enabled state. This is where the platform's bidirectional context injection is configured per tool.

### Visibility

![Tools Visibility](../images/screenshots/light/admin-admin-tools-visibility-light.webp#only-light)![Tools Visibility](../images/screenshots/dark/admin-admin-tools-visibility-dark.webp#only-dark)

Toggle the tool's membership in the platform-wide deny list, and preview whether a given persona can access it before committing the change.

## Activity (Dashboard tabs)

The admin Dashboard hosts the platform activity views as tabs: **MCP**, **API Gateway**, **Health**, **Indexing**, and **Events**. (Indexing is documented above.) Each works across configurable time ranges (1h, 6h, 24h, 7d).

### MCP

The MCP tab provides platform-wide analytics over MCP tool-call activity.

![MCP Activity](../images/screenshots/light/admin-admin-audit-mcp-light.webp#only-light)![MCP Activity](../images/screenshots/dark/admin-admin-audit-mcp-dark.webp#only-dark)

Includes summary cards, the activity timeline, and top tools / top users charts — focused on MCP tool calls with performance percentiles and error tracking.

### API Gateway

The API Gateway tab visualizes outbound REST gateway traffic proxied through the platform.

![API Gateway Activity](../images/screenshots/light/admin-admin-audit-apigateway-light.webp#only-light)![API Gateway Activity](../images/screenshots/dark/admin-admin-audit-apigateway-dark.webp#only-dark)

Includes the connection-to-operation traffic flow (Sankey), an inbound-vs-outbound health split by status category, and breakdowns by status class, method, and calling identity.

### Health

The Health tab reports per-node platform health scraped from Prometheus.

![Health](../images/screenshots/light/admin-admin-audit-health-light.webp#only-light)![Health](../images/screenshots/dark/admin-admin-audit-health-dark.webp#only-dark)

Per-node uptime, CPU, resident memory, heap, and goroutine counts across the platform fleet, with any missing metric rendered as a dash.

### Events

The Events tab provides a searchable, filterable log of every tool call.

![Audit Events](../images/screenshots/light/admin-admin-audit-events-light.webp#only-light)![Audit Events](../images/screenshots/dark/admin-admin-audit-events-dark.webp#only-dark)

Features:

- **Filters** — User, tool, status (success/failure), and time range dropdowns
- **Sortable columns** — Timestamp, user, tool, toolkit, connection, duration, status, and enrichment
- **Export** — Export CSV and Export JSON buttons
- **Event detail drawer** — Click any row to open the full detail:
    - **Identity** — User email, persona, session ID
    - **Execution** — Tool name, toolkit, connection, duration
    - **Status** — Success/failure, enrichment status
    - **Transport** — HTTP or stdio, request/response sizes, content block count
    - **Parameters** — Full request parameters as JSON

## Knowledge & Memory (review and promotion)

The separate admin Knowledge & Memory page was merged into the unified **Knowledge** page in the user portal (see [Portal User Guide](portal-user.md#knowledge)). Review and promotion gate on the `apply_knowledge` capability, not an admin role: whoever holds the tool sees the review surfaces inside the Knowledge page, whether or not they are an admin.

Inside the Knowledge page, `apply_knowledge` holders get:

- **Review queue** (Insights tab) - All captured insights across users, with status/category/confidence filters and an insight detail drawer (full metadata, entity URNs, suggested actions, related columns, review notes, approve/reject actions).
- **Changesets** (Insights tab) - Catalog changes that resulted from approved knowledge: the target DataHub URN, change type, who applied it, and status, with rollback to revert applied changes.
- **All Memory** (Memory tab) - Every memory record across all users, filterable by lifecycle class (`sink_class`: Preference, Event, Business knowledge, Operational rule, Schema/entity), category, status, and source, with a detail drawer (full markdown content, entity URNs, metadata, stale reason, archive action).

## Assets (Admin)

The admin Assets page shows all platform assets across all users with search and filtering.

![Admin Assets](../images/screenshots/light/admin-admin-assets-light.webp#only-light)![Admin Assets](../images/screenshots/dark/admin-admin-assets-dark.webp#only-dark)

The table displays name, owner email, content type, file size, sharing status, and creation date. Click any asset to open the detail view:

![Admin Asset Detail](../images/screenshots/light/admin-admin-asset-detail-light.webp#only-light)![Admin Asset Detail](../images/screenshots/dark/admin-admin-asset-detail-dark.webp#only-dark)

The admin asset detail renders the asset content in a full-screen viewer with Preview/Source toggle, owner display, and management actions (Delete, Download, Share).

## Resources (Admin)

The admin Resources page shows managed resources across all personas and scopes.

![Admin Resources](../images/screenshots/light/admin-admin-resources-light.webp#only-light)![Admin Resources](../images/screenshots/dark/admin-admin-resources-dark.webp#only-dark)

Features:

- **Scope tabs** — All Resources, Global, and per-persona tabs (admin, data-engineer, finance-executive, etc.)
- **Search and filter** — Text search and category dropdown
- **Upload** button — Upload new resources scoped to any persona
- **Resource table** — Name, scope badge, category, MIME type, tags, file size, uploader email, and last updated date

## Prompts (Admin)

The admin Prompts page provides global prompt management across all scopes and personas.

![Admin Prompts](../images/screenshots/light/admin-admin-prompts-light.webp#only-light)![Admin Prompts](../images/screenshots/dark/admin-admin-prompts-dark.webp#only-dark)

Features:

- **Scope filter** — Dropdown to filter by Global, Persona, Personal, or System scope
- **Search** — Full-text search across name and description
- **New Prompt** — Create prompts with scope, persona assignment, tags, and enabled/disabled state
- **Sortable table** — Name, scope badge, description, owner, category, and actions
- **Scope badges** — Global (blue), Persona (purple), Personal (gray), System (amber)
- **Status badges** — Lifecycle state next to each name: draft (gray), approved (emerald), deprecated (amber), superseded (rose)
- **Lifecycle controls** — Editing a prompt exposes a status selector to move it through draft -> approved -> deprecated/superseded; approval stamps the acting admin. Selecting **superseded** reveals a field to record the replacement prompt name.
- **Tags** — Comma-separated labels set on create and edit, shown as chips in the expanded row
- **Promotion review queue** — A panel at the top of the page lists personal prompts whose owners have requested promotion, showing the owner, the requested scope (persona with the target personas, or global), and the description. **Approve** applies the requested scope/personas and marks the prompt approved; **Reject** clears the request and leaves it personal. If the promoted name already exists in the shared namespace, approval is blocked with a conflict so the owner renames first. The panel is hidden when no requests are pending.

## Connections

The Connections page manages toolkit backend instances (Trino, DataHub, S3, MCP gateway) using a split-pane layout.

![Connections](../images/screenshots/light/admin-admin-connections-light.webp#only-light)![Connections](../images/screenshots/dark/admin-admin-connections-dark.webp#only-dark)

**Left pane** — Connection list grouped by kind (DataHub, S3, Trino), with source badges (**file** or **database**), descriptions, and tool counts.

**Right pane** — Selected connection detail showing:

- **Metadata** — Kind, created by, and last updated
- **Configuration** — Key-value pairs with "Show sensitive" toggle for passwords and tokens
- **Actions** — Edit and Delete buttons

**Source tracking:**

| Badge | Meaning |
|-------|---------|
| **file** | Defined in the YAML config file. Read-only in the admin UI. |
| **database** | Created via the admin UI. Fully editable. |
| **both** | Defined in config with a database override. Database version is active. |

- **File connections** are read-only. Editing creates a database override (source becomes "both").
- **Deleting a "both" connection** removes the override and reverts to the file version.
- **+ Add Connection** at the bottom creates database-only connections.

### MCP Gateway Connections

Connections of kind **mcp** proxy upstream MCP servers and re-expose
their tools as `<connection_name>__<remote_tool>` (e.g.
`vendor__list_contacts`). They share the same split-pane layout as other
connections; the right pane adds a row of gateway-specific actions
beneath the metadata block:

| Action | What it does |
|--------|--------------|
| **Test connection** | Dials the upstream with the current form values (without saving) and reports whether tool discovery succeeded. Use to validate credentials before persisting. |
| **Refresh tools** | Re-dials a saved connection and re-registers its tool catalog on the live MCP server. Use after the upstream changes its tools. |
| **Enrichment rules** | Opens a side drawer for the cross-enrichment rule editor (see below). |

#### Add MCP Connection

The **+ Add Connection** form for kind `mcp` exposes:

- **Endpoint** — URL of the upstream MCP server (streamable HTTP).
- **Connection name** — local prefix for the proxied tools.
- **Auth mode** — `None` / `Bearer token` / `API key` / `OAuth 2.1`.
- **Credential** (bearer/api_key) — encrypted at rest with `ENCRYPTION_KEY`.
- **OAuth fields** (when `auth_mode=OAuth 2.1`):
   - **Grant type** — `client_credentials` (machine-to-machine) or `authorization_code + PKCE (browser sign-in)` for upstreams like Salesforce Hosted MCP that require human sign-in.
   - **Authorization URL** — appears only for `authorization_code`; e.g. `https://login.salesforce.com/services/oauth2/authorize`.
   - **Token URL** — OAuth token endpoint.
   - **Client ID / Client Secret** — from the upstream's OAuth app registration.
   - **Scope** — for `authorization_code`, include `refresh_token` so cron jobs and scheduled prompts survive access-token expiry.
- **Connect timeout** / **Call timeout** — bounds the dial + tool-call durations.

After saving an `authorization_code` connection, the right pane shows an
amber **Not connected** banner with a **Connect** button.

#### OAuth Connect Button

For `authorization_code` connections:

1. Click **Connect** on the connection card.
2. A new tab opens to the upstream's `/authorize` URL with PKCE state
   and `redirect_uri=<platform-host>/api/v1/admin/oauth/callback`.
3. Operator authenticates with the upstream provider.
4. The upstream redirects back to the platform's callback. The platform
   exchanges the code for tokens and stores them encrypted at rest in
   `gateway_oauth_tokens` (AES-256-GCM via `ENCRYPTION_KEY`).
5. The card now shows **Authorized by `<email>` `<time ago>`** and the
   tool list populates.

The platform refreshes the access token automatically using the stored
refresh token, so cron jobs and scheduled prompts run untouched until
the upstream invalidates the refresh token. Click **Reconnect** to
re-authorize manually if needed; click **Refresh now** to force an
immediate refresh.

#### Cross-Enrichment Rules Drawer

Clicking **Enrichment rules** on a saved gateway connection opens a
slide-out drawer for managing rules that join proxied tool responses
with native warehouse / catalog context:

- **Rule list** — one row per rule, with toggle for enable/disable,
  edit, delete, and **Dry-run** preview.
- **New rule** — opens the rule editor with three structured sections:
   - **Tool name** (autocomplete from this connection's discovered tools).
   - **When predicate** — `always` or `response_contains` with JSONPath.
   - **Enrich action** — source (`trino` or `datahub`), operation, and
     parameters with JSONPath bindings (`$.args`, `$.response`, `$.user`).
   - **Merge strategy** — where the enrichment lands in the response
     (`enrichment` by default; configurable path).
- **Dry-run** — paste a sample tool call, get the merged response back
  without executing any side effects.

Rule failures attach a `warning:` text content to the response and never
fail the parent tool call. See [Gateway Toolkit](gateway.md#cross-enrichment-rules)
for the full rule schema.

## Personas

The Personas page manages role-based tool access rules and context overrides using the same split-pane layout as Connections.

![Personas](../images/screenshots/light/admin-admin-personas-light.webp#only-light)![Personas](../images/screenshots/dark/admin-admin-personas-dark.webp#only-dark)

**Left pane** — Persona list with display name, slug, role count, and resolved tool count.

**Right pane** — Selected persona detail showing:

- **Metadata** — Priority, resolved tools count, and assigned roles
- **Tool Access Rules** — Allow patterns (green badges, e.g., `trino_*`, `datahub_*`) and deny patterns (red badges, e.g., `memory_capture`)
- **Resolved Tools** — Expandable list of the actual tools this persona can access
- **Context Overrides** — Description prefix and agent instructions suffix that customize AI behavior for this persona

See [Personas](../personas/overview.md) for configuration details.

## API Keys

The Keys page manages API keys for programmatic authentication.

![API Keys](../images/screenshots/light/admin-admin-keys-light.webp#only-light)![API Keys](../images/screenshots/dark/admin-admin-keys-dark.webp#only-dark)

Features:

- **Key table** — Name, source badge (file/database), email, description, roles badge, expiration date, and actions
- **Expired keys** — Shown with dimmed text and "Expired" badge
- **+ Add Key** — Create keys with name, email, description, roles, and expiration preset (Never, 24h, 7d, 30d, 90d, 1yr). The plaintext key is shown only once at creation.
- **Delete** — Available for database-managed keys only; file keys are read-only
- **Source badges** — Same file/database/both system as Connections

## Users

The Users page manages the known-users directory: a record of people (first
name, last name, email) used to make sharing easier. It is not an
authorization layer and grants no access; it only gives the share picker names
to resolve and suggest.

Features:

- **User table** — Name, email, status badge, and last-seen date
- **Status badge** — **Active** (green) for someone seen via a real sign-in, or **Invited** (amber) for someone an admin pre-added who has not logged in yet
- **+ Add User** — Pre-add a person by email (with optional first and last name) so they are selectable for sharing before they have ever signed in
- **Edit** — Change a person's first and last name. Admin-entered names take precedence: a later sign-in only fills blank name fields, it never overwrites a name an admin set
- **Search** — Filter the directory by name or email
- **Auto-recording** — Anyone who authenticates (OIDC/OAuth) is upserted into the directory automatically with the name from their token claims; API-key and anonymous sessions are not recorded

Requires a database. Without one the directory is disabled and the share
dialog falls back to free-typed email only.

## Change Log

The Change Log page provides an audit trail of all configuration changes made via the admin UI.

![Change Log](../images/screenshots/light/admin-admin-changelog-light.webp#only-light)![Change Log](../images/screenshots/dark/admin-admin-changelog-dark.webp#only-dark)

Each entry shows:

- **Config key** — The configuration path that changed (e.g., `server.description`, `server.agent_instructions`)
- **Action** — Set (red badge) indicating a value was written
- **Timestamp** — When the change was made

## Local Development

Run the portal locally with demo data using [Mock Service Worker](https://mswjs.io/):

```bash
cd ui
npm install
VITE_MSW=true npm run dev
```

Open `http://localhost:5173/portal/` — no backend required. The mock data includes realistic ACME Corporation demo content with 200+ audit events, 50 knowledge insights, 6 personas, and 12 users.

For full-stack development with a real backend:

```bash
make dev-up                                        # Start PostgreSQL
go run ./cmd/mcp-data-platform --config dev/platform.yaml  # Start server
psql -h localhost -U platform -d mcp_platform -f dev/seed.sql  # Seed demo data
cd ui && npm run dev                               # Start React dev server
```

See [`dev/README.md`](https://github.com/txn2/mcp-data-platform/blob/main/dev/README.md) for complete local development instructions.

### Generating Screenshots

Automated screenshot generation captures every portal page in light and dark modes:

```bash
cd ui
npm run screenshots              # Generate PNG screenshots
npm run screenshots:convert      # Convert to optimized WebP
```

Screenshots are saved to `docs/images/screenshots/light/` and `docs/images/screenshots/dark/`. See `ui/e2e/screenshots/README.md` for configuration options including custom branding.

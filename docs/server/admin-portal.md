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

## Tools

### Overview

The Tools Overview tab shows all configured connections and a complete tool inventory.

![Tools Overview](../images/screenshots/light/admin-admin-tools-overview-light.webp#only-light)![Tools Overview](../images/screenshots/dark/admin-admin-tools-overview-dark.webp#only-dark)

Each connection card displays the toolkit type (Trino, DataHub, S3), connection name, and the tools it provides. The Tool Inventory table below lists every registered tool with its description pulled from the MCP schema, visibility status, kind, and toolkit assignment.

### Explore

The Explore tab provides an interactive tool execution environment for testing and debugging.

![Tools Explore](../images/screenshots/light/admin-admin-tools-explore-light.webp#only-light)![Tools Explore](../images/screenshots/dark/admin-admin-tools-explore-dark.webp#only-dark)

Features:

- **Tool browser** — Tools grouped by connection (Trino, DataHub, S3) with search filtering
- **Dynamic parameter form** — Auto-generated from each tool's JSON schema with type-appropriate inputs (text areas for SQL, number fields for limits, dropdowns for enums)
- **Result display** — Rendered markdown tables for structured data, with a Raw toggle for JSON output
- **Semantic context** — Cross-injection enrichment shown below results: dataset descriptions, owners, tags, column metadata, glossary terms, and lineage sources
- **Execution history** — Timestamped log of tool calls with duration, status, and replay capability

## Audit Log

### Overview

The Audit Overview tab provides platform-wide analytics across configurable time ranges.

![Audit Overview](../images/screenshots/light/admin-admin-audit-overview-light.webp#only-light)![Audit Overview](../images/screenshots/dark/admin-admin-audit-overview-dark.webp#only-dark)

Includes the same summary cards, activity timeline, top tools, and top users charts as the Dashboard — focused specifically on audit data with additional performance percentiles and error tracking.

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

## Knowledge & Memory

### Overview

The Knowledge & Memory Overview provides insight and memory statistics with distribution charts.

![Knowledge Overview](../images/screenshots/light/admin-admin-knowledge-overview-light.webp#only-light)![Knowledge Overview](../images/screenshots/dark/admin-admin-knowledge-overview-dark.webp#only-dark)

The overview includes:

- **Knowledge Capture cards** — Total insights, pending review, approved, applied, rejected, and approval rate
- **Insight Status Distribution** — Donut chart showing insight lifecycle states (pending, approved, applied, rejected, rolled back, superseded)
- **Memory cards** — Total memories, active, stale, and dimensions count
- **Memory Status Distribution** — Donut chart of memory states
- **Memory by Dimension** — Distribution across LOCOMO dimensions (knowledge, event, entity, relationship, preference)

### Knowledge Capture

The Knowledge Capture tab lists all captured insights with filtering and admin review actions.

![Knowledge Capture](../images/screenshots/light/admin-admin-knowledge-knowledge-light.webp#only-light)![Knowledge Capture](../images/screenshots/dark/admin-admin-knowledge-knowledge-dark.webp#only-dark)

Features:

- **Summary cards** — Pending review, total insights, top category, and applied count
- **Filters** — Status, category, and confidence dropdowns
- **Sortable table** — Created at, captured by, category, confidence, insight text, and status
- **Status badges** — Color-coded: pending (amber), approved (green), applied (green), rejected (red), rolled back (red), superseded (gray)
- **Insight detail drawer** — Click any row to see full metadata, entity URNs, suggested actions, related columns, review notes, and approve/reject buttons

### All Memory

The All Memory tab shows every memory record across all users and sessions.

![All Memory](../images/screenshots/light/admin-admin-knowledge-memory-light.webp#only-light)![All Memory](../images/screenshots/dark/admin-admin-knowledge-memory-dark.webp#only-dark)

Features:

- **Summary cards** — Total, active, stale, and archived counts
- **Filters** — Dimension, category, status, and source dropdowns
- **Sortable table** — Created, user, persona, dimension, category, content preview, status, and confidence
- **Memory detail drawer** — Full content (rendered as markdown), entity URNs, metadata, stale reason, and archive action

### Changesets

The Changesets tab tracks catalog changes that resulted from approved knowledge.

![Changesets](../images/screenshots/light/admin-admin-knowledge-changesets-light.webp#only-light)![Changesets](../images/screenshots/dark/admin-admin-knowledge-changesets-dark.webp#only-dark)

Each changeset records what was changed, the target DataHub URN, the change type (e.g., Update Column Description), who applied it, and its status. Changesets support rollback to revert applied catalog changes.

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
- **New Prompt** — Create prompts with scope, persona assignment, and enabled/disabled state
- **Sortable table** — Name, scope badge, description, owner, category, and actions
- **Scope badges** — Global (blue), Persona (purple), Personal (gray), System (amber)

## Connections

The Connections page manages toolkit backend instances (Trino, DataHub, S3) using a split-pane layout.

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

## Personas

The Personas page manages role-based tool access rules and context overrides using the same split-pane layout as Connections.

![Personas](../images/screenshots/light/admin-admin-personas-light.webp#only-light)![Personas](../images/screenshots/dark/admin-admin-personas-dark.webp#only-dark)

**Left pane** — Persona list with display name, slug, role count, and resolved tool count.

**Right pane** — Selected persona detail showing:

- **Metadata** — Priority, resolved tools count, and assigned roles
- **Tool Access Rules** — Allow patterns (green badges, e.g., `trino_*`, `datahub_*`) and deny patterns (red badges, e.g., `capture_insight`)
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

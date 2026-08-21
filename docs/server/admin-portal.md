---
description: Admin Portal web dashboard for monitoring, auditing, tool exploration, knowledge governance, and platform configuration. Visual guide with screenshots.
---

# Admin Portal

The Admin Portal is an interactive web dashboard for managing and monitoring the platform. Enable it with `portal.enabled: true` in your configuration.

```yaml
portal:
  enabled: true
  brand_name: "ACME"
  brand_url: "https://acme.example.com"
  logo: https://example.com/logo.svg
  logo_light: https://example.com/logo-for-light-bg.svg
  logo_dark: https://example.com/logo-for-dark-bg.svg

admin:
  enabled: true
  persona: admin
```

The portal is served at `/portal/`. Authentication is required — use the same credentials as the [Admin API](admin-api.md). The sidebar is divided into **User** pages (see [User Portal](portal-user.md)) and **Admin** pages (described here).

### Branding

Name the deployment once with `portal.brand_name` and the portal follows it everywhere: the sidebar and browser tab read **`<brand_name>` Portal** ("ACME" gives "ACME Portal"), and the same brand names the public viewer header, the branded denial pages, and the built-in MCP Apps. A brand that already ends in "Portal" is used as-is rather than doubled. `portal.title` overrides the composed title outright, for a deployment whose portal is called something other than its brand.

`portal.brand_name` and `portal.brand_url` fall back to `brand_name` / `brand_url` in the `mcpapps.apps.platform-info.config` block, so a deployment that branded its MCP App keeps that brand without repeating it — and it keeps its current title, because only a brand named in the `portal` block composes the title. The `portal` block wins when both are set, and is the one to use: it needs no MCP Apps configuration at all, and its brand is written into the built-in apps that name none of their own. An `mcpapps` block the operator has disabled contributes nothing.

With `portal.brand_url` set, the sidebar brand mark — logo and name together — becomes a link to the brand's own site, opening in a new tab so a reader mid-task does not lose the portal to it. Unset, the mark is inert markup rather than a link to nowhere.

Customize the logo via `portal.logo`, `portal.logo_light`, and `portal.logo_dark`. The portal picks the theme-appropriate logo automatically:

- **Light theme**: `logo_light` → `logo` → built-in default
- **Dark theme**: `logo_dark` → `logo` → built-in default

The resolved logo is also used as the browser favicon. A built-in activity icon is used when no logo is configured. Logos should be square SVGs for best results.

#### Version link

The portal header shows the running server version. Point `portal.version_url` at release notes, a changelog, or an internal wiki page and the version becomes a link there:

```yaml
portal:
  version_url: "https://acme.example.com/changelog"
```

Unset, the version stays plain text — no reader is offered a link the operator never pointed anywhere.

#### Email logo

Notification emails need a separate asset. Mail clients strip inline SVG, so `portal.logo` cannot be reused; set `portal.logo_email` to a raster **PNG** URL:

```yaml
portal:
  logo_email: https://example.com/logo-email.png   # PNG only, max 1 MB
```

The PNG is fetched once at startup and attached to each message as an inline part, so it renders even in the clients that block remote images by default. Recipients never request the URL themselves, which means it only has to be reachable from the server, not from the public internet.

The logo is additive: the brand wordmark still renders beneath it, and doubles as the image's `alt` text. Leave `logo_email` unset and emails render the wordmark alone. A URL that is unreachable or does not serve `image/png` logs a warning at startup and falls back to the wordmark; it never blocks notification delivery.

### Public Viewer Branding

Shared asset links (the public viewer at `/portal/view/{token}`) display a two-zone header. The **right zone** shows the platform brand (`portal.brand_name` and `portal.logo`, linked to `portal.brand_url`). The **left zone** is an optional implementor brand for the organization deploying the platform:

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
- **Expiration notice** — When the share has an expiration, a notice bar shows the relative time remaining (e.g., "This page expires in 6 hours"). Only a public link is created with one, so the notice is what an older share created under the previous rule may still show as well. Hidden when the share has no expiry, or when its creator set `hide_expiration`.
- **Notice text** — Configurable per-share via `notice_text`. Defaults to "Proprietary & Confidential. Only share with authorized viewers." Set to `""` to hide the notice entirely.

These fields are set per-share when creating a share via `POST /api/v1/portal/assets/{id}/shares`:

```json
{"access_mode": "public", "expires_in": "24h", "hide_expiration": true, "notice_text": "Internal use only."}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `expires_in` | string | - | Positive duration string (e.g., `"24h"`, `"72h"`). Required for `access_mode: public` and rejected for every other share: a public link is a bearer credential and must expire, while a share that resolves against who the viewer is ends on revocation. |
| `shared_with_user_id` | string | - | Target user ID for private shares |
| `shared_with_email` | string | - | Target email for private shares |
| `hide_expiration` | bool | `false` | Hide the expiration countdown in the public viewer |
| `notice_text` | string\|null | `"Proprietary & Confidential. Only share with authorized viewers."` | Custom notice text for the public viewer. Omit or `null` for the default. Set to `""` to hide the notice entirely. Max 500 characters. |
| `access_mode` | string | `restricted` with a recipient, `authenticated` without | Who the token opens for: `restricted` (named recipient and the creator), `authenticated` (any signed-in user), or `public` (anyone with the link). `public` is never implied; `restricted` without a recipient is rejected with 400. |

Anonymous access is opt-in. Every route under `/portal/view/` (the page, the
raw content, both thumbnail routes, and the three collection-item routes)
resolves the share's access mode before serving anything, and refuses with 403
when the caller is not admitted.

A refused browser navigation renders a branded landing page instead of bare
text: sign-in with a return path for account holders, and, on shares naming an
email address, a request button for a single-use, 15-minute view link emailed
only to that stored address. A claimed link opens a view-only guest session
scoped to that one share (a signed cookie derived from the browser-session
signing key); guests never reach the portal or its API, and revoking the share
ends their access immediately. Link issuance is rate-limited per IP and capped
per share, and requires the notification substrate (SMTP), a database, browser
sessions, and `portal.public_base_url`. Subresource fetches and API-style
callers keep plain-status refusals.

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

The **Indexing** tab of the Dashboard (alongside MCP, API Gateway, Health, and Events) is an admin-only, cross-kind view of embedding-index health for every consumer of the shared `index_jobs` queue (`pkg/indexjobs`): api-catalog operation vectors, tool descriptors, and any future consumer, which gets visibility here for free the moment it registers. Embedding work runs off the request path, so a provider outage, a model dimension mismatch, or repeated retries can silently degrade `ranking=semantic`/`hybrid` to lexical with only a log line as signal; this tab is the single place to answer whether indexing is healthy, what is covered, what failed, and why. A row write enqueues its own job, so new content normally appears here as a `write` job that completes within seconds of the save.

It is system-wide and admin-only by platform convention (operators see all indexing; it is not a per-persona capability). All data is real `index_jobs` and vector-table state — no mocked dimensions. The page polls every 5 seconds so it reflects work as the worker, reconciler, and reaper complete it.

![Indexing](../images/screenshots/light/admin-admin-audit-indexing-light.webp#only-light)![Indexing](../images/screenshots/dark/admin-admin-audit-indexing-dark.webp#only-dark)

The tab includes:

- **Provider health banner** — The embedding provider's kind, model, and dimension, or a clear degraded state (noop / unconfigured) since a bad provider makes the whole index meaningless and pauses indexing.
- **Per-kind health cards (summary-first)** — Each kind leads with one plain health **verdict** computed server-side: **Up to date** (the single resting state for a fully-indexed, quiescent, failure-free kind), **Indexing…** (work in flight), or **Degraded** (an open failure or a coverage shortfall). Equivalent states look identical: every up-to-date kind shows the same green badge and the same `N / M · 100%` coverage bar (api-catalog's expected comes from `operation_count`; tools writes its complete registered set atomically, so its indexed count is also its expected), and a recency line ("last indexed <relative>", or "fully indexed" for a kind seeded outside the queue, never "never"). The per-unit job-state breakdown ("units by last run") is shown only when there is active work or an open failure, so an up-to-date card is not cluttered with an all-zero or stale stat row. A **Re-index** button re-enqueues every out-of-sync unit of the kind.
- **Throughput timeline** — Completed jobs over time (d3 area), so an operator can see indexing keeping up or stalling.
- **Embed latency** — Per-kind started-to-completed duration (p50 with a p95 marker), surfacing slow passes such as the CPU-only embedder case.
- **In flight** — Running jobs with worker id, lease countdown, and items-done progress for long passes.
- **Retry backoff** — Pending jobs that already failed once, with attempt count and next run time.
- **Failure triage (self-resolving)** — Units with open failures, grouped by error signature. Each unit shows first-seen / last-seen timestamps, occurrence and attempt counts, and "last succeeded" context, with an expandable drill-in to the un-redacted error and the underlying job id. A failure auto-resolves (leaves the panel) once a later job for the same unit succeeds; **Retry** re-enqueues the unit and the card clears when it next succeeds, and **Dismiss** is the explicit fallback that resolves a failure (such as a removed consumer's leftover rows) that no future success will supersede. A unit that has failed repeatedly without ever succeeding also shows when its automatic retries resume ("retries paused, resuming in 4h"): the periodic sweep backs off rather than re-queueing a deterministic failure every five minutes, and saying so keeps a deferred unit from reading as one still being hammered. **Retry** ignores the deferral.
- **Jobs drill-down** — A filterable table (by kind and status) of recent jobs with trigger, attempts, last update, and error. The trigger says what produced the job: `write` for a row mutation's own enqueue (a saved asset, an edited knowledge page, an approved prompt, an uploaded resource), `reconciler` for a timer-driven gap sweep, `manual_retry` for the operator escape hatch. Routine timer-driven reconciler successes for a unit (which every replica re-runs on its own schedule) are collapsed into a single "synced ×N" row so they do not drown the table.

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

Shown for gateway-proxied (MCP) tools with a connection. Lists the enrichment rules attached to the tool — each rule's predicate, action source and operation, merge strategy, and enabled state. This is where the platform's bidirectional cross-enrichment is configured per tool.

### Visibility

![Tools Visibility](../images/screenshots/light/admin-admin-tools-visibility-light.webp#only-light)![Tools Visibility](../images/screenshots/dark/admin-admin-tools-visibility-dark.webp#only-dark)

Toggle the tool's membership in the platform-wide deny list, and preview whether a given persona can access it before committing the change.

## Activity (Dashboard tabs)

The admin Dashboard hosts the platform activity views as tabs: **MCP**, **API Gateway**, **Health**, **Indexing**, **Events**, and **Notifications**. (Indexing is documented above.) The first four work across configurable time ranges (1h, 6h, 24h, 7d).

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

- **Filters** — User, tool, status (success/failure), and time range dropdowns, plus a **Session ID** box that narrows the table to one session's calls
- **Sortable columns** — Timestamp, user, tool, toolkit, connection, duration, status, and enrichment
- **Purpose** — The one sentence the agent stated about why the call was made, truncated to fit and shown in full on hover and in the drawer. A dash means none was stated: the tool is outside the [gated set](configuration.md#purpose-configuration), or the caller (an MCP App, a managed script, the REST shim, a portal run) cannot thread arguments at all. The column does not sort — alphabetical order over free prose means nothing — but the search box matches it, so an operator can find every call made for a given task.
- **Export** — Export CSV and Export JSON buttons
- **Event detail drawer** — Click any row to open the full detail:
    - **Identity** — User email, persona, session ID (a link to the [session](#sessions))
    - **Execution** — Tool name, toolkit, connection, duration
    - **Status** — Success/failure, enrichment status
    - **Transport** — HTTP or stdio, request/response sizes, content block count
    - **Purpose** — The full stated purpose, above the parameters; omitted when none was stated
    - **Parameters** — Full request parameters as JSON

![Event detail](../images/screenshots/light/admin-admin-audit-event-detail-light.webp#only-light)![Event detail](../images/screenshots/dark/admin-admin-audit-event-detail-dark.webp#only-dark)

### Notifications

The Notifications tab is the admin read on email delivery: whether the platform's notification emails are reaching people, and what happened to the ones that did not. Without it, a broken SMTP relay is invisible until a user reports never receiving a share.

![Notification delivery](../images/screenshots/light/admin-admin-audit-notifications-light.webp#only-light)![Notification delivery](../images/screenshots/dark/admin-admin-audit-notifications-dark.webp#only-dark)

Counts by status sit above the list — Failed, Pending, Sending, Sent — as an at-a-glance health read, and each count doubles as a filter. The list shows every queue row with when it was raised, its recipient, the subject line the email carried, its category, delivery status, and attempt count. Clicking a row opens the detail, which exists for the failure case: the error the mail server returned, verbatim, alongside how many attempts the queue made before giving up.

Filters narrow by recipient, status, and category. The tab is admin-only and shows every recipient's rows; each user sees their own activity in **Settings > Recent notifications** in the user portal.

It shows recent history rather than an archive: the send worker purges resolved rows on a retention schedule (30 days by default, undelivered rows after 7), and the effective window is stated above the list so an empty view is not mistaken for a quiet week. See [Email Notifications](notifications.md) for the delivery semantics behind the statuses.

## Sessions

The Sessions page is the platform's work, grouped by who was doing it. The Events tab answers "what happened on the platform"; this answers "what was this person working on".

![Sessions](../images/screenshots/light/admin-admin-sessions-light.webp#only-light)![Sessions](../images/screenshots/dark/admin-admin-sessions-dark.webp#only-dark)

A session is read back from the audit log rather than stored, so the list outlives the session record itself and reaches as far back as audit retention. Each row carries when the session was last active, its id and kind (**Agent**, **Portal run**, **Script run**, **Transport** — see [the kinds](audit.md#sessions-read-back-from-the-log)), the caller and the persona they worked as, how many calls it made, how many failed, and what it produced. Filters narrow by user, kind, sessions with failures, and sessions that saved assets.

The first facet is the time window, and it is a control rather than a hidden default: the list rolls up every event in range, so it opens on the last 7 days and offers 24 hours, 30 days, and all time. Widening it is the reader's choice, and nothing is withheld without saying so.

Clicking a row opens the session.

![Session detail](../images/screenshots/light/admin-admin-session-detail-light.webp#only-light)![Session detail](../images/screenshots/dark/admin-admin-session-detail-dark.webp#only-dark)

The detail opens on the identity line — who ran it, as what, and when it started — then five figures: calls, failures, the wall-clock span from first call to last, and the assets and insights it produced. Below them:

- **Assets** — What the session saved, opening in the admin asset viewer.
- **Insights** — What it captured, each with the review status it is sitting at.
- **Timeline** — Every call in the order it was made, showing the [purpose](audit.md#why-a-call-happened) the agent stated, the tool, the connection, the outcome, and the duration. Clicking a row opens the same event drawer the Events tab opens, so a call reached from a session reads exactly as it does from the log.

## Calls

The Calls page is every data-access call the platform recorded, across every caller: each query against a query engine and each invocation through the API gateway, with the reason its caller stated and what came of the result. Where Sessions answers "what was this person working on", this answers "which queries actually earned their keep".

![Calls](../images/screenshots/light/admin-admin-calls-light.webp#only-light)![Calls](../images/screenshots/dark/admin-admin-calls-dark.webp#only-dark)

A record is derived from the audit log and kept in its own table, because the two are kept for different reasons: audit retention is the deployment's history window, while a query worth reusing is worth keeping as long as it is worth running. What is not stored is the outcome. `satisfied`, `failed`, `superseded` and `ran` are computed on every read from the call's own result, from whatever later **named** it, and from what the same session read afterwards, so an outcome can never disagree with the asset or the capture that gives it meaning. The four are defined in [the user portal's My Calls](portal-user.md#my-calls).

Filters narrow by user, kind (SQL or API), connection, outcome, and free text over the purpose and the statement. **Awaiting review** is the review queue: the records that answered something and carry no decision yet, ordered by reuse first, because a query a stranger re-ran is better evidence than one its own author vouched for.

Clicking a row opens the record.

![Call detail](../images/screenshots/light/admin-admin-call-detail-light.webp#only-light)![Call detail](../images/screenshots/dark/admin-admin-call-detail-dark.webp#only-dark)

The detail opens on the outcome, the reuse count, the duration and the response size, then the statement or request line, the datasets it addressed, what was built from it, and the session that ran it (opening the session detail above). The reference an agent cites the call by (`mcp:call:<id>`) is on the record, which is also what `fetch` dereferences.

A satisfied record can be **published**:

- A **SQL** record becomes a Query entity in the data catalog, associated with every dataset the statement reads rather than one of them, through the same DataHub write path `apply_knowledge` uses. The name comes from the stated purpose; the description carries the purpose, the session it came from, and how many later sessions re-ran it.
- An **API** record becomes a saved example on its endpoint, keyed by connection, which the next agent sees when it reads that endpoint's schema.

A record that is not worth publishing is **declined** with a note, and stops being offered. A deployment with no DataHub connection refuses a SQL promotion rather than reporting one that persisted nothing.

The catalog is written from the audit pipeline, so it exists exactly where audit does: a deployment with no database, or with `audit.enabled: false`, records no calls and serves no call pages.

Records are swept by what they came to rather than by age alone. A record that answered something, was promoted, was declined, or was re-run by another session is kept for as long as the deployment runs; a query that ran and came to nothing ages out after `calls.retention_days` (90 by default). See [Call Catalog Configuration](configuration.md#call-catalog-configuration).

## Knowledge & Memory (review and promotion)

The separate admin Knowledge & Memory page was merged into the unified **Knowledge** page in the user portal (see [Portal User Guide](portal-user.md#knowledge)). Review and promotion gate on the `apply_knowledge` capability, not an admin role: whoever holds the tool sees the review surfaces inside the Knowledge page, whether or not they are an admin.

Inside the Knowledge page, `apply_knowledge` holders get:

- **Review queue** (Insights tab) - All captured insights across users, with status/category/confidence filters and an insight detail drawer (full metadata, entity URNs, suggested actions, related columns, review notes, approve/reject actions). A pending-review count is badged on the sidebar Knowledge item and the Insights tab.
- **Changesets** (Knowledge tab) - The record of insights promoted into knowledge: the target DataHub URN or knowledge page, change type, who applied it, and status, with rollback to revert applied changes. They sit with the promoted knowledge rather than with the unpromoted insights in the review pipeline.

Opening an insight from the review queue shows the full review drawer: the captured statement, entity URNs, suggested catalog actions, related columns, the capture/review/apply audit trail, and approve/reject controls.

![Insight review](../images/screenshots/light/admin-knowledge-insight-detail-light.webp#only-light)![Insight review](../images/screenshots/dark/admin-knowledge-insight-detail-dark.webp#only-dark)

### Observed warehouse state

A claim about an entity the platform can query is one the platform can check for itself. When a pending insight's entity URN resolves through the configured query provider to an available table, the drawer puts that observation under the claim: what the entity is queryable as, which connection it lives on, and how many rows it currently holds.

![Insight review with observed warehouse state](../images/screenshots/light/admin-knowledge-insight-observed-light.webp#only-light)![Insight review with observed warehouse state](../images/screenshots/dark/admin-knowledge-insight-observed-dark.webp#only-dark)

When the claim states a number and the table estimates a different one, the drawer carries an advisory marker naming both. It is advisory only: an estimate is an estimate, the number in the claim may be about something else entirely, and approve and reject stay exactly as available as before. Nothing is ever refused mechanically.

![Insight review with a claim conflict](../images/screenshots/light/admin-knowledge-insight-conflict-light.webp#only-light)![Insight review with a claim conflict](../images/screenshots/dark/admin-knowledge-insight-conflict-dark.webp#only-dark)

Row estimation is off by default, because `COUNT(*)` can scan a whole table (`enrichment.estimate_row_counts`, see [Configuration](configuration.md)). With it off, the block still states that the entity exists and is queryable, and claims no count on the platform's behalf.

![Insight review without a row estimate](../images/screenshots/light/admin-knowledge-insight-no-estimate-light.webp#only-light)![Insight review without a row estimate](../images/screenshots/dark/admin-knowledge-insight-no-estimate-dark.webp#only-dark)

The block is absent, rather than empty, whenever there is nothing to show: a decided insight, an entity URN the provider cannot resolve, a table it reports unavailable, a slow warehouse, and a deployment with no query provider all render the drawer exactly as it was.

The Memory tab is personal to each user; there is no all-user memory view, because the only memory that crosses between users is an insight (handled in the review queue above).

## Assets (Admin)

The admin Assets page shows all platform assets across all users with search and filtering.

![Admin Assets](../images/screenshots/light/admin-admin-assets-light.webp#only-light)![Admin Assets](../images/screenshots/dark/admin-admin-assets-dark.webp#only-dark)

The table displays name, owner email, content type, file size, sharing status, and creation date. Click any asset to open the detail view:

![Admin Asset Detail](../images/screenshots/light/admin-admin-asset-detail-light.webp#only-light)![Admin Asset Detail](../images/screenshots/dark/admin-admin-asset-detail-dark.webp#only-dark)

The admin asset detail renders the asset content in a full-screen viewer with Preview/Source toggle, owner display, and management actions (Delete, Download, Share).

Assets and Collections are two faces of one section, so the page opens on a tab strip that moves between them.

Every one of those actions works on an asset the admin does not own, Share included. This matters most for content produced by a non-human principal: an agent session authenticated with an API key owns its assets under an identity like `<key name>@apikey.local`, which nobody can sign in as. Without an admin share, such an asset could be read but never handed to the person who needs it. The same authority covers collections and personal prompts: an admin can share one, read its share list, and revoke a share on it. Shares an admin creates are attributed to the admin, so the audit trail names who actually granted the access.

## Collections (Admin)

The Collections face of the admin Assets page lists every asset collection on the platform, whoever owns it.

![Admin Collections](../images/screenshots/light/admin-admin-collections-light.webp#only-light)![Admin Collections](../images/screenshots/dark/admin-admin-collections-dark.webp#only-dark)

The table displays name, description, owner email, the tags of the assets inside, sharing status, and the date the collection was last touched. Search matches name, description, and owner email. Click any row to open the collection:

![Admin Collection Detail](../images/screenshots/light/admin-admin-collection-detail-light.webp#only-light)![Admin Collection Detail](../images/screenshots/dark/admin-admin-collection-detail-dark.webp#only-dark)

The detail view renders the collection's sections and the assets in each one, with the owner named beside the title. Its items open in the admin asset viewer, so reading a collection never depends on owning what is in it. Share hands the collection to the people who need it; Delete removes the collection and leaves its assets in place. Edit details corrects the two fields the collection carries of its own:

![Admin Collection Details Form](../images/screenshots/light/admin-admin-collection-edit-light.webp#only-light)![Admin Collection Details Form](../images/screenshots/dark/admin-admin-collection-edit-dark.webp#only-dark)

Sections stay with the owner's editor: this page changes the collection's own name and description, not its contents.

This is the collection half of the same authority the asset view carries, and it matters for the same reason. A collection an agent session created belongs to an identity nobody can sign in as, and every owner-scoped list is blind to it — the assets inside it stayed visible one tab over while the thing that grouped them could be seen by nobody at all.

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

The create/edit form is a markdown editor with auto-extracted `{argument}` placeholders, a scope selector (global, persona, personal), persona targeting, and lifecycle status. When an author requests promotion, a review-queue banner surfaces pending prompts for approval or rejection.

![New Prompt](../images/screenshots/light/admin-admin-prompt-create-light.webp#only-light)![New Prompt](../images/screenshots/dark/admin-admin-prompt-create-dark.webp#only-dark)

Features:

- **Scope filter** — Dropdown to filter by Global, Persona, Personal, or System scope
- **Search** — Full-text search across name and description
- **New Prompt** — Create prompts with scope, persona assignment, tags, and enabled/disabled state. A global or persona prompt created by an admin lands `approved` with the creating admin stamped as approver — the creator is the reviewer, so nothing sits in draft with no approve affordance in sight
- **Sortable table** — Name, scope badge, description, owner, category, and actions
- **Scope badges** — Global (blue), Persona (purple), Personal (gray), System (amber)
- **Status badges** — Lifecycle state next to each name: draft (gray), approved (emerald), deprecated (amber), superseded (rose)
- **Lifecycle controls** — Editing a prompt exposes a status selector to move it through draft -> approved -> deprecated/superseded; approval stamps the acting admin. Selecting **superseded** reveals a field to record the replacement prompt name.
- **Tags** — Comma-separated labels set on create and edit, shown as chips in the expanded row
- **Promotion review queue** — A panel at the top of the page lists personal prompts whose owners have requested promotion, showing the owner, the requested scope (persona with the target personas, or global), and the description. **Approve** applies the requested scope/personas and marks the prompt approved; **Reject** clears the request and leaves it personal. If the promoted name already exists in the shared namespace, approval is blocked with a conflict so the owner renames first. The panel is hidden when no requests are pending.

## Scripts (Admin)

The Scripts page is the operator's view of the platform's managed scripts:
every script that exists, and what has been running. A saved script runs — the
latest saved version is what `run_script` and a schedule execute, presenting
the roles its author held at the save — so this page lists and explains rather
than gating anything. See
[Managed Scripts: Security Model](../scripts/security.md).

![Admin Scripts](../images/screenshots/light/admin-admin-scripts-light.webp#only-light)![Admin Scripts](../images/screenshots/dark/admin-admin-scripts-dark.webp#only-dark)

**All scripts** lists every script with who owns it and what it is executing:
**Runs vN** for a script in service, or its lifecycle state (disabled,
deprecated, superseded) when nothing will execute it. A script is one person's,
so its owner is who sees it, runs it, and under whose authority a scheduled run
executes; a script showing **nobody** as its owner was authored by a principal
carrying no address and is visible only to administrators. Opening a row opens
the script.

### One script

The script page an administrator opens is the page its owner opens.

![One script](../images/screenshots/light/admin-admin-script-detail-light.webp#only-light)![One script](../images/screenshots/dark/admin-admin-script-detail-dark.webp#only-dark)

Everything an owner does is here for every script — run it now, edit the
source, validate and dry-run the edit, set or pause the cadence, read the
version history and the run history — plus the one thing only an administrator
does: **Owner**, which moves the script to another person.

![Transferring a script](../images/screenshots/light/admin-admin-script-owner-light.webp#only-light)![Transferring a script](../images/screenshots/dark/admin-admin-script-owner-dark.webp#only-dark)

A transfer hands over everything at once, since ownership is the whole of what a
script is: what its owner sees, edits, runs, and schedules. It is recorded as a
new version authored by the administrator making it, and from then on a run
presents THAT administrator's roles — which is how a script comes to run with an
administrator's reach, and the reason to move one to an administrator in the
first place. The move is refused when the receiving owner already keeps a script
of the same name, and it is recorded in the audit log as a `script_transfer_owner`
event naming both ends of the move. It is also how an ownerless script gets an
owner. The version history shows each version's
author and the roles they held, which are the roles a run of that version
presents. A version's detail states whether its exact source has been dry-run,
by whom, and how it ended — the account is matched by the source itself, so it
describes the code in front of the reader and no other version.

There is deliberately one script page rather than two. Two would have meant the
administrator's and the owner's answers to "what can I do with this script"
drifting apart, one feature at a time.

### Runs

The other question an operator has is what has been running. The **Runs** tab
answers it across every script.

![Script runs](../images/screenshots/light/admin-admin-script-runs-light.webp#only-light)![Script runs](../images/screenshots/dark/admin-admin-script-runs-dark.webp#only-dark)

The panels read the metrics the run worker and the scheduler emit
(`script_runs_total`, `script_run_duration_seconds`, `script_runs_running`, and
`script_missed_fires_total` — see [Observability](observability.md)): how many
runs finished in the window and how many failed, the slowest five percent, how
many are executing right now across every replica, the succeeded-against-
everything-else split over time, the busiest scripts, and the automations that
are missing fires. A missed fire is the one thing the run table cannot show,
because it is precisely a run that does not exist.

The table beneath them is the exact recent history from the platform's own
records: which script, what triggered the run, how it ended and why when it
failed, how long it took, and what it produced. It shows the 50 most recent
runs — the store's own ceiling — and says so when it fills, because older runs
are kept for as long as the retention window allows and the charts above cover
that whole window whatever the table holds. The two sources are deliberate —
the metrics survive run retention and aggregate across replicas, and the rows
carry the reason a particular run failed. A deployment with no metrics backend
configured says so and still shows the history.

## Agent Instructions

The Agent Instructions page edits the operating guidance every agent session receives. The editor is a split markdown view (source on the left, live preview on the right); a **Database override** badge appears when the value is stored in the database rather than the config file.

![Agent Instructions](../images/screenshots/light/admin-admin-agent-instructions-light.webp#only-light)![Agent Instructions](../images/screenshots/dark/admin-admin-agent-instructions-dark.webp#only-dark)

Above the editor sits the read-only **Platform baseline**: the platform-owned "how to operate" guidance composed beneath your instructions. It names only the tools this deployment exposes (search, query, save, capture), so you can see what is already covered and add only your business and deployment context on top.

## Description

The Description page sets the platform's identity string, surfaced to MCP clients (for example in `platform_info`). Same split markdown editor and database-override semantics as Agent Instructions.

![Description](../images/screenshots/light/admin-admin-description-light.webp#only-light)![Description](../images/screenshots/dark/admin-admin-description-dark.webp#only-dark)

## API Catalogs

API Catalogs are versioned, globally-owned bundles of OpenAPI 3.x specs that `kind: api` connections share. One catalog can back many connections, so a single upload (for example a Salesforce or Stripe spec) documents every connection that points at that vendor.

![API Catalogs](../images/screenshots/light/admin-admin-api-catalogs-light.webp#only-light)![API Catalogs](../images/screenshots/dark/admin-admin-api-catalogs-dark.webp#only-dark)

**Left pane**: Catalogs grouped by name, each showing its component-spec count and how many connections reference it.

**Right pane**: The selected catalog's component specs, each with an embedding-health badge (`78/78 indexed`, or a live `running` count while a spec re-embeds), source badge (URL / upload / inline), and last-fetched timestamp. A banner summarizes catalog-wide readiness ("all specs indexed; semantic ranking is active"). Per-spec actions cover refresh-from-URL, retry-embedding, edit, and delete; catalog actions are Edit, Clone, and Delete (blocked while any connection references the catalog).

Ingest a spec by paste, file upload, or a public HTTPS URL (fetched once, ETag captured). Per-operation embeddings power semantic endpoint ranking in `api_list_endpoints`.

![Add spec](../images/screenshots/light/admin-catalog-spec-modal-light.webp#only-light)![Add spec](../images/screenshots/dark/admin-catalog-spec-modal-dark.webp#only-dark)

![New catalog](../images/screenshots/light/admin-admin-catalog-create-light.webp#only-light)![New catalog](../images/screenshots/dark/admin-admin-catalog-create-dark.webp#only-dark)

See [API Catalogs](api-catalogs.md) for the full catalog model, ingestion paths, and the embedding job queue.

## Connections

The Connections page manages toolkit backend instances (Trino, DataHub, S3, MCP gateway) using a split-pane layout.

![Connections](../images/screenshots/light/admin-admin-connections-light.webp#only-light)![Connections](../images/screenshots/dark/admin-admin-connections-dark.webp#only-dark)

**Left pane** — Connection list grouped by kind (DataHub, S3, Trino), with source badges (**file** or **database**), descriptions, and tool counts.

**Right pane** — Selected connection detail showing:

- **Metadata** — Kind, created by, and last updated
- **Configuration** — Key-value pairs with "Show sensitive" toggle for passwords and tokens
- **Actions** — Edit, and Delete for a connection the config file does not declare

**Source tracking:**

| Badge | Meaning |
|-------|---------|
| **file** | A live toolkit connection with no stored record |
| **database** | A stored record with no live toolkit connection |
| **both** | Both a live connection and a stored record |

**both** is the ordinary state for nearly every connection on a database-backed
deployment, not a sign of an override: the platform seeds a credential-free
record for each connection the config file declares so that
`mcp:connection:(kind,name)` knowledge-page references resolve. The badge
therefore does not say where a connection came from. The detail pane says so
directly for the connections the file declares.

- **Connections the config file declares cannot be edited or deleted here.**
  The detail pane says the file declares them and offers neither button; the API
  refuses both requests with `409`. Deleting the stored record would drop the
  connection from every live toolkit of its kind, on the replica handling the
  request and on every peer, until each of them restarted and the file put it
  back. Saving a record for one reached the running process but was discarded at
  the next restart, because the platform skips a name the file already declares.
  Change the config file instead.
- **+ Add Connection** at the bottom creates database-only connections.

Creating or editing a connection opens a kind-aware editor: a markdown description plus the configuration fields for the selected kind (Trino host/port/catalog, S3 bucket/region, DataHub server, or an API-gateway base URL and catalog picker), with TLS material and auth handled inline.

![New Connection](../images/screenshots/light/admin-admin-connection-create-light.webp#only-light)![New Connection](../images/screenshots/dark/admin-admin-connection-create-dark.webp#only-dark)

![Edit Connection](../images/screenshots/light/admin-admin-connection-edit-light.webp#only-light)![Edit Connection](../images/screenshots/dark/admin-admin-connection-edit-dark.webp#only-dark)

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

Creating or editing a persona opens the editor: an identity panel (name, display name, roles, priority) beside a live **Permissions** explorer that previews exactly which tools and connections the allow/deny patterns resolve to, with a running allowed/denied count and a resolution trace. Quick templates (Administrator, Read Only, Analyst, Engineer) seed common policies. A separate **AI Assistant Behavior** tab tunes the persona's prompts and hints.

![New Persona](../images/screenshots/light/admin-admin-persona-create-light.webp#only-light)![New Persona](../images/screenshots/dark/admin-admin-persona-create-dark.webp#only-dark)

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

The add-key form collects a name, optional owner email and description, roles (with a role browser), and an expiration. The generated key is shown once in a copy-now banner and never again.

![Add API Key](../images/screenshots/light/admin-admin-key-create-light.webp#only-light)![Add API Key](../images/screenshots/dark/admin-admin-key-create-dark.webp#only-dark)

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

![Users](../images/screenshots/light/admin-admin-users-light.webp#only-light)![Users](../images/screenshots/dark/admin-admin-users-dark.webp#only-dark)

Features:

- **User table** — Name, email, status badge, and last-seen date
- **Status badge** — **Active** (green) for someone seen via a real sign-in, or **Invited** (amber) for someone an admin pre-added who has not logged in yet
- **+ Add User** — Pre-add a person by email (with optional first and last name) so they are selectable for sharing before they have ever signed in
- **Edit** — Change a person's first and last name. Admin-entered names take precedence: a later sign-in only fills blank name fields, it never overwrites a name an admin set
- **Search** — Filter the directory by name or email
- **Auto-recording** — Anyone who authenticates (OIDC/OAuth) is upserted into the directory automatically with the name from their token claims; API-key and anonymous sessions are not recorded

Requires a database. Without one the directory is disabled and the share
dialog falls back to free-typed email only.

## Settings

The Settings page holds global platform settings. The first section is
**Email (SMTP)**, which configures outbound mail for [email
notifications](notifications.md). Host, port, credentials, sender address,
and TLS mode are stored in the database (the password encrypted at rest
and write-only), and a **Send test** action verifies the configuration by
delivering a test email; when the target address has opted out of
notification emails, an informational notice appears next to the send
action (the test still sends). Like other admin configuration, editing
requires database config mode. Email branding (footer text, legal links,
Reply-To) is implementor-owned YAML, not part of this page; see the
[portal configuration](configuration.md).

A **Knowledge review queue alert** section follows: it decides when the
platform emails an operator about unreviewed insights — the pending-count
and oldest-age thresholds, the re-alert cooldown, and the recipients. A
section that would deliver nothing (enabled with no recipients, or with
both thresholds cleared) says so in a banner rather than saving silently.
See [review queue alerts](notifications.md#review-queue-alerts).

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

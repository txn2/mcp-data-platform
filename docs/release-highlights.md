# Release Highlights

A timeline-oriented quick reference to the significant features and improvements
shipped since v1.61.2, spanning releases v1.62.0 through v1.81.1. It groups the
work by theme and tags each item with the release it landed in, so you can scan
what changed without reading every per-version note.

The [GitHub Releases](https://github.com/txn2/mcp-data-platform/releases) remain
the authoritative, per-release changelog with exact commits and verification
artifacts. This page is a curated summary; routine dependency and CI bumps are
omitted.

## At a glance

- The **API Gateway** (`kind=api`) matured from a basic proxy into a full
  toolkit: versioned OpenAPI catalogs, semantic endpoint discovery, mTLS and
  private-CA trust, HTTP Basic and OAuth auth modes, WebDAV methods, a REST
  shim for non-MCP clients, bounded streaming for large and binary bodies, and
  a Postgres-backed embedding pipeline.
- A built-in **`platform-admin` self-configuration connection** lets an admin
  operate the platform (personas, connections, prompts, API keys) by asking the
  agent instead of clicking through the Portal.
- **Connection access is now deny-by-default**, matching the tool axis: a
  persona reaches a connection only when it explicitly grants it.
- New **semantic discovery and indexing** stack: `platform_find_tools`, a
  reusable indexing-job framework, a cross-kind embedding-health dashboard,
  pgvector-backed embeddings, and hybrid (vector plus lexical) memory recall.
- A **first-class prompt library** (epic #525): a draft/approved/deprecated
  lifecycle with tags, admin promotion with a review queue, native user-to-user
  sharing, and semantic discovery, capped by **relevance search on every
  surface** (insights, prompts, saved assets, and collections) through one
  shared hybrid ranking (v1.80.0 and v1.81.0).
- A full **observability layer**: Prometheus metrics on the hot paths, an
  authenticated PromQL proxy, redesigned admin dashboards, and partitioned,
  caller-tagged audit logging.

## API Gateway (`kind=api`)

The largest area of investment. The gateway now proxies arbitrary REST and HTTP
APIs through the same auth, persona, and audit pipeline as the rest of the
platform.

- **Versioned API catalogs and `api_get_endpoint_schema`**: OpenAPI specs are
  stored as versioned catalogs that back connections; the schema tool returns
  per-operation parameters, request body, and per-status responses. Later work
  surfaced `oneOf`/`anyOf`/`allOf`, response headers, and `const`, and tolerated
  real-world spec drift and PascalCase primitive type names (v1.66.0 and around).
- **`api_list_specs` and multi-spec catalogs**: browse a catalog's component
  specs before drilling into one operation set, with a multi-spec gate.
- **REST shim for non-MCP clients (v1.63.0)**: `POST /api/v1/gateway/{connection}/invoke`
  exposes `api_invoke_endpoint` to HTTP clients such as Apache NiFi, Airflow, and
  `curl`, governed by the same persona allowlists and audit pipeline.
- **Auth modes**: added **HTTP Basic** (v1.65.0) and **mTLS with private-CA
  trust** alongside a connection editor UX (v1.67.0); OAuth connection config
  was unified across toolkit kinds (v1.70.0).
- **WebDAV method support (v1.68.0)**: `PROPFIND`, `MKCOL`, `MOVE`, `COPY`.
- **Per-spec base path override** and other discovery/ranking refinements,
  including a fix so `api_get_endpoint_schema` resolves synthesized operation
  ids under a base path (v1.79.2).
- **Bounded memory and streaming**: a global in-flight memory budget plus a raw
  streaming passthrough route, `api_export` streaming directly to S3 instead of
  buffering the full body (v1.77.0), and refusal of binary response bodies
  before buffering (v1.77.1). These keep the shared gateway process at bounded
  memory regardless of response size.
- **Embedding pipeline**: a Postgres-backed embedding job queue (v1.62.0),
  operation embeddings persisted in pgvector keyed on spec, incremental embed
  progress with configurable worker concurrency (v1.64.0), and embeddings warmed
  at connection-add time off the request path.
- **Correct gateway semantics**: upstream status is surfaced in the response
  body while the platform status reflects platform-level outcomes; auth/authz
  denials map to `401`/`403` rather than `500`; `Content-Type` is driven from the
  OpenAPI catalog.

## Self-configuration and connection access

- **Built-in `platform-admin` self-connection (v1.78.0)**: a loopback API
  gateway connection pointed at the platform's own `/api/v1/admin/*` REST API,
  with its catalog sourced from the spec embedded in the binary so it always
  matches the running version. Calls use identity passthrough, so changes are
  authenticated and audited as the acting admin.
- **Deny-by-default connection access (v1.79.0)**: connections now behave like
  tools. A persona reaches a connection only via an explicit `connections.allow`
  match; an empty allow grants nothing. This replaced an earlier fail-open
  default and a separate admin-only flag. (Breaking: persona configs must list
  the connections each persona may reach.)
- **Persona-editor connection list cleanup (v1.79.1)**: built-in toolkits with
  no real connection identity (knowledge, memory, portal) no longer appear as
  gateable connections, and the group quick-add suggests patterns that actually
  match.

## Connection OAuth and credentials

- **Unified OAuth connection-config schema (v1.70.0)** across toolkit kinds, so
  an OAuth connection is configured the same way regardless of kind.
- A series of correctness fixes: a distributed refresh lock that preserves the
  IdP error code and description, refresh using plain Basic auth to match the
  exchange, client-auth failures classified as terminal with health surfaced on
  the connection list, and honest event types for locally-decided revocations.

## Semantic discovery, embeddings, and indexing

- **`platform_find_tools` (v1.72.0)**: semantic tool discovery so an agent can
  find the right tool by intent.
- **Reusable indexing-job framework (v1.71.0)** plus an **API-catalog migration**,
  later extended with history retention and a paginated jobs table.
- **Cross-kind Indexing dashboard (v1.73.0)** for embedding health, with later
  work to communicate coherent state rather than raw job rows and to compute a
  real content-diff so indexing stops re-syncing forever (v1.75.x).
- **Hybrid memory recall (v1.74.0)**: vector search with a lexical fallback,
  plus an `index_jobs` backfill.
- **Semantic-similarity fallback for cross-toolkit injection** in the enrichment
  layer.

## Prompt library and relevance search (epic #525)

The prompt-library epic completed across four phases and, together with portal
search work, extended relevance ranking (vector plus lexical, with an automatic
lexical-only fallback) to every searchable surface. Everything is additive and
backward compatible; with no embedding provider configured, each new search
degrades cleanly to keyword ranking.

- **Prompt lifecycle and tags (v1.81.0, phase 1)**: prompts gained a
  `draft -> approved -> deprecated -> superseded(by)` lifecycle with validated
  transitions, GIN-indexed multi-valued `tags[]` alongside the single
  `category`, and scope-aware name uniqueness (personal names unique per owner;
  global and persona names globally unique).
- **Admin promotion and review queue (v1.81.0, phase 2)**: an owner can request
  promotion of a personal prompt to a persona or global scope through
  `manage_prompt update`; admins approve or reject from a review queue.
  Promotion to a shared scope stays admin-only.
- **Native user-to-user prompt sharing (v1.81.0, phase 3)**: an owner can share
  a personal prompt directly with another user by email; the recipient gets a
  real, runnable MCP prompt (served as `shared-<name>`) with its `arguments[]`
  intact and a portal entry under Shared With Me. This reuses the existing
  `portal_shares` table and replaces the prior path that degraded a shared
  prompt into a markdown asset snapshot.
- **Semantic prompt discovery (v1.81.0, phase 4)**: approved, enabled prompts
  are embedded off the request path on the shared index-jobs framework;
  `manage_prompt list query=...` and the portal prompt-library search rank by
  similarity, with visibility and persona scoping applied before ranking.
- **Native MCP prompt visibility (v1.81.0)**: `prompts/list` and `prompts/get`
  are now scoped per caller by a visibility middleware, matching the access
  model the REST API and `manage_prompt` already enforced.
- **Portal Knowledge & Memory search (v1.80.0)**: a debounced free-text search
  box on both the Knowledge and Memory tabs ranks results by relevance, reusing
  the same hybrid primitives as `memory_recall` (knowledge insights are stored
  as `knowledge`-dimension memory records, so there is no separate index).
  Every search is scoped server-side to the caller's email and fails closed
  (`403`) if the identity carries no email.
- **Relevance search over saved assets and collections (v1.81.0)**, plus a new
  `recall_insight` tool, all routed through the one shared hybrid ranking.

## Observability and audit

- **Prometheus metrics** on the tool-call and API gateway chokepoints, including
  inbound API gateway metrics with per-endpoint labels and toolkit plus
  DB-pool metrics with k8s manifests; metrics are enabled by default with a
  hardened shutdown path.
- **Authenticated PromQL query proxy** that accepts the Portal session cookie,
  feeding a **redesigned admin observability dashboard (v1.69.0)**.
- **Audit improvements**: events tagged with an `event_kind` to split MCP from
  API gateway traffic, caller class tagged via source, and monthly audit
  partitions.

## Knowledge and memory

- **Real changeset rollback** with `list_changesets` actions in the knowledge
  toolkit.
- Knowledge and memory scoped correctly in the Portal, with owner unified on
  email and collapsible reveals.
- Hybrid memory recall (see indexing section) and portal relevance search across
  both tabs (see prompt library and relevance search section).

## Portal and admin UI

- **Unified persona editor** with tabbed permissions and AI-behavior views, and
  a live "what can this persona do?" preview across tools and connections.
- **Redesigned admin observability dashboards (v1.69.0)** with denser,
  operator-focused visualizations.
- **Markdown connection descriptions** with a collapsible reveal (v1.77.2).
- Connection list and identifier-input refinements, persona-editor connection
  fixes, and shared-collection improvements (recipient-visible assets,
  idempotent revoke, confirm-before-remove, shared thumbnail access).
- Mermaid node labels preserved in saved markdown assets.
- Two portal reliability fixes (v1.81.0), including one that could blank the
  asset library on a keystroke.

## Reliability, security, and correctness

- **Cross-user provenance leak closed** on the stateless HTTP transport.
- **Cross-replica reload bus** so admin config changes propagate across
  replicas (v1.70.1).
- Connection config validated before save, with bad instances skipped on
  startup instead of failing the whole boot.
- Go toolchain moved to 1.26.4 to clear two standard-library advisories
  (shipped with v1.79.0).
- **Self-describing, uniform tool-execution error contract** in the middleware
  so tool-call failures report consistently (v1.81.1).

## Maintenance

Routine dependency and CI updates across the period (Go modules, the MCP Go
SDK, OpenTelemetry, GitHub Actions, and UI packages) are tracked in the
individual GitHub Releases and are not enumerated here.

---
description: MCP tools from DataHub, Trino, S3, and Knowledge toolkits. Search metadata, run SQL queries, access S3 objects with automatic semantic enrichment, and capture domain knowledge.
---

# Available Tools

mcp-data-platform provides tools from five integrated toolkits. Each tool can be invoked by name through any MCP client.

!!! tip "Reducing token usage with tool visibility"
    The full tool list is 30-35 tools depending on configuration. Deployments that only use a subset can configure `tools.allow` and `tools.deny` at the top level of `platform.yaml` to hide unused tools from `tools/list` responses. This saves LLM context tokens without affecting authorization. See [Configuration](configuration.md#tool-visibility-configuration) for details.

## Tools Summary

| Toolkit | Tool | Description |
|---------|------|-------------|
| Trino | `trino_query` | Execute read-only SQL queries (SELECT, SHOW, DESCRIBE, EXPLAIN) |
| Trino | `trino_execute` | Execute any SQL including write operations (INSERT, UPDATE, DELETE, CREATE, DROP) |
| Trino | `trino_explain` | Get query execution plans |
| Trino | `trino_browse` | Browse the catalog hierarchy: list catalogs, schemas, or tables |
| Trino | `trino_describe_table` | Get table schema and metadata |
| Trino | `trino_export` | Export query results directly to a portal asset (CSV, JSON, Markdown, text) |
| Trino | `trino_list_connections` | List configured Trino connections |
| DataHub | `datahub_get_entity` | Get detailed entity information |
| DataHub | `datahub_get_schema` | Get dataset schema |
| DataHub | `datahub_get_lineage` | Get dataset or column-level lineage |
| DataHub | `datahub_get_queries` | Get popular queries for a dataset |
| DataHub | `datahub_get_glossary_term` | Get glossary term details |
| DataHub | `datahub_browse` | Browse tags, domains, or data products |
| DataHub | `datahub_get_data_product` | Get data product details |
| DataHub | `datahub_create` | Create entities — tags, domains, glossary terms, etc. (if not read-only) |
| DataHub | `datahub_update` | Update metadata — descriptions, tags, owners, domains, etc. (if not read-only) |
| DataHub | `datahub_delete` | Delete entities — tags, domains, queries, etc. (if not read-only) |
| DataHub | `datahub_list_connections` | List configured DataHub connections |
| S3 | `s3_list_buckets` | List S3 buckets |
| S3 | `s3_list_objects` | List objects in a bucket |
| S3 | `s3_get_object` | Get object contents |
| S3 | `s3_get_object_metadata` | Get object metadata |
| S3 | `s3_presign_url` | Generate pre-signed URL |
| S3 | `s3_list_connections` | List configured S3 connections |
| S3 | `s3_put_object` | Upload object (if not read-only) |
| S3 | `s3_delete_object` | Delete object (if not read-only) |
| S3 | `s3_copy_object` | Copy object (if not read-only) |
| Knowledge | `search` | The one way to discover: balanced, grouped-by-source results across catalog, memory, insights, feedback, assets, prompts, API endpoints, and connections |
| Memory | `memory_capture` | The one way to record knowledge: sink-class routed, recall-first |
| Knowledge | `apply_knowledge` | Review and promote reviewed captures to the catalog (admin-only) |
| Memory | `memory_manage` | Manage existing memories: update, forget, list, review_stale (opt-in per persona) |
| Portal | `save_artifact` | Save an AI-generated artifact (JSX, HTML, SVG, etc.) |
| Portal | `manage_artifact` | List, get, update, delete, or relevance-search saved artifacts and collections |
| Portal | `manage_feedback` | Review and respond to human feedback (list pending across everything, get, reply, resolve, request/respond validation) |
| Platform | `platform_find_tools` | Find the most relevant tools for a natural-language task, ranked by semantic similarity (persona-scoped) |

---

## Trino Tools

### trino_query

Execute a read-only SQL query against Trino. Write operations (INSERT, UPDATE, DELETE, CREATE, DROP, etc.) are rejected with a clear error directing users to `trino_execute`.

Annotated with `ReadOnlyHint: true` so MCP clients can auto-approve calls to this tool.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to execute (read-only) |
| `limit` | integer | No | 1000 | Maximum rows to return |
| `connection` | string | No | default | Connection name to use |

**Example:**

```
Show me the top 10 customers by revenue
```

Tool call: `trino_query` with query `SELECT customer_id, SUM(amount) as revenue FROM orders GROUP BY customer_id ORDER BY revenue DESC LIMIT 10`

**Response includes:**

- Query results as formatted table or JSON
- Row count and execution time
- **Semantic context** (if enabled): table description, owners, tags, quality score, deprecation warnings

---

### trino_execute

Execute any SQL against Trino, including write operations (INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, etc.). Use this tool for data modification.

Annotated with `DestructiveHint: true` so MCP clients will prompt for user confirmation.

When `read_only: true` is configured at the instance level, write operations are blocked on this tool as well.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to execute |
| `limit` | integer | No | 1000 | Maximum rows to return |
| `connection` | string | No | default | Connection name to use |

---

### trino_explain

Get the execution plan for a query without running it.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to explain |
| `connection` | string | No | default | Connection name to use |

---

### trino_browse

Browse the Trino catalog hierarchy. Omit all parameters to list catalogs. Provide `catalog` to list schemas. Provide `catalog` and `schema` to list tables (with optional `pattern` filter).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `catalog` | string | No | - | Catalog name. Omit to list all catalogs |
| `schema` | string | No | - | Schema name. Requires `catalog`. Omit to list schemas |
| `pattern` | string | No | - | LIKE pattern to filter tables (only when listing tables) |
| `connection` | string | No | default | Connection name to use |

---

### trino_describe_table

Get detailed information about a table including columns, types, and statistics.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `table` | string | Yes | - | Table name (can be `catalog.schema.table`) |
| `connection` | string | No | default | Connection name to use |

**Response includes:**

- Column names and data types
- Nullable constraints
- Partition information
- **Semantic context** (if enabled): description, owners, tags, quality score

---

### trino_export

Export query results directly to a portal asset file, bypassing the LLM token budget. Use this after validating the query shape with `trino_query` using a small `LIMIT`. The full result set is formatted and written to S3 as an immutable portal asset. Only metadata (asset ID, URL, row count, size) is returned to the agent — not the data.

Requires portal to be enabled with S3 storage configured. Requires explicit persona authorization (not inherited from `trino_query` access by default).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `sql` | string | Yes | - | SQL query to execute (read-only enforced) |
| `format` | string | Yes | - | Output format: `csv`, `json`, `markdown`, or `text` |
| `name` | string | Yes | - | Display name for the exported asset (max 255 chars) |
| `connection` | string | No | default | Trino connection name |
| `description` | string | No | - | Description of the exported asset (max 2000 chars) |
| `tags` | array | No | [] | Tags for categorization. Lowercase kebab-case, max 50 chars each, max 20 tags. Tags starting with `_sys-` are reserved for system use. |
| `limit` | integer | No | deployment max | Maximum rows to export (subject to deployment cap) |
| `idempotency_key` | string | No | - | Client-supplied key to prevent duplicate assets on retry |
| `timeout_seconds` | integer | No | deployment default | Query execution timeout in seconds |
| `create_public_link` | boolean | No | false | Generate a public share link for the exported asset. Useful for automation pipelines that need a shareable URL. |

**Response includes:**

- Asset ID and portal URL
- Public share URL (if `create_public_link` is true)
- Format, row count, and file size in bytes
- No query data (data is written to S3, not returned through the LLM)

**Security features:**

- SQL runs through the same read-only interceptor as `trino_query`
- CSV formula injection escaping enabled by default (cells starting with `=`, `+`, `-`, `@` are escaped)
- Sensitivity tags inherited from source datasets (PII, confidential, etc.) are automatically applied as `_sys-classification:*` tags
- Hard row and byte caps enforced per deployment
- No asset record created unless the S3 write fully succeeds

---

### trino_list_connections

List all configured Trino connections.

**Parameters:** None

---

## DataHub Tools

!!! note "Catalog search moved to `search`"
    Relevance search over the catalog is now part of the universal
    [`search`](#search) tool. The DataHub toolkit retains
    `datahub_browse` for structured navigation (platform/domain/tag/entity-type)
    and the entity-detail tools below.

### datahub_get_entity

Get detailed information about a specific entity.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Entity URN |
| `connection` | string | No | default | Connection name to use |

**Response includes:**

- Full entity metadata
- Owners, tags, glossary terms
- Domain, data product associations
- Deprecation status
- **Query context** (if enabled): Trino table availability

---

### datahub_get_schema

Get the schema for a dataset.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Dataset URN |
| `connection` | string | No | default | Connection name to use |

---

### datahub_get_lineage

Get upstream or downstream lineage for an entity. Set `level=column` for column-level lineage showing which upstream columns feed each downstream column. Default (`dataset`) returns dataset-level relationships with direction and depth control.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Entity URN |
| `level` | string | No | `dataset` | Granularity: `dataset` or `column` |
| `direction` | string | No | `DOWNSTREAM` | `UPSTREAM` or `DOWNSTREAM` (dataset level only) |
| `depth` | integer | No | 1 | Maximum traversal depth, max 5 (dataset level only) |
| `connection` | string | No | default | Connection name to use |

---

### datahub_get_queries

Get popular queries associated with a dataset.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Dataset URN |
| `limit` | integer | No | 10 | Maximum queries to return |
| `connection` | string | No | default | Connection name to use |

---

### datahub_get_glossary_term

Get details about a glossary term.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Glossary term URN |
| `connection` | string | No | default | Connection name to use |

---

### datahub_browse

Browse the DataHub catalog by category. Set `what=tags` to list tags, `what=domains` to list data domains, or `what=data_products` to list data products.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | What to browse: `tags`, `domains`, or `data_products` |
| `filter` | string | No | - | Optional filter string (tags only) |
| `connection` | string | No | default | Connection name to use |

---

### datahub_get_data_product

Get details about a data product.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Data product URN |
| `connection` | string | No | default | Connection name to use |

---

### datahub_create

Create a new entity or resource in DataHub. Uses the `what` discriminator to select the entity type.

Only available when `read_only: false` in the DataHub toolkit configuration.

Annotated with `DestructiveHint: false`, `IdempotentHint: false`, `OpenWorldHint: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | Entity type to create (see table below) |
| `name` | string | Varies | - | Entity name (required for most types) |
| `connection` | string | No | default | Connection name to use |

Additional parameters vary by `what` value — see the [mcp-datahub documentation](https://github.com/txn2/mcp-datahub) for full parameter details per entity type.

| `what` | Creates | Key fields |
|--------|---------|------------|
| `tag` | Tag | `name` |
| `domain` | Domain | `name` |
| `glossary_term` | Glossary term | `name` |
| `data_product` | Data product | `name`, `domain_urn` |
| `document` | Context document (1.4.x+) | `name` |
| `application` | Application | `name` |
| `query` | Saved query | `value` (SQL) |
| `incident` | Incident | `name`, `incident_type`, `entity_urns` |
| `structured_property` | Structured property | `qualified_name`, `value_type`, `entity_types` |
| `data_contract` | Data contract | `dataset_urns` |

---

### datahub_update

Update metadata on an existing DataHub entity. Uses the `what` discriminator to select what to update, with an optional `action` for add/remove operations.

Only available when `read_only: false` in the DataHub toolkit configuration.

Annotated with `DestructiveHint: false`, `IdempotentHint: true`, `OpenWorldHint: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | What to update (see table below) |
| `urn` | string | Varies | - | Entity URN to update |
| `action` | string | Varies | - | `add` or `remove` (required for tags, glossary terms, links, owners) |
| `connection` | string | No | default | Connection name to use |

Additional parameters vary by `what` value — see the [mcp-datahub documentation](https://github.com/txn2/mcp-datahub) for full parameter details.

| `what` | `action` | Description |
|--------|----------|-------------|
| `description` | — | Set entity description |
| `column_description` | — | Set schema field description |
| `tag` | add/remove | Add or remove a tag |
| `glossary_term` | add/remove | Add or remove a glossary term |
| `link` | add/remove | Add or remove a link |
| `owner` | add/remove | Add or remove an owner |
| `domain` | set/remove | Set or remove domain assignment |
| `structured_properties` | set/remove | Set or remove structured property values |
| `structured_property` | — | Update a structured property definition |
| `incident_status` | — | Update incident status |
| `incident` | — | Update incident details |
| `query` | — | Update query properties |
| `document_contents` | — | Update document title/text (1.4.x+) |
| `document_status` | — | Update document status (1.4.x+) |
| `document_related_entities` | — | Update document related entities (1.4.x+) |
| `document_sub_type` | — | Update document sub-type (1.4.x+) |
| `data_contract` | — | Upsert a data contract |

---

### datahub_delete

Delete an entity or resource from DataHub. Uses the `what` discriminator to select the entity type.

Only available when `read_only: false` in the DataHub toolkit configuration.

Annotated with `DestructiveHint: true`, `IdempotentHint: true`, `OpenWorldHint: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | Entity type to delete (see below) |
| `urn` | string | Yes | - | Entity URN to delete |
| `connection` | string | No | default | Connection name to use |

Supported `what` values: `query`, `tag`, `domain`, `glossary_entity`, `data_product`, `application`, `document`, `structured_property`.

---

### datahub_list_connections

List all configured DataHub connections.

**Parameters:** None

---

## S3 Tools

### s3_list_buckets

List available S3 buckets.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `connection` | string | No | default | Connection name to use |

---

### s3_list_objects

List objects in a bucket.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `prefix` | string | No | - | Key prefix filter |
| `delimiter` | string | No | - | Delimiter for hierarchy |
| `max_keys` | integer | No | 1000 | Maximum objects to return |
| `connection` | string | No | default | Connection name to use |

**Response includes:**

- Object keys, sizes, last modified
- **Semantic context** (if enabled): matching DataHub datasets with metadata

---

### s3_get_object

Get the contents of an object.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `connection` | string | No | default | Connection name to use |

---

### s3_get_object_metadata

Get metadata for an object without downloading it.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `connection` | string | No | default | Connection name to use |

---

### s3_presign_url

Generate a pre-signed URL for temporary access.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `expires` | duration | No | 15m | URL expiration time |
| `connection` | string | No | default | Connection name to use |

---

### s3_list_connections

List all configured S3 connections.

**Parameters:** None

---

### s3_put_object

Upload an object to S3. Only available when `read_only: false`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `content` | string | Yes | - | Object content |
| `content_type` | string | No | - | MIME type |
| `connection` | string | No | default | Connection name to use |

---

### s3_delete_object

Delete an object. Only available when `read_only: false`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `connection` | string | No | default | Connection name to use |

---

### s3_copy_object

Copy an object. Only available when `read_only: false`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `source_bucket` | string | Yes | - | Source bucket name |
| `source_key` | string | Yes | - | Source object key |
| `dest_bucket` | string | Yes | - | Destination bucket name |
| `dest_key` | string | Yes | - | Destination object key |
| `connection` | string | No | default | Connection name to use |

---

## Knowledge Tools

!!! tip "Full Documentation"
    For the complete knowledge capture workflow including governance, lifecycle, and configuration, see [Knowledge Capture](../knowledge/overview.md).

### memory_capture

The one way to record knowledge. The `type` (sink-class) is the single organizing axis and drives routing: `personal_preference` and `episodic_event` are live for the capturer immediately; `business_knowledge`, `schema_entity`, and `operational_rule` are recorded as **pending** and reviewed before promotion to a shared catalog via `apply_knowledge`. Lives in the memory toolkit so creating memory never requires the knowledge toolkit.

Capture is **recall-first**: before writing, it runs a similarity check over the caller's own memory and, on a near-duplicate, supersedes the prior record instead of appending. `schema_entity` carries `entity_urns` and optional `suggested_actions` (the catalog-change payload `apply_knowledge` later applies).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `type` | string | Yes | - | Sink-class: personal_preference, episodic_event (both live), business_knowledge, schema_entity, operational_rule (reviewed) |
| `content` | string | Yes | - | Knowledge to record (10-4000 chars) |
| `entity_urns` | array | No | [] | Related DataHub entity URNs (schema_entity); max 10 |
| `suggested_actions` | array | No | [] | Proposed catalog changes for apply_knowledge (schema_entity) |
| `confidence` | string | No | medium | high, medium, low |
| `source` | string | No | user | user, agent_discovery, enrichment_gap |
| `thread_ids` | array | No | [] | Feedback threads this capture resolves |

---

### search

The universal, topology-free discovery entry point. Call it FIRST: one query
fans across every searchable source the caller can access and returns results
**grouped by source** with a **coverage summary**, so the agent sees the shape of
the answer space instead of tunneling into the first tool that comes to mind.
Structured catalog navigation (platform/domain/tag/entity-type filters) stays in
`datahub_browse`; the scoped API drill-down stays in `api_list_endpoints`.

!!! note "`knowledge_search` was renamed to `search`"
    The `#632` read-path tool `knowledge_search` was renamed to `search` in
    `#645` and its corpus widened to include API endpoints and connections.

**Corpus (everything the persona can access):** the technical catalog (DataHub,
when configured), canonical knowledge pages (the internal-knowledge home for
business/domain ontology, searched over their full markdown content), the caller's
personal memory, captured insights, the caller's feedback threads, saved assets,
prompts, API endpoints (aggregated across every API gateway connection, reusing
the per-connection semantic ranking of `api_list_endpoints`), and connections. Memory, insights, and
assets are per-user, scoped server-side to the caller, so a search never surfaces
another user's private records; the catalog, knowledge pages, prompts, endpoints
(each gateway applies its own route policy), and connections are shared.
A caller with no identity still sees shared sources but no per-user data. API
endpoints and connections are in the default corpus, not behind an opt-in.

**Balanced result set.** Rather than one flat relevance list (which lets one
strong source dominate), the display set is built from a total budget with a
per-source floor (so every matching source stays visible), a per-source ceiling
(so none runs away), and redistribution of unused budget to the sources with more
relevant hits. Every response also carries a `coverage` summary of per-source
`matched` vs `shown` counts, so the agent learns where the answer space lives even
when only the top few of each source are displayed. Hits are navigational
snippets (title, `ref`, short context line, `source`); the agent drills in with
the scoped tool (`trino_query`, `api_invoke_endpoint`, `datahub_get_entity`).

A query may be text (`intent`), entity-keyed (`entity_urns`, returning every
source linked to those datasets and their lineage neighbors: the catalog entity,
URN-linked insights, and your URN-linked memory), or both. Ranking is
hybrid (semantic vector + lexical) when an embedding provider is configured and
lexical-only otherwise; an entity-only query reports ranking `entity`. The
response carries a `ranking` field, a `count` (total hits shown), a `groups`
array (each `{source, hits[]}` where every hit pairs the matched `text` with its
`source`, a `ref`, a relevance `score`, and where present `status`, `entity_urns`,
and `dimension`), and a `coverage` array (`{source, matched, shown}`).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `intent` | string | Conditional | - | Natural-language description of what you are looking for. Provide `intent`, `entity_urns`, or both |
| `context` | string | No | - | Optional surrounding context, folded into the intent to sharpen relevance |
| `entity_urns` | array | Conditional | - | Exact entity-keyed lookup: everything linked to these DataHub URNs (the catalog entity, insights about it, and your memory linked to it), expanded along lineage |
| `status` | string | No | - | Optional filter by insight review status (pending, approved, rejected, applied, superseded, rolled_back) |
| `sources` | array | No | - | Narrow the search to named sources (`datahub`, `knowledge_pages`, `memory`, `insights`, `assets`, `prompts`, `endpoints`, `connections`). Only narrows; never opts into a source the persona could not otherwise access |
| `limit` | integer | No | 10 | Total results to display across all sources (max 50) |

---

### Knowledge pages (canonical business/domain knowledge)

Knowledge pages are the platform's **canonical** store for business and domain
knowledge (the internal-knowledge sibling of DataHub), authored as markdown in the
portal. The provisional "draft" of knowledge is the memory/insight inbox; a page,
once it exists, is canonical. They are a distinct, **org-shared** entity (not
owner-scoped portal assets): the markdown body is stored inline in Postgres so
page **content** is semantically searchable, and pages surface in the unified
`search` tool under the `knowledge_pages` source. Threads/feedback attach to a
page (`target_type=asset` reuse is planned; native attach lands with the threads
phase).

**Governance:** every authenticated user can read pages; create/edit/remove is
gated to personas with `apply_knowledge` access (the same authorization that lets
a persona apply everyone's captured insights), so no separate curator role is
introduced.

**REST API** (`/api/v1/portal/knowledge-pages`), mounted with the portal handler:

| Method | Path | Access | Description |
|--------|------|--------|-------------|
| GET | `/knowledge-pages` | any user | List pages (filter by `tag`, `q`, paginated) |
| GET | `/knowledge-pages/search?q=` | any user | Relevance search over page content (hybrid when an embedding provider is configured) |
| GET | `/knowledge-pages/{id}` | any user | Get a page |
| GET | `/knowledge-pages/{id}/versions` | any user | List version history |
| POST | `/knowledge-pages` | apply_knowledge | Create a page |
| PUT | `/knowledge-pages/{id}` | apply_knowledge | Edit a page (snapshots a new version) |
| DELETE | `/knowledge-pages/{id}` | apply_knowledge | Soft-delete a page |

Embeddings are produced off the request path by the shared `indexjobs` reconciler
(`source_kind=portal-knowledge-pages`); an edit clears the page's vector so the
reconciler re-embeds the new content.

---

### apply_knowledge

Review, synthesize, and apply captured insights to their canonical home. Admin-only. Requires `knowledge.apply.enabled: true`.

`apply_knowledge` is the **sink router** (#633): the `apply` action's `sink` decides where a capture is promoted.

- **`sink: datahub`** (default) applies the `changes` to a catalog entity (`entity_urn`).
- **`sink: knowledge_page`** promotes a `business_knowledge` or `operational_rule` capture to a canonical portal **knowledge page**, found-or-created by `page.slug` (so repeated promotions on the same slug consolidate into one living page). `schema_entity` insights go to DataHub; promoting one through the page sink is rejected.

Both sinks record a **changeset** (page promotions use `target_urn = "kp:<slug>"`) listed by `list_changesets` and reversible by `rollback`. Rolling back a page promotion soft-deletes a newly created page or restores a prior version, and is refused if the page was edited after the promotion.

`operational_rule` is stored as a knowledge page like `business_knowledge` (it is non-DataHub canonical knowledge); active enforcement of operational rules via the rules engine is tracked separately.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `action` | string | Yes | bulk_review, review, synthesize, apply, approve, reject, rollback, list_changesets |
| `sink` | string | No | apply target: `datahub` (default) or `knowledge_page` |
| `entity_urn` | string | Conditional | Required for review, synthesize, list_changesets, and apply with `sink=datahub` |
| `page` | object | Conditional | `{slug, title, body, summary?, tags?}` for apply with `sink=knowledge_page` |
| `insight_ids` | array | Conditional | Source insights; required for approve, reject. Sink-class is a non-binding hint; any insight can be applied to either sink (destination chosen at apply) |
| `changes` | array | Conditional | Required for apply with `sink=datahub` |
| `changeset_id` | string | Conditional | Required for rollback |
| `confirm` | bool | No | Required when `require_confirmation` is true (apply and rollback) |
| `review_notes` | string | No | Notes for approve/reject actions |
| `itemize` | bool | No | With `bulk_review`, also return the pending insights themselves (each with `captured_by`, `sink_class`, etc.), paginated by `offset`/`limit` |
| `limit` | int | No | Page size for itemized `bulk_review` (default 20, max 100) |
| `offset` | int | No | Page start for itemized `bulk_review`; pass the previous `next_offset` to continue |

**Actions:**

- **bulk_review**: Counts of all pending insights (`total_pending`, `by_entity`, `by_category`, `by_confidence`). Pass `itemize: true` to enumerate the queue itself, paginated, with each insight's `id`, `captured_by`, and `sink_class` (the relevance-ranked `search` tool cannot list it completely)
- **review**: Insights for a specific entity with current DataHub metadata
- **approve/reject**: Transition insight status with optional notes
- **synthesize**: Structured change proposals from approved insights
- **apply**: Write changes to DataHub with changeset tracking
- **list_changesets**: List an entity's changesets (id, timestamp, actor, change type, rollback status)
- **rollback**: Revert a changeset's changes to their before-image and transition its source insights to `rolled_back` (requires `changeset_id` and `confirm`)

**Supported change types for `apply` action:**

| Change Type | Target | Detail | Entity Types |
|-------------|--------|--------|--------------|
| `update_description` | `column:<fieldPath>` for column-level, empty for entity-level | Description text | datasets (column+entity), dashboards, charts, dataFlows, dataJobs, containers, dataProducts, domains, glossaryTerms, glossaryNodes |
| `add_tag` / `remove_tag` | Ignored | Tag name or URN (e.g., `pii` or `urn:li:tag:pii`) | All |
| `add_glossary_term` | Ignored | Term name or URN | All |
| `flag_quality_issue` | Ignored | Quality issue description | All |
| `add_documentation` | URL | Link description | All |
| `add_curated_query` | Ignored | Query name | Datasets only |
| `set_structured_property` | Property qualified name or URN | Value or JSON array | All (DataHub 1.4.x) |
| `remove_structured_property` | Property qualified name or URN | Removal reason | All (DataHub 1.4.x) |
| `raise_incident` | Incident title | Description | All (DataHub 1.4.x) |
| `resolve_incident` | Incident URN | Resolution message | All (DataHub 1.4.x) |
| `add_context_document` | Document title | Document content | Datasets, glossaryTerms, glossaryNodes, containers (DataHub 1.4.x) |
| `update_context_document` | Document ID | New content (`query_sql` = new title) | Datasets, glossaryTerms, glossaryNodes, containers (DataHub 1.4.x) |
| `remove_context_document` | Document ID | Ignored | All (DataHub 1.4.x) |

For `add_curated_query`, `query_sql` (required) and `query_description` (optional) provide the SQL statement. For `add_context_document` and `update_context_document`, `query_description` is the document category.

---

## Memory Tools

!!! tip "Full documentation"
    For the complete memory layer documentation including architecture, staleness detection, and cross-enrichment, see [Memory Layer](../memory/overview.md).

### memory_manage

Manages the lifecycle of existing persistent memory. Create new memory with `memory_capture`. Opt-in per persona (requires `memory_*` in `tools.allow`). Requires `memory.enabled: true`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `command` | string | No | Operation: `update`, `forget`, `list`, `review_stale`. Omit for help. (Create with `memory_capture`.) |
| `id` | string | For `update`, `forget` | Memory record ID |
| `dimension` | string | No | LOCOMO dimension: `knowledge`, `event`, `entity`, `relationship`, `preference` |
| `category` | string | No | Category: `correction`, `business_context`, `data_quality`, `usage_guidance`, `relationship`, `enhancement`, `general` |
| `confidence` | string | No | `high`, `medium`, `low` (default: `medium`) |
| `source` | string | No | `user`, `agent_discovery`, `enrichment_gap`, `automation`, `lineage_event` |
| `entity_urns` | string[] | No | DataHub entity URNs this memory relates to (max 10) |
| `metadata` | object | No | Arbitrary metadata (e.g., `suggested_actions`, `superseded_by`) |
| `filter_*` | string | No | Filters for `list`: `filter_dimension`, `filter_category`, `filter_status`, `filter_entity_urn` |
| `limit` | int | No | Page size for `list` (default 20, max 100) |
| `offset` | int | No | Pagination offset for `list` |

!!! note "Memory recall moved to `search`"
    Reading memory back (relevance, entity lookup, and lineage/graph traversal)
    is now part of the universal [`search`](#search) tool. The
    memory toolkit retains `memory_manage` for the write path.

---

## Portal Tools

The portal toolkit persists AI-generated artifacts (JSX dashboards, HTML reports, SVG charts) to S3 with PostgreSQL metadata, enabling viewing and sharing. Automatically captures provenance (which tool calls produced the artifact).

!!! tip "Prerequisites"
    Portal tools require `portal.enabled: true`, a configured S3 connection (`portal.s3_connection`), and `database.dsn`. See [Configuration](../reference/configuration.md#portal-configuration).

### save_artifact

Save an AI-generated artifact to the asset portal. Automatically captures provenance tracking which tool calls in the session led to this artifact.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `name` | string | Yes | - | Display name for the artifact (max 255 chars) |
| `content` | string | Yes | - | The artifact content (JSX, HTML, SVG, Markdown, etc.) |
| `content_type` | string | Yes | - | MIME type: text/html, text/jsx, image/svg+xml, text/markdown, application/json, text/csv |
| `description` | string | No | - | Description of the artifact (max 2000 chars) |
| `tags` | array | No | [] | Tags for categorization (max 20 tags, each max 100 chars) |

**Response includes:**

- Asset ID for future reference
- Portal URL for viewing (if `public_base_url` is configured)
- Provenance capture status and tool call count

---

### manage_artifact

List, retrieve, update, or delete saved artifacts. All mutations enforce ownership (users can only modify their own artifacts).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | Action to perform: list, get, update, delete, search |
| `asset_id` | string | Conditional | - | Required for get, update, delete |
| `content` | string | No | - | New content (for update — replaces S3 object) |
| `name` | string | No | - | New name (for update) |
| `description` | string | No | - | New description (for update) |
| `tags` | array | No | - | New tags (for update) |
| `content_type` | string | No | - | New content type (for update, only when replacing content) |
| `query` | string | Conditional | - | Free-text relevance query (required for search) |
| `limit` | integer | No | 50 | Max results for list (max 200); ranked search defaults to 20 (max 100) |

**Actions:**

- **list**: Show the current user's artifacts with metadata
- **get**: Retrieve full asset metadata by ID
- **update**: Change name, description, tags, or replace content
- **delete**: Soft-delete an artifact
- **search**: Rank the caller's own assets by relevance to `query`. Uses the same hybrid (vector + lexical) ranking as the prompt and Knowledge & Memory search: weighted hybrid when an embedding provider is configured, automatic lexical-only fallback otherwise. Returns each match with a `score` and reports `ranking` (`hybrid` or `lexical`). Scoped server-side to the caller's own assets by `owner_id` — the same ownership key the asset library and update/delete checks use, so search returns exactly what you see in the library — and fails closed when the caller has no identity, so a user can never find an asset they cannot view.

---

### manage_feedback

Review and respond to human feedback on your work. Feedback is its own tool (rather than actions on `manage_artifact`) so an agent discovers it by name. Threads live on an asset, collection, or prompt, or on the shared general channel.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | list, get, reply, resolve, request_validation, respond_validation |
| `asset_id` / `collection_id` / `prompt_id` | string | No | - | Scope a `list` to one artifact |
| `target_type` | string | No | - | `standalone` scopes a `list` to the general channel |
| `thread_id` | string | Conditional | - | Required for get, reply, resolve, request_validation, respond_validation |
| `body` | string | Conditional | - | Reply text (required for reply) |
| `status` / `validation_state` / `requires_resolution` | - | No | - | Filters for a targeted `list` |
| `validation_result` | string | Conditional | - | `validated` or `disputed` (required for respond_validation) |
| `validation_reason` | string | No | - | Optional reason recorded on the validation event |
| `limit` / `offset` | integer | No | 50 | Pagination |

**Actions:**

- **list (no target)**: The entry point for "review and act on any pending feedback." Returns the caller's pending feedback across **the assets and collections they own or can edit AND the shared general channel** — unresolved threads they did not author — plus any threads awaiting their validation. Newest first. (Prompt-thread feedback is reached by targeting the prompt with `prompt_id`, admin-only; it is not part of the no-target feed.)
- **list (with a target)**: Threads on one asset/collection/prompt or the standalone channel, filterable by status / validation_state / requires_resolution.
- **get**: One thread plus its full event timeline.
- **reply**: Append a comment to a thread.
- **resolve**: Mark a thread resolved.
- **request_validation**: Route a validation request to the thread author.
- **respond_validation**: The thread author (or an admin) records `validated`/`disputed`; disputing re-opens the thread.

**Access:** scoped to artifacts the caller owns or can edit (admins see all). General-channel threads are readable and replyable by any authenticated caller, and resolved only by the thread author or an admin. `memory_capture thread_ids=[...]` folds a thread into the knowledge loop and resolves it, gated by the same owns-or-edit check.

---

## Inspecting and Managing Tools (Admin Portal)

The portal at `/admin/tools` is a master-detail view: a left rail listing every registered tool grouped by connection or kind, and a right pane with five tabs for the selected tool.

### Detail tabs

| Tab | Purpose |
| --- | --- |
| Overview | Description (editable, see [overrides](#description-overrides) below), routing (toolkit / kind / connection), persona allow/deny matrix with matched pattern, raw input schema. |
| Try It | Dynamic form generated from the tool's input schema. Submits a real `tools/call` and renders the result with optional enrichment blocks. Per-session history with replay. |
| Activity | 24-hour aggregate from the audit log: call count, success rate, average duration. Links to `/admin/audit?tool=<name>`. |
| Enrichment | Gateway-proxied tools only. Lists [cross-enrichment](../cross-enrichment/overview.md) rules attached to this tool, with merge strategy and enabled state. Links to the connection's enrichment drawer. |
| Visibility | Toggle the global kill-switch (see [`tools.deny`](#global-kill-switch-toolsdeny) below) and preview a persona's decision for this tool without editing persona rules. |

### Description overrides

A tool's description is what an LLM agent sees in `tools/list`. Overriding it is the most reliable way to steer agent behavior — for example, to insist that `trino_query` calls `datahub_search` first to discover the table.

Overrides are persisted as config entries with the key `tool.<name>.description`. Resolution order, last wins:

1. **Built-in defaults** in `pkg/middleware/mcp_descriptions.go` — currently `trino_query` and `trino_execute` redirect agents through DataHub discovery.
2. **File-config overrides** in `tools.description_overrides` of `platform.yaml`.
3. **Database overrides** authored from the portal Tools page, stored in the `config_entries` table.

The Overview tab shows an `overridden` badge with the author when a database override is in effect. The "Reset" button removes the database override; the file-config or built-in default takes over. Overrides are picked up at platform startup — saving from the portal updates the live config struct immediately, but the `tools/list` response continues to serve the previously-cached description until restart.

### Global kill-switch (`tools.deny`)

`tools.deny` is a glob list that hides matching tools from `tools/list` responses for **all clients**. It is a cosmetic / token-budget filter, not a security boundary — persona authorization continues to gate `tools/call` independently.

Three equivalent ways to set it:

- Edit `tools.deny` in `platform.yaml` (file mode).
- `PUT /api/v1/admin/config/entries/tools.deny` with a JSON-encoded string array as `value`.
- Click "Hide tool" on the Visibility tab. The portal does a read-modify-write of the `tools.deny` config entry, appending the literal tool name.

When a deny pattern is a glob (e.g. `*_admin_*`) rather than a literal name, the Visibility tab will surface a warning that toggling here only changes the literal entry — the glob must be edited via Config.

### Admin API surface

| Endpoint | Use |
| --- | --- |
| `GET /api/v1/admin/tools` | Inventory of every registered tool with kind / connection. |
| `GET /api/v1/admin/tools/schemas` | Bulk fetch input schemas. |
| `GET /api/v1/admin/tools/{name}` | Aggregating per-tool detail used by the master-detail page. |
| `POST /api/v1/admin/tools/call` | Invoke a tool with parameters; returns the same content envelope clients see. |
| `PUT /api/v1/admin/tools/{name}/visibility` | Add/remove the tool from `tools.deny` (read-modify-write under the hood). |
| `POST /api/v1/admin/personas/{name}/test-access` | Preview a persona's allow/deny decision for one tool. |
| `PUT /api/v1/admin/config/entries/tool.<name>.description` | Save a per-tool description override. Only accepted for keys whose `<name>` matches a registered tool. |
| `DELETE /api/v1/admin/config/entries/tool.<name>.description` | Remove an override and revert to the file or built-in default. |

See [Admin API](admin-api.md) for full request/response shapes.

---

## Platform Tools

### platform_find_tools

`platform_find_tools(query, limit)` ranks the platform's own registered tools by semantic similarity to a natural-language task description, so an agent can discover the right tools by intent instead of scanning every tool name. It is the tool-catalog analogue of `api_list_endpoints`' semantic ranking.

- **Indexing** — every globally-visible tool's descriptor (name, description, and a parameter-schema summary) is embedded through the shared index-jobs framework (`source_kind = "tools"`) and persisted to the `tool_embeddings` table. On each reconcile sweep the tools gap check diffs the live registry against the persisted vectors by descriptor text hash, so a tool addition, removal, description-override edit, or visibility flip is picked up within one interval, while a steady-state corpus produces no job and the index settles rather than re-running every sweep. When a job does run, the worker's text-hash dedup re-embeds only the descriptors that actually changed. Embeddings are persona-neutral (indexed once for the whole catalog).
- **Ranking** — the query is embedded and ranked against the stored vectors with pgvector cosine distance. When no embedding provider is configured or the index is empty, it falls back to a lexical name/description match and sets a `note` explaining why (the same UX as `api_list_endpoints`).
- **Persona scoping** — results are filtered at read time to the tools the caller's persona is permitted to call, exactly like `tools/list`. The model never sees a tool it cannot call. (Row-level filtering, not per-persona embeddings.)
- **Response** — `{ "tools": [ { "name", "description", "score" } ], "note"? }`, ranked most-relevant first and capped at `limit` (default 10, max 50).

This is discovery, not routing: the agent still chooses which returned tool to call.

---

## Next Steps

- [Multi-Provider](multi-provider.md) - Use multiple connections
- [Cross-Enrichment](../cross-enrichment/overview.md) - Understand semantic enrichment
- [Tools API Reference](../reference/tools-api.md) - Complete API specification

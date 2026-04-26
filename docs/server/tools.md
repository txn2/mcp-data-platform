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
| DataHub | `datahub_search` | Search for datasets, dashboards, etc. |
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
| Knowledge | `capture_insight` | Record domain knowledge |
| Knowledge | `apply_knowledge` | Review and apply insights to catalog (admin-only) |
| Memory | `memory_manage` | Create, update, forget, list memories (opt-in per persona) |
| Memory | `memory_recall` | Multi-strategy memory retrieval (entity, semantic, graph, auto) |
| Portal | `save_artifact` | Save an AI-generated artifact (JSX, HTML, SVG, etc.) |
| Portal | `manage_artifact` | List, get, update, or delete saved artifacts |

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

### datahub_search

Search for entities in the DataHub catalog.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | Search query |
| `type` | string | No | - | Entity type filter (dataset, dashboard, etc.) |
| `platform` | string | No | - | Platform filter (trino, snowflake, etc.) |
| `limit` | integer | No | 10 | Maximum results |
| `connection` | string | No | default | Connection name to use |

**Response includes:**

- Matching entities with URN, name, description
- **Query context** (if enabled): whether each dataset is queryable via Trino, sample SQL

---

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

### capture_insight

Record domain knowledge shared during a session. Available to all personas when `knowledge.enabled: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `category` | string | Yes | - | correction, business_context, data_quality, usage_guidance, relationship, enhancement |
| `insight_text` | string | Yes | - | Knowledge to record (10-4000 chars) |
| `confidence` | string | No | medium | high, medium, low |
| `source` | string | No | user | user, agent_discovery, enrichment_gap |
| `entity_urns` | array | No | [] | Related DataHub entity URNs (max 10) |
| `related_columns` | array | No | [] | Related columns (max 20) |
| `suggested_actions` | array | No | [] | Proposed catalog changes (max 5). Action types: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation, add_curated_query, set_structured_property, remove_structured_property, raise_incident, resolve_incident, add_context_document, update_context_document, remove_context_document |

---

### apply_knowledge

Review, synthesize, and apply captured insights to the data catalog. Admin-only. Requires `knowledge.apply.enabled: true`.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `action` | string | Yes | bulk_review, review, synthesize, apply, approve, reject |
| `entity_urn` | string | Conditional | Required for review, synthesize, apply |
| `insight_ids` | array | Conditional | Required for approve, reject |
| `changes` | array | Conditional | Required for apply |
| `confirm` | bool | No | Required when `require_confirmation` is true |
| `review_notes` | string | No | Notes for approve/reject actions |

**Actions:**

- **bulk_review**: Summary of all pending insights grouped by entity
- **review**: Insights for a specific entity with current DataHub metadata
- **approve/reject**: Transition insight status with optional notes
- **synthesize**: Structured change proposals from approved insights
- **apply**: Write changes to DataHub with changeset tracking

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

Manages persistent agent/analyst memory. Opt-in per persona (requires `memory_*` in `tools.allow`). Requires `memory.enabled: true`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `command` | string | No | Operation: `remember`, `update`, `forget`, `list`, `review_stale`. Omit for help. |
| `content` | string | For `remember` | Memory content (10-4000 chars) |
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

### memory_recall

Multi-strategy memory retrieval. Use when cross-enrichment does not surface the context you need. Opt-in per persona.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | Yes | Natural language query for semantic search |
| `strategy` | string | No | `entity`, `semantic`, `graph`, `auto` (default: `auto`) |
| `entity_urns` | string[] | No | DataHub URNs for `entity` and `graph` strategies |
| `dimension` | string | No | Filter by LOCOMO dimension |
| `include_stale` | bool | No | Include stale memories (default: false) |
| `limit` | int | No | Max results (default 10, max 50) |

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
| `action` | string | Yes | - | Action to perform: list, get, update, delete |
| `asset_id` | string | Conditional | - | Required for get, update, delete |
| `content` | string | No | - | New content (for update — replaces S3 object) |
| `name` | string | No | - | New name (for update) |
| `description` | string | No | - | New description (for update) |
| `tags` | array | No | - | New tags (for update) |
| `content_type` | string | No | - | New content type (for update, only when replacing content) |
| `limit` | integer | No | 50 | Max results for list action (max 200) |

**Actions:**

- **list**: Show the current user's artifacts with metadata
- **get**: Retrieve full asset metadata by ID
- **update**: Change name, description, tags, or replace content
- **delete**: Soft-delete an artifact

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

## Next Steps

- [Multi-Provider](multi-provider.md) - Use multiple connections
- [Cross-Enrichment](../cross-enrichment/overview.md) - Understand semantic enrichment
- [Tools API Reference](../reference/tools-api.md) - Complete API specification

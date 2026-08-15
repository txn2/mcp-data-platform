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
| Knowledge | `search` | The one way to discover: balanced, grouped-by-source results across the catalog, context documents, knowledge pages, memory, insights, feedback, assets, prompts, managed scripts, API endpoints, and connections |
| Knowledge | `fetch` | Read a search result in full: dereferences any reference search emits (knowledge page, context document, dataset, asset, prompt, managed script, connection) to its complete content, under the same per-user scope |
| Memory | `memory_capture` | The one way to record knowledge: sink-class routed, recall-first |
| Knowledge | `apply_knowledge` | Review and promote reviewed captures to the catalog (admin-only) |
| Memory | `memory_manage` | Manage existing memories: update, forget, list, review_stale, review_duplicates, consolidate (opt-in per persona) |
| Portal | `save_asset` | Save AI-generated content as an asset (JSX, HTML, SVG, etc.) |
| Portal | `manage_asset` | List, get, update, delete, or relevance-search saved assets and collections, and edit asset content in place (patch, locate, get_content, outline, stats, diff) |
| Portal | `manage_feedback` | Review and respond to human feedback (list pending across everything, get, reply, resolve, request/respond validation) |
| Platform | `platform_find_tools` | Find the most relevant tools for a natural-language task, ranked by semantic similarity (persona-scoped) |
| Platform | `manage_prompt` | Resolve and run prompts by any handle (`use`), plus create, update, delete, list, get, the script-reference commands (attach_script, detach_script), and the content verbs (patch, locate, get_content, outline, stats, diff) |
| Platform | `show_prompts` | Render the prompt library as an interactive browser for the human (presentation-only; call only when the user wants to see their prompts) |
| Platform | `manage_script` | Author, validate, and dry-run managed scripts: small governed Starlark programs for a process whose logic is settled and will repeat |

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

`read_only` is set per instance, and the block applies to the connection the call names — or to the default connection when it names none. A call routed to an instance with `read_only: true` is refused; the other instances of the same toolkit still accept writes.

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
| `description` | — | Set entity description from `value` |
| `column_description` | — | Set schema field description from `value` |
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

Capture is **recall-first**: before writing, it runs a similarity check over the caller's own memory (superseded rows excluded; stale rows stay matchable, since a restatement corrects them). Every near-duplicate at or above the supersede threshold (0.9 cosine) is superseded by the new capture (`superseded` in the response carries the best match id, `superseded_ids` the complete list); matches in the 0.75-0.9 band are returned as `similar_existing` candidates (id + score) so the agent can consolidate instead of creating a near-duplicate. `schema_entity` carries `entity_urns` and optional `suggested_actions` (the catalog-change payload `apply_knowledge` later applies).

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
when configured), the governance vocabulary (DataHub glossary terms, tags, and
domains), context documents, canonical knowledge pages (the internal-knowledge home for
business/domain ontology, searched over their full markdown content), the caller's
personal memory, captured insights, the caller's feedback threads, saved assets,
managed resources (human-uploaded reference material, searched over their metadata
**and their extracted file content**), prompts, managed scripts, API endpoints (aggregated across
every API gateway connection, reusing
the per-connection semantic ranking of `api_list_endpoints`), and connections. Memory, insights, and
assets are per-user, scoped server-side to the caller, so a search never surfaces
another user's private records; the catalog, the governance vocabulary, knowledge
pages, prompts, scripts, endpoints
(each gateway applies its own route policy), and connections are shared. Resources
and scripts are visibility-scoped: global material reaches every caller, persona
material only its members, and user (personal) material only its owner, exactly as
`resources/list` and the script listing compute it. Persona visibility is **membership** (derived from the caller's roles), not the
persona the request resolved to — resolution falls back to the configured default
persona for a caller whose roles match none, and that fallback must not hand them
the default persona's material.
A caller with no identity still sees shared sources but no per-user data. API
endpoints and connections are in the default corpus, not behind an opt-in.

**Connection boundary.** The three topology sources (catalog, connections,
endpoints) are additionally narrowed to the connections the caller's persona is
granted by its `connections.allow` rules — the same predicate that authorizes a
tool call, so what a caller can find and what a caller can call never disagree.
A catalog dataset is attributed to a connection through its DataHub platform
name; a dataset that maps to no configured connection stays visible, and one
reachable through any granted connection stays visible. Removals are reported,
never silent: each `coverage` entry carries a `withheld` count and the response
carries a `withheld_notice` naming the persona and the remedy, so a shortened
result set reads as "present, but not yours to see" rather than "does not
exist". See [Persona Connection Access](../personas/overview.md#connection-access-control).

**Catalog descriptions are semantically searchable.** The catalog source ranks
two ways. The platform keeps its own index of every catalog dataset's text (name,
description, tags, domain) and ranks the query against it first; DataHub's own
keyword search follows as the recall tail, covering the fields the index does not
carry (column names, glossary terms, ownership). This is what makes a fact written
into a dataset's description — by `apply_knowledge`, or by a steward in DataHub —
reachable from a topical query that shares none of its words and names none of its
entities. A dataset both find is shown once. The index is refreshed on a schedule
(`knowledge.catalog_index.sync_interval`, default 30m) by a background index job
and appears on the admin Indexing dashboard as the `catalog-datasets` kind; it
holds a discardable copy of catalog text, so every hit is still dereferenced
against DataHub itself. It needs a DataHub semantic provider, a database, and an
embedding provider; without them the catalog is ranked by DataHub's keyword search
alone, exactly as before.

**The governance vocabulary is a source, not a dataset attribute.** A glossary
term, a tag, and a domain are searchable and fetchable entities in their own
right (`#1160`). Before this, they existed only as attributes of a dataset:
asking what a business term meant returned the datasets tagged with it, never the
term and its definition, and a `urn:li:glossaryTerm:` reference a knowledge page
legitimately carries had no `fetch` owner. The `governance` source answers "what
does X mean here" and the `catalog` source answers "which datasets are about X";
they are siblings so a definition is never crowded out of the display budget by a
broad dataset match. A hit carries the definition in its text, so a term match is
useful without a follow-up `fetch`.

Each vocabulary is ranked by the best read DataHub offers for it, which is not
the same read for all three. Glossary terms and tags have an upstream name
search, which receives the intent and leads because it ranks against DataHub's
own index and is not bounded by an enumeration page. When it returns nothing the
vocabulary is enumerated and ranked locally instead, because `search` receives a
natural-language intent while those upstream searches match a *name*: "what does
net revenue mean here" is a question, not a label, and a source that answered it
only when the caller typed the term exactly would not be answering it. Domains
have no upstream search at all, so the whole set is enumerated (DataHub returns
at most 100) and ranked the same way — by the lexical token-overlap rule the
`connections` source uses for the same reason. The three reads run concurrently,
so the source costs one round trip rather than three, and a vocabulary that fails
costs its own recall rather than the whole source. Results are interleaved across
the three so one vocabulary cannot crowd out the others.

**Connection boundary for governance entities.** A governance URN carries no
DataHub platform segment, so no connection can be attributed to it. The documented
rule for an unattributable URN applies: it stays visible, because the mapping
failed rather than the permission check, and hiding on a guess would drop entities
no connection claims. The datasets listed under a tag, domain, or term by `fetch`
are ordinary catalog entities and *are* filtered by the caller's boundary, with
the removed count and the reason carried on the fetched entity.

**Resource links.** A hit backed by an uploaded file additionally carries an MCP
`resource_link` content block with the resource's canonical `mcp://` URI, name,
description, and MIME type, so a client with native resource support can attach the
file itself rather than only the pointer the model dereferences.

**Balanced result set.** Rather than one flat relevance list (which lets one
strong source dominate), the display set is built from a total budget with a
per-source floor (so every matching source stays visible), a per-source ceiling
(so none runs away), and redistribution of unused budget to the sources with more
relevant hits. Every response also carries a `coverage` summary of per-source
`matched` vs `shown` counts, so the agent learns where the answer space lives even
when only the top few of each source are displayed. Hits are navigational
snippets (title, `ref`, `reference`, short context line, `source`); the agent reads
the full content with [`fetch`](#fetch) (any source) or drills in with a scoped tool
(`trino_query`, `api_invoke_endpoint`).

A query may be text (`intent`), entity-keyed (`entity_urns`, returning every
source linked to those datasets and their lineage neighbors: the catalog entity,
URN-linked insights, and your URN-linked memory), or both. Ranking is
hybrid (semantic vector + lexical) when an embedding provider is configured and
lexical-only otherwise; an entity-only query reports ranking `entity`. The
response carries a `ranking` field, a `count` (total hits shown), a `groups`
array (each `{source, hits[]}` where every hit pairs the matched `text` with its
`source`, a `ref`, a relevance `score`, and where present `status`, `entity_urns`,
and `dimension`), and a `coverage` array (`{source, matched, shown, withheld}`,
where `withheld` is present only when the persona connection boundary removed
matches, alongside a top-level `withheld_notice`).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `intent` | string | Conditional | - | Natural-language description of what you are looking for. Provide `intent`, `entity_urns`, or both |
| `context` | string | No | - | Optional surrounding context, folded into the intent to sharpen relevance |
| `entity_urns` | array | Conditional | - | Exact entity-keyed lookup: everything linked to these DataHub URNs (the catalog entity, insights about it, and your memory linked to it), expanded along lineage |
| `status` | string | No | - | Optional filter by insight review status (pending, approved, rejected, applied, superseded, rolled_back) |
| `sources` | array | No | - | Narrow the search to named sources (`catalog`, `governance`, `context_documents`, `knowledge_pages`, `memory`, `insights`, `feedback`, `assets`, `resources`, `prompts`, `endpoints`, `connections`). Only narrows; never opts into a source the persona could not otherwise access. An unrecognized name is echoed back in the response `unknown_sources` rather than silently ignored |
| `limit` | integer | No | 10 | Total results to display across all sources (max 50) |

---

### search browse mode (enumeration)

`search` is relevance-ranked and floors/caps each source, so it cannot list a
source in full. Browse mode (#695) is the exhaustive counterpart: it pages the
complete set of one source with a total count and **no relevance threshold**, so an
agent can audit, dedup, govern, or migrate a corpus it must first obtain in full.
It is the same `search` tool, not a new one.

A call enters browse mode when it carries **exactly one `sources` entry, no
`intent`, and no `entity_urns`**; pass `offset` to page. Browsable sources:
`knowledge_pages` and `context_documents` (the two tiers that had no enumeration on
the MCP surface). Browsing more than one source at once, a non-browsable source, or
an unknown source is a tool error that names what can be browsed.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `sources` | array | Yes | - | Exactly one source to enumerate (`knowledge_pages` or `context_documents`) |
| `offset` | integer | No | 0 | 0-based start of the page |
| `limit` | integer | No | 50 | Page size (max 100) |

The response is a flat, unranked page: `{source, total, offset, limit, count,
items[]}`, where `total` is the source's full member count (so the agent knows how
many pages remain) and each item carries the same `reference` that `fetch` reads in
full. Context-document enumeration includes every document (drafts and hidden ones),
so the page and `total` describe the same complete set. Scope mirrors `search`: the
two browsable sources are org-global, so any caller may enumerate them; a per-user
source is never browsable for an anonymous caller.

---

### fetch

The companion read verb to [`search`](#search). `search` returns navigational
pointers with truncated snippets; `fetch` dereferences one pointer's `reference`
back to its **complete content**, so the agent reads in full what it found. It is
the single consumer of the `reference` every search hit already carries, and it
collapses the previously fragmented scoped readers (`datahub_get_entity`,
`manage_asset` get, `manage_prompt` get) into one verb. Registered alongside
`search`.

A reference comes in one of two namespaces: `urn:li:...` is the external DataHub
catalog scheme, `mcp:...` is the internal-platform scheme. `fetch` accepts both,
routing each well-formed reference by its form to the owning source:

| Reference form | Source | Returns |
|----------------|--------|---------|
| `mcp:knowledge_page:<id>` | knowledge pages | the full markdown body |
| `urn:li:document:<id>` | context documents | the full document body (the only MCP path to it) |
| `urn:li:dataset:<id>` | catalog | the dataset's catalog context |
| `urn:li:glossaryTerm:<id>` | governance | the term's name and definition, plus the datasets that carry it |
| `urn:li:tag:<id>` | governance | the tag's name and description, plus the datasets that carry it |
| `urn:li:domain:<id>` | governance | the domain's name and description, plus the datasets in it |
| `mcp:asset:<id>` | assets | the asset's metadata record (blob bytes stay in S3, reached with `s3_get_object`/`s3_presign_url`) |
| `mcp:resource:<id>` | resources | the resource's metadata record, plus its contents inline for a text resource at or under 1 MB; a binary or oversized one returns metadata with its canonical `mcp://` URI, MIME type, and size |
| `mcp:prompt:<id>` | prompts | the full prompt |
| `mcp:script:<id>` | scripts | the managed script's contract: name, description, owner, typed parameters, approval state, schedule, and the last successful run with what it produced. Never the source code (fetch-only, not citable on a page) |
| `mcp:connection:(kind,name)` | connections | the connection descriptor |
| `mcp:insight:<id>` | insights | the full captured insight (scoped to the caller; fetch-only, not citable on a page) |
| `mcp:memory:<id>` | memory | the full personal memory record (scoped to the caller; fetch-only, not citable on a page) |

The usual source of a reference is a `search` result's `reference` field, but
`fetch` is not limited to references `search` produced: a well-formed reference
held from another tool works too (for example a `urn:li:dataset:...` from
`datahub_get_lineage` or an `entity_urns` lookup). Feedback threads and API
endpoints emit no reference and are not fetch targets.

A fetched governance entity fills `content` with `{urn, kind, name, description,
datasets[], more_datasets?, datasets_withheld?, notice?}`. The carrier list is
bounded (25); `more_datasets` reports that the list is not known to hold every
carrier -- the catalog counted more, or it could not count at all -- so a bounded
list never reads as the whole membership — page the full set with `datahub_browse` or a
tag/domain-filtered catalog search. `datasets_withheld` and `notice` report what
the caller's connection boundary removed from the list, so a short list is never
mistaken for an unused term. A tag or a domain has no by-URN read upstream, so it
is resolved by listing its vocabulary and matching; an entry past the page DataHub
returns is reported as a clean not-found, the same bound the portal's reference
labels carry.

**Scope mirrors `search` exactly:** the per-user sources (assets, your memory, your
insights) are read only for the identity that owns the record, a
persona/personal-scoped prompt only for the matching caller, and a managed resource
or managed script only for a caller whose visible scopes include it, so `fetch` never
returns content the same caller could not have
found with `search`. The connection boundary applies here too: a dataset URN or
connection reference belonging to a connection the caller's persona is not granted
is reported as not-found, so a citation cannot read around what `search` omitted.
A reference outside the caller's scope is reported as
not-found, indistinguishable from a missing one, so existence does not leak.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `reference` | string | Yes | - | A search result's `reference`, passed exactly as `search` returned it |

The response is `{found, reference, document?, message?}`. A resolved reference
returns `found: true` with a `document` (`{reference, source, title, body?,
content?, entity_urns?}`, where text-bodied sources fill `body` and structured
sources fill `content` with the source-native payload). A stale, unknown, or
out-of-scope reference returns `found: false` with an explanatory `message`, a
**structured not-found, not a tool error**, so a dangling citation is a normal
answer. A malformed call (empty `reference`) and a real backend failure are tool
errors.

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
| GET | `/knowledge-pages/graph` | any user | The corpus as typed nodes and edges for the portal's graph view (filter by `tag`, `limit`) |
| POST | `/knowledge-pages` | apply_knowledge | Create a page |
| PUT | `/knowledge-pages/{id}` | apply_knowledge | Edit a page (snapshots a new version) |
| DELETE | `/knowledge-pages/{id}` | apply_knowledge | Soft-delete a page |

Embeddings are produced off the request path by the shared `indexjobs` queue
(`source_kind=portal-knowledge-pages`). Creating a page, and editing one in a way
that moves its indexed text, enqueues that page's own index job at write time
(trigger `write`), so it enters ranked `search` in roughly the time one embed
takes rather than on the next reconciler sweep; the edit also clears the page's
vector in the same transaction, so it never ranks semantically on its old
content. The reconciler remains the backstop for what a write could not produce:
a page saved or edited while the embedding provider was unavailable, a provider
model swap, and chunk rows lost outside the write path (a database restore, or a
manual prune).

The graph read returns the pages plus every entity their references point at, as
one access-filtered response, so a client draws the whole corpus without an N+1
sweep of the per-page refs endpoint. An entity the viewer cannot access is absent
from it entirely — neither node nor edge — the same visibility rule the per-page
refs and backlinks reads apply. Both the page window and the total node count are
capped, and either cap is reported in the response (`truncated` plus a
human-readable `notice`) rather than applied silently.

---

### apply_knowledge

Review, synthesize, and apply captured insights to their canonical home. Admin-only. Requires `knowledge.apply.enabled: true`.

`apply_knowledge` is the **sink router** (#633): the `apply` action's `sink` decides where a capture is promoted.

- **`sink: datahub`** (default) applies the `changes` to a catalog entity (`entity_urn`).
- **`sink: knowledge_page`** promotes a capture to a canonical portal **knowledge page**, found-or-created by `page.slug` (so repeated promotions on the same slug consolidate into one living page). The capture-time sink-class is a non-binding hint: any insight can be promoted to either sink, with the destination chosen at apply (prefer DataHub for entity-anchored facts, a page for broader business or domain knowledge).

Both sinks record a **changeset** (page promotions use `target_urn = "kp:<slug>"`) listed by `list_changesets` and reversible by `rollback`. Rolling back a page promotion soft-deletes a newly created page or restores a prior version, and is refused if the page was edited after the promotion.

**Without a DataHub connection**, `sink: datahub` is refused rather than silently accepted: the apply returns an error naming the blocked change types, `knowledge.apply.datahub_connection`, and `sink: knowledge_page` as the catalog-free destination, and records no changeset. `rollback` of a DataHub changeset is refused the same way. The one exception is the `add_prompt` change type, which creates a platform prompt rather than writing to the catalog and therefore still applies. The knowledge-page sink is unaffected throughout.

**Citing entities on a page.** To attach an entity reference (a dataset `urn:li:...`, or an `mcp:asset`/`mcp:prompt`/`mcp:collection`/`mcp:connection`/`mcp:knowledge_page`) to a page, pass it in `page.references` or write it in the body as plain text or a markdown link. A reference wrapped in backticks or a fenced code block is treated as a documentation example and intentionally ignored, so a backticked URN produces no reference and no link. Each entry in `page.references` is existence-checked **before** the page is written: a missing **internal** (`mcp:`) entity rejects the apply (a DataHub `urn:li:` reference is free text and is stored as given). References in `page.references` and those carried from the source insights attach with the promotion (so a `rollback` undoes them); a stale insight-carried reference is skipped rather than blocking. A target cited both in `page.references` and inline in the body is stored once. Inline body references are also filtered to those that exist, so a stale `mcp:` token in prose is skipped rather than blocking the page or leaving it partially written. A dropped insight-carried or inline-body reference is not silent: the apply response reports the dropped targets in `references_dropped` (a reference whose target was deleted, or an insight-carried reference that is not citable on a shared page such as `mcp:memory:`/`mcp:insight:`) and always reports the count of references that landed in `references_attached`, so an agent can reconcile what it cited against what was attached and fix the payload or the prose.

`operational_rule` is stored as a knowledge page like `business_knowledge` (it is non-DataHub canonical knowledge); active enforcement of operational rules via the rules engine is tracked separately.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `action` | string | Yes | bulk_review, review, synthesize, apply, approve, reject, rollback, list_changesets, bulk_untag |
| `sink` | string | No | apply target: `datahub` (default) or `knowledge_page` |
| `entity_urn` | string | Conditional | Required for review, synthesize, list_changesets, and apply with `sink=datahub` |
| `tag_urn` | string | Conditional | Required for `bulk_untag`; the tag (name or `urn:li:tag:...`) to remove from every entity that carries it |
| `page` | object | Conditional | `{slug, title, body, summary?, tags?, references?}` for apply with `sink=knowledge_page`. `references` is a list of serialized reference strings (`mcp:<type>:<id>` / `urn:li:...`) attached to the page independent of the body |
| `insight_ids` | array | Conditional | Source insights; required for approve, reject. On apply, pass the promoted insights so they are marked applied and the changeset is linked to them (closes the review loop; the queue then reflects what is live). Sink-class is a non-binding hint; any insight can be applied to either sink (destination chosen at apply) |
| `changes` | array | Conditional | Required for apply with `sink=datahub` |
| `changeset_id` | string | Conditional | Required for rollback |
| `confirm` | bool | No | Required when `require_confirmation` is true (apply and rollback) |
| `review_notes` | string | No | Notes for approve/reject actions |
| `itemize` | bool | No | With `bulk_review`, also return the pending insights themselves (full `insight_text` body, `captured_by`, `sink_class`, `created_at`, `suggested_actions_count`, etc.; full `suggested_actions` omitted, `fetch` for it), paginated by `offset`/`limit`. The response is bounded (`page_size_capped`/`by_entity_truncated` flag any cut) so it stays under the output limit |
| `limit` | int | No | Page size for itemized `bulk_review` (default 20, max 100) |
| `offset` | int | No | Page start for itemized `bulk_review`; pass the previous `next_offset` to continue |

**Actions:**

- **bulk_review**: Counts of all pending insights (`total_pending`, `by_entity`, `by_category`, `by_confidence`) plus a review-queue staleness rollup (`oldest_pending_at`, `oldest_pending_age_days`, `pending_over_30d`, omitted when the queue is empty) so aging review debt is visible. Pass `itemize: true` to enumerate the queue itself, paginated, with each insight's full `insight_text` body, `id`, `captured_by`, `sink_class`, and `suggested_actions_count` (full `suggested_actions` omitted, `fetch` for it; the relevance-ranked `search` tool cannot list the queue completely). The response is bounded so it stays under the output limit: `page_size_capped: true` flags a short insights page (continue with `next_offset`) and `by_entity_truncated: true` flags a capped `by_entity`
- **review**: Insights for a specific entity with current DataHub metadata
- **approve/reject**: Transition insight status with optional notes
- **synthesize**: Structured change proposals from approved insights
- **apply**: Write changes to DataHub with changeset tracking
- **list_changesets**: List an entity's changesets (id, timestamp, actor, change type, rollback status)
- **rollback**: Revert a changeset's changes to their before-image and transition its source insights to `rolled_back` (requires `changeset_id` and `confirm`)
- **bulk_untag**: Remove a tag (`tag_urn`) from every entity a catalog search finds carrying it, recording one changeset for audit (when `require_confirmation` is enabled it first returns the affected count and needs `confirm`; not auto-revertible, re-apply `add_tag` to restore)

**Supported change types for `apply` action:**

| Change Type | Target | Detail | Entity Types |
|-------------|--------|--------|--------------|
| `update_description` | `column:<fieldPath>` for column-level, empty for entity-level | Description text | datasets (column+entity), dashboards, charts, dataFlows, dataJobs, containers, dataProducts, domains, glossaryTerms, glossaryNodes, tags (set `entity_urn` to the tag URN to fix a tag's own definition) |
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
| `delete_tag` | Ignored | Ignored | Tags (`entity_urn` is the tag URN); deletes the tag definition entirely, irreversible |
| `set_custom_property` | customProperties key | Value | datasets, dashboards, charts, dataFlows, dataJobs, containers, dataProducts, domains, glossaryTerms, glossaryNodes |
| `remove_custom_property` | customProperties key | Ignored | datasets, dashboards, charts, dataFlows, dataJobs, containers, dataProducts, domains, glossaryTerms, glossaryNodes |

`delete_tag`, `set_custom_property`, and `remove_custom_property` are recorded for audit but are not auto-revertible. Custom-property changes are batched, so a single apply may not both set and remove custom properties on the same entity (the shared aspect is written non-atomically); use separate apply calls. For `add_curated_query`, `query_sql` (required) and `query_description` (optional) provide the SQL statement. For `add_context_document` and `update_context_document`, `query_description` is the document category.

---

## Memory Tools

!!! tip "Full documentation"
    For the complete memory layer documentation including architecture, staleness detection, and cross-enrichment, see [Memory Layer](../memory/overview.md).

### memory_manage

Manages the lifecycle of existing persistent memory. Create new memory with `memory_capture`. Opt-in per persona (requires `memory_*` in `tools.allow`). Requires `memory.enabled: true`.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `command` | string | No | Operation: `update`, `forget`, `list`, `review_stale`, `review_duplicates`, `consolidate`. Omit for help. (Create with `memory_capture`.) |
| `id` | string | For `update`, `forget`, `consolidate` | Memory record ID (for `consolidate`: the record to keep) |
| `duplicate_id` | string | For `consolidate` | The duplicate record the kept record supersedes |
| `dimension` | string | No | LOCOMO dimension: `knowledge`, `event`, `entity`, `relationship`, `preference` |
| `category` | string | No | Category: `correction`, `business_context`, `data_quality`, `usage_guidance`, `relationship`, `enhancement`, `general` |
| `confidence` | string | No | `high`, `medium`, `low` (default: `medium`) |
| `source` | string | No | `user`, `agent_discovery`, `enrichment_gap`, `automation`, `lineage_event` |
| `entity_urns` | string[] | No | DataHub entity URNs this memory relates to (max 10) |
| `metadata` | object | No | Arbitrary metadata (e.g., `suggested_actions`, `superseded_by`) |
| `filter_*` | string | No | Filters for `list`: `filter_dimension`, `filter_category`, `filter_status`, `filter_entity_urn` |
| `limit` | int | No | Page size for `list` (default 20, max 100); also caps `review_duplicates` pairs (may hold fewer when the byte budget hits, `more_pairs=true`) |
| `offset` | int | No | Pagination offset for `list` (not used by `review_duplicates`) |

!!! note "Memory recall moved to `search`"
    Reading memory back (relevance, entity lookup, and lineage/graph traversal)
    is now part of the universal [`search`](#search) tool. The
    memory toolkit retains `memory_manage` for the write path.

---

## Portal Tools

The portal toolkit persists AI-generated assets (JSX dashboards, HTML reports, SVG charts) to S3 with PostgreSQL metadata, enabling viewing and sharing. Automatically captures provenance (which tool calls produced the asset).

!!! tip "Prerequisites"
    Portal tools require `portal.enabled: true`, a configured S3 connection (`portal.s3_connection`), and `database.dsn`. See [Configuration](configuration.md#portal-configuration).

### save_asset

Save AI-generated content to the asset portal as a versioned asset. Automatically captures provenance tracking which tool calls in the session led to this asset.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `name` | string | Yes | - | Display name for the asset (max 255 chars) |
| `content` | string | Yes | - | The asset content (JSX, HTML, SVG, Markdown, etc.) |
| `content_type` | string | Yes | - | MIME type the asset is stored under. One of `application/json`, `application/octet-stream`, `application/sql`, `application/x-ndjson`, `application/xml`, `application/yaml`, `image/svg+xml`, `text/css`, `text/csv`, `text/html`, `text/javascript`, `text/jsx`, `text/markdown`, `text/plain`, `text/tab-separated-values`, `text/x-python`; anything else is refused (see [Accepted types](content-viewers.md#accepted-types)) |
| `description` | string | No | - | Description of the asset (max 2000 chars) |
| `tags` | array | No | [] | Tags for categorization (max 20 tags, each max 100 chars) |

**Response includes:**

- Asset ID for future reference
- Portal URL for viewing (if `public_base_url` is configured)
- Provenance capture status and tool call count

---

### manage_asset

List, retrieve, update, or delete saved assets, and edit an asset's content in place. All mutations enforce ownership (users can only modify their own assets); the read-only content verbs additionally accept a share grant, so an asset shared with you can be read but not patched.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | Action to perform: list, get, update, delete, search, patch, locate, get_content, outline, stats, diff |
| `asset_id` | string | Conditional | - | Required for get, update, delete, and every content action |
| `content` | string | No | - | New content (for update; replaces the whole body) |
| `name` | string | No | - | New name (for update) |
| `description` | string | No | - | New description (for update) |
| `tags` | array | No | - | New tags (for update) |
| `content_type` | string | No | - | New content type (for update, only when replacing content). Same accepted set as `save_asset`; omit it to keep the type the asset already carries |
| `change_summary` | string | No | - | Summary recorded on the new version (update and patch) |
| `query` | string | Conditional | - | Free-text relevance query (required for search) |
| `limit` | integer | No | 50 | Max results for list (max 200); ranked search defaults to 20 (max 100) |

The patch and navigation arguments (`edits`, `base_version`, `dry_run`, `find`, `pattern`, `section`, `selector`, `occurrence`, `line_start`, `line_end`, `context_bytes`, `from_version`, `to_version`) are the shared content-editing grammar documented in [Editing content in place](#editing-content-in-place). Inside `edits`, `occurrence` is a per-edit field; at the top level it disambiguates a `selector` used to scope `locate` or `get_content`.

**Actions:**

- **list**: Show the current user's assets with metadata
- **get**: Retrieve full asset metadata by ID (the metadata row, not the body)
- **update**: Change name, description, tags, or replace the whole content
- **delete**: Soft-delete an asset
- **search**: Rank the caller's own assets by relevance to `query`. Uses the same hybrid (vector + lexical) ranking as the prompt and Knowledge & Memory search: weighted hybrid when an embedding provider is configured, automatic lexical-only fallback otherwise. Returns each match with a `score` and reports `ranking` (`hybrid` or `lexical`). Scoped server-side to the caller's own assets by `owner_id`, the same ownership key the asset library and update/delete checks use, so search returns exactly what you see in the library, and fails closed when the caller has no identity, so a user can never find an asset they cannot view.
- **patch / locate / get_content / outline / stats / diff**: read and edit the body without moving the whole document. See below.

A patch writes an ordinary new version, so `list_versions` and `revert` keep working, and the version's change summary is the caller's `change_summary` (or a generated "3 edits via patch") instead of a fixed constant.

---

### Editing content in place

Regenerating a whole document to change one sentence costs output tokens proportional to the size of the document rather than the size of the change, and every regeneration is a chance to silently drop an unrelated paragraph. `manage_asset` and `manage_prompt` therefore share one content-editing grammar, implemented once in `pkg/textpatch`: the same argument names, the same operations, and the same error codes on both tools.

The intended loop for a large document is `outline` (or `locate`) to decide where, then `patch` that place. The body crosses the wire in neither direction.

**The verbs**

| Verb | Purpose |
|---|---|
| `outline` | Heading tree with levels, line numbers, and per-section byte size; on an HTML/JSX/SVG asset also the addressable `landmarks` (elements with an `id` or `data-*` marker) |
| `get_content` | Read a span: the whole body, one `section` or `selector`-addressed element, or a `line_range` |
| `stats` | Size, line count, current version, content type, body hash |
| `locate` | Find literal or regex matches: count, line numbers, enclosing section, context windows |
| `patch` | Apply an ordered list of anchored edits |
| `diff` | Compare two versions, or a pending prompt draft against the approved snapshot |

**Patch grammar**

A patch is an ordered list of edits applied to the current body in memory. Nothing is written until every edit resolves.

```json
{
  "action": "patch",
  "asset_id": "ast_...",
  "base_version": 7,
  "edits": [
    { "find": "revenue grew 12% year over year", "replace": "revenue grew 14% year over year" },
    { "op": "replace_section", "section": "## Methodology", "text": "## Methodology\n\nRestated..." },
    { "op": "insert_after", "find": "## Findings", "text": "\n\nAll figures are quarter-end.\n" },
    { "op": "replace", "pattern": "Q[1-4] FY24", "replace": "$0 (restated)", "occurrence": "all" },
    { "op": "move_section", "section": "## Appendix A", "after": "## Appendix B" },
    { "op": "append", "text": "\n\n## Appendix C\n\n..." }
  ],
  "change_summary": "correct the YoY figure, restate methodology, reorder appendices"
}
```

Operations:

- `replace` (the default when `op` is omitted): `find` is matched literally and swapped for `replace`. An empty `replace` deletes the matched text.
- `insert_before` / `insert_after`: `text` is placed relative to the `find` anchor, leaving the anchor in place.
- `replace_section`: names a region with `section` or `selector` (see below) and replaces its whole span with `text`.
- `move_section`: relocate a whole region `before` or `after` another heading, or with `position` set to `start` or `end`.
- `append` / `prepend`: `text` at the end or start of the body. No anchor needed.

`section` and `selector` are also accepted on `replace`, `insert_before`, and `insert_after` to scope the anchor search to one region, which is how a repeated phrase becomes unambiguous without quoting a long anchor.

**Naming a region**

How a region is named is derived from the asset's content type, through the platform's single media-type seam (`pkg/contenttype`); it is never guessed from the bytes.

- **Markdown** (`text/markdown`, `text/plain`): `section` names an ATX heading (`## Methodology`, or a `Report > Methodology` path when headings repeat). The span runs from that heading to the next heading of the same or higher level.
- **HTML, JSX, SVG, XML** (`text/html`, `text/jsx`, `image/svg+xml`, `application/xml`): `section` names an `<h1>`..`<h6>` heading, resolved exactly as markdown resolves `#`. `selector` names an element by CSS selector, and the region is that element's balanced subtree, running from its start tag through its matching end tag, so a `replace_section` or `move_section` can never cut a tag in half. `selector` is refused on a markdown or structureless document, with a message naming the right alternative.
- **Everything else textual** (JSON, CSV, SQL, ...): has no addressable structure. `section` and `selector` are refused with `PATCH_NO_STRUCTURE`; use anchored edits.

The supported selector forms are type (`section`, `Card`), `#id`, `.class` (which also matches a JSX `className`), `[attr]` and `[attr=value]`, joined by descendant (space) or child (`>`) combinators. Element type selectors match HTML and SVG tags case-insensitively; a JSX component name (`Card`) is matched case-sensitively. A selector that matches several elements is refused with `PATCH_AMBIGUOUS` naming the count, and `occurrence` (`first`, `last`, or a 1-based index, since a region is a single element and `all` does not apply) is the explicit opt-in, exactly as for a repeated text anchor.

```json
{
  "action": "patch",
  "asset_id": "ast_...",
  "edits": [
    { "op": "replace_section", "selector": "[data-region=\"revenue\"]", "text": "<Card data-region=\"revenue\">...</Card>" },
    { "op": "replace", "selector": ".metric", "occurrence": 2, "find": "Users", "replace": "Active Users" }
  ]
}
```

On a headingless dashboard, `outline` returns the addressable `landmarks`: every element carrying an `id` or a `data-*` marker, each with its tag, a copyable selector, line, and byte size, so an agent can find where to patch without reading the body.

**Matching rules**

An anchor must resolve to exactly one span. Zero matches and more than one match are both errors, and the error reports the count. `occurrence` (`first`, `last`, `all`, or a 1-based integer) is the explicit opt-in when the caller means a specific one or all of them; an `occurrence: "all"` edit reports how many spans it changed.

Matching is exact first. If an exact match fails, one retry normalizes CRLF to LF and ignores trailing whitespace on each line; if that resolves uniquely the edit applies and the response marks it `normalized: true`. Nothing beyond that: no fuzzy, similarity, or semantic matching, because a plausible-but-wrong edit applied silently is worse than a rejection the agent can correct.

`pattern` is the regex alternative to `find`, on both `locate` and `patch`, with `$1`-style capture references available in `replace`. Go's `regexp` is RE2, so match time is linear in input length and a pathological pattern cannot hang the server; a pattern-length cap, a match cap, and the same all-or-nothing failure rule still apply.

Edits apply in order against the evolving body, so a later edit can anchor on text an earlier edit introduced.

**Atomicity and staleness**

All edits resolve against an in-memory copy; the first failure aborts the entire call and writes nothing. The error names the failing edit by index. `base_version` is optional and checked when supplied; a mismatch is refused with the current version in the error so the agent re-reads and retries. Reads return the version, so a well-behaved agent threads it and gets lost-update protection for free.

**Response**

The response never echoes the new body. It returns the new version number, the new size, and a per-edit outcome (the operation, whether it normalized, how many spans it touched, the line where it landed) plus a unified diff of the changed hunks only.

Unified diff is rejected as an input format and adopted as the output format. As input it would make correctness depend on line numbers and context lines the model must reproduce exactly; as output it is generated by the server from two known strings, and it is the most compact accurate description of what changed.

`dry_run: true` resolves every edit and returns exactly that report, diff included, without writing.

**Search and navigation**

`locate` takes `find` (literal) or `pattern` (regex) and returns, per match: the line number, byte offset, enclosing section heading, and a context window wide enough to copy verbatim into a `find` anchor, plus the total match count. The count is the point: an agent that checks first never hits `PATCH_AMBIGUOUS`.

Line numbers are read output only and are never accepted as an edit anchor. A line number is stale the moment a preceding edit lands, whereas an anchor either matches or errors. `line_start` / `line_end` exist on `get_content` for reading a span and nowhere else.

**Text only**

Every verb here is text-only. A PDF, image, parquet, or other binary asset is refused with `PATCH_NOT_TEXT` rather than corrupted or dumped as garbage, using the platform's single media-type detection seam (`pkg/contenttype`).

**Errors**

Corrective, self-describing envelopes carrying `{code, category, message, hint}`:

| Code | Meaning |
|---|---|
| `PATCH_NO_MATCH` | Anchor text not found, with the edit index. Run `locate` and copy the anchor verbatim. |
| `PATCH_AMBIGUOUS` | Several matches for an anchor or a `selector` with no `occurrence`. Lengthen the anchor, scope it with `section` or `selector`, or set `occurrence`. |
| `PATCH_STALE_BASE` | `base_version` does not match the current version. Re-read and retry. |
| `PATCH_NOT_TEXT` | The target's content type is not textual. |
| `PATCH_TOO_LARGE` | Too many edits, too many regex matches, or a result exceeding the deployment's max content size. |
| `PATCH_SECTION_NOT_FOUND` | Named heading absent (with the document's headings in the message), or a `selector` matched no element. |
| `PATCH_BAD_PATTERN` | Regex fails to compile or exceeds the pattern cap. |
| `PATCH_BAD_EDIT` | The edit names no anchor, names both `find` and `pattern`, names both `section` and `selector`, or uses an unknown operation. |
| `PATCH_BAD_SELECTOR` | The CSS selector does not parse, with the parse error. |
| `PATCH_NO_STRUCTURE` | The content type has no sections or elements to address; use anchored edits. |
| `PATCH_UNRESOLVED_MARKUP` | The markup could not be resolved into a reliable element tree, so no element span was trusted; nothing was written. |

---

### manage_feedback

Review and respond to human feedback on your work. Feedback is its own tool (rather than actions on `manage_asset`) so an agent discovers it by name. Threads live on an asset, collection, or prompt, or on the shared general channel.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | list, get, reply, resolve, request_validation, respond_validation |
| `asset_id` / `collection_id` / `prompt_id` | string | No | - | Scope a `list` to one target |
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

**Access:** scoped to the assets and collections the caller owns or can edit (admins see all). General-channel threads are readable and replyable by any authenticated caller, and resolved only by the thread author or an admin. `memory_capture thread_ids=[...]` folds a thread into the knowledge loop and resolves it, gated by the same owns-or-edit check.

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

### manage_prompt

`manage_prompt` manages database-stored prompts (create, update, delete, list, get) and resolves any prompt to run with the `use` command. `list` supports a ranked free-text `query` over the caller's visible prompts, the same rule browsing applies: approved shared prompts plus every prompt the caller owns at any status, and everything for admins (hybrid semantic + lexical when an embedding provider is configured, lexical otherwise).

**Editing without resending.** `manage_prompt` carries the same content verbs as `manage_asset` (`patch`, `locate`, `get_content`, `outline`, `stats`, `diff`) with the identical grammar documented in [Editing content in place](#editing-content-in-place). Renaming a step in a long operating procedure is one `patch` call whose arguments are the size of the change. A patch routes through the same review gate as any other content edit: patching an approved global or persona prompt produces a pending draft version and the approved snapshot keeps being served until an admin approves it. `diff` with no versions named compares the newest pending draft against the version currently being served, which is the question a reviewer actually has.

**`use`: resolve and run.** When a user names a report, procedure, or recurring task ("run the daily sales report"), the agent resolves it against the prompt library instead of enumerating prompts. `use` accepts any handle in `name`:

- an exact bare name (`daily-sales-report`),
- a display name, case-insensitively ("Daily Sales Report"),
- an `mcp:prompt:<id>` reference (the same reference `search` results carry),
- or free text, ranked against the library.

A single confident match returns `status: "resolved"` with the prompt content (argument values passed in `args` are substituted), its argument specs, any required arguments still missing, and provenance (scope, status, version, approver, owner, reference) so the agent can confirm what it is about to run: "running Daily Sales Report v4, approved by jane@example.com". An ambiguous handle returns `status: "ambiguous"` with a short ranked candidate list to disambiguate, never an error and never a silent first-match. Operator, workflow, and toolkit prompts resolve through `use` as well (they remain read-only to the management commands); the auto-generated `platform-overview` prompt is served only on the native prompts surface.

Non-admins manage only their own personal prompts; admins manage every scope. An admin creating a global or persona prompt is its approver: the prompt lands `approved` (and therefore searchable) with the approval stamped, rather than sitting in draft. `create` and `update` also accept `collection_id` to place the prompt in a shared collection (an id from the `collections` array `list` returns; an empty string clears the placement; collections themselves are created in the portal). On the native MCP prompts surface, database prompts are presented under per-viewer scope-prefixed names (see [Configuration: Prompts](configuration.md#prompts)); `use` insulates users from those presented names entirely.

**Versioning and review.** Every mutation of a prompt's content, display name, description, arguments, or tags snapshots an immutable version with its author; approval stamps bind to the specific version approved. Editing the content or arguments of an **approved global or persona** prompt does not apply immediately: `update` returns `status: "pending_approval"` with the draft's `pending_version`, and every caller keeps being served the approved snapshot until an admin approves the draft (in the admin portal, or `POST /api/v1/admin/prompts/{id}/versions/{version}/approve`). A gated content edit cannot be combined with scope/status/other non-versioned changes in one call; submit them separately. Personal prompts and never-approved drafts version silently. `get` and `list` also report `run_count` and `last_run_at` per prompt, aggregated from prompt-serve audit events (each `prompts/get` and resolved `use` counts as a serve), and `list` results include the shared `collections` list (the portal's organization model) when the store supports collections.

**Attached materials.** A prompt can carry the reference material its procedure depends on: the report template it fills, the checklist it follows, the brand header it embeds, the sample payload it matches. Attachments are links to [managed resources](portal-user.md#resources), stored by resource id, so editing the uploaded file updates every prompt that attaches it.

A resolved prompt delivers them after the prompt text. Text material at or below 64 KiB arrives inline as an MCP embedded resource; anything binary or larger arrives as a resource link the client reads on demand. `use` also lists them in an `attachments` array carrying each item's URI, media type, size, and availability, so the agent can state what materials it received. The delivered material is framed as authoritative: an attached template is to be filled rather than reinvented, an attached checklist followed.

An attachment must be at least as widely visible as the prompt that carries it, so a shared SOP never arrives with materials most of its audience cannot read:

| resource scope | may be attached to |
|---|---|
| global | any prompt |
| persona `P` | personal prompts, and persona prompts scoped to exactly `P` |
| user | the author's own personal prompts only |

The rule is enforced when the attachment is made and again when the prompt changes scope, so requesting promotion of a personal prompt that carries a private template is refused with a message naming the resource. It is checked a third time at serve time against the caller: a reader who cannot read an attachment receives the prompt with a note that some materials were not delivered, never their contents and never their names. Deleting an attached resource does not break the prompt; it still serves, and both the served result and the portal flag the material as missing.

Authors manage attachments from the prompt viewer in the portal, and the resource detail view lists the prompts that attach a resource, so the cost of deleting it is visible first.

**Referenced scripts.** A prompt can also reference the [managed scripts](#manage_script) its procedure depends on: the report the analysis reads, the export it compares against. `attach_script` adds one and `detach_script` removes it; both take `script`, either the `mcp:script:<id>` reference `search` and `fetch` return or a bare script id from `manage_script`. Referencing is an edit to the prompt and takes the same authority every other prompt mutation does.

Serving a prompt delivers each referenced script's **contract** — what it is, what parameters it takes, whether a version is approved, its schedule, and the last successful run with what that run produced — plus the instruction to call `run_script` for fresh output. `use` also lists them in a `scripts` array. Serving never executes a script: a prompt read is a read path, and running code from it would blur audit attribution and turn every read into a potential asset write. The contract never carries the script's source; reading the code is what `manage_script` get is for.

The same visibility rule attachments follow applies, over the script's own scope (`global`, `persona`, `personal`), so a shared procedure never arrives naming an automation most of its audience cannot see. It is enforced at attach time, again when the prompt changes scope or requests promotion, and a third time at serve time against the reader: a caller who cannot see a referenced script receives the prompt with a note that part of its automation was unavailable, never the script's name or parameters. Deleting a referenced script does not break the prompt; it still serves, and the payload reports the reference as no longer existing.

**Prompt browser app.** In MCP Apps-capable hosts, the built-in `prompt-browser` app is bound to the `show_prompts` tool. `show_prompts` is presentation-only: its only job is to render the interactive library browser (search, facets, argument forms, a Run action) for the human, so an agent calls it only when the user wants to see their prompts. `manage_prompt` carries no app and renders nothing, so the agent's own prompt work (resolve, run, create, edit) never puts a UI in front of the user. The rendered app populates itself from its own `manage_prompt` calls. See [MCP Apps: Overview](../mcpapps/overview.md#built-in-app-prompt-browser). `manage_prompt`'s JSON results are complete on their own in clients that do not render apps.

### manage_script

`manage_script` authors, validates, and dry-runs **managed scripts**: small Starlark programs the platform stores, versions, and governs, so a process whose logic is already solved (a KPI report, a recurring export) can be re-run without deriving it again through a conversation. Write a script when the logic is settled and the work will repeat; keep using the query tools directly while you are still exploring.

**The loop.** `create` (or `update`/`patch`), then `validate`, then `run_draft`. `validate` parses and resolves the source without executing it, and reports the capabilities and connections the code references. `run_draft` executes it for real, under **your own identity and persona**, with tighter limits than an approved run will have, persisting nothing. Commands: `create`, `update`, `patch`, `delete`, `get`, `list`, `diff`, `validate`, `run_draft`, `help`, the run-history commands `runs` and `get_run`, the schedule commands `schedule_set`, `schedule_list`, `schedule_enable`, and `schedule_disable`, plus the shared content verbs (`locate`, `get_content`, `outline`, `stats`) documented in [Editing content in place](#editing-content-in-place).

**The dialect, in-context.** Call `help` before writing your first script. Starlark is Python-shaped but deliberately smaller, and `help` states exactly what is available and what a Python instinct will reach for and not find, with the corrective form for each. `get` also retrieves seeded worked examples (`example-daily-sales`, `example-region-rollup`) so a first script starts from a script that runs. `validate` recognizes the predictable Python-isms and answers with a correction rather than a bare parse error: `import` is not available (`json` and `date` are already predeclared), `try`/`except` does not exist (an error fails the run by design; stop deliberately with `fail("why")`), f-strings are not supported (use `.format()` or `%`), `while` and recursion are off, and there is no clock, no randomness, no filesystem, and no network.

**What a script can reach.** A closed, enumerable set, because an enumerable surface is what makes a review meaningful:

| Binding | What it does |
|---|---|
| `platform.query(sql, connection, params)` | Read-only SQL. Returns `{columns, rows, row_count}`, rows as dicts keyed by column name, under hard row and byte caps |
| `platform.export(name, rows, format)` | Declares an output (`csv`, `json`, `markdown`, `text`). In a draft run this reports the shape and size the output would have and writes nothing |
| `print(...)` | The run log, bounded; anything larger belongs in an export |
| `run.run_id`, `run.fire_time`, `run.params[...]` | The frozen run record |
| `json`, `date` | Encode/decode, and date arithmetic over `YYYY-MM-DD` strings |

**Parameters are bound, never spliced.** Write `:name` placeholders and pass the values in `params`; the platform renders each as a typed SQL literal, including a list as a parenthesized `IN` list. Never build SQL by string concatenation. A date binds as a quoted string, so compare it against a `DATE` column as `DATE :day`, which renders the standard literal `DATE '2026-08-12'`.

**A truncated result fails.** `platform.query` refuses a result the engine truncated at the row cap rather than handing the script a partial answer, because a script that sums the first N rows of a larger result reports a wrong total with no sign that anything was missing. Aggregate in SQL, or narrow the query.

**Determinism, precisely.** Same script version + same parameters + same underlying data produce the same output. That is reproducibility, not "identical forever" — the warehouse changes between runs, and that is the point of re-running. There is deliberately no `now()` and no `today()`: the fire time is pinned on `run.fire_time` when the run is created, so a daily report recomputes "yesterday" identically when it is re-run months later to explain what it said. A script error is deterministic and is never retried.

**Scheduling.** `schedule_set` gives an approved script a cadence: a cron expression (`0 7 * * 1-5`) or a descriptor (`@daily`), the IANA timezone it is read in, and the parameter values every fire binds. A script has at most one schedule, and setting one again replaces it. Bound values may contain `${fire_date}`, which expands at each fire to that fire's date in the schedule's timezone and is written onto the run — which is what lets a scheduled run be re-read months later and explain itself. The schedule confers no authority: it decides when the approved version runs, never what it may reach. A fire arriving while the previous run is still going is skipped and recorded, a gap in service produces one run for the latest fire rather than a burst, and a failed scheduled run emails the script's owner and its approving administrator. Full behavior in [Running Managed Scripts](../scripts/running.md#running-one-on-a-schedule).

**Discoverable, and addressable.** A script is reachable the way every other platform entity is: it has an `mcp:script:<id>` reference, `search` finds it by name, description, and parameter contract, and `fetch` dereferences that reference to its contract — what it is, what it takes, whether a version is approved, its cadence, and the last successful run with what that run produced. The reference resolves by id, so renaming a script never breaks one that is stored. What a script is FOR is answered by that contract; the source stays behind `get` and the review surface, because reading code is a reviewer's job rather than a caller's. Discovery grants nothing: finding a script says it exists and what it takes, while running it is still `run_script` under the execution gate. Search shows a caller exactly the set `list` would — global scripts, their persona's, and their own — and skips dead ends (disabled, deprecated, superseded), though a reference to one still resolves and says plainly that it will not run.

**Governance.** Every mutation snapshots an immutable version with its author. `scripts.approved_version_id` is the execution gate: the platform executes only an approved version, and until a version is approved `run_draft` is the only way a script runs at all. Once a script has an approved version, editing its source or parameter contract lands as a pending draft (`update` returns `status: "pending_approval"`) and the approved version keeps running; a gated edit cannot be combined with scope, status, or other non-versioned changes in one call. Non-admins manage their own personal scripts; admins manage every scope. The security model is documented in full in [Managed Scripts: Security Model](../scripts/security.md).

### show_prompts

Renders the user's prompt library as an interactive browser for the human to look at. Call it only when the human wants to see, browse, or pick from their prompts visually ("show me my prompts", "open my prompt library"). It performs no data operation and returns only a short confirmation; the rendered app populates itself from its own `manage_prompt` calls. For running, creating, editing, or listing prompts as part of your own work, use `manage_prompt`, which returns data and renders no UI. Optional `search` pre-focuses the library.

---

## Next Steps

- [Multi-Provider](multi-provider.md) - Use multiple connections
- [Cross-Enrichment](../cross-enrichment/overview.md) - Understand semantic enrichment
- [Tools API Reference](../reference/tools-api.md) - Complete API specification

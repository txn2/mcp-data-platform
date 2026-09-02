---
description: Complete API specification for all MCP tools. Parameters, response schemas, error codes for DataHub, Trino, and S3 operations.
---

# Tools API Reference

Complete specification for all MCP tools provided by mcp-data-platform.

## Error contract

Every failed tool call returns a uniform, self-describing error so an agent can tell a correctable mistake from a platform problem and act on it. A failure sets `isError: true` and carries both a human-readable text message and a machine-readable `structuredContent.error` object:

```json
{
  "isError": true,
  "content": [
    { "type": "text", "text": "the \"asset_id\" parameter is required (code: missing_required_parameter) Hint: Supply \"asset_id\" and retry. This is a problem with the call's arguments, not a platform fault." }
  ],
  "structuredContent": {
    "error": {
      "code": "missing_required_parameter",
      "category": "client_input",
      "message": "the \"asset_id\" parameter is required",
      "hint": "Supply \"asset_id\" and retry. This is a problem with the call's arguments, not a platform fault."
    }
  }
}
```

| Field | Meaning |
|-------|---------|
| `code` | Stable, machine-readable identifier the agent may branch on (for example `missing_required_parameter`, `invalid_arguments`, `not_found`, `unauthorized`, `setup_required`, `internal_error`). |
| `category` | Broad class (see below) telling the agent whose fault the failure is. |
| `message` | The specific failure. |
| `hint` | The corrective action, when the caller can take one. |

**Categories**

| Category | Whose fault | What to do |
|----------|-------------|------------|
| `client_input` | The call | Fix the arguments and retry. |
| `not_found` | The call | The named resource does not exist; correct the reference. |
| `authentication_failed` | The caller's identity | Provide valid credentials. |
| `authorization_denied` | The caller's identity | The persona is not permitted; request access. |
| `user_declined` | The user | A consent prompt was declined. |
| `setup_required` | Session state | Call the required setup tool first. |
| `feature_unavailable` | Deployment config | The feature is not enabled on this deployment; do not present it as an outage. |
| `internal` | The platform | Not the caller's fault; do not retry with modified input. |
| `tool_error` | Unclassified | A tool failure that has not been given a finer category; the message is still descriptive. |

The contract is uniform by construction: a normalization layer guarantees every error result carries this envelope even when an individual tool returns only a bare message, so an agent never receives an opaque, undifferentiated string. The `category` is also recorded on the audit log (`error_category`) for operators.

### Unknown arguments are refused, not ignored

The platform's own tools publish input schemas that are closed to unknown top-level arguments (`"additionalProperties": false`). A misnamed argument fails at the tool boundary, before the handler runs, with `code: invalid_arguments`, `category: client_input`, and a message naming the offending property — for example, passing `parameters` to `api_invoke_endpoint` instead of `query_params`:

```json
{
  "error": {
    "code": "invalid_arguments",
    "category": "client_input",
    "message": "validating \"arguments\": validating root: unexpected additional properties [\"parameters\"]",
    "hint": "The arguments do not match the tool's input schema. Read the tool's schema, correct or drop the named property, and retry. This is a problem with the call's arguments, not a platform fault."
  }
}
```

Nested maps stay open where they carry a foreign namespace: `query_params`, `headers`, and `body` on the `api_*` tools accept arbitrary keys, because those names belong to the upstream API rather than to the tool. Tools proxied from an upstream MCP server through a gateway connection keep the upstream's own schema, strict or not.

## Trino Tools

### trino_query

Execute a read-only SQL query against the Trino cluster. Write operations (INSERT, UPDATE, DELETE, CREATE, DROP, etc.) are rejected before reaching Trino. Annotated with `ReadOnlyHint: true` for MCP client auto-approval.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to execute (read-only) |
| `limit` | integer | No | 1000 | Maximum rows to return (capped by max_limit config) |
| `connection` | string | No | the kind's `default:` | Trino connection name |

**Response Schema:**

```json
{
  "columns": [
    {"name": "column_name", "type": "varchar"}
  ],
  "rows": [
    ["value1", "value2"]
  ],
  "row_count": 100,
  "execution_time_ms": 250,
  "query_id": "20240115_123456_00001_xxxxx"
}
```

**Enrichment (when enabled):**

```json
{
  "semantic_context": {
    "description": "Table description from DataHub",
    "owners": [{"name": "Team Name", "type": "group"}],
    "tags": ["tag1", "tag2"],
    "domain": {"name": "Domain Name"},
    "quality_score": 0.95,
    "deprecation": null
  }
}
```

**Error Codes:**

| Code | Cause |
|------|-------|
| `SYNTAX_ERROR` | Invalid SQL syntax |
| `TABLE_NOT_FOUND` | Referenced table doesn't exist |
| `PERMISSION_DENIED` | Insufficient privileges |
| `TIMEOUT` | Query exceeded timeout |
| `WRITE_REJECTED` | Write SQL rejected (use `trino_execute` instead) |

---

### trino_execute

Execute any SQL against the Trino cluster, including write operations (INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, etc.). Annotated with `DestructiveHint: true` so MCP clients prompt for confirmation.

`read_only` is set per instance, and the block applies to the connection the call names — or to the default connection when it names none. A call routed to an instance with `read_only: true` is refused; the other instances of the same toolkit still accept writes.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to execute |
| `limit` | integer | No | 1000 | Maximum rows to return (capped by max_limit config) |
| `connection` | string | No | the kind's `default:` | Trino connection name |

**Response Schema:** Same as `trino_query`.

---

### trino_explain

Get the execution plan for a SQL query.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to explain |
| `connection` | string | No | the kind's `default:` | Trino connection name |

**Response Schema:**

```json
{
  "plan": "Query Plan\n- TableScan[table = ...]\n  ...",
  "format": "text"
}
```

---

### trino_browse

Browse the Trino catalog hierarchy. Omit all parameters to list catalogs. Provide `catalog` to list schemas. Provide `catalog` and `schema` to list tables.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `catalog` | string | No | - | Catalog name. Omit to list all catalogs |
| `schema` | string | No | - | Schema name. Requires `catalog`. Omit to list schemas |
| `pattern` | string | No | - | LIKE pattern to filter tables (only when listing tables) |
| `connection` | string | No | the kind's `default:` | Trino connection name |

**Response Schema (list catalogs):**

```json
{
  "catalogs": ["hive", "iceberg", "memory"]
}
```

**Response Schema (list schemas):**

```json
{
  "catalog": "hive",
  "schemas": ["default", "sales", "marketing"]
}
```

**Response Schema (list tables):**

```json
{
  "catalog": "hive",
  "schema": "sales",
  "tables": [
    {"name": "orders", "type": "TABLE"},
    {"name": "customers", "type": "TABLE"},
    {"name": "daily_revenue", "type": "VIEW"}
  ]
}
```

---

### trino_describe_table

Get table schema and metadata.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `table` | string | Yes | - | Table name (can be `catalog.schema.table`) |
| `connection` | string | No | the kind's `default:` | Trino connection name |

**Response Schema:**

```json
{
  "table": {
    "catalog": "hive",
    "schema": "sales",
    "name": "orders"
  },
  "columns": [
    {
      "name": "order_id",
      "type": "bigint",
      "nullable": false,
      "comment": "Unique order identifier"
    }
  ],
  "partitioning": ["order_date"],
  "properties": {
    "format": "PARQUET"
  }
}
```

---

### trino_list_connections

List configured Trino connections.

**Parameters:** None

**Response Schema:**

```json
{
  "connections": [
    {
      "name": "primary",
      "display_name": "Production",
      "host": "trino.example.com",
      "catalog": "hive",
      "schema": "default"
    }
  ]
}
```

---

## DataHub Tools

### datahub_search

Search for entities in the catalog.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | Search query |
| `type` | string | No | - | Entity type: `dataset`, `dashboard`, `chart`, `dataflow` |
| `platform` | string | No | - | Platform filter: `trino`, `snowflake`, `s3`, etc. |
| `limit` | integer | No | 10 | Maximum results (capped by max_limit config) |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

```json
{
  "results": [
    {
      "urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)",
      "name": "orders",
      "description": "Customer orders",
      "platform": "trino",
      "type": "dataset",
      "owners": ["Data Team"],
      "tags": ["pii", "financial"]
    }
  ],
  "total": 150,
  "has_more": true
}
```

**Enrichment (when enabled):**

```json
{
  "query_context": {
    "urn:li:dataset:...": {
      "queryable": true,
      "connection": "primary",
      "table_identifier": {
        "catalog": "hive",
        "schema": "sales",
        "table": "orders"
      },
      "sample_query": "SELECT * FROM hive.sales.orders LIMIT 10"
    }
  }
}
```

---

### Reading a catalog entity by URN

The DataHub toolkit registers no by-URN read tool. A dataset, glossary term,
tag, domain, or data product is read in full with [`fetch`](#fetch) on its
`urn:li:...` reference; the retired `datahub_get_entity`, `datahub_get_schema`,
`datahub_get_queries`, `datahub_get_glossary_term`, and
`datahub_get_data_product` tools are not registered under any name.

**A fetched dataset** (`fetch urn:li:dataset:...`) returns the catalog's full
record as the document `content`:

```json
{
  "urn": "urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.sales.orders,PROD)",
  "name": "orders",
  "type": "DATASET",
  "platform": "trino",
  "sub_types": ["table"],
  "description": "Customer orders from e-commerce platform",
  "owners": [{"urn": "urn:li:corpuser:ana", "type": "user", "name": "ana"}],
  "tags": ["pii", "financial"],
  "glossary_terms": [{"urn": "urn:li:glossaryTerm:Order", "name": "Order"}],
  "domain": {"urn": "urn:li:domain:sales", "name": "Sales"},
  "custom_properties": {"refresh_schedule": "daily"},
  "created": "2024-01-01T00:00:00Z",
  "schema": {
    "version": 3,
    "fields": [
      {"field_path": "order_id", "type": "NUMBER", "native_type": "bigint", "nullable": false,
       "description": "Unique order identifier", "tags": ["pii"]}
    ],
    "primary_keys": ["order_id"],
    "foreign_keys": []
  },
  "queries": [
    {"urn": "urn:li:query:q1", "name": "completed orders",
     "statement": "SELECT * FROM orders WHERE status = 'completed'", "source": "MANUAL",
     "created_by": "urn:li:corpuser:ana", "created": "2024-01-15T10:00:00Z"}
  ],
  "total_queries": 1,
  "related_documents": [{"urn": "urn:li:document:d1", "title": "Orders runbook"}],
  "query_availability": {
    "available": true,
    "query_table": "warehouse.sales.orders",
    "connection": "primary",
    "estimated_rows": 1250000
  }
}
```

`unavailable` names any part of the record (`schema`, `queries`,
`related_documents`) the catalog could not serve on that read, so an absent
part is distinguishable from an empty one. `query_availability` is present when
a query provider is configured; the document's `verifiable` field names the
same table when the dataset is queryable.

**A fetched glossary term** (`fetch urn:li:glossaryTerm:...`) returns:

```json
{
  "urn": "urn:li:glossaryTerm:Revenue",
  "kind": "glossary_term",
  "name": "Revenue",
  "description": "Total monetary value from sales transactions",
  "parent_node": "urn:li:glossaryNode:FinancialMetrics",
  "owners": [{"urn": "urn:li:corpuser:cfo", "type": "user", "name": "cfo"}],
  "custom_properties": {"calculation": "SUM(line_item_amount)"},
  "datasets": [{"urn": "urn:li:dataset:...", "name": "warehouse.sales.invoices"}],
  "more_datasets": false
}
```

A tag or domain returns the same shape without `parent_node`, `owners`, and
`custom_properties`.

**A fetched data product** (`fetch urn:li:dataProduct:...`) returns:

```json
{
  "urn": "urn:li:dataProduct:customer360",
  "kind": "data_product",
  "name": "Customer 360",
  "description": "Unified customer view combining all customer data sources",
  "domain": {"urn": "urn:li:domain:marketing", "name": "Marketing"},
  "owners": [{"urn": "urn:li:corpgroup:marketing-data", "type": "group", "name": "Marketing Data Team"}],
  "custom_properties": {"sla": "99.9%", "refresh": "hourly"},
  "datasets": [
    {"urn": "urn:li:dataset:customers", "name": "customers"},
    {"urn": "urn:li:dataset:customer_events", "name": "customer_events"}
  ]
}
```

Member datasets outside the caller's connection boundary are withheld and
counted in `datasets_withheld`, with `notice` explaining it.

---

### datahub_get_lineage

Get upstream or downstream lineage for an entity. Set `level=column` for column-level lineage showing which upstream columns feed each downstream column. Default (`dataset`) returns dataset-level relationships with direction and depth control.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Entity URN |
| `level` | string | No | `dataset` | Granularity: `dataset` or `column` |
| `direction` | string | No | `downstream` | `upstream` or `downstream` (dataset level only) |
| `depth` | integer | No | 3 | Maximum traversal depth, max 5 (dataset level only) |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema (dataset level):**

```json
{
  "root": "urn:li:dataset:...",
  "direction": "downstream",
  "entities": [
    {
      "urn": "urn:li:dataset:...",
      "name": "daily_orders_agg",
      "type": "dataset",
      "depth": 1
    }
  ],
  "relationships": [
    {
      "source": "urn:li:dataset:orders",
      "target": "urn:li:dataset:daily_orders_agg",
      "type": "TRANSFORMED"
    }
  ]
}
```

**Response Schema (column level):**

```json
{
  "root": "urn:li:dataset:...",
  "column_lineage": [
    {
      "downstream": {
        "urn": "urn:li:dataset:daily_orders_agg",
        "column": "total_revenue"
      },
      "upstreams": [
        {
          "urn": "urn:li:dataset:orders",
          "column": "total_amount"
        }
      ]
    }
  ]
}
```

---

### datahub_browse

Browse the DataHub catalog by category. Set `what=tags` to list tags, `what=domains` to list data domains, or `what=data_products` to list data products.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | What to browse: `tags`, `domains`, or `data_products` |
| `filter` | string | No | - | Optional filter string (tags only) |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema (tags):**

```json
{
  "tags": [
    {"urn": "urn:li:tag:pii", "name": "pii", "description": "Contains PII"},
    {"urn": "urn:li:tag:financial", "name": "financial", "description": "Financial data"}
  ]
}
```

**Response Schema (domains):**

```json
{
  "domains": [
    {
      "urn": "urn:li:domain:sales",
      "name": "Sales",
      "description": "Sales and revenue data",
      "entity_count": 45
    }
  ]
}
```

**Response Schema (data_products):**

```json
{
  "data_products": [
    {
      "urn": "urn:li:dataProduct:customer360",
      "name": "Customer 360",
      "description": "Unified customer view",
      "domain": "urn:li:domain:marketing",
      "assets": 12
    }
  ]
}
```


---

### datahub_list_connections

List configured DataHub connections.

**Parameters:** None

**Response Schema:**

```json
{
  "connections": [
    {
      "name": "primary",
      "display_name": "Primary Catalog",
      "url": "https://datahub.example.com"
    }
  ]
}
```

---

### datahub_create

Create a new entity or resource in DataHub. Uses the `what` discriminator to select the entity type. Only available when `read_only: false`.

Annotated with `DestructiveHint: false`, `IdempotentHint: false`, `OpenWorldHint: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | Entity type to create (see table below) |
| `name` | string | Varies | - | Entity name (required for most types) |
| `connection` | string | No | first configured | DataHub connection name |

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

**Response Schema:**

```json
{
  "urn": "urn:li:tag:new-tag",
  "message": "Created tag 'new-tag'"
}
```

---

### datahub_update

Update metadata on an existing DataHub entity. Uses the `what` discriminator to select what to update, with an optional `action` for add/remove operations. Only available when `read_only: false`.

Annotated with `DestructiveHint: false`, `IdempotentHint: true`, `OpenWorldHint: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | What to update (see table below) |
| `urn` | string | Varies | - | Entity URN to update |
| `action` | string | Varies | - | `add` or `remove` (required for tags, glossary terms, links, owners) |
| `connection` | string | No | first configured | DataHub connection name |

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

**Response Schema:**

```json
{
  "urn": "urn:li:dataset:...",
  "message": "Updated description on urn:li:dataset:..."
}
```

---

### datahub_delete

Delete an entity or resource from DataHub. Uses the `what` discriminator to select the entity type. Only available when `read_only: false`.

Annotated with `DestructiveHint: true`, `IdempotentHint: true`, `OpenWorldHint: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `what` | string | Yes | - | Entity type to delete (see below) |
| `urn` | string | Yes | - | Entity URN to delete |
| `connection` | string | No | first configured | DataHub connection name |

Supported `what` values: `query`, `tag`, `domain`, `glossary_entity`, `data_product`, `application`, `document`, `structured_property`.

**Response Schema:**

```json
{
  "urn": "urn:li:tag:old-tag",
  "message": "Deleted tag 'old-tag'"
}
```

---

## S3 Tools

### s3_list_buckets

List available S3 buckets.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "buckets": [
    {
      "name": "data-lake",
      "creation_date": "2024-01-01T00:00:00Z",
      "region": "us-east-1"
    }
  ]
}
```

---

### s3_list_objects

List objects in a bucket.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `prefix` | string | No | - | Key prefix filter |
| `delimiter` | string | No | - | Delimiter for hierarchy (typically `/`) |
| `max_keys` | integer | No | 1000 | Maximum objects to return |
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "bucket": "data-lake",
  "prefix": "sales/orders/",
  "objects": [
    {
      "key": "sales/orders/2024/01/data.parquet",
      "size": 52428800,
      "last_modified": "2024-01-15T10:30:00Z",
      "storage_class": "STANDARD"
    }
  ],
  "common_prefixes": ["sales/orders/2024/02/"],
  "is_truncated": false
}
```

---

### s3_get_object

Get object contents.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "bucket": "data-lake",
  "key": "config/settings.json",
  "content": "{\"setting\": \"value\"}",
  "content_type": "application/json",
  "size": 25,
  "last_modified": "2024-01-15T10:30:00Z"
}
```

Note: Content is limited by `max_get_size` configuration.

---

### s3_get_object_metadata

Get object metadata without downloading content.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "bucket": "data-lake",
  "key": "sales/orders/data.parquet",
  "size": 52428800,
  "content_type": "application/octet-stream",
  "last_modified": "2024-01-15T10:30:00Z",
  "etag": "\"d41d8cd98f00b204e9800998ecf8427e\"",
  "metadata": {
    "x-amz-meta-created-by": "etl-pipeline"
  }
}
```

---

### s3_presign_url

Generate a pre-signed URL.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `expires` | string | No | `15m` | URL expiration (e.g., `1h`, `30m`) |
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "url": "https://bucket.s3.amazonaws.com/key?X-Amz-...",
  "expires_at": "2024-01-15T11:00:00Z"
}
```

---

### s3_list_connections

List configured S3 connections.

**Parameters:** None

**Response Schema:**

```json
{
  "connections": [
    {
      "name": "primary",
      "display_name": "Data Lake",
      "region": "us-east-1",
      "read_only": true
    }
  ]
}
```

---

### s3_put_object

Upload an object. Only available when `read_only: false`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `content` | string | Yes | - | Object content |
| `content_type` | string | No | `application/octet-stream` | MIME type |
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "bucket": "data-lake",
  "key": "uploads/file.json",
  "etag": "\"d41d8cd98f00b204e9800998ecf8427e\"",
  "size": 1024
}
```

---

### s3_delete_object

Delete an object. Only available when `read_only: false`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `bucket` | string | Yes | - | Bucket name |
| `key` | string | Yes | - | Object key |
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "bucket": "data-lake",
  "key": "uploads/file.json",
  "deleted": true
}
```

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
| `connection` | string | No | first configured | S3 connection name |

**Response Schema:**

```json
{
  "source": {
    "bucket": "data-lake",
    "key": "original/file.json"
  },
  "destination": {
    "bucket": "data-lake",
    "key": "backup/file.json"
  },
  "copied": true
}
```

---

## Knowledge Tools

For the full governance workflow, see [Knowledge Capture](../knowledge/overview.md).

### memory_capture

Record domain knowledge shared during a session (memory toolkit). Available to all personas when the memory layer is enabled (memory defaults on when a database is configured; `memory.enabled: false` disables capture).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `type` | string | Yes | - | Sink-class. Live: `personal_preference`, `episodic_event`. Reviewed (creates a pending insight): `business_knowledge`, `schema_entity`, `operational_rule` |
| `content` | string | Yes | - | The knowledge to record (10-4000 characters) |
| `confidence` | string | No | `medium` | Confidence level: `high`, `medium`, `low` |
| `entity_urns` | array | No | `[]` | DataHub URNs this knowledge relates to (max 10) |
| `related_columns` | array | No | `[]` | Columns related to this knowledge (max 20) |
| `suggested_actions` | array | No | `[]` | Proposed catalog changes (max 5) |

**Suggested Action Schema:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `action_type` | string | Yes | One of: `update_description`, `add_tag`, `add_glossary_term`, `flag_quality_issue`, `add_documentation`, `add_curated_query` |
| `target` | string | Yes | Target of the change (entity name, column name, or URL) |
| `detail` | string | Yes | Change detail (new description, tag name, term name, query name, etc.) |
| `query_sql` | string | Conditional | SQL statement (required for `add_curated_query`) |
| `query_description` | string | No | Optional description for `add_curated_query` |

**Response Schema:**

```json
{
  "insight_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "status": "pending",
  "message": "Insight captured. It will be reviewed by a data catalog administrator."
}
```

---

### apply_knowledge

Review, synthesize, and apply captured insights to the data catalog. Admin-only. Requires `knowledge.apply.enabled: true`.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `action` | string | Yes | One of: `bulk_review`, `review`, `synthesize`, `apply`, `approve`, `reject`, `rollback`, `list_changesets`, `bulk_untag` |
| `entity_urn` | string | Conditional | Required for `review`, `synthesize`, `apply`, `list_changesets`; optional for `rollback` (validates the changeset belongs to this entity) |
| `tag_urn` | string | Conditional | Required for `bulk_untag`; the tag (name or `urn:li:tag:...`) to remove from every entity that carries it |
| `insight_ids` | array | Conditional | Required for `approve`, `reject`; optional for `synthesize`, `apply` |
| `changes` | array | Conditional | Required for `apply` |
| `changeset_id` | string | Conditional | Required for `rollback` |
| `confirm` | bool | No | Required when `require_confirmation` is enabled (for `apply` and `rollback`) |
| `review_notes` | string | No | Notes for `approve`/`reject` actions |
| `itemize` | bool | No | With `bulk_review`, also return the pending insights themselves (full `insight_text` body, `captured_by`, `sink_class`, `created_at`, `suggested_actions_count`, ...; full `suggested_actions` omitted, `fetch` for it), paginated by `offset`/`limit`. The insights window is byte-budgeted (`page_size_capped: true` flags a short page, continue with `next_offset`) and `by_entity` is capped (`by_entity_truncated: true`) so the response stays under the output limit |
| `limit` | int | No | Page size for itemized `bulk_review` (default 20, max 100) |
| `offset` | int | No | Page start for itemized `bulk_review`; pass the previous `next_offset` to continue |

**Change Schema (for `apply` action):**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `change_type` | string | Yes | One of: `update_description`, `add_tag`, `remove_tag`, `add_glossary_term`, `flag_quality_issue`, `add_documentation`, `add_curated_query`, `set_structured_property`, `remove_structured_property`, `raise_incident`, `resolve_incident`, `add_context_document`, `update_context_document`, `remove_context_document`, `delete_tag`, `set_custom_property`, `remove_custom_property` |
| `target` | string | Yes | Target of the change (see below) |
| `detail` | string | Yes | Change detail (see below) |
| `query_sql` | string | Conditional | SQL statement (required for `add_curated_query`). For `update_context_document`, the new title |
| `query_description` | string | No | Optional description for `add_curated_query`. For `add_context_document`/`update_context_document`, the document category |

**Target and detail by change type:**

| Change Type | Target | Detail |
|-------------|--------|--------|
| `update_description` | `column:<fieldPath>` for column-level, empty for entity-level (or the tag URN via `entity_urn` to fix a tag's own definition) | Description text |
| `add_tag` / `remove_tag` | Ignored | Tag name or URN |
| `add_glossary_term` | Ignored | Term name or URN |
| `flag_quality_issue` | Ignored | Quality issue description (sets the `QualityIssue` tag; with a detail, also raises a DataHub incident carrying it) |
| `add_documentation` | URL | Link description |
| `add_curated_query` | Ignored | Query name |
| `set_structured_property` | Property qualified name or URN | Value or JSON array |
| `remove_structured_property` | Property qualified name or URN | Removal reason |
| `raise_incident` | Incident title | Description |
| `resolve_incident` | Incident URN | Resolution message |
| `add_context_document` | Document title | Document content |
| `update_context_document` | Document ID | New content |
| `remove_context_document` | Document ID | Ignored |
| `delete_tag` | Ignored (`entity_urn` is the tag URN) | Ignored |
| `set_custom_property` | customProperties key | Value |
| `remove_custom_property` | customProperties key | Ignored |

`delete_tag` deletes a tag definition entirely and is irreversible; `set_custom_property`/`remove_custom_property` edit an entity's legacy `customProperties` (datasets, dashboards, charts, dataFlows, dataJobs, containers, dataProducts, domains, glossaryTerms, glossaryNodes) and, like structured properties, are recorded but not auto-revertible. A single apply may set multiple properties or remove multiple properties, but not both on the same entity (the shared aspect is written non-atomically); use separate apply calls.

**Actions:**

| Action | Description | Required Params |
|--------|-------------|-----------------|
| `bulk_review` | Counts of all pending insights; pass optional `itemize: true` (with `limit`/`offset`) to enumerate the queue, each with its full `insight_text` body, `captured_by`, `sink_class`, and `suggested_actions_count` (response bounded per page; `page_size_capped`/`by_entity_truncated` flag any cut) | None |
| `review` | Insights for a specific entity with current DataHub metadata | `entity_urn` |
| `approve` | Transition insights to approved status | `insight_ids` |
| `reject` | Transition insights to rejected status | `insight_ids` |
| `synthesize` | Structured change proposals from approved insights | `entity_urn` |
| `apply` | Write changes to DataHub with changeset tracking | `entity_urn`, `changes` |
| `list_changesets` | List an entity's changesets (id, timestamp, actor, change type, rollback status) | `entity_urn` |
| `rollback` | Revert a changeset's changes to their before-image | `changeset_id`, `confirm` |
| `bulk_untag` | Remove a tag from every entity a catalog search finds carrying it, recording one changeset (not auto-revertible) | `tag_urn`, `confirm` |

`rollback` reverts the changes an `apply` made: it removes added tags/glossary terms/documentation links (keeping any that pre-existed in the before-image), restores a changed description, transitions the source insights to `rolled_back`, and marks the changeset rolled back. It is refused if the changeset is already rolled back, if a newer changeset has since modified the same aspect, or if the changeset touched change types whose prior state was not captured or is irreversible (column descriptions, structured properties, custom properties, incidents, curated queries, context documents, prompts, `delete_tag`, `bulk_untag`).

**Response Schema (apply):**

```json
{
  "changeset_id": "cs_x1y2z3a4b5c6d7e8f9a0b1c2d3e4f5a6",
  "entity_urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)",
  "changes_applied": 2,
  "insights_marked_applied": 1,
  "revertible": true,
  "resulting_state": {
    "description": "Order records with gross margin amounts (before returns)",
    "tags": ["urn:li:tag:gross-margin"],
    "glossary_terms": [],
    "owners": []
  },
  "message": "Changes applied to DataHub. Roll back with action=rollback changeset_id=cs_x1y2z3a4b5c6d7e8f9a0b1c2d3e4f5a6. changes_applied counts requested changes; verify against resulting_state below."
}
```

`revertible` is a boolean reflecting whether the changeset can be rolled back automatically (computed with the same all-or-nothing gate rollback enforces; any single unrevertible change makes the whole changeset `false`, so there is no partial rollback). When it is `false`, an `unrevertible_change_types` array names the change types with no before-image and the `message` states why instead of advertising a rollback. `list_changesets` entries carry the same two fields (and report `false` once a changeset has been rolled back).

See [Governance Workflow](../knowledge/governance.md) for detailed examples of each action.

---

## Portal Tools

The portal toolkit persists AI-generated assets to S3 with PostgreSQL metadata. Requires `portal.enabled: true`.

### save_asset

Save AI-generated content (JSX dashboard, HTML report, SVG chart, etc.) to the asset portal as a versioned asset. Captures provenance: the calls the asset was built from, read from the audit log at write time. See [Provenance](../server/provenance.md).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `name` | string | Yes | - | Display name (max 255 chars) |
| `content` | string | Yes | - | Artifact content |
| `content_type` | string | Yes | - | MIME type: `text/html`, `text/jsx`, `image/svg+xml`, `text/markdown`, `application/json`, `application/x-ndjson`, `text/csv`, `text/tab-separated-values`, `application/xml`, `application/yaml`, `application/sql`, `text/plain` |

A generic `content_type` (`text/plain` or `application/octet-stream`) is replaced
by the type detected from the content, so a JSON payload saved under a catch-all
type still opens in the JSON viewer. A specific declaration is always honored.
Detection can only reclassify content into passive families: it never promotes a
payload to `text/html`, `text/jsx` or `image/svg+xml`. See
[Content Types and Viewers](../server/content-viewers.md).
| `description` | string | No | `""` | Description (max 2000 chars) |
| `tags` | array | No | `[]` | Tags for categorization (max 20 tags, each max 100 chars) |
| `sources` | array | No | session window | The calls this asset was built from, as the `call_id` (or `mcp:call:<id>` reference) each query and API invocation returns. Replaces the default window; only the caller's own calls resolve (max 100) |

**Response Schema:**

```json
{
  "asset_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "portal_url": "https://portal.example.com/portal/assets/a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "message": "Artifact saved successfully.",
  "provenance_captured": true,
  "calls_recorded": 5
}
```

**Storage layout:**

Content is stored in S3 at `{s3_prefix}{user_id}/{asset_id}/content.{ext}` where the extension is derived from the content type.

---

### manage_asset

List, retrieve, update, delete, or share saved assets. All mutations enforce ownership: users can only modify their own assets.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | One of: `list`, `get`, `update`, `delete`, `search`, `share`, `list_shares`, `revoke_share` |
| `asset_id` | string | Conditional | - | Required for `get`, `update`, `delete`, `share`, `list_shares` |
| `content` | string | No | - | New content (for `update` — replaces S3 object) |
| `name` | string | No | - | New name (for `update`) |
| `description` | string | No | - | New description (for `update`) |
| `tags` | array | No | - | New tags (for `update`) |
| `content_type` | string | No | - | New content type (for `update`, only when replacing content) |
| `sources` | array | No | session window | The calls behind a content edit (`update`, `patch`), as `call_id` values or `mcp:call:<id>` references. Recorded as a new capture alongside the ones earlier versions carry |
| `query` | string | Conditional | - | Free-text relevance query (required for `search`) |
| `limit` | integer | No | 50 | Max results for `list` (max 200); ranked `search` defaults to 20 (max 100) |
| `recipient` | string | No | - | Who to share with (`share`): an email address, or a name resolved against the known-users directory. Omit for a link share |
| `permission` | string | No | `viewer` | `viewer` or `editor` (`share`). A link share is always `viewer` |
| `access_mode` | string | No | `authenticated` | Who a link share admits (`share`, no recipient): `authenticated` or `public` |
| `expires_in` | string | Conditional | - | Duration bounding a public link (`24h`). Required for `access_mode: public`, refused for every other share |
| `share_id` | string | Conditional | - | Share to end (required for `revoke_share`) |

**Actions:**

| Action | Description | Required Params |
|--------|-------------|-----------------|
| `list` | Show current user's assets | None |
| `get` | Retrieve full asset metadata | `asset_id` |
| `update` | Change metadata or replace content | `asset_id` |
| `delete` | Soft-delete an asset | `asset_id` |
| `search` | Rank the caller's own assets by relevance to `query` (hybrid vector + lexical, lexical-only fallback). Returns each match with a `score` plus a `ranking` field; scoped server-side to the caller's own assets by `owner_id` (the library's ownership key) and fails closed without an identity. | `query` |
| `share` | Grant access to an asset you own. With `recipient`: a restricted share addressed to that person, who is emailed the link. Without: a link, `authenticated` by default. Owner (or admin) only; anonymous callers refused. | `asset_id` |
| `list_shares` | The shares that currently grant access to the asset — revoked and expired ones excluded — with recipient, permission, access mode, view URL, and access count | `asset_id` |
| `revoke_share` | End one share by ID. Its token stops opening the asset immediately | `share_id` |

Registering an asset as a queryable table is the separate [`manage_table`](#manage_table) tool, which serves an uploaded resource through the same action.

**Response Schema (list):**

```json
{
  "assets": [
    {
      "id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
      "owner_id": "user@example.com",
      "name": "Revenue Dashboard",
      "description": "Monthly revenue breakdown",
      "content_type": "text/html",
      "s3_bucket": "portal-assets",
      "s3_key": "artifacts/user/asset-id/content.html",
      "size_bytes": 4096,
      "tags": ["dashboard", "revenue"],
      "provenance": {
        "user_id": "user@example.com",
        "session_id": "dps_abc123",
        "captures": [
          {
            "tool": "save_asset",
            "captured_at": "2026-01-15T10:05:00Z",
            "version": 1,
            "session_id": "dps_abc123",
            "event_ids": ["8kQ2f1uVQ2S1p0aT4Hn2Zw"],
            "calls": [
              {
                "event_id": "8kQ2f1uVQ2S1p0aT4Hn2Zw",
                "kind": "sql",
                "tool": "trino_query",
                "connection": "warehouse",
                "statement": "SELECT region, revenue FROM sales.quarterly",
                "purpose": "Totalling Q4 revenue by region for the board deck.",
                "outcome": "success",
                "duration_ms": 1840,
                "timestamp": "2026-01-15T10:00:00Z"
              }
            ]
          }
        ]
      },
      "created_at": "2024-01-15T10:05:00Z",
      "updated_at": "2024-01-15T10:05:00Z"
    }
  ],
  "total": 1
}
```

**Response Schema (update/delete):**

```json
{
  "asset_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "message": "Asset updated successfully."
}
```

**Error Codes:**

| Condition | Error Message |
|-----------|---------------|
| Missing asset_id | `asset_id is required for {action} action` |
| Asset not found | `asset not found: ...` |
| Wrong owner | `you can only {action} your own assets` |
| Invalid action | `invalid action "...": must be one of: list, get, update, delete` |

---

### manage_table

Make a stored CSV readable as a query-engine table over the directory the file already sits in, so `trino_query` can join it to warehouse tables. Nothing is copied or ingested.

The file is named by its `reference`, the string a `search` hit and a `fetch` document carry, so one action serves every kind of stored file and no argument names the kind.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | One of: `register`, `list`, `unregister` |
| `reference` | string | Conditional | - | The file: `mcp:resource:<id>` (uploaded reference material) or `mcp:asset:<id>` (saved asset). Required for `register` and `list` |
| `connection` | string | Conditional | - | Trino connection whose scratch schema holds the table (required for `register`) |
| `table_name` | string | No | filename slug | Name for the registered table; persona-prefixed either way |
| `follow` | boolean | No | `true` | Whether the table follows the file: each revision or version written over it moves the table onto the new contents. `false` pins the table to the version it is registered over |
| `repair` | boolean | No | `false` | Save a corrected version of a file that cannot be read as a table the way it is stored, and register that. The choice is kept on the registration: a following table corrects each later version carrying the same kind of defect and moves onto the corrected version |
| `registration_id` | string | Conditional | - | Registration to drop (required for `unregister`) |

**Actions:**

| Action | Description | Required Params |
|--------|-------------|-----------------|
| `register` | Create an external table over the file. Every column is `VARCHAR`, so the response carries a sample join showing the `CAST` | `reference`, `connection` |
| `list` | The tables registered over this file, each with its columns, whether it follows the file (`follow`), whether the file has moved on since (`stale`), and why a following table is behind (`follow_error`) | `reference` |
| `unregister` | Drop one registered table. The file itself is unchanged | `registration_id` |

**Response Schema (register):**

```json
{
  "reference": "mcp:resource:res_01HK7R9F",
  "registration_id": "reg_9f2c1d4b8a3e5602",
  "connection": "scratch",
  "query_table": "scratch.uploads.analyst_vendor_keys",
  "columns": ["store_id", "vendor_code", "rebate_pct"],
  "sample_sql": "SELECT ... CAST(u.store_id AS integer) ...",
  "registered_by": "analyst@example.com",
  "stale": false,
  "follow": true,
  "message": "Registered as scratch.uploads.analyst_vendor_keys on connection scratch. Every column is VARCHAR, so a join to a typed column needs a CAST. The table follows the file: each revision or version written moves it onto the new contents. Register with follow=false for a table pinned to this version."
}
```

**Error Codes:**

| Condition | Error Message |
|-----------|---------------|
| Missing reference | `reference is required for {action}: pass the mcp:resource: or mcp:asset: reference from a search hit, verbatim.` |
| Missing connection | `connection is required for register: ...` |
| Missing registration_id | `registration_id is required for unregister: ...` |
| Not a stored-file reference | `reference "..." is not a reference to a stored file: ...` |
| Missing, deleted, or not yours | `that reference names no stored file you can register` |
| No signed-in identity | `Registering a table needs a signed-in identity. ...` |
| Deployment cannot register | `This deployment cannot register tables: it needs a Trino connection with a scratch catalog and schema configured. ...` |

Who may register is authority to change the file, not to read it: an asset by its owner or an administrator, a resource by its uploader or an administrator of its scope. See [Registered Tables](../server/registered-tables.md).

---

### manage_resource

Write a file into the managed resource library. A managed resource is the only kind of file a saved asset can reference, so this is what makes the data half of a referencing asset refreshable by the platform rather than only by a person at an upload form: a scheduled script rewrites the CSV a dashboard reads, and the dashboard is not touched.

Content crosses the wire in one of two fields. `content` carries text — CSV, JSON, Markdown, SVG — and `content_base64` carries base64-encoded bytes for a binary file such as a PNG or a PDF. Exactly one is given, and both are capped at the deployment's `portal.max_content_size` (10 MB by default), the same cap `save_asset` applies.

A `create` declares what the bytes are in `content_type`; a create that does not is refused. The type is not detected on this path, because the families an agent writes most cannot be named from content: SVG, HTML, JSX and Markdown are all stored `text/plain` when nothing is declared, and `text/plain` under `nosniff` is a broken image or an unrendered document wherever an asset references the file. A `replace_content` keeps the type the resource already carries unless it declares a new one, so a refresh cannot reclassify a file under every reference to it. The types the platform stores are listed on the built-in knowledge page `mcp:knowledge_page:platform-content-types-for-stored-files` and in [Content Types and Viewers](../server/content-viewers.md).

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | - | One of: `create`, `replace_content` |
| `reference` | string | Conditional | - | The resource to write over: `mcp:resource:<id>`, from a `search` hit, a `fetch` document, or a `create` (required for `replace_content`) |
| `content` | string | Conditional | - | The file as text. This or `content_base64` |
| `content_base64` | string | Conditional | - | The file as base64-encoded bytes. This or `content` |
| `content_type` | string | Conditional | - | Media type the bytes are (required for `create`). Not detected: SVG, HTML, JSX and Markdown all read as plain text to a byte sniffer. `replace_content` keeps the type the resource already carries when it is omitted; a file stored under a generic type is re-detected from its bytes |
| `filename` | string | Conditional | - | Name of the file (required for `create`), normalized to lowercase with spaces replaced. `replace_content` ignores it |
| `display_name` | string | Conditional | - | Name shown in the resource library (required for `create`) |
| `path` | string | Conditional | - | The folder path the file is filed under inside its library (required for `create`), for example `datasets` or `datasets/media-manager/shows`. Slash-separated; each folder name is lowercase letters, digits and hyphens starting with a letter, at most 31 characters; at most 8 folders deep and 200 characters overall |
| `description` | string | Conditional | - | What the file is and what reads it (required for `create`) |
| `tags` | string[] | No | `[]` | Tags for filtering in the library |
| `scope` | string | No | `user` | `user`, `persona`, or `global` |
| `scope_id` | string | No | you | Persona name for `scope=persona`; must be empty for `scope=global` |
| `change_summary` | string | No | `Content replaced via manage_resource` | Why the content changed, shown in the version history beside the revision |

**Actions:**

| Action | Description | Required Params |
|--------|-------------|-----------------|
| `create` | File new content as a managed resource and report its `mcp://` URI and its `mcp:resource:` reference | `filename`, `display_name`, `path`, `description`, `content_type`, content |
| `replace_content` | Write new content over an existing resource, keeping its id, URI and filename, and record the change as its next version | `reference`, content |

**Response Schema (create):**

```json
{
  "resource_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "reference": "mcp:resource:a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "uri": "mcp://user/550e8400-e29b-41d4-a716-446655440000/datasets/weather-daily.csv",
  "filename": "weather-daily.csv",
  "display_name": "Daily Weather",
  "scope": "user",
  "scope_id": "550e8400-e29b-41d4-a716-446655440000",
  "category": "datasets",
  "content_type": "text/csv",
  "size_bytes": 2481,
  "message": "Created. Reference it from an asset by passing the uri above in save_asset's 'references', ..."
}
```

`replace_content` returns the same shape plus `version`, the number the content was recorded as, and `tables` when a table is registered over the file: one sentence per table, saying it followed onto the new version (`scratch.uploads.analyst_stores on scratch now reads version 7.`) or is pinned and now behind it, with the same sentences appended to `message`. A create reports no version: it records version 1 only where the deployment keeps a version trail, and a number the history may not hold is worse than none. See [Following the file](../server/registered-tables.md#following-the-file).

**Error Codes:**

| Condition | Error Message |
|-----------|---------------|
| Missing content type on a create | `content_type is required for create: name the media type the bytes are ...` |
| Missing content | `content is required: pass the file as text in 'content', or as base64-encoded bytes in 'content_base64' ...` |
| Both content fields | `pass content or content_base64, not both: ...` |
| Malformed base64 | `content_base64 is not valid base64: ...` |
| Over the size cap | `content size N exceeds maximum M bytes. A file this large has to be uploaded through the portal's resource library ...` |
| Scope refused | `you cannot write to the global scope, which is administrators only: managed-resource write refused` |
| Missing reference | `reference is required for replace_content: pass the mcp:resource:<id> reference ...` |
| Reference of another kind | `reference "..." names a target of type "asset", not "resource". ...` |
| Missing, deleted, or not visible | `there is no managed resource "<id>" you can see: no such managed resource` |
| No version trail | `this deployment keeps no version history for managed resources, so content cannot be replaced: managed-resource write unavailable` |
| No signed-in identity | `Writing a managed resource needs a signed-in identity. ...` |
| No managed-resource layer | `This deployment has no managed-resource library to write to: ... Nothing was saved.` |

Creating is scope authority — your own user scope, a persona you administer, or the global scope as a platform administrator — and a refusal names the scope rather than the file, because where it was filed is what the caller has to change. Replacing is the authority to change that file: its uploader, or an administrator of its scope. A resource you cannot see is answered as absent, whether it is missing, deleted, or somebody else's. A managed-script run is judged as the person it acts for: it authenticates as a principal that owns no file, so a create with no scope named files into its version author's library and a replacement reaches what that person uploaded ([Script security](../scripts/security.md#who-a-run-acts-for)). See [Asset References](../server/asset-references.md).

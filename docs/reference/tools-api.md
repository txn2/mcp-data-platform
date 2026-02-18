---
description: Complete API specification for all MCP tools. Parameters, response schemas, error codes for DataHub, Trino, and S3 operations.
---

# Tools API Reference

Complete specification for all MCP tools provided by mcp-data-platform.

## Trino Tools

### trino_query

Execute a read-only SQL query against the Trino cluster. Write operations (INSERT, UPDATE, DELETE, CREATE, DROP, etc.) are rejected before reaching Trino. Annotated with `ReadOnlyHint: true` for MCP client auto-approval.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to execute (read-only) |
| `limit` | integer | No | 1000 | Maximum rows to return (capped by max_limit config) |
| `connection` | string | No | first configured | Trino connection name |

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

When `read_only: true` is configured at the instance level, write operations are blocked.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to execute |
| `limit` | integer | No | 1000 | Maximum rows to return (capped by max_limit config) |
| `connection` | string | No | first configured | Trino connection name |

**Response Schema:** Same as `trino_query`.

---

### trino_explain

Get the execution plan for a SQL query.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to explain |
| `connection` | string | No | first configured | Trino connection name |

**Response Schema:**

```json
{
  "plan": "Query Plan\n- TableScan[table = ...]\n  ...",
  "format": "text"
}
```

---

### trino_list_catalogs

List available catalogs.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `connection` | string | No | first configured | Trino connection name |

**Response Schema:**

```json
{
  "catalogs": ["hive", "iceberg", "memory"]
}
```

---

### trino_list_schemas

List schemas in a catalog.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `catalog` | string | No | configured default | Catalog name |
| `connection` | string | No | first configured | Trino connection name |

**Response Schema:**

```json
{
  "catalog": "hive",
  "schemas": ["default", "sales", "marketing"]
}
```

---

### trino_list_tables

List tables in a schema.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `catalog` | string | No | configured default | Catalog name |
| `schema` | string | No | configured default | Schema name |
| `connection` | string | No | first configured | Trino connection name |

**Response Schema:**

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
| `connection` | string | No | first configured | Trino connection name |

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

### datahub_get_entity

Get detailed entity information.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Entity URN |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

```json
{
  "urn": "urn:li:dataset:...",
  "type": "dataset",
  "name": "orders",
  "description": "Customer orders from e-commerce platform",
  "platform": "trino",
  "created": "2024-01-01T00:00:00Z",
  "modified": "2024-01-15T12:00:00Z",
  "owners": [
    {"name": "Data Team", "type": "group", "email": "data@example.com"}
  ],
  "tags": ["pii", "financial"],
  "glossary_terms": ["Order", "Transaction"],
  "domain": {
    "urn": "urn:li:domain:sales",
    "name": "Sales"
  },
  "deprecation": null,
  "custom_properties": {
    "refresh_schedule": "daily"
  }
}
```

---

### datahub_get_schema

Get dataset schema.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Dataset URN |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

```json
{
  "urn": "urn:li:dataset:...",
  "fields": [
    {
      "name": "order_id",
      "type": "NUMBER",
      "native_type": "bigint",
      "nullable": false,
      "description": "Unique order identifier",
      "tags": ["pii"],
      "glossary_terms": ["Order ID"]
    }
  ],
  "primary_keys": ["order_id"],
  "foreign_keys": []
}
```

---

### datahub_get_lineage

Get data lineage.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Entity URN |
| `direction` | string | No | `downstream` | `upstream` or `downstream` |
| `depth` | integer | No | 3 | Maximum traversal depth |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

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

---

### datahub_get_queries

Get popular queries for a dataset.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Dataset URN |
| `limit` | integer | No | 10 | Maximum queries to return |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

```json
{
  "urn": "urn:li:dataset:...",
  "queries": [
    {
      "query": "SELECT * FROM orders WHERE status = 'completed'",
      "user": "analyst@example.com",
      "executed_at": "2024-01-15T10:00:00Z",
      "execution_count": 150
    }
  ]
}
```

---

### datahub_get_glossary_term

Get glossary term details.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Glossary term URN |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

```json
{
  "urn": "urn:li:glossaryTerm:Revenue",
  "name": "Revenue",
  "description": "Total monetary value from sales transactions",
  "parent": "urn:li:glossaryTerm:FinancialMetrics",
  "related_terms": ["Gross Revenue", "Net Revenue"],
  "custom_properties": {
    "calculation": "SUM(line_item_amount)"
  }
}
```

---

### datahub_list_tags

List available tags.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `limit` | integer | No | 100 | Maximum tags to return |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

```json
{
  "tags": [
    {"urn": "urn:li:tag:pii", "name": "pii", "description": "Contains PII"},
    {"urn": "urn:li:tag:financial", "name": "financial", "description": "Financial data"}
  ]
}
```

---

### datahub_list_domains

List data domains.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `limit` | integer | No | 100 | Maximum domains to return |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

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

---

### datahub_list_data_products

List data products.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `domain` | string | No | - | Filter by domain URN |
| `limit` | integer | No | 100 | Maximum products to return |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

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

### datahub_get_data_product

Get data product details.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Data product URN |
| `connection` | string | No | first configured | DataHub connection name |

**Response Schema:**

```json
{
  "urn": "urn:li:dataProduct:customer360",
  "name": "Customer 360",
  "description": "Unified customer view combining all customer data sources",
  "domain": {
    "urn": "urn:li:domain:marketing",
    "name": "Marketing"
  },
  "owners": ["Marketing Data Team"],
  "assets": [
    {"urn": "urn:li:dataset:customers", "name": "customers", "type": "dataset"},
    {"urn": "urn:li:dataset:customer_events", "name": "customer_events", "type": "dataset"}
  ],
  "custom_properties": {
    "sla": "99.9%",
    "refresh": "hourly"
  }
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

### capture_insight

Record domain knowledge shared during a session. Available to all personas when `knowledge.enabled: true`.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `category` | string | Yes | - | One of: `correction`, `business_context`, `data_quality`, `usage_guidance`, `relationship`, `enhancement` |
| `insight_text` | string | Yes | - | The knowledge to record (10-4000 characters) |
| `confidence` | string | No | `medium` | Confidence level: `high`, `medium`, `low` |
| `entity_urns` | array | No | `[]` | DataHub URNs this insight relates to (max 10) |
| `related_columns` | array | No | `[]` | Columns related to this insight (max 20) |
| `suggested_actions` | array | No | `[]` | Proposed catalog changes (max 5) |

**Suggested Action Schema:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `action_type` | string | Yes | One of: `update_description`, `add_tag`, `add_glossary_term`, `flag_quality_issue`, `add_documentation` |
| `target` | string | Yes | Target of the change (entity name, column name, or URL) |
| `detail` | string | Yes | Change detail (new description, tag name, term name, etc.) |

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
| `action` | string | Yes | One of: `bulk_review`, `review`, `synthesize`, `apply`, `approve`, `reject` |
| `entity_urn` | string | Conditional | Required for `review`, `synthesize`, `apply` |
| `insight_ids` | array | Conditional | Required for `approve`, `reject`; optional for `synthesize`, `apply` |
| `changes` | array | Conditional | Required for `apply` |
| `confirm` | bool | No | Required when `require_confirmation` is enabled |
| `review_notes` | string | No | Notes for `approve`/`reject` actions |

**Change Schema (for `apply` action):**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `change_type` | string | Yes | One of: `update_description`, `add_tag`, `add_glossary_term`, `flag_quality_issue`, `add_documentation` |
| `target` | string | Yes | Target of the change |
| `detail` | string | Yes | Change detail |

**Actions:**

| Action | Description | Required Params |
|--------|-------------|-----------------|
| `bulk_review` | Summary of all pending insights grouped by entity | None |
| `review` | Insights for a specific entity with current DataHub metadata | `entity_urn` |
| `approve` | Transition insights to approved status | `insight_ids` |
| `reject` | Transition insights to rejected status | `insight_ids` |
| `synthesize` | Structured change proposals from approved insights | `entity_urn` |
| `apply` | Write changes to DataHub with changeset tracking | `entity_urn`, `changes` |

**Response Schema (apply):**

```json
{
  "changeset_id": "cs_x1y2z3a4b5c6d7e8f9a0b1c2d3e4f5a6",
  "entity_urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)",
  "changes_applied": 2,
  "insights_marked_applied": 1,
  "message": "Changes applied to DataHub. Changeset cs_x1y2z3a4b5c6d7e8f9a0b1c2d3e4f5a6 recorded for rollback."
}
```

See [Governance Workflow](../knowledge/governance.md) for detailed examples of each action.

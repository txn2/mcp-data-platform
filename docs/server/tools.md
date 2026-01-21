# Available Tools

mcp-data-platform provides tools from three integrated toolkits. Each tool can be invoked by name through any MCP client.

## Tools Summary

| Toolkit | Tool | Description |
|---------|------|-------------|
| Trino | `trino_query` | Execute SQL queries |
| Trino | `trino_explain` | Get query execution plans |
| Trino | `trino_list_catalogs` | List available catalogs |
| Trino | `trino_list_schemas` | List schemas in a catalog |
| Trino | `trino_list_tables` | List tables in a schema |
| Trino | `trino_describe_table` | Get table schema and metadata |
| Trino | `trino_list_connections` | List configured Trino connections |
| DataHub | `datahub_search` | Search for datasets, dashboards, etc. |
| DataHub | `datahub_get_entity` | Get detailed entity information |
| DataHub | `datahub_get_schema` | Get dataset schema |
| DataHub | `datahub_get_lineage` | Get data lineage |
| DataHub | `datahub_get_queries` | Get popular queries for a dataset |
| DataHub | `datahub_get_glossary_term` | Get glossary term details |
| DataHub | `datahub_list_tags` | List available tags |
| DataHub | `datahub_list_domains` | List data domains |
| DataHub | `datahub_list_data_products` | List data products |
| DataHub | `datahub_get_data_product` | Get data product details |
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

---

## Trino Tools

### trino_query

Execute a SQL query against Trino.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to execute |
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

### trino_explain

Get the execution plan for a query without running it.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | Yes | - | SQL query to explain |
| `connection` | string | No | default | Connection name to use |

---

### trino_list_catalogs

List all available catalogs in the Trino cluster.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `connection` | string | No | default | Connection name to use |

---

### trino_list_schemas

List schemas in a catalog.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `catalog` | string | No | configured default | Catalog to list schemas from |
| `connection` | string | No | default | Connection name to use |

---

### trino_list_tables

List tables in a schema.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `catalog` | string | No | configured default | Catalog name |
| `schema` | string | No | configured default | Schema name |
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

Get upstream or downstream lineage for an entity.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `urn` | string | Yes | - | Entity URN |
| `direction` | string | No | `downstream` | `upstream` or `downstream` |
| `depth` | integer | No | 3 | Maximum traversal depth |
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

### datahub_list_tags

List available tags in DataHub.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `limit` | integer | No | 100 | Maximum tags to return |
| `connection` | string | No | default | Connection name to use |

---

### datahub_list_domains

List data domains.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `limit` | integer | No | 100 | Maximum domains to return |
| `connection` | string | No | default | Connection name to use |

---

### datahub_list_data_products

List data products.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `domain` | string | No | - | Filter by domain URN |
| `limit` | integer | No | 100 | Maximum products to return |
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

## Next Steps

- [Multi-Provider](multi-provider.md) - Use multiple connections
- [Cross-Injection](../cross-injection/overview.md) - Understand semantic enrichment
- [Tools API Reference](../reference/tools-api.md) - Complete API specification

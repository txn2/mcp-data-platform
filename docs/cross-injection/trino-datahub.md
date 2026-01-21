# Trino to DataHub Enrichment

When `trino_semantic_enrichment` is enabled, Trino tool results include semantic metadata from DataHub. This gives you business context alongside query results.

## What Gets Enriched

| Tool | Enrichment Trigger |
|------|-------------------|
| `trino_query` | Table names in the query |
| `trino_describe_table` | The described table |
| `trino_list_tables` | Each table in the list |

## Enriched Data

For each table identified, the platform fetches:

- **Description** - What this table contains
- **Owners** - Who is responsible for this data
- **Tags** - Classification labels (pii, financial, etc.)
- **Domain** - Business domain this data belongs to
- **Glossary Terms** - Business definitions applied to the table
- **Quality Score** - Data quality metric (0-1)
- **Deprecation** - Whether the table is deprecated and what to use instead

## Example: Describe Table

**Request:**
```
Describe the orders table
```

**Tool call:** `trino_describe_table` with table `hive.sales.orders`

**Response without enrichment:**
```
Column          | Type      | Nullable
----------------|-----------|----------
order_id        | bigint    | NO
customer_id     | bigint    | YES
order_date      | date      | YES
total_amount    | decimal   | YES
status          | varchar   | YES
```

**Response with enrichment:**
```
Column          | Type      | Nullable
----------------|-----------|----------
order_id        | bigint    | NO
customer_id     | bigint    | YES
order_date      | date      | YES
total_amount    | decimal   | YES
status          | varchar   | YES

---
Semantic Context:
- Description: Customer orders from the e-commerce platform
- Owners: Sales Data Team (sales-data@example.com)
- Tags: financial, pii
- Domain: Sales
- Quality Score: 0.94
- Glossary Terms: Order, Customer Transaction
```

## Example: Query Results

**Request:**
```
Show me orders from the last 7 days
```

**Tool call:** `trino_query` with:
```sql
SELECT order_id, customer_id, total_amount
FROM hive.sales.orders
WHERE order_date >= current_date - interval '7' day
LIMIT 100
```

**Response includes:**
```
[Query results...]

---
Semantic Context for hive.sales.orders:
- Description: Customer orders from the e-commerce platform
- Owners: Sales Data Team
- Quality Score: 0.94
- Note: This table contains PII (customer_id links to personal data)
```

## Deprecation Warnings

If a table is marked deprecated in DataHub, you'll see a warning:

```
⚠️ DEPRECATION WARNING:
This table (hive.sales.orders) is deprecated.
Reason: Migrated to new schema
Replacement: Use hive.sales.orders_v2 instead
Deprecated since: 2024-01-15
```

This helps prevent queries against outdated tables.

## Table Resolution

The enrichment middleware resolves table names from various formats:

| Query Format | Resolved Table |
|--------------|----------------|
| `orders` | Uses default catalog.schema |
| `sales.orders` | Uses default catalog |
| `hive.sales.orders` | Fully qualified |

The resolved table identifier is used to look up DataHub metadata.

## Configuration

Enable Trino semantic enrichment:

```yaml
injection:
  trino_semantic_enrichment: true

semantic:
  provider: datahub
  instance: primary      # Which DataHub instance to use
  cache:
    enabled: true
    ttl: 5m             # Cache lookups for 5 minutes
```

## DataHub URN Matching

The platform constructs a DataHub URN from the Trino table identifier:

```
Trino: hive.sales.orders
DataHub URN: urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)
```

For this to work, your DataHub must have ingested metadata from your Trino instance with matching identifiers.

## Handling Missing Metadata

If a table doesn't exist in DataHub:

- No semantic context is appended
- The original Trino result is returned unchanged
- No error is surfaced

This allows queries to work even for tables not yet cataloged.

## Column-Level Enrichment

When describing a table, column-level metadata can also be included:

```yaml
Column: customer_id
- Type: bigint
- Description: Unique customer identifier
- Tags: pii
- Glossary Term: Customer ID
```

This requires column-level annotations in DataHub.

## Performance

Each enriched request adds a DataHub API call. With caching enabled:

- First request: ~100-200ms additional latency
- Cached requests: ~1ms additional latency

Cache TTL of 5 minutes works well for most use cases since metadata changes infrequently.

## Troubleshooting

**No enrichment appears:**

1. Verify DataHub connection is configured
2. Check that the table exists in DataHub with matching URN
3. Ensure `trino_semantic_enrichment: true` is set
4. Check semantic provider instance matches your DataHub config

**Stale metadata:**

- Reduce cache TTL or disable caching
- Trigger a metadata refresh in DataHub

## Next Steps

- [DataHub → Trino](datahub-trino.md) - The reverse direction
- [Configuration](../reference/configuration.md) - All options

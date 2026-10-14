# Trino to Catalog Enrichment

When `trino_semantic_enrichment` is on (the default), Trino tool results carry
the curation the [platform catalog](../server/catalog.md) holds for the tables
they touch, so business context arrives with the data.

## What Gets Enriched

| Tool | Enrichment Trigger |
|------|-------------------|
| `trino_query` | Table names in the query |
| `trino_describe_table` | The described table |
| `trino_browse` | Each table in the list (when browsing tables) |

## Enriched Data

For each table identified, the platform reads the catalog dataset and appends a
`semantic_context` block:

- **urn** - The dataset's catalog reference, `mcp:dataset:(trino,<connection>,<catalog>.<schema>.<table>)`
- **description** - What this table contains
- **owners** - Who is responsible for this data
- **tags** - Classification labels (`pii`, `financial`, ...)
- **domain** - The business domain the table belongs to
- **glossary_terms** - Business definitions applied to the table
- **custom_properties** and **structured_properties** - Curated key/value facts
- **active_incidents** and **incidents** - Open data-quality incidents recorded on the table
- **last_modified** - When the dataset's curation last changed

Column-level curation (each column's description, tags and glossary terms) is
added as `column_context`. A column tagged `pii` is reported with `is_pii`, and
one tagged `pii` or `sensitive` with `is_sensitive`. On `trino_query`, only the
columns the SQL names are included unless `column_context_filtering` is off.

## Example: Describe Table

**Tool call:** `trino_describe_table` with table `hive.sales.orders`

The result carries the columns Trino reports, followed by:

```json
{
  "semantic_context": {
    "urn": "mcp:dataset:(trino,production,hive.sales.orders)",
    "description": "Customer orders from the e-commerce platform",
    "owners": [{"urn": "sales-data@example.com", "type": "user", "name": "sales-data@example.com", "email": "sales-data@example.com"}],
    "tags": ["financial", "pii"],
    "domain": {"urn": "mcp:domain:4f1c", "name": "Sales"},
    "glossary_terms": [{"urn": "mcp:glossary_term:9a2e", "name": "Order"}]
  },
  "column_context": {
    "customer_id": {
      "description": "Unique customer identifier",
      "tags": ["pii"],
      "is_pii": true,
      "is_sensitive": true
    }
  }
}
```

## Which Connection's Curation

A catalog dataset belongs to the connection that sees it: the same table seen
through two Trino connections is two datasets, each with its own curation. The
enrichment reads the dataset on the connection the call ran against, and
otherwise the first connection that catalogs a table at that path.

## Table Resolution

The enrichment middleware resolves table names from various formats:

| Query Format | Resolved Table |
|--------------|----------------|
| `orders` | Uses the connection's default catalog and schema |
| `sales.orders` | Uses the default catalog |
| `hive.sales.orders` | Fully qualified |

The catalog keys a dataset by the connection's own catalog, schema and table
names, so no name mapping is configured.

## Configuration

```yaml
enrichment:
  trino_semantic_enrichment: true    # Default: true
  column_context_filtering: true     # Only include SQL-referenced columns (default)
```

With a database, the platform catalog is the semantic provider; no `semantic:`
block is needed. `semantic.provider: noop` turns the layer off.

## Handling Missing Metadata

If the catalog holds no dataset for a table, no semantic context is appended
and the Trino result is returned unchanged. With `semantic_fallback: true`,
the platform searches the catalog for similar datasets and offers the closest
as a suggested match, marked `match_kind: semantic`.

## Curating What Enrichment Shows

Everything above is curation written on the dataset: in the portal's
**Knowledge > Catalog** section, through `apply_knowledge`, or through the
[portal catalog API](../server/catalog.md#the-portal-catalog). Enrichment
reads the catalog on every call, so a change is visible on the next one.

## Troubleshooting

**No enrichment appears:**

1. Check that the connection has been scanned: **Admin > Catalog > Scans**.
2. Check that the table is not excluded in the catalog settings.
3. Ensure `trino_semantic_enrichment` is not set to `false`.
4. Check that the dataset has curation; an uncurated dataset adds little.

## Next Steps

- [Catalog to Trino](catalog-trino.md) - The reverse direction
- [Configuration](../server/configuration.md) - All options

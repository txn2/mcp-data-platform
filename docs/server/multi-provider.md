# Multi-Provider Configuration

mcp-data-platform supports connecting to multiple instances of each service type. This allows you to:

- Query different Trino clusters (production, staging, data warehouse)
- Search across multiple DataHub instances
- Access different S3 accounts or regions

## Configuring Multiple Instances

Each toolkit section accepts multiple named instances:

```yaml
toolkits:
  trino:
    production:
      host: trino-prod.example.com
      port: 443
      user: analyst
      ssl: true
      catalog: hive
      connection_name: Production

    staging:
      host: trino-staging.example.com
      port: 443
      user: analyst
      ssl: true
      catalog: hive
      connection_name: Staging

    warehouse:
      host: trino-dw.example.com
      port: 443
      user: analyst
      ssl: true
      catalog: iceberg
      connection_name: Data Warehouse

  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}
      connection_name: Primary Catalog

    legacy:
      url: https://datahub-legacy.example.com
      token: ${DATAHUB_LEGACY_TOKEN}
      connection_name: Legacy Catalog

  s3:
    data_lake:
      region: us-east-1
      access_key_id: ${AWS_ACCESS_KEY_ID}
      secret_access_key: ${AWS_SECRET_ACCESS_KEY}
      connection_name: Data Lake

    archive:
      region: us-west-2
      access_key_id: ${ARCHIVE_AWS_ACCESS_KEY_ID}
      secret_access_key: ${ARCHIVE_AWS_SECRET_ACCESS_KEY}
      connection_name: Archive
```

## Using Connections in Tools

Every tool accepts a `connection` parameter to specify which instance to use:

```
Query the orders table in the staging environment
```

Tool call: `trino_query` with:
- `query`: `SELECT * FROM orders LIMIT 10`
- `connection`: `staging`

## Listing Available Connections

Use the `*_list_connections` tools to see configured instances:

- `trino_list_connections` - Lists all Trino connections
- `datahub_list_connections` - Lists all DataHub connections
- `s3_list_connections` - Lists all S3 connections

**Example response:**

```json
{
  "connections": [
    {
      "name": "production",
      "display_name": "Production",
      "host": "trino-prod.example.com",
      "catalog": "hive"
    },
    {
      "name": "staging",
      "display_name": "Staging",
      "host": "trino-staging.example.com",
      "catalog": "hive"
    }
  ]
}
```

## Default Connection

The first instance configured becomes the default. You can rely on this behavior or explicitly specify which instance to use for semantic providers:

```yaml
semantic:
  provider: datahub
  instance: primary    # Use the "primary" DataHub instance

query:
  provider: trino
  instance: production # Use the "production" Trino instance

storage:
  provider: s3
  instance: data_lake  # Use the "data_lake" S3 instance
```

## Cross-Injection with Multiple Providers

When you have multiple instances, the semantic enrichment uses the configured provider instances:

```yaml
semantic:
  provider: datahub
  instance: primary       # Semantic context comes from this instance

query:
  provider: trino
  instance: production    # Query availability checks use this instance

storage:
  provider: s3
  instance: data_lake     # Storage availability uses this instance

injection:
  trino_semantic_enrichment: true   # All Trino results get DataHub context
  datahub_query_enrichment: true    # DataHub results show Trino availability
```

With this configuration:

- Querying any Trino instance enriches results with metadata from the `primary` DataHub
- Searching any DataHub instance shows query availability from `production` Trino

## Environment-Specific Configuration

Use environment variables to manage different environments:

```yaml
toolkits:
  trino:
    main:
      host: ${TRINO_HOST}
      port: ${TRINO_PORT}
      user: ${TRINO_USER}
      password: ${TRINO_PASSWORD}
      ssl: true
```

Development:
```bash
export TRINO_HOST=localhost
export TRINO_PORT=8080
export TRINO_USER=admin
```

Production:
```bash
export TRINO_HOST=trino-prod.example.com
export TRINO_PORT=443
export TRINO_USER=service-account
export TRINO_PASSWORD="..."
```

## Connection-Specific Personas

You can restrict personas to specific connections using tool patterns:

```yaml
personas:
  definitions:
    analyst:
      display_name: Data Analyst
      roles: ["analyst"]
      tools:
        allow:
          - "trino_*"
          - "datahub_*"
        deny: []

    staging_user:
      display_name: Staging User
      roles: ["staging"]
      tools:
        allow:
          - "trino_query"       # Can query
          - "trino_list_*"      # Can explore schema
          - "trino_describe_*"  # Can describe tables
        deny:
          - "trino_explain"     # Cannot see execution plans
```

Note: Connection-level filtering requires custom middleware. The built-in persona system filters by tool name patterns only.

## Practical Examples

### Data Mesh Setup

```yaml
toolkits:
  datahub:
    sales:
      url: https://datahub-sales.example.com
      token: ${SALES_DATAHUB_TOKEN}
      connection_name: Sales Domain

    marketing:
      url: https://datahub-marketing.example.com
      token: ${MARKETING_DATAHUB_TOKEN}
      connection_name: Marketing Domain

    finance:
      url: https://datahub-finance.example.com
      token: ${FINANCE_DATAHUB_TOKEN}
      connection_name: Finance Domain
```

### Hybrid Cloud

```yaml
toolkits:
  s3:
    aws:
      region: us-east-1
      connection_name: AWS S3

    gcs:
      endpoint: https://storage.googleapis.com
      region: auto
      access_key_id: ${GCS_ACCESS_KEY}
      secret_access_key: ${GCS_SECRET_KEY}
      connection_name: Google Cloud Storage

    minio:
      endpoint: http://minio.internal:9000
      use_path_style: true
      disable_ssl: true
      access_key_id: ${MINIO_ACCESS_KEY}
      secret_access_key: ${MINIO_SECRET_KEY}
      connection_name: On-Premise MinIO
```

## Next Steps

- [Cross-Injection](../cross-injection/overview.md) - How enrichment works
- [Configuration Reference](../reference/configuration.md) - All configuration options

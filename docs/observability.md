---
title: Observability
description: Prometheus metrics exposed by mcp-data-platform — names, labels, cardinality, and how to scrape them.
---

# Observability

The platform exposes Prometheus metrics built on the OpenTelemetry metrics SDK
with a Prometheus exporter. Metrics are off by default; enable them by setting
`OTEL_METRICS_ENABLED=true`. The `/metrics` endpoint binds to a dedicated
listener, separate from the MCP/HTTP transport, on `OTEL_METRICS_ADDR` (default
`:9090`).

Deployment manifests for scraping and for the starter recording/alert rules
live in [`deployments/observability/`](https://github.com/txn2/mcp-data-platform/tree/main/deployments/observability).

## Enabling

| Env var | Default | Meaning |
|---------|---------|---------|
| `OTEL_METRICS_ENABLED` | `false` | Enable the recorder and the `/metrics` listener. |
| `OTEL_METRICS_ADDR` | `:9090` | Bind address for the `/metrics` HTTP listener. |

When disabled, every recording path is a no-op and the platform behaves exactly
as before.

## Exposed metrics

All names below are the **exposed** Prometheus names (the exporter appends
`_total` to counters and `_seconds` to duration histograms). Histograms also
expose `_bucket`, `_sum`, and `_count` series. Label cardinality is deliberately
bounded; raw URLs, query strings, request/response bodies, user UUIDs, and
session IDs are never used as labels (they belong on traces and in audit logs).

### MCP tool calls

| Metric | Type | Labels |
|--------|------|--------|
| `mcp_tool_calls_total` | counter | `tool`, `toolkit_kind`, `persona`, `status_category` |
| `mcp_tool_call_duration_seconds` | histogram | `tool`, `toolkit_kind`, `persona`, `status_category` |
| `mcp_inflight_tool_calls` | gauge | (none) |

### API gateway (apigateway toolkit)

| Metric | Type | Labels |
|--------|------|--------|
| `apigateway_outbound_total` | counter | `connection`, `http_status_class`, `status_category` |
| `apigateway_outbound_duration_seconds` | histogram | `connection`, `http_status_class`, `status_category` |
| `apigateway_inbound_requests_total` | counter | `connection`, `operation_id`, `method`, `status_class`, `identity` |
| `apigateway_inbound_duration_seconds` | histogram | `connection`, `operation_id`, `method`, `status_class` |

`identity` is recorded only on the inbound request counter, never on the
duration histogram, to keep the bucket series from multiplying by identity.

### Trino (query provider)

Recorded for the Trino queries and catalog/metadata calls the query provider
makes (cross-injection enrichment). User-facing `trino_query` tool calls are
also counted by `mcp_tool_calls_total{toolkit_kind="trino"}`.

| Metric | Type | Labels |
|--------|------|--------|
| `trino_queries_total` | counter | `status`, `query_kind` |
| `trino_query_duration_seconds` | histogram | `query_kind` |

`query_kind` is the SQL verb (`select`, `show`, `insert`, ...) for SQL queries,
or the metadata operation (`list_catalogs`, `list_schemas`, `list_tables`,
`describe_table`) for catalog calls; unknown SQL maps to `other`.

> A `trino_bytes_scanned_total` metric was considered but is not implemented:
> the mcp-trino client (v1.3.0) does not expose a bytes-scanned figure in its
> query stats, so there is no honest source for it.

### DataHub (semantic provider)

| Metric | Type | Labels |
|--------|------|--------|
| `datahub_requests_total` | counter | `operation`, `status` |
| `datahub_request_duration_seconds` | histogram | `operation` |

`operation` is one of `get_entity`, `get_schema`, `get_schemas`, `get_lineage`,
`get_column_lineage`, `get_glossary_term`, `get_queries`.

### S3 (S3 toolkit)

| Metric | Type | Labels |
|--------|------|--------|
| `s3_operations_total` | counter | `operation`, `status` |
| `s3_operation_duration_seconds` | histogram | `operation` |

`operation` is the S3 tool name (`list_buckets`, `list_objects`, `get_object`,
`get_object_metadata`, `presign_url`, ...).

### OAuth

| Metric | Type | Labels |
|--------|------|--------|
| `oauth_token_issuance_total` | counter | `grant_type`, `status` |
| `oauth_token_refresh_total` | counter | `status` |
| `oauth_token_refresh_duration_seconds` | histogram | (none) |

### Database connection pools

Reported at scrape time from each managed `*sql.DB`'s `Stats()`. The platform
shares one pool, registered under `pool="platform"`.

| Metric | Type | Labels |
|--------|------|--------|
| `db_pool_open_connections` | gauge | `pool` |
| `db_pool_in_use` | gauge | `pool` |
| `db_pool_idle` | gauge | `pool` |
| `db_pool_wait_count_total` | counter | `pool` |
| `db_pool_wait_duration_seconds_total` | counter | `pool` |

## Status labels

The `status` / `status_category` labels are a closed set so a status code or
error message can never inflate cardinality: `ok`, `auth_err`, `authz_err`,
`validation_err`, `upstream_err`, `internal_err`. Toolkit/provider metrics use
`ok` for success and `upstream_err` for a failed external call.

## Recording and alert rules

Starter rules ship in `deployments/observability/`. Recording-rule names follow
the `level:metric:operations` convention, e.g. `mcp:tool_call_duration:p95_5m`
and `apigateway:inbound_error_rate:5m`. See that directory's README for how to
load them and confirm scraping with `up{job="mcp-data-platform"}`.

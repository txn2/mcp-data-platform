# Changelog

All releases are documented on the [GitHub Releases page](https://github.com/txn2/mcp-data-platform/releases).

## Recent Changes

### Tool Visibility Filtering

Added config-driven `tools:` allow/deny filter on `tools/list` responses. Reduces LLM token usage by hiding unused tools from discovery. This is a visibility optimization, not a security boundary — persona auth continues to gate `tools/call`.

```yaml
tools:
  allow:
    - "trino_*"
    - "datahub_*"
  deny:
    - "*_delete_*"
```

### Admin Portal (v0.17.x)

Interactive web dashboard for platform administration. Enable with `admin.portal: true`. Provides audit log exploration, tool execution testing, and system monitoring at the admin path prefix.

### Admin REST API (v0.17.x)

HTTP endpoints for system health, configuration management, persona CRUD, auth key management, audit queries, and knowledge management. Supports three operating modes (standalone, file + DB, bootstrap + DB config). Interactive Swagger UI at `/api/v1/admin/docs/`.

### Knowledge Capture & Apply (v0.17.x)

Two MCP tools for domain knowledge lifecycle:

- `capture_insight`: Records domain knowledge during AI sessions (corrections, business context, data quality observations, usage tips, relationships, enhancements)
- `apply_knowledge`: Admin-only tool for reviewing, synthesizing, and applying insights to DataHub with changeset tracking and rollback

Admin REST API endpoints for managing insights and changesets outside the MCP protocol.

### Config Schema Versioning (v0.17.x)

`apiVersion` field in configuration files enables safe schema evolution. Migration tooling (`mcp-data-platform migrate-config`) converts between versions while preserving `${VAR}` references.

### Session Externalization (v0.17.x)

Externalize MCP session state to PostgreSQL for zero-downtime restarts and horizontal scaling. Configure with `sessions.store: database`. Includes session hijack prevention via token hash verification.

### Session Metadata Deduplication

Avoids repeating semantic metadata for previously-enriched tables within a session. Saves LLM context tokens on repeat queries to the same tables. Configurable modes: `reference`, `summary`, `none`.

### Query Enrichment Row Estimation

`COUNT(*)` row estimation in DataHub query enrichment is disabled by default to avoid expensive full-table scans on large datasets.

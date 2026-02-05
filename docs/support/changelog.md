# Changelog

All notable changes to mcp-data-platform are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Documentation overhaul
- S3 semantic enrichment (S3 results include DataHub metadata)
- DataHub storage enrichment (DataHub results include S3 availability)
- Storage provider interface for S3 integration

### Changed
- Improved cross-injection architecture with bidirectional enrichment

---

## [0.1.0] - Initial Release

### Added

#### Core Platform
- Platform orchestration with toolkit registry
- Middleware chain architecture for request processing
- Configuration via YAML with environment variable expansion

#### Toolkits
- **Trino Toolkit** - Integration with mcp-trino
  - `trino_query` - Execute SQL queries
  - `trino_explain` - Get query execution plans
  - `trino_list_catalogs` - List available catalogs
  - `trino_list_schemas` - List schemas in a catalog
  - `trino_list_tables` - List tables in a schema
  - `trino_describe_table` - Get table schema and metadata
  - `trino_list_connections` - List configured connections

- **DataHub Toolkit** - Integration with mcp-datahub
  - `datahub_search` - Search for entities
  - `datahub_get_entity` - Get entity details
  - `datahub_get_schema` - Get dataset schema
  - `datahub_get_lineage` - Get data lineage
  - `datahub_get_queries` - Get popular queries
  - `datahub_get_glossary_term` - Get glossary term details
  - `datahub_list_tags` - List available tags
  - `datahub_list_domains` - List data domains
  - `datahub_list_data_products` - List data products
  - `datahub_get_data_product` - Get data product details
  - `datahub_list_connections` - List configured connections

- **S3 Toolkit** - Integration with mcp-s3
  - `s3_list_buckets` - List S3 buckets
  - `s3_list_objects` - List objects in a bucket
  - `s3_get_object` - Get object contents
  - `s3_get_object_metadata` - Get object metadata
  - `s3_presign_url` - Generate pre-signed URL
  - `s3_list_connections` - List configured connections
  - `s3_put_object` - Upload object (when not read-only)
  - `s3_delete_object` - Delete object (when not read-only)
  - `s3_copy_object` - Copy object (when not read-only)

#### Cross-Injection
- Semantic enrichment middleware
- Trino → DataHub: Query results include semantic metadata
- DataHub → Trino: Search results include query availability

#### Providers
- Semantic provider interface with DataHub implementation
- Query provider interface with Trino implementation
- Storage provider interface with S3 implementation
- Caching decorator for semantic provider

#### Authentication
- OIDC authentication with any compliant provider
- API key authentication for service accounts
- OAuth 2.1 server with PKCE and DCR

#### Authorization
- Persona-based access control
- Tool filtering with allow/deny wildcard patterns
- Role mapping (OIDC roles → personas)
- Priority-based persona selection

#### Audit
- Tool call logging
- PostgreSQL-backed audit storage
- Configurable retention

#### Transport
- stdio transport for CLI integration
- SSE transport for remote access
- TLS support for SSE

### Dependencies
- github.com/modelcontextprotocol/go-sdk - MCP SDK
- github.com/txn2/mcp-trino - Trino toolkit
- github.com/txn2/mcp-datahub - DataHub toolkit
- github.com/txn2/mcp-s3 - S3 toolkit

---

## Version History

| Version | Release Date | Go Version | Notes |
|---------|--------------|------------|-------|
| 0.1.0 | TBD | 1.24+ | Initial release |

---

## Upgrade Guide

### Upgrading to 0.1.0

This is the initial release. No upgrade path required.

### Future Upgrades

Each release will include upgrade instructions for breaking changes. Generally:

1. Read the changelog for breaking changes
2. Update configuration if needed
3. Update Go dependency: `go get github.com/txn2/mcp-data-platform@latest`
4. Rebuild your application
5. Test before deploying to production

---

## Deprecation Policy

- Deprecated features are marked in the changelog
- Deprecated features remain functional for at least one minor version
- Breaking changes only occur in major version bumps
- Security fixes may require immediate changes regardless of version

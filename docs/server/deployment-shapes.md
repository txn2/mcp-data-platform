---
description: Which backends a mcp-data-platform deployment needs. The semantic stack for cross-enrichment, the API-and-knowledge shape that needs only PostgreSQL, and the combined deployment.
---

# Deployment Shapes

A deployment's shape is the set of backends it attaches. The shape determines which surfaces are available, and it is independent of the [operating mode](operating-modes.md), which is determined solely by whether `database.dsn` is set.

Cross-enrichment is the platform's core feature and needs a semantic layer. The gateways, knowledge layer, memory, portal, and `search`/`fetch` do not: they are database-backed and run with no catalog and no warehouse behind them. Both shapes are supported, and most deployments end up combining them.

## The shapes

| Shape | Backends | Core capability |
|-------|----------|-----------------|
| [Semantic stack](#semantic-stack) | DataHub, optionally Trino and S3 | Cross-enrichment: every data response carries business context |
| [API and knowledge](#api-and-knowledge) | PostgreSQL | Gateways, knowledge, memory, portal, universal search |
| [Combined](#combined) | Both | Cross-enrichment plus the database-backed surfaces |

## Semantic stack

The shape the platform was built for. DataHub supplies meaning, Trino supplies SQL access, S3 supplies object storage, and [cross-enrichment](../cross-enrichment/overview.md) wires them together so a response from one service carries context from the others. See [The Data Stack](../concepts/components.md) for why each component was chosen, and [Overview](overview.md) for the configuration.

DataHub is what cross-enrichment requires. Trino and S3 are optional additions to it.

## API and knowledge

A deployment with no data warehouse and no catalog. PostgreSQL is the one infrastructure requirement, because connections, catalogs, knowledge, memory, and portal assets are all database-backed.

What this shape provides:

- [API gateway](api-gateway.md) and [MCP gateway](gateway.md) connections, authored in the admin portal with encrypted credentials and OAuth grants
- [API catalogs](api-catalogs.md): versioned OpenAPI bundles with semantic endpoint ranking
- The [knowledge layer](../knowledge/overview.md): insight capture, human-in-the-loop review, and promotion to canonical knowledge pages
- The [memory layer](../memory/overview.md): persistent recall across sessions
- The [portal](portal-user.md): saved assets, collections, prompts, feedback threads, sharing
- `search` and `fetch` federating over knowledge pages, assets, prompts, connections, insights, and memory
- The full security and operations envelope: OIDC and API-key auth, the OAuth 2.1 server, personas, audit logging, observability, notifications

A minimal configuration:

```yaml
server:
  name: mcp-data-platform
  transport: http
  address: ":8080"

database:
  dsn: ${DATABASE_URL}

auth:
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: admin
        roles: ["admin"]

admin:
  enabled: true
  persona: admin

# API connections are authored in the admin portal and stored in the
# database; enabling the toolkit is all the YAML needs to say.
toolkits:
  api:
    enabled: true

personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
```

There is no `semantic:` or `query:` block. Omitting them selects the noop providers, which is a supported configuration rather than an error, and enrichment stays enabled at no cost because it no-ops without a semantic provider.

Starting with the configuration above and an empty database, `tools/list` returns twenty-two tools:

```
api_get_endpoint_schema   list_connections   manage_table          run_script
api_invoke_endpoint       manage_asset       memory_capture        save_asset
api_list_endpoints        manage_feedback    memory_manage         search
api_list_specs            manage_prompt      platform_find_tools   show_prompts
apply_knowledge           manage_resource    platform_info         show_scripts
fetch                     manage_script
```

`manage_table` and `manage_resource` are registered by the portal toolkit unconditionally, so both are listed here even though neither can act in this shape: `manage_table` reports that there is no Trino connection with a scratch target to register onto, and `manage_resource` that there is no managed-resource library to write to, since resource content needs blob storage and this configuration declares no S3 connection (see [Object storage is optional here too](#object-storage-is-optional-here-too) below). Both say so instead of reporting a write that did not happen.

### What this shape does not have

- **Cross-enrichment.** With no semantic provider there is no business context to add, so responses carry only what the called service returned.
- **Trino and S3 tools.** No `trino_*` or `s3_*` tools are registered, and no `datahub_*` tools.
- **Catalog search results.** `search` federates the database-backed sources; the technical catalog provider registers only when the semantic provider is DataHub.
- **Writing knowledge back to the catalog.** `apply_knowledge` with the default `sink: datahub` refuses on a deployment with no DataHub connection rather than reporting a write it cannot perform. Use `sink: knowledge_page` to promote captured knowledge to a canonical knowledge page, which is the catalog-free destination.

### Object storage is optional here too

Without an `s3_connection`, portal assets are stored in the database and managed-resource blob storage is disabled; the platform logs the fallback and starts normally. Add S3 when asset volume warrants it.

## Combined

Attach a warehouse and catalog to an API-and-knowledge deployment, or add API connections to a semantic-stack deployment, and both sets of surfaces are present at once. Neither addition is a migration: the semantic and query providers are selected by the `semantic:` and `query:` blocks, and API connections are database rows, so a deployment grows into this shape by configuration.

This is where cross-enrichment pays off most, because captured knowledge and proxied API responses sit alongside warehouse data under one auth, persona, and audit pipeline.

## Relationship to operating modes

Shape and mode are orthogonal:

| | Standalone (no database) | Database-backed |
|---|---|---|
| **Semantic stack** | DataHub, Trino, S3 tools with cross-enrichment. No knowledge, memory, portal, or gateways. | Everything. |
| **API and knowledge** | Not available: every surface in this shape is database-backed. | The shape described above. |

See [Operating Modes](operating-modes.md) for the full feature-availability comparison.

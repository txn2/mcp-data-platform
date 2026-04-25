# Gateway Toolkit

The gateway toolkit lets the platform act as an MCP **client** against arbitrary upstream MCP servers and re-expose their tools through the platform's own MCP server. Every proxied tool inherits the platform's authentication, persona enforcement, and audit logging — operators get one security envelope across every MCP they integrate.

!!! info "When to use the gateway"
    Use the gateway when you want a third-party MCP (vendor APIs, internal services, partner integrations) to participate in the platform's security model without being a separately managed MCP endpoint. If you only need the platform's native data access (DataHub, Trino, S3) the gateway is not required.

## Architecture

```mermaid
graph LR
    Client[AI Client] -->|tools/list, tools/call| Platform[mcp-data-platform]
    Platform -->|auth + persona check| Forwarder[Gateway Forwarder]
    Forwarder -->|tools/call| Upstream1[Upstream MCP A]
    Forwarder -->|tools/call| Upstream2[Upstream MCP B]
    Upstream1 -->|response| Forwarder
    Upstream2 -->|response| Forwarder
    Forwarder -->|enriched response| Platform
    Platform -->|response + audit| Client

    EnrichmentEngine[Enrichment Engine] -.->|context| Forwarder
    EnrichmentEngine -.-> TrinoSrc[Trino Source]
    EnrichmentEngine -.-> DataHubSrc[DataHub Source]
```

The forwarder dials each configured upstream once at startup, discovers its tool catalog, and re-registers every tool under a connection-namespaced local name (`<connection>__<remote_tool>`). Persona rules and audit middleware see proxied tools the same way they see native tools, with no special handling required.

## Terminology

- The platform IS the gateway — it owns the proxying behavior, the admin endpoints under `/api/v1/admin/gateway/*`, the enrichment-rule storage, and the docs you are reading.
- A **connection** of kind `mcp` is a single remote MCP server the gateway proxies to. Operators see `mcp` as a connection kind in the admin UI, alongside `trino`, `s3`, and `datahub`.

## Configuring connections

MCP connections live in the database, not in `platform.yaml`. Operators add and authenticate them through the admin portal (or directly via the admin REST API). Required for the kind to be active in YAML:

```yaml
toolkits:
  mcp:
    enabled: true
    # No instances here — connections are managed via the admin portal.
```

Once enabled, create a connection through the admin REST API:

```bash
curl -X PUT \
  -H "X-API-Key: $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "config": {
      "endpoint": "https://vendor.example.com/mcp",
      "auth_mode": "bearer",
      "credential": "your-vendor-token",
      "connection_name": "vendor"
    },
    "description": "Vendor analytics MCP"
  }' \
  https://platform.example.com/api/v1/admin/connection-instances/mcp/vendor
```

The credential field is encrypted at rest (AES-256-GCM) when `ENCRYPTION_KEY` is set. The connection name (`vendor` above) becomes the prefix for every proxied tool: a remote `get_contact` tool surfaces as `vendor__get_contact`.

### Authentication modes

| `auth_mode` | Header injected on outbound requests   |
|-------------|----------------------------------------|
| `none`      | none                                   |
| `bearer`    | `Authorization: Bearer <credential>`   |
| `api_key`   | `X-API-Key: <credential>`              |
| `oauth`     | `Authorization: Bearer <token>` (token acquired and refreshed automatically) |

Connections use a **shared service credential** per connection — one upstream identity for every platform user that hits the proxied tool. User-level attribution remains in the audit log; the upstream sees the connection's credential.

### OAuth 2.1

Two grants are supported:

| `oauth_grant`         | Use when                                                                  |
|-----------------------|----------------------------------------------------------------------------|
| `client_credentials`  | The upstream supports machine-to-machine credentials (no human in loop).   |
| `authorization_code`  | The upstream requires a browser sign-in (Salesforce Hosted MCP, etc.).     |

**`client_credentials`** — set `oauth_token_url`, `oauth_client_id`, `oauth_client_secret`, and `oauth_scope`. The platform fetches a token on first use and refreshes automatically as it expires. No human interaction required.

**`authorization_code`** — adds `oauth_authorization_url` and uses PKCE (RFC 7636). After saving the connection, click **Connect** in the admin portal. The browser is redirected to the upstream, you sign in, and the upstream redirects back to `/api/v1/admin/oauth/callback` with an authorization code. The platform exchanges the code for an access token + refresh token, encrypts both at rest, and persists them.

Once connected, the refresh token keeps the access token alive without further interaction — including for cron jobs and scheduled prompts running at 3 a.m. The platform reauthorizes silently every time the access token expires. Operators only need to click Connect again if the upstream invalidates the refresh token.

The OAuth token row lives in `gateway_oauth_tokens` (migration `000035`). When `ENCRYPTION_KEY` is set, both `access_token` and `refresh_token` are encrypted with AES-256-GCM. Without an encryption key, tokens are stored in plaintext and the admin UI surfaces a warning.

#### Salesforce Hosted MCP

Salesforce's Hosted MCP Server (Beta as of Dreamforce 2025) requires `authorization_code` + PKCE through an **External Client App (ECA)** with the `Web Server Flow` enabled.

1. In Salesforce Setup, create an External Client App with OAuth scopes `api`, `refresh_token`, and the MCP scope.
2. Set the callback URL to `https://<your-platform-host>/api/v1/admin/oauth/callback`.
3. In the platform admin portal, add an MCP connection of kind `mcp`:
    - `endpoint`: the Salesforce Hosted MCP URL
    - `auth_mode`: `oauth`
    - `oauth_grant`: `authorization_code`
    - `oauth_authorization_url`: `https://login.salesforce.com/services/oauth2/authorize` (or your domain)
    - `oauth_token_url`: `https://login.salesforce.com/services/oauth2/token`
    - `oauth_client_id` / `oauth_client_secret`: ECA consumer key/secret
    - `oauth_scope`: `api refresh_token <mcp scope>`
4. Save the connection, then click **Connect**. Sign in to Salesforce, approve the scopes, and the platform persists the tokens.
5. The connection's tools are now usable by any persona that allows them, including from scheduled cron prompts that run while no one is watching.

### Test and refresh endpoints

Two gateway-specific admin endpoints help operators manage connections:

- **Test connection** — `POST /api/v1/admin/gateway/connections/{name}/test` dials the upstream with a posted config (without saving) and returns the discovered tool list. Useful for validating credentials before persisting.
- **Refresh connection** — `POST /api/v1/admin/gateway/connections/{name}/refresh` re-dials a stored connection and re-registers its tools on the live MCP server. Use after an upstream changes its tool catalog.

Both endpoints respect the `[REDACTED]` placeholder for sensitive fields, so the admin UI can re-test an existing connection without re-entering secrets.

## Persona enforcement

Proxied tools are subject to persona rules with the same syntax as native tools. The double-underscore separator (`__`) makes gateway tools easy to target by pattern:

```yaml
personas:
  marketer:
    roles: ["marketing_team"]
    tools:
      allow:
        - "trino_query"           # Native: read warehouse
        - "vendor__list_*"        # Gateway: read vendor objects
        - "vendor__send_*"        # Gateway: trigger vendor sends
      deny:
        - "vendor__delete_*"      # Block destructive vendor calls
```

See [Tool Filtering](../personas/tool-filtering.md) for the full pattern grammar.

## Cross-enrichment rules

The gateway can run declarative enrichment rules that augment a proxied tool's response with context fetched from another platform source (Trino query, DataHub lookup). Rules let operators turn a vendor MCP from "tool that returns vendor data" into "tool that returns vendor data joined with the customer's warehouse context" — without writing Go code.

A rule has three structured fields stored as JSONB:

```json
{
  "tool_name": "vendor__get_contact",
  "when_predicate": { "kind": "response_contains", "paths": ["$.email"] },
  "enrich_action": {
    "source": "trino",
    "operation": "query",
    "parameters": {
      "connection": "warehouse",
      "sql_template": "SELECT lifetime_value, last_order_at FROM mart.customers WHERE email = :email",
      "email": "$.response.email"
    }
  },
  "merge_strategy": { "kind": "path", "path": "warehouse_signals" },
  "enabled": true
}
```

When `vendor__get_contact` returns a response containing `email`, the engine resolves `:email` from `$.response.email`, runs the SQL against the named Trino connection, and merges the result into `response.warehouse_signals`. The original response content is preserved; the enrichment lands in `StructuredContent` so the LLM sees both.

### Predicates

| `kind`               | Behavior                                                                  |
|----------------------|---------------------------------------------------------------------------|
| `always` (default)   | Rule fires on every successful tool call.                                 |
| `response_contains`  | Rule fires only when every JSONPath in `paths` resolves in the response.  |

### Sources

| Source     | Operation             | Parameters                                  |
|------------|-----------------------|---------------------------------------------|
| `trino`    | `query`               | `connection`, `sql_template`, `<bindings>`  |
| `datahub`  | `get_entity`          | `urn`                                       |
| `datahub`  | `get_glossary_term`   | `urn`                                       |

Bindings (any string parameter starting with `$.` or `$[`) are JSONPath expressions resolved against `{ args, response, user }`. SQL template `:name` placeholders are substituted with safely-quoted ANSI-SQL literals; single-quoted regions are skipped so timestamp literals are never mangled.

### Merge strategies

| `kind`         | Behavior                                                              |
|----------------|-----------------------------------------------------------------------|
| `path` (default) | Attaches the source result to `response[merge.path]`. Default path is `enrichment`. |

### Failure mode

Rule failures **never** break the parent tool call. Each warning is appended to the response as an additional `TextContent` entry prefixed `warning:`, and the unaltered response content is returned. This keeps enrichment opt-in — a misconfigured rule degrades gracefully.

### Authoring rules

Rules are persisted in `gateway_enrichment_rules` (migration `000034`) and managed through the admin REST API:

```
GET    /api/v1/admin/gateway/connections/{name}/enrichment-rules
POST   /api/v1/admin/gateway/connections/{name}/enrichment-rules
GET    /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}
PUT    /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}
DELETE /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}
POST   /api/v1/admin/gateway/connections/{name}/enrichment-rules/{id}/dry-run
```

The `dry-run` endpoint accepts a sample `{ args, response, user }` and returns the merged response plus per-rule traces (timing, errors). Use it from the admin UI's rule editor to validate bindings before going live.

## Failure isolation

A gateway upstream that's unreachable at startup logs a structured warning, records zero tools for that connection, and does **not** block platform startup. Other connections (gateway and native) keep working. Recovery requires either a platform restart (when the upstream is back) or a `refresh` admin call.

A connection that becomes unhealthy at runtime returns tool-error results prefixed `upstream:<connection>:` so the LLM can self-correct, and the audit log captures the failure with the same event shape as a successful call.

## What's next

- [Persona Tool Filtering](../personas/tool-filtering.md) — write composite personas with native + gateway tools.
- [Audit Logging](audit.md) — review proxied tool calls with the same query patterns as native tools.
- [Admin API](admin-api.md) — full REST reference for connection and enrichment-rule CRUD.

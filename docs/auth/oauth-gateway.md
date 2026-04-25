# OAuth to Upstream MCPs (Gateway)

This document covers OAuth **outbound** from the platform to a remote
MCP server it proxies. For OAuth **inbound** from clients (Claude
Desktop) to the platform, see [OAuth Server](oauth-server.md).

The two flows are separate, serve different purposes, and use
independent storage:

| Concern | OAuth inbound (this platform as **provider**) | OAuth outbound (this platform as **client**) |
|---------|----------------------------------------------|----------------------------------------------|
| Doc | [oauth-server.md](oauth-server.md) | this doc |
| Who is the OAuth provider? | this platform | the upstream MCP (Salesforce, vendor, etc.) |
| Who is the OAuth client? | Claude Desktop or another MCP client | this platform's gateway toolkit |
| Tokens stored at | `oauth_clients` (DCR) / session | `gateway_oauth_tokens` (encrypted) |
| Use case | "let Claude Desktop call this platform" | "let this platform call a third-party MCP" |

---

## Why outbound OAuth?

The [gateway toolkit](../server/gateway.md) lets the platform proxy
upstream MCP servers and re-expose their tools as
`<connection>__<remote_tool>`. Many useful upstreams (Salesforce
Hosted MCP, vendor-specific MCPs, partner integrations) require
OAuth 2.1 to authenticate. The platform handles that flow for the
operator: one-time browser sign-in, then unattended token refresh —
including for cron jobs and scheduled prompts.

## Supported grants

Two OAuth 2.1 grants are supported, configured per gateway connection
in the admin portal:

### `client_credentials` (machine-to-machine)

Use when the upstream issues a service token without human
interaction. Requires `oauth_token_url`, `oauth_client_id`,
`oauth_client_secret`, and (optionally) `oauth_scope`. The platform
fetches a token on first use and refreshes automatically as it
expires. No browser involved.

### `authorization_code` + PKCE (browser sign-in)

Use when the upstream requires a human sign-in (Salesforce, most
vendor MCPs). The platform implements PKCE per RFC 7636 with the
S256 challenge method. Adds `oauth_authorization_url` to the
configuration.

After saving the connection, the operator clicks **Connect** in the
admin portal:

```
1. Portal POST /api/v1/admin/gateway/connections/{name}/oauth-start
   → returns authorization URL with code_challenge, state, redirect_uri
2. Portal opens that URL in a new tab
3. Operator authenticates with the upstream
4. Upstream redirects browser to /api/v1/admin/oauth/callback?code=…&state=…
5. Platform exchanges the code for tokens at oauth_token_url
6. Tokens persisted to gateway_oauth_tokens (encrypted with ENCRYPTION_KEY)
7. Browser redirects back to /portal/admin/connections
```

Once connected, the access token is refreshed automatically using the
stored refresh token. Cron jobs and scheduled prompts run untouched
until the upstream invalidates the refresh token (operator clicks
**Reconnect** to re-authorize).

## Token storage

| Table | Holds | Encryption |
|-------|-------|------------|
| `gateway_oauth_tokens` | access_token, refresh_token, expires_at, authenticated_by | AES-256-GCM via `ENCRYPTION_KEY` |
| `oauth_pkce_states` | code_verifier (paired secret), state, redirect_uri | AES-256-GCM via `ENCRYPTION_KEY`; rows expire after 10 min |

`ENCRYPTION_KEY` is required for any production gateway deployment
using OAuth. Without it, tokens and verifiers are stored in plaintext
and the platform logs a warning.

## Multi-replica considerations

PKCE state must be visible across replicas: `oauth-start` may land on
replica A while the upstream's redirect lands the callback on replica
B. The platform automatically uses the Postgres-backed PKCE store when
a database is configured. Single-replica deployments can fall back to
the in-memory store.

## Salesforce Hosted MCP setup walkthrough

Salesforce's Hosted MCP requires an **External Client App (ECA)** with
the `Web Server Flow` enabled. Steps from a fresh org:

1. **Salesforce Setup** → External Client Apps → New External Client App.
2. Enable OAuth, set scopes: `api`, `refresh_token`, plus the MCP scope.
3. Set callback URL to `https://<your-platform-host>/api/v1/admin/oauth/callback`.
4. Save and capture the **consumer key** and **consumer secret**.
5. **In the platform admin portal** (`/portal/admin/connections`),
   click **+ Add Connection** and select kind **mcp**:
    - Endpoint: the Salesforce Hosted MCP URL
    - Auth mode: **OAuth 2.1**
    - Grant type: **authorization_code + PKCE**
    - Authorization URL: `https://login.salesforce.com/services/oauth2/authorize`
      (or your domain-specific URL)
    - Token URL: `https://login.salesforce.com/services/oauth2/token`
    - Client ID / Secret: the ECA consumer key / secret
    - Scope: `api refresh_token <mcp scope>`
6. Save, click **Connect**, sign in to Salesforce, approve the scopes.
7. The connection card now shows "Authorized by `<email>` `<time ago>`",
   the Salesforce-MCP tools are available as
   `salesforce__<tool>` in `tools/list`, and any persona that allows
   `salesforce__*` can call them.

The platform refreshes the access token automatically. Scheduled
prompts (cron jobs at 1 AM, etc.) run untouched until Salesforce
invalidates the refresh token.

## Related

- [Gateway Toolkit](../server/gateway.md) — full connection-config reference
- [Admin API: Gateway Endpoints](../server/admin-api.md#gateway-endpoints) — `oauth-start`, callback, connection CRUD
- [Admin Portal: MCP Gateway Connections](../server/admin-portal.md#mcp-gateway-connections) — UI walkthrough
- [Operating Modes](../server/operating-modes.md#mcp-gateway-requirements) — DB / encryption requirements

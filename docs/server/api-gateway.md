# API Gateway Toolkit

The API gateway toolkit (`kind: api`) proxies arbitrary REST/HTTP APIs through the platform's auth, persona, and audit pipeline. It is the HTTP/JSON sibling of the MCP [Gateway Toolkit](gateway.md), which proxies upstream MCP servers.

A single tool — `api_invoke_endpoint` — handles every operation on every configured API. Operators register the upstream as a `connection` of kind `api`; the model supplies method, path, query, body, and optional headers at call time. No tools are generated per endpoint, so adding ten APIs does not inflate the tool catalog by a thousand entries.

## When to use

Use the API gateway for upstreams that expose a REST API and authenticate with a bearer token, an API key, or OAuth 2.1. Common targets:

- Blackbaud SKY (constituent / fundraising APIs)
- Google APIs (Drive, Calendar, BigQuery REST surface)
- Salesforce REST API
- Internal HTTP services that should ride the platform's audit pipeline

For upstream **MCP** servers, use the MCP gateway (`kind: mcp`) instead.

## Configuring a connection

API connections are stored in the database, not in `platform.yaml`. Enable the kind, then author connections through the admin portal or the admin REST API.

```yaml
toolkits:
  api:
    enabled: true
    # No instances here — connections are managed via the admin portal.
```

Minimal connection config (bearer auth):

```bash
curl -X PUT \
  -H "X-API-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d '{
    "config": {
      "base_url": "https://api.vendor.example.com",
      "auth_mode": "bearer",
      "credential": "your-vendor-token"
    },
    "description": "Vendor REST API"
  }' \
  https://platform.example.com/api/v1/admin/connection-instances/api/vendor
```

### Auth modes

| `auth_mode` | What it sends |
|---|---|
| `none` | No outbound auth header |
| `bearer` | `Authorization: Bearer <credential>` |
| `api_key` | `<api_key_header>: <credential>` (header) or `?<api_key_param>=<credential>` (query) |
| `oauth2_client_credentials` | Token fetched at `oauth2_token_url`, applied as `Authorization: Bearer …` |
| `oauth2_authorization_code` | Browser sign-in once; refresh token persisted (encrypted); access tokens refreshed silently |

The OAuth 2.1 authorization-code grant completes via the platform's shared `/api/v1/admin/oauth/callback` endpoint, the same path the MCP gateway uses. Register that exact callback URL with the upstream IdP.

## Static headers

Some APIs require **both** an OAuth bearer **and** a separate header on every call. `auth_mode` is a single value, so the toolkit cannot satisfy that with `auth_mode` alone. `static_headers` is the second slot.

Headers listed under `static_headers` are attached to every outbound request, in addition to whatever `auth_mode` contributes. They are **operator-supplied**: the model cannot set, override, or read them, and validation refuses to load a connection whose `static_headers` would collide with the auth path.

### Encryption at rest

Header values are encrypted with AES-256-GCM via the platform's `FieldEncryptor` (same mechanism that protects `credential`, `client_secret`, etc.). Set `ENCRYPTION_KEY` to enable; without it, values are stored in plaintext just like every other sensitive field. The admin API redacts header values to `"[REDACTED]"` so the portal can edit other fields without ever showing the secret.

### Validation rules

- Header names must use only the RFC 7230 token character set (no spaces, no colons).
- Values cannot contain CR/LF/NUL (refused as a header-smuggling vector).
- Cannot set `Authorization` (use `auth_mode`).
- Cannot set the API-key header chosen by `auth_mode: api_key` (already managed).
- Cannot set hop-by-hop headers Go's net/http manages itself: `Host`, `Content-Length`, `Connection`, `Transfer-Encoding`, `Upgrade`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`.

The model is also blocked at request time from supplying a custom header whose name collides with any `static_headers` entry — the operator's header is authoritative.

### Header precedence

For each outbound request, headers are layered in this order (later wins):

1. Per-call headers from the tool input (`api_invoke_endpoint.headers`).
2. `static_headers` (operator-configured).
3. `auth_mode` contribution (`Authorization`, API-key header, etc.).

## Example: Blackbaud SKY

Blackbaud's SKY API requires **both** a user OAuth bearer **and** the application's `Bb-Api-Subscription-Key` header on every call.

```bash
curl -X PUT \
  -H "X-API-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d '{
    "config": {
      "base_url": "https://api.sky.blackbaud.com",
      "auth_mode": "oauth2_authorization_code",
      "oauth2_authorization_url": "https://oauth2.sky.blackbaud.com/authorization",
      "oauth2_token_url":         "https://oauth2.sky.blackbaud.com/token",
      "oauth2_client_id":         "your-blackbaud-app-id",
      "oauth2_client_secret":     "your-blackbaud-app-secret",
      "static_headers": {
        "Bb-Api-Subscription-Key": "your-subscription-key"
      }
    },
    "description": "Blackbaud SKY"
  }' \
  https://platform.example.com/api/v1/admin/connection-instances/api/blackbaud
```

Register `https://<platform-host>/api/v1/admin/oauth/callback` as a redirect URI on the Blackbaud application before clicking **Connect** in the portal.

## Example: Google APIs

Google APIs that bill quota against a separate project use the `x-goog-user-project` header alongside the OAuth bearer.

```json
"config": {
  "base_url": "https://www.googleapis.com",
  "auth_mode": "oauth2_authorization_code",
  "oauth2_authorization_url": "https://accounts.google.com/o/oauth2/v2/auth",
  "oauth2_token_url":         "https://oauth2.googleapis.com/token",
  "oauth2_client_id":         "your-google-client-id",
  "oauth2_client_secret":     "your-google-client-secret",
  "oauth2_scopes":            ["https://www.googleapis.com/auth/drive.readonly"],
  "static_headers": {
    "x-goog-user-project": "your-quota-project-id"
  }
}
```

## Example: Salesforce REST

Salesforce's REST API typically does not need a second header, but the same shape works when a Salesforce instance fronts the API with an API gateway that adds a subscription header.

```json
"config": {
  "base_url": "https://your-instance.my.salesforce.com",
  "auth_mode": "oauth2_authorization_code",
  "oauth2_authorization_url": "https://login.salesforce.com/services/oauth2/authorize",
  "oauth2_token_url":         "https://login.salesforce.com/services/oauth2/token",
  "oauth2_client_id":         "your-connected-app-consumer-key",
  "oauth2_client_secret":     "your-connected-app-consumer-secret",
  "oauth2_scopes":            ["api", "refresh_token"]
}
```

Add `refresh_token` to `oauth2_scopes` so Salesforce issues a refresh token — without it, the platform cannot keep the connection alive across access-token expiry.

## Admin portal

The admin portal's **Connections** page surfaces `static_headers` as a key/value editor under each `kind: api` connection. Existing values are masked (the portal never sees the cleartext secret after the first save); add or delete to change the set. Names remain visible so an operator can confirm which headers are configured without revealing the values.

# API Gateway Toolkit

The API gateway toolkit (`kind: api`) proxies arbitrary REST/HTTP APIs through the platform's auth, persona, and audit pipeline. It is the HTTP/JSON sibling of the MCP [Gateway Toolkit](gateway.md), which proxies upstream MCP servers.

The toolkit exposes three MCP tools — `api_invoke_endpoint`, `api_list_endpoints`, `api_get_endpoint_schema` — that handle every operation on every configured API. Operators register the upstream as a `connection` of kind `api`; the model uses `api_list_endpoints` to discover what's available, `api_get_endpoint_schema` to learn the precise parameter shape of one operation, and `api_invoke_endpoint` to make the call. No tools are generated per endpoint, so adding ten APIs does not inflate the tool catalog by a thousand entries.

OpenAPI specs that describe each upstream are stored separately in **API catalogs** — versioned, globally-owned bundles that many connections can reference. See [API Catalogs](api-catalogs.md) for the full surface.

## When to use

Use the API gateway for upstreams that expose a REST API and authenticate with a bearer token, an API key, or OAuth 2.1. Common targets:

- Salesforce REST API
- Google APIs (Drive, Calendar, BigQuery REST surface)
- GitHub REST API
- Stripe API
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

## REST gateway for non-MCP clients

`api_invoke_endpoint` is also reachable over plain HTTP for clients that do not speak MCP (e.g. Apache NiFi, Airflow's HttpOperator, a shell script with `curl`). The route is connection-scoped:

```
POST /api/v1/gateway/{connection}/invoke
```

Auth is the same as every other REST surface on the platform: `Authorization: Bearer <token>` or `X-API-Key: <key>`. The credential resolves to a user identity, persona, and audit subject through the same MCP middleware chain the MCP transport uses, so persona allowlists for `api_invoke_endpoint` and route-policy rules apply identically.

Request body (the `connection` is taken from the URL and overrides any value in the body):

```json
{
  "method":          "GET",
  "path":            "/v1/things",
  "query_params":    { "limit": 50 },
  "headers":         { "X-Trace": "abc" },
  "body":            null,
  "timeout_seconds": 30
}
```

Response: HTTP 200 with the toolkit's [`InvokeOutput`](https://github.com/txn2/mcp-data-platform/blob/main/pkg/toolkits/apigateway/invoke.go) shape. The upstream HTTP status is returned in `status`, not in the platform's response code:

```json
{
  "status":      200,
  "headers":     { "Content-Type": ["application/json"] },
  "body":        { "items": [ ... ] },
  "duration_ms": 245
}
```

Platform-level outcomes use HTTP status codes: `400` for a malformed request body, `401` for missing/invalid credentials, `403` for persona or route-policy denial, `404` for an unregistered connection, `500` for an internal failure. The split keeps "the platform refused" distinguishable from "the upstream returned 4xx/5xx" — a NiFi pipeline can route on the platform status and still inspect `status` inside the body for the upstream outcome.

The route is only mounted when at least one `kind: api` toolkit instance is loaded. When `auth.allow_anonymous` is `false`, requests without a credential are rejected at the HTTP layer before the in-memory MCP session is created.

### Apache NiFi example

Wire an `InvokeHTTP` processor to the gateway:

| Property | Value |
|---|---|
| HTTP Method | `POST` |
| URL | `https://platform.example.com/api/v1/gateway/vendor/invoke` |
| Content-Type | `application/json` |

Set an `X-API-Key` (or `Authorization`) attribute on the FlowFile and reference it from an `InvokeHTTP` dynamic property mapped to the header name. The FlowFile content is the JSON body above; downstream processors can use `EvaluateJsonPath` to lift `$.status` and `$.body` into attributes for the response-code routing relationships.

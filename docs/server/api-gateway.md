# API Gateway Toolkit

The API gateway toolkit (`kind: api`) proxies arbitrary REST/HTTP APIs through the platform's auth, persona, and audit pipeline. It is the HTTP/JSON sibling of the MCP [Gateway Toolkit](gateway.md), which proxies upstream MCP servers.

The toolkit exposes four MCP tools — `api_invoke_endpoint`, `api_list_endpoints`, `api_list_specs`, `api_get_endpoint_schema` — that handle every operation on every configured API. Operators register the upstream as a `connection` of kind `api`; the model uses `api_list_specs` to browse the sections of a multi-spec catalog, `api_list_endpoints` to discover the operations in one section, `api_get_endpoint_schema` to learn the precise parameter shape of one operation, and `api_invoke_endpoint` to make the call. No tools are generated per endpoint, so adding ten APIs does not inflate the tool catalog by a thousand entries.

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
| `basic` | `Authorization: Basic base64(username:password)` per RFC 7617. For legacy APIs (Jenkins, on-prem Jira / Confluence Server / DC, internal apps) that never moved to bearer or OAuth. `password` may be empty for the `token:` pattern some APIs use. |
| `oauth` | OAuth 2.1. The grant is set separately in `oauth_grant` (`client_credentials` or `authorization_code`). `client_credentials` fetches a token at `oauth_token_url` and applies `Authorization: Bearer ...`; `authorization_code` adds a one-time browser sign-in with a persisted (encrypted) refresh token and silent refresh. |
| `mtls` | No header. Authentication happens at the TLS handshake (RFC 5246 / 8446) via the configured client certificate. Used by upstreams that map the cert's subject DN to an internal user identity (service mesh peers, PKI-fronted internal APIs, healthcare integration engines, financial messaging endpoints, FedRAMP services, etc.). |

The OAuth config keys (`oauth_grant`, `oauth_token_url`, `oauth_authorization_url`, `oauth_client_id`, `oauth_client_secret`, `oauth_scope`, `oauth_prompt`, `oauth_endpoint_auth_style`) are shared with every other toolkit kind, so an OAuth connection is configured the same way regardless of kind. `oauth_scope` is a single space-delimited string (the OAuth 2.0 wire form).

The OAuth 2.1 authorization-code grant completes via the platform's shared `/api/v1/admin/oauth/callback` endpoint, the same path the MCP gateway uses. Register that exact callback URL with the upstream IdP.

> **Deprecated (still accepted).** Earlier api-gateway connections used an `oauth2_*` key prefix and encoded the grant in the `auth_mode` value (`oauth2_client_credentials` / `oauth2_authorization_code`), with `oauth2_scopes` as an array. Those are read as a fallback and rewritten to the canonical keys automatically by a database migration on upgrade; no reconnect is required. The fallback is scheduled for removal in a future release.

## Private CAs and mTLS

mTLS (RFC 5246 / 8446 client certificate authentication at the TLS handshake) is the standard way HTTPS clients authenticate when a header bearer is not enough. The toolkit supports it generically; nothing here is vendor-specific. Common targets:

- Service mesh peering (Istio, Linkerd, Consul Connect) where workload identity is a mesh-issued client cert.
- PKI-fronted enterprise APIs that pre-date OAuth.
- Healthcare integration engines (Mirth Connect, Rhapsody, InterSystems IRIS HealthShare).
- Financial messaging endpoints: SWIFT REST surfaces, Open Banking / FAPI, bank-direct payment APIs.
- FedRAMP / DoD-boundary services (DISA Cloud IL4/5) where a DoD-CA-issued cert is the access gate.
- HashiCorp Vault when the configured auth method is `cert/`.
- Kubernetes API server, etcd, and other PKI-bootstrapped infra.
- Apache Kafka REST Proxy, Schema Registry, NiFi, and similar Apache projects when deployed with the standard security profile.
- Any HTTPS service signed by a private CA the host does not carry by default (the CA-bundle half of this feature is useful on its own, even when no client cert is required).

Two TLS concerns live on every `kind: api` connection regardless of auth mode:

- **Outbound client certificate** (`mtls_client_cert_pem` + `mtls_client_key_pem`). The gateway presents this cert during the TLS handshake. With `auth_mode: mtls`, the cert IS the credential; with any other auth mode (bearer, api_key, basic, oauth2_*), the cert is layered on top.
- **Custom server CA trust** (`tls_ca_bundle_pem`). A PEM bundle appended to the system root pool when verifying the upstream's TLS certificate. Required when the upstream is signed by a private CA (corporate root, cluster-internal CA) that the host's default cert store does not carry. Public CAs remain trusted; the bundle never substitutes for the system roots.

Both are optional and orthogonal. An internal HTTPS service behind a private CA may only need the bundle; an upstream that requires mTLS but has a public TLS cert needs only the cert + key; an upstream that wants both (signed by a private CA AND requiring client auth) sets all three.

There is no `insecure_skip_verify` flag. To talk to a self-signed endpoint, paste the endpoint's CA into `tls_ca_bundle_pem`.

### Validation rules

- `mtls_client_cert_pem` and `mtls_client_key_pem` must be set together (or both empty). The toolkit refuses a connection with only one half of the pair.
- The cert and key must parse as PEM and the key must match the cert (`tls.X509KeyPair` runs a signature check at write time).
- Key strength is enforced: RSA must be at least 2048 bits, ECDSA must use one of P-256 / P-384 / P-521, Ed25519 is accepted. Smaller or non-NIST keys are rejected.
- `tls_ca_bundle_pem`, when set, must contain at least one parseable `CERTIFICATE` block. A bundle that contains only PRIVATE KEY blocks is rejected.
- `auth_mode: mtls` requires both cert and key.

### Encryption at rest

The private key (`mtls_client_key_pem`) is encrypted with AES-256-GCM via the platform's `FieldEncryptor` when `ENCRYPTION_KEY` is set. The cert and CA bundle are public material and stored in plain text. Admin API responses redact the private key as `[REDACTED]`; re-submitting the value `[REDACTED]` on a PUT preserves the existing key.

### Cert expiry surfacing

GET responses on `/api/v1/admin/connection-instances/api/{name}` include `mtls_cert_not_after` as an RFC3339 UTC timestamp parsed from the leaf cert. The portal renders an expiry badge from this field (green at 30 or more days remaining, amber under 30 days, red when expired). The badge is informational only; the toolkit does NOT refuse to make calls with an expired cert because the upstream's TLS layer will reject the handshake on its own and the model's error feedback loop is the right place to learn this.

### IdP behind a private CA

When `auth_mode` is `oauth` (either grant) and the IdP itself is signed by a private CA, set `tls_ca_bundle_pem` on the connection. The same bundle is honored by the token-exchange and refresh paths so token fetches succeed against private IdPs. Client mTLS material is NOT presented to the IdP; if your IdP requires a client cert at the token endpoint, that's a separate concern from upstream mTLS and is not yet supported.

### Configuring an mTLS connection

The shape is the same for every upstream: obtain a client cert + private key from the upstream's CA (or the CA that the upstream is configured to trust), give the gateway both PEMs plus the CA's cert, and select the right `auth_mode`. Below is a generic example using `openssl`; the curl call is identical for any upstream that wants mTLS.

```bash
# 1. Obtain (or mint, for testing) a client cert from the CA the upstream trusts.
#    In production this comes from your PKI tooling; for a smoke test, openssl
#    can mint a leaf signed by a CA you also control.

openssl req -new -newkey rsa:2048 -nodes \
  -keyout gw.key -out gw.csr \
  -subj "/CN=mcp-data-platform/OU=service"

openssl x509 -req -in gw.csr -CA upstream-ca.crt -CAkey upstream-ca.key \
  -CAcreateserial -out gw.crt -days 365 -sha256

# 2. Register the cert's identity with the upstream.
#    The exact step depends on the upstream: an Apache project may map the DN
#    in authorizations.xml; a service mesh ingress may bind the SPIFFE ID;
#    Vault's cert auth method matches against the cert directly. Whatever the
#    upstream's identity-mapping mechanism is, do it now.

# 3. Create the gateway connection.

curl -X PUT \
  -H "X-API-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d "$(jq -n \
        --arg cert "$(cat gw.crt)" \
        --arg key  "$(cat gw.key)" \
        --arg ca   "$(cat upstream-ca.crt)" '{
    config: {
      base_url:             "https://upstream.example.org",
      auth_mode:            "mtls",
      mtls_client_cert_pem: $cert,
      mtls_client_key_pem:  $key,
      tls_ca_bundle_pem:    $ca
    },
    description: "Internal HTTPS upstream behind private CA"
  }')" \
  https://platform.example.com/api/v1/admin/connection-instances/api/upstream

# 4. Verify the connection by hitting any path the upstream exposes.

curl -X POST -H "X-API-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d '{"method":"GET","path":"/healthz"}' \
  https://platform.example.com/api/v1/gateway/upstream/invoke
```

For upstreams that issue their own client certs via tooling (Apache NiFi's `tls-toolkit.sh`, Vault's PKI engine, cert-manager, your corporate PKI portal), substitute that tool's output for the `openssl` step. The toolkit only sees the PEM-encoded cert, key, and CA bundle.

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
  "auth_mode": "oauth",
  "oauth_grant":             "authorization_code",
  "oauth_authorization_url": "https://accounts.google.com/o/oauth2/v2/auth",
  "oauth_token_url":         "https://oauth2.googleapis.com/token",
  "oauth_client_id":         "your-google-client-id",
  "oauth_client_secret":     "your-google-client-secret",
  "oauth_scope":             "https://www.googleapis.com/auth/drive.readonly",
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
  "auth_mode": "oauth",
  "oauth_grant":             "authorization_code",
  "oauth_authorization_url": "https://login.salesforce.com/services/oauth2/authorize",
  "oauth_token_url":         "https://login.salesforce.com/services/oauth2/token",
  "oauth_client_id":         "your-connected-app-consumer-key",
  "oauth_client_secret":     "your-connected-app-consumer-secret",
  "oauth_scope":             "api refresh_token"
}
```

Add `refresh_token` to `oauth_scope` so Salesforce issues a refresh token — without it, the platform cannot keep the connection alive across access-token expiry.

## Admin portal

The admin portal's **Connections** page surfaces `static_headers` as a key/value editor under each `kind: api` connection. Existing values are masked (the portal never sees the cleartext secret after the first save); add or delete to change the set. Names remain visible so an operator can confirm which headers are configured without revealing the values.

## Memory safety and the in-flight budget

The gateway is a single shared process serving every connection and toolkit. Both buffering tools — `api_invoke_endpoint` (caps a single response at the connection's `max_response_bytes`, default 10 MiB) and `api_export` (caps a single export at `portal.export.max_bytes`, default 100 MiB) — read the response into memory. Per-request caps bound one call, but they do **not** bound the **sum** of concurrent calls: a burst of large responses, each under its own cap, can collectively exhaust the heap and get the container OOMKilled (exit 137), taking down every in-flight request on the pod.

The global **in-flight memory budget** closes that gap. It tracks the bytes committed to response buffering across all connections and both tools, and refuses a new buffered read — before allocating the buffer — when granting it would push the total past the ceiling. A refused request returns the structured `gateway_memory_budget_exhausted` error, which the REST shim maps to a retryable `429`.

```yaml
apigateway:
  memory:
    # Global ceiling on bytes committed to response buffering across all
    # api connections and both api_invoke_endpoint and api_export.
    # 0 = disabled (per-request caps still apply). A buffered read that
    # would exceed this is rejected with 429 before allocating.
    max_in_flight_bytes: 314572800     # 300 MiB

    # All-or-nothing cap for the /invoke-raw streaming route. An upstream
    # whose Content-Length exceeds this is rejected with 413 before any
    # bytes stream. 0 = no cap (streaming keeps memory bounded anyway).
    raw_max_bytes: 1073741824          # 1 GiB
```

Sizing `max_in_flight_bytes`: budget roughly **3× the raw body size** per concurrent large request (raw body + decoded copy + JSON-escaped envelope copy) and leave headroom for GC and the other toolkits' working set. A safe target keeps

```
max_in_flight_bytes ≈ (container_memory_limit × 0.6) / 3
```

so peak buffering stays well under the heap even at full utilization. Do **not** set it to the whole container limit or `GOMEMLIMIT` — that leaves no room for the transient marshalling copies or for GC. The raw passthrough route (below) is the memory-bounded path for legitimately large bodies and is exempt from the budget because it streams instead of buffering.

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

Platform-level outcomes use HTTP status codes: `400` for a malformed request body, `401` for missing/invalid credentials, `403` for persona or route-policy denial, `404` for an unregistered connection, `413` when a raw-mode body exceeds the configured cap, `429` when the global in-flight memory budget is momentarily exhausted, `502`/`504` for an unreachable or timed-out upstream, `500` for an internal failure. The split keeps "the platform refused" distinguishable from "the upstream returned 4xx/5xx" — a NiFi pipeline can route on the platform status and still inspect `status` inside the body for the upstream outcome.

Retry semantics follow the status: `413` is permanent (the same request cannot succeed) and must not be retried; `429` is transient (the budget drains as concurrent reads finish) and is safe to retry with backoff, with a `Retry-After` header on the response.

The route is only mounted when at least one `kind: api` toolkit instance is loaded. When `auth.allow_anonymous` is `false`, requests without a credential are rejected at the HTTP layer before the in-memory MCP session is created.

### Raw passthrough for large or binary bodies

`api_invoke_endpoint` buffers the upstream response and wraps it in the JSON envelope, which is the wrong shape for a large download or a binary object. For those, the REST shim offers a streaming passthrough on a separate route:

```
POST /api/v1/gateway/{connection}/invoke-raw
```

The request body is identical to `/invoke`. Instead of an `InvokeOutput` envelope, the gateway streams the upstream body straight to the client (`io.Copy`) with the upstream status code and `Content-Type`/`Content-Disposition`/`ETag`/`Cache-Control` headers, **still injecting the held upstream credential** — the caller never holds it. Memory stays bounded regardless of body size because the body is never buffered.

Auth, persona authorization, route policy, and audit apply identically to `/invoke`: the raw request flows through the same in-memory MCP session, so a persona scoped to `GET /v1/files/*` cannot stream from a denied path.

Size limit (all-or-nothing): when `apigateway.memory.raw_max_bytes` is set and the upstream's declared `Content-Length` exceeds it, the request is rejected with `413` **before any bytes are streamed**, carrying a structured body:

```json
{
  "error":        "upstream_body_too_large",
  "limit_bytes":  1073741824,
  "actual_bytes": 2147483648,
  "connection":   "vendor",
  "path":         "/v1/files/big.parquet"
}
```

For chunked responses (no `Content-Length`) the limit is enforced during the copy; because the status line is already sent it cannot become a `413`, so the stream is cut at the limit. Leave `raw_max_bytes` at `0` to disable the cap — streaming keeps memory bounded either way; the cap is a policy guard, not a memory guard.

### Apache NiFi example

Wire an `InvokeHTTP` processor to the gateway:

| Property | Value |
|---|---|
| HTTP Method | `POST` |
| URL | `https://platform.example.com/api/v1/gateway/vendor/invoke` |
| Content-Type | `application/json` |

Set an `X-API-Key` (or `Authorization`) attribute on the FlowFile and reference it from an `InvokeHTTP` dynamic property mapped to the header name. The FlowFile content is the JSON body above; downstream processors can use `EvaluateJsonPath` to lift `$.status` and `$.body` into attributes for the response-code routing relationships.

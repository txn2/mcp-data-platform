# Configuration

mcp-data-platform uses YAML configuration with environment variable expansion. Variables in the format `${VAR_NAME}` are replaced with their environment values at load time.

## How Configuration Works

**File mode** (default): Configuration is loaded from a YAML file at startup. This is the simplest deployment — no database required.

**File + database**: Adding `database.dsn` unlocks persistent platform features (audit logging, knowledge capture, session externalization). When a database is available, individual config entries stored in the `config_entries` table override file defaults for whitelisted keys. Changes made via the admin API take effect immediately without restart. File defaults are preserved and used as fallback when database entries are deleted.

| What you configure | What it unlocks |
|--------------------|-----------------|
| YAML file only | Read-only config, in-memory sessions, no audit |
| `database.dsn` | Audit logging, knowledge capture, OAuth persistence, database-backed sessions, per-key config overrides via admin API |
| `database.dsn` + `admin.enabled: true` | REST endpoints for system health, config entries CRUD, personas, auth keys, audit |

See [Operating Modes](operating-modes.md) for the full comparison and [Admin API](admin-api.md) for the REST endpoints.

### Unknown keys and strict parsing

By default the loader accepts a config file that contains keys it does not
recognize: each unknown key is logged as a prominent `WARN` at startup and then
ignored. This applies at every level that maps to a defined config field: a
stray key under `server:`, `auth.oidc:`, or inside a persona definition is
flagged just like a stray top-level key. This keeps older configs loading, but
it also means a typo or a renamed key silently does nothing.

Set `config.strict: true` to reject unknown keys with a hard error at startup
instead. This is recommended, since it turns typos and stale keys into an
immediate, actionable failure rather than a silent no-op:

```yaml
config:
  strict: true
```

Free-form maps are exempt because they accept arbitrary keys by design: the
`toolkits` tree, each toolkit's `config:` map, and the persona *names* under
`personas:` (the fields *within* a persona definition, such as `display_name`
and `tools`, are still validated). A future release will make strict rejection
the default; you will be able to opt back out with `config.strict: false`.

## Configuration File

Create a `platform.yaml` file:

```yaml
apiVersion: v1

server:
  name: mcp-data-platform
  transport: stdio

toolkits:
  trino:
    enabled: true
    instances:
      primary:
        host: trino.example.com
        port: 443
        user: ${TRINO_USER}
        password: ${TRINO_PASSWORD}
        ssl: true
        catalog: hive
        schema: default
    default: primary

  datahub:
    enabled: true
    instances:
      primary:
        url: https://datahub.example.com
        token: ${DATAHUB_TOKEN}
    default: primary

  s3:
    enabled: true
    instances:
      primary:
        region: us-east-1
        access_key_id: ${AWS_ACCESS_KEY_ID}
        secret_access_key: ${AWS_SECRET_ACCESS_KEY}
    default: primary

enrichment:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  s3_semantic_enrichment: true
  column_context_filtering: true     # Only include SQL-referenced columns (default: true)
```

> **Naming note:** the config-block key is `enrichment:`. The legacy
> `injection:` key still loads as a deprecated alias (with a warning) so
> existing configs keep working, but new configs should use `enrichment:`.
> The feature has always been about enriching tool responses with context.

## Config Versioning

Every configuration file should include an `apiVersion` field as the first key. This enables safe schema evolution with deprecation warnings and migration tooling.

```yaml
apiVersion: v1

server:
  name: mcp-data-platform
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiVersion` | string | `v1` | Config schema version. Omitting defaults to `v1` for backward compatibility. |

**Supported versions**: `v1` (current)

### Version Lifecycle

- **current**: Actively supported, no warnings
- **deprecated**: Still works, emits a warning at startup with migration guidance
- **removed**: Rejected at startup with an error pointing to the migration tool

### Migration Tool

Migrate config files to the latest version:

```bash
# From file to stdout
mcp-data-platform migrate-config --config platform.yaml

# From stdin to file
cat platform.yaml | mcp-data-platform migrate-config --output migrated.yaml

# Specify target version
mcp-data-platform migrate-config --config platform.yaml --target-version v1
```

The migration tool preserves `${VAR}` environment variable references.

## Server Configuration

```yaml
server:
  name: mcp-data-platform      # Server name reported to clients
  transport: stdio             # stdio or http
  address: ":8080"             # Listen address for HTTP transports
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | `mcp-data-platform` | Server name in MCP handshake |
| `version` | string | build-injected (`dev` in unlinked builds) | Server version reported to clients |
| `description` | string | - | Explains when to use this MCP server - which business, products, or domains it covers. Agents use this to route questions to the right MCP server; also shown in `platform_info` |
| `tags` | array | `[]` | Discovery keywords (company names, product names, business domains) that agents match against user questions |
| `transport` | string | `stdio` | Transport protocol: `stdio` or `http` (`sse` accepted for backward compatibility) |
| `address` | string | `:8080` | Listen address for HTTP transports |
| `tls.enabled` | bool | `false` | Enable TLS for HTTP transport |
| `tls.cert_file` | string | - | Path to TLS certificate |
| `tls.key_file` | string | - | Path to TLS private key |

!!! warning "HTTP Transport Security"
    When using HTTP transport without TLS, a warning is logged. For production deployments, always enable TLS to encrypt credentials in transit.

### Prompts

The platform registers MCP prompts at three levels:

1. **Auto-registered `platform-overview`** — Built dynamically from `server.description` and enabled toolkits. Lists what the platform can do based on which toolkits (DataHub, Trino, S3, Portal, Knowledge) are configured.

2. **Operator-configured prompts** — Defined in `server.prompts`. Support typed arguments with `{placeholder}` substitution in content.

3. **Workflow prompts** — Registered automatically when required toolkits are present. Provide guided multi-step workflows (e.g., `explore-available-data`, `create-interactive-dashboard`, `create-a-report`, `trace-data-lineage`).

Operator-configured prompts override any auto-registered prompt with the same name. Toolkits (Portal, Knowledge) may also register their own prompts via the `PromptDescriber` interface.

```yaml
server:
  description: "ACME Corp analytics platform"
  prompts:
    - name: routing_rules
      description: "How to route queries between systems"
      content: |
        Before querying, determine if you need ENTITY STATE or ANALYTICS...
    - name: explore-topic
      description: "Explore data about a specific topic"
      content: "Find all datasets related to {topic} and summarize key metrics."
      arguments:
        - name: topic
          description: "The topic to explore"
          required: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `server.prompts[].name` | string | required | Prompt name |
| `server.prompts[].description` | string | - | Prompt description |
| `server.prompts[].content` | string | required | Prompt content (supports `{arg_name}` placeholders) |
| `server.prompts[].arguments` | array | `[]` | Typed arguments for the prompt |
| `server.prompts[].arguments[].name` | string | required | Argument name (maps to `{name}` in content) |
| `server.prompts[].arguments[].description` | string | - | Argument description shown to clients |
| `server.prompts[].arguments[].required` | bool | `false` | Whether the argument is required |

**Built-in workflow prompts:**

| Prompt | Required Toolkits | Description |
|--------|-------------------|-------------|
| `explore-available-data` | DataHub | Discover datasets about a topic |
| `create-interactive-dashboard` | DataHub, Trino, Portal | Full workflow: discover, query, visualize, save |
| `create-a-report` | DataHub, Trino | Discover data, query it, produce a Markdown report |
| `trace-data-lineage` | DataHub | Trace upstream/downstream lineage for a dataset |

All registered prompts (platform + toolkit) are included in the `platform_info` tool response and visible in the platform-info app's Prompts tab.

### Streamable HTTP Configuration

The HTTP transport serves both legacy SSE (`/sse`, `/message`) and Streamable HTTP (`/`) endpoints. Streamable HTTP session behavior is configured under `server.streamable`:

```yaml
server:
  streamable:
    session_timeout: 30m
    stateless: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `session_timeout` | duration | `30m` | How long an idle session persists before cleanup |
| `stateless` | bool | `false` | Disable session tracking (no `Mcp-Session-Id` validation) |

## Authentication Configuration

```yaml
auth:
  allow_anonymous: false       # Require authentication (default)
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"
    audience: "mcp-data-platform"
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: "admin"
        roles: ["admin"]
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allow_anonymous` | bool | `false` | Allow unauthenticated requests |
| `oidc.enabled` | bool | `false` | Enable OIDC authentication |
| `oidc.issuer` | string | - | OIDC issuer URL |
| `oidc.client_id` | string | - | OAuth client ID |
| `oidc.audience` | string | - | Expected token audience |
| `oidc.role_claim_path` | string | `roles` | Path to roles in token claims |
| `oidc.role_prefix` | string | - | Filter roles to those with this prefix |
| `api_keys.enabled` | bool | `false` | Enable API key authentication |
| `api_keys.keys` | array | - | List of API key configurations |

Token clock skew is a fixed 30 seconds and is not exposed as a YAML setting.

The JWKS signing keys are fetched from the issuer at startup and cached for one hour. The cache self-heals on demand: a token whose key is missing because the cache has expired, or because the IdP rotated its keys, triggers a single refresh from the issuer, performed during that request's validation and honoring the request's own deadline. Concurrent requests collapse into one fetch, which runs independently so a slow issuer cannot pin request goroutines past their deadlines. Refreshes are throttled by the outcome of the last fetch: after a success the next on-demand refresh is at most once per minute, so a flood of unknown key IDs cannot hammer the issuer; after a failure a short recovery window applies so a brief issuer outage heals within seconds rather than being held down for the full minute. No restart is required after key rotation, and none of this is exposed as a YAML setting.

!!! note "Fail-Closed Security"
    Authentication follows a fail-closed model. Missing tokens, invalid signatures, expired tokens, or missing required claims (`sub`, `exp`) all result in denied access. If a JWKS refresh fails while the cache is expired, tokens are rejected rather than accepted unverified.

### Browser Sessions (OIDC Login for Portal UI)

When both `auth.oidc` and `auth.browser_session` are enabled, the portal UI offers SSO login via the configured OIDC provider. The flow uses authorization code with PKCE and stores the session in an HMAC-SHA256 signed JWT cookie.

```yaml
auth:
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/platform"
    client_id: "mcp-data-platform"
    client_secret: "${OIDC_CLIENT_SECRET}"
    audience: "mcp-data-platform"
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"
    scopes: [openid, profile, email]
  browser_session:
    enabled: true
    signing_key: "${SESSION_SIGNING_KEY}"  # openssl rand -base64 32
    ttl: 8h
    secure: true
    same_site: lax
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `browser_session.enabled` | bool | `false` | Enable cookie-based browser sessions |
| `browser_session.signing_key` | string | - | Base64-encoded HMAC key (32+ bytes) |
| `browser_session.ttl` | duration | `8h` | Session lifetime |
| `browser_session.secure` | bool | `true` | HTTPS-only cookies (set `false` for local dev) |
| `browser_session.cookie_name` | string | `mcp_session` | Cookie name |
| `browser_session.domain` | string | - | Cookie domain restriction |
| `browser_session.same_site` | string | `lax` | Cookie `SameSite` mode: `lax`, `strict`, or `none`. `none` requires `secure: true` and disables the browser's built-in CSRF defense (see below) |

The portal UI automatically detects OIDC availability and shows an SSO button. API key authentication remains as a fallback. MCP protocol clients are unaffected — browser sessions only apply to the portal HTTP endpoints.

!!! warning "Session Limitations"
    Sessions are stateless (no server-side store). Individual sessions cannot be revoked. Rotating `signing_key` invalidates all active sessions. Users must re-authenticate after TTL expires.

#### CSRF Protection

Because portal, admin, and managed-resources mutations can be authenticated by the session cookie, which the browser attaches automatically, the platform enforces token-based CSRF protection on cookie-authenticated, state-changing requests (`POST`, `PUT`, `PATCH`, `DELETE`):

- On login, `GET /api/v1/portal/me` returns a `csrf_token` bound to the session (an HMAC over the session subject under the signing key; stateless, no server store).
- The SPA echoes it in the `X-CSRF-Token` header on every non-`GET` request. Requests missing or presenting an invalid token are rejected with `403`.
- Read-only requests (`GET`, `HEAD`, `OPTIONS`) are exempt, as are API-key / Bearer-authenticated requests, because those credentials are not attached automatically by the browser and so are not vulnerable to CSRF.

`SameSite=Lax` (the default) is retained as defense-in-depth. Setting `same_site: none` removes that browser-level defense and makes the `X-CSRF-Token` check the sole protection; the platform logs a startup warning in that case.

### OAuth 2.1 Server (Inbound)

The built-in `oauth:` block turns the platform itself into an OAuth 2.1 authorization server, for clients like Claude Desktop that expect to sign in directly to the MCP server rather than through an existing OIDC provider. For most deployments, `auth.oidc` or `auth.api_keys` above are simpler and sufficient. See [OAuth 2.1 Server](../auth/oauth-server.md) for the full config reference, Dynamic Client Registration guidance, and setup walkthrough.

#### Rate limiting

The unauthenticated `/token` and `/register` endpoints are rate limited by default. `/token` runs a bcrypt compare per attempt and `/register` runs a bcrypt hash plus a database insert per request, so both are CPU (and, for `/register`, storage) amplification levers. Each endpoint has a per-client-IP limit plus an internal global backstop that bounds total throughput regardless of how requests attribute to IPs.

```yaml
oauth:
  rate_limit:
    enabled: true                 # default: true; set false to disable limiting
    trusted_proxies:              # CIDRs whose X-Forwarded-For is trusted
      - "10.0.0.0/8"
    token:
      requests_per_minute: 60     # default: 60
      burst: 10                   # default: 10
    register:
      requests_per_minute: 10     # default: 10
      burst: 3                    # default: 3
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rate_limit.enabled` | bool | `true` | Enable rate limiting for `/token` and `/register` |
| `rate_limit.trusted_proxies` | list | `[]` | CIDRs whose `X-Forwarded-For` is trusted for client attribution. Empty trusts none: the direct peer address is used and forwarding headers are ignored. Set this to your ingress/load-balancer CIDRs so per-client limiting works behind a proxy without being spoofable |
| `rate_limit.token.requests_per_minute` | int | `60` | Per-IP `/token` limit |
| `rate_limit.token.burst` | int | `10` | Per-IP `/token` burst allowance |
| `rate_limit.register.requests_per_minute` | int | `10` | Per-IP `/register` limit |
| `rate_limit.register.burst` | int | `3` | Per-IP `/register` burst allowance |

On limit, the endpoint returns HTTP 429 with a `Retry-After` header and an `{"error":"slow_down"}` JSON body. The global backstop for each endpoint is sized at ten times its per-IP rate and burst.

Dynamically-registered (DCR) clients that are never issued a token are reaped 24 hours after registration by the OAuth store's cleanup routine, bounding `oauth_clients` growth from the unauthenticated `/register` endpoint. Pre-registered (config-file) clients are never eligible.

## Database Configuration

The `database` block configures the PostgreSQL connection used by audit logging, knowledge capture, session externalization, OAuth persistence, and (optionally) the config store.

```yaml
database:
  dsn: ${DATABASE_URL}
  max_open_conns: 25
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dsn` | string | - | PostgreSQL connection string |
| `max_open_conns` | int | `25` | Maximum open database connections |

!!! note "What the database unlocks"
    Setting `dsn` enables audit logging, knowledge capture, session externalization, and OAuth persistence. Without it, these features degrade to in-memory or noop implementations.

## Config Store

When a database is available (`database.dsn` is set), the platform uses a granular key/value config store. Individual config entries in the `config_entries` table override file defaults for whitelisted keys. Changes made via the admin API take effect immediately (hot-reload) without restart. Deleting a database entry restores the file default for that key.

**Whitelisted keys (phase 1):**

| Key | Description |
|-----|-------------|
| `server.description` | Platform description shown in `platform-overview` prompt and `platform_info` tool |
| `server.agent_instructions` | Business/deployment context layered beneath the platform-owned instruction baseline (see below) |

Only whitelisted keys can be set via the admin API. Attempting to set a non-whitelisted key returns `400 Bad Request`.

### Agent instruction composition

The instructions an agent receives via `platform_info` are composed in layers:

```
[platform baseline]          platform-owned, versioned with the release, always present:
                             how to operate (search-first / topology discovery, capture
                             proactively). Names only tools the caller's persona can reach.
  + server.agent_instructions   admin: business/deployment context (which backends hold what,
                                 data origins, domain rules)
  + persona suffix/override      persona tuning (override replaces the admin layer only)
  + runtime notes                e.g. the uploaded-resources hint
```

The platform baseline is non-overridable and updates automatically when the platform is upgraded, so the operating model never has to be re-authored per deployment. A persona's `agent_instructions_override` replaces the admin layer only; the baseline is always present. Because the baseline names a tool (`search`, `memory_capture`) only when that tool is registered and the persona is allowed to call it, it never points an agent at a tool it cannot use. The agent receives the baseline as part of the composed `agent_instructions` in the `platform_info` response; admins can see the baseline on its own read-only in the portal's Agent Instructions screen and via `GET /api/v1/admin/config/agent-instructions-baseline`.

```yaml
config_store:
  mode: file      # Deprecated — ignored. Presence is accepted for backward compatibility.
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `config_store.mode` | string | `file` | **Deprecated.** Ignored at runtime. The config entries system activates automatically when `database.dsn` is set. Accepted without error for backward compatibility. |

See [Operating Modes](operating-modes.md) for the full comparison of deployment configurations.

## Tool Visibility Configuration

The `tools` block controls which tools appear in `tools/list` responses. This is a **visibility filter** for reducing LLM token usage — it hides tools from discovery but does not affect authorization. Persona-level tool filtering (see [Tool Filtering](../personas/tool-filtering.md)) remains the security boundary for `tools/call`.

```yaml
tools:
  allow:
    - "trino_*"
    - "datahub_*"
  deny:
    - "*_delete_*"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tools.allow` | array | `[]` | Tool name patterns to include in `tools/list` |
| `tools.deny` | array | `[]` | Tool name patterns to exclude from `tools/list` |
| `tools.description_overrides` | map | `{}` | Override tool descriptions in `tools/list` (key: tool name, value: description text). Config values take precedence over built-in defaults, e.g. the built-in `trino_query`/`trino_execute` overrides that guide agents to call `search` first |

**Semantics:**

- No patterns configured: all tools visible (default)
- Allow only: only matching tools appear
- Deny only: all tools appear except denied
- Both: allow patterns are evaluated first, then deny removes from that set

Patterns use `filepath.Match` syntax — `*` matches any sequence of non-separator characters. For example, `trino_*` matches `trino_query`, `trino_execute`, and `trino_describe_table`.

!!! tip "When to use this"
    Deployments that only use a subset of toolkits (e.g., only Trino) can hide unused tools to save tokens. A full tool list is 26-33 tools; filtering to `trino_*` reduces it to 8.

!!! warning "Not a security boundary"
    Tool visibility filtering only affects `tools/list` responses. A user who knows a tool name can still call it via `tools/call` if their persona allows it. Use persona tool filtering for access control.

## Admin API Configuration

The `admin` block enables and configures the REST API for system health, configuration management, persona CRUD, auth key management, and audit queries.

```yaml
admin:
  enabled: true
  persona: admin
  path_prefix: /api/v1/admin
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable admin REST API. Set `false` to disable. The routes always require admin-role auth to use, so enabling exposes the route, not open access. |
| `persona` | string | `admin` | Persona required for admin access |
| `path_prefix` | string | `/api/v1/admin` | URL prefix for admin endpoints |

!!! note "HTTP transport required"
    The admin API is served over HTTP. It is not available when running in `stdio` transport mode.

The admin portal provides a web-based dashboard for audit log exploration, tool execution testing, and system monitoring. Enable with `portal.enabled: true`. When enabled, it is served at `/portal/`. See [Admin API](admin-api.md) for the full endpoint reference and [Admin Portal](admin-portal.md) for the visual guide.

## Portal Configuration

The `portal` block enables the asset portal - the web UI plus REST API that persists AI-generated artifacts (JSX dashboards, HTML reports, SVG charts, exports) to S3 with PostgreSQL metadata tracking. See [Admin Portal](admin-portal.md) for branding and public-viewer walkthroughs and [User Portal](portal-user.md) for the end-user feature tour.

```yaml
portal:
  enabled: true
  title: "ACME Data Platform"                     # Sidebar/branding title
  tagline: "Sign in to access your data."         # Login-screen subtitle
  oidc_button_label: "Sign in with ACME Keycloak" # Login-screen SSO button text
  logo: https://example.com/logo.svg              # Logo URL (fallback for both themes)
  logo_light: https://example.com/logo-light.svg  # Logo for light theme
  logo_dark: https://example.com/logo-dark.svg    # Logo for dark theme
  s3_connection: primary        # S3 toolkit instance for artifact storage
  s3_bucket: portal-artifacts   # Bucket for artifact content
  s3_prefix: "artifacts/"       # Key prefix within the bucket
  public_base_url: "https://portal.example.com"   # Base URL for portal links
  max_content_size: 10485760    # Max artifact size in bytes (default: 10MB)
  implementor:                                    # Optional implementor brand (left zone of public viewer header)
    name: "ACME Corp"
    logo: "https://acme.com/logo.svg"
    url: "https://acme.com"
  rate_limit:                                     # Public portal viewer rate limiting
    requests_per_minute: 60
    burst_size: 10
  export:                                         # trino_export configuration
    enabled: true                                 # auto-enabled when portal + trino are configured
    max_rows: 100000                              # hard row cap per export
    max_bytes: 104857600                          # hard byte cap (100 MB)
    default_timeout: "5m"                         # default query timeout
    max_timeout: "10m"                             # maximum allowed timeout
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the portal SPA frontend and artifact API |
| `title` | string | `MCP Data Platform` | Sidebar/branding title text |
| `tagline` | string | `Sign in to access the platform.` | Login-screen subtitle text |
| `oidc_button_label` | string | `Sign in with OIDC` | Login-screen SSO button text |
| `logo` | string | - | URL to logo image (used for both themes if no theme-specific logo is set) |
| `logo_light` | string | - | URL to logo for light theme (overrides `logo`) |
| `logo_dark` | string | - | URL to logo for dark theme (overrides `logo`) |
| `s3_connection` | string | - | Name of the S3 toolkit instance to use for artifact storage |
| `s3_bucket` | string | `portal-assets` | S3 bucket for storing artifact content |
| `s3_prefix` | string | `artifacts/` | Key prefix within the bucket |
| `public_base_url` | string | - | Base URL for portal links returned in `save_artifact` responses |
| `max_content_size` | int | `10485760` | Maximum artifact size in bytes (10 MB) |
| `implementor.name` | string | - | Implementor display name shown in the left zone of the public viewer header |
| `implementor.logo` | string | - | URL to implementor SVG logo (fetched once at startup, max 1 MB) |
| `implementor.url` | string | - | Clickable link wrapping the implementor name and logo |
| `rate_limit.requests_per_minute` | int | `60` | Public portal viewer rate limit |
| `rate_limit.burst_size` | int | `10` | Public portal viewer burst allowance |
| `export.enabled` | bool | auto | Enable `trino_export` tool. Auto-enabled when portal and Trino are both configured. Set `false` to disable |
| `export.max_rows` | int | `100000` | Hard row cap for exports |
| `export.max_bytes` | int64 | `104857600` | Hard byte cap for formatted output (100 MB) |
| `export.default_timeout` | string | `5m` | Default query timeout for exports |
| `export.max_timeout` | string | `10m` | Maximum allowed query timeout for exports |

!!! note "Prerequisites"
    Portal requires `database.dsn` to be configured for metadata storage, and at least one S3 toolkit instance for artifact content storage.

## Audit Configuration

The `audit` block controls audit logging of MCP tool calls. Audit events are written asynchronously to PostgreSQL.

```yaml
audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` (when a database is available) | Enable audit logging. Set `false` to disable. |
| `log_tool_calls` | bool | `true` | Log MCP tool call events. Set `false` to keep audit on but skip per-tool-call rows. |
| `retention_days` | int | `90` | Days to retain audit events |

!!! note "Requires database"
    Audit logging requires `database.dsn` to be configured. With a database available and no `audit:` block, both audit and per-tool-call logging are on by default. Setting `enabled: false` disables audit entirely; `log_tool_calls: false` keeps audit on but stops recording per-tool-call events.

See [Audit Logging](audit.md) for query examples and retention details.

## Session Configuration

The `sessions` block controls how MCP session state is stored. In-memory sessions are lost on restart; database-backed sessions survive restarts and support multi-replica deployments.

```yaml
sessions:
  store: database
  ttl: 30m
  cleanup_interval: 1m
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `store` | string | `memory` | Backend: `memory` or `database` |
| `ttl` | duration | streamable `session_timeout` | Session lifetime |
| `cleanup_interval` | duration | `1m` | Cleanup routine interval |

!!! note "Requires database"
    The `database` store requires `database.dsn` to be configured.

See [Session Externalization](session-externalization.md) for architecture details and multi-replica considerations.

### Explicit Session Handles

The `sessions.handles` block controls explicit session handles (issue #792). When enabled, `platform_info` mints a `session_id` that the model passes back as an ordinary argument on every subsequent tool call. This is the pattern the [MCP 2026-07-28 release candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) recommends after removing the protocol-level session and the `Mcp-Session-Id` header ([SEP-2567](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567)).

This makes `platform_info` structurally unskippable (no handle exists until it is called, and gated tools require one), gives audit and provenance a deliberate session key, and keeps the platform working unchanged when clients move to the sessionless protocol.

```yaml
sessions:
  handles:
    enabled: true      # mint, advertise, and validate handles (default on)
    ttl: 8h            # handle lifetime, refreshed on use
    require: true      # refuse gated calls without a valid platform_info handle
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Mint a `session_id` from `platform_info`, advertise it on every tool's input schema, validate and strip it on each call. Set `false` for byte-identical legacy transport-session behavior. |
| `ttl` | duration | `8h` | Handle lifetime, refreshed on use. |
| `require` | bool | `true` | Refuse any gated tool call that does not carry a valid `platform_info`-minted handle with `SESSION_REQUIRED`. A transport-level `Mcp-Session-Id` (or the stdio sentinel) is **not** accepted as a fallback; it is the churning per-call value the handle exists to replace (issue #800). Set `false` for a softer landing during rollout, where a handle-less call falls back to the transport session instead of being refused. |

Every tool advertises the injected `session_id` argument except `platform_info` (which mints it). Upstream toolkits never see the argument: the platform strips it before the handler runs. A handle presented by a different authenticated identity, or an unknown/expired handle, is refused with `SESSION_EXPIRED`. With `require: true`, `platform_info` mints and threads a handle on every transport (stdio, SSE, Streamable HTTP); there is no stdio carve-out. The `mcp_session_resolution_total{source}` metric (`explicit`, `transport`, `stdio`, `none`) shows how much traffic still relies on a transport session; with `require: true` only `explicit` and `none` occur on gated tools.

## Toolkit Configuration

### Trino

```yaml
toolkits:
  trino:
    enabled: true
    instances:
      primary:                   # Instance name (can be any identifier)
        host: trino.example.com
        port: 443
        user: analyst
        password: ${TRINO_PASSWORD}
        catalog: hive
        schema: default
        ssl: true
        ssl_verify: true
        timeout: 120s
        default_limit: 1000
        max_limit: 10000
        read_only: false
        connection_name: primary
    default: primary
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | **required** | Trino coordinator hostname |
| `port` | int | 8080 (443 if SSL) | Trino coordinator port |
| `user` | string | **required** | Trino username |
| `password` | string | - | Trino password (if auth enabled) |
| `catalog` | string | - | Default catalog |
| `schema` | string | - | Default schema |
| `ssl` | bool | `false` | Enable SSL/TLS |
| `ssl_verify` | bool | `true` | Verify SSL certificates |
| `timeout` | duration | `120s` | Query timeout |
| `default_limit` | int | `1000` | Default row limit for queries |
| `max_limit` | int | `10000` | Maximum allowed row limit |
| `read_only` | bool | `false` | Restrict to read-only queries |
| `connection_name` | string | instance name | Display name for this connection |
| `descriptions` | map | `{}` | Override tool descriptions for this instance (key: tool name, value: description text) |

### DataHub

```yaml
toolkits:
  datahub:
    enabled: true
    instances:
      primary:
        url: https://datahub.example.com
        token: ${DATAHUB_TOKEN}
        timeout: 30s
        default_limit: 10
        max_limit: 100
        max_lineage_depth: 5
        connection_name: primary
        read_only: true
    default: primary
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `url` | string | **required** | DataHub GMS URL |
| `token` | string | - | DataHub access token |
| `timeout` | duration | `30s` | API request timeout |
| `default_limit` | int | `10` | Default search result limit |
| `max_limit` | int | `100` | Maximum search result limit |
| `max_lineage_depth` | int | `5` | Maximum lineage traversal depth |
| `connection_name` | string | instance name | Display name for this connection |
| `read_only` | bool | `false` | Restrict to read operations (disables write tools) |
| `descriptions` | map | `{}` | Override tool descriptions for this instance (key: tool name, value: description text) |

### S3

```yaml
toolkits:
  s3:
    enabled: true
    instances:
      primary:
        region: us-east-1
        endpoint: ""                    # Custom endpoint for MinIO, etc.
        public_endpoint: ""             # Public endpoint for presigned URLs (see below)
        access_key_id: ${AWS_ACCESS_KEY_ID}
        secret_access_key: ${AWS_SECRET_ACCESS_KEY}
        session_token: ""
        profile: ""                     # AWS profile name
        use_path_style: false           # Use path-style URLs
        timeout: 30s
        disable_ssl: false
        read_only: true                 # Restrict to read operations
        max_get_size: 10485760          # 10MB
        max_put_size: 104857600         # 100MB
        connection_name: primary
        bucket_prefix: ""               # Filter to buckets with this prefix
    default: primary
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `region` | string | `us-east-1` | AWS region |
| `endpoint` | string | - | Custom S3 endpoint for data operations (for MinIO, SeaweedFS, etc.) |
| `public_endpoint` | string | - | Public-facing endpoint used only to sign presigned URLs (`s3_presign_url`). When set to an externally resolvable address, presigned URLs are signed against it instead of `endpoint`, while data traffic keeps using `endpoint`. Empty falls back to `endpoint`. |
| `access_key_id` | string | - | AWS access key ID |
| `secret_access_key` | string | - | AWS secret access key |
| `session_token` | string | - | AWS session token (for temporary creds) |
| `profile` | string | - | AWS credentials profile name |
| `use_path_style` | bool | `false` | Use path-style S3 URLs |
| `timeout` | duration | `30s` | Request timeout |
| `disable_ssl` | bool | `false` | Disable SSL (for local testing) |
| `read_only` | bool | `false` | Restrict to read operations |
| `max_get_size` | int64 | `10485760` | Max bytes to read from objects |
| `max_put_size` | int64 | `104857600` | Max bytes to write to objects |
| `connection_name` | string | instance name | Display name for this connection |
| `bucket_prefix` | string | - | Only show buckets with this prefix |
| `descriptions` | map | `{}` | Override tool descriptions for this instance (key: tool name, value: description text) |

### MCP Gateway

The `mcp` toolkit kind proxies upstream MCP servers and re-exposes their
tools as `<connection_name>__<remote_tool>`. **Connections are managed
exclusively through the admin portal** — no per-instance config goes in
`platform.yaml`. The only YAML knob is `enabled`, which turns the kind on.

```yaml
toolkits:
  mcp:
    enabled: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Register the gateway toolkit kind. When false, mcp connections in `connection_instances` are ignored. |

**Required environment for the OAuth + at-rest encryption path:**

| Variable | Required for | Notes |
|----------|--------------|-------|
| `ENCRYPTION_KEY` | Encrypted credentials in `connection_instances`, `gateway_oauth_tokens`, and `oauth_pkce_states` | 32 bytes of key material, accepted in three forms: 64 hex characters, 44-character base64, or 32 raw bytes (set via `printf` / file). Without it, sensitive fields are stored in plaintext and the platform logs a warning. Required for any production gateway deployment. |
| `DATABASE_URL` | OAuth `authorization_code` grant (refresh-token persistence) and multi-replica deployments | Without a database, OAuth tokens live in process memory only and don't survive restarts. Multi-replica deployments additionally need this so PKCE state is shared across pods. |

See [Gateway Toolkit](gateway.md) for the connection-config reference,
auth modes (`none`/`bearer`/`api_key`/`oauth`), OAuth grant types
(`client_credentials` and `authorization_code` + PKCE), and the
cross-enrichment rule schema.

## Cross-Enrichment Configuration

```yaml
enrichment:
  trino_semantic_enrichment: true    # Add DataHub context to Trino results
  datahub_query_enrichment: true     # Add Trino availability to DataHub results
  s3_semantic_enrichment: true       # Add DataHub context to S3 results
  datahub_storage_enrichment: true   # Add S3 availability to DataHub results
  unwrap_json: true               # Auto-unwrap single-row VARCHAR-of-JSON (default: true)
  column_context_filtering: true     # Only include SQL-referenced columns (default: true)

  # Memory-enrichment payload budget (issue #761): keeps recalled memories a
  # supporting note rather than crowding out the analyzed data.
  memory_limit: 5                    # Max memory records recalled per tool call (default: 5)
  memory_context_budget_bytes: 1500  # Byte budget for rendered summaries; over-budget records become fetchable stubs; 0 disables (default: 1500)
  memory_summary_bytes: 280          # Per-record summary excerpt cap; 0 = full content (default: 280)

  # Session metadata deduplication (avoids repeating metadata for same table)
  session_dedup:
    enabled: true             # Default: true
    mode: reference           # reference (default), summary, none
    entry_ttl: 5m             # Defaults to semantic.cache.ttl
    session_timeout: 30m      # Defaults to server.streamable.session_timeout
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trino_semantic_enrichment` | bool | `true` | Enrich Trino results with DataHub metadata. Default on; read-only and no-ops without a semantic provider. Set `false` to disable. |
| `datahub_query_enrichment` | bool | `true` | Add query availability to DataHub search results. Default on; set `false` to disable. |
| `s3_semantic_enrichment` | bool | `true` | Enrich S3 results with DataHub metadata. Default on; set `false` to disable. |
| `datahub_storage_enrichment` | bool | `true` | Add S3 availability to DataHub results. Default on; set `false` to disable. |
| `unwrap_json` | bool | `true` | Auto-unwrap single-row VARCHAR-of-JSON results |
| `column_context_filtering` | bool | `true` | Limit column enrichment to SQL-referenced columns |
| `memory_limit` | int | `5` | Max memory records recalled and rendered into `memory_context` per tool call |
| `memory_context_budget_bytes` | int | `1500` | Byte budget for the rendered memory summaries; records beyond it are listed as compact `id`+`reference` stubs in `memory_context_omitted` (still fetchable, at least one always rendered). `0` disables the budget |
| `memory_summary_bytes` | int | `280` | Per-record summary-first excerpt cap; the full record is fetchable via its `mcp:memory:<id>` reference. `0` renders full content |
| `session_dedup.enabled` | bool | `true` | Whether session dedup is active |
| `session_dedup.mode` | string | `reference` | Repeat query content: `reference`, `summary`, `none` |
| `session_dedup.entry_ttl` | duration | semantic cache TTL | How long a table stays "already sent" |
| `session_dedup.session_timeout` | duration | streamable session timeout | Idle session cleanup interval |

## Tuning Configuration

Static operational rules that shape agent behavior via `platform_info` guidance and tool descriptions.

```yaml
tuning:
  rules:
    quality_threshold: 0.7
  prompts_dir: "/etc/mcp/prompts"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rules.quality_threshold` | float | `0.7` | Minimum DataHub quality score below which a warning is surfaced |
| `prompts_dir` | string | - | Directory of additional prompt resource files |

## Search-First Gate Configuration

A hard gate that refuses query tools until the agent calls `search` in the session. When a query tool (`trino_query`, `trino_execute`) is called before any discovery tool, the tool handler **does not run**; a `SEARCH_REQUIRED` error result is returned instructing the agent to call `search` first. Once `search` has been called at least once in a session, every subsequent query tool call in that session proceeds normally with no further check. The gate is enabled by default; set `require_search: false` to disable gating (and hinting) entirely.

```yaml
workflow:
  require_search: false             # Default: true (gate on). false disables it entirely.
  # discovery_tools: []             # Tools that satisfy the gate (defaults to search + the datahub_* tools)
  # query_tools: []                 # Tools that are gated (defaults to trino_query, trino_execute)
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `require_search` | bool | `true` | Enable the search-first hard gate. `false` disables gating with no block and no hint. |
| `discovery_tools` | array | `search` + the `datahub_*` tools | Tool names that satisfy the gate. `search` is the front door agents are steered toward, and the `datahub_*` discovery tools also count so a persona granted `datahub_*` (but not `search`) is not locked out. |
| `query_tools` | array | `trino_query`, `trino_execute` | Tool names gated by discovery |

!!! warning "Behavior change"
    `require_search` replaces the former `workflow.require_discovery_before_query` and its warn-after-execution behavior. It is a breaking rename (not aliased) and a hard gate: a deployment that never touched workflow gating will begin refusing `trino_query`/`trino_execute` until `search` is called once per session. The older `tuning.rules.require_datahub_check` static hint has been removed.

## Semantic and Query Provider Configuration

Specify which toolkit instance provides semantic metadata and query execution:

```yaml
semantic:
  provider: datahub           # Provider type: datahub or noop
  instance: primary           # Which DataHub instance to use
  cache:
    enabled: true
    ttl: 5m

query:
  provider: trino             # Provider type: trino or noop
  instance: primary           # Which Trino instance to use

storage:
  provider: s3                # Provider type: s3 or noop
  instance: primary           # Which S3 instance to use
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `semantic.provider` | string | - | Provider type: `datahub` or `noop` |
| `semantic.instance` | string | - | Toolkit instance name |
| `semantic.cache.enabled` | bool | `false` | Enable semantic metadata caching |
| `semantic.cache.ttl` | duration | `5m` | Cache TTL |
| `query.provider` | string | - | Provider type: `trino` or `noop` |
| `query.instance` | string | - | Toolkit instance name |
| `storage.provider` | string | - | Provider type: `s3` or `noop` |
| `storage.instance` | string | - | Toolkit instance name |

**URN mapping** (`semantic.urn_mapping`, `query.urn_mapping`) translates catalog and platform names when Trino and DataHub name the same data differently - see [Trino to DataHub](../cross-enrichment/trino-datahub.md#urn-mapping-for-mismatched-names) for the full config reference. **Lineage-aware enrichment** (`semantic.lineage`) inherits column metadata from upstream datasets when a table's own columns lack it - see [Lineage Inheritance](../cross-enrichment/lineage.md) for the full config reference and worked examples.

## Persona Configuration

Personas define tool access based on user roles. The security model follows a **default-deny** approach.

Persona names are keyed directly under `personas:` (the config's `Definitions`
field is an inline map, so there is no `definitions:` wrapper key).

```yaml
personas:
  analyst:
    display_name: "Data Analyst"
    roles: ["analyst", "data_engineer"]
    tools:
      allow: ["search", "trino_*", "datahub_*"]
      deny: ["*_delete_*", "*_drop_*"]
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
  default_persona: analyst
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `<name>` | map | - | Named persona configuration, keyed directly under `personas:` |
| `<name>.display_name` | string | - | Human-readable name |
| `<name>.roles` | array | - | Roles that map to this persona |
| `<name>.tools.allow` | array | `[]` | Allowed tool patterns |
| `<name>.tools.deny` | array | `[]` | Denied tool patterns |
| `<name>.context.description_prefix` | string | - | Prepended to platform description |
| `<name>.context.description_override` | string | - | Replaces platform description entirely |
| `<name>.context.agent_instructions_suffix` | string | - | Appended to the admin `agent_instructions` layer |
| `<name>.context.agent_instructions_override` | string | - | Replaces the admin `agent_instructions` layer only; the platform baseline is always present |
| `default_persona` | string | - | Persona for users without role match |

!!! warning "Default-Deny Security"
    Users without a resolved persona have **no tool access**. The built-in default persona denies all tools. You must define explicit personas with tool access for your users.

## Knowledge Capture Configuration

Knowledge capture records domain knowledge shared during AI sessions and provides a workflow for applying approved insights to the DataHub catalog. See [Knowledge Capture](../knowledge/overview.md) for the full feature documentation.

```yaml
knowledge:
  enabled: true
  apply:
    enabled: true
    datahub_connection: primary
    require_confirmation: true
  reflexive_capture:
    enabled: true
  search_provider_timeout: 5s
  search_embed_timeout: 5s
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` (when a database is available) | Enable the knowledge review and write-back toolkit (`apply_knowledge`). Knowledge capture lives in the memory toolkit (`memory_capture`) and is enabled with the memory layer, not this flag |
| `apply.enabled` | bool | `true` (when a database is available) | Enable the `apply_knowledge` tool for admin review and catalog write-back. Set `false` to disable. Still gated behind database availability. |
| `apply.datahub_connection` | string | - | DataHub instance name for write-back operations |
| `apply.require_confirmation` | bool | `false` | Require explicit `confirm: true` on apply actions |
| `reflexive_capture.enabled` | bool | `true` | Auto-capture a "misconception + fix" correction when a Trino query errors and a later related same-session query on the same connection succeeds (#635). Source `automation`, reviewed sink-class (enters review, never live), gated by the persona's `memory_capture` grant. Default-on when the memory subsystem is available; set `false` to disable |
| `search_provider_timeout` | duration | `5s` | Per-provider deadline for the `search` fan-out arms. Each knowledge source (catalog, memory, insights, endpoints, …) is bounded by this, so one slow source drops out as a collected error while the rest still return, instead of stalling the whole search. Set a negative duration to disable the bound (a search then waits for its slowest provider). |
| `search_embed_timeout` | duration | `5s` | Deadline for the serial intent-embedding step in `search`, independent of `search_provider_timeout`. A slow or unreachable embedder degrades to lexical ranking rather than stalling the search; because that silently loses semantic relevance, this knob lets you give a slow (cold or CPU-only) embedder more headroom to preserve `hybrid` ranking without loosening the fan-out bound. Set a negative duration to disable the bound. |

!!! note "Prerequisites"
    Knowledge capture requires `database.dsn` to be configured. The `apply_knowledge` tool requires the admin persona.

## Memory Layer Configuration

The memory layer provides persistent memory for agent and analyst sessions with vector search, cross-enrichment, and staleness detection. See [Memory Layer](../memory/overview.md) for the full feature documentation.

```yaml
memory:
  enabled: true
  embedding:
    provider: ollama
    ollama:
      url: "http://localhost:11434"
      model: "nomic-embed-text"
      timeout: 30s
      max_input_bytes: 6000
  staleness:
    enabled: true
    interval: 15m
    batch_size: 50
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` (when database available) | Enable memory layer. Set `false` to explicitly disable. |
| `embedding.provider` | string | `noop` | Embedding provider: `ollama` or `noop` |
| `embedding.ollama.url` | string | `http://localhost:11434` | Ollama API base URL |
| `embedding.ollama.model` | string | `nomic-embed-text` | Ollama embedding model (768-dim) |
| `embedding.ollama.timeout` | duration | `30s` | Embedding API timeout |
| `embedding.ollama.max_input_bytes` | int | `6000` | Per-text input cap (bytes) applied before embedding. The platform truncates input itself on a UTF-8 boundary because Ollama's `truncate` flag is unreliable for content over the model's context. The default sits below `nomic-embed-text`'s ~2048-token boundary; raise it only for a larger-context model. Only the embedded text is trimmed; stored content is unaffected. |
| `staleness.enabled` | bool | `false` | Enable background staleness watcher |
| `staleness.interval` | duration | `15m` | Staleness check interval |
| `staleness.batch_size` | int | `50` | Records per check cycle |

!!! note "Prerequisites"
    Memory requires `database.dsn` to be configured and the pgvector PostgreSQL extension installed. Memory tools are opt-in per persona (`memory_*` in `tools.allow`).

!!! note "Ollama batch endpoint"
    Batch embedding calls (API gateway spec indexing) issue a single `POST /api/embed` request per batch against modern Ollama servers. Servers that lack the batch endpoint (response: HTTP 404) are detected on the first call; the platform logs a WARN and transparently falls back to one `POST /api/embeddings` request per text. Upgrading the Ollama server is recommended for substantially faster batch indexing on multi-spec catalogs. Memory writes embed one record at a time and always use `/api/embeddings`.

## API Gateway Configuration

Cluster-wide tuning for the API gateway toolkit's background work. Connection-level configuration (`base_url`, `auth_mode`, credentials) lives in the connection store; this section is for knobs that apply to every API connection.

```yaml
apigateway:
  embed_jobs:
    workers: 1
    embed_timeout: 5m
    lease_duration: 10m
    batch_size: 32
    retention_days: 14
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `embed_jobs.workers` | int | `1` | Number of embedding-worker goroutines per pod. Each goroutine independently claims and processes jobs; the lease + `SKIP LOCKED` predicate in the queue's claim path prevents two goroutines (in the same pod or across pods) from picking the same job. Increase to 2-4 for deployments with many specs and a fast embedder; CPU-only embedders typically saturate at 1 because the bottleneck is the embedding model. |
| `embed_jobs.embed_timeout` | duration | `5m` | HTTP timeout the worker applies to its batched `/api/embed` POSTs against Ollama. Scoped to the worker only so the shared 30s `memory.embedding.ollama.timeout` continues to govern request-path callers (memory recall, memory_capture, etc.); a wedged Ollama therefore fails MCP tool calls in 30s while the worker tolerates the longer batched-inference floor. Lower this on GPU embedders to tighten the failure floor. |
| `embed_jobs.lease_duration` | duration | `10m` | Time a claim stamps on a job; the worker heartbeat re-stamps it at `lease_duration / 3` cadence so a long embed pass is not reaped mid-flight. Must be greater than `embed_timeout`. Caps "pod went silent", not "embed batch is slow". |
| `embed_jobs.batch_size` | int | `32` | Texts per upstream EmbedBatch call. Sets the *starting* chunk size only: when a chunk exceeds `embed_timeout`, the worker automatically halves it and retries the sub-chunks down to a floor of one text, so a batch too large for a slow (e.g. CPU-only) embedder converges to a size that completes and persists partial progress instead of failing the whole unit at a fixed size. Non-timeout provider errors (5xx, malformed response) still fail fast without subdividing. Lower this to skip the initial shrink cycles on a known-slow embedder; raise it on GPU embedders where per-call overhead dominates. |
| `embed_jobs.retention_days` | int | `14` | Age past which finished `index_jobs` history is purged by the background retainer: succeeded rows and failed rows that were resolved (superseded by a later success or operator-dismissed). The reconciler records one row per unit per sweep, so this keeps the table bounded while preserving a recent window for the admin Indexing dashboard's throughput, latency, and job-log views. Open failures (`failed` with no `resolved_at`) and in-flight jobs (`pending` / `running`) are never purged regardless of age. `0` uses the default (14); a negative value disables retention (history grows unbounded, for externally-managed cleanup). |

## MCP Apps Configuration

MCP Apps provide interactive UI components that enhance tool results. The platform provides the infrastructure; you provide the HTML/JS/CSS apps.

```yaml
mcpapps:
  enabled: true
  apps:
    query_results:
      enabled: true
      assets_path: "/etc/mcp-apps/query-results"
      tools:
        - trino_query
        - trino_execute
      csp:
        resource_domains:
          - "https://cdn.jsdelivr.net"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable MCP Apps infrastructure |
| `apps` | map | - | Named app configurations |
| `apps.<name>.enabled` | bool | `true` | Enable this app |
| `apps.<name>.assets_path` | string | **required** | Absolute path to app directory |
| `apps.<name>.tools` | array | **required** | Tools this app enhances |
| `apps.<name>.csp.resource_domains` | array | - | Allowed CDN origins |

See [MCP Apps Configuration](../mcpapps/configuration.md) for complete options.

## Resource Templates Configuration

Resource templates expose platform data as browseable, parameterized MCP resources using RFC 6570 URI templates.

```yaml
resources:
  enabled: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable resource templates |

When enabled, the platform registers these resource templates:

- `schema://{catalog}.{schema}/{table}` — Table schema with column types and descriptions
- `glossary://{term}` — Glossary term definitions
- `availability://{catalog}.{schema}/{table}` — Query availability and row counts

Clients that support resource browsing (e.g., Claude Desktop) will show these as navigable resources alongside tools.

## Custom Resources Configuration

Custom resources let you expose arbitrary static content as named MCP resources — brand assets, operational limits, environment docs, or any structured blob that agents can read by URI. They are registered whenever `resources.custom` is non-empty, independent of `resources.enabled`.

```yaml
resources:
  custom:
    - uri: "brand://theme"
      name: "Brand Theme"
      description: "Primary brand colors and site URL"
      mime_type: "application/json"
      content: |
        {
          "colors": {"primary": "#FF6B35", "secondary": "#004E89"},
          "url": "https://example.com"
        }

    - uri: "brand://logo"
      name: "Brand Logo SVG"
      mime_type: "image/svg+xml"
      content_file: "/etc/platform/logo.svg"
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `uri` | string | Yes | Unique resource URI (e.g., `brand://theme`, `docs://limits`) |
| `name` | string | Yes | Human-readable name shown in `resources/list` |
| `description` | string | No | Optional description for MCP clients |
| `mime_type` | string | Yes | MIME type (e.g., `application/json`, `image/svg+xml`, `text/plain`) |
| `content` | string | One of | Inline content (text, JSON, SVG, etc.) |
| `content_file` | string | One of | Absolute path to a file; read on every request (supports hot-reload) |

`content` and `content_file` are mutually exclusive. Invalid entries (missing required fields, both or neither content fields set) are skipped with a warning at startup; valid entries in the same list are still registered.

## Progress Notifications Configuration

Progress notifications send granular updates to MCP clients during long-running Trino queries. The client must include `_meta.progressToken` in the request to receive updates. Enabled by default; set `enabled: false` to opt out.

```yaml
progress:
  enabled: false   # only needed to opt out; defaults to true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `*bool` | `true` (nil = enabled) | Enable progress notifications |

When enabled, Trino query execution sends progress updates including rows scanned, bytes processed, and query stage information. Clients that don't send a `progressToken` receive no notifications (zero overhead).

## Client Logging Configuration

Client logging sends server-to-client log messages via the MCP `logging/setLevel` protocol. Messages include enrichment decisions, timing data, and platform diagnostics. Enabled by default; set `enabled: false` to opt out.

```yaml
client_logging:
  enabled: false   # only needed to opt out; defaults to true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `*bool` | `true` (nil = enabled) | Enable client logging |

Zero overhead if the client hasn't subscribed via `logging/setLevel`. When active, log messages report semantic cache hits/misses, enrichment timing, and cross-enrichment decisions.

## Elicitation Configuration

Elicitation requests user confirmation before potentially expensive or sensitive operations. Requires client-side elicitation support (e.g., Claude Desktop). Gracefully degrades to a no-op if the client doesn't support elicitation. Enabled by default (including `cost_estimation` and `pii_consent`); set `enabled: false` at any level to opt out.

```yaml
elicitation:
  enabled: false        # only needed to opt out; defaults to true
  cost_estimation:
    enabled: false      # only needed to opt out; defaults to true
    row_threshold: 1000000
  pii_consent:
    enabled: false      # only needed to opt out; defaults to true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `*bool` | `true` (nil = enabled) | Enable elicitation |
| `cost_estimation.enabled` | `*bool` | `true` (nil = enabled) | Prompt before expensive queries |
| `cost_estimation.row_threshold` | int | `1000000` | Row count threshold from `EXPLAIN` IO estimates |
| `pii_consent.enabled` | `*bool` | `true` (nil = enabled) | Prompt when query accesses PII-tagged columns |

!!! note "Client support required"
    Elicitation uses the MCP `elicitation/create` capability. Clients that don't support elicitation will not receive prompts — queries proceed without confirmation.

!!! warning "Behavior change for existing deployments"
    Elicitation is user-facing: with no `elicitation` block at all, cost-estimation and PII-consent prompts now fire out of the box. `cost_estimation` still respects `row_threshold` (default 1,000,000 rows), so it only prompts on large queries. Deployments that relied on the previous silent-off default should add `elicitation.enabled: false` (or disable the sub-features individually) to keep the prior behavior.

## Icons Configuration

Icons add visual metadata to tools, resources, and prompts in MCP list responses. Upstream toolkits (Trino, DataHub, S3) provide default icons; this configuration overrides or extends them. Enabled by default; set `enabled: false` to opt out.

```yaml
icons:
  enabled: false   # only needed to opt out; defaults to true
  tools:
    trino_query:
      src: "https://example.com/custom-trino.svg"
      mime_type: "image/svg+xml"
  resources:
    "schema://{catalog}.{schema}/{table}":
      src: "https://example.com/schema.svg"
  prompts:
    knowledge_capture:
      src: "https://example.com/knowledge.svg"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `*bool` | `true` (nil = enabled) | Enable icon injection middleware |
| `tools` | map | - | Icon overrides keyed by tool name |
| `resources` | map | - | Icon overrides keyed by resource URI |
| `prompts` | map | - | Icon overrides keyed by prompt name |
| `*.src` | string | - | Icon source URL |
| `*.mime_type` | string | - | Icon MIME type (e.g., `image/svg+xml`) |

!!! tip "Default icons"
    Each upstream toolkit provides a default icon for all its tools. You only need this configuration if you want to customize or override those defaults.

## Environment Variables

Common environment variables:

| Variable | Description |
|----------|-------------|
| `TRINO_USER` | Trino username |
| `TRINO_PASSWORD` | Trino password |
| `DATAHUB_TOKEN` | DataHub access token |
| `AWS_ACCESS_KEY_ID` | AWS access key |
| `AWS_SECRET_ACCESS_KEY` | AWS secret key |
| `AWS_SESSION_TOKEN` | AWS session token |
| `DATABASE_URL` | PostgreSQL connection string (for audit/OAuth) |

## Complete Example

```yaml
apiVersion: v1

server:
  name: mcp-data-platform
  transport: http
  address: ":8080"

database:
  dsn: ${DATABASE_URL}

portal:
  enabled: true

admin:
  enabled: true
  persona: admin

# Hide unused tools from tools/list to save LLM tokens
tools:
  allow:
    - "trino_*"
    - "datahub_*"
    - "memory_capture"
  deny:
    - "*_delete_*"

audit:
  enabled: true
  log_tool_calls: true
  retention_days: 90

sessions:
  store: database
  ttl: 30m
  cleanup_interval: 1m

auth:
  api_keys:
    enabled: true
    keys:
      - key: ${API_KEY_ADMIN}
        name: "admin"
        roles: ["admin"]

toolkits:
  trino:
    enabled: true
    instances:
      primary:
        host: trino.example.com
        port: 443
        user: ${TRINO_USER}
        password: ${TRINO_PASSWORD}
        ssl: true
        catalog: hive
        schema: default
        default_limit: 1000
        max_limit: 10000
    default: primary

  datahub:
    enabled: true
    instances:
      primary:
        url: https://datahub.example.com
        token: ${DATAHUB_TOKEN}
        default_limit: 10
        max_limit: 100
    default: primary

  s3:
    enabled: true
    instances:
      primary:
        region: us-east-1
        read_only: true
    default: primary

semantic:
  provider: datahub
  instance: primary
  cache:
    enabled: true
    ttl: 5m

query:
  provider: trino
  instance: primary

storage:
  provider: s3
  instance: primary

enrichment:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  s3_semantic_enrichment: true
  unwrap_json: true
  column_context_filtering: true

resources:
  enabled: true

# progress, client_logging, and elicitation (with cost_estimation and
# pii_consent) are all enabled by default, so no block is needed here unless
# you want to opt out or customize (e.g. a lower cost_estimation.row_threshold).

personas:
  analyst:
    display_name: "Data Analyst"
    roles: ["analyst"]
    tools:
      allow: ["search", "trino_query", "trino_execute", "trino_explain", "datahub_*"]
      deny: ["*_delete_*"]
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
  default_persona: analyst
```

## Next Steps

- [Operating Modes](operating-modes.md) - Standalone, file + DB, and bootstrap + DB config modes
- [Admin API](admin-api.md) - REST endpoints for system, config, personas, auth keys, audit
- [Tools](tools.md) - Available tools and parameters
- [Multi-Provider](multi-provider.md) - Configure multiple instances
- [Authentication](../auth/overview.md) - Add authentication
- [Personas](../personas/overview.md) - Role-based access control
- [MCP Apps](../mcpapps/overview.md) - Interactive UI for tool results
- [Middleware Reference](../reference/middleware.md) - Request processing chain details

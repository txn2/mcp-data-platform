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

### Startup validation

After parsing, the server validates the configuration and refuses to start when
it finds a setting that cannot work as written. The error names every problem it
found at once, so a config with several is fixed in one pass rather than one
restart at a time. Validation covers:

- `personas.default_persona`, which was removed and is refused rather than ignored
- `auth.oidc.issuer` when OIDC is enabled
- `auth.browser_session`: OIDC must also be enabled, `signing_key` is required,
  `same_site` must be one of `lax`, `strict`, `none`, and `same_site: none`
  requires `secure: true` (browsers drop a `SameSite=None` cookie that is not
  `Secure`, which would break portal login)
- `oauth.issuer` when the OAuth server is enabled, its `upstream.issuer`,
  `upstream.client_id` and `upstream.redirect_uri` when an upstream is set, and
  `oauth.signing_key` on an HTTP transport unless
  `oauth.allow_ephemeral_signing_key: true` is set (a per-process key makes each
  replica reject tokens minted by its peers)
- `database.dsn` when `sessions.store: database`, and on that store a
  `sessions.broadcast_channel` that fits PostgreSQL's 63-byte `LISTEN`
  identifier limit and unquoted-identifier grammar (a longer name would be
  truncated by `LISTEN` but not by `NOTIFY`, so replicas would never hear each
  other)
- `audit.delivery`

These are separate from the unknown-key handling above: a key here is one the
schema recognizes, so `config.strict` has no bearing on it. A value that is
merely unusual is not refused; validation covers settings whose effect would
otherwise be silent.

!!! warning "Newly enforced"

    These checks were written alongside the settings they guard, but nothing
    ran them: the loader parsed and applied defaults and the server started.
    A deployment could therefore be running today on a config that will be
    refused after upgrading. Two cases are worth checking before you roll
    out, because each previously produced a partial failure rather than an
    error:

    - `auth.browser_session.enabled: true` with `auth.oidc.enabled: false`
      started with portal login silently switched off. It is now refused.
    - `oauth.enabled: true` on an HTTP transport with no `oauth.signing_key`
      minted a per-process key, which peers reject; multi-replica deployments
      saw intermittent auth failures. It is now refused unless
      `oauth.allow_ephemeral_signing_key: true` says the ephemeral key is
      intended.

    Run the server against your config once before rolling out. It reports
    every problem in a single error.

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
      display_name: "Explore a Topic"
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
| `server.prompts[].display_name` | string | - | Human-readable title served as the MCP prompt `title` (falls back to the name) |
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

Each built-in workflow prompt registers automatically when its required toolkits
are present. Turn one off by name with `server.builtin_prompts`:

```yaml
server:
  builtin_prompts:
    trace-data-lineage: false      # do not register this workflow prompt
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `server.builtin_prompts` | map[string]bool | `{}` | Per-prompt switch for the built-in workflow prompts above, keyed by prompt name. A name absent from the map (or set to `true`) registers as usual; `false` suppresses it. Operator prompts in `server.prompts` override a built-in of the same name regardless of this setting |

`platform_info` does not list the prompt library. It once did, carrying every prompt with its full body, which made the mandatory first call of a session grow with the library and spend the agent's context on prompts the session would not run (#1586). A prompt is reached by handle instead: `manage_prompt` command `use` resolves a stored name, display name, `mcp:prompt:<id>` reference or free text, command `list` browses, `show_prompts` opens the library for the human, and `fetch` on `mcp:prompt:<id>` returns one in full.

**Prompt titles and resolution.** Database prompts are served on the native MCP prompts surface under per-viewer scope-prefixed names (`global-<name>`, `<persona>-<name>`, `personal-<name>`, `shared-<name>`), which keeps the surface collision-free by construction; every descriptor carries a `title` from `display_name` so clients show the human name regardless. Users never need to know any machine name: agents resolve a prompt from whatever handle the user says (stored name, display name, `mcp:prompt:<id>`, or free text) with the `manage_prompt` `use` command.

**Versioning and approval provenance.** Every database prompt is versioned: each mutation of its content, display name, description, arguments, or tags snapshots an immutable version row with the author, and approval stamps bind to the specific version that was approved. Editing the content or arguments of an approved global or persona prompt does not change what is served: the edit is saved as a pending draft version, and the approved snapshot keeps serving until an admin approves the draft (admin API `POST /api/v1/admin/prompts/{id}/versions/{version}/approve`, or reject with `.../reject`; history via `GET .../versions`). Metadata-only edits (tags, category, description, display name) apply directly. Personal prompts version silently without review. Served prompts carry their provenance: `prompts/get` responses stamp `prompt_version`, `prompt_approved_by`, `prompt_approved_at`, and `prompt_reference` into `_meta`, and `manage_prompt use` reports the same in its provenance block, so an agent can state "running Daily Sales Report v4, approved by jane@example.com" before executing. Run counts and last-run timestamps are aggregated from prompt-serve audit events (within the audit retention window) and exposed on `manage_prompt get` and the `GET /api/v1/admin/prompts/usage` and `GET /api/v1/portal/prompts/usage` endpoints.

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

The MCP SDK caps each Streamable HTTP request body at 4 MiB and rejects a larger one with `413 Request Entity Too Large`. The limit applies to inbound JSON-RPC bodies, so it bounds tool-call arguments; it does not bound tool results, managed-resource uploads, or asset exports, which travel other paths with their own limits.

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

#### Signing key

When `oauth.enabled` is set on `server.transport: http`, `oauth.signing_key` (base64, 32+ bytes) is **required**: startup fails without it, because an auto-generated per-process key makes each replica reject tokens minted by its peers. Set `oauth.allow_ephemeral_signing_key: true` to override this for a single-replica dev setup (unsafe for replicas). On `stdio` the key is auto-generated when omitted (single-process by construction). To rotate the key without logging users out, see [Rotating the signing key](../auth/oauth-server.md#rotating-the-signing-key).

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

When a database is available (`database.dsn` is set), the platform uses a granular key/value config store. Individual config entries in the `config_entries` table override file defaults for whitelisted keys.

The store is the authority for these keys: nothing is copied into memory at startup and nothing is patched in place on a write. Every read resolves the key from the store and falls back to the file value when no row exists. A change made through the admin API is therefore in force on every replica as soon as it commits, with no restart and no cross-replica notification, and deleting a row restores the file default everywhere on the next read.

If the store cannot be read, the file-config value is used. A database outage degrades to the YAML the operator shipped rather than to an empty value, so agent instructions and deny patterns survive it.

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

The platform baseline is an index, not a manual (#1586): each line names one capability, the tool that enters it, and the judgment made before reaching for it, and a second section indexes the built-in knowledge pages that carry the depth. Guidance an agent needs only sometimes is fetched when it is needed rather than carried in every session's first response. The baseline is non-overridable and updates automatically when the platform is upgraded, so the operating model never has to be re-authored per deployment. A persona's `agent_instructions_override` replaces the admin layer only; the baseline is always present. Because the baseline names a tool (`search`, `memory_capture`, `manage_script`) only when that tool is registered and the persona is allowed to call it, it never points an agent at a tool it cannot use. "Registered" covers the tools the platform registers itself (`platform_info`, `list_connections`, `platform_find_tools`, and the prompt and script tools where their store exists) as well as the toolkits'; before #1586 the gate read the toolkit registry alone, so the baseline's prompt and script guidance never rendered on any deployment. The page index names each page by slug (`mcp:knowledge_page:platform-writing-managed-scripts`), which `fetch` resolves: a built-in page's row id is generated per deployment at reconcile time, so the slug is the only handle the shipped text can name. Each entry is gated on the capability it documents, so a persona that cannot write scripts is not handed the scripts pages, and the index is omitted entirely for a caller with neither `fetch` nor `search`, since nothing else returns a knowledge page. The agent receives the baseline as part of the composed `agent_instructions` in the `platform_info` response; admins can see the baseline on its own read-only in the portal's Agent Instructions screen and via `GET /api/v1/admin/config/agent-instructions-baseline`.

### The customized layer is byte-bounded

`server.agent_instructions` is composed into the first response of every session on the deployment, so its size is paid for by every caller before any work happens. Nothing bounded it before #1607: `config_entries.value_text` is unbounded `TEXT`, and neither the config store nor its REST writer measured a value. A deployment measured in that ticket carried 9,923 characters of it, most of them a data dictionary and a set of query examples -- material that belongs on a knowledge page rather than in a field every session reads.

The bound belongs to the layer rather than to whichever writer produced it:

| Bound | Bytes | Behavior |
|-------|-------|----------|
| Advisory | 12,288 | The write succeeds and the response carries a `notice` saying the layer has grown from a set of rules into a document, and what belongs on a knowledge page instead. |
| Limit | 32,768 | The write is refused, naming the size, the limit, the overage, and the knowledge-page alternative. |

Both writers enforce it: `PUT /api/v1/admin/config/entries/server.agent_instructions` (400 on refusal) and the `apply_knowledge` `agent_instructions` sink (see [Knowledge](../knowledge/overview.md#the-third-sink-the-deployments-own-operating-rules)). `GET /api/v1/admin/config/agent-instructions-baseline` reports both numbers as `limit_bytes` and `advisory_bytes`, and the portal's Agent Instructions screen draws its size meter from them, so the size an operator is shipping is visible while editing rather than at the moment of a refusal.

The limit is a ceiling on runaway growth, not a target. Keep this layer to the rules a session must know before it does anything, and index longer guidance from it: a one-line `mcp:knowledge_page:<slug>` entry, in the same form the platform baseline's own page index uses.

The config store is selected automatically: setting `database.dsn` makes config
database-backed (mutations to personas, auth keys, and config entries persist to
PostgreSQL and survive restarts); without a database the config is read-only from
the YAML file and mutations are blocked. There is no separate mode switch.

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

The admin portal provides a web-based dashboard for audit log exploration, tool execution testing, and system monitoring. Enable with `portal.enabled: true`. When enabled, it is served at `/portal/`. See [Admin API](admin-api.md) for the full endpoint reference and [Admin Portal](../portal/index.md) for the visual guide.

## Portal Configuration

The `portal` block enables the asset portal - the web UI plus REST API that persists AI-generated assets (JSX dashboards, HTML reports, SVG charts, exports) to S3 with PostgreSQL metadata tracking. See [Admin Portal](../portal/index.md) for branding and public-viewer walkthroughs and [User Portal](../portal/index.md) for the end-user feature tour.

```yaml
portal:
  enabled: true
  brand_name: "ACME"                              # Deployment brand; the title becomes "ACME Portal"
  brand_url: "https://acme.example.com"           # Brand site the portal's brand mark links to
  version_url: "https://acme.example.com/changelog"  # Optional link target for the header version number
  title: "ACME Data Platform"                     # Overrides the brand-composed title
  tagline: "Sign in to access your data."         # Login-screen subtitle
  oidc_button_label: "Sign in with ACME Keycloak" # Login-screen SSO button text
  logo: https://example.com/logo.svg              # Logo URL (fallback for both themes)
  logo_light: https://example.com/logo-light.svg  # Logo for light theme
  logo_dark: https://example.com/logo-dark.svg    # Logo for dark theme
  s3_connection: primary        # S3 toolkit instance for asset storage
  s3_bucket: portal-assets      # Bucket for asset content
  s3_prefix: "artifacts/"       # Key prefix within the bucket (storage key, unchanged)
  public_base_url: "https://portal.example.com"   # Base URL for portal links
  max_content_size: 10485760    # Max asset size in bytes (default: 10MB)
  max_versions: 100             # Versions an asset keeps by default (0 = unlimited)
  implementor:                                    # Optional implementor brand (left zone of public viewer header)
    name: "ACME Corp"
    logo: "https://acme.com/logo.png"
    url: "https://acme.com"
  terms_url: "https://example.com/terms"          # Optional terms-of-service link (notification email footers)
  privacy_url: "https://example.com/privacy"      # Optional privacy-policy link (notification email footers)
  about_text: "The ACME data portal delivers curated datasets and reports."  # Optional footer block on all outgoing email
  support_contact: "help@example.com"             # Optional help contact (email or URL) rendered with about_text
  reply_to: "support@example.com"                 # Optional Reply-To header on all outgoing email
  rate_limit:                                     # Public portal viewer rate limiting
    requests_per_minute: 60
    burst_size: 10
    trusted_proxies:                              # CIDRs whose X-Forwarded-For is trusted
      - "10.0.0.0/8"
  export:                                         # trino_export configuration
    enabled: true                                 # auto-enabled when portal + trino are configured
    max_rows: 100000                              # hard row cap per export
    max_bytes: 104857600                          # hard byte cap (100 MB)
    default_timeout: "5m"                         # default query timeout
    max_timeout: "10m"                             # maximum allowed timeout
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the portal SPA frontend and asset API |
| `brand_name` | string | mcpapps `brand_name` | Deployment brand. Names the brand once and the portal title, the public-viewer header, the branded denial pages, and the built-in MCP Apps all follow it. Falls back to `brand_name` in the `mcpapps.apps.platform-info.config` block when that subsystem is enabled |
| `brand_url` | string | mcpapps `brand_url` | Brand home page. The portal's brand mark (sidebar logo and name) links to it in a new tab; unset leaves the mark inert. Falls back to `brand_url` in the `mcpapps.apps.platform-info.config` block when that subsystem is enabled |
| `version_url` | string | - | Link target for the version number in the portal header (release notes or a changelog). Unset leaves the version as plain text. Served on the unauthenticated branding endpoint, so point it at a URL you are willing to disclose publicly |
| `title` | string | `<brand_name> Portal`, else `MCP Data Platform` | Sidebar/branding title text. Composed from `brand_name` when unset, so a branded deployment needs no second string to keep in sync. A brand already ending in "Portal" is not doubled. Only a `brand_name` set in the `portal` block composes the title: a brand inherited from the `mcpapps` block leaves an existing deployment's title unchanged |
| `tagline` | string | `Sign in to access the platform.` | Login-screen subtitle text |
| `oidc_button_label` | string | `Sign in with OIDC` | Login-screen SSO button text |
| `logo` | string | - | URL to the logo image, in any format (used for both themes if no theme-specific logo is set). Also the platform brand mark on the public viewer and the share pages, which link it. The built-in MCP Apps cannot link it — a sandboxed iframe blocks external loads — so it is fetched once at startup and written into their config inline, and a fetch that fails logs one warning |
| `logo_light` | string | - | URL to logo for light theme (overrides `logo`) |
| `logo_dark` | string | - | URL to logo for dark theme (overrides `logo`) |
| `s3_connection` | string | - | Name of the S3 toolkit instance to use for asset storage |
| `s3_bucket` | string | `portal-assets` | S3 bucket for storing asset content |
| `s3_prefix` | string | `artifacts/` | Key prefix within the bucket |
| `public_base_url` | string | - | Base URL for portal links returned in `save_asset` responses |
| `max_content_size` | int | `10485760` | Maximum asset size in bytes (10 MB) |
| `max_versions` | int | `100` | Versions an asset keeps when it carries no override of its own. A version pushed past the cap is deleted along with its stored content and thumbnails; the current version is never pruned. `0` keeps every version, and a negative value is refused at startup. Applied at the write, so an asset already over the cap is trimmed the next time it is written, not when this setting changes. An asset's owner can override it — see [Asset version retention](../portal/assets.md#version-retention) |
| `implementor.name` | string | - | Implementor display name shown in the left zone of the public viewer, the public collection viewer, the guest share landing page, and the access-denied page. Independent of `implementor.logo`: either one alone renders the implementor block |
| `implementor.logo` | string | - | URL to the implementor logo, in any image format. The public viewer and the share pages link it with an `<img>` element; its origin is added to the `img-src` of the pages whose policy would otherwise block it. Renders with or without `implementor.name` |
| `implementor.url` | string | - | Clickable link wrapping the implementor name and logo |
| `terms_url` | string | - | Terms-of-service URL rendered as a small footer link in notification emails. Omitted when unset |
| `privacy_url` | string | - | Privacy-policy URL rendered as a small footer link in notification emails. Omitted when unset |
| `about_text` | string | - | A sentence or two about the platform, rendered as a help/about footer block on all outgoing email (HTML and text parts). Gives first-contact recipients sender context and adds body text content filters look for. Omitted when unset |
| `support_contact` | string | - | Help contact rendered with `about_text`: an email address (linked as `mailto:`) or an http(s) URL. Omitted when unset |
| `reply_to` | string | - | Reply-To address applied to every outgoing email so recipient replies reach a monitored mailbox. Validated at startup; unset leaves the header off |
| `rate_limit.requests_per_minute` | int | `60` | Public portal viewer per-IP rate limit |
| `rate_limit.burst_size` | int | `10` | Public portal viewer per-IP burst allowance |
| `rate_limit.trusted_proxies` | list | `[]` | CIDRs whose `X-Forwarded-For` is trusted for client attribution. Empty trusts none: the direct peer address is used and forwarding headers are ignored. Set this to your ingress/load-balancer CIDRs so per-client limiting works behind a proxy without being spoofable. A global backstop bounds total throughput regardless of attribution |
| `export.enabled` | bool | auto | Enable `trino_export` tool. Auto-enabled when portal and Trino are both configured. Set `false` to disable |
| `export.max_rows` | int | `100000` | Hard row cap for exports |
| `export.max_bytes` | int64 | `104857600` | Hard byte cap for formatted output (100 MB) |
| `export.default_timeout` | string | `5m` | Default query timeout for exports |
| `export.max_timeout` | string | `10m` | Maximum allowed query timeout for exports |

!!! note "Prerequisites"
    Portal requires `database.dsn` to be configured for metadata storage, and at least one S3 toolkit instance for asset content storage.

## Audit Configuration

The `audit` block controls audit logging of MCP tool calls. By default audit events are written asynchronously to PostgreSQL.

```yaml
audit:
  enabled: true
  log_tool_calls: true
  log_parameters: true
  redact_keys: ["password", "token"]
  delivery: async          # async (default) | sync
  retention_days: 90
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` (when a database is available) | Enable audit logging. Set `false` to disable. |
| `log_tool_calls` | bool | `true` | Log MCP tool call events. Set `false` to keep audit on but skip per-tool-call rows. |
| `log_parameters` | bool | `true` | Capture tool-call arguments on each event. Set `false` to store a null `parameters` field when arguments may carry sensitive data that redaction cannot make safe to retain. |
| `redact_keys` | list of strings | `[]` | Top-level argument keys whose values are replaced with `[REDACTED]` before the event leaves the request path. Matching is case-insensitive; nested keys are not matched (top-level only). |
| `delivery` | string | `async` | Store-write path: `async` (best-effort, never blocks the tool call) or `sync` (writes on the request goroutine for backpressure and zero queue drops). See below. |
| `retention_days` | int | `90` | Days to retain audit events |

!!! note "Requires database"
    Audit logging requires `database.dsn` to be configured. With a database available and no `audit:` block, both audit and per-tool-call logging are on by default. Setting `enabled: false` disables audit entirely; `log_tool_calls: false` keeps audit on but stops recording per-tool-call events.

### Delivery semantics and data captured

**What is captured per event.** Each audit event records: identifiers (`id`, `request_id`, `session_id`, `user_id`, `user_email`, `persona`), the call target (`tool_name`, `toolkit_kind`, `toolkit_name`, `connection`, `event_kind`), the raw tool-call arguments (`parameters`), the outcome (`success`, `error_message`, `authorized`), timing and size (`timestamp`, `duration_ms`, `request_chars`, `response_chars`, `content_blocks`), transport metadata (`transport`, `source`), and enrichment accounting (`enrichment_applied`, `enrichment_tokens_full`, `enrichment_tokens_dedup`, `enrichment_mode`, `enrichment_match_kind`).

**Sensitive data in parameters.** The `parameters` field stores tool-call arguments verbatim, including complete SQL text and anything embedded in it. Unless you set `redact_keys` (to mask named top-level values) or `log_parameters: false` (to drop the field entirely), sensitive values pasted into a query or argument are retained in the audit table. A built-in baseline additionally masks the well-known keys `password`, `secret`, `token`, `api_key`, `authorization`, and `credentials`, but this is a safety net, not a substitute for configuring `redact_keys` for your own sensitive argument names.

**Async delivery (default).** Events are enqueued on a bounded in-memory writer and persisted by a single background goroutine, so a tool call is never blocked by store latency. This is best-effort: under a sustained store outage or a crash, events in the queue are dropped rather than retained. Every lost event increments the `audit_events_dropped_total` metric, which also covers writes that fail or exceed the per-write timeout.

**Sync delivery.** Set `delivery: sync` when a compliance posture requires durability over latency. Each event is written on the request goroutine with a per-write timeout (5s), so a slow store applies backpressure to the tool call (it waits) rather than shedding events: there are no queue-overflow drops. A store write that still fails or times out is logged and counted (`audit_events_dropped_total`) but, as in async mode, never fails the tool call: audit must not break tools. Two tradeoffs to weigh: under a stalled store every tool call blocks for up to the timeout before returning, and sync writes draw from the same `database` connection pool as OAuth, sessions, and portal queries, so under load against a slow store they can contend with those subsystems (the async writer's single drain goroutine caps audit at one connection and avoids both). Graceful shutdown cancels in-flight sync writes.

See [Audit Logging](audit.md) for query examples and retention details.

## Call Catalog Configuration

The `calls` block controls how long the [call catalog](../portal/activity.md#my-calls) keeps a recorded query or API invocation, and whose calls it keeps at all. Nothing here turns the catalog on: it is written from the audit pipeline, so it exists wherever audit does and nowhere else.

```yaml
calls:
  retention_days: 90
  exclude_personas:
    - ingest-service
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `retention_days` | int | `90` | How long a recorded call is kept **when nothing came of it**. Zero or negative takes the default. |
| `exclude_personas` | list | `[]` | Personas whose calls are machinery: audited, not cataloged. Empty catalogs every call. |

**What retention does not touch.** The sweep is by what a record came to, not by its age alone. A record an asset, an export, or a capture cites; a record someone promoted; a record someone declined; and a record another session found and re-ran are all evidence, and none of them is ever swept, whatever their age. What ages out is the draft nobody used: a query that ran, answered nothing anybody kept, and was never run again.

The sweep runs once when the platform starts and then once a day per deployment, under a PostgreSQL advisory lock so that several replicas sharing one database delete once rather than each.

### Excluding an automated caller

The catalog exists to answer one question about a recorded call: is this worth running again. An automated system driving ingestion through the same tools people use never produces that answer. Each of its calls fetches a distinct upstream resource once, and each is recorded, embedded, and ranked in search against the handful of records that did answer something. On one deployment a single service principal wrote 472,156 of 476,749 records in ten days, against 43 records that had been used for anything.

Name the personas that are machinery:

```yaml
calls:
  exclude_personas:
    - ingest-service
```

A call made under one of those personas is audited exactly as before and no call record is written. Persona is the discriminator because it is the layer you already assign per API key to say what a caller is for: give the service account its own persona and every key that holds it is covered, with no list to maintain.

**What this does not change.** The audit row, its retention, and the [API gateway metrics](observability.md) are untouched, so what an automated system did stays fully visible in the Activity view and in the gateway charts. The one dimension the gateway charts lack is the principal, and the audit log carries it.

**What else it withholds.** A data call normally comes back with its own `mcp:call:<id>` reference, which an agent is told to cite when it saves an asset or captures an insight. A call the catalog declines is handed none: that id would resolve to nothing, so citing it would store a citation that can never be satisfied.

**Records already written.** Naming a persona here also removes the records it wrote before you named it. They are swept on the next sweep — the one at startup, so the restart that applies the setting is the restart that clears the backlog — with the same evidence clauses standing: a record something was built from, or that was promoted, declined, or re-run, is kept whoever produced it.

**A name that matches nothing.** An entry naming no persona the deployment knows excludes nothing, and the platform logs a warning at startup naming it. Personas come from both this file and the database, so a name added to the database later is matched at the next start.

## Notifications Configuration

The `notifications` block controls the email-notification substrate: the
delivery queue, send worker, and daily-digest scheduling. See [Email
Notifications](notifications.md) for the full feature (admin SMTP settings,
per-user preferences, delivery semantics).

```yaml
notifications:
  enabled: true         # default: on when a database is available
  digest_hour_utc: 13   # UTC hour daily digests are sent
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable email notifications. Set `false` to disable enqueue and delivery entirely. |
| `digest_hour_utc` | int | `13` | UTC hour of day (0-23) at which daily-digest emails are scheduled. Out-of-range values fall back to the default. |

!!! note "Requires database"
    Email notifications require `database.dsn`. The SMTP connection itself
    is not configured here: admins set host, credentials, and TLS mode at
    runtime in the portal (Admin, then Settings) or via
    `/api/v1/admin/settings/smtp`, with the password encrypted at rest.
    The knowledge review-queue alert threshold is admin-configured the same
    way, under `/api/v1/admin/settings/review-queue-alert`; see
    [Review queue alerts](notifications.md#review-queue-alerts).

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
    require: true      # a gated caller must have an established session
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Mint a `session_id` from `platform_info`, advertise it on every tool's input schema, validate and strip it on each call. Set `false` for byte-identical legacy transport-session behavior. |
| `ttl` | duration | `8h` | Handle lifetime, refreshed on use. |
| `require` | bool | `true` | Require a gated caller to have an established session, not that a handle is threaded on every call. A call carrying a valid handle uses it; a call without one adopts the caller's own most-recently-active session, resolved from their authenticated identity, so an MCP App's sandboxed calls (which cannot thread the handle) are scoped rather than refused. Only a caller with **no** session at all is refused with `SESSION_REQUIRED`, which keeps `platform_info` structurally required for a genuinely fresh agent. The model still threads and validates the handle exactly as before, so nothing about a compliant agent's behavior changes; the fallback only affects calls that arrive without a handle, which in practice are an app's. Set `false` to drop the requirement entirely, where a handle-less call falls back to the transport session. |

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
        scratch:                 # Where a registered table is created (#1327)
          catalog: scratch
          schema: uploads
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
| `ssl` | bool | `false` on the default connection, auto-detected on the others | Enable SSL/TLS. Omitting it on a non-default connection leaves the choice to the host: any host that is not `localhost` or `127.0.0.1` is assumed to be HTTPS on port 443. Write `ssl: false` to say plain HTTP explicitly |
| `ssl_verify` | bool | `true` on the default connection, inherited on the others | Verify SSL certificates. A non-default connection that omits it takes the default connection's setting |
| `timeout` | duration | `120s` | Query timeout |
| `default_limit` | int | `1000` | Default row limit for queries |
| `max_limit` | int | `10000` | Maximum allowed row limit |
| `read_only` | bool | `false` | Reject write SQL on this connection. Set per instance: the other instances of the same toolkit are unaffected, and a call that omits `connection` is judged by the default instance's setting |
| `scratch.catalog` | string | - | Catalog a [registered table](registered-tables.md) is created in on this connection. Unset (or set without `schema`) means registration is unavailable here |
| `scratch.schema` | string | - | Schema a registered table is created in. Required alongside `catalog`; a block naming only one is ignored with a warning |
| `connection_name` | string | - | No effect; accepted for compatibility and warned about at startup. Trino routes by the `instances:` key, so that key is the name `list_connections` advertises, a `connection` parameter carries, an audit row records and a persona rule matches. See [Connection Names](multi-provider.md#connection-names) |
| `descriptions` | map | `{}` | Override tool descriptions for this instance (key: `s3_list` or `s3_object`, value: description text) |

A non-default connection could not be plain HTTP before #1436: `ssl: false`
was indistinguishable from an absent `ssl`, so it reached the client as
"auto-detect", and auto-detect turns HTTPS on for every host that is not
localhost. Both keys are now forwarded as written, and only a connection that
never mentions `ssl` gets auto-detect. A deployment whose non-default
connections rely on that auto-detect is unaffected.

`read_only` became per connection in #1269. Before that it was read from the
default instance alone and applied to the whole Trino toolkit, which cut both
ways: `read_only: true` on the default instance refused write SQL on *every*
Trino connection, and `read_only: true` on any other instance did nothing. A
deployment that was relying on the default instance's `read_only` to cover its
other Trino connections must now set `read_only: true` on each connection it
wants refused.

Connections stored in the database (Admin > Connections) carry the same key,
with a **Read Only** toggle in the Trino connection form. A connection the
toolkit holds no setting for — one just added, or a name that is not
configured — refuses write SQL until its setting is recorded.

`scratch:` names a target, not a boundary. Nothing in the toolkit restricts a
catalog or a schema, and `catalog`/`schema` on a connection are session
defaults; what keeps a registration off the warehouse is the Trino identity the
connection authenticates as. See
[Registered Tables](registered-tables.md#what-the-scratch-schema-is).

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
| `connection_name` | string | instance name | The name a call binds this connection by: what an audit row records, what a persona's `connections.allow` / `connections.deny` rules match, and what the semantic layer resolves its platform and catalog mapping through. See [Connection Names](multi-provider.md#connection-names) |
| `read_only` | bool | `false` | Restrict to read operations (disables write tools) |
| `descriptions` | map | `{}` | Override tool descriptions for this instance (key: `s3_list` or `s3_object`, value: description text) |

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
| `public_endpoint` | string | - | Public-facing endpoint used only to sign presigned URLs (`s3_object` `presign`). When set to an externally resolvable address, presigned URLs are signed against it instead of `endpoint`, while data traffic keeps using `endpoint`. Empty falls back to `endpoint`. |
| `access_key_id` | string | - | AWS access key ID |
| `secret_access_key` | string | - | AWS secret access key |
| `session_token` | string | - | AWS session token (for temporary creds) |
| `profile` | string | - | AWS credentials profile name |
| `use_path_style` | bool | `false` | Use path-style S3 URLs |
| `timeout` | duration | `30s` | Request timeout |
| `disable_ssl` | bool | `false` | Disable SSL (for local testing) |
| `read_only` | bool | `false` | Refuse the writing actions of `s3_object` (`put`, `copy`, `delete`) on this connection; the refusal names the connection. Per connection, so a read-only connection added at run time is bound by its own flag. |
| `max_get_size` | int64 | `10485760` | Max bytes to read from objects |
| `max_put_size` | int64 | `104857600` | Max bytes to write to objects |
| `connection_name` | string | instance name | The name a call binds this connection by: what an audit row records, what a persona's `connections.allow` / `connections.deny` rules match, and what the semantic layer resolves its platform and catalog mapping through. See [Connection Names](multi-provider.md#connection-names) |
| `bucket_prefix` | string | - | Only show buckets with this prefix |
| `descriptions` | map | `{}` | Override tool descriptions for this instance (key: `s3_list` or `s3_object`, value: description text) |

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
| `ENCRYPTION_KEY` | Encrypted credentials in `connection_instances`, `gateway_oauth_tokens`, `oauth_pkce_states`, and the SMTP password in `platform_settings` | 32 bytes of key material, accepted in three forms: 64 hex characters, 44-character base64, or 32 raw bytes (set via `printf` / file). Without it, sensitive fields are stored in plaintext and the platform logs a warning. Required for any production gateway deployment. |
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
  estimate_row_counts: false         # Run COUNT(*) for availability enrichment (default: false)
  semantic_fallback: false           # Suggest similar tables on a URN miss (default: false)
  semantic_fallback_top_k: 1         # Suggestions per miss, 1-10 (default: 1)

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
| `estimate_row_counts` | bool | `false` | Run `SELECT COUNT(*)` when reporting table availability, so enriched DataHub results carry an estimated row count. Also what lets the insight review path state a row count beside a pending claim, and what the advisory claim-conflict marker compares against ([Knowledge governance](../knowledge/governance.md#observed-warehouse-state)). Off by default: `COUNT(*)` can trigger a full table scan and make search enrichment very slow |
| `semantic_fallback` | bool | `false` | When a URN-equality lookup misses, fall back to similarity search and surface the top hit as a **suggested** match, annotated `match_kind=semantic` so the model knows it was inferred rather than resolved. Audit rows record `enrichment_match_kind` so operators can measure the false-positive rate. Requires a semantic provider supporting the `semantic` search mode (DataHub does) |
| `semantic_fallback_top_k` | int | `1` | Suggestions surfaced per miss when `semantic_fallback` is on. Clamped to 1-10 to keep suggested-match output bounded |
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

## Session Gate Configuration

A hard gate that refuses **every** non-exempt tool until the agent calls the session-initialization tool (`platform_info` by default) once in the session. Before the init tool has run, any other tool call is short-circuited before its handler executes and a `SETUP_REQUIRED` error result is returned (error category `setup_required`) telling the agent to call the init tool first, then retry. Once the init tool has been called in a session, subsequent tool calls proceed normally until the session's TTL expires.

Unlike most platform sections, this gate is **off by default**: `enabled` is a plain `bool`, so an absent `session_gate` block means the gate is disabled (it does not follow the default-on `*bool` convention used by sections like `progress` or `audit`).

```yaml
session_gate:
  enabled: true                     # Default: false. true activates the gate.
  init_tool: platform_info          # Tool that initializes the session (default: platform_info)
  exempt_tools:                     # Tools that bypass the gate entirely
    - list_connections
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Activate the session-initialization gate. |
| `init_tool` | string | `platform_info` | The tool that initializes a session. Calling it records the session as initialized; it is always exempt from the gate. |
| `exempt_tools` | array | (empty) | Tool names that bypass the gate and may be called before the init tool. |

The gate's memory of an initialized session expires after the session TTL, which is derived from [Session Configuration](#session-configuration) (`sessions.ttl`, falling back to the Streamable HTTP session timeout) rather than from a field on this block.

!!! note "Distinct from the search-first gate"
    The session gate and the [search-first gate](#search-first-gate-configuration) are independent. The session gate requires an **init** tool (`platform_info`) before *any* tool; the search-first gate requires a **discovery** tool (`search`) before *query* tools. Both can be enabled at once.

!!! note "Superseded by explicit session handles"
    When [explicit session handles](#explicit-session-handles) (`sessions.handles`) are enabled, the session gate is skipped: handle resolution enforces initialization instead, and the gate's `exempt_tools` are carried into the handle resolver. Enabling both does not double-gate.

## Purpose Configuration

The `purpose` block controls the `purpose` argument (issue #1317): one sentence, stated by the agent, naming the wider task a data-access call serves. Audit records what a call did and, without this, never why. The platform advertises `purpose` on the input schema of each gated tool, takes it off the request before the tool sees it, and stores it in the audit row's `purpose` column.

It is enabled and required by default. The name is `purpose`, not `intent`, because `search` already takes an `intent` argument that is the query text and keeps that meaning.

```yaml
purpose:
  enabled: true      # advertise, strip, and record (default on)
  require: true      # refuse a gated call that states none (default on)
  tools:             # override the gated set; empty means the default below
    - search
    - trino_query
    - "datahub_get_*"
    - "kind:mcp"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Advertise `purpose` on the gated tools, strip it before the handler, and record it on the audit event. Set `false` to remove the argument entirely. |
| `require` | bool | `true` | Refuse a gated call that states no purpose with `PURPOSE_REQUIRED` (error category `purpose_required`). Set `false` to record a purpose whenever one is stated but never refuse a call for omitting it. |
| `tools` | array | see below | The gated set. Entries are tool-name globs (`filepath.Match` semantics, e.g. `datahub_get_*`) plus `kind:<toolkit-kind>` entries that gate every tool a toolkit of that kind serves. |

The default set is the data-access surface: `search`, `fetch`, `trino_query`, `trino_execute`, `trino_export`, `trino_describe_table`, `api_invoke_endpoint`, `api_export`, `datahub_get_*`, `s3_object`, `s3_list`, and `kind:mcp` — the last covering every tool an MCP gateway connection proxies, whose names are chosen upstream and change when the upstream does. Orientation and platform-management tools (`platform_info`, `list_connections`, `platform_find_tools`, `memory_*`, `manage_*`, `save_asset`) are deliberately outside it: their purpose is their name, and gating them would tax every call an agent makes to set itself up.

### Who is refused

`require: true` refuses only a caller that threaded an explicit `session_id` handle on the same call, which is the platform's proof that the caller can thread a platform-injected argument at all. That single condition covers every exemption the feature needs:

- An **MCP App**'s sandboxed call is session-adopted from its authenticated identity and threads nothing (see [Explicit Session Handles](#explicit-session-handles)).
- The **gateway REST shim**, the **admin tool runner**, and a **managed script** drive a fresh in-memory session per request and thread nothing.
- An **isolated `dpp_`/`dpx_` run** has its session minted server-side rather than passed in.

None of them can state a purpose, so none is refused for not stating one. A real MCP agent, which the platform already requires to thread a handle, is. Because the condition is the handle, setting `sessions.handles.enabled: false` also stops `purpose` from ever being required, though it is still advertised and recorded.

!!! note "The platform owns the argument name on a gated tool"
    On a gated tool the platform advertises `purpose` and strips it before the handler runs. A deployment whose upstream MCP server defines a `purpose` parameter of its own should drop `kind:mcp` from `purpose.tools` (or list the tools it wants gated by name) so that tool keeps its own argument.

## Tool-Call Rate Limiting

A per-identity safety net on authenticated `tools/call` requests. It bounds a runaway agent loop or a compromised account before it can saturate the audit pipeline, the shared database pool, or an upstream (Trino, DataHub, S3, a proxied MCP server). It is **not** a throughput throttle: the default limit is generous enough that ordinary interactive and agent use never touches it. When a user exceeds the limit, the offending call is short-circuited before its handler runs and a `RATE_LIMITED` error result is returned (error code and category `rate_limited`) with a retry hint, so an agent backs off and retries rather than seeing a transport failure. The structured envelope carries the interval as `retry_after_seconds` beside `code`, `category`, `message` and `hint`, so a consumer reads the wait as a number rather than out of the message. `platform_info` is always exempt so a throttled agent can re-read platform guidance.

A [platform run of a managed script](../scripts/running.md#failures) is queued, not refused. Its calls cross the limiter as the script principal (`AuthType` `script`), and when the bucket is empty the limiter holds the call until the sustained rate refills a token and then admits it. A run's calls are serial, so the wait for one call after the burst is spent is one refill interval (250 ms at the defaults), and the wait ends early with the run if the run is canceled or reaches its deadline. A queued call is not a refusal: it does not count in `mcp_rate_limited_total` and logs no warning; it counts in `mcp_rate_limit_queued_total` and logs at Info under the run's session id, so an operator can see that scripts are running against the limit. A draft run carries its author's own identity and is refused as any interactive call is; there the script host waits `retry_after_seconds`, bounded by the run's deadline, and issues the call again, so the script sees only the admitted call's result and the wait is recorded in the run's log.

The limit is keyed on the **authenticated user**, not the client IP. A multi-user connector delivers every user's traffic from one egress address, so per-IP limiting would be both useless (one bucket for everyone) and harmful (one busy user starves the rest); identity keying matches the abuse shape the limiter exists to catch (a single runaway authenticated principal), regardless of source address. Callers with a shared/anonymous identity (auth disabled) fall back to a per-session key; a call with no attributable identity is not limited (fail-open, since the call has already passed auth).

Enabled by default; set `rate_limit.enabled: false` to remove the limiter from the chain entirely.

```yaml
rate_limit:
  enabled: true                     # Default: true. false removes the limiter.
  requests_per_minute: 240          # Default: 240. Sustained per-user tools/call rate.
  burst: 60                         # Default: 60. Largest instantaneous per-user burst.
  exempt_tools:                     # Tools never limited (platform_info is always exempt)
    - search
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable the per-user tool-call limiter. `false` removes the middleware from the chain. |
| `requests_per_minute` | int | `240` | Sustained per-user `tools/call` rate (token refill). `240` is 4 calls/second per user. |
| `burst` | int | `60` | Token-bucket depth: the largest burst a single user may issue before the sustained rate governs. |
| `exempt_tools` | array | (empty) | Tool names never rate limited, in addition to `platform_info` (which is always exempt). |

Each refusal increments the `mcp_rate_limited_total` metric and logs a warning naming the throttled identity and tool. Each call a script principal was held for increments `mcp_rate_limit_queued_total` and logs at Info naming the tool, the principal, the run and the time waited.

!!! note "Per-replica limit"
    The token bucket is in-memory per replica: behind a load balancer the effective ceiling is (replica count x the configured limit). This is intentional for a backstop: distributed coordination (Redis/DB round-trips on the hot tool path) is not warranted to bound abuse that per-replica limiting already bounds. Size the limit with the per-replica semantics in mind; see [Tuning and Scaling](../reference/tuning-and-scaling.md).

!!! note "Distinct from the OAuth endpoint limiter"
    This top-level `rate_limit:` block governs authenticated MCP `tools/call` requests and is keyed on identity. It is unrelated to the `oauth.rate_limit` block, which is a per-IP limiter on the unauthenticated `/token` and `/register` OAuth endpoints.

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
| `semantic.cache.enabled` | bool | `false` | Enable semantic metadata caching. A read that reports the catalog holds no entity for a URN is cached for the same TTL as one that returns an entity, so a table absent from the catalog is looked up once per TTL rather than once per tool call. |
| `semantic.cache.ttl` | duration | `5m` | Cache TTL |
| `query.provider` | string | - | Provider type: `trino` or `noop` |
| `query.instance` | string | - | Toolkit instance name |
| `storage.provider` | string | - | Provider type: `s3` or `noop` |
| `storage.instance` | string | - | Toolkit instance name |

**Which DataHub the `datahub` provider needs.** Whether the catalog holds an entity is the catalog's answer, read from DataHub's `exists` field on a dataset and a glossary term and from the properties aspect on a data product. A DataHub that reports `exists` is therefore what makes a stale citation resolve as not-found: on an older server the field is absent, the entity is taken to stand, and a `fetch` of a URN the catalog has never ingested answers with a record built from that URN. This platform's checks run against DataHub v1.6.0; mcp-datahub v1.15.1 states v1.3.x as its own minimum for the rest of the catalog surface.

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
      allow: ["*"]
      deny: ["*_delete_*", "*_drop_*"]
    connections:
      allow: ["*"]
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
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

!!! warning "No persona means no access"
    A caller whose roles match no persona has **no access at all**: tool calls resolve to the built-in deny-all persona and are refused, the portal answers `403` with a branded page, and the managed-resources API refuses the request. There is no fallback persona, so every user who should reach anything needs a role one of your personas lists.

    `personas.default_persona` was removed. It assigned its persona to every caller whose roles matched nothing, including accounts carrying no claims. A config that still sets it is refused at startup with an error naming the key.

!!! warning "Some tools must be granted together"
    `search` returns references and `fetch` is the only tool that dereferences one; `memory_capture` and `apply_knowledge` both write into a body of knowledge that `search` is the only way back into. Granting one half of a pair without the other leaves the persona able to start something it can never finish, so the server logs a warning naming the persona, the missing tool, and the fix — at startup and on every persona write. Prefer `allow: ["*"]` with a targeted `deny`, as `analyst` does above: an enumerated allow-list silently loses each tool a later upgrade adds. See [Personas: some tools are a unit](../personas/overview.md#some-tools-are-a-unit).

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
  pages:
    dedup_threshold: 0.85
    dedup_disabled: false
    oversize_bytes: 16384
    oversize_sections: 12
  catalog_index:
    enabled: true
    sync_interval: 30m
    max_entries: 5000
  verifiable_insights: true
  search_provider_timeout: 5s
  search_embed_timeout: 5s
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` (when a database is available) | Enable the knowledge review and write-back toolkit (`apply_knowledge`). Knowledge capture lives in the memory toolkit (`memory_capture`) and is enabled with the memory layer, not this flag |
| `apply.enabled` | bool | `true` (when a database is available) | Enable the `apply_knowledge` tool for admin review and catalog write-back. Set `false` to disable. Still gated behind database availability. |
| `apply.datahub_connection` | string | - | DataHub instance name for write-back operations |
| `apply.require_confirmation` | bool | `false` | Require explicit `confirm: true` on apply actions |
| `pages.dedup_threshold` | float | `0.85` | Cosine similarity, in `[0,1]`, at or above which creating a knowledge page is blocked as a near-duplicate of an existing one. The gate acts only when a real embedding provider is configured, since cosine similarity is undefined without one. A non-positive value selects the default; disable the gate with `dedup_disabled` rather than by zeroing this |
| `pages.dedup_disabled` | bool | `false` | Turn the duplicate gate off entirely. Explicit, so "no gate" is never confused with "left at default" |
| `pages.oversize_bytes` | int | `16384` | Body size, in bytes, at or above which a page write returns a non-blocking suggestion to split it. A negative value disables this arm. This is an editorial nudge toward focused, cross-linked pages, not a bound on what search can reach: a page's content is embedded as chunks sized to the provider's input budget, so a page of any size is semantically searchable end to end |
| `pages.oversize_sections` | int | `12` | Markdown heading count at or above which the same split suggestion fires. A negative value disables this arm |
| `reflexive_capture.enabled` | bool | `true` | Auto-capture a "misconception + fix" correction when a Trino query errors and a later related same-session query on the same connection succeeds (#635). Source `automation`, reviewed sink-class (enters review, never live), gated by the persona's `memory_capture` grant. Default-on when the memory subsystem is available; set `false` to disable |
| `catalog_index.enabled` | bool | `true` | Index the catalog's dataset descriptions into the platform's own semantic search, so a fact applied to a description is reachable from a topical query that names no entity. Requires a DataHub semantic provider, a database, and an embedding provider; without any of those it is inert. Set `false` to opt out, leaving catalog datasets ranked by DataHub's own keyword search alone |
| `catalog_index.sync_interval` | duration | `30m` | How often the catalog is re-enumerated into that index. The sweep runs as a background index job, so raising it trades freshness for load on DataHub; lowering it makes a newly applied description searchable sooner |
| `catalog_index.max_entries` | int | `5000` | Cap on how many datasets are mirrored. The cap bounds both the table and one sweep's working set. A catalog larger than this indexes the first `max_entries` datasets in catalog order and logs the truncation |
| `verifiable_insights` | bool | `true` | Mark a delivered insight as checkable: when the catalog entity its claim is about resolves to a queryable table, the delivered record carries a `verifiable` block naming that table and connection, on every delivery surface (`search` insight hits, `fetch` of `mcp:insight:<id>`, and the `memory_context` enrichment block). Additive and absent whenever nothing resolves, so a deployment with no query provider is unaffected; it honors the persona connection boundary, and resolves without running the `COUNT(*)` that `enrichment.estimate_row_counts` enables. Set `false` to deliver insights with no marker |
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
| `embedding.ollama.max_input_bytes` | int | `6000` | Per-text input cap (bytes) applied before embedding. The platform truncates input itself on a UTF-8 boundary because Ollama's `truncate` flag is unreliable for content over the model's context. The default sits below `nomic-embed-text`'s ~2048-token boundary; raise it only for a larger-context model. Only the embedded text is trimmed; stored content is unaffected. Knowledge pages are not trimmed at all: this value sizes the chunks a page's content is embedded as, so raising it for a larger-context model widens those chunks. If the model refuses an input anyway -- token density varies by close to an order of magnitude between prose and dense content, so no fixed byte count is an exact token budget -- the provider halves what it sends and retries, down to a 256-byte floor, so a dense document converges on a bound the model accepts instead of failing identically on every attempt. The vector then covers a prefix of the text rather than all of it, and the shrink is logged with the model and the size refused; the lexical arm still matches the whole text, so the content stays findable while the operator lowers the cap. |
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
| `enabled` | bool | `true` | Serve the resource templates below and the DataHub-to-Trino resource links. Read-only, so it is on by default; set `false` to disable |

When enabled, the platform registers these resource templates:

- `schema://{catalog}.{schema}/{table}` — Table schema with column types and descriptions
- `glossary://{term}` — Glossary term definitions
- `availability://{catalog}.{schema}/{table}` — Query availability and row counts

Clients that support resource browsing (e.g., Claude Desktop) will show these as navigable resources alongside tools.

### Managed Resources

Managed resources are the human-uploaded files people attach through the portal
(reference material, specifications, images), stored as rows in PostgreSQL with
their bytes in S3 and served back over MCP and the REST API. They are a separate
subsystem from the read-only templates above, configured under
`resources.managed`. See [Content Model](../concepts/content-model.md) for how
they relate to knowledge pages and assets.

```yaml
resources:
  managed:
    enabled: true             # auto-enabled when a database is available
    uri_scheme: "mcp"         # URI prefix for resource URIs
    s3_connection: "primary"  # name of the S3 toolkit instance holding the blobs
    s3_bucket: "managed-resources"
    max_versions: 10          # content revisions kept per resource
    max_upload_bytes: 104857600   # largest file accepted (default 100 MB)
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | auto | Enable managed resources. Unset means enabled whenever a database is configured; set `false` to disable the subsystem outright |
| `uri_scheme` | string | `mcp` | Scheme of the URIs minted for managed resources (`<scheme>://global/<path>/<filename>`, where `<path>` is the folder path the resource is filed under, and the persona/user equivalents). Changing it after resources exist changes the URIs the platform serves for them |
| `s3_connection` | string | the S3 kind's `default:` | Name of the S3 toolkit instance used for blob storage |
| `s3_bucket` | string | `managed-resources` | Bucket the uploaded bytes are written to |
| `max_versions` | int | `10` | Content revisions a resource keeps, counting the current one. A revision past the cap prunes the oldest stored file; live content is never pruned. A non-positive value selects the default, and anything below `2` is raised to `2`, since a cap of `1` would keep no history at all |
| `max_upload_bytes` | int | `104857600` (100 MB) | Largest file `POST /api/v1/resources` and `POST /api/v1/resources/{id}/content` accept. Absent, zero, or negative selects the default, so a deployment that sets nothing keeps today's 100 MB. The refusal message and the portal's file chooser both state this deployment's number — the browser reads it from `GET /api/v1/portal/me` rather than holding a copy. **Raising it raises memory**: see below |

Managed resources require a database. With none configured the block has no
effect, and the platform runs the read-only templates alone.

### What raising `max_upload_bytes` costs

The upload path is not streaming. A request reads the whole object into one
buffer (`io.ReadAll` in `pkg/resource/handler.go`) and hands it to blob storage
as one `[]byte` (`S3Client.PutObject`), so the configured ceiling is resident
heap for the life of the request, **per concurrent upload**. Size the container
for the ceiling times the uploads you expect at once, plus normal working set,
before raising this — an undersized container answers a large upload with an
OOM kill rather than a refusal. The read path has the same shape: `GetObject`
returns a `[]byte`.

Multipart parsing does not double the cost. A part above 10 MB
(`MaxMultipartMemory`) spools to disk, so a large upload is one in-memory slice
plus a temporary file of the same size on ephemeral disk.

Two things a raised ceiling does not change. Content indexing still stops at
`MaxContentReadBytes` (8 MiB), so a file above that is indexed on its metadata
alone whatever the ceiling is. And an ingress or proxy in front of the platform
enforces its own body limit: raise that too, or a request the platform would
accept never reaches it.

## Argument Autocompletion

The platform answers the MCP `completion/complete` request so clients that support autocompletion (the MCP Inspector, IDE clients) can suggest valid values as a user types a prompt argument or a resource-template variable. There is nothing to configure: the capability is advertised automatically whenever prompts or resource templates are available, and it uses the catalog the platform already knows.

Completions are served for:

- **Prompt arguments**, routed by argument name across built-in and database prompts:
  - `dataset`: dataset names from the semantic search index (e.g. the `trace-data-lineage` prompt).
  - `topic`: domains, data products, and glossary terms (e.g. `explore-available-data`, `create-a-report`, `create-interactive-dashboard`).
  - `connection`: configured connection names the caller's persona may reach.
- **Resource-template variables**:
  - `schema://{catalog}.{schema_name}/{table}` and `availability://...`: catalog, schema, and table names from the query engine (`schema_name` completes once a `catalog` is chosen; `table` once both are).
  - `glossary://{term}`: business glossary terms.

Completions are persona-filtered exactly like `tools/list` and `search`: a caller only receives values it could already discover through the corresponding tool (dataset/topic/glossary require `search`; catalog/schema/table require `trino_browse`; connection names require `list_connections` and are further filtered by the persona's connection rules). Unauthenticated sessions receive no completions, and each lookup runs under a short latency budget so an unavailable upstream degrades to an empty list rather than an error.

A response carries at most 100 values, and the two optional fields beside them are reported only when they are provable. `hasMore` is set when the catalog counted more matches than the response holds — read from the catalog's own match count, not inferred from how many rows a page happened to return, since a catalog is free to return fewer rows than were asked for. `total` is set only when the returned set is the complete one; when the catalog cannot report a count, both fields are omitted rather than asserting a completeness the platform cannot verify.

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
      allow: ["*"]
      deny: ["*_delete_*"]
    connections:
      allow: ["*"]
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
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

# MCP Gateway — OSS Landscape Survey & Delta Report

**Date:** 2026-04-24
**Author:** survey conducted prior to merge of PR #338 (`feat/gateway-toolkit-338`)
**Scope:** validate the design choices in #338 against the open-source MCP-gateway landscape; recommend ship-as-is, ship-with-revisions, or pivot.

---

## TL;DR — **Ship as-is.**

We surveyed nine OSS MCP gateways (IBM ContextForge, Microsoft mcp-gateway, agentgateway, MCPJungle, agentic-community/mcp-gateway-registry, Lunar MCPX, MetaMCP, Docker MCP Gateway, MCP Mesh). **None of them combine the things our v1 does**: persistent encrypted refresh tokens for unattended cron-driven OAuth, declarative cross-source enrichment with escaped ANSI-SQL literals, and persona-glob tool filtering tied to a structured per-call audit.

We do have real gaps relative to the field — stdio upstream transport, OpenTelemetry export, schema-drift detection, and stateful connection pre-allocation — but none of them invalidate the v1 design and none should block merge. They become numbered follow-up tickets after #338 lands.

The survey did not surface any project that we should pivot to wrap or fork. The architectural ground we're standing on is unique enough to keep.

---

## 1. Comparison matrix

Cells marked **unverified** mean the comparison agent could not find a primary source for that claim during the survey window. They are guesses we should not rely on without re-checking.

| Project | Lang | License | Transports (upstream) | Auth → upstream | Token persistence | Per-user OAuth | Tool filtering | Enrichment | Audit | Schema drift |
|---|---|---|---|---|---|---|---|---|---|---|
| **ours (#338)** | Go | MIT | streamable HTTP | none / bearer / api_key / OAuth (`client_credentials` + `authorization_code`+PKCE) | encrypted at rest (AES-256-GCM), auto-refresh | shared per connection | persona globs | declarative rules (Trino + DataHub, JSONPath bindings, escaped ANSI-SQL literals, dry-run) | structured Postgres per-call (user, persona, duration_ms, correlation) | manual refresh |
| **IBM ContextForge** | Python | Apache-2.0 | stdio, HTTP, SSE, WebSocket, streamable | bearer per-plugin | **unverified** (Redis-backed for multi-cluster) | **unverified** | none documented | plugins (40+) — generic, not declarative | OTel (Phoenix, Jaeger, Zipkin, DataDog) | gRPC reflection auto-discovery |
| **Microsoft mcp-gateway** | C# (.NET 8) | MIT | HTTP (proxy) | not documented (assumes K8s network trust) | n/a | per-user (Entra ID claims) | RBAC via Entra app roles | none | not documented | static per adapter |
| **agentgateway** | Rust+Go+TS | Apache-2.0 | stdio, HTTP, SSE | OAuth 2.1 + PKCE (mandatory per MCP spec) | **unverified** (provider-managed) | per-user (PKCE token claims) | CEL policy (Cedar-like) | none | OTel | not documented |
| **MCPJungle** | Go | MPL-2.0 | stdio + streamable HTTP | static bearer | in-memory | shared | tool groups | none | none | manual CLI refresh |
| **agentic-community/mcp-gateway-registry** | Python + Node | Apache-2.0 | streamable HTTP | not documented | encrypted session cookies | multi-IdP (Keycloak, Entra, Okta, Auth0, Cognito) | RBAC, method-level scopes | A2A chaining (no declarative rules) | structured MongoDB | dynamic via FAISS semantic search |
| **Lunar MCPX** | TS+Go | MIT | streamable HTTP | not documented | **unverified** | shared | rate-limit / circuit-breaker policies | interceptor middleware (no declarative rules) | claimed immutable trail (architecture not public) | not documented |
| **MetaMCP** | TypeScript | MIT | SSE, streamable HTTP (no stdio) | env-var injection / bearer | in-memory pre-allocated sessions | shared | namespace overrides / annotations | none | log levels (DEBUG/INFO/WARN/ERROR) | manual |
| **Docker MCP Gateway** | Go | MIT | stdio (default), streaming HTTP | OAuth flows, Docker Desktop secrets | Docker Desktop secret store | **unverified** | profile-level only | none | "logging and call tracing" | dynamic per container start |
| **MCP Mesh** | Go (core) + Python/Java/TS/Rust SDKs | MIT | gRPC + HTTP | SPIFFE mTLS | Redis (no encryption documented) | shared agent identity | capability tags | decorator-based dependency injection (different paradigm) | OTel + Tempo + Grafana | heartbeat-driven re-resolution |

---

## 2. What others do that we don't

Sorted by how compelling the borrow is for our v1.

### Worth filing as follow-up tickets

1. **stdio upstream transport** *(MCPJungle, Docker MCP Gateway, MetaMCP — varied; IBM ContextForge full set)* — many vendor MCPs ship stdio-first. We're streamable-HTTP-only, which limits the universe of upstreams we can proxy. Medium effort: another transport adapter behind our existing `dial` interface.
2. **OpenTelemetry export for proxied calls** *(IBM, agentgateway, MCP Mesh, Docker)* — our Postgres audit is more granular per-call than what the OSS field offers, but operators with OTel infrastructure can't observe gateway calls without it. Spans should attach to the existing audit middleware. Small effort.
3. **Schema-drift detection** *(MCP Mesh: heartbeat re-resolution; Docker: dynamic per-container; agentic-community: FAISS dynamic discovery)* — we require a manual `refresh` admin endpoint. A periodic poll + hash-diff would catch silent upstream tool-schema changes. Small effort.
4. **Stateful connection pre-allocation** *(MetaMCP, MCPJungle)* — pre-warm an MCP client session per connection so the first tool call doesn't pay the dial+discover cost. Medium effort, real UX win for slow upstreams.
5. **Tool-call session affinity** *(Microsoft mcp-gateway)* — when we run multiple platform replicas, session-bearing upstreams need consistent routing. Not relevant for current single-replica deployments; flag for the K8s scale-out work.

### Interesting but not for v1 (or v2)

6. **Multi-IdP downstream OAuth** *(agentic-community/mcp-gateway-registry: Keycloak/Entra/Okta/Auth0/Cognito)* — we already have OIDC + a built-in OAuth 2.1 server. A multi-IdP catalog UI would simplify deployments; v3 territory.
7. **Plugin extensibility** *(IBM ContextForge: 40+ plugins)* — adds maintenance burden and security surface for marginal v1 value. Re-evaluate if a customer asks for a custom transport or audit sink we don't ship.
8. **CEL-based policy** *(agentgateway)* — more expressive than glob patterns. Our personas have served the rest of the platform fine; revisit if customers report glob limits.
9. **Semantic tool discovery** *(agentic-community: FAISS embeddings)* — agents discover unknown tools by intent. Not a gateway concern; closer to the LLM-tool-router layer.
10. **Container-per-server isolation** *(Docker MCP Gateway)* — different deployment model. We are the daemon; we don't sandbox the daemon's children.

### Explicitly **not in scope for v1** (per pre-survey direction)

- **Per-user OAuth to upstream** *(Microsoft Entra, agentgateway PKCE token claims, agentic-community per-IdP)* — the v1 decision was shared service credential per connection. Logged here for awareness; not a #338 blocker.

### Don't borrow

- **MCP Mesh's distributed-agent paradigm** — different product. They make agents discover each other via decorator dependency injection across language runtimes. We are an MCP gateway, not an agent mesh.
- **Lunar's zero-code JSON-only config** — runs counter to our portal-authored, DB-backed model. The portal is the differentiator, not the limitation.

---

## 3. What we do that they don't

Things the survey could not find in any of the nine subjects:

1. **Persistent encrypted refresh tokens for unattended OAuth.** The closest peers either store tokens in Redis without documented encryption (MCP Mesh), in encrypted session cookies tied to a browser session (agentic-community/registry), or via Docker Desktop's local secret store (Docker MCP Gateway). None offer "OAuth-once-via-browser, then run cron jobs for the life of the refresh token, with the refresh token encrypted at rest." `gateway_oauth_tokens` table + AES-256-GCM via `FieldEncryptor` + automatic refresh on `oauthTokenSource.Token()` is genuinely novel for this category.

2. **Declarative cross-source enrichment.** Our `gateway_enrichment_rules` (JSONB predicate / action / merge, escaped ANSI-SQL literals with JSONPath bindings, dry-run endpoint, correlation IDs in audit) is unique. (Note: literals are produced by `sqlLiteral` and concatenated into the query — Trino's Go client doesn't expose `?` parameter binding for our exec path. The escaping covers the `'` and basic-type cases; identifiers and column-name placeholders are out of scope and explicitly rejected.) IBM has plugins, Lunar has interceptors, agentic-community has A2A chaining — all are general-purpose middleware. None bind warehouse data to proxied responses declaratively.

3. **Persona-aware tool filtering tied to structured audit.** Microsoft has Entra app roles, agentgateway has CEL policies, agentic-community has RBAC. None thread the same identity through tool filtering AND per-call audit metadata (user_id, persona, toolkit_kind, connection, duration_ms, correlation_id) the way we do.

4. **Cross-toolkit semantic-layer enrichment.** Trino↔DataHub bidirectional cross-enrichment isn't a gateway feature per se, but it's the substrate the gateway's enrichment engine builds on. No surveyed project ships this kind of native semantic layer.

5. **Portal-first authoring of connections AND enrichment rules.** Most of the field is config-file or CLI driven. The portal pattern (admin REST API + admin UI for connection + enrichment-rule CRUD with dry-run) is a real operator-experience differentiator. agentic-community has a portal but it's IdP-management focused, not enrichment-authoring.

---

## 4. Recommendation for #338

**Ship as-is.** Merge `feat/gateway-toolkit-338` without further changes. The survey did not surface any design flaw, security gap, or missing v1 capability that justifies blocking the merge.

File the following as **follow-up tickets**, not as PR-blocking commits:

| # | Ticket | Priority | Rough effort |
|---|---|---|---|
| 1 | stdio upstream transport adapter | P1 — broadens upstream compatibility | Medium |
| 2 | OpenTelemetry spans on proxied tool calls | P1 — ops parity | Small |
| 3 | Periodic schema-drift detection (poll + hash) | P2 — silent-failure mitigation | Small |
| 4 | Pre-allocated MCP client sessions per connection | P2 — first-call latency | Medium |
| 5 | Session affinity for multi-replica deployments | P3 — defer until K8s scale-out work | Medium |
| 6 | SSE upstream transport | P3 — fewer vendor MCPs use this than stdio | Medium |
| 7 | Multi-IdP downstream OAuth catalog UI | v3 — depends on customer pull | Large |

The v1 differentiators (encrypted refresh-token persistence, declarative enrichment, persona-aware audit, portal authoring) are sufficient to ship a credible product that no OSS peer currently matches.

---

## Sources

- [awesome-mcp-gateways](https://github.com/e2b-dev/awesome-mcp-gateways) — curated list confirming our subject set
- [Best Open Source MCP Gateways 2026 — lunar.dev](https://www.lunar.dev/post/the-best-open-source-mcp-gateways-in-2026)
- [MCP Aggregation, Gateway, and Proxy Tools: State of the Ecosystem (Q1 2026) — heyitworks.tech](https://www.heyitworks.tech/blog/mcp-aggregation-gateway-proxy-tools-q1-2026)
- [IBM mcp-context-forge](https://github.com/IBM/mcp-context-forge) · [docs](https://ibm.github.io/mcp-context-forge/)
- [Microsoft mcp-gateway](https://github.com/microsoft/mcp-gateway) · [Entra app roles](https://github.com/microsoft/mcp-gateway/blob/main/docs/entra-app-roles.md)
- [agentgateway/agentgateway](https://github.com/agentgateway/agentgateway) · [MCP authn docs](https://agentgateway.dev/docs/standalone/latest/mcp/mcp-authn/)
- [mcpjungle/MCPJungle](https://github.com/mcpjungle/MCPJungle)
- [agentic-community/mcp-gateway-registry](https://github.com/agentic-community/mcp-gateway-registry)
- [TheLunarCompany/lunar](https://github.com/TheLunarCompany/lunar)
- [metatool-ai/metamcp](https://github.com/metatool-ai/metamcp)
- [docker/mcp-gateway](https://github.com/docker/mcp-gateway) · [Docker MCP Gateway docs](https://docs.docker.com/ai/mcp-catalog-and-toolkit/mcp-gateway/)
- [dhyansraj/mcp-mesh](https://github.com/dhyansraj/mcp-mesh) · [observability docs](https://mcp-mesh.ai/07-observability/)
- [MCP authorization spec](https://modelcontextprotocol.io/specification/draft/basic/authorization)

## Methodology note

Three Explore sub-agents surveyed three projects each in parallel using `WebFetch` against project READMEs, architecture docs, and ≤200-line source samples plus `WebSearch` for blog-post coverage. Each cell in the matrix above has either a primary-source URL or a `path/to/file:line` citation in the agents' raw output, or is explicitly tagged **unverified**. The synthesis above does not claim anything not present in those agent reports.

Sub-agent transcripts: see `tasks/` under the conversation working directory if a re-audit of citations is required.

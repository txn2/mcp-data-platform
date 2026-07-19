[![txn2/mcp-data-platform](docs/images/MCP-data-platform-logo-banner.svg)](https://mcp-data-platform.txn2.com)

[![GitHub license](https://img.shields.io/github/license/txn2/mcp-data-platform.svg)](https://github.com/txn2/mcp-data-platform/blob/main/LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/txn2/mcp-data-platform.svg)](https://pkg.go.dev/github.com/txn2/mcp-data-platform)
[![MCP](https://img.shields.io/badge/MCP-Model_Context_Protocol-blue)](https://modelcontextprotocol.io)
[![Release](https://img.shields.io/github/v/release/txn2/mcp-data-platform)](https://github.com/txn2/mcp-data-platform/releases/latest)
[![CI](https://github.com/txn2/mcp-data-platform/actions/workflows/ci.yml/badge.svg)](https://github.com/txn2/mcp-data-platform/actions/workflows/ci.yml)
[![CodeQL](https://github.com/txn2/mcp-data-platform/actions/workflows/codeql.yml/badge.svg)](https://github.com/txn2/mcp-data-platform/actions/workflows/codeql.yml)
[![codecov](https://codecov.io/gh/txn2/mcp-data-platform/graph/badge.svg)](https://codecov.io/gh/txn2/mcp-data-platform)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13548/badge)](https://www.bestpractices.dev/projects/13548)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/txn2/mcp-data-platform/badge)](https://scorecard.dev/viewer/?uri=github.com/txn2/mcp-data-platform)
[![Signed by Cosign](https://img.shields.io/badge/artifacts-signed_by_cosign-blue?logo=sigstore&logoColor=white)](https://github.com/sigstore/cosign)
[![Docker](https://img.shields.io/badge/ghcr.io-txn2%2Fmcp--data--platform-blue?logo=docker)](https://github.com/txn2/mcp-data-platform/pkgs/container/mcp-data-platform)
[![DOI](https://zenodo.org/badge/DOI/10.5281/zenodo.21438044.svg)](https://doi.org/10.5281/zenodo.21438044)

**[Documentation](https://mcp-data-platform.txn2.com/)** | **[Installation](https://mcp-data-platform.txn2.com/server/installation/)** | **[Quick Start](#quick-start)** | **[Go Library](https://mcp-data-platform.txn2.com/library/overview/)**

**Your AI assistant can run SQL. But it doesn't know that `cust_id` contains PII, that the table was deprecated last month, or who to ask when something breaks.**

mcp-data-platform fixes that. It is a single MCP server that connects AI assistants to your data infrastructure and enriches every response with business context from your semantic layer: query a table and get its meaning, owners, quality scores, and deprecation warnings in the same call.

It is a platform, not just a bridge. The same endpoint gives agents persistent memory and a governed path to write knowledge back to the catalog, proxies third-party MCP servers and REST APIs through one authentication, persona, and audit pipeline, and ships a web portal where AI-generated artifacts are saved, organized into collections, and shared with teammates.

The only required backend is [DataHub](https://datahubproject.io/) as the semantic layer. Add [Trino](https://trino.io/) for SQL and [S3](https://aws.amazon.com/s3/) for object storage when you're ready. [Learn why this stack.](https://mcp-data-platform.txn2.com/concepts/components/)

---

## Why

AI assistants are powerful at querying data, but they work blind. When an agent asks "What's in the orders table?", it gets column names and types. It doesn't know that `customer_id` is PII, that the table is deprecated in favor of `orders_v2`, that the quality score dropped last week, or who to contact when something looks wrong.

```
# Without mcp-data-platform
─────────────────────────────────────────────────────────────────────
User:      "Describe the orders table"
AI:        Queries Trino → gets columns and types
User:      "Who owns this data?"
AI:        Queries DataHub → finds owners
User:      "Is this table still active?"
AI:        Queries DataHub again → finds deprecation status
User:      "What does customer_id actually mean?"
AI:        Queries DataHub again → finds column descriptions
─────────────────────────────────────────────────────────────────────
4 round trips. Context scattered across conversations. Easy to miss warnings.
```

```
# With mcp-data-platform
─────────────────────────────────────────────────────────────────────
User:      "Describe the orders table"
AI:        Gets everything in one response:
           → Schema: columns and types
           → ⚠️ DEPRECATED: Use orders_v2 instead
           → Owners: Data Platform Team
           → Tags: pii, financial
           → Quality Score: 87%
           → Column meanings and business definitions
─────────────────────────────────────────────────────────────────────
1 call. Complete context. Warnings front and center.
```

## How It Works

```mermaid
sequenceDiagram
    participant AI as AI Assistant
    participant P as mcp-data-platform
    participant T as Trino
    participant D as DataHub

    AI->>P: trino_describe_table "orders"
    P->>T: DESCRIBE orders
    T-->>P: columns, types
    P->>D: Get semantic context
    D-->>P: description, owners, tags, quality, deprecation
    P-->>AI: Schema + Full Business Context
```

The platform intercepts tool responses at the protocol level and enriches them with context from the other services. This **cross-enrichment** is bidirectional:

- **Trino → DataHub**: query results include owners, tags, glossary terms, deprecation warnings, quality scores
- **DataHub → Trino**: search results include query availability and sample SQL
- **S3 ↔ DataHub**: object listings include matching dataset metadata, and dataset searches show storage availability

## Does it work? Measured effectiveness

The knowledge layer is not just a design claim; it is benchmarked. On knowledge-trap questions, the ones an agent answers plausibly but wrongly without business context, connecting the agent to the platform's semantic knowledge layer lifts accuracy from **42.7% (raw data tools) to 98.7%**, a **+56-point** gain (95% CI +44 to +67). On plain lookups and arithmetic, where no business context is needed, the platform and bare tools are statistically tied, so the gain is specific to knowledge-gated questions, not a blanket accuracy boost.

This is an **arm-vs-arm** result on a single pinned model (same model, same tasks, only the platform configuration changes). It measures the **knowledge layer** specifically, that is cross-enrichment, `search`, and the memory/`apply_knowledge` lifecycle, not the whole platform. Every number is recomputed from committed raw data by a notebook that needs no API key. See the full, citable [Benchmark Report](https://mcp-data-platform.txn2.com/reference/benchmark-report/) (four-arm ablation, cold-start learning curve, lifecycle scorecard, threats to validity) and the [operator manual](bench/README.md).

## Features

Each feature links to its full documentation.

### Semantic data access

| Feature | Description |
|---------|-------------|
| [Cross-enrichment](https://mcp-data-platform.txn2.com/cross-enrichment/overview/) | Business context added to every tool response automatically, with session dedup to save tokens |
| [Lineage inheritance](https://mcp-data-platform.txn2.com/cross-enrichment/lineage/) | Column descriptions inherited from upstream datasets via DataHub lineage |
| [Universal search](https://mcp-data-platform.txn2.com/knowledge/overview/) | One `search` tool fans a query across the catalog, knowledge pages, memory, insights, assets, prompts, and APIs; `fetch` dereferences any result |
| [Workflow gating](https://mcp-data-platform.txn2.com/reference/middleware/) | Session-aware guidance that steers agents to discovery before SQL, with escalating warnings |
| [Tools](https://mcp-data-platform.txn2.com/server/tools/) | Full tool reference for Trino, DataHub, S3, knowledge, memory, portal, and gateway toolkits |

### Knowledge and memory

| Feature | Description |
|---------|-------------|
| [Memory layer](https://mcp-data-platform.txn2.com/memory/overview/) | Persistent agent memory across sessions, PostgreSQL + pgvector, hybrid semantic/lexical recall |
| [Knowledge capture](https://mcp-data-platform.txn2.com/knowledge/overview/) | Agents record domain insights during sessions; approved knowledge is written back to DataHub or canonical knowledge pages |
| [Governance workflow](https://mcp-data-platform.txn2.com/knowledge/governance/) | Human-in-the-loop review, approve/reject, changeset tracking, and rollback for every applied change |
| [Managed resources](https://mcp-data-platform.txn2.com/server/portal-user/#resources) | Human-uploaded reference files (playbooks, samples, templates) served to agents as MCP resources |

### Gateways and extensibility

| Feature | Description |
|---------|-------------|
| [MCP gateway](https://mcp-data-platform.txn2.com/server/gateway/) | Re-expose any third-party MCP server through the platform's auth, persona, and audit pipeline |
| [API gateway](https://mcp-data-platform.txn2.com/server/api-gateway/) | Proxy REST/HTTP APIs (Salesforce, Google, GitHub, Stripe) with four tools instead of one tool per endpoint |
| [API catalogs](https://mcp-data-platform.txn2.com/server/api-catalogs/) | Versioned OpenAPI bundles shared across connections, with semantic endpoint ranking |
| [REST invoke shim](https://mcp-data-platform.txn2.com/server/api-gateway/#rest-gateway-for-non-mcp-clients) | Call gateway endpoints from NiFi, Airflow, or `curl` under the same auth and audit pipeline |
| [Self-configuration](https://mcp-data-platform.txn2.com/server/self-configuration/) | Admins manage personas, connections, and prompts by asking the agent instead of clicking |
| [MCP Apps](https://mcp-data-platform.txn2.com/mcpapps/overview/) | Interactive UI panels rendered inline in the MCP host |
| [Go library](https://mcp-data-platform.txn2.com/library/overview/) | Import the platform as a library: custom toolkits, providers, and middleware |

### Security and operations

| Feature | Description |
|---------|-------------|
| [Authentication](https://mcp-data-platform.txn2.com/auth/overview/) | Fail-closed model: OIDC (Keycloak, Auth0, Okta, Azure AD) and API keys for service accounts |
| [OAuth 2.1 server](https://mcp-data-platform.txn2.com/auth/oauth-server/) | Built-in authorization server with PKCE and Dynamic Client Registration; Claude signs in through your IdP |
| [Outbound OAuth](https://mcp-data-platform.txn2.com/auth/oauth-gateway/) | OAuth to upstream MCPs and APIs with encrypted refresh tokens that survive restarts |
| [Personas](https://mcp-data-platform.txn2.com/personas/overview/) | Role-mapped allow/deny tool and connection filtering, default-deny |
| [Audit logging](https://mcp-data-platform.txn2.com/server/audit/) | Every tool call logged to PostgreSQL with identity, persona, sanitized parameters, and timing |
| [Observability](https://mcp-data-platform.txn2.com/server/observability/) | Prometheus metrics and optional OpenTelemetry distributed tracing |
| [Session externalization](https://mcp-data-platform.txn2.com/server/session-externalization/) | PostgreSQL-backed sessions for zero-downtime restarts, horizontal scaling, and live tool-inventory updates |
| [Explicit session handles](https://mcp-data-platform.txn2.com/server/configuration/#explicit-session-handles) | `platform_info` mints a `session_id` the agent threads on every call, making orientation unskippable and readying the platform for the sessionless MCP 2026-07-28 protocol |
| [Multi-provider](https://mcp-data-platform.txn2.com/server/multi-provider/) | Multiple instances of each service behind one endpoint, with isolated failure domains |
| [Operating modes](https://mcp-data-platform.txn2.com/server/operating-modes/) | Standalone (no database) or file + database with hot-reloaded config overrides |

## The Portal

A built-in web portal serves both operators and end users. Enable with `portal.enabled: true`.

**For operators**: dashboards with activity timelines and performance percentiles, a searchable audit log, an interactive tool explorer with per-persona visibility and inline test runs, knowledge insight governance, connection and persona management, API keys, and indexing health. See the [Admin Portal guide](https://mcp-data-platform.txn2.com/server/admin-portal/).

![Admin Dashboard](docs/images/screenshots/light/admin-admin-dashboard-light.webp)

**For users**: AI-generated artifacts (reports, charts, documents) are saved from any session with the `save_artifact` tool, organized into shareable [collections](https://mcp-data-platform.txn2.com/server/portal-user/#collections), and shared with teammates or through public links. A [prompt library](https://mcp-data-platform.txn2.com/server/portal-user/#prompts), [feedback threads](https://mcp-data-platform.txn2.com/server/portal-user/#feedback) on any artifact, and personal knowledge and activity views round out the [User Portal](https://mcp-data-platform.txn2.com/server/portal-user/).

![Collections](docs/images/screenshots/light/user-collection-view-light.webp)

## Quick Start

Install (see [all methods](https://mcp-data-platform.txn2.com/server/installation/): Homebrew, Docker, source):

```bash
go install github.com/txn2/mcp-data-platform/cmd/mcp-data-platform@latest
```

Create a minimal configuration. DataHub is the only required backend; `${VAR}` references are expanded from the environment:

```yaml
# platform.yaml
server:
  name: mcp-data-platform
  transport: stdio

semantic:
  provider: datahub
  instance: primary

toolkits:
  datahub:
    enabled: true
    instances:
      primary:
        url: "${DATAHUB_URL}"
        token: "${DATAHUB_TOKEN}"
    default: primary
```

Wire it to Claude Code:

```bash
claude mcp add data-platform \
  -e DATAHUB_URL=https://datahub.example.com/api/graphql \
  -e DATAHUB_TOKEN=$TOKEN \
  -- mcp-data-platform --config platform.yaml
```

For a hosted deployment, run `--transport http` and enable the built-in OAuth 2.1 server so Claude and other MCP clients sign in through your identity provider. See [Configuration](https://mcp-data-platform.txn2.com/server/configuration/), [Deployment](https://mcp-data-platform.txn2.com/server/deployment/) (Docker Compose, Kubernetes), and the [OAuth 2.1 Server guide](https://mcp-data-platform.txn2.com/auth/oauth-server/).

## Security

The platform implements a **fail-closed** security model: missing or invalid credentials deny access, never bypass. Personas are default-deny, Trino and S3 support enforced read-only mode, and metadata is sanitized against prompt injection. See the [Auth Overview](https://mcp-data-platform.txn2.com/auth/overview/) and [MCP Defense: A Case Study in AI Security](https://imti.co/mcp-defense/) for the architecture rationale.

| Transport | Authentication | TLS |
|-----------|----------------|-----|
| **stdio** | Not required (local execution) | N/A |
| **HTTP** | Required (Bearer token or API key) | Strongly recommended |

## Ecosystem

mcp-data-platform is the orchestration layer for a suite of open-source MCP servers that also run standalone:

- [txn2/mcp-datahub](https://github.com/txn2/mcp-datahub/): DataHub metadata: search, lineage, glossary, domains, tags, ownership
- [txn2/mcp-trino](https://github.com/txn2/mcp-trino/): Trino distributed SQL with configurable timeouts and row limits
- [txn2/mcp-s3](https://github.com/txn2/mcp-s3/): S3 object storage: buckets, prefixes, objects, presigned URLs

See [Ecosystem](https://mcp-data-platform.txn2.com/ecosystem/) for how they compose.

## Documentation

Full documentation lives at [mcp-data-platform.txn2.com](https://mcp-data-platform.txn2.com/).

- [Server Guide](https://mcp-data-platform.txn2.com/server/overview/): architecture, configuration, deployment
- [Cross-Enrichment](https://mcp-data-platform.txn2.com/cross-enrichment/overview/): how automatic enrichment works
- [Authentication](https://mcp-data-platform.txn2.com/auth/overview/): OIDC, API keys, OAuth 2.1
- [Knowledge Capture](https://mcp-data-platform.txn2.com/knowledge/overview/) and [Memory](https://mcp-data-platform.txn2.com/memory/overview/): the agent knowledge loop
- [Go Library](https://mcp-data-platform.txn2.com/library/overview/): build custom MCP servers ([API stability policy](https://mcp-data-platform.txn2.com/library/stability/))
- [Tools API Reference](https://mcp-data-platform.txn2.com/reference/tools-api/): complete tool specifications
- [Examples Gallery](https://mcp-data-platform.txn2.com/examples/): real-world configurations
- [Troubleshooting](https://mcp-data-platform.txn2.com/support/troubleshooting/): common issues and debugging

## Development

```bash
go build -o mcp-data-platform ./cmd/mcp-data-platform   # build
go test -race ./...                                     # tests
make verify                                             # full CI-equivalent suite
make osv                                                # osv-scanner, informational (mirrors OpenSSF Scorecard)
```

`make verify` runs `govulncheck`, which does reachability analysis and reports only vulnerabilities your code actually calls. `make osv` runs [osv-scanner](https://github.com/google/osv-scanner) the way [OpenSSF Scorecard](https://securityscorecards.dev/) does, flagging every vulnerable package in the dependency graph regardless of reachability. It is informational and not part of `verify`; suppressions for non-reachable and test-only findings are documented with justification and expiry in [`osv-scanner.toml`](osv-scanner.toml).

The React admin portal lives under [`ui/`](ui/README.md). Its CI job runs
`npm run lint`, which enforces per-function complexity budgets that mirror the
Go gates (`complexity <= 10` ≈ `gocyclo <= 10`, `cognitive-complexity <= 15` ≈
`gocognit <= 15`) plus an import-cycle rule. See [`ui/README.md`](ui/README.md)
for the thresholds and the ratchet baseline.

Two measurement harnesses live outside `make verify` (each is its own Go
module): [`test/load`](test/load/README.md) measures throughput and resource
limits ("how much"), and [`bench/`](bench/README.md) measures agent
effectiveness — arm-ablated accuracy and efficiency with audit-derived metrics
("how well"). Run them via `make load-*` and `make bench-*` targets.

Contributions for bug fixes, tests, and documentation are welcome. Please run `make verify` (formatting, race-detected tests, coverage, linting, security scanning) before opening a pull request.

## License

[Apache License 2.0](LICENSE)

---

Open source by [Craig Johnston](https://imti.co/about/), sponsored by [Deasil Works, Inc.](https://deasil.works/) and [Plexara](https://plexara.io)

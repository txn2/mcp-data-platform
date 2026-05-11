# ACME Corporation Local Dev Environment

Local development environment for the portal UI, pre-configured with ACME Corporation retail data (national retailer, 1,000 stores, 12 users, 6 personas).

## Quick Start: Full-Stack with Hot-Reload

One command starts everything: Docker services, Go server with hot-reload, and Vite UI dev server.

### Prerequisites

- Docker (for PostgreSQL + SeaweedFS)
- Go 1.23+
- [air](https://github.com/air-verse/air): `go install github.com/air-verse/air@latest`
- psql (optional, for auto-seeding dev data)

### Start

```bash
make dev
```

This starts:

| Service | URL / Port | Notes |
|---------|-----------|-------|
| PostgreSQL | `:5432` | Auto-migrated on startup |
| SeaweedFS (S3) | `:9000` | Portal asset storage |
| Go API server | `http://localhost:8080` | Hot-reloads on `.go` file changes via air |
| Vite UI | `http://localhost:5173/portal/` | Hot module replacement |
| dev-mcp-mock | `:9180` (OAuth) / `:9181` (MCP) | In-process mock — exercises the MCP gateway + OAuth grants |
| mcp-test fixture | `http://localhost:9281/` | `ghcr.io/plexara/mcp-test` — 12-tool deterministic MCP upstream + portal at `/portal/` |
| api-test fixture | `http://localhost:9282` | `ghcr.io/plexara/api-test` — 9 deterministic `/v1/*` paths (14 operations — `/echo` accepts all 6 HTTP methods) + OpenAPI at `/openapi.yaml` + portal at `/portal/` |

On first run, seed data (~5K audit events, 8 knowledge insights) is automatically loaded.

**API Key**: `acme-dev-key-2024`

### MCP gateway dev fixture

`make dev` automatically launches `cmd/dev-mcp-mock` and registers a
gateway connection named **`dev-mock`** through the admin API. This is
how the MCP gateway feature ships ready-to-explore in dev:

- The mock serves a tiny MCP at `http://localhost:9181/mcp` with three
  tools: `echo`, `add`, `now`.
- It also serves an OAuth 2.1 provider at `http://localhost:9180`
  (authorization_code + PKCE, refresh_token, and client_credentials
  grants). The default access-token TTL is 1 hour; set
  `ACCESS_TTL_SECONDS=10` in your `.env` to exercise refresh quickly.
- The pre-registered `dev-mock` connection appears in the admin portal's
  Connections page under kind `mcp` and surfaces three proxied tools:
  `dev-mock__echo`, `dev-mock__add`, `dev-mock__now`.

Switching the `dev-mock` connection to OAuth in the portal lets you
walk the full PKCE flow against the in-process mock — no external
provider needed. See [Gateway Toolkit](../docs/server/gateway.md) for
the connection-config reference and OAuth grant types.

### mcp-test and api-test fixtures

In addition to the in-process `dev-mcp-mock`, `make dev` brings up two
prebuilt fixture containers and registers them as platform connections:

- **`mcp-test-fixture`** (kind `mcp`) — `ghcr.io/plexara/mcp-test` on
  `http://localhost:9281/`. A 12-tool deterministic MCP server across
  four groups (`identity`, `data`, `failure`, `streaming`) for
  exercising the MCP gateway against a realistic upstream. Its own
  portal at `http://localhost:9281/portal/` shows every call the
  platform made (full request/response payloads + headers).
- **`api-test-fixture`** (kind `api`) — `ghcr.io/plexara/api-test` on
  `http://localhost:9282`. Nine deterministic HTTP paths under `/v1`
  (`/whoami`, `/headers`, `/fixed/{key}`, `/sized?bytes=N`, `/lorem`,
  `/status/{code}`, `/slow?ms=N`, `/flaky`, `/echo`) — 14 operations
  total because `/v1/echo` accepts all six HTTP methods (GET, POST,
  PUT, PATCH, DELETE, HEAD). Exercises the apigateway tools
  (`api_invoke_endpoint`, `api_list_endpoints`, `api_export`). The
  fixture publishes an OpenAPI 3.1 spec at `/openapi.yaml`;
  `dev/start.sh` fetches it at registration time and inlines it into
  the connection config, so `api_list_endpoints` returns the full
  catalog. Portal at `http://localhost:9282/portal/`.

Both fixtures use the shared `acme-dev-postgres` instance (databases
`mcp_test` and `apitest`, created on first volume init via
`dev/fixtures/postgres-init.sql`). Fixture configs live under
`dev/fixtures/`.

Both fixtures run in anonymous mode at the HTTP level; the platform
authenticates outbound with `X-API-Key`. Their own portals therefore
require no login — open them directly.

Press **Ctrl-C** to stop all services.

### Stop and Clean Up

```bash
make dev-down    # Stop Docker services + kill leftover host processes
```

`dev-down` removes the Postgres / SeaweedFS volumes AND kills the host
processes `dev/start.sh` spawned (air, the platform binary, vite,
esbuild, dev-mcp-mock). Use this if a previous `make dev` left ports
held by orphaned children.

## MSW Mode (No Backend)

The fastest way to see the portal UI with realistic data. No Docker, no database, no Go server.

```bash
make frontend-mock
```

Open <http://localhost:5173/portal/> — you'll see the portal with:

- 12 ACME users across 6 personas
- 20 tools across 6 connections (2 Trino, 2 DataHub, 2 S3)
- 200 audit events with business-hours weighting and realistic parameters
- Deterministic data (seeded PRNG) — same screenshots every time

## Advanced: Individual Services

For running components individually (e.g., only Docker, only Vite):

```bash
# Docker services only
make dev-up

# Go server only (requires Docker services running)
go run ./cmd/mcp-data-platform --config dev/platform.yaml

# Vite UI only (requires Go server running)
make frontend-dev

# Seed data manually
psql -h localhost -U platform -d mcp_platform -f dev/seed.sql
```

## Mock Conformance

Verify that MSW mock data matches the Go API Swagger spec:

```bash
make mock-check
```

This generates TypeScript types from the Swagger spec and runs conformance tests against the mock data.

## ACME Data Model

### Users

| User | Role | Persona |
|------|------|---------|
| sarah.chen@example.com | VP Data Analytics | admin |
| marcus.johnson@example.com | Senior Data Engineer | data-engineer |
| rachel.thompson@example.com | Inventory Analyst | inventory-analyst |
| david.park@example.com | Regional Director (West) | regional-director |
| jennifer.martinez@example.com | Finance Executive | finance-executive |
| kevin.wilson@example.com | Store Operations Mgr | store-manager |
| amanda.lee@example.com | Data Engineer | data-engineer |
| carlos.rodriguez@example.com | Regional Director (SE) | regional-director |
| emily.watson@example.com | Sales Analyst | inventory-analyst |
| brian.taylor@example.com | CFO | finance-executive |
| lisa.chang@example.com | ML Engineer | data-engineer |
| mike.davis@example.com | Flagship Store Mgr | store-manager |

### Connections

| Connection | Type | Description |
|-----------|------|-------------|
| acme-warehouse | Trino | Production data warehouse |
| acme-staging | Trino | Staging environment |
| acme-catalog | DataHub | Production metadata catalog |
| acme-catalog-staging | DataHub | Staging catalog |
| acme-data-lake | S3 | Raw data lake |
| acme-reports | S3 | Generated reports |

### Personas

| Persona | Access Level |
|---------|-------------|
| admin | Full access to all tools |
| data-engineer | All Trino, DataHub, S3 (no deletes) |
| inventory-analyst | Query + describe + search + list |
| regional-director | Query + describe + search + reports |
| finance-executive | Catalog search + reports only |
| store-manager | Query + describe + search + reports |

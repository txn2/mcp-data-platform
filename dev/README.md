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

On first run, seed data (~5K audit events, 8 knowledge insights) is automatically loaded.

**API Key**: `acme-dev-key-2024`

Press **Ctrl-C** to stop all services.

### Stop and Clean Up

```bash
make dev-down    # Stop Docker services and remove volumes
```

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

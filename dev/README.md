# ACME Corporation Local Dev Environment

Local development environment for the admin UI portal, pre-configured with ACME Corporation retail data (national retailer, 1,000 stores, 12 users, 6 personas).

## Quick Start: MSW Mode (No Backend)

The fastest way to see the admin UI with realistic data. No Docker, no database, no Go server.

```bash
cd admin-ui
npm install
VITE_MSW=true npm run dev
```

Open <http://localhost:5173/admin/> — you'll see the ACME dashboard with:

- 12 ACME users across 6 personas (admin, data-engineer, inventory-analyst, regional-director, finance-executive, store-manager)
- 20 tools across 6 connections (2 Trino, 2 DataHub, 2 S3)
- 200 audit events with business-hours weighting and realistic parameters
- Deterministic data (seeded PRNG) — same screenshots every time

## Full-Stack Mode (Real API)

For testing against real backend API endpoints with PostgreSQL.

### Prerequisites

- Docker (for PostgreSQL)
- Go 1.23+

### Start

```bash
# 1. Start PostgreSQL
make dev-up

# 2. Start the Go server (runs migrations automatically)
go run ./cmd/mcp-data-platform --config dev/platform.yaml

# 3. (Optional) Seed with historical data
psql -h localhost -U platform -d mcp_platform -f dev/seed.sql

# 4. Start the admin UI dev server
cd admin-ui && npm run dev
```

Open <http://localhost:5173/admin/>

**API Key**: `acme-dev-key-2024`

### Stop

```bash
make dev-down
```

## ACME Data Model

### Users

| User | Role | Persona |
|------|------|---------|
| sarah.chen@acme-corp.com | VP Data Analytics | admin |
| marcus.johnson@acme-corp.com | Senior Data Engineer | data-engineer |
| rachel.thompson@acme-corp.com | Inventory Analyst | inventory-analyst |
| david.park@acme-corp.com | Regional Director (West) | regional-director |
| jennifer.martinez@acme-corp.com | Finance Executive | finance-executive |
| kevin.wilson@acme-corp.com | Store Operations Mgr | store-manager |
| amanda.lee@acme-corp.com | Data Engineer | data-engineer |
| carlos.rodriguez@acme-corp.com | Regional Director (SE) | regional-director |
| emily.watson@acme-corp.com | Sales Analyst | inventory-analyst |
| brian.taylor@acme-corp.com | CFO | finance-executive |
| lisa.chang@acme-corp.com | ML Engineer | data-engineer |
| mike.davis@acme-corp.com | Flagship Store Mgr | store-manager |

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

# E2E Testing for MCP Data Platform

This directory contains end-to-end tests for validating the cross-injection functionality of the MCP Data Platform.

## Overview

The E2E tests validate four cross-injection paths:

| Path | Source Tool | Target Provider | Enrichment Key |
|------|-------------|-----------------|----------------|
| 1 | `trino_describe_table` | DataHub Semantic | `semantic_context` |
| 2 | `datahub_search` | Trino Query | `query_context` |
| 3 | `s3_list_objects` | DataHub Semantic | `semantic_context.matching_datasets` |
| 4 | `datahub_search` (S3) | S3 Storage | `storage_context` |

## Prerequisites

- Docker and Docker Compose
- Go 1.21+
- DataHub CLI (for full tests): `pip install acryl-datahub`
- At least 8GB RAM available (DataHub requires ~6GB)

## Quick Start

```bash
# Start platform services (PostgreSQL, Trino, SeaweedFS)
make e2e-up

# For full tests, start DataHub in another terminal
datahub docker quickstart

# Seed test data into DataHub
make e2e-seed

# Run E2E tests
make e2e-test

# Cleanup
make e2e-down
```

## Architecture

### Services

| Service | Port | Purpose |
|---------|------|---------|
| PostgreSQL | 5432 | OAuth/Audit storage |
| Trino | 8090 | Query execution |
| SeaweedFS | 9000 | S3-compatible storage |
| DataHub GMS | 8080 | Semantic metadata (via quickstart) |
| DataHub Frontend | 9002 | DataHub UI (via quickstart) |

### Test Data

The test environment includes:

**Trino Tables** (in `memory.e2e_test` schema):
- `test_orders` - Full metadata (owners, tags, domain)
- `legacy_users` - Deprecated table
- `products` - No DataHub metadata
- `customer_metrics` - Analytics table

**S3 Buckets** (SeaweedFS):
- `test-data-lake` - Contains sample data with DataHub metadata
- `test-analytics` - Empty bucket without metadata

**DataHub Entities**:
- Datasets for all test tables
- Tags: `e2e-test`, `ecommerce`, `pii`
- Domains: `ecommerce`, `data-platform`, `analytics`
- Users: `alice`, `bob`, `analytics-team`

## Directory Structure

```
test/e2e/
├── README.md                    # This file
├── cross_injection_test.go      # Main E2E test file
├── helpers/                     # Test utilities
│   ├── config.go                # E2E configuration
│   ├── wait.go                  # Service readiness checks
│   ├── platform.go              # Platform test setup
│   └── assertions.go            # Custom assertions
├── init/                        # Service initialization
│   ├── postgres/
│   │   └── 01_init.sql          # Database schema
│   ├── seaweedfs/
│   │   └── s3.json              # S3 credentials config
│   └── trino/
│       ├── catalog/
│       │   └── memory.properties
│       ├── etc/
│       │   └── config.properties
│       └── setup.sql            # Test table creation
└── testdata/                    # Test data
    ├── datahub/
    │   ├── datasets.json        # Dataset entities
    │   ├── owners.json          # User entities
    │   ├── tags.json            # Tag entities
    │   └── domains.json         # Domain entities
    └── s3/
        └── sample.parquet       # Sample S3 object
```

## Configuration

Tests use environment variables for configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_TRINO_HOST` | `localhost` | Trino host |
| `E2E_TRINO_PORT` | `8090` | Trino port |
| `E2E_DATAHUB_URL` | `http://localhost:8080` | DataHub GMS URL |
| `E2E_DATAHUB_TOKEN` | (empty) | DataHub auth token |
| `E2E_POSTGRES_DSN` | `postgres://...` | PostgreSQL connection |
| `E2E_S3_ENDPOINT` | `localhost:9000` | S3 endpoint (SeaweedFS) |
| `E2E_S3_ACCESS_KEY` | `admin` | S3 access key |
| `E2E_S3_SECRET_KEY` | `admin_secret` | S3 secret key |
| `E2E_TIMEOUT` | `30s` | Test timeout |

## Running Tests

### Full E2E Tests (with DataHub)

```bash
# Terminal 1: Start DataHub
datahub docker quickstart

# Terminal 2: Run tests
make e2e-up
make e2e-seed
make e2e-test
make e2e-down
```

### Partial Tests (without DataHub)

Tests that require DataHub will be skipped automatically:

```bash
make e2e-up
make e2e-test  # DataHub tests will be skipped
make e2e-down
```

### Running Specific Tests

```bash
# Run specific test
go test -v -tags=integration ./test/e2e/... -run TestTrinoToDataHubEnrichment

# Run with verbose output
go test -v -tags=integration ./test/e2e/... -count=1
```

## Troubleshooting

### Services not starting

Check Docker logs:
```bash
make e2e-logs
```

### DataHub not ready

DataHub takes 2-3 minutes to fully start. Check status:
```bash
curl http://localhost:8080/health
```

### Test data not seeded

Manually seed data:
```bash
datahub put --file test/e2e/testdata/datahub/datasets.json
```

### Memory issues

DataHub requires significant memory. Ensure Docker has at least 8GB allocated.

## CI Integration

The E2E tests run in GitHub Actions via `.github/workflows/e2e.yml`. Due to DataHub's resource requirements, CI runs may use a separate workflow or larger runners.

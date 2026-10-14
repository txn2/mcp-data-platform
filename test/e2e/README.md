# E2E Testing for MCP Data Platform

This directory contains end-to-end tests that run the platform against real
services (PostgreSQL, Trino, SeaweedFS) and check the cross-enrichment
behavior and the knowledge lifecycle through the real assembled system.

## Prerequisites

- Docker and Docker Compose
- Go (the version in `go.mod`)

## Quick Start

```bash
# Start platform services (PostgreSQL, Trino, SeaweedFS)
make e2e-up

# Run E2E tests
make e2e-test

# Cleanup
make e2e-down
```

## Architecture

### Services

| Service | Port | Purpose |
|---------|------|---------|
| PostgreSQL | 5432 | OAuth, audit, and the platform catalog |
| Trino | 8090 | Query execution |
| SeaweedFS | 9000 | S3-compatible storage |

The semantic layer is the platform's own catalog, stored in the same
PostgreSQL, so the stack above is the whole of what the suites need.

### Test Data

**Trino Tables** (in `memory.e2e_test` schema, created by
`init/trino/setup.sql`):
- `test_orders`
- `legacy_users`
- `products`
- `customer_metrics`

**S3 Buckets** (SeaweedFS):
- `test-data-lake` - Contains sample data
- `test-analytics` - Empty bucket

## Directory Structure

```
test/e2e/
├── README.md                    # This file
├── *_test.go                    # The integration-tagged suites
├── helpers/                     # Test utilities (configuration, readiness, platform setup, assertions)
├── init/                        # Service initialization
│   ├── postgres/                # Database schema
│   ├── seaweedfs/               # S3 credentials config
│   └── trino/                   # Catalog, server config and test table creation
└── testdata/                    # Test data
```

## Configuration

Tests use environment variables for configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_TRINO_HOST` | `localhost` | Trino host |
| `E2E_TRINO_PORT` | `8090` | Trino port |
| `E2E_POSTGRES_DSN` | `postgres://...` | PostgreSQL connection |
| `E2E_S3_ENDPOINT` | `localhost:9000` | S3 endpoint (SeaweedFS) |
| `E2E_S3_ACCESS_KEY` | `admin` | S3 access key |
| `E2E_S3_SECRET_KEY` | `admin_secret` | S3 secret key |
| `E2E_TIMEOUT` | `30s` | Test timeout |

## Running Tests

```bash
# Run one test
go test -v -tags=integration ./test/e2e/... -run <TestName>

# Run every suite with verbose output
go test -v -tags=integration ./test/e2e/... -count=1
```

## Troubleshooting

### Services not starting

Check Docker logs:
```bash
make e2e-logs
```

## CI Integration

The E2E suites run nightly in GitHub Actions via
`.github/workflows/e2e-nightly.yml`, against the same compose stack.

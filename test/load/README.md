# Load-test harness

A Go load generator for `mcp-data-platform` (issue #921). It drives named
workloads against a running platform over the MCP streamable-HTTP protocol and
the REST surfaces, scrapes the platform's own Prometheus metrics before, during,
and after each run, optionally captures pprof profiles, and writes a
self-contained JSON report plus a human summary.

It is written in Go against the official MCP SDK — the same one the platform
ships — because the hot path is the MCP protocol (initialize handshake, session,
`tools/call`), which generic HTTP load tools (k6, vegeta) cannot exercise
realistically.

## Why a separate module

`test/load` is its own Go module (`go.mod` here) on purpose. The repository root
runs coverage, test, and lint gates over `./...`, and a nested module is never
matched by the root's `./...` — so the harness stays out of the root coverage
denominator with no build-tag gymnastics. It has its own `.golangci.yml` scoped
to a CLI test tool. Run its checks from this directory:

```bash
cd test/load
go build ./... && go vet ./... && go test ./...
golangci-lint run ./...
```

Or from the repo root: `make load-test`.

Like mutation testing, load testing is **deliberately not part of `make verify`**
— it stands up Docker services and a real server binary and runs for tens of
seconds to an hour per scenario. Do not add `load-*` to the `verify` target.

## Running

From the repository root:

```bash
make load-up                              # compose stack + release-built platform + loadgen
make load-run SCENARIO=mcp-tool-call      # run one scenario
make load-down                            # stop everything
```

`make load-up` also expects a DataHub quickstart for the full stack (semantic
enrichment live). It sets `LOG_LEVEL=info`, a release-style build (no race
detector), the metrics listener, and an opt-in pprof listener
(`PPROF_ADDR`, off in normal deployments).

Config toggles (set on `load-up`):

| Env | Effect |
| --- | --- |
| `OAUTH_RL_ENABLED=false` | disable the OAuth per-IP limiter (raw `oauth-token` path) |
| `AUDIT_DELIVERY=sync` | durable audit delivery (backpressure instead of drops) |
| `LOAD_KEY` | admin API key the harness authenticates with |

`make load-run` override variables: `CONCURRENCY`, `DURATION`, `WARMUP`, `RATE`.

## Scenarios

| Name | What it exercises |
| --- | --- |
| `mcp-tool-call` | authenticated MCP sessions issuing `search` then `trino_query` — the primary hot path |
| `mcp-session-churn` | initialize/teardown-heavy load on the session store |
| `oauth-token` | OAuth DCR (`/register`): the bcrypt-bound, rate-limited path (see note below) |
| `portal-read` | portal REST list/read endpoints plus the public viewer path |
| `audit-burst` | high-concurrency audited calls sized past the async audit queue |
| `soak` | fixed moderate rate for a long duration; asserts flat memory and goroutines |

**On `oauth-token`:** the platform's token endpoint is not headless-drivable for
a load test — it supports only `authorization_code`/`refresh_token`, and
`authorization_code` requires a browser round-trip through an upstream IdP. The
`/register` (dynamic client registration) endpoint runs the same
`bcrypt.GenerateFromPassword` cost and is governed by the same `oauth.rate_limit`
limiter, so it is the representative bcrypt-bound OAuth measurement.

## Output

Each run writes `report-<scenario>.json`: run config, per-operation throughput
and latency percentiles, the before/during/after metric scrapes and their
deltas, scenario pass/fail assertions, and captured pprof profile paths. A
`-release-build` flag records whether the target was a release build; numbers
from a non-release target are flagged not publishable.

Published reference numbers live in
[`docs/reference/tuning-and-scaling.md`](../../docs/reference/tuning-and-scaling.md)
under "Measured limits".

## Layout

```
test/load/
├── config/platform.load.yaml   platform config for load runs
├── loadgen/                    CLI entry point
└── internal/
    ├── target/                 endpoints + auth + HTTP client
    ├── mcpc/                   MCP session + tool-call helper
    ├── scrape/                 Prometheus /metrics scrape
    ├── profile/                pprof capture
    ├── stats/                  latency percentiles + throughput
    ├── report/                 JSON report + human summary
    ├── runner/                 concurrency/duration/rate driver
    ├── harness/                run pipeline + Scenario/Worker contract
    └── scenario/               the six named workloads
```

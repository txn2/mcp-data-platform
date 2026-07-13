# Tuning and Scaling

This page documents resource sizing, Go runtime tuning, and horizontal-scaling
characteristics for `mcp-data-platform`. Numbers are starting points for a
production deployment; measure your own workload with the built-in Prometheus
endpoint before locking limits.

## 1. Baseline measurements

Steady-state observations from a single-replica production install handling
roughly 1 request/second of API gateway traffic (NiFi-driven), default config,
`LOG_LEVEL=debug`, semantic enrichment enabled:

| Metric              | Value                                |
| ------------------- | ------------------------------------ |
| CPU (avg)           | ~125m (range 100m to 160m)           |
| CPU (peak observed) | 160m                                 |
| Memory (RSS)        | ~68 MiB, very stable                 |
| Pod uptime sampled  | ~2.5 hours, 10 samples 10s apart     |

The pod was running `LOG_LEVEL=debug`, which inflates CPU and allocations. A
production install should run at `info`. Memory is essentially flat; the Go
heap is bounded by short-lived per-request allocations plus a small set of
long-lived caches.

Section 1 is a *floor*: steady state at ~1 request/second. Section 2 below is
the companion *ceiling*, what a single replica sustains under saturation,
measured with the load harness.

## 2. Measured limits

The numbers below come from the load harness in
[`test/load`](https://github.com/txn2/mcp-data-platform/tree/main/test/load),
driven against a single replica. They are **actual measurements**, not
extrapolations. Reproduce them with the commands at the end of this section.

### Reference environment

A single-box, co-located setup. Read the numbers as an order-of-magnitude
ceiling for one replica on capable hardware, **not** as an SLA and **not** as a
production-topology benchmark.

| Component | Detail |
| --- | --- |
| Host | Apple M5 Pro, 18 cores, 48 GiB RAM, macOS 26.5 (arm64) |
| Platform binary | native host process, Go 1.26, release-style build (no race detector), `LOG_LEVEL=info`, `GOMAXPROCS=18` (host default), single replica |
| Backing services | PostgreSQL 16 (pgvector), Trino 453, SeaweedFS, DataHub GMS v1.5 - all co-located in Docker Desktop (8-CPU / ~12 GiB VM) on the same machine |
| Harness | `test/load/loadgen` over MCP streamable HTTP + REST; the platform's own `/metrics` scraped before/during/after each run; pprof captured per run |

Two properties of this environment shape the numbers and must be kept in mind:

- **No CPU cgroup limit.** The binary saw all 18 host cores. A Kubernetes pod
  with `limits.cpu` set (and `GOMAXPROCS` matched to it, per section 4) scales
  CPU-bound results down roughly in proportion to the core count.
- **Co-located datastores.** Postgres, Trino, and DataHub share the machine, so
  their latency is best-case (loopback, warm caches). A production deployment
  with the database and query engine across the network will see higher
  tail latency on the data-dependent operations.

### Per-scenario results

Each scenario is one named workload; error rate is the share of calls that
returned a transport or tool-level error. Throughput is per operation over the
measured window (a scenario may issue several operations per iteration).

**`mcp-tool-call`** - 16 concurrent MCP sessions, each issuing `search` then
`trino_query`, 30s. The platform's primary hot path: auth, authz, search-first
gate, tool execution, cross-enrichment, audit.

| Operation | Sustained | p50 | p95 | p99 | Error rate |
| --- | --- | --- | --- | --- | --- |
| `search` | 66/s | 109 ms | 426 ms | 533 ms | 0% |
| `trino_query` | 66/s | 56 ms | 219 ms | 303 ms | 0% |

~134 tool calls/second total. Note the audit drop counter moved +2196 over this
run: even at a moderate tool-call rate the async audit writer sheds events (see
`audit-burst`).

**`mcp-session-churn`** - 16 workers connecting a fresh MCP session (full
`initialize` handshake), calling `platform_info`, and tearing the session down,
30s. Pressures the session-store create/destroy path.

| Operation | Sustained | p50 | p95 | p99 | Error rate |
| --- | --- | --- | --- | --- | --- |
| `session_connect` | 602/s | 0.3 ms | 0.7 ms | 1.7 ms | 0% |
| `platform_info` | 602/s | 4.9 ms | 111 ms | 137 ms | 0% |

~600 session lifecycles/second. Session creation itself is cheap; the tail on
`platform_info` reflects contention, not session cost.

**`portal-read`** - 16 workers rotating across the authenticated portal REST
read endpoints plus the unauthenticated public viewer, 30s.

| Operation | Sustained | p50 | p95 | p99 | Error rate |
| --- | --- | --- | --- | --- | --- |
| `portal_me` | 132/s | 0.2 ms | 0.5 ms | 0.7 ms | 0% |
| `portal_assets_list` | 132/s | 100 ms | 178 ms | 221 ms | 0% |
| `portal_collections_list` | 133/s | 3.9 ms | 28 ms | 120 ms | 0% |
| `portal_shared_with_me` | 133/s | 7.4 ms | 41 ms | 137 ms | 0% |
| `portal_public_view` | 132/s | 6.2 ms | 95 ms | 129 ms | 0% |

The per-IP portal viewer rate limiter was raised for this run so the public path
measures raw serving throughput rather than the limiter (all harness load
originates from one IP).

**`oauth-token`** - OAuth dynamic client registration (`POST /register`),
8 workers, 30s. This is the headless-drivable, bcrypt-bound path on the OAuth
server: registration runs `bcrypt.GenerateFromPassword` at the default cost and
is governed by the same `oauth.rate_limit` limiter as the token endpoint. (The
token endpoint itself is not headless-drivable for load: it supports only
`authorization_code`/`refresh_token`, and `authorization_code` requires a
browser round-trip through an upstream IdP. `/register` exercises the same
bcrypt cost and the same limiter.)

| Configuration | Result |
| --- | --- |
| Limiter off (raw) | **163 registrations/s**, p50 48 ms, p95 50 ms, 100% success |
| Limiter on (default: 10 rpm / burst 3) | 10 succeeded, 28,730 refused with 429 over 30s - the limiter engages immediately |

The raw path is **CPU-bound on bcrypt**: over the 30s window the process burned
253 CPU-seconds, saturating all 18 cores. On a cgroup-limited pod the ceiling
scales down with the core count. This is the single most CPU-intensive operation
in the platform; size OAuth-heavy deployments accordingly.

**`audit-burst`** - 64 workers issuing the audited `search` tool at high
concurrency, 20s, sized to push past the async audit queue (default cap 1024).
Run once per delivery mode:

| Delivery mode | Sustained | p50 | `audit_events_dropped_total` over run |
| --- | --- | --- | --- |
| `async` (default) | 357/s | 167 ms | **+6,483** (events shed under burst) |
| `sync` | 351/s | 168 ms | **+0** (no drops) |

This confirms the documented loss model: async delivery is best-effort and sheds
events when the single drain goroutine falls behind a burst; sync delivery drops
nothing. On this co-located setup the sync latency penalty is negligible because
Postgres services the write sub-millisecond over loopback - with a networked
database, sync delivery would trade that flat drop count for measurably higher
per-call latency.

**`soak`** - 8 workers holding a fixed 5 requests/second of `search`, 15 minutes
(the specified soak is 1 hour; this run was 15 minutes - pass `DURATION=1h` to
reproduce the full duration). 4,500 calls, 0% errors, p50 63 ms / p95 68 ms /
p99 74 ms, sampled every 5 seconds (181 scrapes).

| Metric | Start | End | Change | Threshold |
| --- | --- | --- | --- | --- |
| `go_goroutines` | 40 | 49 | +9 (+22%) | +50% |
| `process_resident_memory_bytes` | 78.2 MiB | 83.3 MiB | +5.1 MiB (+6.5%) | +25% |

Both stayed well within tolerance. The small goroutine and RSS rise is pool and
cache warm-up that settles, not unbounded growth - RSS is essentially flat and
goroutine count plateaus. The scenario asserts these bounds and passes.

### What saturates first

- **The async audit writer is the first thing to give.** It is a single drain
  goroutine behind a bounded (1024) channel; under a sustained tool-call burst it
  falls behind and `audit_events_dropped_total` climbs while tool latency stays
  flat - the drop is deliberate, non-blocking, best-effort. It shows up even in
  `mcp-tool-call` at ~134 calls/s. If every audit event must be durable, set
  `audit.delivery: sync` and budget for the per-call write latency; otherwise the
  drop counter is the signal to watch.
- **CPU saturates on bcrypt, not on the data path.** The tool-call path is
  I/O-bound (Trino, DataHub GraphQL, Postgres) and cheap on CPU. The OAuth
  registration/token path is bcrypt-bound and will pin every available core
  first. A deployment that issues or refreshes many tokens needs CPU headroom
  the analytics path does not.
- **Memory and goroutines stay flat.** Across every scenario RSS and goroutine
  count settle after warmup; the soak confirms no drift over time. The Go heap
  is bounded by short-lived per-request allocations plus a small set of
  long-lived caches, matching the section 1 observation.

### Capacity planning

The headline for sizing: for tool-call traffic the platform is **I/O-bound, not
CPU-bound**. A tool call costs only a couple of milliseconds of platform CPU;
its latency is mostly waiting on Trino, DataHub, and Postgres. Dividing each
run's `process_cpu_seconds_total` delta by the calls it served gives a stable
unit cost:

| Workload | CPU per call |
| --- | --- |
| `search` + `trino_query` (full enrichment) | ~3.0 ms (the heaviest normal path) |
| `search` only | ~2.6 ms |
| portal REST read | ~1.3 ms |
| session connect + `platform_info` | ~1.2 ms |
| OAuth registration (bcrypt) | ~52 ms (~20x everything else) |

**Per-core rule of thumb.** One core is 1000 ms of CPU per second, so at ~3 ms
for the heaviest call: a CPU ceiling near **330 tool-calls/s per core**, or about
**200/s per core** planned at 60% utilization for burst headroom. OAuth is the
exception at roughly **20 registrations/s per core** (bcrypt is single-threaded
per hash, so about one core per concurrent hash). This per-core figure is
extrapolated from the measured unit cost, not a measured saturation point: the
`mcp-tool-call` run used ~0.4 cores at 132 calls/s with ample headroom, because
something downstream binds before platform CPU does (see "what saturates first").

**Translating to users.** A typical user is an agent session (Claude, Cursor).
Agents are bursty: a few calls per turn, then seconds of model reasoning and
human read time. A defensible central estimate is about 4 tool-calls/minute
(0.067/s) per active session. Users supported is capacity divided by per-user
rate. For **two 8-core replicas** (16 cores, CPU-bound capacity around 3,000
tool-calls/s at 200/s/core):

| Active-user cadence | Concurrently-active users (CPU ceiling) |
| --- | --- |
| Heavy agent (1 call / 3s) | ~9,000 |
| Typical agent (1 call / 15s) | ~45,000 |
| Light / bursty (1 call / 30s) | ~90,000 |

Read those as an upper bound that proves a point, not a target: on CPU alone a
small two-replica deployment carries tens of thousands of concurrently-active
agent users. For any realistic user base the platform tier is not the
constraint. Size for burst headroom and HA, then plan real capacity around the
downstream query engines and the audit pipeline (see "what saturates first").

**Replica shape: fewer-larger vs more-smaller.** The choice between, say, two
8-core replicas (16 cores total) and three 4-core replicas (12 cores total)
depends on what binds:

- **Raw CPU** favors two 8-core (16 cores beats 12). Relevant only for a
  CPU-bound path such as heavy OAuth bcrypt.
- **Durable audit throughput** favors three 4-core. The async audit writer is a
  single drain goroutine *per replica*, so three replicas give three drains
  versus two. Audit-write capacity scales with replica count, not cores.
- **Resilience** favors three 4-core. Losing one of three sheds 33% of capacity
  versus 50% for one of two, and rolling updates keep more of the fleet serving.
- **Fixed overhead** favors two 8-core. Each replica carries baseline memory,
  per-replica caches (a sticky session re-routed to another replica re-fetches
  enrichment), background workers, and its own DB pool (three replicas total 75
  connections at the default 25 each, versus 50, against a default Postgres
  `max_connections` of 100), and its own copy of the per-replica rate limiters.

For this platform's typical I/O-bound, audited workload, **three 4-core replicas
are usually the better default** for resilience and audit headroom, despite less
total CPU. Choose **two 8-core** when a CPU-bound path (OAuth at scale, very
heavy enrichment) needs the extra ~33% of cores. Either way, size Trino and
DataHub first: that is the real ceiling.

### Reproducing

```bash
# 1. Stand up the compose stack + a release-built platform binary (LOG_LEVEL=info,
#    no race detector). Add a DataHub quickstart separately for the full stack.
make load-up

# 2. Run a scenario (see build/loadgen -list for names). Reports and pprof
#    profiles land under build/load-reports/.
make load-run SCENARIO=mcp-tool-call
make load-run SCENARIO=mcp-session-churn
make load-run SCENARIO=portal-read
make load-run SCENARIO=audit-burst           # async (default)
make load-run SCENARIO=oauth-token           # limiter on (default)

# Variants toggled by environment on load-up:
make load-down && AUDIT_DELIVERY=sync make load-up
make load-run SCENARIO=audit-burst           # sync delivery
make load-down && OAUTH_RL_ENABLED=false make load-up
make load-run SCENARIO=oauth-token           # raw bcrypt path

# Long-running soak (default 15m; the specified soak is 1h - pass DURATION=1h):
make load-run SCENARIO=soak DURATION=15m

# 3. Tear everything down.
make load-down
```

Each run writes a self-contained JSON report (throughput, error rate, latency
percentiles, and the scraped platform metrics) plus CPU/heap/goroutine pprof
profiles. Load testing is deliberately not part of `make verify` - see the
`load-*` targets in the `Makefile`.

## 3. Resource requests and limits

The defaults shipped in `configs/` are intentionally conservative. For higher
traffic, scale them as follows. The "high-traffic" column targets ~10 sustained
requests/second with bursty peaks (e.g., scheduled ETL jobs against the API
gateway).

| Field            | Low (≤1 RPS) | Medium (1-5 RPS) | High (5-15 RPS) |
| ---------------- | ------------ | ---------------- | --------------- |
| `requests.cpu`   | 100m         | 250m             | 500m            |
| `limits.cpu`     | 500m         | 1500m            | 3000m           |
| `requests.memory`| 128Mi        | 256Mi            | 512Mi           |
| `limits.memory`  | 256Mi        | 512Mi            | 1Gi             |

Set `requests.cpu` close to observed steady-state to give the scheduler an
honest picture; set `limits.cpu` 3-5x higher than steady-state to absorb burst
without throttling. CPU throttling under burst load is the most common cause
of latency spikes in this service.

## 4. Go runtime environment

The binary is a static Go program; the Go runtime is **not cgroup-aware by
default**. Set these env vars on the container to match the runtime to the
cgroup.

### `GOMEMLIMIT` (required)

`GOMEMLIMIT` tells the Go GC the soft memory cap. Without it the GC defaults
to a heap-relative target (`GOGC=100`, double the live heap), which can push
allocations past the cgroup memory limit and trigger an OOM kill even though
the process could have GC'd more aggressively.

```yaml
env:
  - name: GOMEMLIMIT
    value: "450MiB"   # set to ~90% of the container memory limit
```

The 90% rule leaves headroom for off-heap allocations (cgo, network buffers,
stack), which `GOMEMLIMIT` does not bound. Pair with a Kubernetes downward
API reference if you want it to track the limit automatically:

```yaml
env:
  - name: GOMEMLIMIT
    valueFrom:
      resourceFieldRef:
        resource: limits.memory
        divisor: 1Mi
```

(Then multiply or use a percentage-based wrapper if you want headroom.)

### `GOMAXPROCS` (required)

`GOMAXPROCS` defaults to the number of host CPUs visible inside the
container, which in Kubernetes is the **node's CPU count**, not the cgroup's
quota. On a 64-core node with a 500m CPU limit, Go spawns 64 worker threads,
fights itself for the 0.5 CPU quota, and wastes cycles on context switches
and scheduler contention.

Two options:

1. **Static value** matching `limits.cpu` rounded up:

   ```yaml
   env:
     - name: GOMAXPROCS
       value: "2"   # for limits.cpu: 1500m, round up to 2
   ```

2. **`go.uber.org/automaxprocs`**: pull the package into `main.go`; it reads
   the cgroup CPU quota at startup and sets `GOMAXPROCS` accordingly. This is
   the recommended approach for containers where the limit may change between
   deployments.

### `GOGC` (optional)

The default `GOGC=100` is fine for typical workloads. Lower values (50, 75)
GC more aggressively, trading CPU for lower steady-state heap. Higher values
reduce GC CPU at the cost of more RSS. Tune only with measurements in hand;
do not lower `GOGC` to "save memory" without checking that `GOMEMLIMIT` is
already in place.

### Putting it together

```yaml
env:
  - name: GOMEMLIMIT
    value: "900MiB"     # limits.memory: 1Gi, ~88%
  - name: GOMAXPROCS
    value: "3"          # limits.cpu: 3000m
  - name: GOGC
    value: "100"        # default; document the intent
```

## 5. Horizontal scaling

The service is designed to run with multiple replicas behind a Kubernetes
Service. The following components are HA-safe:

- **OAuth 2.1 server**: clients, authorization codes, refresh tokens, and
  PKCE verifiers are persisted to PostgreSQL when `DATABASE_DSN` is set
  (`pkg/oauth/postgres/store.go`). The in-memory store is a dev-only
  fallback for `DATABASE_DSN`-less mode.
- **Audit log**: writes go straight to PostgreSQL
  (`pkg/audit/postgres/store.go`). At 1M tool calls/day that is roughly 12
  writes/second average, well within a single Postgres instance.
- **Embedding jobs**: the embed-jobs worker uses a PostgreSQL-backed queue
  with `pg_try_advisory_lock` for coordination
  (`pkg/platform/apigateway_embed_jobs.go`). Multiple replicas compete for
  work without duplicating jobs.
- **Connection OAuth refresh**: the upstream token refresher uses a
  PostgreSQL advisory lock so only one replica refreshes a given connection
  at a time (`pkg/connoauth/refresher.go`).
- **API gateway REST shim**: each REST request builds an ephemeral in-memory
  MCP session for the duration of the call
  (`pkg/gatewayhttp/handler.go:203`). There is no cross-request session
  state, so any replica can serve any request.
- **Outbound HTTP**: the API gateway toolkit maintains a per-connection
  `http.Transport` with `MaxIdleConns` and `IdleConnTimeout`
  (`pkg/toolkits/apigateway/toolkit.go:1046`). Connections to upstream APIs
  are pooled inside each replica.

### Per-replica state to be aware of

These caches are per-replica. They affect behavior, not correctness:

- **`SessionEnrichmentCache`**: deduplicates semantic enrichment payloads
  within a long-running MCP session (`pkg/middleware/session_cache.go`).
  REST-shim calls (the high-volume HTTP-client path) get a fresh session per
  request, so this cache is effectively bypassed. For sticky MCP sessions
  (Claude Desktop, Cursor), routing the same session to a different replica
  costs a few extra enrichment payloads, not correctness.
- **Portal rate limiter**: token bucket keyed by IP in `pkg/portal/`. This
  guards the public viewer page, not the API gateway. With N replicas a
  single client sees roughly N times its configured budget. If you depend on
  the portal rate limit for SLO enforcement, terminate at an ingress-level
  rate limiter instead.
- **Tool-call rate limiter**: token bucket keyed by authenticated user
  (`internal/platform/toolratelimit/toolratelimit.go`), configured under the top-level
  `rate_limit:` block. It is a safety net against a runaway agent loop or a
  compromised account. The sizing guidance above (audit writes/second, the
  per-replica DB pool, upstream capacity) is exactly what it protects. With N
  replicas a single user's effective ceiling is N times the configured
  `requests_per_minute`/`burst`; the default (240 rpm, burst 60) is deliberately
  generous so ordinary use never touches it, so the per-replica multiplication is
  not a concern for the backstop's purpose. Each refusal increments
  `mcp_rate_limited_total`. See [Tool-Call Rate Limiting](../server/configuration.md#tool-call-rate-limiting).

### Replica count and PostgreSQL connections

The DB pool defaults to `MaxOpenConns = 25` per replica
(`pkg/platform/config.go:63`). Three replicas total 75 connections. Default
Postgres `max_connections` is 100; account for the migrate job, admin REST
handlers, and any other tenants of the same database.

Recommended:

- 1 replica: leave defaults.
- 2-3 replicas: drop `database.max_open_conns` to 15 in `platform.yaml`
  (3 × 15 = 45, comfortable margin).
- Run a separate read-replica or pgbouncer if you scale beyond 3.

### Liveness and readiness

The deployment exposes `/healthz` and `/readyz` on port 8080:

- `readinessProbe`: 5s initial, 10s period, 3s timeout
- `livenessProbe`: 10s initial, 30s period, 3s timeout

On rolling updates, set `strategy.rollingUpdate.maxSurge: 1` and
`maxUnavailable: 0` so at least one replica is always serving.

### Graceful shutdown

On SIGTERM the platform runs a four-stage shutdown chain. Each stage has
its own timeout; the sum must fit inside the pod's
`terminationGracePeriodSeconds` or Kubernetes will SIGKILL whatever is
still running.

| Stage | What happens | Default | Configurable via |
|---|---|---|---|
| 1. Pre-shutdown delay | `/readyz` flips to `draining` (503). Sleep so the LoadBalancer/Ingress can deregister this pod and stop sending new requests. | 2s | `server.shutdown.pre_shutdown_delay` |
| 2. HTTP drain | `http.Server.Shutdown` waits for in-flight handlers (MCP tool calls, REST shim invokes) to return. Handlers that don't finish by the deadline have their request context canceled and are abandoned. | 25s | `server.shutdown.grace_period` |
| 3. Lifecycle stop | `platform.Stop` fires every `OnStop` callback: embed-jobs worker, reaper, reconciler, LISTEN/NOTIFY listener. Bounded so a hung worker (slow Postgres, stuck embedding call) cannot stall shutdown. Abandoned jobs are safe: their PostgreSQL leases expire and another replica reclaims them on the next poll tick. | 10s | hard-coded `lifecycleStopTimeout` in `cmd/mcp-data-platform/main.go` |
| 4. Platform close | Audit flush, OAuth refresher stop, session cache flush, DB pool close, metrics provider shutdown. | a few seconds | n/a |

The full budget for the defaults is `2 + 25 + 10 + ~3 ≈ 40s`. Set
`terminationGracePeriodSeconds` accordingly. The default 30s in the
example manifest is too tight for the default platform configuration;
60s leaves comfortable headroom.

For deployments with long-running tool calls (large Trino queries, slow
upstream API gateway calls), raise `server.shutdown.grace_period` and
`terminationGracePeriodSeconds` together. Reasonable starting point:

```yaml
# platform.yaml
server:
  shutdown:
    pre_shutdown_delay: 3s
    grace_period: 45s
```

```yaml
# Deployment manifest
spec:
  template:
    spec:
      terminationGracePeriodSeconds: 70   # 3 + 45 + 10 + ~5 buffer
```

In-flight tool calls that exceed the grace period are abandoned, not
rolled back. If a tool has a side effect (write to DataHub, S3 PUT,
external API mutation), the side effect may or may not have completed
when the handler is canceled. For idempotent operations this is fine;
for non-idempotent ones, design the upstream caller to retry safely.

### Pod anti-affinity

For 2+ replicas, prefer scheduling on different nodes:

```yaml
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchExpressions:
              - key: app
                operator: In
                values:
                  - mcp-data-platform
          topologyKey: kubernetes.io/hostname
```

## 6. Observability

Prometheus metrics are exposed on `:9090` by default
(`OTEL_METRICS_ADDR` overrides; `OTEL_METRICS_ENABLED=false` disables
the listener). Metrics include per-tool
invocation counts and durations, API gateway upstream latency, and the Go
runtime collectors. With metrics on, the recommended HPA driver is
`apigateway_invoke_duration_seconds_count` rate-of-change (request rate) or
`process_cpu_seconds_total` (CPU saturation), not raw CPU utilization.

`LOG_LEVEL=info` is the production default. `debug` adds substantial
allocations on the hot path; only enable it temporarily.

## 7. Autoscaling

A horizontal pod autoscaler driven by CPU utilization works correctly once
`GOMAXPROCS` is set:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: mcp-data-platform
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: mcp-data-platform
  minReplicas: 2
  maxReplicas: 5
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 60
```

For traffic-shaped scaling, use the Prometheus adapter and target the
API gateway request rate metric directly.

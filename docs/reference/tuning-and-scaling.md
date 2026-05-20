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

## 2. Resource requests and limits

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

## 3. Go runtime environment

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

## 4. Horizontal scaling

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

## 5. Observability

Prometheus metrics are exposed on `:9090` by default
(`OTEL_METRICS_ADDR` overrides; `OTEL_METRICS_ENABLED=false` disables
the listener). Metrics include per-tool
invocation counts and durations, API gateway upstream latency, and the Go
runtime collectors. With metrics on, the recommended HPA driver is
`apigateway_invoke_duration_seconds_count` rate-of-change (request rate) or
`process_cpu_seconds_total` (CPU saturation), not raw CPU utilization.

`LOG_LEVEL=info` is the production default. `debug` adds substantial
allocations on the hot path; only enable it temporarily.

## 6. Autoscaling

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

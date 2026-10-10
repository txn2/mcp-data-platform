# ClickStack backend for mcp-data-platform

A self-hosted observability backend with the platform pre-wired: traces,
metrics and logs in ClickHouse, the HyperDX UI over them, eight dashboards,
six saved searches and the alerts, applied by a seed. The platform knows
nothing about ClickStack; it emits OpenTelemetry (OTLP), and everything here
is configuration around that.

## Licensing and what this is not

- The bundle references the upstream image `clickhouse/clickstack-all-in-one`
  by digest and vendors nothing. ClickHouse and the OpenTelemetry Collector are
  Apache 2.0 and the HyperDX UI is MIT.
- **The all-in-one image runs MongoDB for the UI's state, and MongoDB is
  licensed under the SSPL.** It is part of the upstream image, not of this
  repository, and it is pulled only when you run this optional profile.
- **The all-in-one image is not a production layout.** Upstream documents it
  for evaluation, demos and proofs of concept: one container, no replication,
  no fault tolerance. For production use upstream's Helm chart, or managed
  ClickStack, as their deployment options page describes
  (https://clickhouse.com/docs/use-cases/observability/clickstack/deployment).
  The collector configurations, the platform settings and the seed here work
  unchanged against either.

## Files

| File | What it is |
|------|------------|
| `compose.yaml` | Evaluation profile: ClickStack, the edge collector, the seed. |
| `compose.substrate.yaml` | Adds the substrate receivers to the edge collector. |
| `platform.env` | The variables that point mcp-data-platform at the edge collector. |
| `.env.example` | The compose profile's settings; copy to `.env`. |
| `collector-edge.yaml` | The per-deployment collector: OTLP in, identity, privacy, tail sampling, export. |
| `collector-scrape.yaml` | Scrapes the platform's Prometheus listener instead, for a deployment that keeps metrics off OTLP, and renames the series to the names the OTLP push uses (no `_total`, no unit suffix), so the dashboards read either path. Never with OTLP metrics, or every series arrives twice. |
| `collector-substrate.yaml` | PostgreSQL (receiver plus SQL queries), the host filesystem, SeaweedFS and ClickHouse's own health. |
| `collector-node.yaml` | Per-node collector in Kubernetes: pod logs and volume capacity. |
| `collector-central.yaml` | Optional gateway in front of a shared ClickStack for a fleet. |
| `clickhouse-prometheus.xml` | Turns on ClickHouse's Prometheus endpoint (off in the image). |
| `k8s/`, `kustomization.yaml` | The same in Kubernetes: `kubectl apply -k`. |
| `seed/` | `seed.py` and the dashboards, saved searches and alerts it applies. |

## Evaluate with compose

```bash
cd deployments/observability/clickstack
cp .env.example .env            # set CLICKSTACK_INGESTION_KEY and HYPERDX_SEED_PASSWORD
docker compose up -d
```

Then start mcp-data-platform with the variables in `platform.env`, and open
http://localhost:8080, signing in as `HYPERDX_SEED_EMAIL`. The seed has
already created the dashboards (search "MCP Platform"), the saved searches
("MCP:") and, when `HYPERDX_ALERT_WEBHOOK_URL` is set, the alerts.

The platform's side, in short: every span leaves the platform
(`OTEL_TRACES_SAMPLER_ARG=1.0`) because the edge collector's tail sampling can
only keep what arrives; metrics and logs go over OTLP
(`OTEL_METRICS_EXPORTER=otlp`, `OTEL_LOGS_EXPORTER=otlp`); and
`MCP_PLATFORM_DEPLOYMENT_ID` names the deployment on every signal, which is
what the dashboards' Deployment filter reads.

Retention is set per signal: `CLICKSTACK_TRACES_TTL` (7 days), `_LOGS_TTL`
(14) and `_METRICS_TTL` (30), as Go durations. ClickStack's own default is 3
days. A changed value is applied to the existing tables when ClickStack next
starts.

### Substrate receivers

```bash
docker compose -f compose.yaml -f compose.substrate.yaml up -d
```

The PostgreSQL receivers read with a monitoring role, never the platform's own:

```sql
CREATE ROLE otel_monitor LOGIN PASSWORD '...';
GRANT pg_monitor TO otel_monitor;
GRANT CONNECT ON DATABASE mcp_platform TO otel_monitor;
```

SeaweedFS reports nothing until each component is started with
`-metricsPort` (and the S3 gateway with `-s3.metricsPort`); point
`SUBSTRATE_SEAWEEDFS_METRICS` at it. A managed PostgreSQL reports its free
storage through the provider's monitoring, not through these receivers.

The platform reports its own side of capacity itself: each platform table's
size and row estimate, each vector index, the transaction id age, the bytes and
objects it holds in each bucket by purpose, and the orphaned objects and
dangling references a reconcile finds (see `docs/server/observability.md`).

## Fleets

Run one edge collector per deployment, beside it, with that deployment's
`EDGE_DEPLOYMENT_ID`, exporting to one shared backend: ClickStack directly, or
`collector-central.yaml` in front of it for one authenticated entry point.
Every dashboard carries Deployment, Environment and Version filters; left
empty they show the whole fleet, and a single deployment needs nothing set.

Tail sampling stays at the edge, where every span of a deployment's traces
arrives. A central sampling tier scaled to several replicas needs the
load-balancing exporter in front of it, routing by trace id; the comment at the
top of `collector-central.yaml` has the configuration.

## Kubernetes

```bash
kubectl create namespace observability
kubectl -n observability create secret generic clickstack-secrets --from-literal=ingestion-key="$(openssl rand -hex 24)"
kubectl -n observability create secret generic otel-substrate --from-literal=pg-user=otel_monitor --from-literal=pg-password='...'
kubectl apply -k deployments/observability/clickstack
```

Edit `EDGE_DEPLOYMENT_ID` and the substrate hosts in `k8s/collector-edge.yaml`
first, and merge `k8s/platform-env-patch.yaml` into the platform's Deployment.
`k8s/clickstack.yaml` is the all-in-one image with two volumes, for evaluation
only (see above).

The seed Job needs a personal API key, which exists only once a user does:
sign in to HyperDX, copy the key from Team Settings > API Keys, then

```bash
kubectl -n observability create secret generic hyperdx-seed --from-literal=api-key='...' --from-literal=webhook-url='https://...'
kubectl -n observability delete job hyperdx-seed --ignore-not-found && kubectl apply -k deployments/observability/clickstack
```

## The seed

`seed/seed.py` applies everything under `seed/` through HyperDX's API
(`/api/v2`), by name: missing objects are created, changed ones replaced, and
an unchanged directory writes nothing on a second run. It needs `HYPERDX_API_KEY`,
or `--bootstrap` on a fresh evaluation install, which creates the first user
and reads its key through the sign-up and sign-in routes HyperDX's own
evaluation harness uses (not part of the documented API).

Alerts need a channel. The self-hosted API offers three webhook services:
`slack` (an incoming webhook), `generic` (any URL that accepts a JSON POST)
and `incidentio`. The seed creates one generic webhook from
`HYPERDX_ALERT_WEBHOOK_URL` and sends every alert to it; with none set it
creates the dashboards and saved searches and skips the alerts.

A tile or alert is a short query spec (a counter's rate, a ratio, a gauge, a
histogram quantile, a regression against a trailing baseline, or SQL), which
the seed renders into ClickHouse SQL over the OTel tables. `python3 seed.py
--render` prints every query with HyperDX's macros expanded, so a definition
can be run against ClickHouse directly.

The same alerts, for Prometheus, are in `../alert-rules.yaml`, apart from the
ones that read the substrate receivers.

# Observability deployment manifests

## Choose your backend

The platform emits OpenTelemetry and knows nothing about any backend: pick
the one that fits what you already run.

| Backend | Where | What you get |
|---------|-------|--------------|
| Prometheus | this directory | A scrape of the platform's metrics listener, starter recording rules and alert rules. Metrics only. |
| ClickStack | [`clickstack/`](clickstack/README.md) | Traces, metrics and logs in ClickHouse with the HyperDX UI, eight dashboards, saved searches and alerts applied by a seed, a per-deployment edge collector for fleets, and receivers for PostgreSQL, the volumes, SeaweedFS and ClickHouse itself. The bundled all-in-one image is for evaluation; see its README. |
| Anything else that speaks OTLP | `otel-collector.yaml` | Point `OTEL_EXPORTER_OTLP_ENDPOINT` at a collector and export where you like. |

The rest of this page is the Prometheus path.

## Prometheus

Plain Kubernetes manifests for scraping mcp-data-platform with Prometheus and
loading the starter recording and alert rules. No Helm chart, no Operator CRDs;
every file applies standalone with `kubectl apply -f`.

| File | What it is |
|------|------------|
| `pod-annotations.yaml` | Example Deployment patch enabling the metrics listener and the `prometheus.io/*` scrape annotations. |
| `recording-rules.yaml` | ConfigMap with starter recording rules (pre-computed p95s and error rates). |
| `alert-rules.yaml` | ConfigMap with alert rules: the starter group (5xx rate, latency regression, auth spike, DB pool saturation, target down) and the fleet group (tool error rates and latency regression, failing connection credentials, script queue health, silent background loops, dropped audit events, pool exhaustion, configuration warnings, bucket budgets, PostgreSQL volume and transaction id age, refused storage writes, rising orphans, version spread). |
| `alert-rules.test.yaml` | promtool unit tests for the alert rules; `make alert-rules-test` runs them. The `AuthFailureSpike` cases include a deployment without the OAuth server, where two of the alert's three series are absent. |
| `otel-collector.yaml` | Example OpenTelemetry Collector config for OTLP traces: OTLP receiver, tail sampling (keep the errors and >2s traces that arrive, down-sample the rest), OTLP export to Tempo. Pair it with `OTEL_TRACES_SAMPLER_ARG=1.0` on the platform: the head sampler decides before a span's outcome is known, so at its default about 90% of error traces never reach the collector. |

### 1. Make the pods scrapable

The platform exposes Prometheus metrics on a dedicated listener, separate from
the MCP/HTTP transport port. Enable it and annotate the pods:

```bash
# Merge the annotations + env into your Deployment (edit names/namespace first).
kubectl apply -f pod-annotations.yaml
```

This sets `OTEL_METRICS_ENABLED=true`, binds the listener to `:9090`
(`OTEL_METRICS_ADDR`, path `/metrics`), and adds:

```yaml
prometheus.io/scrape: "true"
prometheus.io/port: "9090"
prometheus.io/path: "/metrics"
```

If your Prometheus uses annotation-based pod discovery (the standard
`kubernetes-pods` scrape job), no further scrape config is needed.

### 2. Load the rules

The rules ship as ConfigMaps so they work without the Prometheus Operator.
Apply them, mount them into your Prometheus pod, and reference them from
`prometheus.yml`:

```bash
kubectl apply -f recording-rules.yaml
kubectl apply -f alert-rules.yaml
```

```yaml
# prometheus deployment (excerpt)
volumeMounts:
  - name: mcp-rules
    mountPath: /etc/prometheus/rules/mcp-data-platform
volumes:
  - name: mcp-rules
    projected:
      sources:
        - configMap: { name: mcp-data-platform-recording-rules }
        - configMap: { name: mcp-data-platform-alert-rules }
```

```yaml
# prometheus.yml
rule_files:
  - /etc/prometheus/rules/mcp-data-platform/*.yaml
```

Validate the rule files before shipping:

```bash
promtool check rules recording-rules.yaml alert-rules.yaml
```

(The ConfigMap wrapper is plain k8s; `promtool` checks the embedded
`groups:` document, so extract the `data` value or run it against the mounted
file.)

### 3. Confirm Prometheus is scraping

Query Prometheus for the target's health:

```promql
up{job="mcp-data-platform"}
```

A value of `1` means the scrape is healthy. Then confirm platform series exist:

```promql
sum(rate(mcp_tool_calls_total[5m]))
```

### Metrics reference

The exposed metric names, labels, and cardinality notes are documented in
[`docs/observability.md`](../../docs/observability.md).

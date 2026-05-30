# Observability deployment manifests

Plain Kubernetes manifests for scraping mcp-data-platform with Prometheus and
loading the starter recording and alert rules. No Helm chart, no Operator CRDs;
every file applies standalone with `kubectl apply -f`.

| File | What it is |
|------|------------|
| `pod-annotations.yaml` | Example Deployment patch enabling the metrics listener and the `prometheus.io/*` scrape annotations. |
| `recording-rules.yaml` | ConfigMap with starter recording rules (pre-computed p95s and error rates). |
| `alert-rules.yaml` | ConfigMap with starter alert rules (5xx rate, latency regression, auth spike, DB pool saturation, target down). |

## 1. Make the pods scrapable

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

## 2. Load the rules

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

## 3. Confirm Prometheus is scraping

Query Prometheus for the target's health:

```promql
up{job="mcp-data-platform"}
```

A value of `1` means the scrape is healthy. Then confirm platform series exist:

```promql
sum(rate(mcp_tool_calls_total[5m]))
```

## Metrics reference

The exposed metric names, labels, and cardinality notes are documented in
[`docs/observability.md`](../../docs/observability.md).

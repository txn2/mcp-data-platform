import type { PromVectorResponse, PromMatrixResponse } from "@/api/observability/types";

// Per-node health queries. Each is a raw per-process gauge, so Prometheus
// returns one series PER node (one scrape target = one pod), labeled with
// `instance` and, under Kubernetes service discovery, `pod`. They are NOT
// aggregated, so the Health view can show each node individually.
export const NODE_QUERIES = {
  uptime: "time() - process_start_time_seconds",
  cpu: "rate(process_cpu_seconds_total[1m])",
  rss: "process_resident_memory_bytes",
  heap: "go_memstats_heap_alloc_bytes",
  goroutines: "go_goroutines",
  inflight: "mcp_inflight_tool_calls",
} as const;

// Numeric per-node fields (everything on NodeHealth except id/label).
export type NodeMetricKey = "uptime" | "cpu" | "rss" | "heap" | "goroutines" | "inflight";

export interface NodeHealth {
  id: string;
  label: string;
  uptime?: number; // seconds
  cpu?: number; // cores (rate of cpu-seconds)
  rss?: number; // bytes (resident memory)
  heap?: number; // bytes (go heap in use)
  goroutines?: number;
  inflight?: number;
}

// nodeIdentity derives a node's id/display label from a series' labels:
// the Kubernetes pod name when present (the admin-facing default), else the
// scrape instance (host:port) for non-k8s deployments.
function nodeIdentity(metric: Record<string, string>): { id: string; label: string } {
  const name = metric["pod"] || metric["instance"] || "unknown";
  return { id: name, label: name };
}

// mergeNodeMetrics joins several per-node instant-query results (one query
// per metric) into a NodeHealth row per node, keyed by node identity.
// Missing metrics for a node stay undefined so the UI renders "-" rather
// than a misleading zero.
export function mergeNodeMetrics(
  parts: { key: NodeMetricKey; resp: PromVectorResponse | undefined }[],
): NodeHealth[] {
  const byNode = new Map<string, NodeHealth>();
  for (const { key, resp } of parts) {
    for (const r of resp?.data?.result ?? []) {
      const { id, label } = nodeIdentity(r.metric);
      let node = byNode.get(id);
      if (!node) {
        node = { id, label };
        byNode.set(id, node);
      }
      const v = Number(r.value[1]);
      if (Number.isFinite(v)) node[key] = v;
    }
  }
  return [...byNode.values()].sort((a, b) => a.label.localeCompare(b.label));
}

// Per-node RANGE queries for the trend charts. Each returns one matrix
// series per node over the requested window.
export const NODE_RANGE_QUERIES = {
  cpu: "rate(process_cpu_seconds_total[5m])",
  memory: "process_resident_memory_bytes",
} as const;

export interface TrendPoint {
  t: number; // unix seconds
  value: number;
}

// seriesByNode maps a per-node matrix response to each node's time series,
// keyed by the same node identity (pod, instance fallback) as the instant
// metrics, so a node's trend lines up with its row.
export function seriesByNode(resp: PromMatrixResponse | undefined): Map<string, TrendPoint[]> {
  const out = new Map<string, TrendPoint[]>();
  for (const s of resp?.data?.result ?? []) {
    const { id } = nodeIdentity(s.metric);
    out.set(
      id,
      s.values.map(([ts, v]) => ({ t: ts, value: Number(v) })),
    );
  }
  return out;
}

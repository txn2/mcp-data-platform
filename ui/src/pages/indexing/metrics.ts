import type {
  PromMatrixResponse,
  PromVectorResponse,
} from "@/api/observability/types";
import type { KindLatency } from "@/components/charts/IndexLatencyTrack";
import type { ThroughputPoint } from "@/components/charts/IndexThroughputTimeline";

// PromQL for the Indexing dashboard's Throughput and Embed latency panels
// (#1837), and the adapters that turn the answers into what the charts take.
//
// The series come from the index-job worker (pkg/observability/metrics_indexjobs.go):
//
//   indexjob_jobs_total{kind, trigger, outcome}       every claimed job, as it settles
//   indexjob_duration_seconds_bucket{kind, outcome}   claim to settling write
//
// Both are counted as they happen, so they cover the whole queue however large
// its backlog. The page of job rows the dashboard used before held only the
// newest 500, which during a backlog are all pending: no completion, so both
// panels went blank exactly when they mattered.
//
// It is a pure module -- no React, no fetch -- so the query strings are
// versioned and tested on their own, following pages/scripts/runMetrics.ts.

const jobsTotal = "indexjob_jobs_total";
const durationBucket = "indexjob_duration_seconds_bucket";
const durationCount = "indexjob_duration_seconds_count";
const succeeded = `outcome="succeeded"`;

/** WINDOW is the span both panels cover, and what their hints say. */
export const WINDOW = "24h";
export const WINDOW_SECONDS = 24 * 3600;

/** STEP is the throughput resolution: one point per 15 minutes. */
export const STEP_SECONDS = 900;
const step = "15m";

/**
 * completionsPerStep counts the jobs that completed in each step, across kinds
 * and replicas. increase over the step, not rate: the chart reads "jobs
 * completed", and a count per step is that number.
 */
export function completionsPerStep(): string {
  return `sum(increase(${jobsTotal}{${succeeded}}[${step}]))`;
}

/** latencyQuantile is one quantile of a successful pass's duration, per kind. */
export function latencyQuantile(q: number): string {
  return `histogram_quantile(${q}, sum by (kind, le) (rate(${durationBucket}{${succeeded}}[${WINDOW}])))`;
}

/** passesByKind counts the successful passes each quantile is drawn from. */
export function passesByKind(): string {
  return `sum by (kind) (increase(${durationCount}{${succeeded}}[${WINDOW}]))`;
}

/**
 * matrixToThroughput folds a range answer into chart points. The query sums to
 * one series, so there is at most one; a step Prometheus could not compute is
 * dropped rather than drawn as zero.
 */
export function matrixToThroughput(
  resp: PromMatrixResponse | undefined,
): ThroughputPoint[] {
  const series = resp?.data?.result?.[0];
  if (!series) return [];
  return series.values
    .map(([ts, val]) => ({ t: ts * 1000, count: Number(val) }))
    .filter((p) => Number.isFinite(p.count));
}

/**
 * vectorsToLatency joins the three quantiles and the pass count by kind. A kind
 * with no successful pass in the window is left out: its quantiles are NaN,
 * and a bar of nothing reads as a pass that took no time.
 */
export function vectorsToLatency(
  p50: PromVectorResponse | undefined,
  p95: PromVectorResponse | undefined,
  p99: PromVectorResponse | undefined,
  count: PromVectorResponse | undefined,
): KindLatency[] {
  const q50 = byKind(p50);
  const q95 = byKind(p95);
  const q99 = byKind(p99);
  const n = byKind(count);
  const rows: KindLatency[] = [];
  for (const [kind, c] of n) {
    const a = q50.get(kind);
    const b = q95.get(kind);
    const d = q99.get(kind);
    if (c <= 0 || a === undefined || b === undefined || d === undefined)
      continue;
    rows.push({
      kind,
      p50Ms: a * 1000,
      p95Ms: b * 1000,
      p99Ms: d * 1000,
      count: Math.round(c),
    });
  }
  return rows.sort((x, y) => x.kind.localeCompare(y.kind));
}

function byKind(resp: PromVectorResponse | undefined): Map<string, number> {
  const out = new Map<string, number>();
  for (const r of resp?.data?.result ?? []) {
    const kind = r.metric.kind;
    const v = Number(r.value[1]);
    if (kind && Number.isFinite(v)) out.set(kind, v);
  }
  return out;
}

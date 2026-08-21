import type { BreakdownEntry, TimeseriesBucket } from "@/api/admin/types";
import type { PromMatrixResponse, PromVectorResponse } from "@/api/observability/types";

// PromQL for the managed-script series (#1307), and the adapters that turn the
// answers into what the charts take.
//
// The series come from the run worker and the scheduler
// (pkg/observability/metrics.go):
//
//   script_runs_total{script, trigger, status}   runs that reached a terminal state
//   script_run_duration_seconds_bucket{script}   how long they took
//   script_runs_running                          runs executing right now
//   script_missed_fires_total{script}            fires the misfire policy stepped over
//
// It is a pure module — no React, no fetch — so the query strings are versioned
// and tested on their own, following pages/audit/promql.ts.

const runsTotal = "script_runs_total";
const durationBucket = "script_run_duration_seconds_bucket";
const running = "script_runs_running";
const missedTotal = "script_missed_fires_total";

// topN bounds a per-script breakdown. The label is bounded by the number of
// scripts a deployment has, but a chart with two hundred bars is unreadable
// long before it is expensive.
const topN = 10;

/** runRateByStatus plots runs per second, split by how they ended. */
export function runRateByStatus(step: string): string {
  return `sum by (status) (rate(${runsTotal}[${step}]))`;
}

/** runDurationP95 is the 95th-percentile run duration over the window. */
export function runDurationP95(window: string): string {
  return `histogram_quantile(0.95, sum by (le) (rate(${durationBucket}[${window}])))`;
}

/** runsInFlight is how many runs are executing right now, across replicas. */
export function runsInFlight(): string {
  return `sum(${running})`;
}

/** runTotalOverWindow counts every run that finished in the window. */
export function runTotalOverWindow(window: string): string {
  return `sum(increase(${runsTotal}[${window}]))`;
}

/** failuresOverWindow counts the ones that failed. */
export function failuresOverWindow(window: string): string {
  return `sum(increase(${runsTotal}{status="failed"}[${window}]))`;
}

/** runsByScript ranks the busiest scripts over the window. */
export function runsByScript(window: string): string {
  return `topk(${topN}, sum by (script) (increase(${runsTotal}[${window}])))`;
}

/**
 * missedFiresByScript ranks the scripts that are not keeping their cadence.
 * It is the one series that says a schedule is quietly falling behind: the run
 * table shows what ran, and a missed fire is precisely what did not.
 */
export function missedFiresByScript(window: string): string {
  return `topk(${topN}, sum by (script) (increase(${missedTotal}[${window}])))`;
}

/**
 * statusMatrixToTimeseries folds a status-split matrix into the buckets the
 * chart takes: succeeded on one line, everything else on the other.
 *
 * Everything else rather than "failed" alone, deliberately — a fire skipped
 * because the previous run was still going is not a success, and a chart that
 * counted only failures would draw a flat green line over a script that
 * has stopped producing anything.
 */
export function statusMatrixToTimeseries(resp: PromMatrixResponse | undefined): TimeseriesBucket[] {
  const byBucket = new Map<number, TimeseriesBucket>();
  for (const series of resp?.data?.result ?? []) {
    const ok = series.metric.status === "succeeded";
    for (const [ts, val] of series.values) {
      const n = Number(val);
      if (!Number.isFinite(n)) continue;
      const bucket = byBucket.get(ts) ?? emptyBucket(ts);
      bucket.count += n;
      if (ok) bucket.success_count += n;
      else bucket.error_count += n;
      byBucket.set(ts, bucket);
    }
  }
  return [...byBucket.entries()].sort(([a], [b]) => a - b).map(([, v]) => v);
}

function emptyBucket(ts: number): TimeseriesBucket {
  return {
    bucket: new Date(ts * 1000).toISOString(),
    count: 0,
    success_count: 0,
    error_count: 0,
    avg_duration_ms: 0,
  };
}

/** vectorToBreakdown maps an instant query to ranked rows keyed by one label. */
export function vectorToBreakdown(
  resp: PromVectorResponse | undefined,
  labelKey: string,
): BreakdownEntry[] {
  return (resp?.data?.result ?? [])
    .map((r) => ({
      dimension: r.metric[labelKey] ?? "(unnamed)",
      count: Math.round(Number(r.value[1])),
      success_rate: 0,
      avg_duration_ms: 0,
    }))
    .filter((row) => row.count > 0)
    .sort((a, b) => b.count - a.count);
}

/**
 * scalar reads the one number an instant query returns, or undefined when the
 * window holds nothing. Undefined and zero are different answers — "nothing has
 * run" is not "everything succeeded" — so the caller renders them differently.
 */
export function scalar(resp: PromVectorResponse | undefined): number | undefined {
  const raw = resp?.data?.result?.[0]?.value?.[1];
  if (raw === undefined) return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? n : undefined;
}

/**
 * stepFor is the rate window a preset deserves. A 5-minute rate over a week of
 * data draws noise; an hour-wide rate over one hour draws a single point.
 */
export function stepFor(preset: string): string {
  switch (preset) {
    case "1h":
      return "5m";
    case "6h":
      return "10m";
    case "7d":
      return "1h";
    default:
      return "30m";
  }
}

/** resolutionFor is the query_range step, in seconds, for a preset. */
export function resolutionFor(preset: string): number {
  switch (preset) {
    case "1h":
      return 60;
    case "6h":
      return 300;
    case "7d":
      return 3600;
    default:
      return 900;
  }
}

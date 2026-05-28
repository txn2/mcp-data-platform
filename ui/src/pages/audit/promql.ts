// PromQL query builders and result adapters for the API Gateway
// activity view. Kept as a pure module (no React, no fetch) so the
// query strings are versioned and unit-tested independently of the UI.
//
// Queries run over the inbound metrics emitted by the apigateway
// toolkit (#460):
//   apigateway_inbound_requests_total{connection, operation_id, method, status_class, identity}
//   apigateway_inbound_duration_seconds_bucket{connection, operation_id, method, status_class, le}
import type { BreakdownEntry, TimeseriesBucket } from "@/api/admin/types";
import type { PromVectorResponse, PromMatrixResponse } from "@/api/observability/types";

const reqTotal = "apigateway_inbound_requests_total";
const durBucket = "apigateway_inbound_duration_seconds_bucket";

// topN bounds breakdown queries so a high-cardinality dimension cannot
// return an unbounded series set to the chart.
const topN = 10;

// escapeLabel escapes a label value for safe interpolation into a
// PromQL matcher. Backslashes and double quotes are the only
// metacharacters inside a "..."-quoted PromQL label value.
function escapeLabel(v: string): string {
  return v.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

// matcher builds a {k="v",...} label matcher from exact-match pairs,
// skipping empty values.
function matcher(pairs: Record<string, string>): string {
  const parts = Object.entries(pairs)
    .filter(([, v]) => v !== "")
    .map(([k, v]) => `${k}="${escapeLabel(v)}"`);
  return parts.length > 0 ? `{${parts.join(",")}}` : "";
}

// topConnectionsByVolume ranks connections by total inbound requests
// over the window (e.g. "1h", "24h", "7d").
export function topConnectionsByVolume(window: string): string {
  return `topk(${topN}, sum by (connection) (increase(${reqTotal}[${window}])))`;
}

// connectionRequestTotal is the total request count for one connection
// over the window.
export function connectionRequestTotal(connection: string, window: string): string {
  return `sum(increase(${reqTotal}${matcher({ connection })}[${window}]))`;
}

// connectionErrorRate is the fraction of a connection's requests in the
// 4xx/5xx status classes over the window. The `> 0` on the denominator
// makes the expression empty (rather than +Inf/NaN) when the connection
// had no traffic in the window.
export function connectionErrorRate(connection: string, window: string): string {
  const errored = `sum(increase(${reqTotal}{connection="${escapeLabel(connection)}",status_class=~"4xx|5xx"}[${window}]))`;
  const total = `sum(increase(${reqTotal}${matcher({ connection })}[${window}]))`;
  return `${errored} / (${total} > 0)`;
}

// latencyQuantile builds a histogram_quantile over the connection's
// duration buckets. q is a fraction (0.5, 0.95, 0.99). This is the one
// place raw histogram buckets are queried (the issue allows raw
// histograms only for drilldowns needing bucket detail).
export function latencyQuantile(q: number, connection: string, window: string): string {
  return `histogram_quantile(${q}, sum by (le) (rate(${durBucket}${matcher({ connection })}[${window}])))`;
}

// topEndpoints ranks a connection's operations by request volume.
export function topEndpoints(connection: string, window: string): string {
  return `topk(${topN}, sum by (operation_id) (increase(${reqTotal}${matcher({ connection })}[${window}])))`;
}

// endpointByLabel ranks the values of `label` (status_class, method, or
// identity) for one connection+operation by request volume.
export function endpointByLabel(
  label: "status_class" | "method" | "identity",
  connection: string,
  operationID: string,
  window: string,
): string {
  return `topk(${topN}, sum by (${label}) (increase(${reqTotal}${matcher({ connection, operation_id: operationID })}[${window}])))`;
}

// requestRateRange builds a range query (for the timeseries chart) of
// request rate per second, optionally scoped to a connection and/or
// operation. The rate window is the chart's step so the line is smooth
// at the chosen resolution.
export function requestRateRange(
  filters: { connection?: string; operationID?: string },
  rateWindow: string,
): string {
  const m = matcher({
    connection: filters.connection ?? "",
    operation_id: filters.operationID ?? "",
  });
  return `sum(rate(${reqTotal}${m}[${rateWindow}]))`;
}

// --- result adapters ---

// promVectorToBreakdown maps an instant query result to the chart's
// BreakdownEntry[] keyed by one metric label. success_rate and
// avg_duration_ms are not derivable from a volume vector, so they are 0
// (the bar chart only renders count; the other fields satisfy the type).
export function promVectorToBreakdown(
  resp: PromVectorResponse | undefined,
  labelKey: string,
): BreakdownEntry[] {
  if (!resp?.data?.result) {
    return [];
  }
  return resp.data.result.map((r) => ({
    dimension: r.metric[labelKey] ?? "(none)",
    count: Math.round(Number(r.value[1])),
    success_rate: 0,
    avg_duration_ms: 0,
  }));
}

// firstScalar reads the single numeric value from an instant query that
// is expected to return one (unlabeled) series: an error rate, a latency
// quantile, or a total. Returns undefined when there is no result (e.g.
// no traffic in the window), which the UI renders as "-".
export function firstScalar(resp: PromVectorResponse | undefined): number | undefined {
  const v = resp?.data?.result?.[0]?.value?.[1];
  if (v === undefined) {
    return undefined;
  }
  const n = Number(v);
  return Number.isNaN(n) ? undefined : n;
}

// promMatrixToTimeseries maps the first series of a range query to the
// chart's TimeseriesBucket[] (count per bucket). The proxy queries a
// single aggregated series for the timeseries, so the first result is
// the line; success/error split is not available from a plain rate
// series and is left at 0.
export function promMatrixToTimeseries(resp: PromMatrixResponse | undefined): TimeseriesBucket[] {
  const series = resp?.data?.result?.[0];
  if (!series) {
    return [];
  }
  return series.values.map(([ts, val]) => ({
    bucket: new Date(ts * 1000).toISOString(),
    count: Number(val),
    success_count: 0,
    error_count: 0,
    avg_duration_ms: 0,
  }));
}

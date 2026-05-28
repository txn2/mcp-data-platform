// Mock Prometheus responses for the observability PromQL proxy, used in
// MSW (VITE_MSW=true) dev and Playwright. The handler inspects the
// PromQL `query` string and returns a plausible vector/matrix so the
// API Gateway activity view renders real-looking data without a live
// Prometheus.
import type {
  PromVectorResponse,
  PromMatrixResponse,
} from "@/api/observability/types";

function vector(samples: { metric: Record<string, string>; value: number }[]): PromVectorResponse {
  const now = Math.floor(Date.now() / 1000);
  return {
    status: "success",
    data: {
      resultType: "vector",
      result: samples.map((s) => ({ metric: s.metric, value: [now, String(s.value)] })),
    },
  };
}

function scalarVector(value: number): PromVectorResponse {
  return vector([{ metric: {}, value }]);
}

// promInstantFor returns a canned instant-query response based on the
// shape of the PromQL query (which "by (label)" grouping or scalar
// aggregate it is).
export function promInstantFor(query: string): PromVectorResponse {
  if (query.includes("by (connection)")) {
    return vector([
      { metric: { connection: "salesforce" }, value: 18432 },
      { metric: { connection: "stripe" }, value: 9211 },
      { metric: { connection: "iterable" }, value: 4096 },
    ]);
  }
  if (query.includes("by (operation_id)")) {
    return vector([
      { metric: { operation_id: "listContacts" }, value: 8123 },
      { metric: { operation_id: "getAccount" }, value: 5320 },
      { metric: { operation_id: "createEvent" }, value: 2011 },
    ]);
  }
  if (query.includes("by (status_class)")) {
    return vector([
      { metric: { status_class: "2xx" }, value: 7600 },
      { metric: { status_class: "4xx" }, value: 420 },
      { metric: { status_class: "5xx" }, value: 103 },
    ]);
  }
  if (query.includes("by (method)")) {
    return vector([
      { metric: { method: "GET" }, value: 6900 },
      { metric: { method: "POST" }, value: 1223 },
    ]);
  }
  if (query.includes("by (identity)")) {
    return vector([
      { metric: { identity: "nifi-etl" }, value: 7800 },
      { metric: { identity: "analytics-cron" }, value: 323 },
    ]);
  }
  if (query.includes("histogram_quantile")) {
    if (query.includes("0.99")) return scalarVector(1.42);
    if (query.includes("0.95")) return scalarVector(0.88);
    return scalarVector(0.142);
  }
  if (query.includes("status_class=~")) {
    // error-rate numerator/full expression -> a fraction
    return scalarVector(0.031);
  }
  // total requests (sum(increase(...)))
  return scalarVector(18432);
}

// promRangeFor returns a canned range-query (matrix) response: one
// series of evenly spaced buckets across [start, end].
export function promRangeFor(start: number, end: number, step: number): PromMatrixResponse {
  const values: [number, string][] = [];
  const safeStep = step > 0 ? step : 60;
  for (let t = start; t <= end; t += safeStep) {
    // A gentle wave so the line is visibly non-flat in the chart.
    const v = 20 + Math.round(15 * Math.sin((t - start) / safeStep / 3));
    values.push([t, String(v)]);
  }
  return {
    status: "success",
    data: { resultType: "matrix", result: [{ metric: {}, values }] },
  };
}

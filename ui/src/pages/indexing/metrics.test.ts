import { describe, expect, it } from "vitest";
import type { PromVectorResponse } from "@/api/observability/types";
import {
  completionsPerStep,
  latencyQuantile,
  matrixToThroughput,
  passesByKind,
  vectorsToLatency,
} from "./metrics";

// The query strings are the contract with the exporter
// (pkg/observability/metrics_indexjobs.go); a rename there must fail here.
describe("indexing PromQL", () => {
  it("counts successful jobs per step across kinds and replicas", () => {
    expect(completionsPerStep()).toBe(
      'sum(increase(indexjob_jobs_total{outcome="succeeded"}[15m]))',
    );
  });

  it("reads a quantile of successful passes per kind", () => {
    expect(latencyQuantile(0.95)).toBe(
      'histogram_quantile(0.95, sum by (kind, le) (rate(indexjob_duration_seconds_bucket{outcome="succeeded"}[24h])))',
    );
    expect(passesByKind()).toBe(
      'sum by (kind) (increase(indexjob_duration_seconds_count{outcome="succeeded"}[24h]))',
    );
  });
});

function vec(rows: [string, string][]): PromVectorResponse {
  return {
    status: "success",
    data: {
      resultType: "vector",
      result: rows.map(([kind, v]) => ({ metric: { kind }, value: [0, v] })),
    },
  };
}

describe("matrixToThroughput", () => {
  it("maps each step to a point in milliseconds and drops NaN steps", () => {
    expect(
      matrixToThroughput({
        status: "success",
        data: {
          resultType: "matrix",
          result: [
            {
              metric: {},
              values: [
                [10, "2"],
                [20, "NaN"],
                [30, "5"],
              ],
            },
          ],
        },
      }),
    ).toEqual([
      { t: 10_000, count: 2 },
      { t: 30_000, count: 5 },
    ]);
  });

  it("is empty with no answer", () => {
    expect(matrixToThroughput(undefined)).toEqual([]);
  });
});

describe("vectorsToLatency", () => {
  it("joins the quantiles by kind, in milliseconds, and leaves out a kind with no passes", () => {
    const rows = vectorsToLatency(
      vec([
        ["calls", "0.5"],
        ["tools", "NaN"],
        ["resources", "2"],
      ]),
      vec([
        ["calls", "1"],
        ["resources", "4"],
      ]),
      vec([
        ["calls", "2"],
        ["resources", "8"],
      ]),
      vec([
        ["calls", "100"],
        ["tools", "0"],
        ["resources", "3"],
      ]),
    );
    expect(rows).toEqual([
      { kind: "calls", p50Ms: 500, p95Ms: 1000, p99Ms: 2000, count: 100 },
      { kind: "resources", p50Ms: 2000, p95Ms: 4000, p99Ms: 8000, count: 3 },
    ]);
  });
});

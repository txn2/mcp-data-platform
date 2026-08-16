import { describe, it, expect } from "vitest";
import type { PromMatrixResponse, PromVectorResponse } from "@/api/observability/types";
import {
  failuresOverWindow,
  missedFiresByScript,
  resolutionFor,
  runDurationP95,
  runRateByStatus,
  runTotalOverWindow,
  runsByScript,
  runsInFlight,
  scalar,
  statusMatrixToTimeseries,
  stepFor,
  vectorToBreakdown,
} from "./runMetrics";

// The queries are pinned because a typo in a metric name draws an empty chart
// rather than an error: the panel renders, the line is flat, and nothing says
// the series does not exist. These names must match the instruments in
// pkg/observability/metrics.go.

describe("the queries name the instruments the platform emits", () => {
  it("counts runs by status", () => {
    expect(runRateByStatus("5m")).toBe("sum by (status) (rate(script_runs_total[5m]))");
  });

  it("reads duration from the histogram's buckets", () => {
    expect(runDurationP95("24h")).toBe(
      "histogram_quantile(0.95, sum by (le) (rate(script_run_duration_seconds_bucket[24h])))",
    );
  });

  it("sums the in-flight gauge across replicas", () => {
    expect(runsInFlight()).toBe("sum(script_runs_running)");
  });

  it("counts runs and failures over a window", () => {
    expect(runTotalOverWindow("24h")).toBe("sum(increase(script_runs_total[24h]))");
    expect(failuresOverWindow("24h")).toBe(
      'sum(increase(script_runs_total{status="failed"}[24h]))',
    );
  });

  it("bounds the per-script breakdowns", () => {
    expect(runsByScript("7d")).toContain("topk(10,");
    expect(missedFiresByScript("7d")).toBe(
      "topk(10, sum by (script) (increase(script_missed_fires_total[7d])))",
    );
  });
});

describe("folding a status-split matrix", () => {
  const resp: PromMatrixResponse = {
    status: "success",
    data: {
      resultType: "matrix",
      result: [
        {
          metric: { status: "succeeded" },
          values: [
            [100, "2"],
            [200, "3"],
          ],
        },
        {
          metric: { status: "failed" },
          values: [
            [100, "1"],
            [200, "0"],
          ],
        },
      ],
    },
  } as PromMatrixResponse;

  it("puts successes and everything else on their own lines", () => {
    const buckets = statusMatrixToTimeseries(resp);
    expect(buckets).toHaveLength(2);
    expect(buckets[0]).toMatchObject({ count: 3, success_count: 2, error_count: 1 });
    expect(buckets[1]).toMatchObject({ count: 3, success_count: 3, error_count: 0 });
  });

  // A skipped fire is not a success. Counting only status="failed" would draw a
  // clean chart over an automation that has stopped producing anything.
  it("counts a skipped overlap against the run, not for it", () => {
    const skipped = {
      status: "success",
      data: {
        resultType: "matrix",
        result: [{ metric: { status: "skipped_overlap" }, values: [[100, "4"]] }],
      },
    } as PromMatrixResponse;
    expect(statusMatrixToTimeseries(skipped)[0]).toMatchObject({ error_count: 4, success_count: 0 });
  });

  it("orders the buckets in time", () => {
    const out = statusMatrixToTimeseries(resp);
    expect(new Date(out[0]!.bucket).getTime()).toBeLessThan(new Date(out[1]!.bucket).getTime());
  });

  it("survives an empty answer", () => {
    expect(statusMatrixToTimeseries(undefined)).toEqual([]);
  });
});

describe("reading instant answers", () => {
  const vector = {
    status: "success",
    data: {
      resultType: "vector",
      result: [
        { metric: { script: "daily-sales-report" }, value: [100, "12"] },
        { metric: { script: "warehouse-freshness" }, value: [100, "0"] },
        { metric: {}, value: [100, "3"] },
      ],
    },
  } as PromVectorResponse;

  it("ranks a breakdown and drops what did not happen", () => {
    const rows = vectorToBreakdown(vector, "script");
    expect(rows.map((r) => r.dimension)).toEqual(["daily-sales-report", "(unnamed)"]);
    expect(rows[0]!.count).toBe(12);
  });

  // "Nothing has run" and "everything succeeded" are different answers.
  it("distinguishes no answer from zero", () => {
    expect(scalar(undefined)).toBeUndefined();
    expect(scalar(vector)).toBe(12);
    expect(
      scalar({ status: "success", data: { resultType: "vector", result: [] } } as PromVectorResponse),
    ).toBeUndefined();
  });
});

describe("resolution follows the window", () => {
  it("widens the rate window as the range grows", () => {
    expect(stepFor("1h")).toBe("5m");
    expect(stepFor("24h")).toBe("30m");
    expect(stepFor("7d")).toBe("1h");
  });

  it("widens the query step too", () => {
    expect(resolutionFor("1h")).toBe(60);
    expect(resolutionFor("7d")).toBe(3600);
  });
});

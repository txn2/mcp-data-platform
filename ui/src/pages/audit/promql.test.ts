import { describe, it, expect } from "vitest";
import {
  topConnectionsByVolume,
  connectionRequestTotal,
  connectionErrorRate,
  latencyQuantile,
  topEndpoints,
  endpointByLabel,
  requestRateRange,
  promVectorToBreakdown,
  promMatrixToTimeseries,
  firstScalar,
} from "./promql";
import type { PromVectorResponse, PromMatrixResponse } from "@/api/observability/types";

describe("PromQL builders", () => {
  it("ranks top connections by volume", () => {
    expect(topConnectionsByVolume("24h")).toBe(
      "topk(10, sum by (connection) (increase(apigateway_inbound_requests_total[24h])))",
    );
  });

  it("totals requests for one connection", () => {
    expect(connectionRequestTotal("salesforce", "1h")).toBe(
      'sum(increase(apigateway_inbound_requests_total{connection="salesforce"}[1h]))',
    );
  });

  it("computes error rate scoped to 4xx/5xx with a zero-guarded denominator", () => {
    expect(connectionErrorRate("salesforce", "24h")).toBe(
      'sum(increase(apigateway_inbound_requests_total{connection="salesforce",status_class=~"4xx|5xx"}[24h]))'
        + ' / (sum(increase(apigateway_inbound_requests_total{connection="salesforce"}[24h])) > 0)',
    );
  });

  it("builds a histogram_quantile over duration buckets", () => {
    expect(latencyQuantile(0.95, "salesforce", "24h")).toBe(
      "histogram_quantile(0.95, sum by (le) (rate(apigateway_inbound_duration_seconds_bucket"
        + '{connection="salesforce"}[24h])))',
    );
  });

  it("ranks top endpoints for a connection", () => {
    expect(topEndpoints("salesforce", "7d")).toBe(
      'topk(10, sum by (operation_id) (increase(apigateway_inbound_requests_total{connection="salesforce"}[7d])))',
    );
  });

  it("ranks an endpoint by status_class / method / identity", () => {
    expect(endpointByLabel("identity", "salesforce", "getAccount", "24h")).toBe(
      "topk(10, sum by (identity) (increase(apigateway_inbound_requests_total"
        + '{connection="salesforce",operation_id="getAccount"}[24h])))',
    );
  });

  it("builds a request-rate range query scoped to connection+operation", () => {
    expect(requestRateRange({ connection: "salesforce", operationID: "getAccount" }, "5m")).toBe(
      'sum(rate(apigateway_inbound_requests_total{connection="salesforce",operation_id="getAccount"}[5m]))',
    );
  });

  it("builds an unscoped request-rate range query", () => {
    expect(requestRateRange({}, "5m")).toBe(
      "sum(rate(apigateway_inbound_requests_total[5m]))",
    );
  });

  it("escapes quotes and backslashes in label values", () => {
    expect(connectionRequestTotal('a"b\\c', "1h")).toBe(
      'sum(increase(apigateway_inbound_requests_total{connection="a\\"b\\\\c"}[1h]))',
    );
  });
});

describe("PromQL result adapters", () => {
  it("maps a vector response to breakdown entries by label", () => {
    const resp: PromVectorResponse = {
      status: "success",
      data: {
        resultType: "vector",
        result: [
          { metric: { connection: "salesforce" }, value: [1700000000, "1234"] },
          { metric: { connection: "stripe" }, value: [1700000000, "56.7"] },
          { metric: {}, value: [1700000000, "3"] },
        ],
      },
    };
    expect(promVectorToBreakdown(resp, "connection")).toEqual([
      { dimension: "salesforce", count: 1234, success_rate: 0, avg_duration_ms: 0 },
      { dimension: "stripe", count: 57, success_rate: 0, avg_duration_ms: 0 },
      { dimension: "(none)", count: 3, success_rate: 0, avg_duration_ms: 0 },
    ]);
  });

  it("returns empty breakdown for undefined or empty result", () => {
    expect(promVectorToBreakdown(undefined, "connection")).toEqual([]);
  });

  it("maps a matrix response's first series to timeseries buckets", () => {
    const resp: PromMatrixResponse = {
      status: "success",
      data: {
        resultType: "matrix",
        result: [
          {
            metric: {},
            values: [
              [1700000000, "5"],
              [1700000060, "8"],
            ],
          },
        ],
      },
    };
    const out = promMatrixToTimeseries(resp);
    expect(out).toHaveLength(2);
    expect(out[0]).toEqual({
      bucket: new Date(1700000000 * 1000).toISOString(),
      count: 5,
      success_count: 0,
      error_count: 0,
      avg_duration_ms: 0,
    });
    expect(out[1]?.count).toBe(8);
  });

  it("returns empty timeseries for undefined or empty result", () => {
    expect(promMatrixToTimeseries(undefined)).toEqual([]);
  });

  it("extracts a single scalar value, or undefined when absent/NaN", () => {
    const resp: PromVectorResponse = {
      status: "success",
      data: { resultType: "vector", result: [{ metric: {}, value: [1700000000, "0.142"] }] },
    };
    expect(firstScalar(resp)).toBeCloseTo(0.142);
    expect(firstScalar(undefined)).toBeUndefined();
    expect(
      firstScalar({ status: "success", data: { resultType: "vector", result: [] } }),
    ).toBeUndefined();
    expect(
      firstScalar({
        status: "success",
        data: { resultType: "vector", result: [{ metric: {}, value: [1, "NaN"] }] },
      }),
    ).toBeUndefined();
  });
});

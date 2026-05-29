import { describe, it, expect } from "vitest";
import {
  topConnectionsByVolume,
  connectionRequestTotal,
  connectionErrorRate,
  latencyQuantile,
  topEndpoints,
  endpointByLabel,
  requestRateRange,
  connectionOperationFlow,
  statusClassRateRange,
  promVectorToBreakdown,
  promVectorToFlow,
  promMatrixToStatusStack,
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

describe("connection -> operation flow", () => {
  it("builds the by-connection-and-operation query", () => {
    expect(connectionOperationFlow("24h")).toBe(
      "sum by (connection, operation_id) (increase(apigateway_inbound_requests_total[24h]))",
    );
  });

  it("maps a vector into a sankey graph with per-connection operation nodes", () => {
    const resp: PromVectorResponse = {
      status: "success",
      data: {
        resultType: "vector",
        result: [
          { metric: { connection: "salesforce", operation_id: "listContacts" }, value: [1, "800"] },
          { metric: { connection: "salesforce", operation_id: "getAccount" }, value: [1, "500"] },
          { metric: { connection: "stripe", operation_id: "listCharges" }, value: [1, "300"] },
          { metric: { connection: "stripe", operation_id: "listContacts" }, value: [1, "0"] }, // dropped (0)
        ],
      },
    };
    const g = promVectorToFlow(resp);
    // 2 connection nodes + 3 operation nodes (the 0-value link is dropped).
    expect(g.links).toHaveLength(3);
    expect(g.nodes.filter((n) => n.kind === "connection").map((n) => n.name).sort()).toEqual([
      "salesforce",
      "stripe",
    ]);
    // Operation nodes are connection-scoped: salesforce/listContacts and
    // stripe/listContacts are distinct nodes despite the shared name.
    const opNodes = g.nodes.filter((n) => n.kind === "operation");
    expect(opNodes).toHaveLength(3);
    // Every link points from a connection node to an operation node.
    for (const l of g.links) {
      expect(g.nodes[l.source]!.kind).toBe("connection");
      expect(g.nodes[l.target]!.kind).toBe("operation");
    }
  });

  it("returns an empty graph for undefined", () => {
    expect(promVectorToFlow(undefined)).toEqual({ nodes: [], links: [] });
  });
});

describe("status-class stacked area", () => {
  it("builds the rate-by-status-class range query", () => {
    expect(statusClassRateRange("5m")).toBe(
      "sum by (status_class) (rate(apigateway_inbound_requests_total[5m]))",
    );
  });

  it("merges multi-series matrix into per-timestamp 2xx/4xx/5xx buckets", () => {
    const resp: PromMatrixResponse = {
      status: "success",
      data: {
        resultType: "matrix",
        result: [
          { metric: { status_class: "2xx" }, values: [[100, "30"], [160, "32"]] },
          { metric: { status_class: "4xx" }, values: [[100, "3"], [160, "4"]] },
          { metric: { status_class: "503" }, values: [[100, "1"]] }, // folds into 5xx by leading digit
        ],
      },
    };
    const out = promMatrixToStatusStack(resp);
    expect(out).toHaveLength(2);
    expect(out[0]).toEqual({
      bucket: new Date(100 * 1000).toISOString(),
      "2xx": 30,
      "4xx": 3,
      "5xx": 1,
    });
    expect(out[1]).toMatchObject({ "2xx": 32, "4xx": 4, "5xx": 0 });
  });

  it("returns empty for undefined", () => {
    expect(promMatrixToStatusStack(undefined)).toEqual([]);
  });
});

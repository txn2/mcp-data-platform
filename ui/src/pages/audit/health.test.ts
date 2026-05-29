import { describe, it, expect } from "vitest";
import { mergeNodeMetrics, seriesByNode } from "./health";
import type { PromVectorResponse, PromMatrixResponse } from "@/api/observability/types";

function vec(samples: { metric: Record<string, string>; value: string }[]): PromVectorResponse {
  return {
    status: "success",
    data: {
      resultType: "vector",
      result: samples.map((s) => ({ metric: s.metric, value: [1_700_000_000, s.value] })),
    },
  };
}

describe("mergeNodeMetrics", () => {
  it("merges per-metric vectors into one row per node, keyed by pod, sorted by label", () => {
    const nodes = mergeNodeMetrics([
      {
        key: "uptime",
        resp: vec([
          { metric: { pod: "pod-b" }, value: "100" },
          { metric: { pod: "pod-a" }, value: "200" },
        ]),
      },
      { key: "cpu", resp: vec([{ metric: { pod: "pod-a" }, value: "0.5" }]) },
    ]);
    expect(nodes.map((n) => n.id)).toEqual(["pod-a", "pod-b"]);
    expect(nodes[0]).toMatchObject({ id: "pod-a", label: "pod-a", uptime: 200, cpu: 0.5 });
    expect(nodes[1]).toMatchObject({ id: "pod-b", uptime: 100 });
    // A metric missing for a node stays undefined (renders "-"), not 0.
    expect(nodes[1]!.cpu).toBeUndefined();
  });

  it("falls back to the instance label when pod is absent (non-k8s)", () => {
    const nodes = mergeNodeMetrics([
      { key: "goroutines", resp: vec([{ metric: { instance: "10.0.0.1:9464" }, value: "42" }]) },
    ]);
    expect(nodes[0]).toMatchObject({ id: "10.0.0.1:9464", label: "10.0.0.1:9464", goroutines: 42 });
  });

  it("returns no nodes for undefined responses", () => {
    expect(mergeNodeMetrics([{ key: "cpu", resp: undefined }])).toEqual([]);
  });

  it("ignores non-finite values", () => {
    const nodes = mergeNodeMetrics([
      { key: "cpu", resp: vec([{ metric: { pod: "p" }, value: "NaN" }]) },
    ]);
    expect(nodes[0]!.cpu).toBeUndefined();
  });
});

describe("seriesByNode", () => {
  it("maps a matrix to per-node point arrays keyed by node identity", () => {
    const m: PromMatrixResponse = {
      status: "success",
      data: {
        resultType: "matrix",
        result: [
          { metric: { pod: "pod-a" }, values: [[100, "0.1"], [160, "0.2"]] },
          { metric: { pod: "pod-b" }, values: [[100, "0.3"]] },
        ],
      },
    };
    const byNode = seriesByNode(m);
    expect(byNode.get("pod-a")).toEqual([
      { t: 100, value: 0.1 },
      { t: 160, value: 0.2 },
    ]);
    expect(byNode.get("pod-b")).toEqual([{ t: 100, value: 0.3 }]);
  });

  it("returns an empty map for undefined", () => {
    expect(seriesByNode(undefined).size).toBe(0);
  });
});

import { describe, expect, it } from "vitest";
import type { FlowStructure } from "@/api/portal/hooks/scriptFlow";
import { sampleGraph } from "./testGraph";
import {
  COLLAPSE_AT,
  boxText,
  defaultFolded,
  failedWithin,
  fold,
  loopCalls,
  neighbors,
  nodeText,
  nodesIn,
  runState,
  stepCard,
  untested,
} from "./structureModel";

// big is a structure whose helper holds more nodes than open folded.
function big(): FlowStructure {
  const s = sampleGraph().structure;
  const extra = Array.from({ length: COLLAPSE_AT }, (_, i) => ({
    id: `x:${i}`,
    kind: "step" as const,
    step: "op:1",
    box: "b:1",
    line: 5,
  }));
  return { ...s, nodes: [...s.nodes, ...extra] };
}

describe("structureModel", () => {
  it("counts the nodes a box holds, nested boxes included", () => {
    const s = sampleGraph().structure;
    expect(nodesIn(s, "b:1")).toBe(1);
    const nested = { ...s, boxes: [...s.boxes, { id: "b:3", kind: "loop" as const, label: "for x in y", parent: "b:1", line: 5 }] };
    nested.nodes = [...nested.nodes, { id: "s:9", kind: "step", step: "op:1", box: "b:3", line: 5 }];
    expect(nodesIn(nested, "b:1")).toBe(2);
  });

  it("opens a large helper folded, except around where a run failed", () => {
    expect(defaultFolded(sampleGraph().structure)).toEqual(new Set());
    expect(defaultFolded(big())).toEqual(new Set(["b:1"]));
    expect(defaultFolded(big(), "s:2")).toEqual(new Set());
    expect(defaultFolded(big(), "b:1")).toEqual(new Set());
  });

  it("draws a folded helper as one node carrying its edges", () => {
    const s = sampleGraph().structure;
    const v = fold(s, new Set(["b:1"]));
    expect(v.nodes.find((n) => n.id === "s:2")).toBeUndefined();
    const f = v.nodes.find((n) => n.id === "b:1")!;
    expect(f.kind).toBe("fn");
    expect(v.boxes.map((b) => b.id)).toEqual(["b:2"]);
    expect(v.edges).toContainEqual({ from: "s:1", to: "b:1" });
    expect(v.edges).toContainEqual({ from: "b:1", to: "s:3" });
    expect(fold(s, new Set())).toEqual({ nodes: s.nodes, edges: s.edges, boxes: s.boxes });
  });

  it("drops the edges inside a folded box and keeps one of each left", () => {
    const s = big();
    s.edges = [...s.edges, { from: "s:2", to: "x:0" }, { from: "x:0", to: "s:3" }];
    const v = fold(s, new Set(["b:1"]));
    expect(v.edges.filter((e) => e.from === "b:1" && e.to === "s:3")).toHaveLength(1);
    expect(v.edges.some((e) => e.from === e.to)).toBe(false);
  });

  it("words each node as agreed", () => {
    const g = sampleGraph();
    const text = (id: string) => nodeText(g.structure.nodes.find((n) => n.id === id)!, g);
    expect(text("s:1")).toBe("Start");
    expect(text("s:8")).toBe("End");
    expect(text("s:6")).toBe("Stops: unknown mode");
    expect(text("s:4")).toBe('If mode == "full"');
    expect(text("s:2")).toBe("Query trino");
    expect(nodeText({ id: "r", kind: "return", line: 3 })).toBe("Returns early");
    expect(nodeText({ id: "s", kind: "stop", line: 3 })).toBe("Stops");
    expect(nodeText({ id: "p", kind: "step", step: "op:99", label: "platform.query", line: 3 }, g)).toBe("platform.query");
    expect(nodeText({ id: "p", kind: "step", line: 3 })).toBe("platform call");
    expect(stepCard({ id: "s:1", kind: "start", line: 0 }, g)).toBeUndefined();
    expect(boxText(g.structure.boxes[1]!)).toBe("Repeats: for d in days");
    expect(boxText(g.structure.boxes[0]!)).toBe("load(day)");
  });

  it("lights a call's data neighbors in the value graph", () => {
    const g = sampleGraph();
    expect(neighbors(g, "op:2")).toEqual(new Set(["op:1", "op:2", "op:3"]));
    // The state edge carries nothing a call is fed.
    expect(neighbors(g, "op:4")).toEqual(new Set(["op:4"]));
  });

  it("counts a loop by the most calls one call inside it made", () => {
    const s = sampleGraph().structure;
    const stat = (calls: number) => ({ calls, duration_ms: 0, response_chars: 0, outputs: 0, rows: 0, failed_calls: 0, reached: true, failed: false });
    expect(loopCalls(s, "b:2", { "op:2": stat(7) })).toBe(7);
    expect(loopCalls(s, "b:2", {})).toBe(0);
    expect(loopCalls(s, "b:2")).toBe(0);
  });

  it("reads a node as ran, skipped, failed or plain", () => {
    const s = sampleGraph().structure;
    const node = (id: string) => s.nodes.find((n) => n.id === id)!;
    const reached = { calls: 1, duration_ms: 0, response_chars: 0, outputs: 0, rows: 0, failed_calls: 0, reached: true, failed: false };
    const run = { nodes: { "op:1": reached }, status: "failed", structure_failed: "s:6", unplaced: false };
    expect(runState(node("s:2"), run, s)).toBe("ran");
    expect(runState(node("s:3"), run, s)).toBe("skipped");
    expect(runState(node("s:6"), run, s)).toBe("failed");
    expect(runState(node("s:8"), run, s)).toBe("skipped");
    expect(runState(node("s:8"), { ...run, status: "succeeded" }, s)).toBe("ran");
    expect(runState(node("s:4"), run, s)).toBe("plain");
    expect(runState(node("s:6"), { ...run, structure_failed: undefined }, s)).toBe("skipped");
    expect(runState(node("s:3"), undefined, s)).toBe("plain");
    expect(runState(node("s:3"), { ...run, unplaced: true }, s)).toBe("plain");

    const folded = fold(s, new Set(["b:1"])).nodes.find((n) => n.id === "b:1")!;
    expect(runState(folded, run, s)).toBe("ran");
    expect(runState(folded, { ...run, nodes: {} }, s)).toBe("skipped");
    expect(runState(folded, { ...run, structure_failed: "s:2" }, s)).toBe("failed");
  });

  it("says whether a run failed inside a box", () => {
    const s = sampleGraph().structure;
    expect(failedWithin(s, "b:1", "s:2")).toBe(true);
    expect(failedWithin(s, "b:1", "b:1")).toBe(true);
    expect(failedWithin(s, "b:1", "s:3")).toBe(false);
    expect(failedWithin(s, "b:1", undefined)).toBe(false);
  });

  it("marks a node on a line the tests miss, but never Start, End or a folded helper", () => {
    const s = sampleGraph().structure;
    const missed = new Set([0, 13, 8]);
    expect(untested(s.nodes[5]!, missed)).toBe(true);
    expect(untested(s.nodes[0]!, missed)).toBe(false);
    expect(untested(s.nodes[4]!, missed)).toBe(false);
    expect(untested(s.nodes[5]!, undefined)).toBe(false);
    const folded = fold(s, new Set(["b:1"])).nodes.find((n) => n.id === "b:1")!;
    expect(untested(folded, missed)).toBe(false);
  });
});

import { describe, expect, it } from "vitest";
import type { ElkNode } from "elkjs/lib/elk-api";
import { elkGraph, layoutFlow, place } from "./flowLayout";
import { sampleGraph } from "./testGraph";

describe("flowLayout", () => {
  it("nests each card in its box and puts every edge at the root", () => {
    const g = sampleGraph();
    const root = elkGraph(g);
    const ids = root.children!.map((c) => c.id);
    expect(ids).toEqual(["g:/load", "state", "op:2", "op:3", "op:4"]);
    const box = root.children![0]!;
    expect(box.children!.map((c) => c.id)).toEqual(["op:1"]);
    expect(box.layoutOptions!["elk.padding"]).toContain("top=58");
    expect(root.edges!.map((e) => `${e.sources[0]}>${e.targets[0]}`)).toEqual([
      "state>op:1",
      "op:1>op:2",
      "op:2>op:3",
      "state>op:4",
    ]);
  });

  it("reads positions relative to their parents, and edges relative to their box", () => {
    const g = sampleGraph();
    const out: ElkNode = {
      id: "root",
      width: 900,
      height: 400,
      children: [
        {
          id: "g:/load",
          x: 100,
          y: 50,
          width: 300,
          height: 200,
          children: [{ id: "op:1", x: 16, y: 40, width: 250, height: 60 }],
          edges: [
            {
              id: "e1",
              sources: ["op:1"],
              targets: ["op:2"],
              container: "g:/load",
              sections: [{ id: "s", startPoint: { x: 0, y: 0 }, endPoint: { x: 10, y: 0 } }],
            },
          ],
        },
      ],
      edges: [
        { id: "e0", sources: ["state"], targets: ["op:1"], sections: [{ id: "s", startPoint: { x: 1, y: 2 }, endPoint: { x: 3, y: 2 } }] },
        { id: "e99", sources: ["x"], targets: ["y"] },
      ],
    };
    const l = place(g, out);
    expect(l.width).toBe(900);
    expect(l.groups[0]).toMatchObject({ x: 100, y: 50, width: 300 });
    expect(l.nodes[0]).toMatchObject({ x: 116, y: 90 });
    expect(l.edges.map((e) => e.path)).toEqual(["M1,2 L3,2", "M100,50 L110,50"]);
  });

  it("lays a real graph out left to right", async () => {
    const g = sampleGraph();
    const l = await layoutFlow(g);
    const at = (id: string) => l.nodes.find((n) => n.node.id === id)!;
    expect(l.nodes).toHaveLength(g.nodes.length);
    expect(l.groups).toHaveLength(1);
    expect(at("op:1").x).toBeLessThan(at("op:2").x);
    expect(at("op:2").x).toBeLessThan(at("op:3").x);
    const box = l.groups[0]!;
    expect(at("op:1").x).toBeGreaterThanOrEqual(box.x);
    expect(at("op:1").x + at("op:1").width).toBeLessThanOrEqual(box.x + box.width);
    expect(l.edges.length).toBeGreaterThanOrEqual(g.edges.length);
    for (const e of l.edges) expect(e.path).toMatch(/^M/);
    // The save is laid out where the run ends, not in front of run.state.
    expect(at("state").x).toBeLessThan(at("op:4").x);
    expect(l.edges.find((e) => e.edge.kind === "state")?.reversed).toBe(true);
  });
});

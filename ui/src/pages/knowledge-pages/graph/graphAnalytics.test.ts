import { describe, it, expect } from "vitest";
import {
  betweennessCentrality,
  louvainCommunities,
  modularity,
  neighborhood,
  pathEdgeKeys,
  shortestPath,
  type Adjacency,
  type EdgeLike,
} from "./graphAnalytics";

/**
 * Every graph here has an answer that is derivable by hand, so these tests pin
 * the algorithms to arithmetic rather than to whatever the implementation
 * currently happens to produce.
 */

function edges(...pairs: [string, string][]): EdgeLike[] {
  return pairs.map(([source, target]) => ({ source, target }));
}

/** adjacencyOf builds the undirected neighbour index from an edge list. */
function adjacencyOf(ids: string[], list: EdgeLike[]): Adjacency {
  const adj: Adjacency = new Map(ids.map((id) => [id, new Set<string>()]));
  for (const e of list) {
    adj.get(e.source)?.add(e.target);
    adj.get(e.target)?.add(e.source);
  }
  return adj;
}

describe("betweennessCentrality", () => {
  it("scores a path graph by hand-countable pair counts", () => {
    // a-b-c-d-e. Pairs whose shortest path crosses c: {a,b} x {d,e} = 4.
    // Across b: a x {c,d,e} = 3. By symmetry d is 3. Endpoints cross nothing.
    const ids = ["a", "b", "c", "d", "e"];
    const list = edges(["a", "b"], ["b", "c"], ["c", "d"], ["d", "e"]);

    const cb = betweennessCentrality(ids, adjacencyOf(ids, list));

    expect(cb.get("a")).toBe(0);
    expect(cb.get("b")).toBe(3);
    expect(cb.get("c")).toBe(4);
    expect(cb.get("d")).toBe(3);
    expect(cb.get("e")).toBe(0);
  });

  it("scores a star's centre at every pair of leaves", () => {
    // Four leaves: every one of the C(4,2)=6 leaf pairs goes through the hub.
    const ids = ["hub", "l1", "l2", "l3", "l4"];
    const list = edges(["hub", "l1"], ["hub", "l2"], ["hub", "l3"], ["hub", "l4"]);

    const cb = betweennessCentrality(ids, adjacencyOf(ids, list));

    expect(cb.get("hub")).toBe(6);
    for (const leaf of ["l1", "l2", "l3", "l4"]) expect(cb.get(leaf)).toBe(0);
  });

  it("splits credit evenly between two equally short routes", () => {
    // A square: a and c are joined by two 2-hop routes, through b and through d,
    // so each carries half of that one pair. No other pair needs an intermediate.
    const ids = ["a", "b", "c", "d"];
    const list = edges(["a", "b"], ["b", "c"], ["c", "d"], ["d", "a"]);

    const cb = betweennessCentrality(ids, adjacencyOf(ids, list));

    for (const id of ids) expect(cb.get(id)).toBeCloseTo(0.5);
  });

  it("ranks the bridge of two clusters above every node inside them", () => {
    // This is the shape the view exists to surface: one entity joining two
    // otherwise separate groups of pages.
    const left = ["l1", "l2", "l3"];
    const right = ["r1", "r2", "r3"];
    const ids = [...left, "bridge", ...right];
    const list = edges(
      ["l1", "l2"], ["l2", "l3"], ["l3", "l1"],
      ["r1", "r2"], ["r2", "r3"], ["r3", "r1"],
      ["l1", "bridge"], ["bridge", "r1"],
    );

    const cb = betweennessCentrality(ids, adjacencyOf(ids, list));

    const bridge = cb.get("bridge")!;
    for (const id of [...left, ...right]) expect(bridge).toBeGreaterThan(cb.get(id)!);
  });

  it("gives every node zero on a graph with no edges", () => {
    const ids = ["a", "b", "c"];
    const cb = betweennessCentrality(ids, adjacencyOf(ids, []));
    for (const id of ids) expect(cb.get(id)).toBe(0);
  });
});

describe("louvainCommunities", () => {
  it("separates two triangles joined by a single edge", () => {
    const ids = ["a1", "a2", "a3", "b1", "b2", "b3"];
    const list = edges(
      ["a1", "a2"], ["a2", "a3"], ["a3", "a1"],
      ["b1", "b2"], ["b2", "b3"], ["b3", "b1"],
      ["a1", "b1"],
    );

    const c = louvainCommunities(ids, list);

    expect(c.get("a1")).toBe(c.get("a2"));
    expect(c.get("a2")).toBe(c.get("a3"));
    expect(c.get("b1")).toBe(c.get("b2"));
    expect(c.get("b2")).toBe(c.get("b3"));
    expect(c.get("a1")).not.toBe(c.get("b1"));
    expect(new Set(c.values()).size).toBe(2);
  });

  it("keeps one clique in a single community", () => {
    const ids = ["a", "b", "c", "d"];
    const list = edges(["a", "b"], ["a", "c"], ["a", "d"], ["b", "c"], ["b", "d"], ["c", "d"]);

    const c = louvainCommunities(ids, list);

    expect(new Set(c.values()).size).toBe(1);
  });

  it("gives isolated nodes their own communities", () => {
    const ids = ["a", "b", "c"];
    const c = louvainCommunities(ids, edges(["a", "b"]));
    expect(c.get("a")).toBe(c.get("b"));
    expect(c.get("c")).not.toBe(c.get("a"));
  });

  it("labels communities densely from zero", () => {
    // The labels index a colour scale, so they must be 0..k-1 with no holes.
    const ids = ["a1", "a2", "b1", "b2", "c1", "c2"];
    const c = louvainCommunities(
      ids,
      edges(["a1", "a2"], ["b1", "b2"], ["c1", "c2"]),
    );
    const labels = [...new Set(c.values())].sort((x, y) => x - y);
    expect(labels).toEqual([0, 1, 2]);
  });

  it("is deterministic across runs", () => {
    // The view recomputes on every filter change; a partition that reshuffled
    // would repaint the whole graph a different colour each time.
    const ids = ["a1", "a2", "a3", "b1", "b2", "b3"];
    const list = edges(
      ["a1", "a2"], ["a2", "a3"], ["a3", "a1"],
      ["b1", "b2"], ["b2", "b3"], ["b3", "b1"],
      ["a1", "b1"],
    );
    const first = louvainCommunities(ids, list);
    const second = louvainCommunities(ids, list);
    expect([...second.entries()]).toEqual([...first.entries()]);
  });

  it("survives an edge naming a node that is not in the set", () => {
    const c = louvainCommunities(["a", "b"], edges(["a", "b"], ["a", "ghost"]));
    expect(c.size).toBe(2);
    expect(c.get("a")).toBe(c.get("b"));
  });
});

describe("modularity", () => {
  it("is high for a partition that matches real cluster structure", () => {
    const ids = ["a1", "a2", "a3", "b1", "b2", "b3"];
    const list = edges(
      ["a1", "a2"], ["a2", "a3"], ["a3", "a1"],
      ["b1", "b2"], ["b2", "b3"], ["b3", "b1"],
      ["a1", "b1"],
    );
    expect(modularity(ids, list, louvainCommunities(ids, list))).toBeGreaterThan(0.3);
  });

  it("is zero when everything is in one community", () => {
    const ids = ["a", "b", "c"];
    const list = edges(["a", "b"], ["b", "c"]);
    const single = new Map(ids.map((id) => [id, 0]));
    expect(modularity(ids, list, single)).toBeCloseTo(0);
  });

  it("is zero on a graph with no edges", () => {
    expect(modularity(["a"], [], new Map([["a", 0]]))).toBe(0);
  });
});

describe("neighborhood", () => {
  const ids = ["a", "b", "c", "d", "e"];
  const adj = adjacencyOf(ids, edges(["a", "b"], ["b", "c"], ["c", "d"], ["d", "e"]));

  it("returns the roots alone at depth zero", () => {
    expect([...neighborhood(adj, ["a"], 0)]).toEqual(["a"]);
  });

  it("grows one hop per level", () => {
    expect([...neighborhood(adj, ["a"], 1)].sort()).toEqual(["a", "b"]);
    expect([...neighborhood(adj, ["a"], 2)].sort()).toEqual(["a", "b", "c"]);
    expect([...neighborhood(adj, ["a"], 3)].sort()).toEqual(["a", "b", "c", "d"]);
  });

  it("stops early when the component is exhausted", () => {
    expect(neighborhood(adj, ["a"], 99).size).toBe(5);
  });

  it("takes the union of several roots", () => {
    expect([...neighborhood(adj, ["a", "e"], 1)].sort()).toEqual(["a", "b", "d", "e"]);
  });
});

describe("shortestPath", () => {
  const ids = ["a", "b", "c", "d", "x"];
  const adj = adjacencyOf(ids, edges(["a", "b"], ["b", "c"], ["c", "d"], ["a", "d"]));

  it("returns the hops in order, inclusive of both ends", () => {
    const path = shortestPath(adj, "a", "c")!;
    expect(path).toEqual(["a", "b", "c"]);
  });

  it("takes the shorter of two routes", () => {
    // a-d is a direct edge as well as a 3-hop route through b and c.
    expect(shortestPath(adj, "a", "d")).toEqual(["a", "d"]);
  });

  it("returns the single node when the ends are the same", () => {
    expect(shortestPath(adj, "a", "a")).toEqual(["a"]);
  });

  it("returns null across disconnected components", () => {
    expect(shortestPath(adj, "a", "x")).toBeNull();
  });
});

describe("pathEdgeKeys", () => {
  it("keys each hop independently of direction", () => {
    expect([...pathEdgeKeys(["a", "b", "c"])].sort()).toEqual([...pathEdgeKeys(["c", "b", "a"])].sort());
    expect(pathEdgeKeys(["a", "b", "c"]).size).toBe(2);
  });

  it("has no edges for a single-node path", () => {
    expect(pathEdgeKeys(["a"]).size).toBe(0);
  });
});

describe("modularity edge cases", () => {
  it("does not count a self-loop as internal weight", () => {
    // buildWeighted drops a self-loop from the degrees, so counting it as an
    // internal edge here would push the score outside its documented [-1, 1].
    const single = new Map([
      ["a", 0],
      ["b", 0],
    ]);
    const q = modularity(["a", "b"], edges(["a", "b"], ["a", "a"]), single);
    expect(q).toBeCloseTo(0);
    expect(q).toBeLessThanOrEqual(1);
  });
});

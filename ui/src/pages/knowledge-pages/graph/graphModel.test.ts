import { describe, it, expect } from "vitest";
import type { KnowledgeGraphEdge, KnowledgeGraphNode } from "@/api/portal/hooks";
import {
  MAX_NODE_RADIUS,
  MIN_NODE_RADIUS,
  bridgeRank,
  buildGraphIndex,
  filterByTypes,
  matchingNodeIds,
  nodeDestination,
  nodeRadius,
  nodeStyle,
  typeFacet,
  typeLabel,
} from "./graphModel";

function page(id: string, label = id): KnowledgeGraphNode {
  return { id: `mcp:knowledge_page:${id}`, type: "knowledge_page", label, exists: true, page: true };
}

function entity(id: string, type: string, label: string): KnowledgeGraphNode {
  return { id, type, label, exists: true, page: false };
}

function edge(source: string, target: string, type: string): KnowledgeGraphEdge {
  return { source, target, type, ref_source: "manual" };
}

describe("buildGraphIndex", () => {
  it("records adjacency in both directions and the degree of each node", () => {
    const nodes = [page("a"), page("b"), entity("urn:li:dataset:x", "datahub", "x")];
    const edges = [
      edge("mcp:knowledge_page:a", "urn:li:dataset:x", "datahub"),
      edge("mcp:knowledge_page:b", "urn:li:dataset:x", "datahub"),
    ];

    const { neighbors, degree } = buildGraphIndex(nodes, edges);

    // The dataset bridges the two pages: hovering it must light both.
    expect([...(neighbors.get("urn:li:dataset:x") ?? [])].sort()).toEqual([
      "mcp:knowledge_page:a",
      "mcp:knowledge_page:b",
    ]);
    expect([...(neighbors.get("mcp:knowledge_page:a") ?? [])]).toEqual(["urn:li:dataset:x"]);
    expect(degree.get("urn:li:dataset:x")).toBe(2);
    expect(degree.get("mcp:knowledge_page:a")).toBe(1);
  });

  it("gives an isolated node an empty neighborhood and zero degree", () => {
    const { neighbors, degree } = buildGraphIndex([page("lonely")], []);
    expect(neighbors.get("mcp:knowledge_page:lonely")?.size).toBe(0);
    expect(degree.get("mcp:knowledge_page:lonely")).toBe(0);
  });
});

describe("filterByTypes", () => {
  const nodes = [
    page("a"),
    entity("urn:li:dataset:x", "datahub", "x"),
    entity("mcp:asset:a1", "asset", "Revenue Dashboard"),
  ];
  const edges = [
    edge("mcp:knowledge_page:a", "urn:li:dataset:x", "datahub"),
    edge("mcp:knowledge_page:a", "mcp:asset:a1", "asset"),
  ];

  it("is the identity when nothing is hidden", () => {
    const out = filterByTypes(nodes, edges, new Set());
    expect(out.nodes).toBe(nodes);
    expect(out.edges).toBe(edges);
  });

  it("drops a hidden type together with every edge that would dangle", () => {
    const out = filterByTypes(nodes, edges, new Set(["datahub"]));
    expect(out.nodes.map((n) => n.id)).toEqual(["mcp:knowledge_page:a", "mcp:asset:a1"]);
    expect(out.edges).toHaveLength(1);
    expect(out.edges[0]?.target).toBe("mcp:asset:a1");
  });

  it("keeps page nodes even when their type is hidden", () => {
    // Pages are the corpus the view exists to show; hiding them would leave
    // referenced entities floating with nothing that explains them.
    const out = filterByTypes(nodes, edges, new Set(["knowledge_page"]));
    expect(out.nodes.map((n) => n.id)).toContain("mcp:knowledge_page:a");
  });

  it("keeps a cited page that is outside the listing window", () => {
    // Only pages IN the window carry page=true. A page cited from inside the
    // window but listed outside it is still a knowledge page, so the "pages are
    // never hidden" rule must key on the type, not on that flag — otherwise it
    // would be the single page the type filter can delete.
    const cited = { ...page("outside"), page: false };
    const out = filterByTypes([page("a"), cited], [], new Set(["knowledge_page"]));
    expect(out.nodes.map((n) => n.id)).toContain("mcp:knowledge_page:outside");
  });
});

describe("matchingNodeIds", () => {
  const nodes = [page("a", "Fiscal Calendar"), page("b", "Revenue Definition")];

  it("matches labels case-insensitively on a substring", () => {
    expect([...matchingNodeIds(nodes, "fiscal")]).toEqual(["mcp:knowledge_page:a"]);
    expect([...matchingNodeIds(nodes, "DEFINITION")]).toEqual(["mcp:knowledge_page:b"]);
  });

  it("matches nothing for an empty or whitespace query", () => {
    // Clearing the search box must remove the focus, not select the whole graph.
    expect(matchingNodeIds(nodes, "").size).toBe(0);
    expect(matchingNodeIds(nodes, "   ").size).toBe(0);
  });
});

describe("nodeDestination", () => {
  it("opens a knowledge page by id", () => {
    expect(nodeDestination(page("kp1"))).toEqual({ kind: "page", id: "kp1" });
  });

  it("navigates to an entity's portal home", () => {
    expect(nodeDestination(entity("mcp:asset:a1", "asset", "Revenue Dashboard"))).toEqual({
      kind: "path",
      path: "/assets/a1",
    });
    expect(nodeDestination(entity("mcp:prompt:p1", "prompt", "Summary"))).toEqual({
      kind: "path",
      path: "/prompts/p1",
    });
  });

  it("opens a catalog entity in the catalog tab", () => {
    // A catalog node's label is derived from its URN, so the only way to reach
    // the real entity is a deep link keyed by the whole URN.
    const urn = "urn:li:dataset:(trino,sales.orders,PROD)";
    expect(nodeDestination(entity(urn, "datahub", "sales.orders"))).toEqual({
      kind: "path",
      path: `/knowledge/catalog?urn=${encodeURIComponent(urn)}`,
    });
  });

  it("has no destination for a connection", () => {
    expect(nodeDestination(entity("mcp:connection:(trino,wh)", "connection", "wh (trino)"))).toBeNull();
  });

  it("has no destination for a broken reference", () => {
    // There is nothing left to open, so the node must not be clickable-through.
    const broken = { ...page("gone"), exists: false };
    expect(nodeDestination(broken)).toBeNull();
  });
});

describe("typeFacet", () => {
  it("counts the types present, most-used first", () => {
    const nodes = [
      page("a"),
      page("b"),
      entity("urn:li:dataset:x", "datahub", "x"),
    ];
    expect(typeFacet(nodes)).toEqual([
      ["knowledge_page", 2],
      ["datahub", 1],
    ]);
  });
});

describe("node marks", () => {
  it("names every known type and falls back for an unknown one", () => {
    expect(typeLabel("knowledge_page")).toBe("Page");
    expect(typeLabel("datahub")).toBe("Catalog");
    expect(typeLabel("something_new")).toBe("Other");
    expect(nodeStyle("something_new").fill).toBeTruthy();
  });

  it("maps normalized importance onto a bounded radius range", () => {
    expect(nodeRadius(0)).toBe(MIN_NODE_RADIUS);
    expect(nodeRadius(1)).toBe(MAX_NODE_RADIUS);
    expect(nodeRadius(0.1)).toBeLessThan(nodeRadius(0.5));
    // Out-of-range input clamps rather than producing an absurd mark.
    expect(nodeRadius(-5)).toBe(MIN_NODE_RADIUS);
    expect(nodeRadius(99)).toBe(MAX_NODE_RADIUS);
  });

  it("keeps the low end of the scale distinguishable", () => {
    // A square-root curve is used precisely so a single dominant hub does not
    // flatten everything else into one indistinguishable size.
    expect(nodeRadius(0.25) - nodeRadius(0.05)).toBeGreaterThan(1);
  });
});

describe("bridgeRank", () => {
  it("returns null for a node that bridges nothing", () => {
    expect(bridgeRank(0, [0, 4, 9])).toBeNull();
    expect(bridgeRank(-1, [0, 4, 9])).toBeNull();
  });

  it("calls the corpus's strongest bridge exactly that", () => {
    // A star's hub is the only node that bridges anything, so it is a population
    // of one. Reporting it as "top 100%" — which a below-count percentile does —
    // says the opposite of what it is.
    const rank = bridgeRank(28, [28, 0, 0, 0]);
    expect(rank).not.toBeNull();
    expect(rank!.strongest).toBe(true);
    expect(rank!.rank).toBe(1);
    expect(rank!.total).toBe(1);
  });

  it("ranks among the bridges only, ignoring the leaves", () => {
    const rank = bridgeRank(4, [10, 4, 1, 0, 0, 0, 0, 0, 0, 0])!;
    expect(rank.rank).toBe(2);
    expect(rank.total).toBe(3);
  });

  it("never reports a meaningless top 100%", () => {
    // The weakest bridge in a population of three is rank 3/3; the band is
    // clamped so the phrase always reads as a top-N band.
    for (const score of [10, 4, 1]) {
      const rank = bridgeRank(score, [10, 4, 1, 0, 0])!;
      expect(rank.percentile).toBeGreaterThanOrEqual(1);
      expect(rank.percentile).toBeLessThanOrEqual(100);
    }
  });
});

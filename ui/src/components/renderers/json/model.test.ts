import { describe, it, expect } from "vitest";
import {
  flattenJson,
  visibleNodes,
  allContainerPaths,
  defaultCollapsed,
  searchNodes,
  ancestorPaths,
  valueAtPath,
  resolvePath,
  containerSummary,
  jsonTypeOf,
  type JsonValue,
} from "./model";

const doc = {
  total: 2,
  active: true,
  missing: null,
  results: [
    { id: 1, name: "acme", tags: ["a", "b"] },
    { id: 2, name: "globex", tags: [] },
  ],
  "weird key": { nested: "value" },
} satisfies JsonValue;

describe("jsonTypeOf", () => {
  it.each([
    [null, "null"],
    [[], "array"],
    [{}, "object"],
    [1, "number"],
    [true, "boolean"],
    ["x", "string"],
  ])("types %s", (value, want) => {
    expect(jsonTypeOf(value as JsonValue)).toBe(want);
  });
});

describe("flattenJson", () => {
  it("produces one node per key and element, depth-first", () => {
    const { nodes, truncated } = flattenJson(doc);
    expect(truncated).toBe(false);

    const paths = nodes.map((n) => n.path);
    expect(paths[0]).toBe("$");
    expect(paths).toContain("$.total");
    expect(paths).toContain("$.results");
    expect(paths).toContain("$.results[0]");
    expect(paths).toContain("$.results[0].name");
    expect(paths).toContain("$.results[0].tags[1]");
  });

  it("bracket-quotes a key that is not an identifier", () => {
    const { nodes } = flattenJson(doc);
    expect(nodes.map((n) => n.path)).toContain('$["weird key"].nested');
  });

  it("records container child counts and parent links", () => {
    const { nodes } = flattenJson(doc);
    const results = nodes.find((n) => n.path === "$.results");
    expect(results?.container).toBe(true);
    expect(results?.childCount).toBe(2);
    expect(results?.parentPath).toBe("$");

    const name = nodes.find((n) => n.path === "$.results[0].name");
    expect(name?.container).toBe(false);
    expect(name?.value).toBe("acme");
    expect(name?.parentPath).toBe("$.results[0]");
  });

  it("stops at the node cap and says so", () => {
    const wide = { items: Array.from({ length: 500 }, (_, i) => ({ i })) };
    const { nodes, truncated } = flattenJson(wide as JsonValue, 50);
    expect(truncated).toBe(true);
    expect(nodes.length).toBeLessThanOrEqual(50);
  });
});

describe("visibleNodes", () => {
  it("hides every descendant of a collapsed container", () => {
    const { nodes } = flattenJson(doc);
    const visible = visibleNodes(nodes, new Set(["$.results"]));
    const paths = visible.map((n) => n.path);

    expect(paths).toContain("$.results");
    expect(paths).not.toContain("$.results[0]");
    expect(paths).not.toContain("$.results[0].name");
    // A sibling of the collapsed container is unaffected.
    expect(paths).toContain('$["weird key"]');
  });

  it("shows everything when nothing is collapsed", () => {
    const { nodes } = flattenJson(doc);
    expect(visibleNodes(nodes, new Set()).length).toBe(nodes.length);
  });

  it("leaves only the root visible when every container is collapsed", () => {
    const { nodes } = flattenJson(doc);
    const visible = visibleNodes(nodes, allContainerPaths(nodes));
    expect(visible.map((n) => n.path)).toEqual(["$"]);
  });
});

describe("defaultCollapsed", () => {
  it("opens the top level and collapses the rest", () => {
    const { nodes } = flattenJson(doc);
    const collapsed = defaultCollapsed(nodes);
    const visible = visibleNodes(nodes, collapsed).map((n) => n.path);

    expect(visible).toContain("$.results");
    expect(visible).toContain("$.total");
    expect(visible).not.toContain("$.results[0]");
  });
});

describe("searchNodes", () => {
  it("matches keys and scalar values, case-insensitively", () => {
    const { nodes } = flattenJson(doc);

    expect(searchNodes(nodes, "NAME").map((m) => m.path)).toEqual([
      "$.results[0].name",
      "$.results[1].name",
    ]);
    expect(searchNodes(nodes, "globex")).toEqual([{ path: "$.results[1].name", where: "value" }]);
  });

  it("matches numbers and booleans by their rendered text", () => {
    const { nodes } = flattenJson(doc);
    expect(searchNodes(nodes, "true").map((m) => m.path)).toContain("$.active");
  });

  it("returns nothing for an empty query", () => {
    const { nodes } = flattenJson(doc);
    expect(searchNodes(nodes, "   ")).toEqual([]);
  });
});

describe("ancestorPaths", () => {
  it("lists the containers between a node and the root", () => {
    const { nodes } = flattenJson(doc);
    expect(ancestorPaths(nodes, "$.results[0].tags[1]")).toEqual([
      "$.results[0].tags",
      "$.results[0]",
      "$.results",
      "$",
    ]);
  });

  it("returns nothing for the root", () => {
    const { nodes } = flattenJson(doc);
    expect(ancestorPaths(nodes, "$")).toEqual([]);
  });
});

describe("resolvePath and valueAtPath", () => {
  it("walks a generated path back to its value", () => {
    // Round-tripping is what makes copy-path useful: a path that cannot be
    // resolved is not a path anyone can paste anywhere.
    const { nodes } = flattenJson(doc);
    for (const node of nodes) {
      expect(resolvePath(doc, node.path)).not.toBeUndefined();
    }
  });

  it("resolves bracket-quoted keys and array indices", () => {
    expect(resolvePath(doc, '$["weird key"].nested')).toBe("value");
    expect(resolvePath(doc, "$.results[1].id")).toBe(2);
    expect(resolvePath(doc, "$")).toBe(doc);
  });

  it("returns undefined for a path that does not exist", () => {
    expect(resolvePath(doc, "$.nope.deeper")).toBeUndefined();
    expect(resolvePath(doc, "not-a-path")).toBeUndefined();
  });

  it("copies scalars raw and containers as pretty JSON", () => {
    expect(valueAtPath(doc, "$.results[0].name")).toBe("acme");
    expect(valueAtPath(doc, "$.total")).toBe("2");
    expect(valueAtPath(doc, "$.results[1]")).toBe(JSON.stringify(doc.results[1], null, 2));
    expect(valueAtPath(doc, "$.nope")).toBe("");
  });
});

describe("containerSummary", () => {
  it("counts children and pluralizes", () => {
    const { nodes } = flattenJson(doc);
    const results = nodes.find((n) => n.path === "$.results");
    const weird = nodes.find((n) => n.path === '$["weird key"]');

    expect(containerSummary(results!)).toBe("[ 2 items ]");
    expect(containerSummary(weird!)).toBe("{ 1 item }");
  });
});

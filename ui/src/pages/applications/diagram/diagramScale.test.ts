import { describe, expect, it } from "vitest";
import type { ApplicationDiagram, ApplicationDiagramNode } from "@/api/portal/hooks/applications";
import { warehouseFindings, warehouseReach } from "@/mocks/data/applicationWarehouse";
import {
  LARGE_LINKS,
  cellText,
  changeSummary,
  isLarge,
  reachOf,
  relatedTo,
  rowNote,
  tableLinkCount,
  tablesOf,
  unwrittenTables,
} from "./diagramScale";
import { largeLayout, linkPath } from "./largeLayout";

// The pure half of #2700: where the cutoff falls, who reads and writes what
// from both ends, what a row says, and where the large drawing puts a row.

function node(id: string, kind: ApplicationDiagramNode["kind"], extra: Partial<ApplicationDiagramNode> = {}): ApplicationDiagramNode {
  return { id, kind, label: id, group: kind === "table" ? "schema" : "application", part: kind === "script", computed: false, detail: [], ...extra };
}

function warehouse(): ApplicationDiagram {
  const reach = warehouseReach();
  const scripts = Array.from({ length: 20 }, (_, i) => node(`script:wh-${String(i + 1).padStart(2, "0")}`, "script"));
  return {
    version: "latest",
    nodes: [...scripts, node("source:stock-events", "source"), node("schema:acme-shop/wh", "schema", { group: "schema" }), ...reach.nodes],
    links: reach.links,
    groups: [],
    findings: warehouseFindings,
  };
}

// small is an application of n automations each writing its own table.
function small(n: number): ApplicationDiagram {
  const nodes: ApplicationDiagramNode[] = [];
  const links: ApplicationDiagram["links"] = [];
  for (let i = 0; i < n; i++) {
    nodes.push(node(`script:s${i}`, "script"), node(`table:t${i}`, "table"));
    links.push({ from: `script:s${i}`, to: `table:t${i}`, kind: "writes" });
  }
  return { version: "latest", nodes, links, groups: [], findings: [] };
}

describe("the cutoff", () => {
  it("draws up to LARGE_LINKS automation-to-table links one by one, and past it as a band", () => {
    expect(isLarge(small(LARGE_LINKS))).toBe(false);
    expect(isLarge(small(LARGE_LINKS + 1))).toBe(true);
    expect(isLarge(warehouse())).toBe(true);
  });

  it("counts only an automation reading or writing a table", () => {
    const d = small(2);
    d.nodes.push(node("asset:a", "asset"));
    d.links.push({ from: "script:s0", to: "asset:a", kind: "refreshes" }, { from: "asset:a", to: "script:s1", kind: "calls" });
    expect(tableLinkCount(d)).toBe(2);
  });
});

describe("reach", () => {
  it("indexes each automation's tables and each table's automations, reads and writes joined", () => {
    const d = warehouse();
    const r = reachOf(d);
    // Post refunds (wh-12) reads order, order_line, payment and refund_reason, and writes refund and order_status.
    const refunds = r.byScript.get("script:wh-12")!;
    expect(refunds.get("table:acme-shop/wh.refund")).toBe("writes");
    expect(refunds.get("table:acme-shop/wh.order_line")).toBe("reads");
    expect(r.byTable.get("table:acme-shop/wh.refund")!.get("script:wh-12")).toBe("writes");
    // Sync stock (wh-07) writes stock_level.
    expect(r.byScript.get("script:wh-07")!.get("table:acme-shop/wh.stock_level")).toBe("writes");
  });

  it("joins a read and a write of one table into both, and leaves a removed link out", () => {
    const d = small(1);
    d.links.push({ from: "script:s0", to: "table:t0", kind: "reads" }, { from: "script:s0", to: "table:t0", kind: "reads", change: "removed" });
    expect(reachOf(d).byScript.get("script:s0")!.get("table:t0")).toBe("both");
    expect(cellText("both")).toBe("R W");
    expect(cellText("writes")).toBe("W");
    expect(cellText(undefined)).toBe("");
  });

  it("puts the schema's tables first and leaves a table named only when it runs out of the columns", () => {
    const tables = tablesOf(warehouse());
    expect(tables).toHaveLength(20);
    expect(tables.every((t) => !t.computed)).toBe(true);
  });
});

describe("what a row says", () => {
  it("names its counts, a table no part writes, and how the selection reaches it", () => {
    const d = warehouse();
    const reach = reachOf(d);
    const unwritten = unwrittenTables(d.findings);
    const byId = new Map(d.nodes.map((n) => [n.id, n]));
    const facts = { reach, related: null, unwritten };
    expect(rowNote(byId.get("script:wh-12")!, facts)).toEqual({ text: "reads 4 · writes 3", tone: "plain" });
    expect(rowNote(byId.get("table:acme-shop/wh.refund_reason")!, facts)).toEqual({ text: "no writer · read by 1", tone: "warn" });
    expect(rowNote(byId.get("source:stock-events")!, facts)).toBeNull();

    const selected = { ...facts, related: relatedTo(d, reach, "script:wh-12") };
    expect(rowNote(byId.get("table:acme-shop/wh.refund")!, selected)).toEqual({ text: "writes", tone: "writes" });
    expect(rowNote(byId.get("table:acme-shop/wh.payment")!, selected)).toEqual({ text: "reads", tone: "reads" });
    expect(selected.related!.lit.has("table:acme-shop/wh.customer")).toBe(false);
  });

  it("says how a compared node changed", () => {
    const n = node("script:s0", "script", { change: "changed", version: 4, was_version: 3 });
    const facts = { reach: reachOf(small(1)), related: null, unwritten: new Set<string>() };
    expect(rowNote(n, facts)).toEqual({ text: "v3 → v4", tone: "changed" });
    expect(rowNote({ ...n, change: "added" }, facts)).toEqual({ text: "added", tone: "added" });
  });

  it("summarizes what a comparison marks", () => {
    const d = small(2);
    d.nodes[0] = { ...d.nodes[0]!, change: "added" };
    d.nodes[2] = { ...d.nodes[2]!, change: "removed" };
    const s = changeSummary(d);
    expect(s.added.map((n) => n.id)).toEqual(["script:s0"]);
    expect(s.removed.map((n) => n.id)).toEqual(["script:s1"]);
    expect(s.changed).toEqual([]);
  });
});

describe("the large layout", () => {
  it("puts starters, automations, other reaches and tables in four columns", () => {
    const layout = largeLayout(warehouse(), "acme-shop/wh");
    expect(layout.rows.get("source:stock-events")!.column).toBe(0);
    expect(layout.rows.get("script:wh-01")!.column).toBe(1);
    expect(layout.rows.get("computed:script:wh-12:acme-shop")!.column).toBe(2);
    expect(layout.rows.get("table:acme-shop/wh.customer")!.column).toBe(3);
    expect(layout.rows.has("schema:acme-shop/wh")).toBe(false);
    expect(layout.boxes.map((b) => `${b.label} ${b.count}`)).toEqual(["Automations 20", "Tables 20"]);
    expect(layout.band.x2).toBeGreaterThan(layout.band.x1);
  });

  it("routes a link out of the side facing its target", () => {
    const layout = largeLayout(warehouse(), "");
    const from = layout.rows.get("script:wh-01")!;
    const to = layout.rows.get("table:acme-shop/wh.order")!;
    expect(linkPath(from, to, 0)).toMatch(new RegExp(`^M${from.x + from.width} `));
    expect(linkPath(to, from, 0)).toMatch(new RegExp(`^M${to.x} `));
  });
});

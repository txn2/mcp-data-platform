import type {
  ApplicationDetail,
  ApplicationDiagram,
  ApplicationDiagramLink,
  ApplicationDiagramNode,
  ApplicationPartKind,
} from "@/api/portal/hooks/applications";

// An application drawn (#2540), as GET /portal/applications/:app/diagram
// answers. Every application's parts are drawn from its record; shop also
// reaches its schema's tables, a library outside it, a channel, and an output
// its report names only when it runs. What shop reaches moves with the
// versions drawn, so a comparison has something to mark: release 1 notifies
// no channel, and script-001 v5 exports a named weekly summary where v4's
// output is named when it runs, and loads report-format v3 where v4 loads v2.

const SHOP_REPORT = "script:script-001";
const SHOP_API = "script:script-002";
const SHOP_DASHBOARD = "asset:ast-001";
const ORDERS = "table:acme-shop/shop.orders";
const REFUNDS = "table:acme-shop/shop.refunds";

const NODE_KIND: Record<ApplicationPartKind, ApplicationDiagramNode["kind"]> = {
  script: "script",
  asset: "asset",
  source: "source",
  schema: "schema",
  site: "asset",
};

// partDetail is what a part's card adds: a script's schedule and endpoint.
function partDetail(d: ApplicationDetail, kind: ApplicationPartKind, id: string): string[] {
  const view = d.parts.find((p) => p.kind === kind && p.id === id);
  const out: string[] = [];
  if (view?.schedule) out.push(`schedule ${view.schedule.cron_spec} ${view.schedule.timezone}`);
  if (kind === "script" && id === "script-002") out.push("endpoint /endpoints/shop-orders/");
  return out;
}

function partNodes(d: ApplicationDetail, pins: Map<string, number>): ApplicationDiagramNode[] {
  return d.application.parts.map((p) => {
    const view = d.parts.find((v) => v.kind === p.kind && v.id === p.id);
    const version = pins.get(`${p.kind}/${p.id}`);
    return {
      id: `${p.kind}:${p.id}`,
      kind: NODE_KIND[p.kind],
      label: view?.name ?? p.id,
      group: p.kind === "schema" ? "schema" : "application",
      part: true,
      part_id: p.id,
      ...(version ? { version } : {}),
      computed: false,
      detail: partDetail(d, p.kind, p.id),
    };
  });
}

function outside(id: string, kind: ApplicationDiagramNode["kind"], label: string, extra: Partial<ApplicationDiagramNode> = {}) {
  return { id, kind, label, group: "outside", part: false, computed: false, detail: [], ...extra };
}

function table(id: string, label: string): ApplicationDiagramNode {
  return { id, kind: "table", label, group: "schema", part: false, computed: false, detail: [] };
}

// shopReach is what shop's parts reach at the versions drawn.
function shopReach(version: string, pins: Map<string, number>) {
  const report = pins.get("script/script-001") ?? 0;
  const nodes: ApplicationDiagramNode[] = [
    table(ORDERS, "orders"),
    table(REFUNDS, "refunds"),
    outside("library:report-format", "library", "report-format"),
  ];
  const links: ApplicationDiagramLink[] = [
    { from: "source:billing-events", to: SHOP_API, kind: "event" },
    { from: SHOP_API, to: ORDERS, kind: "writes" },
    { from: SHOP_REPORT, to: ORDERS, kind: "reads" },
    { from: SHOP_REPORT, to: REFUNDS, kind: "reads" },
    { from: SHOP_REPORT, to: SHOP_DASHBOARD, kind: "refreshes" },
    { from: SHOP_REPORT, to: "library:report-format", kind: "loads", detail: report >= 5 ? "v3" : "v2" },
    { from: SHOP_DASHBOARD, to: SHOP_API, kind: "calls", detail: "shop-orders" },
  ];
  if (version !== "1") {
    nodes.push(outside("channel:ops-alerts", "channel", "ops-alerts"));
    links.push({ from: SHOP_REPORT, to: "channel:ops-alerts", kind: "notifies" });
  }
  if (report >= 5) {
    nodes.push(outside("output:script-001:weekly-summary", "output", "weekly-summary"));
    links.push({ from: SHOP_REPORT, to: "output:script-001:weekly-summary", kind: "exports" });
  } else {
    const id = `computed:${SHOP_REPORT}:out0`;
    nodes.push(outside(id, "output", "output named when it runs", { computed: true }));
    links.push({ from: SHOP_REPORT, to: id, kind: "exports" });
  }
  return { nodes, links };
}

function draw(d: ApplicationDetail, version: string, pins: Map<string, number>): ApplicationDiagram {
  const a = d.application;
  const nodes = partNodes(d, pins);
  const links: ApplicationDiagramLink[] = [];
  const groups: ApplicationDiagram["groups"] = [
    { id: "application", label: a.name },
    { id: "outside", label: "Outside the application" },
  ];
  if (a.schema) groups.push({ id: "schema", label: `${a.connection}/${a.schema}`, parent: "application" });
  if (a.name === "shop") {
    const reach = shopReach(version, pins);
    nodes.push(...reach.nodes);
    links.push(...reach.links);
  }
  return { version, nodes, links, groups, findings: d.findings ?? [] };
}

const linkKey = (l: ApplicationDiagramLink) => `${l.from}\u0000${l.to}\u0000${l.kind}`;

// compare marks next against prev as the server's Compare does.
function compare(next: ApplicationDiagram, prev: ApplicationDiagram): ApplicationDiagram {
  const before = new Map(prev.nodes.map((n) => [n.id, n]));
  const nodes: ApplicationDiagramNode[] = next.nodes.map((n) => {
    const was = before.get(n.id);
    if (!was) return { ...n, change: "added" };
    if ((was.version ?? 0) !== (n.version ?? 0)) return { ...n, change: "changed", was_version: was.version ?? 0 };
    return { ...n, change: "unchanged" };
  });
  const seen = new Set(next.nodes.map((n) => n.id));
  for (const n of prev.nodes) if (!seen.has(n.id)) nodes.push({ ...n, change: "removed" });
  const had = new Map(prev.links.map((l) => [linkKey(l), l]));
  const has = new Set(next.links.map(linkKey));
  const links: ApplicationDiagramLink[] = next.links.map((l) => {
    const was = had.get(linkKey(l));
    if (!was) return { ...l, change: "added" };
    if ((was.detail ?? "") !== (l.detail ?? "")) {
      return { ...l, change: "changed", ...(was.detail ? { was_detail: was.detail } : {}) };
    }
    return { ...l, change: "unchanged" };
  });
  for (const l of prev.links) if (!has.has(linkKey(l))) links.push({ ...l, change: "removed" });
  return { ...next, compared_with: prev.version, nodes, links };
}

/** shopDiagram is an application drawn at version, marked against compareWith when one is given. */
export function shopDiagram(
  d: ApplicationDetail,
  version: string,
  pins: Map<string, number>,
  compareWith: string,
  comparePins: Map<string, number> | null,
): ApplicationDiagram {
  const next = draw(d, version, pins);
  if (!compareWith || !comparePins) return next;
  return compare(next, draw(d, compareWith, comparePins));
}

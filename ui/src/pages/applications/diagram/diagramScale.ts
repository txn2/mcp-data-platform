import type {
  ApplicationDiagram,
  ApplicationDiagramLink,
  ApplicationDiagramNode,
  ApplicationFinding,
} from "@/api/portal/hooks/applications";

// The pure half of a large application's diagram (#2700). Past LARGE_LINKS
// links between automations and tables the drawing stops routing every one of
// them: it draws one band between the two boxes and the links of what is
// selected, and offers the same graph as a table. Everything that view reads
// about who reads and writes what is worked out here.

/** LARGE_LINKS is the most automation-to-table links drawn one by one; past it the diagram is large. */
export const LARGE_LINKS = 12;

/** isTableLink reports whether a link is an automation reading or writing a table. */
export function isTableLink(l: ApplicationDiagramLink, byId: Map<string, ApplicationDiagramNode>): boolean {
  if (l.kind !== "reads" && l.kind !== "writes") return false;
  const from = byId.get(l.from);
  const to = byId.get(l.to);
  return from?.kind === "script" && to?.kind === "table";
}

/** tableLinkCount is how many automation-to-table links a diagram holds. */
export function tableLinkCount(d: ApplicationDiagram): number {
  const byId = new Map(d.nodes.map((n) => [n.id, n]));
  return d.links.filter((l) => isTableLink(l, byId)).length;
}

/** isLarge reports whether a diagram draws its automation-to-table links as a band. */
export function isLarge(d: ApplicationDiagram): boolean {
  return tableLinkCount(d) > LARGE_LINKS;
}

/** Access is what one automation does to one table. */
export type Access = "reads" | "writes" | "both";

/** Reach is who reads and writes what, from both ends. */
export interface Reach {
  /** byScript holds, per automation, each table it reaches and how. */
  byScript: Map<string, Map<string, Access>>;
  /** byTable holds, per table, each automation that reaches it and how. */
  byTable: Map<string, Map<string, Access>>;
}

function join(prev: Access | undefined, kind: "reads" | "writes"): Access {
  if (!prev || prev === kind) return kind;
  return "both";
}

function put(m: Map<string, Map<string, Access>>, key: string, other: string, kind: "reads" | "writes") {
  const inner = m.get(key) ?? new Map<string, Access>();
  inner.set(other, join(inner.get(other), kind));
  m.set(key, inner);
}

/** reachOf is every automation-to-table link of a diagram, indexed from both ends. Removed links are left out. */
export function reachOf(d: ApplicationDiagram): Reach {
  const byId = new Map(d.nodes.map((n) => [n.id, n]));
  const reach: Reach = { byScript: new Map(), byTable: new Map() };
  for (const l of d.links) {
    if (!isTableLink(l, byId) || l.change === "removed") continue;
    const kind = l.kind as "reads" | "writes";
    put(reach.byScript, l.from, l.to, kind);
    put(reach.byTable, l.to, l.from, kind);
  }
  return reach;
}

/** writes reports whether an access writes; reads whether it reads. */
export const writes = (a: Access | undefined) => a === "writes" || a === "both";
export const reads = (a: Access | undefined) => a === "reads" || a === "both";

/** count is how many of a node's accesses pass a test. */
export function count(m: Map<string, Access> | undefined, test: (a: Access) => boolean): number {
  let n = 0;
  for (const a of m?.values() ?? []) if (test(a)) n++;
  return n;
}

/** accessLabel is how a cell or a tag names an access. */
export function accessLabel(a: Access | undefined): string {
  if (a === "both") return "reads, writes";
  return a ?? "";
}

/** cellText is the Table view's cell for an access: R, W, or R W. */
export function cellText(a: Access | undefined): string {
  if (a === "both") return "R W";
  if (a === "writes") return "W";
  if (a === "reads") return "R";
  return "";
}

/** scriptsOf is the diagram's automations, in the order the server sends them. */
export function scriptsOf(d: ApplicationDiagram): ApplicationDiagramNode[] {
  return d.nodes.filter((n) => n.kind === "script");
}

/** isSchemaTable reports whether a node is a table of the application's schema. */
export function isSchemaTable(n: ApplicationDiagramNode): boolean {
  return n.kind === "table" && n.group === "schema" && !n.computed;
}

/**
 * tablesOf is the tables a Table view has a column for: the schema's first,
 * then those outside it. A table named only when it runs is no column: it is
 * no one table.
 */
export function tablesOf(d: ApplicationDiagram): ApplicationDiagramNode[] {
  const tables = d.nodes.filter((n) => n.kind === "table" && !n.computed);
  return [...tables.filter(isSchemaTable), ...tables.filter((n) => !isSchemaTable(n))];
}

/** unwrittenTables is each table a finding says no part is known to write. */
export function unwrittenTables(findings: ApplicationFinding[]): Set<string> {
  return new Set(findings.filter((f) => f.kind === "unwritten_table").map((f) => f.node));
}

/** Related is how a node stands to what is selected, for the drawing's dimming and tags. */
export interface Related {
  /** lit holds every node the selection reaches, the selection included. */
  lit: Set<string>;
  /** access names how each automation or table the selection reaches is reached. */
  access: Map<string, Access>;
}

/** relatedTo is what a selection lights: its links' other ends, and how each table or automation is reached. */
export function relatedTo(d: ApplicationDiagram, reach: Reach, selected: string | null): Related | null {
  if (!selected) return null;
  const lit = new Set<string>([selected]);
  for (const l of d.links) {
    if (l.from === selected) lit.add(l.to);
    if (l.to === selected) lit.add(l.from);
  }
  const access = new Map<string, Access>([
    ...(reach.byScript.get(selected) ?? new Map<string, Access>()),
    ...(reach.byTable.get(selected) ?? new Map<string, Access>()),
  ]);
  return { lit, access };
}

/** ChangeSummary is what a comparison found, for the side column. */
export interface ChangeSummary {
  added: ApplicationDiagramNode[];
  changed: ApplicationDiagramNode[];
  removed: ApplicationDiagramNode[];
}

/** changeSummary is the parts and tables a comparison marks, by how. */
export function changeSummary(d: ApplicationDiagram): ChangeSummary {
  const of = (c: string) => d.nodes.filter((n) => n.change === c && n.kind !== "schema");
  return { added: of("added"), changed: of("changed"), removed: of("removed") };
}

/** View is how a large application is shown: drawn, or as a table. */
export type DiagramView = "drawing" | "table";

/** Grouping is how the phone's Table view lists the graph. */
export type Grouping = "script" | "table";

/** RowTone is how a row's note is set: plain, a tag for how the selection reaches it, a warning, or a change. */
export type RowTone = "plain" | "writes" | "reads" | "warn" | "added" | "removed" | "changed";

/** RowNote is what a row says at its right edge. */
export interface RowNote {
  text: string;
  tone: RowTone;
}

/** RowFacts is what a row's note is worked out from. */
export interface RowFacts {
  reach: Reach;
  related: Related | null;
  unwritten: Set<string>;
}

// changeNote is a compared node's note, none when unchanged.
function changeNote(n: ApplicationDiagramNode): RowNote | null {
  if (n.change === "added" || n.change === "removed") return { text: n.change, tone: n.change };
  if (n.change === "changed") return { text: `v${n.was_version ?? 0} → v${n.version ?? 0}`, tone: "changed" };
  return null;
}

// countNote is an automation's or a table's counts.
function countNote(n: ApplicationDiagramNode, f: RowFacts): RowNote {
  if (n.kind === "script") {
    const m = f.reach.byScript.get(n.id);
    return { text: `reads ${count(m, reads)} · writes ${count(m, writes)}`, tone: "plain" };
  }
  const m = f.reach.byTable.get(n.id);
  const readBy = count(m, reads);
  if (f.unwritten.has(n.id)) return { text: `no writer · read by ${readBy}`, tone: "warn" };
  return { text: `written by ${count(m, writes)} · read by ${readBy}`, tone: "plain" };
}

/**
 * rowNote is what a row of the large drawing says: with a selection, how the
 * selection reaches it; comparing, how it changed; else its counts. Rows other
 * than automations and tables say nothing beyond a change.
 */
export function rowNote(n: ApplicationDiagramNode, f: RowFacts): RowNote | null {
  const access = f.related?.access.get(n.id);
  if (access) return { text: accessLabel(access), tone: writes(access) ? "writes" : "reads" };
  const changed = changeNote(n);
  if (changed) return changed;
  if (n.kind === "script" || (n.kind === "table" && !n.computed)) return countNote(n, f);
  return null;
}

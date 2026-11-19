import type { ApplicationDiagram, ApplicationDiagramLink, ApplicationDiagramNode } from "@/api/portal/hooks/applications";

// Where a large application's drawing puts each part (#2700): four columns
// read left to right as the work flows. What starts automations (webhook
// sources, forms); the automations, in their box; what they reach other than
// tables (assets, channels, outputs, libraries, a table named only when it
// runs); and the tables, the schema's in its box first. Rows, not routed
// cards: at twenty automations and twenty tables a person reads names in a
// list, and the links worth drawing are the ones the selection picks.

export const ROW_HEIGHT = 24;
const PITCH = 28;
const TOP = 56;
const BOX_TOP = 16;
const BOX_PAD = 12;

export interface Column {
  x: number;
  width: number;
}

// The four columns, left to right.
export const COLUMNS: Column[] = [
  { x: 16, width: 160 },
  { x: 200, width: 260 },
  { x: 508, width: 170 },
  { x: 720, width: 260 },
];

export const LARGE_WIDTH = 996;

export interface PlacedRow {
  node: ApplicationDiagramNode;
  column: number;
  x: number;
  y: number;
  width: number;
}

export interface Box {
  label: string;
  /** detail is the box's name after its label: the schema's name. */
  detail?: string;
  count: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface LargeLayout {
  width: number;
  height: number;
  rows: Map<string, PlacedRow>;
  boxes: Box[];
  /** band is where the one band between the automations and the tables is drawn. */
  band: { x1: number; x2: number; y: number };
}

// columnOf sorts a node into its column; -1 for one drawn as a box heading.
function columnOf(n: ApplicationDiagramNode, starts: Set<string>): number {
  if (n.kind === "schema") return -1;
  if (n.kind === "script") return 1;
  if (n.kind === "table" && !n.computed) return 3;
  return starts.has(n.id) ? 0 : 2;
}

// starters is every node that only starts automations: it links to one and
// nothing links to it from one.
function starters(d: ApplicationDiagram, byId: Map<string, ApplicationDiagramNode>): Set<string> {
  const toScript = new Set<string>();
  const fromScript = new Set<string>();
  for (const l of d.links) {
    if (byId.get(l.to)?.kind === "script") toScript.add(l.from);
    if (byId.get(l.from)?.kind === "script") fromScript.add(l.to);
  }
  return new Set([...toScript].filter((id) => !fromScript.has(id)));
}

/** largeLayout places every row of a large diagram. */
export function largeLayout(d: ApplicationDiagram, schemaLabel: string): LargeLayout {
  const byId = new Map(d.nodes.map((n) => [n.id, n]));
  const starts = starters(d, byId);
  const columns: ApplicationDiagramNode[][] = [[], [], [], []];
  for (const n of d.nodes) {
    const c = columnOf(n, starts);
    if (c >= 0) columns[c]!.push(n);
  }
  // The schema's tables first, then those outside it.
  columns[3]!.sort((a, b) => Number(b.group === "schema") - Number(a.group === "schema"));
  const rows = new Map<string, PlacedRow>();
  columns.forEach((nodes, column) => {
    const col = COLUMNS[column]!;
    nodes.forEach((node, i) => rows.set(node.id, { node, column, x: col.x, y: TOP + i * PITCH, width: col.width }));
  });
  const longest = Math.max(1, columns[1]!.length, columns[3]!.length);
  const boxHeight = TOP - BOX_TOP + longest * PITCH + BOX_PAD;
  const box = (column: number, label: string, count: number, detail?: string): Box => ({
    label,
    detail,
    count,
    x: COLUMNS[column]!.x - BOX_PAD,
    y: BOX_TOP,
    width: COLUMNS[column]!.width + 2 * BOX_PAD,
    height: boxHeight,
  });
  const height = Math.max(BOX_TOP + boxHeight, TOP + Math.max(columns[0]!.length, columns[2]!.length) * PITCH) + BOX_TOP;
  return {
    width: LARGE_WIDTH,
    height,
    rows,
    boxes: [box(1, "Automations", columns[1]!.length), box(3, "Tables", columns[3]!.length, schemaLabel)],
    band: { x1: COLUMNS[1]!.x + COLUMNS[1]!.width + BOX_PAD, x2: COLUMNS[3]!.x - BOX_PAD, y: BOX_TOP + boxHeight / 2 },
  };
}

/**
 * linkPath is a link drawn between two rows: out of the side facing the
 * other row and in at the other's facing side, turning once in the gap before
 * the target. lane staggers the turn so links into one table do not overlap.
 */
export function linkPath(from: PlacedRow, to: PlacedRow, lane: number): string {
  const mid = ROW_HEIGHT / 2;
  const y1 = from.y + mid;
  const y2 = to.y + mid;
  const offset = 8 + (lane % 6) * 4;
  if (from.column <= to.column) {
    const x1 = from.x + from.width;
    const x2 = to.column === from.column ? to.x + to.width : to.x;
    const turn = to.column === from.column ? x1 + offset : x2 - offset;
    return `M${x1} ${y1} H${turn} V${y2} H${x2}`;
  }
  const x1 = from.x;
  const x2 = to.x + to.width;
  return `M${x1} ${y1} H${x2 + offset} V${y2} H${x2}`;
}

/** lanes numbers each link by its place among the links into the same row. */
export function lanes(links: ApplicationDiagramLink[]): Map<ApplicationDiagramLink, number> {
  const seen = new Map<string, number>();
  const out = new Map<ApplicationDiagramLink, number>();
  for (const l of links) {
    const n = seen.get(l.to) ?? 0;
    seen.set(l.to, n + 1);
    out.set(l, n);
  }
  return out;
}

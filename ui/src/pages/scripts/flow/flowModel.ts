import type {
  FlowChange,
  FlowEdge,
  FlowNode,
  FlowNodeRun,
  FlowOtherCall,
  FlowRole,
  ScriptFlow,
} from "@/api/portal/hooks/scriptFlow";

// The pure half of the Flow tab (#1906): how a step's card is worded and sized,
// what a selection lights up, and which cards a range of source lines
// produced. The layout and the drawing read everything they need from here, so
// the rules a reader sees are testable without a browser.

// ROLE_COLOR is the bar color for each role, from the portal's chart palette so
// it follows the light and dark themes.
export const ROLE_COLOR: Record<FlowRole, string> = {
  input: "hsl(var(--chart-5))",
  reads: "hsl(var(--chart-1))",
  writes: "hsl(var(--chart-2))",
  output: "hsl(var(--chart-3))",
};

export const ROLE_LABEL: Record<FlowRole, string> = {
  input: "Input",
  reads: "Reads",
  writes: "Writes",
  output: "Output",
};

// CHANGE_COLOR and CHANGE_LABEL mark a node of a compared graph (#1908).
export const CHANGE_COLOR: Record<FlowChange, string> = {
  added: "hsl(var(--chart-3))",
  changed: "hsl(var(--chart-4))",
  removed: "hsl(var(--destructive))",
};

export const CHANGE_LABEL: Record<FlowChange, string> = {
  added: "added",
  changed: "changed",
  removed: "removed",
};

// changeCounts is how many nodes a compared graph marks each way.
export function changeCounts(graph: ScriptFlow): Record<FlowChange, number> {
  const out: Record<FlowChange, number> = { added: 0, changed: 0, removed: 0 };
  for (const n of graph.nodes) if (n.change) out[n.change]++;
  return out;
}

// SELECT_COLOR is the one accent a selection is drawn in.
export const SELECT_COLOR = "hsl(var(--chart-4))";

// Card geometry, in CSS pixels at zoom 1.
export const CARD_WIDTH = 250;
const CARD_HEAD = 34;
const LINE_HEIGHT = 17;
const CHIP_ROW = 24;
const CARD_PAD = 6;
export const MAX_DETAIL_LINES = 4;

// Fonts the card text is measured and drawn in.
export const FONT_TITLE = "600 13px ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif";
export const FONT_BODY = "12px ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif";
export const FONT_MONO = "11.5px ui-monospace, SF Mono, Menlo, Consolas, monospace";
export const FONT_CHIP = "11px ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif";

export interface CardLine {
  text: string;
  font: string;
  mono: boolean;
  muted: boolean;
}

export interface CardText {
  lines: CardLine[];
  chips: string[];
}

// purposeShort drops the "Script name: " prefix authors put on every purpose
// in a script, which the card is already inside.
export function purposeShort(purpose: string | undefined): string {
  if (!purpose) return "";
  const i = purpose.indexOf(": ");
  return i > 0 && i <= 48 ? purpose.slice(i + 2) : purpose;
}

// cardText is what a card says below its title: the author's purpose, the
// operation or path, the tables and keys it touches, and the chips for the
// helper it runs through and the loop it repeats in.
// formatDuration writes a run's time the way a person reads it.
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const m = Math.floor(ms / 60_000);
  return `${m}m ${Math.round((ms % 60_000) / 1000)}s`;
}

// runChip is what one run did at a card, as its chip: the calls, how many of
// them failed (#1933), and their time; or the rows it wrote; or that it ran.
export function runChip(stat: FlowNodeRun | undefined): string | null {
  if (!stat) return null;
  if (stat.calls > 0) {
    const failed = stat.failed_calls > 0 ? ` · ${stat.failed_calls} failed` : "";
    return `${stat.calls} call${stat.calls === 1 ? "" : "s"}${failed} · ${formatDuration(stat.duration_ms)}`;
  }
  if (stat.outputs > 0) return `${stat.rows} row${stat.rows === 1 ? "" : "s"}`;
  return stat.reached ? "ran" : null;
}

export function cardText(n: FlowNode, stat?: FlowNodeRun): CardText {
  const lines: CardLine[] = [];
  const purpose = purposeShort(n.purpose);
  if (purpose) lines.push({ text: purpose, font: FONT_BODY, mono: false, muted: false });
  const plainSubtitle = n.kind === "state" || n.kind === "save_state";
  if (n.subtitle) {
    lines.push({
      text: n.subtitle,
      font: plainSubtitle ? FONT_BODY : FONT_MONO,
      mono: !plainSubtitle,
      muted: plainSubtitle,
    });
  }
  for (const d of n.detail.slice(0, MAX_DETAIL_LINES)) {
    lines.push({ text: d, font: FONT_MONO, mono: true, muted: true });
  }
  const chips: string[] = [];
  const ran = runChip(stat);
  if (ran) chips.push(ran);
  if (n.change) chips.push(CHANGE_LABEL[n.change]);
  if (n.wrapper) chips.push(`${n.wrapper}()`);
  const loop = n.loops[n.loops.length - 1];
  if (loop) chips.push(`↻ ${loop.replace(/^for /, "")}`);
  return { lines, chips };
}

export function cardHeight(n: FlowNode, stat?: FlowNodeRun): number {
  const { lines, chips } = cardText(n, stat);
  return CARD_HEAD + lines.length * LINE_HEIGHT + (chips.length ? CHIP_ROW : 0) + CARD_PAD;
}

// Text measurement. A canvas measures exactly; where there is none (a test
// runner's DOM has no canvas) an average glyph width stands in, which only
// changes where a long line is cut.
let measureCtx: CanvasRenderingContext2D | null | undefined;
function hasCanvas(): boolean {
  return typeof document !== "undefined" && !/jsdom/i.test(navigator.userAgent);
}
export function textWidth(text: string, font: string): number {
  if (measureCtx === undefined) {
    measureCtx = hasCanvas() ? document.createElement("canvas").getContext("2d") : null;
  }
  if (measureCtx) {
    measureCtx.font = font;
    return measureCtx.measureText(text).width;
  }
  const px = Number(/(\d+(?:\.\d+)?)px/.exec(font)?.[1] ?? 12);
  return text.length * px * 0.58;
}

// fitText cuts text to the width it is drawn in, marking the cut.
export function fitText(text: string, maxWidth: number, font: string): string {
  if (textWidth(text, font) <= maxWidth) return text;
  let lo = 0;
  let hi = text.length;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (textWidth(text.slice(0, mid) + "…", font) <= maxWidth) lo = mid;
    else hi = mid - 1;
  }
  return text.slice(0, lo) + "…";
}

// roundedPath draws an orthogonal route with rounded corners.
export function roundedPath(points: Array<{ x: number; y: number }>, radius = 8): string {
  const [start, ...rest] = points;
  if (!start) return "";
  let d = `M${start.x},${start.y}`;
  let prev = start;
  rest.forEach((c, i) => {
    const next = rest[i + 1];
    if (!next) {
      d += ` L${c.x},${c.y}`;
      return;
    }
    const r = Math.min(
      radius,
      Math.hypot(c.x - prev.x, c.y - prev.y) / 2,
      Math.hypot(next.x - c.x, next.y - c.y) / 2,
    );
    const inX = Math.sign(c.x - prev.x);
    const inY = Math.sign(c.y - prev.y);
    const outX = Math.sign(next.x - c.x);
    const outY = Math.sign(next.y - c.y);
    d += ` L${c.x - inX * r},${c.y - inY * r} Q${c.x},${c.y} ${c.x + outX * r},${c.y + outY * r}`;
    prev = c;
  });
  return d;
}

// Selection is what the reader picked: a card, a function box, or a
// parameter; in the Structure view (#1972) also a control node (a decision,
// an exit, a folded helper) or a loop or helper box; with a run drawn, one of
// its calls, by its place in the run's timeline (#1982).
export type Selection =
  | { kind: "node"; id: string }
  | { kind: "call"; index: number }
  | { kind: "group"; id: string }
  | { kind: "param"; name: string }
  | { kind: "struct"; id: string }
  | { kind: "sbox"; id: string }
  | null;

// LineRange is a span of source lines, both ends included.
export interface LineRange {
  from: number;
  to: number;
}

// Lit is what the diagram lights up: the cards, and the edges between them.
export interface Lit {
  nodes: Set<string>;
  edges: Set<FlowEdge>;
  // dimOthers is true when the selection defines a set the rest is not in: a
  // parameter's reach, or the cards some source lines produced.
  dimOthers: boolean;
}

// lit computes what a selection, or a range of source lines, lights up. A
// selection on the diagram wins over lines selected in Source.
export function lit(graph: ScriptFlow, sel: Selection, lines: LineRange | null): Lit {
  if (sel?.kind === "node") return litNode(graph, sel.id);
  if (sel?.kind === "param") return litParam(graph, sel.name);
  if (!sel && lines) {
    const nodes = new Set(nodesForLines(graph, lines));
    return { nodes, edges: new Set(), dimOthers: nodes.size > 0 };
  }
  return { nodes: new Set(), edges: new Set(), dimOthers: false };
}

// litNode is a card and the edges into and out of it.
function litNode(graph: ScriptFlow, id: string): Lit {
  const edges = new Set(graph.edges.filter((e) => e.from === id || e.to === id));
  return { nodes: new Set([id]), edges, dimOthers: false };
}

// litParam is exactly the steps a parameter's value reaches.
function litParam(graph: ScriptFlow, name: string): Lit {
  const p = graph.params.find((x) => x.name === name);
  return { nodes: new Set(p?.reaches ?? []), edges: new Set(), dimOthers: true };
}

// nodeLines is the source a card came from: the platform call, and the line a
// folded wrapper is called from.
export function nodeLines(n: FlowNode): LineRange[] {
  if (!n.line) return [];
  const out: LineRange[] = [{ from: n.line, to: Math.max(n.line, n.end_line || n.line) }];
  if (n.site) out.push({ from: n.site, to: n.site });
  return out;
}

// nodesForLines is the cards some source lines produced: every card whose call,
// or whose folded wrapper's call site, falls in the range.
export function nodesForLines(graph: ScriptFlow, range: LineRange): string[] {
  const hit = (r: LineRange) => r.from <= range.to && r.to >= range.from;
  return graph.nodes.filter((n) => nodeLines(n).some(hit)).map((n) => n.id);
}

// nodeLabel names a card in a list: its title, and its subtitle when it has one.
export function nodeLabel(graph: ScriptFlow, id: string): string {
  const n = graph.nodes.find((x) => x.id === id);
  if (!n) return id;
  return n.subtitle ? `${n.title} · ${n.subtitle}` : n.title;
}

// excerpt is a few numbered source lines around a line.
export function excerpt(source: string, line: number, before: number, after: number): string {
  if (!source || !line) return "";
  const lines = source.split("\n");
  const from = Math.max(1, line - before);
  const to = Math.min(lines.length, line + after);
  const width = String(to).length;
  const out: string[] = [];
  for (let i = from; i <= to; i++) out.push(`${String(i).padStart(width)}  ${lines[i - 1]}`);
  return out.join("\n");
}

// CallGroup is the calls of one tool a run made that no card made (#1972),
// counted rather than listed one per line.
export interface CallGroup {
  tool: string;
  succeeded: number;
  failed: number;
  // errors counts each failure message, most frequent first.
  errors: { error: string; count: number }[];
  medianMS: number;
  totalMS: number;
  calls: FlowOtherCall[];
}

// groupCalls groups calls by tool, with the tools that had failures first.
export function groupCalls(calls: FlowOtherCall[]): CallGroup[] {
  const byTool = new Map<string, FlowOtherCall[]>();
  for (const c of calls) {
    const list = byTool.get(c.tool) ?? [];
    list.push(c);
    byTool.set(c.tool, list);
  }
  const out: CallGroup[] = [];
  for (const [tool, list] of byTool) {
    const errors = new Map<string, number>();
    for (const c of list) {
      if (!c.success) errors.set(c.error || "no message", (errors.get(c.error || "no message") ?? 0) + 1);
    }
    const durations = list.map((c) => c.duration_ms).sort((a, b) => a - b);
    const failed = list.filter((c) => !c.success).length;
    out.push({
      tool,
      succeeded: list.length - failed,
      failed,
      errors: [...errors].map(([error, count]) => ({ error, count })).sort((a, b) => b.count - a.count),
      medianMS: durations[Math.floor((durations.length - 1) / 2)] ?? 0,
      totalMS: durations.reduce((s, d) => s + d, 0),
      calls: list,
    });
  }
  return out.sort((a, b) => b.failed - a.failed || b.calls.length - a.calls.length || a.tool.localeCompare(b.tool));
}

// callGroupText is one group as the side panel reads it:
// "api_invoke_endpoint: 54 succeeded, 144 failed (Not Found 132, Forbidden 12), median 76 ms".
export function callGroupText(g: CallGroup): string {
  const parts = [`${g.succeeded} succeeded`];
  if (g.failed > 0) {
    const why = g.errors.map((e) => `${e.error} ${e.count}`).join(", ");
    parts.push(`${g.failed} failed (${why})`);
  }
  return `${g.tool}: ${parts.join(", ")}, median ${formatDuration(g.medianMS)}`;
}

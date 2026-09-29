import type { ElkExtendedEdge, ElkNode } from "elkjs/lib/elk-api";
import type { FlowNodeRun, ScriptFlow, StructBox, StructEdge } from "@/api/portal/hooks/scriptFlow";
import { CARD_WIDTH, FONT_MONO, FONT_TITLE, cardHeight, roundedPath, textWidth } from "./flowModel";
import { loadElk } from "./flowLayout";
import { boxText, nodeText, stepCard, type StructureView, type ViewNode } from "./structureModel";

// Laying the Structure view out (#1972) is ELK's layered algorithm top to
// bottom, the direction flowcharts are read in, with the loop and helper
// boxes as nested nodes so edges route into and out of them.

export interface PlacedStructNode {
  node: ViewNode;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PlacedBox {
  box: StructBox;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PlacedStructEdge {
  key: string;
  edge: StructEdge;
  path: string;
  label?: { x: number; y: number };
}

export interface StructureLayout {
  width: number;
  height: number;
  nodes: PlacedStructNode[];
  boxes: PlacedBox[];
  edges: PlacedStructEdge[];
}

// Control node geometry.
const PILL_HEIGHT = 34;
const DECISION_HEIGHT = 40;
const FOLDED_HEIGHT = 58;
const MAX_CONTROL_WIDTH = 420;
const LABEL_WIDTH = 28;
const LABEL_HEIGHT = 16;
// Box padding: room for the heading, and for a caption under it.
const BOX_PAD_TOP = 40;
const BOX_PAD_TOP_CAPTIONED = 58;

const ROOT_OPTIONS: Record<string, string> = {
  "elk.algorithm": "layered",
  "elk.direction": "DOWN",
  "elk.hierarchyHandling": "INCLUDE_CHILDREN",
  "elk.layered.spacing.nodeNodeBetweenLayers": "40",
  "elk.spacing.nodeNode": "28",
  "elk.layered.spacing.edgeNodeBetweenLayers": "18",
  "elk.edgeRouting": "ORTHOGONAL",
  "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
  "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
  "elk.padding": "[top=24,left=24,bottom=24,right=24]",
  "elk.edgeLabels.placement": "TAIL",
};

const BOX_PREFIX = "box:";

// nodeSize is how big a node is drawn.
export function nodeSize(
  n: ViewNode,
  graph: ScriptFlow,
  run?: Record<string, FlowNodeRun>,
): { width: number; height: number } {
  if (n.kind === "step") {
    const card = stepCard(n, graph);
    return { width: CARD_WIDTH, height: card ? cardHeight(card, run?.[card.id]) : PILL_HEIGHT + 16 };
  }
  if (n.kind === "fn") return { width: CARD_WIDTH, height: FOLDED_HEIGHT };
  const text = nodeText(n, graph);
  // A decision and a stop are drawn in the monospace face, since they quote
  // the source; they are measured in it.
  const font = n.kind === "if" || n.kind === "stop" ? FONT_MONO : FONT_TITLE;
  const width = Math.min(MAX_CONTROL_WIDTH, Math.max(96, textWidth(text, font) + 52));
  return { width, height: n.kind === "if" || n.kind === "stop" ? DECISION_HEIGHT : PILL_HEIGHT };
}

// elkStructure is the ELK input for a structure: every node inside its box,
// every box inside its parent's, and every edge at the root.
export function elkStructure(view: StructureView, graph: ScriptFlow, run?: Record<string, FlowNodeRun>): ElkNode {
  const root: ElkNode = { id: "root", layoutOptions: ROOT_OPTIONS, children: [], edges: [] };
  const byId = new Map(view.boxes.map((b) => [b.id, b]));
  const made = new Map<string, ElkNode>();
  const box = (id: string | undefined): ElkNode => {
    if (!id || !byId.has(id)) return root;
    const known = made.get(id);
    if (known) return known;
    const b = byId.get(id)!;
    const top = b.caption ? BOX_PAD_TOP_CAPTIONED : BOX_PAD_TOP;
    const width = Math.ceil(textWidth(boxText(b), FONT_TITLE)) + 90;
    const el: ElkNode = {
      id: BOX_PREFIX + id,
      layoutOptions: {
        "elk.padding": `[top=${top},left=16,bottom=16,right=16]`,
        "elk.nodeSize.constraints": "MINIMUM_SIZE",
        "elk.nodeSize.minimum": `(${width},${top + 20})`,
      },
      children: [],
    };
    made.set(id, el);
    box(b.parent).children!.push(el);
    return el;
  };
  for (const b of view.boxes) box(b.id);
  for (const n of view.nodes) box(n.box).children!.push({ id: n.id, ...nodeSize(n, graph, run) });
  view.edges.forEach((e, i) => {
    const edge: ElkExtendedEdge = { id: `e${i}`, sources: [e.from], targets: [e.to] };
    if (e.label) edge.labels = [{ id: `l${i}`, text: e.label, width: LABEL_WIDTH, height: LABEL_HEIGHT }];
    root.edges!.push(edge);
  });
  return root;
}

// placeStructure reads ELK's output into absolute positions. ELK reports a
// child relative to its parent, and an edge relative to the box it was moved
// to.
export function placeStructure(view: StructureView, out: ElkNode): StructureLayout {
  const layout: StructureLayout = { width: out.width ?? 0, height: out.height ?? 0, nodes: [], boxes: [], edges: [] };
  const nodes = new Map(view.nodes.map((n) => [n.id, n]));
  const boxes = new Map(view.boxes.map((b) => [b.id, b]));
  const origin = new Map<string, { x: number; y: number }>();
  const elkEdges: ElkExtendedEdge[] = [];
  const walk = (n: ElkNode, ox: number, oy: number) => {
    const x = ox + (n.x ?? 0);
    const y = oy + (n.y ?? 0);
    origin.set(n.id, { x, y });
    placeOne(layout, n, { x, y }, nodes, boxes);
    elkEdges.push(...(n.edges ?? []));
    for (const c of n.children ?? []) walk(c, x, y);
  };
  walk(out, -(out.x ?? 0), -(out.y ?? 0));
  for (const e of elkEdges) layout.edges.push(...placeEdge(view, e, origin));
  return layout;
}

// placeOne records one ELK node as the structure node or box it stands for.
function placeOne(
  layout: StructureLayout,
  n: ElkNode,
  at: { x: number; y: number },
  nodes: Map<string, ViewNode>,
  boxes: Map<string, StructBox>,
) {
  const size = { ...at, width: n.width ?? 0, height: n.height ?? 0 };
  const node = nodes.get(n.id);
  if (node) layout.nodes.push({ node, ...size });
  const box = n.id.startsWith(BOX_PREFIX) ? boxes.get(n.id.slice(BOX_PREFIX.length)) : undefined;
  if (box) layout.boxes.push({ box, ...size });
}

// placeEdge is one ELK edge's routes in absolute coordinates, with its arm's
// label on the first.
function placeEdge(
  view: StructureView,
  e: ElkExtendedEdge,
  origin: Map<string, { x: number; y: number }>,
): PlacedStructEdge[] {
  const edge = view.edges[Number(e.id.slice(1))];
  if (!edge) return [];
  const off = (e.container && origin.get(e.container)) || { x: 0, y: 0 };
  const label = e.labels?.[0];
  const labelAt =
    label?.x !== undefined && label.y !== undefined ? { x: label.x + off.x, y: label.y + off.y } : undefined;
  return (e.sections ?? []).map((s, i) => ({
    key: `${e.id}.${i}`,
    edge,
    path: roundedPath([s.startPoint, ...(s.bendPoints ?? []), s.endPoint].map((p) => ({ x: p.x + off.x, y: p.y + off.y }))),
    label: i === 0 ? labelAt : undefined,
  }));
}

// layoutStructure lays a structure out.
export async function layoutStructure(
  view: StructureView,
  graph: ScriptFlow,
  run?: Record<string, FlowNodeRun>,
): Promise<StructureLayout> {
  const elk = await loadElk();
  const out = await elk.layout(elkStructure(view, graph, run));
  return placeStructure(view, out);
}

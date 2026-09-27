import type { ElkExtendedEdge, ElkNode } from "elkjs/lib/elk-api";
import type { FlowEdge, FlowGroup, FlowNode, ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import { CARD_WIDTH, cardHeight, roundedPath } from "./flowModel";

// Laying the graph out (#1906) is elkjs's layered algorithm, left to right,
// with the function boxes as nested nodes so edges route across them. ELK is
// loaded the first time a diagram is drawn: it is a large library that only
// this tab uses.

export interface PlacedNode {
  node: FlowNode;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PlacedGroup {
  group: FlowGroup;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PlacedEdge {
  key: string;
  edge: FlowEdge;
  path: string;
  // reversed is true when the path runs from the edge's target to its source:
  // the state edge, which is laid out backwards so the cycle it closes does
  // not pull the save to the front of the diagram.
  reversed: boolean;
}

export interface FlowLayout {
  width: number;
  height: number;
  nodes: PlacedNode[];
  groups: PlacedGroup[];
  edges: PlacedEdge[];
}

// Box padding: room for the signature, and for the caption under it.
const GROUP_PAD_TOP = 40;
const GROUP_PAD_TOP_CAPTIONED = 58;

const ROOT_OPTIONS: Record<string, string> = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.hierarchyHandling": "INCLUDE_CHILDREN",
  "elk.layered.spacing.nodeNodeBetweenLayers": "64",
  "elk.spacing.nodeNode": "22",
  "elk.layered.spacing.edgeNodeBetweenLayers": "22",
  "elk.edgeRouting": "ORTHOGONAL",
  "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
  "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
  "elk.padding": "[top=24,left=24,bottom=24,right=24]",
  "elk.layered.mergeEdges": "true",
  "elk.layered.mergeHierarchyEdges": "true",
};

const GROUP_PREFIX = "g:";

// elkGraph is the ELK input for a flow graph: every card inside its box, every
// box inside its parent's, and every edge at the root, where ELK's hierarchy
// handling moves it to the box both ends share.
export function elkGraph(graph: ScriptFlow): ElkNode {
  const root: ElkNode = { id: "root", layoutOptions: ROOT_OPTIONS, children: [], edges: [] };
  const boxes = new Map<string, ElkNode>();
  const byId = new Map(graph.groups.map((g) => [g.id, g]));
  const box = (id: string | undefined): ElkNode => {
    if (!id || !byId.has(id)) return root;
    const known = boxes.get(id);
    if (known) return known;
    const g = byId.get(id)!;
    const top = g.caption ? GROUP_PAD_TOP_CAPTIONED : GROUP_PAD_TOP;
    const made: ElkNode = {
      id: GROUP_PREFIX + id,
      layoutOptions: { "elk.padding": `[top=${top},left=16,bottom=16,right=16]` },
      children: [],
    };
    boxes.set(id, made);
    box(g.parent).children!.push(made);
    return made;
  };
  for (const g of graph.groups) box(g.id);
  for (const n of graph.nodes) {
    box(n.group).children!.push({ id: n.id, width: CARD_WIDTH, height: cardHeight(n) });
  }
  const ids = new Set(graph.nodes.map((n) => n.id));
  graph.edges.forEach((e, i) => {
    if (!ids.has(e.from) || !ids.has(e.to)) return;
    // The state edge goes from the save back to run.state, the one cycle a
    // graph has. Laid out as it points, it makes the save the first layer;
    // laid out reversed, the save stays where the run ends.
    const [from, to] = e.kind === "state" ? [e.to, e.from] : [e.from, e.to];
    root.edges!.push({ id: `e${i}`, sources: [from], targets: [to] });
  });
  return root;
}

// place reads ELK's output into absolute positions. ELK reports a child
// relative to its parent, and an edge relative to the box it was moved to.
export function place(graph: ScriptFlow, out: ElkNode): FlowLayout {
  const layout: FlowLayout = {
    width: out.width ?? 0,
    height: out.height ?? 0,
    nodes: [],
    groups: [],
    edges: [],
  };
  const { origin, elkEdges } = placeNodes(graph, out, layout);
  for (const e of elkEdges) layout.edges.push(...placeEdge(graph, e, origin));
  return layout;
}

// placeNodes walks ELK's tree, placing every card and box, and returns each
// element's absolute origin with every edge found on the way.
function placeNodes(graph: ScriptFlow, out: ElkNode, layout: FlowLayout) {
  const nodesById = new Map(graph.nodes.map((n) => [n.id, n]));
  const groupsById = new Map(graph.groups.map((g) => [g.id, g]));
  const origin = new Map<string, { x: number; y: number }>();
  const elkEdges: ElkExtendedEdge[] = [];
  const record = (n: ElkNode, x: number, y: number) => {
    const size = { x, y, width: n.width ?? 0, height: n.height ?? 0 };
    const flowNode = nodesById.get(n.id);
    if (flowNode) layout.nodes.push({ node: flowNode, ...size });
    const group = n.id.startsWith(GROUP_PREFIX) ? groupsById.get(n.id.slice(GROUP_PREFIX.length)) : undefined;
    if (group) layout.groups.push({ group, ...size });
  };
  const walk = (n: ElkNode, ox: number, oy: number) => {
    const x = ox + (n.x ?? 0);
    const y = oy + (n.y ?? 0);
    origin.set(n.id, { x, y });
    record(n, x, y);
    elkEdges.push(...(n.edges ?? []));
    for (const c of n.children ?? []) walk(c, x, y);
  };
  walk(out, -(out.x ?? 0), -(out.y ?? 0));
  return { origin, elkEdges };
}

// placeEdge is one ELK edge's routes in absolute coordinates.
function placeEdge(
  graph: ScriptFlow,
  e: ElkExtendedEdge,
  origin: Map<string, { x: number; y: number }>,
): PlacedEdge[] {
  const edge = graph.edges[Number(e.id.slice(1))];
  if (!edge) return [];
  const off = (e.container && origin.get(e.container)) || { x: 0, y: 0 };
  return (e.sections ?? []).map((s, i) => ({
    key: `${e.id}.${i}`,
    edge,
    reversed: edge.kind === "state",
    path: roundedPath(
      [s.startPoint, ...(s.bendPoints ?? []), s.endPoint].map((p) => ({ x: p.x + off.x, y: p.y + off.y })),
    ),
  }));
}

type Elk = { layout: (graph: ElkNode) => Promise<ElkNode> };
let elkInstance: Promise<Elk> | null = null;

function loadElk(): Promise<Elk> {
  elkInstance ??= import("elkjs/lib/elk.bundled.js").then((m) => new m.default());
  return elkInstance;
}

// layoutFlow lays a graph out.
export async function layoutFlow(graph: ScriptFlow): Promise<FlowLayout> {
  const elk = await loadElk();
  const out = await elk.layout(elkGraph(graph));
  return place(graph, out);
}

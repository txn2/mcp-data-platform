import type { ElkExtendedEdge, ElkNode } from "elkjs/lib/elk-api";
import type {
  ApplicationDiagram,
  ApplicationDiagramGroup,
  ApplicationDiagramLink,
  ApplicationDiagramNode,
} from "@/api/portal/hooks/applications";
import { loadElk } from "@/pages/scripts/flow/flowLayout";
import { roundedPath, textWidth } from "@/pages/scripts/flow/flowModel";
import { FONT_CHIP, NODE_WIDTH, linkLabel, nodeHeight } from "./diagramModel";

// Laying an application out (#2540) is the script Flow tab's: elkjs's layered
// algorithm left to right, loaded the first time a diagram is drawn, with the
// groups (the application, its schema inside it, and what is outside it) as
// nested nodes so links route across them, and each link's kind as a label.

export interface PlacedNode {
  node: ApplicationDiagramNode;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PlacedGroup {
  group: ApplicationDiagramGroup;
  depth: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PlacedLink {
  key: string;
  link: ApplicationDiagramLink;
  path: string;
  label?: { x: number; y: number; text: string };
}

export interface DiagramLayout {
  width: number;
  height: number;
  nodes: PlacedNode[];
  groups: PlacedGroup[];
  links: PlacedLink[];
}

const GROUP_PAD_TOP = 40;
const LABEL_HEIGHT = 16;

const ROOT_OPTIONS: Record<string, string> = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.hierarchyHandling": "INCLUDE_CHILDREN",
  "elk.layered.spacing.nodeNodeBetweenLayers": "72",
  "elk.spacing.nodeNode": "24",
  "elk.layered.spacing.edgeNodeBetweenLayers": "22",
  "elk.edgeRouting": "ORTHOGONAL",
  "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
  "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
  "elk.padding": "[top=24,left=24,bottom=24,right=24]",
  "elk.edgeLabels.placement": "CENTER",
};

const GROUP_PREFIX = "g:";

// drawnGroups is the groups that hold a node, directly or through a group
// inside them; an empty box says nothing.
function drawnGroups(d: ApplicationDiagram): Map<string, ApplicationDiagramGroup> {
  const byId = new Map(d.groups.map((g) => [g.id, g]));
  const out = new Map<string, ApplicationDiagramGroup>();
  for (const n of d.nodes) {
    let g = byId.get(n.group);
    while (g && !out.has(g.id)) {
      out.set(g.id, g);
      g = g.parent ? byId.get(g.parent) : undefined;
    }
  }
  return out;
}

// elkDiagram is the ELK input: every card inside its group's box, every box
// inside its parent's, and every link at the root, where ELK's hierarchy
// handling moves it to the box both ends share. A node in a group the diagram
// does not draw (a removed node's schema, gone since) is drawn at the root.
export function elkDiagram(d: ApplicationDiagram): ElkNode {
  const root: ElkNode = { id: "root", layoutOptions: ROOT_OPTIONS, children: [], edges: [] };
  const groups = drawnGroups(d);
  const boxes = new Map<string, ElkNode>();
  const box = (id: string | undefined): ElkNode => {
    const g = id ? groups.get(id) : undefined;
    if (!g) return root;
    const known = boxes.get(g.id);
    if (known) return known;
    const made: ElkNode = {
      id: GROUP_PREFIX + g.id,
      layoutOptions: { "elk.padding": `[top=${GROUP_PAD_TOP},left=16,bottom=16,right=16]` },
      children: [],
    };
    boxes.set(g.id, made);
    box(g.parent).children!.push(made);
    return made;
  };
  for (const g of groups.values()) box(g.id);
  for (const n of d.nodes) box(n.group).children!.push({ id: n.id, width: NODE_WIDTH, height: nodeHeight(n) });
  const ids = new Set(d.nodes.map((n) => n.id));
  d.links.forEach((l, i) => {
    if (!ids.has(l.from) || !ids.has(l.to) || l.from === l.to) return;
    const text = linkLabel(l);
    root.edges!.push({
      id: `e${i}`,
      sources: [l.from],
      targets: [l.to],
      labels: [{ id: `l${i}`, text, width: Math.ceil(textWidth(text, FONT_CHIP)) + 10, height: LABEL_HEIGHT }],
    });
  });
  return root;
}

// placeDiagram reads ELK's output into absolute positions. ELK reports a child
// relative to its parent, and an edge relative to the box it was moved to.
export function placeDiagram(d: ApplicationDiagram, out: ElkNode): DiagramLayout {
  const layout: DiagramLayout = { width: out.width ?? 0, height: out.height ?? 0, nodes: [], groups: [], links: [] };
  const nodes = new Map(d.nodes.map((n) => [n.id, n]));
  const groups = new Map(d.groups.map((g) => [g.id, g]));
  const origin = new Map<string, { x: number; y: number }>();
  const edges: ElkExtendedEdge[] = [];
  const record = (n: ElkNode, at: { x: number; y: number }, depth: number) => {
    const size = { ...at, width: n.width ?? 0, height: n.height ?? 0 };
    const node = nodes.get(n.id);
    if (node) layout.nodes.push({ node, ...size });
    const group = n.id.startsWith(GROUP_PREFIX) ? groups.get(n.id.slice(GROUP_PREFIX.length)) : undefined;
    if (group) layout.groups.push({ group, depth, ...size });
  };
  const walk = (n: ElkNode, ox: number, oy: number, depth: number) => {
    const at = { x: ox + (n.x ?? 0), y: oy + (n.y ?? 0) };
    origin.set(n.id, at);
    record(n, at, depth);
    edges.push(...(n.edges ?? []));
    for (const c of n.children ?? []) walk(c, at.x, at.y, depth + 1);
  };
  walk(out, -(out.x ?? 0), -(out.y ?? 0), 0);
  for (const e of edges) layout.links.push(...placeLink(d, e, origin));
  return layout;
}

// placeLink is one ELK edge's routes in absolute coordinates, with its label
// on the first.
function placeLink(d: ApplicationDiagram, e: ElkExtendedEdge, origin: Map<string, { x: number; y: number }>): PlacedLink[] {
  const link = d.links[Number(e.id.slice(1))];
  if (!link) return [];
  const off = (e.container && origin.get(e.container)) || { x: 0, y: 0 };
  const l = e.labels?.[0];
  const label =
    l?.x !== undefined && l.y !== undefined ? { x: l.x + off.x, y: l.y + off.y, text: l.text ?? linkLabel(link) } : undefined;
  return (e.sections ?? []).map((s, i) => ({
    key: `${e.id}.${i}`,
    link,
    path: roundedPath([s.startPoint, ...(s.bendPoints ?? []), s.endPoint].map((p) => ({ x: p.x + off.x, y: p.y + off.y }))),
    label: i === 0 ? label : undefined,
  }));
}

/** layoutDiagram lays an application diagram out. */
export async function layoutDiagram(d: ApplicationDiagram): Promise<DiagramLayout> {
  const elk = await loadElk();
  const out = await elk.layout(elkDiagram(d));
  return placeDiagram(d, out);
}

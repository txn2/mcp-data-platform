import type { KnowledgeGraphEdge, KnowledgeGraphNode } from "@/api/portal/hooks";
import { parseRef, refHref } from "@/lib/entityRefs";

/**
 * Pure model helpers for the knowledge-graph view (#1162): node styling, the
 * neighborhood index that drives hover highlighting, the type facet, and the
 * navigation target of a node. Kept separate from the React components so the
 * graph's behavior is testable without a DOM or a running simulation.
 */

/**
 * NODE_STYLE gives each node type its mark. Shape carries the type distinction
 * (so the graph reads without color, and in either theme), and the hue separates
 * the corpus itself (pages) from the entities it describes. Colors are literal
 * hsl() rather than theme tokens because SVG fills cannot use Tailwind classes
 * that the two themes swap; these hues are chosen to hold contrast on both the
 * light and dark canvas.
 */
/** PAGE_TYPE is the node type of a knowledge page, the corpus the graph is of. */
export const PAGE_TYPE = "knowledge_page";

export const NODE_STYLE: Record<string, { fill: string; shape: "circle" | "square" | "diamond"; label: string }> = {
  knowledge_page: { fill: "hsl(221, 83%, 60%)", shape: "circle", label: "Page" },
  asset: { fill: "hsl(172, 66%, 45%)", shape: "square", label: "Asset" },
  collection: { fill: "hsl(262, 60%, 62%)", shape: "square", label: "Collection" },
  prompt: { fill: "hsl(291, 55%, 58%)", shape: "diamond", label: "Prompt" },
  datahub: { fill: "hsl(35, 85%, 52%)", shape: "diamond", label: "Catalog" },
  connection: { fill: "hsl(199, 75%, 48%)", shape: "square", label: "Connection" },
};

const UNKNOWN_STYLE = { fill: "hsl(215, 15%, 55%)", shape: "circle" as const, label: "Other" };

/** nodeStyle returns the mark for a node type, with a neutral fallback. */
export function nodeStyle(type: string) {
  return NODE_STYLE[type] ?? UNKNOWN_STYLE;
}

/** typeLabel is the human-readable name of a node type, for facets and legends. */
export function typeLabel(type: string): string {
  return nodeStyle(type).label;
}

/**
 * nodeRadius maps a node's normalized importance (betweenness, or degree when
 * nothing bridges anything) to a radius. The square root keeps the gradient
 * legible: on a linear scale one dominant hub flattens everything else into
 * indistinguishable dots.
 */
export function nodeRadius(weight: number): number {
  const w = Math.min(1, Math.max(0, weight));
  return MIN_NODE_RADIUS + Math.sqrt(w) * (MAX_NODE_RADIUS - MIN_NODE_RADIUS);
}

export const MIN_NODE_RADIUS = 5;
export const MAX_NODE_RADIUS = 16;

/** normalize maps raw scores into [0,1] against the largest, or all-zero when
 * nothing scores (a graph where no node bridges anything). */
export function normalize(scores: Map<string, number>): Map<string, number> {
  let max = 0;
  for (const s of scores.values()) max = Math.max(max, s);
  if (max <= 0) return new Map([...scores.keys()].map((k) => [k, 0]));
  return new Map([...scores].map(([k, s]) => [k, s / max]));
}

/** GraphIndex is the derived structure the canvas needs to draw and highlight.
 * References have a direction (a page cites an entity), so the index keeps the
 * undirected adjacency the traversals need AND the two directed views the
 * inspector reads to say "cites" versus "cited by". */
export interface GraphIndex {
  /** node id -> the ids directly connected to it (either direction). */
  neighbors: Map<string, Set<string>>;
  /** node id -> the ids it references. */
  cites: Map<string, Set<string>>;
  /** node id -> the ids that reference it. */
  citedBy: Map<string, Set<string>>;
  /** node id -> how many edges touch it. */
  degree: Map<string, number>;
}

/** buildGraphIndex derives the adjacency, both directed views, and the degree. */
export function buildGraphIndex(nodes: KnowledgeGraphNode[], edges: KnowledgeGraphEdge[]): GraphIndex {
  const neighbors = new Map<string, Set<string>>();
  const cites = new Map<string, Set<string>>();
  const citedBy = new Map<string, Set<string>>();
  const degree = new Map<string, number>();
  for (const n of nodes) {
    neighbors.set(n.id, new Set());
    cites.set(n.id, new Set());
    citedBy.set(n.id, new Set());
    degree.set(n.id, 0);
  }
  for (const e of edges) {
    neighbors.get(e.source)?.add(e.target);
    neighbors.get(e.target)?.add(e.source);
    cites.get(e.source)?.add(e.target);
    citedBy.get(e.target)?.add(e.source);
    degree.set(e.source, (degree.get(e.source) ?? 0) + 1);
    degree.set(e.target, (degree.get(e.target) ?? 0) + 1);
  }
  return { neighbors, cites, citedBy, degree };
}

/**
 * COMMUNITY_FILLS are the cluster tints, used behind a community's convex hull
 * rather than on the nodes: type already owns the node colour channel, so
 * clusters are shown as regions the nodes sit inside. Deliberately low-chroma so
 * a hull never competes with the marks on top of it, in either theme.
 */
export const COMMUNITY_FILLS = [
  "hsl(221, 70%, 55%)",
  "hsl(28, 80%, 52%)",
  "hsl(160, 55%, 42%)",
  "hsl(291, 50%, 58%)",
  "hsl(199, 70%, 45%)",
  "hsl(350, 60%, 55%)",
  "hsl(48, 75%, 48%)",
  "hsl(262, 50%, 60%)",
];

/** communityFill returns a cluster's tint, cycling for a very fragmented graph. */
export function communityFill(community: number): string {
  return COMMUNITY_FILLS[community % COMMUNITY_FILLS.length]!;
}

/** BridgeRank places a node among the corpus's bridges. */
export interface BridgeRank {
  /** 1 for the strongest bridge in the corpus. */
  rank: number;
  /** How many nodes bridge anything at all. */
  total: number;
  /** True when nothing in the corpus bridges more. */
  strongest: boolean;
  /** Top-N% band, at least 1 — never the meaningless "top 100%". */
  percentile: number;
}

/**
 * bridgeRank places a node's betweenness among all nodes that bridge anything at
 * all. Nodes scoring zero (every leaf) are excluded from the population, so the
 * band means "top N% of actual bridges" rather than a number inflated by leaves.
 * Returns null for a node that bridges nothing.
 */
export function bridgeRank(score: number, scores: Iterable<number>): BridgeRank | null {
  if (score <= 0) return null;
  const bridging = [...scores].filter((s) => s > 0);
  if (bridging.length === 0) return null;
  const rank = bridging.filter((s) => s > score).length + 1;
  return {
    rank,
    total: bridging.length,
    strongest: rank === 1,
    percentile: Math.max(1, Math.round((rank / bridging.length) * 100)),
  };
}

/**
 * typeFacet counts the node types present, most-used first, so the filter offers
 * only types the graph actually contains rather than every type the platform has.
 */
export function typeFacet(nodes: KnowledgeGraphNode[]): [string, number][] {
  const counts = new Map<string, number>();
  for (const n of nodes) counts.set(n.type, (counts.get(n.type) ?? 0) + 1);
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

/**
 * filterByTypes drops nodes of hidden types and every edge that would dangle
 * from one, so the filtered graph is always internally consistent. An empty
 * `hidden` set is the identity. Knowledge pages are never hidden: they are the
 * corpus the view exists to show, and hiding them would leave orphaned entities.
 * The exemption keys on the TYPE, not on `page` (which marks only the pages
 * inside the listing window), so a page cited from inside the window but listed
 * outside it is exempt too rather than being the one page the filter can remove.
 */
export function filterByTypes(
  nodes: KnowledgeGraphNode[],
  edges: KnowledgeGraphEdge[],
  hidden: Set<string>,
): { nodes: KnowledgeGraphNode[]; edges: KnowledgeGraphEdge[] } {
  if (hidden.size === 0) return { nodes, edges };
  const kept = nodes.filter((n) => n.type === PAGE_TYPE || !hidden.has(n.type));
  const keptIds = new Set(kept.map((n) => n.id));
  return { nodes: kept, edges: edges.filter((e) => keptIds.has(e.source) && keptIds.has(e.target)) };
}

/**
 * matchingNodeIds returns the nodes whose label matches the query, which the
 * canvas rings and centers. An empty or whitespace query matches nothing, so
 * clearing the search box removes the focus rather than selecting everything.
 */
export function matchingNodeIds(nodes: KnowledgeGraphNode[], query: string): Set<string> {
  const q = query.trim().toLowerCase();
  if (!q) return new Set();
  return new Set(nodes.filter((n) => n.label.toLowerCase().includes(q)).map((n) => n.id));
}

/**
 * nodeDestination returns what clicking a node should do: open a knowledge page
 * by id, navigate to an entity's portal home, or nothing for a type with no
 * in-app destination (a connection or a catalog URN). A broken reference has no
 * destination either — there is nothing left to open.
 */
export function nodeDestination(node: KnowledgeGraphNode):
  | { kind: "page"; id: string }
  | { kind: "path"; path: string }
  | null {
  if (!node.exists) return null;
  const parsed = parseRef(node.id);
  if (!parsed) return null;
  if (parsed.type === PAGE_TYPE) {
    return parsed.id ? { kind: "page", id: parsed.id } : null;
  }
  const href = refHref(parsed.type, parsed.id, parsed.urn);
  return href ? { kind: "path", path: href } : null;
}

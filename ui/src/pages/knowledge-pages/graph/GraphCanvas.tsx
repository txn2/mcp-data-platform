import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { GraphNodeMark } from "./GraphNodeMark";
import { CommunityHulls } from "./CommunityHulls";
import { pathEdgeKey } from "./graphAnalytics";
import { placeLabels, type LabelCandidate } from "./labelPlacement";
import { truncateLabel } from "./GraphNodeMark";
import type { SimLink, SimNode } from "./useForceLayout";
import { endpointID } from "./useForceLayout";
import { toGraphPoint, wheelZoomFactor, type useGraphViewport } from "./useGraphViewport";

/**
 * DRAG_SLOP is how far the pointer may travel before a press counts as a drag
 * rather than a click. A dragged node follows the cursor, so the pointer is
 * still over it on release and the browser fires a click — without this, every
 * drag would also activate the node.
 */
const DRAG_SLOP = 4;

/** ARROW_GAP separates an arrowhead from the node it points at. */
const ARROW_GAP = 3;

/** GraphHighlight is everything that changes how the graph is painted without
 * changing what it contains. */
export interface GraphHighlight {
  /** Nodes matching the search box; ringed, with everything else dimmed. */
  matches: Set<string>;
  /** The node open in the inspector. */
  selected: string | null;
  /** Nodes on the traced shortest path. */
  pathNodes: Set<string>;
  /** Undirected keys of the edges on that path. */
  pathEdges: Set<string>;
  /** node id -> community, for the cluster hulls. */
  communities: Map<string, number>;
  /** Whether to draw the cluster hulls at all. */
  showHulls: boolean;
}

interface GraphCanvasProps {
  nodes: SimNode[];
  links: SimLink[];
  /** Bumped by the layout whenever positions change, so the canvas redraws. */
  version: number;
  neighbors: Map<string, Set<string>>;
  highlight: GraphHighlight;
  width: number;
  height: number;
  /** Pan/zoom state, owned by the caller so its controls live in the toolbar
   * rather than floating over (and hiding) the nodes in a corner. */
  viewport: ReturnType<typeof useGraphViewport>;
  onDragNode: (id: string, x: number, y: number) => void;
  onSelect: (id: string) => void;
}

/**
 * GraphCanvas draws the knowledge graph and owns its direct manipulation: pan,
 * zoom, node drag, hover highlighting, and selection. It renders the positions
 * the layout solved; it never solves them itself.
 */
export function GraphCanvas(props: GraphCanvasProps) {
  const { nodes, links, version, neighbors, highlight, width, height, viewport, onDragNode, onSelect } =
    props;
  const svgRef = useRef<SVGSVGElement>(null);
  const dragging = useRef<string | null>(null);
  const pressAt = useRef<{ x: number; y: number } | null>(null);
  const dragged = useRef(false);
  const [hovered, setHovered] = useState<string | null>(null);
  const { transform } = viewport;

  // The set of nodes drawn at full strength. Hover wins over the search focus so
  // inspecting a node is never fought by an active query.
  const lit = useMemo(
    () => litNodes(hovered, highlight, neighbors),
    [hovered, highlight, neighbors],
  );

  const localPoint = useCallback((e: { clientX: number; clientY: number }) => {
    const rect = svgRef.current?.getBoundingClientRect();
    return { px: e.clientX - (rect?.left ?? 0), py: e.clientY - (rect?.top ?? 0) };
  }, []);

  // Wheel zoom is bound manually, non-passively. React attaches wheel listeners
  // at the root as PASSIVE, so a handler declared with onWheel cannot call
  // preventDefault — the page would scroll underneath every zoom gesture.
  //
  // The dependency is the zoom callback, NOT the viewport object: that object is
  // rebuilt on every render, and this component re-renders on every simulation
  // tick, so depending on it would detach and reattach the listener ~60 times a
  // second and drop the events that land in between.
  const { zoomBy } = viewport;
  useEffect(() => {
    const el = svgRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      zoomBy(wheelZoomFactor(e.deltaY, e.deltaMode), e.clientX - rect.left, e.clientY - rect.top);
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [zoomBy]);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent<SVGSVGElement>) => {
      const { px, py } = localPoint(e);
      dragged.current = false;
      pressAt.current = { x: px, y: py };
      viewport.startPan(px, py);
    },
    [localPoint, viewport],
  );

  const handlePointerMove = useCallback(
    (e: React.PointerEvent<SVGSVGElement>) => {
      const { px, py } = localPoint(e);
      const from = pressAt.current;
      if (from && Math.hypot(px - from.x, py - from.y) > DRAG_SLOP) dragged.current = true;
      const id = dragging.current;
      if (id) {
        const p = toGraphPoint(transform, px, py);
        onDragNode(id, p.x, p.y);
        return;
      }
      viewport.panTo(px, py);
    },
    [localPoint, onDragNode, transform, viewport],
  );

  const handlePointerUp = useCallback(() => {
    dragging.current = null;
    pressAt.current = null;
    viewport.endPan();
  }, [viewport]);

  const startNodeDrag = useCallback(
    (e: React.PointerEvent, id: string) => {
      e.stopPropagation();
      dragging.current = id;
      dragged.current = false;
      pressAt.current = { x: localPoint(e).px, y: localPoint(e).py };
    },
    [localPoint],
  );

  // A press that turned into a drag must not also select: the node follows the
  // cursor, so the browser still fires a click on release.
  const selectNode = useCallback(
    (node: SimNode) => {
      if (dragged.current) return;
      onSelect(node.id);
    },
    [onSelect],
  );

  // Which labels are actually drawn. A force layout packs related nodes
  // together, so simply painting every label produces an unreadable pile; the
  // placement pass resolves the collisions by priority instead. The selection,
  // the search matches and a traced path are never dropped.
  const labelled = useMemo(
    () => placeLabels(labelCandidates(nodes, lit), forcedLabels(highlight)),
    // The simulation mutates node positions in place without replacing the
    // array, so the tick counter is what says the positions changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [nodes, lit, highlight, version],
  );

  return (
    <svg
      ref={svgRef}
      width={width}
      height={height}
      role="application"
      aria-label="Knowledge graph"
      className="touch-none select-none rounded-lg border border-border bg-card"
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onPointerLeave={handlePointerUp}
    >
      <defs>
        <ArrowMarker id="kg-arrow" className="text-muted-foreground" />
        <ArrowMarker id="kg-arrow-path" className="text-primary" />
      </defs>
      <g transform={`translate(${transform.x},${transform.y}) scale(${transform.k})`}>
        {highlight.showHulls && (
          <CommunityHulls nodes={nodes} communities={highlight.communities} version={version} />
        )}
        <g stroke="currentColor" className="text-muted-foreground">
          {links.map((l, i) => (
            <GraphEdge
              key={`${endpointID(l.source)}->${endpointID(l.target)}-${i}`}
              link={l}
              lit={lit}
              pathEdges={highlight.pathEdges}
            />
          ))}
        </g>
        {nodes.map((n) => (
          <GraphNodeMark
            key={n.id}
            node={n}
            dimmed={lit !== null && !lit.has(n.id)}
            matched={highlight.matches.has(n.id)}
            selected={highlight.selected === n.id}
            onPath={highlight.pathNodes.has(n.id)}
            showLabel={labelled.has(n.id)}
            onHover={setHovered}
            onDragStart={startNodeDrag}
            onSelect={selectNode}
          />
        ))}
      </g>
    </svg>
  );
}

/** ArrowMarker defines one arrowhead, coloured by its class. */
function ArrowMarker({ id, className }: { id: string; className: string }) {
  return (
    <marker
      id={id}
      viewBox="0 0 8 8"
      refX="7"
      refY="4"
      markerWidth="5"
      markerHeight="5"
      orient="auto"
      className={className}
    >
      <path d="M0,0 L8,4 L0,8 z" fill="currentColor" />
    </marker>
  );
}

/**
 * litNodes returns the nodes to draw at full strength, or null when nothing is
 * focused and the whole graph is lit. Hovering lights the node and its immediate
 * neighbourhood; with no hover, a traced path wins over an active search.
 */
export function litNodes(
  hovered: string | null,
  highlight: Pick<GraphHighlight, "matches" | "pathNodes">,
  neighbors: Map<string, Set<string>>,
): Set<string> | null {
  if (hovered) {
    return new Set<string>([hovered, ...(neighbors.get(hovered) ?? [])]);
  }
  if (highlight.pathNodes.size > 0) return highlight.pathNodes;
  return highlight.matches.size > 0 ? highlight.matches : null;
}

/** EdgeGeometry is one edge's resolved endpoints and how to paint it. */
interface EdgeGeometry {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  active: boolean;
  onPath: boolean;
}

/**
 * edgeGeometry resolves a link's endpoints to coordinates, stopping the line
 * short of the target node so the arrowhead sits beside the mark instead of
 * under it. Returns null for a link the simulation has not yet bound to node
 * objects, which is the only state in which an edge cannot be drawn.
 */
function edgeGeometry(
  link: SimLink,
  lit: Set<string> | null,
  pathEdges: Set<string>,
): EdgeGeometry | null {
  const source = link.source as SimNode;
  const target = link.target as SimNode;
  if (typeof source !== "object" || typeof target !== "object") return null;
  const x1 = source.x ?? 0;
  const y1 = source.y ?? 0;
  const end = shortenToNode(x1, y1, target.x ?? 0, target.y ?? 0, target.radius + ARROW_GAP);
  return {
    x1,
    y1,
    x2: end.x,
    y2: end.y,
    active: lit === null || (lit.has(source.id) && lit.has(target.id)),
    onPath: pathEdges.has(pathEdgeKey(source.id, target.id)),
  };
}

/** shortenToNode pulls a segment's end back along its own direction by `by`. */
function shortenToNode(x1: number, y1: number, x2: number, y2: number, by: number) {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const len = Math.hypot(dx, dy);
  if (len <= by) return { x: x2, y: y2 };
  return { x: x2 - (dx / len) * by, y: y2 - (dy / len) * by };
}

/** GraphEdge draws one reference, pointing from the page at what it references. */
function GraphEdge({
  link,
  lit,
  pathEdges,
}: {
  link: SimLink;
  lit: Set<string> | null;
  pathEdges: Set<string>;
}) {
  const geom = edgeGeometry(link, lit, pathEdges);
  if (!geom) return null;
  if (geom.onPath) {
    return (
      <line
        x1={geom.x1}
        y1={geom.y1}
        x2={geom.x2}
        y2={geom.y2}
        className="text-primary"
        strokeWidth={2.5}
        strokeOpacity={1}
        markerEnd="url(#kg-arrow-path)"
      />
    );
  }
  return (
    <line
      x1={geom.x1}
      y1={geom.y1}
      x2={geom.x2}
      y2={geom.y2}
      strokeWidth={geom.active ? 1.25 : 1}
      strokeOpacity={geom.active ? 0.5 : 0.1}
      strokeDasharray={link.refSource === "inline" ? "4 3" : undefined}
      markerEnd={geom.active ? "url(#kg-arrow)" : undefined}
    />
  );
}

/**
 * labelCandidates ranks every node's label. A node lit by the current hover
 * outranks the rest, and past that the mark's own radius (its bridging score)
 * decides — so when two labels collide, the one on the more structurally
 * important node survives.
 */
function labelCandidates(nodes: SimNode[], lit: Set<string> | null): LabelCandidate[] {
  return nodes.map((n) => ({
    id: n.id,
    text: truncateLabel(n.label),
    x: n.x ?? 0,
    y: n.y ?? 0,
    radius: n.radius,
    priority: (lit?.has(n.id) ? 1000 : 0) + n.radius,
  }));
}

/** forcedLabels are the nodes whose label must be drawn whatever it collides
 * with, because the reader asked for that node specifically. */
function forcedLabels(highlight: GraphHighlight): Set<string> {
  const forced = new Set<string>(highlight.matches);
  for (const id of highlight.pathNodes) forced.add(id);
  if (highlight.selected) forced.add(highlight.selected);
  return forced;
}

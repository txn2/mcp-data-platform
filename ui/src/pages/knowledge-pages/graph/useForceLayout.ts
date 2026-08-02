import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  forceCenter,
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force";
import type { KnowledgeGraphEdge, KnowledgeGraphNode } from "@/api/portal/hooks";
import { nodeRadius } from "./graphModel";

/** SimNode is a graph node with the position the simulation solves for. */
export interface SimNode extends SimulationNodeDatum, KnowledgeGraphNode {
  radius: number;
}

/** SimLink is an edge; d3 replaces the endpoint ids with the node objects. */
export interface SimLink extends SimulationLinkDatum<SimNode> {
  type: string;
  refSource: string;
}

/**
 * MAX_ANIMATED_NODES is the size above which the layout is solved in one
 * synchronous batch instead of animated. Animation is a render per frame, which
 * a focused neighbourhood can easily afford and a whole dense corpus cannot; the
 * result is the same layout, reached without the frame budget.
 */
const MAX_ANIMATED_NODES = 250;

/** SETTLE_TICKS is the batch solve's step count for a graph too big to animate. */
const SETTLE_TICKS = 220;

/** endpointID reads a link endpoint, which d3 rewrites from an id to a node. */
export function endpointID(endpoint: SimLink["source"]): string {
  return typeof endpoint === "object" ? ((endpoint as SimNode).id ?? "") : String(endpoint);
}

/**
 * useForceLayout solves and animates a force-directed layout, and lets the
 * caller drag individual nodes. A dragged node keeps its pin, so a reader can
 * pull a cluster apart and have it stay legible; the caller's reset control
 * clears the pins by bumping `generation`.
 */
export function useForceLayout(
  nodes: KnowledgeGraphNode[],
  edges: KnowledgeGraphEdge[],
  /** Per-node importance in [0,1], which sets each node's radius. */
  weight: Map<string, number>,
  /** node id -> community, so the layout groups a cluster in one region. */
  communities: Map<string, number>,
  size: { width: number; height: number },
  generation: number,
) {
  // Positions survive a filter or depth change so widening the neighbourhood
  // grows the picture the reader is looking at rather than replacing it.
  const positions = useRef(new Map<string, { x: number; y: number }>());
  const [version, setVersion] = useState(0);

  // The simulation is part of the memo's RESULT, not a ref written during
  // render: under StrictMode a render is invoked twice and only one result is
  // kept, so a simulation stashed in a ref can end up being a different one from
  // the nodes that were rendered — dragging would move objects nothing draws.
  const layout = useMemo(() => {
    if (size.width <= 0 || size.height <= 0 || nodes.length === 0) {
      return { nodes: [] as SimNode[], links: [] as SimLink[], sim: null };
    }
    const simNodes = seedNodes(nodes, weight, positions.current, size);
    const byID = new Map(simNodes.map((n) => [n.id, n]));
    const simLinks: SimLink[] = edges
      .filter((e) => byID.has(e.source) && byID.has(e.target))
      .map((e) => ({ source: e.source, target: e.target, type: e.type, refSource: e.ref_source }));
    return {
      nodes: simNodes,
      links: simLinks,
      sim: buildSimulation(simNodes, simLinks, communities, size),
    };
    // `generation` is the caller's reset signal: bumping it rebuilds the layout.
  }, [nodes, edges, weight, communities, size.width, size.height, generation]); // eslint-disable-line react-hooks/exhaustive-deps

  // Run the layout. A small graph animates so the reader sees it settle and
  // responds continuously to a drag; a large one is solved in one batch.
  useEffect(() => {
    const sim = layout.sim;
    if (!sim) return;
    if (layout.nodes.length > MAX_ANIMATED_NODES) {
      sim.tick(SETTLE_TICKS);
      sim.stop();
      rememberPositions(layout.nodes, positions.current);
      setVersion((v) => v + 1);
      return;
    }
    sim.on("tick", () => {
      rememberPositions(layout.nodes, positions.current);
      setVersion((v) => v + 1);
    });
    sim.alpha(1).restart();
    return () => {
      sim.on("tick", null);
      sim.stop();
    };
  }, [layout]);

  const dragTo = useCallback(
    (id: string, x: number, y: number) => {
      const sim = layout.sim;
      const node = sim?.nodes().find((n) => n.id === id);
      if (!sim || !node) return;
      node.fx = x;
      node.fy = y;
      if (layout.nodes.length > MAX_ANIMATED_NODES) {
        // Not animating: advance a few steps by hand so the neighbours still
        // follow the dragged node.
        sim.alpha(0.3).tick(4);
        sim.stop();
        rememberPositions(sim.nodes(), positions.current);
        setVersion((v) => v + 1);
        return;
      }
      sim.alpha(0.3).restart();
    },
    [layout],
  );

  return { nodes: layout.nodes, links: layout.links, version, dragTo };
}

/** rememberPositions caches each node's solved position by id. */
function rememberPositions(nodes: SimNode[], into: Map<string, { x: number; y: number }>) {
  for (const n of nodes) {
    if (n.x != null && n.y != null) into.set(n.id, { x: n.x, y: n.y });
  }
}

/**
 * seedNodes builds the simulation nodes, reusing any position already solved.
 * A node with no remembered position starts near the middle rather than at d3's
 * default spiral, so newly revealed neighbours grow out of the graph instead of
 * flying in from the edges.
 */
function seedNodes(
  nodes: KnowledgeGraphNode[],
  weight: Map<string, number>,
  known: Map<string, { x: number; y: number }>,
  size: { width: number; height: number },
): SimNode[] {
  return nodes.map((n, i) => {
    const prev = known.get(n.id);
    const angle = (i / Math.max(1, nodes.length)) * Math.PI * 2;
    return {
      ...n,
      radius: nodeRadius(weight.get(n.id) ?? 0),
      x: prev?.x ?? size.width / 2 + Math.cos(angle) * 40,
      y: prev?.y ?? size.height / 2 + Math.sin(angle) * 40,
    };
  });
}

/** buildSimulation wires the forces that shape the layout, stopped. */
function buildSimulation(
  simNodes: SimNode[],
  simLinks: SimLink[],
  communities: Map<string, number>,
  size: { width: number; height: number },
) {
  return forceSimulation<SimNode, SimLink>(simNodes)
    .force(
      "link",
      forceLink<SimNode, SimLink>(simLinks)
        .id((d) => d.id)
        // Pages sit further from each other than from the entities they
        // describe, so a page and its references read as one cluster.
        .distance((l) => (endpointIsPage(l.source) && endpointIsPage(l.target) ? 130 : 80))
        .strength(0.3),
    )
    .force("charge", forceManyBody<SimNode>().strength(-300).distanceMax(520))
    .force("center", forceCenter(size.width / 2, size.height / 2))
    // The collision radius is padded well past the mark because a node carries a
    // label under it; without the padding, labels of adjacent nodes overlap into
    // an unreadable pile even though the marks themselves are clear.
    .force("collide", forceCollide<SimNode>().radius((d) => d.radius + 26))
    // Pull each detected cluster into its own region, so the communities the
    // analysis found are somewhere on screen rather than interleaved through
    // each other. Without it a cluster is a statistic, not a thing you can see.
    .force("cluster", forceCluster(communities))
    // Applied last so it has the final say on every node's position.
    .force("bounds", forceBounds(size))
    .alphaDecay(0.035)
    .stop();
}

/** CLUSTER_STRENGTH is how hard a node is pulled toward its community's centre.
 * Strong enough to separate the clusters, weak enough that the reference edges
 * still decide the shape inside one. */
const CLUSTER_STRENGTH = 0.55;

/**
 * forceCluster attracts every node toward the centroid of its own community.
 * The centroids are recomputed each tick from the live positions, so the regions
 * emerge from the layout rather than being imposed on fixed coordinates.
 */
function forceCluster(communities: Map<string, number>) {
  let nodes: SimNode[] = [];
  const force = (alpha: number) => {
    const centroids = communityCentroids(nodes, communities);
    for (const d of nodes) {
      const c = communities.get(d.id);
      const centre = c === undefined ? undefined : centroids.get(c);
      if (!centre) continue;
      d.vx = (d.vx ?? 0) + (centre.x - (d.x ?? 0)) * CLUSTER_STRENGTH * alpha;
      d.vy = (d.vy ?? 0) + (centre.y - (d.y ?? 0)) * CLUSTER_STRENGTH * alpha;
    }
  };
  force.initialize = (ns: SimNode[]) => {
    nodes = ns;
  };
  return force;
}

/** communityCentroids averages each community's member positions. */
function communityCentroids(nodes: SimNode[], communities: Map<string, number>) {
  const sums = new Map<number, { x: number; y: number; n: number }>();
  for (const d of nodes) {
    const c = communities.get(d.id);
    if (c === undefined) continue;
    const acc = sums.get(c) ?? { x: 0, y: 0, n: 0 };
    acc.x += d.x ?? 0;
    acc.y += d.y ?? 0;
    acc.n += 1;
    sums.set(c, acc);
  }
  const centroids = new Map<number, { x: number; y: number }>();
  for (const [c, acc] of sums) centroids.set(c, { x: acc.x / acc.n, y: acc.y / acc.n });
  return centroids;
}

/** endpointIsPage reports whether a link endpoint is a knowledge-page node. */
function endpointIsPage(endpoint: SimLink["source"]): boolean {
  return typeof endpoint === "object" && (endpoint as SimNode).page === true;
}

/** Padding that keeps a node's mark AND its label inside the canvas. Labels are
 * centered under the mark and bounded by truncateLabel, so the horizontal pad is
 * the wider of the two. */
const BOUND_PAD_X = 70;
const BOUND_PAD_Y = 26;

/** VELOCITY_DECAY mirrors d3-force's default: a tick multiplies each velocity by
 * this before integrating it into the position. forceBounds must account for it
 * to know where a node will actually land. */
const VELOCITY_DECAY = 0.6;

/**
 * forceBounds confines nodes to the canvas. Without it, repulsion pushes the
 * outermost nodes past the edge and they are simply clipped away — a graph that
 * silently hides part of the corpus is worse than a slightly denser one.
 *
 * It clamps the position a node is about to REACH, not the one it currently
 * holds, and cancels the velocity that would carry it out. A force runs before
 * d3 integrates `x += vx`, so clamping x alone is undone by that same tick and
 * a crowded canvas still throws its outermost nodes off the edge.
 */
function forceBounds(size: { width: number; height: number }) {
  let nodes: SimNode[] = [];
  const force = () => {
    for (const n of nodes) {
      const bx = clampAxis(n.x ?? 0, n.vx ?? 0, BOUND_PAD_X, size.width - BOUND_PAD_X);
      n.x = bx.pos;
      n.vx = bx.vel;
      const by = clampAxis(n.y ?? 0, n.vy ?? 0, BOUND_PAD_Y, size.height - BOUND_PAD_Y);
      n.y = by.pos;
      n.vy = by.vel;
    }
  };
  force.initialize = (ns: SimNode[]) => {
    nodes = ns;
  };
  return force;
}

/** clampAxis keeps `pos + decayed velocity` inside [min, max], stopping the
 * component of the velocity that points out of bounds. */
export function clampAxis(pos: number, vel: number, min: number, max: number) {
  const landing = pos + vel * VELOCITY_DECAY;
  if (landing < min) return { pos: min, vel: 0 };
  if (landing > max) return { pos: max, vel: 0 };
  return { pos, vel };
}

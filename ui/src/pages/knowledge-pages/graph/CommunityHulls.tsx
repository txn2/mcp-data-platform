import { useMemo } from "react";
import { polygonHull } from "d3-polygon";
import { communityFill } from "./graphModel";
import type { SimNode } from "./useForceLayout";

/** HULL_PAD pushes the hull outward from its members so nodes sit inside the
 * region rather than on its edge. */
const HULL_PAD = 26;

/** MIN_HULL_MEMBERS is the smallest cluster worth outlining. A pair has no area
 * and does not read as a topic; outlining every three-node offshoot fills the
 * canvas with regions that say less than the substantial clusters do. */
const MIN_HULL_MEMBERS = 4;

/**
 * CommunityHulls tints the region behind each detected cluster. Type already
 * owns the node colour channel, so clusters are shown as areas the nodes sit
 * inside — the reader sees the topic groupings without losing what each mark IS.
 * Drawn behind the edges, so it never competes with the graph itself.
 */
export function CommunityHulls({
  nodes,
  communities,
  version,
}: {
  nodes: SimNode[];
  communities: Map<string, number>;
  /** Bumped by the layout on every tick. The simulation MUTATES the node objects
   * in place and never replaces the array, so without this the hulls would be
   * computed once from the seed ring and never move again — regions drawn in the
   * middle of the canvas while their members settled elsewhere. */
  version: number;
}) {
  // eslint-disable-next-line react-hooks/exhaustive-deps -- `version` is the mutation signal
  const hulls = useMemo(() => buildHulls(nodes, communities), [nodes, communities, version]);
  return (
    <g aria-hidden="true">
      {hulls.map((h) => (
        <path
          key={h.community}
          d={h.d}
          fill={communityFill(h.community)}
          fillOpacity={0.07}
          stroke={communityFill(h.community)}
          strokeOpacity={0.25}
          strokeWidth={1}
          strokeLinejoin="round"
        />
      ))}
    </g>
  );
}

/** buildHulls computes one padded convex hull per cluster large enough to have one. */
function buildHulls(nodes: SimNode[], communities: Map<string, number>) {
  const byCommunity = new Map<number, [number, number][]>();
  for (const n of nodes) {
    const c = communities.get(n.id);
    if (c === undefined || n.x == null || n.y == null) continue;
    const points = byCommunity.get(c) ?? [];
    points.push([n.x, n.y]);
    byCommunity.set(c, points);
  }

  const hulls: { community: number; d: string }[] = [];
  for (const [community, points] of byCommunity) {
    if (points.length < MIN_HULL_MEMBERS) continue;
    const hull = polygonHull(points);
    if (!hull) continue;
    hulls.push({ community, d: hullPath(padOutward(hull)) });
  }
  return hulls.sort((a, b) => a.community - b.community);
}

/** padOutward pushes each hull vertex away from the hull's centroid, which
 * inflates the region enough to contain the marks without a true polygon offset. */
function padOutward(hull: [number, number][]): [number, number][] {
  const cx = hull.reduce((s, p) => s + p[0], 0) / hull.length;
  const cy = hull.reduce((s, p) => s + p[1], 0) / hull.length;
  return hull.map(([x, y]) => {
    const dx = x - cx;
    const dy = y - cy;
    const len = Math.hypot(dx, dy) || 1;
    return [x + (dx / len) * HULL_PAD, y + (dy / len) * HULL_PAD] as [number, number];
  });
}

/** hullPath renders the polygon as a closed SVG path. */
function hullPath(points: [number, number][]): string {
  return `M${points.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join("L")}Z`;
}

import { useMemo } from "react";
import type { KnowledgeGraphEdge, KnowledgeGraphNode } from "@/api/portal/hooks";
import {
  betweennessCentrality,
  louvainCommunities,
  modularity,
  type Adjacency,
} from "./graphAnalytics";
import { buildGraphIndex, normalize, type GraphIndex } from "./graphModel";

/**
 * MAX_ANALYSED_NODES bounds the O(V*E) betweenness pass. Above it the view falls
 * back to degree for node size and reports that it did, rather than freezing the
 * tab on a pathological corpus or silently showing a different metric under the
 * same label.
 */
export const MAX_ANALYSED_NODES = 900;

/** GraphAnalysis is everything derived from the graph's structure alone. */
export interface GraphAnalysis {
  index: GraphIndex;
  neighbors: Adjacency;
  /** node id -> community index, from Louvain. */
  communities: Map<string, number>;
  /** How many communities the corpus falls into. */
  communityCount: number;
  /** Modularity of that partition: how pronounced the clustering actually is. */
  modularity: number;
  /** node id -> raw betweenness (shortest paths bridged). */
  betweenness: Map<string, number>;
  /** node id -> importance in [0,1], driving node size. */
  weight: Map<string, number>;
  /** The nodes that bridge the most, most first. Empty when nothing bridges. */
  topBridges: string[];
  /** True when the corpus was too large to run betweenness on. */
  degraded: boolean;
}

/**
 * useGraphAnalysis derives the structure of the corpus: its clusters, its
 * bridges, and how strongly clustered it is at all. This is what separates the
 * view from a decorative node diagram — the layout shows that structure exists,
 * these numbers say what it is.
 */
export function useGraphAnalysis(
  nodes: KnowledgeGraphNode[],
  edges: KnowledgeGraphEdge[],
): GraphAnalysis {
  return useMemo(() => {
    const index = buildGraphIndex(nodes, edges);
    const ids = nodes.map((n) => n.id);
    const communities = louvainCommunities(ids, edges);
    const degraded = ids.length > MAX_ANALYSED_NODES;
    const betweenness = degraded
      ? new Map(ids.map((id) => [id, 0]))
      : betweennessCentrality(ids, index.neighbors);

    // Size by what bridges the graph; fall back to raw connectedness when
    // nothing bridges anything (a corpus of disjoint stars) or when the
    // betweenness pass was skipped.
    const bridging = [...betweenness.values()].some((s) => s > 0);
    const weight = normalize(bridging ? betweenness : index.degree);

    const topBridges = [...betweenness.entries()]
      .filter(([, s]) => s > 0)
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([id]) => id);

    return {
      index,
      neighbors: index.neighbors,
      communities,
      communityCount: new Set(communities.values()).size,
      modularity: modularity(ids, edges, communities),
      betweenness,
      weight,
      topBridges,
      degraded,
    };
  }, [nodes, edges]);
}

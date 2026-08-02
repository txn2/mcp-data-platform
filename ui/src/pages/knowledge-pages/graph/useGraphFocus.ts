import { useCallback, useMemo, useState } from "react";
import type { KnowledgeGraphEdge, KnowledgeGraphNode } from "@/api/portal/hooks";
import { neighborhood, pathEdgeKeys, shortestPath, type Adjacency } from "./graphAnalytics";

/** GraphMode is whether the view is exploring around one node or showing the
 * whole corpus at once. */
export type GraphMode = "focus" | "corpus";

/** DEPTHS are the neighbourhood radii the focus view offers. Beyond three hops
 * a corpus this dense is the whole graph again, which is what corpus mode is for. */
export const DEPTHS = [1, 2, 3] as const;

export const DEFAULT_DEPTH = 2;

interface FocusInput {
  nodes: KnowledgeGraphNode[];
  edges: KnowledgeGraphEdge[];
  neighbors: Adjacency;
  /** The corpus's strongest bridges, most first; the first is where the view
   * opens when the reader has not chosen a starting point. */
  topBridges: string[];
}

/**
 * useGraphFocus owns where the reader is looking. The view opens on ONE node and
 * its neighbourhood rather than the whole corpus, because a whole-corpus force
 * layout is a hairball that answers nothing: exploration starts somewhere and
 * grows outward. Corpus mode remains available for the overview.
 */
export function useGraphFocus({ nodes, edges, neighbors, topBridges }: FocusInput) {
  const [mode, setMode] = useState<GraphMode>("focus");
  const [depth, setDepth] = useState<number>(DEFAULT_DEPTH);
  const [chosenFocus, setChosenFocus] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState<string | null>(null);
  // The first end of a path the reader is tracing; the second is the next node
  // they click.
  const [pathFrom, setPathFrom] = useState<string | null>(null);
  const [path, setPath] = useState<string[] | null>(null);

  // Where the view opens: the reader's choice, else the corpus's biggest bridge
  // (the most informative single starting point), else the first node.
  const defaultFocus = topBridges[0] ?? nodes[0]?.id ?? null;
  const focusID = chosenFocus && nodes.some((n) => n.id === chosenFocus) ? chosenFocus : defaultFocus;

  /** The ids drawn: the focus neighbourhood plus one hop out of anything the
   * reader explicitly expanded. Corpus mode draws everything. */
  const visibleIDs = useMemo(() => {
    if (mode === "corpus" || !focusID) return null;
    const ids = neighborhood(neighbors, [focusID], depth);
    for (const id of expanded) {
      if (!ids.has(id)) continue;
      for (const w of neighbors.get(id) ?? []) ids.add(w);
    }
    // A traced path stays whole even where it leaves the neighbourhood, so the
    // answer to "how are these connected" is never half-drawn.
    for (const id of path ?? []) ids.add(id);
    return ids;
  }, [mode, focusID, depth, expanded, neighbors, path]);

  const visible = useMemo(() => {
    if (!visibleIDs) return { nodes, edges };
    return {
      nodes: nodes.filter((n) => visibleIDs.has(n.id)),
      edges: edges.filter((e) => visibleIDs.has(e.source) && visibleIDs.has(e.target)),
    };
  }, [nodes, edges, visibleIDs]);

  // A traced path force-adds its nodes to the visible set, so it must be cleared
  // by any action that redefines what the reader is looking at. Leaving it set
  // pins the whole path on screen and makes Focus and the depth control look
  // broken: the neighbourhood narrows but the drawing does not.
  const focusOn = useCallback((id: string) => {
    setPath(null);
    setPathFrom(null);
    setChosenFocus(id);
    setSelected(id);
    setExpanded(new Set());
    setMode("focus");
  }, []);

  const changeDepth = useCallback((next: number) => {
    setPath(null);
    setDepth(next);
  }, []);

  const changeMode = useCallback((next: GraphMode) => {
    setPath(null);
    setMode(next);
  }, []);

  const expand = useCallback((id: string) => {
    setExpanded((prev) => new Set(prev).add(id));
  }, []);

  const startPath = useCallback((id: string) => {
    setPath(null);
    setPathFrom(id);
  }, []);

  const cancelPath = useCallback(() => {
    setPathFrom(null);
    setPath(null);
  }, []);

  /**
   * selectNode is what a click means. While a path is being traced the click
   * completes it; otherwise it selects the node for the inspector. Selecting
   * never navigates away — leaving the graph to read one page was the reason
   * exploring it cost a round trip per node.
   */
  const selectNode = useCallback(
    (id: string) => {
      if (pathFrom && pathFrom !== id) {
        setPath(shortestPath(neighbors, pathFrom, id) ?? []);
        setPathFrom(null);
        setSelected(id);
        return;
      }
      setSelected(id);
    },
    [pathFrom, neighbors],
  );

  const pathEdges = useMemo(() => pathEdgeKeys(path ?? []), [path]);
  const pathNodes = useMemo(() => new Set(path ?? []), [path]);

  // The selection must survive only as long as its node does. A type filter can
  // remove the selected node from the corpus, and an inspector still describing
  // something no longer in the graph offers actions that silently do nothing.
  const liveSelection = selected && nodes.some((n) => n.id === selected) ? selected : null;

  return {
    mode,
    setMode: changeMode,
    depth,
    setDepth: changeDepth,
    focusID,
    focusOn,
    expanded,
    expand,
    selected: liveSelection ?? focusID,
    selectNode,
    visible,
    pathFrom,
    startPath,
    cancelPath,
    /** The traced path's nodes in order, [] when the two ends are unconnected,
     * null when no path has been traced. */
    path,
    pathNodes,
    pathEdges,
  };
}

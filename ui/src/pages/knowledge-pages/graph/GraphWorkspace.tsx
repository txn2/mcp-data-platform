import { useMemo, useState } from "react";
import { AlertTriangle, RotateCcw, ZoomIn, ZoomOut } from "lucide-react";
import type { KnowledgeGraphNode, KnowledgeGraphResponse } from "@/api/portal/hooks";
import { GraphCanvas } from "./GraphCanvas";
import { GraphInspector } from "./GraphInspector";
import { GraphSummary } from "./GraphSummary";
import { GraphToolbar } from "./GraphToolbar";
import { filterByTypes, matchingNodeIds, nodeDestination, typeFacet } from "./graphModel";
import { useForceLayout } from "./useForceLayout";
import { useGraphAnalysis } from "./useGraphAnalysis";
import { useGraphFocus } from "./useGraphFocus";
import { ZOOM_IN, ZOOM_OUT, useGraphViewport } from "./useGraphViewport";

const CANVAS_HEIGHT = 620;

/** INSPECTOR_WIDTH is the detail pane's width plus the gap, subtracted from the
 * measured container to size the canvas. Mirrors the w-72 on GraphInspector. */
const INSPECTOR_WIDTH = 288 + 12;

/**
 * MIN_HULL_NODES is the size below which cluster regions are noise. Hulls are
 * also drawn only in corpus mode: a focused neighbourhood is a slice through
 * several clusters at once, so their regions overlap into a wash that says less
 * than the inspector's one-line cluster readout does.
 */
const MIN_HULL_NODES = 12;

interface GraphWorkspaceProps {
  data: KnowledgeGraphResponse;
  width: number;
  query: string;
  onOpenPage: (id: string) => void;
  onNavigate?: (path: string) => void;
}

/**
 * GraphWorkspace analyses the corpus and draws it.
 *
 * It opens on ONE node and its neighbourhood, not the whole corpus: a
 * whole-corpus force layout is a hairball that answers nothing, and exploration
 * starts somewhere and grows outward. The starting point is the corpus's
 * strongest bridge, the single most informative place to begin. The structure is
 * measured, not merely drawn — Louvain communities become the cluster regions,
 * betweenness centrality sizes the nodes and names the bridges, and any two
 * nodes can be joined by their shortest chain of references.
 */
export function GraphWorkspace({ data, width, query, onOpenPage, onNavigate }: GraphWorkspaceProps) {
  const [hiddenTypes, setHiddenTypes] = useState<Set<string>>(new Set());
  // Bumping the generation rebuilds the layout, releasing every dragged node.
  const [generation, setGeneration] = useState(0);
  const viewport = useGraphViewport();

  const facet = useMemo(() => typeFacet(data.nodes), [data.nodes]);
  const corpus = useMemo(
    () => filterByTypes(data.nodes, data.edges, hiddenTypes),
    [data.nodes, data.edges, hiddenTypes],
  );
  const analysis = useGraphAnalysis(corpus.nodes, corpus.edges);
  const focus = useGraphFocus({
    nodes: corpus.nodes,
    edges: corpus.edges,
    neighbors: analysis.neighbors,
    topBridges: analysis.topBridges,
  });

  // Keyed on the FILTERED corpus: a node the type filter removed is no longer
  // inspectable, so the pane stops describing something that is not in the graph.
  const nodeByID = useMemo(() => new Map(corpus.nodes.map((n) => [n.id, n])), [corpus.nodes]);
  // Search the whole corpus, not just what is drawn. The canvas can only ring
  // the matches it contains, but the count must be the truth about the corpus —
  // reporting "0 matching" for a page that is loaded and simply outside the
  // current neighbourhood is a lie the reader cannot see through.
  const matches = useMemo(() => matchingNodeIds(corpus.nodes, query), [corpus.nodes, query]);
  const visibleMatches = useMemo(
    () => new Set(focus.visible.nodes.filter((n) => matches.has(n.id)).map((n) => n.id)),
    [focus.visible.nodes, matches],
  );
  const canvasWidth = Math.max(320, width - INSPECTOR_WIDTH);
  const layout = useForceLayout(
    focus.visible.nodes,
    focus.visible.edges,
    analysis.weight,
    analysis.communities,
    { width: canvasWidth, height: CANVAS_HEIGHT },
    generation,
  );

  const openNode = (node: KnowledgeGraphNode) => {
    const dest = nodeDestination(node);
    if (!dest) return;
    if (dest.kind === "page") onOpenPage(dest.id);
    else onNavigate?.(dest.path);
  };

  const toggleType = (type: string) =>
    setHiddenTypes((prev) => {
      const next = new Set(prev);
      if (!next.delete(type)) next.add(type);
      return next;
    });

  return (
    <div className="space-y-3">
      <GraphToolbar
        facet={facet}
        hiddenTypes={hiddenTypes}
        onToggleType={toggleType}
        mode={focus.mode}
        onModeChange={focus.setMode}
        depth={focus.depth}
        onDepthChange={focus.setDepth}
      />

      <div className="flex flex-wrap items-center gap-1.5">
        <GraphSummary
          analysis={analysis}
          visibleNodes={focus.visible.nodes.length}
          visibleEdges={focus.visible.edges.length}
          corpusNodes={corpus.nodes.length}
          mode={focus.mode}
          focusLabel={nodeByID.get(focus.focusID ?? "")?.label}
          matchCount={matches.size}
          matchesInView={visibleMatches.size}
          queryText={query.trim()}
        />
        <div className="ml-auto flex items-center gap-1.5">
          <ViewportButton
            label="Zoom in"
            onClick={() => viewport.zoomBy(ZOOM_IN, canvasWidth / 2, CANVAS_HEIGHT / 2)}
          >
            <ZoomIn className="h-3.5 w-3.5" />
          </ViewportButton>
          <ViewportButton
            label="Zoom out"
            onClick={() => viewport.zoomBy(ZOOM_OUT, canvasWidth / 2, CANVAS_HEIGHT / 2)}
          >
            <ZoomOut className="h-3.5 w-3.5" />
          </ViewportButton>
          <ViewportButton label="Fit" onClick={viewport.reset}>
            Fit
          </ViewportButton>
          <ViewportButton label="Reset layout" onClick={() => setGeneration((g) => g + 1)}>
            <RotateCcw className="h-3.5 w-3.5" /> Reset layout
          </ViewportButton>
        </div>
      </div>

      {data.truncated && data.notice && (
        <p className="flex items-start gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" />
          <span>{data.notice}</span>
        </p>
      )}

      <div className="flex gap-3">
        <GraphCanvas
          nodes={layout.nodes}
          links={layout.links}
          version={layout.version}
          neighbors={analysis.neighbors}
          highlight={{
            matches: visibleMatches,
            selected: focus.selected,
            pathNodes: focus.pathNodes,
            pathEdges: focus.pathEdges,
            communities: analysis.communities,
            showHulls:
              focus.mode === "corpus" &&
              analysis.communityCount > 1 &&
              focus.visible.nodes.length >= MIN_HULL_NODES,
          }}
          width={canvasWidth}
          height={CANVAS_HEIGHT}
          viewport={viewport}
          onDragNode={layout.dragTo}
          onSelect={focus.selectNode}
        />
        <GraphInspector
          node={focus.selected ? (nodeByID.get(focus.selected) ?? null) : null}
          analysis={analysis}
          nodeByID={nodeByID}
          path={focus.path}
          tracingFrom={focus.pathFrom}
          onSelect={focus.selectNode}
          onFocus={focus.focusOn}
          onExpand={focus.expand}
          onStartPath={focus.startPath}
          onCancelPath={focus.cancelPath}
          onOpen={openNode}
        />
      </div>

      <p className="text-xs text-muted-foreground">
        Drag the canvas to pan and scroll to zoom. Drag a node to pull its cluster out; it stays
        where you drop it. Click a node to inspect it, and use the inspector to re-centre on it,
        pull in its neighbours, trace a path to another node, or open it.
      </p>
    </div>
  );
}

/** ViewportButton is one pan/zoom control in the toolbar row. */
function ViewportButton({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      onClick={onClick}
      className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 text-xs font-medium text-muted-foreground hover:bg-muted"
    >
      {children}
    </button>
  );
}

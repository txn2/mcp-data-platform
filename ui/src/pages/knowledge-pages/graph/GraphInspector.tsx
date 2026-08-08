import { ArrowUpRight, Crosshair, Plus, Route, X } from "lucide-react";
import type { KnowledgeGraphNode } from "@/api/portal/hooks";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  bridgeRank,
  communityFill,
  nodeDestination,
  nodeStyle,
  typeLabel,
} from "./graphModel";
import { CatalogNodeDetail } from "./CatalogNodeDetail";
import type { GraphAnalysis } from "./useGraphAnalysis";

interface GraphInspectorProps {
  node: KnowledgeGraphNode | null;
  analysis: GraphAnalysis;
  /** Resolves a node id back to its node, for the neighbour lists. */
  nodeByID: Map<string, KnowledgeGraphNode>;
  /** The traced path, or null when none. Empty means the two ends do not connect. */
  path: string[] | null;
  /** Set while the reader is choosing the far end of a path. */
  tracingFrom: string | null;
  onSelect: (id: string) => void;
  onFocus: (id: string) => void;
  onExpand: (id: string) => void;
  onStartPath: (id: string) => void;
  onCancelPath: () => void;
  onOpen: (node: KnowledgeGraphNode) => void;
}

/**
 * GraphInspector is the detail pane for the selected node: what it is, how it
 * sits in the corpus (its cluster and how much it bridges), and what it
 * references in each direction. It exists so reading the graph does not mean
 * leaving it — every neighbour is one click away in place, and opening the
 * underlying page is a deliberate act rather than the only thing a click can do.
 */
export function GraphInspector(props: GraphInspectorProps) {
  const { node, analysis, path, tracingFrom } = props;
  if (!node) {
    return (
      <Card asChild className="w-72 shrink-0 p-4 text-sm text-muted-foreground">
        <aside>Select a node to inspect it.</aside>
      </Card>
    );
  }

  const cites = [...(analysis.index.cites.get(node.id) ?? [])];
  const citedBy = [...(analysis.index.citedBy.get(node.id) ?? [])];

  return (
    <Card asChild className="w-72 shrink-0 gap-3 overflow-y-auto p-4">
      <aside>
        <InspectorHeader node={node} />

        {/* A catalog node's label is derived from its URN, so it is neither the
            entity's name nor something the catalog can be searched for. Resolve
            it against the catalog and show what is actually there. */}
        {node.type === "datahub" && <CatalogNodeDetail urn={node.id} />}

        <InspectorStats
          node={node}
          analysis={analysis}
          citeCount={cites.length}
          citedByCount={citedBy.length}
        />

        {tracingFrom === node.id ? (
          <TracingPrompt onCancel={props.onCancelPath} />
        ) : (
          path && <PathReadout path={path} nodeByID={props.nodeByID} onSelect={props.onSelect} />
        )}

        <div className="flex flex-wrap gap-1.5">
          <InspectorAction icon={Crosshair} label="Focus" onClick={() => props.onFocus(node.id)} />
          <InspectorAction icon={Plus} label="Expand" onClick={() => props.onExpand(node.id)} />
          <InspectorAction icon={Route} label="Path from" onClick={() => props.onStartPath(node.id)} />
          {nodeDestination(node) && (
            <InspectorAction icon={ArrowUpRight} label="Open" onClick={() => props.onOpen(node)} />
          )}
        </div>

        <NeighbourList title="References" ids={cites} nodeByID={props.nodeByID} onSelect={props.onSelect} />
        <NeighbourList
          title="Referenced by"
          ids={citedBy}
          nodeByID={props.nodeByID}
          onSelect={props.onSelect}
        />
      </aside>
    </Card>
  );
}

/** InspectorHeader names the node and its type, flagging a removed target. */
function InspectorHeader({ node }: { node: KnowledgeGraphNode }) {
  const style = nodeStyle(node.type);
  return (
    <header>
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <span
          className="inline-block h-2.5 w-2.5 shrink-0"
          style={{
            backgroundColor: style.fill,
            borderRadius: style.shape === "circle" ? "9999px" : "2px",
          }}
        />
        {typeLabel(node.type)}
        {!node.exists && <span className="text-destructive">· removed</span>}
      </div>
      <h3 className="mt-1 text-sm font-semibold leading-snug text-foreground">{node.label}</h3>
      {/* The identifier the reference actually stores. For a catalog or
          connection node the label above is derived from it, so without this the
          reader cannot tell what the node IS, copy it, or look it up anywhere
          else. Selectable and wrapped rather than truncated. */}
      {!node.page && (
        <p className="mt-1 select-text break-all font-mono text-[10px] leading-tight text-muted-foreground">
          {node.id}
        </p>
      )}
    </header>
  );
}

/**
 * InspectorStats is where the node sits in the corpus: how many references run
 * each way, how much of the graph it bridges, and which cluster it belongs to.
 * The bridge figure is the one a card list cannot give at all.
 */
function InspectorStats({
  node,
  analysis,
  citeCount,
  citedByCount,
}: {
  node: KnowledgeGraphNode;
  analysis: GraphAnalysis;
  citeCount: number;
  citedByCount: number;
}) {
  const community = analysis.communities.get(node.id);
  return (
    <dl className="space-y-1.5 text-xs">
      <Stat label="References out" value={String(citeCount)} />
      <Stat label="Referenced by" value={String(citedByCount)} />
      <BridgeStat node={node} analysis={analysis} />
      {community !== undefined && analysis.communityCount > 1 && (
        <Stat
          label="Cluster"
          value={`#${community + 1} of ${analysis.communityCount}`}
          swatch={communityFill(community)}
        />
      )}
    </dl>
  );
}

/**
 * BridgeStat reports how much of the graph runs through this node. When the
 * corpus was too large to rank, it says the measurement was not taken rather
 * than reporting the placeholder zero as "bridges nothing" — a hub would
 * otherwise be labelled the exact opposite of what it is.
 */
function BridgeStat({ node, analysis }: { node: KnowledgeGraphNode; analysis: GraphAnalysis }) {
  if (analysis.degraded) {
    return (
      <Stat
        label="Bridges"
        value="not measured"
        hint="This corpus is too large to rank bridges; narrow it with a tag or a type filter."
      />
    );
  }
  const raw = analysis.betweenness.get(node.id) ?? 0;
  const rank = bridgeRank(raw, analysis.betweenness.values());
  if (!rank) {
    return (
      <Stat
        label="Bridges"
        value="nothing"
        hint="No shortest path between two other nodes runs through this one."
      />
    );
  }
  const score = Math.round(raw);
  const band = rank.strongest ? "strongest in view" : `top ${rank.percentile}%`;
  return (
    <Stat
      label="Bridges"
      value={`${score} path${score === 1 ? "" : "s"} · ${band}`}
      hint={`Shortest paths between other nodes that pass through this one. Ranked ${rank.rank} of ${rank.total} nodes that bridge anything.`}
    />
  );
}

/** TracingPrompt tells the reader the graph is waiting for the path's far end. */
function TracingPrompt({ onCancel }: { onCancel: () => void }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-primary/40 bg-primary/5 px-2.5 py-2 text-xs">
      <Route className="mt-px size-3.5 shrink-0 text-primary" />
      <span>Click another node to trace the shortest path to it.</span>
      <Button
        variant="ghost"
        size="icon-xs"
        className="-my-1 ml-auto"
        onClick={onCancel}
        aria-label="Cancel path"
      >
        <X />
      </Button>
    </div>
  );
}

/** Stat is one labelled figure in the inspector's summary. */
function Stat({
  label,
  value,
  hint,
  swatch,
}: {
  label: string;
  value: string;
  hint?: string;
  swatch?: string;
}) {
  return (
    <div className="flex items-baseline justify-between gap-2" title={hint}>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="flex items-center gap-1.5 font-medium text-foreground">
        {swatch && (
          <span className="inline-block h-2 w-2 rounded-full" style={{ backgroundColor: swatch }} />
        )}
        {value}
      </dd>
    </div>
  );
}

/** InspectorAction is one of the pane's verbs. */
function InspectorAction({
  icon: Icon,
  label,
  onClick,
}: {
  icon: typeof Crosshair;
  label: string;
  onClick: () => void;
}) {
  return (
    <Button variant="outline" size="xs" className="text-muted-foreground" onClick={onClick}>
      <Icon /> {label}
    </Button>
  );
}

/** PathReadout lists the hops of a traced path, or says there are none. */
function PathReadout({
  path,
  nodeByID,
  onSelect,
}: {
  path: string[];
  nodeByID: Map<string, KnowledgeGraphNode>;
  onSelect: (id: string) => void;
}) {
  if (path.length === 0) {
    return (
      <p className="rounded-md border border-border bg-muted/50 px-2.5 py-2 text-xs text-muted-foreground">
        Those two are not connected by any chain of references.
      </p>
    );
  }
  return (
    <div className="rounded-md border border-border bg-muted/50 px-2.5 py-2">
      <p className="mb-1 text-xs font-medium text-foreground">
        {path.length - 1} hop{path.length === 2 ? "" : "s"} apart
      </p>
      <ol className="space-y-0.5">
        {path.map((id, i) => (
          <li key={id} className="flex items-baseline gap-1 truncate text-xs text-muted-foreground">
            <span className="opacity-60">{i + 1}.</span>
            <Button
              variant="link"
              size="xs"
              className="h-auto truncate p-0 font-normal text-muted-foreground hover:text-foreground"
              onClick={() => onSelect(id)}
            >
              {nodeByID.get(id)?.label ?? id}
            </Button>
          </li>
        ))}
      </ol>
    </div>
  );
}

/** NEIGHBOUR_LIMIT caps a neighbour list so one hub cannot make the pane
 * unscrollably long; the count in the heading always states the true total. */
const NEIGHBOUR_LIMIT = 12;

/** NeighbourList is one direction of a node's references, each selectable in place. */
function NeighbourList({
  title,
  ids,
  nodeByID,
  onSelect,
}: {
  title: string;
  ids: string[];
  nodeByID: Map<string, KnowledgeGraphNode>;
  onSelect: (id: string) => void;
}) {
  if (ids.length === 0) return null;
  const shown = ids.slice(0, NEIGHBOUR_LIMIT);
  return (
    <section>
      <h4 className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {title} ({ids.length})
      </h4>
      <ul className="space-y-0.5">
        {shown.map((id) => (
          <li key={id}>
            <Button
              variant="ghost"
              size="xs"
              className="h-auto w-full justify-start gap-1.5 truncate px-1 py-0.5 font-normal text-muted-foreground"
              onClick={() => onSelect(id)}
            >
              <span
                className="inline-block h-2 w-2 shrink-0"
                style={{
                  backgroundColor: nodeStyle(nodeByID.get(id)?.type ?? "").fill,
                  borderRadius: nodeStyle(nodeByID.get(id)?.type ?? "").shape === "circle" ? "9999px" : "2px",
                }}
              />
              <span className="truncate">{nodeByID.get(id)?.label ?? id}</span>
            </Button>
          </li>
        ))}
      </ul>
      {ids.length > shown.length && (
        <p className="mt-0.5 px-1 text-xs text-muted-foreground opacity-70">
          and {ids.length - shown.length} more
        </p>
      )}
    </section>
  );
}

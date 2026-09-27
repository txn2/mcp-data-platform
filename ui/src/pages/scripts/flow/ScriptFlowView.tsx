import { useEffect, useMemo, useState } from "react";
import { useScriptFlow, type FlowNode, type ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import { Alert, AlertDescription } from "@/components/ui/alert";
import type { SelectedLines } from "@/lib/codemirrorLines";
import { FlowCanvas } from "./FlowCanvas";
import { layoutFlow, type FlowLayout } from "./flowLayout";
import { lit, nodeLines, type Selection } from "./flowModel";
import { FlowSidePanel } from "./FlowSidePanel";

// ScriptFlowView is the Flow tab (#1906): one version of a script drawn as the
// work it does, derived by the platform from the source. The diagram is of the
// saved version; an edit on the Source tab shows here once it is saved.

interface Props {
  scriptId: string;
  version: number;
  source: string;
  /** sourceSelection is the lines selected on the Source tab, whose cards are
   * marked here. */
  sourceSelection: SelectedLines | null;
  /** onShowLines switches to Source with these lines marked. */
  onShowLines: (lines: number[]) => void;
}

export function ScriptFlowView({ scriptId, version, source, sourceSelection, onShowLines }: Props) {
  const { data, isLoading, error } = useScriptFlow(scriptId, version);
  if (isLoading) return <p className="text-sm text-muted-foreground">Reading the script…</p>;
  if (error || !data) {
    return (
      <Alert variant="destructive">
        <AlertDescription>The flow of this script could not be read.</AlertDescription>
      </Alert>
    );
  }
  if (!data.ok) return <FlowFindings graph={data} />;
  if (data.nodes.length === 0) {
    return (
      <p className="text-sm text-muted-foreground" data-testid="flow-empty">
        This script makes no platform calls: it reads nothing, writes nothing and produces no
        output, so there is nothing to draw.
      </p>
    );
  }
  return (
    <LaidOutFlow
      graph={data}
      source={source}
      sourceSelection={sourceSelection}
      onShowLines={onShowLines}
    />
  );
}

// FlowFindings is a source that does not parse: what is wrong with it, in place
// of a diagram, since an empty canvas would read as a script that does nothing.
function FlowFindings({ graph }: { graph: ScriptFlow }) {
  return (
    <div className="space-y-2" data-testid="flow-findings">
      <p className="text-sm">
        Version {graph.version} does not parse, so it cannot be drawn. Fix these on the Source tab:
      </p>
      <ul className="space-y-1 text-sm">
        {graph.findings.map((f, i) => (
          <li key={i} className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2">
            {f.line ? <span className="font-mono text-xs text-muted-foreground">line {f.line}: </span> : null}
            {f.message}
            {f.hint && <div className="mt-1 text-xs text-muted-foreground">{f.hint}</div>}
          </li>
        ))}
      </ul>
    </div>
  );
}

function LaidOutFlow({
  graph,
  source,
  sourceSelection,
  onShowLines,
}: {
  graph: ScriptFlow;
  source: string;
  sourceSelection: SelectedLines | null;
  onShowLines: (lines: number[]) => void;
}) {
  const [layout, setLayout] = useState<{ graph: ScriptFlow; layout: FlowLayout } | null>(null);
  const [failed, setFailed] = useState(false);
  const [selection, setSelection] = useState<Selection>(null);
  // Lines selected in Source replace whatever was picked on the diagram: the
  // newest question the reader asked is which cards those lines produced.
  const [seenLines, setSeenLines] = useState(sourceSelection);
  if (seenLines !== sourceSelection) {
    setSeenLines(sourceSelection);
    if (sourceSelection) setSelection(null);
  }

  useEffect(() => {
    let live = true;
    layoutFlow(graph).then(
      (l) => live && setLayout({ graph, layout: l }),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, [graph]);

  const lighted = useMemo(() => lit(graph, selection, sourceSelection), [graph, selection, sourceSelection]);
  const open = (n: FlowNode) => onShowLines(nodeLines(n).flatMap((r) => range(r.from, r.to)));

  if (failed) {
    return (
      <Alert variant="destructive">
        <AlertDescription>The diagram could not be laid out.</AlertDescription>
      </Alert>
    );
  }
  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]" data-testid="script-flow">
      <div className="min-w-0 space-y-2">
        {graph.truncated && (
          <p className="text-xs text-muted-foreground">
            This script expands into more steps than one diagram draws; the first{" "}
            {graph.nodes.length} are shown.
          </p>
        )}
        {layout?.graph === graph ? (
          <FlowCanvas
            layout={layout.layout}
            selection={selection}
            lit={lighted}
            onSelect={setSelection}
            onOpen={open}
          />
        ) : (
          <p className="text-sm text-muted-foreground">Laying out the diagram…</p>
        )}
      </div>
      <FlowSidePanel
        graph={graph}
        source={source}
        selection={selection}
        onSelect={setSelection}
        onShowLines={onShowLines}
      />
    </div>
  );
}

function range(from: number, to: number): number[] {
  const out: number[] = [];
  for (let i = from; i <= to; i++) out.push(i);
  return out;
}

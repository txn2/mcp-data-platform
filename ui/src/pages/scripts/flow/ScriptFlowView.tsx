import { useEffect, useMemo, useState } from "react";
import {
  useScriptFlow,
  useScriptRunFlow,
  type FlowNode,
  type ScriptFlow,
  type ScriptRunFlow,
} from "@/api/portal/hooks/scriptFlow";
import type { ScriptRun } from "@/api/portal/hooks/scripts";
import { useScriptRuns } from "@/api/portal/hooks/scriptRuns";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { formatWhen } from "../runFormat";
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
  /** onShowLines switches to Source with these lines marked; absent where
   * there is no Source beside the diagram. */
  onShowLines?: (lines: number[]) => void;
  /** compareWith draws the version marked with what changed since this older
   * version (#1908). */
  compareWith?: number;
  /** owned offers the run picker (#1907): the runs are the owner's and an
   * administrator's to read. */
  owned?: boolean;
}

export function ScriptFlowView(props: Props) {
  const { scriptId, owned, compareWith } = props;
  // A comparison draws no run. The query is off for it, but a disabled query
  // still answers from the cache the Flow tab filled, so the list is read only
  // where runs are drawn.
  const drawsRuns = !!owned && !compareWith;
  const runs = useScriptRuns(scriptId, drawsRuns);
  // undefined is "the latest run", which is what the tab opens on; null is
  // the saved version with no run drawn.
  const [picked, setPicked] = useState<string | null | undefined>(undefined);
  const list = drawsRuns ? (runs.data?.data ?? []) : [];
  const runId = picked === undefined ? (list[0]?.id ?? null) : picked;
  const runFlow = useScriptRunFlow(scriptId, runId);
  return (
    <div className="space-y-3">
      {list.length > 0 && (
        <RunPicker runs={list} value={runId} onChange={setPicked} />
      )}
      {runId ? <RunFlow {...props} query={runFlow} /> : <VersionFlow {...props} />}
    </div>
  );
}

// RunPicker chooses the run drawn on the diagram (#1907), from the run history
// the Runs section lists, or none.
function RunPicker({
  runs,
  value,
  onChange,
}: {
  runs: ScriptRun[];
  value: string | null;
  onChange: (id: string | null) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <span className="text-xs text-muted-foreground">Run</span>
      <Select value={value ?? NO_RUN} onValueChange={(v) => onChange(v === NO_RUN ? null : v)}>
        <SelectTrigger aria-label="Run drawn on the diagram" className="h-8 w-auto min-w-64">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={NO_RUN}>No run: the saved version</SelectItem>
          {runs.map((r) => (
            <SelectItem key={r.id} value={r.id}>
              v{r.version} · {r.status} · {formatWhen(r.finished_at ?? r.started_at ?? r.fire_time)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

const NO_RUN = "none";

// RunFlow is one run drawn on the diagram of the version it executed, which
// may be older than the version that runs now.
function RunFlow(props: Props & { query: ReturnType<typeof useScriptRunFlow> }) {
  const { data, isLoading, error } = props.query;
  if (isLoading) return <p className="text-sm text-muted-foreground">Reading the run…</p>;
  if (error || !data) {
    return (
      <Alert variant="destructive">
        <AlertDescription>This run could not be drawn.</AlertDescription>
      </Alert>
    );
  }
  if (!data.graph.ok) return <FlowFindings graph={data.graph} />;
  return (
    <LaidOutFlow
      graph={data.graph}
      run={data}
      source={data.version === props.version ? props.source : ""}
      sourceSelection={props.sourceSelection}
      onShowLines={data.version === props.version ? props.onShowLines : undefined}
    />
  );
}

// VersionFlow is the saved version's diagram, or a comparison of it.
function VersionFlow({ scriptId, version, source, sourceSelection, onShowLines, compareWith }: Props) {
  const { data, isLoading, error } = useScriptFlow(scriptId, version, compareWith);
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
  run,
}: {
  graph: ScriptFlow;
  source: string;
  sourceSelection: SelectedLines | null;
  onShowLines?: (lines: number[]) => void;
  run?: ScriptRunFlow;
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

  const runNodes = run?.nodes;
  useEffect(() => {
    let live = true;
    layoutFlow(graph, runNodes).then(
      (l) => live && setLayout({ graph, layout: l }),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, [graph, runNodes]);

  const lighted = useMemo(() => lit(graph, selection, sourceSelection), [graph, selection, sourceSelection]);
  const open = (n: FlowNode) => onShowLines?.(nodeLines(n).flatMap((r) => range(r.from, r.to)));

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
            run={runNodes}
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
        run={run}
      />
    </div>
  );
}

function range(from: number, to: number): number[] {
  const out: number[] = [];
  for (let i = from; i <= to; i++) out.push(i);
  return out;
}

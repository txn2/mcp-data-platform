import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight, Maximize, Minimize } from "lucide-react";
import {
  useScriptFlow,
  useScriptRunFlow,
  type FlowNode,
  type ScriptFlow,
  type ScriptRunFlow,
} from "@/api/portal/hooks/scriptFlow";
import { usePortalScriptVersions, type ScriptRun } from "@/api/portal/hooks/scripts";
import { RUN_PAGE_SIZE, useScriptRunPage } from "@/api/portal/hooks/scriptRuns";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { SelectedLines } from "@/lib/codemirrorLines";
import { runPickerLabel } from "../runFormat";
import { FlowCanvas } from "./FlowCanvas";
import { layoutFlow, type FlowLayout } from "./flowLayout";
import { lit, nodeLines, type Selection } from "./flowModel";
import { FlowSidePanel } from "./FlowSidePanel";
import { FLOW_VIEWS, initialView, rememberView, type FlowView } from "./flowView";
import { StructureCanvas } from "./StructureCanvas";
import { layoutStructure, type StructureLayout } from "./structureLayout";
import { defaultFolded, fold, neighbors } from "./structureModel";
import { TimelineView } from "./TimelineView";

// ScriptFlowView is the Flow tab (#1906, #1972): one version of a script drawn
// three ways, all derived by the platform from the source. Structure is the
// order it runs in, from Start to its exits; Calls is which call's result
// feeds which; Timeline is a run's calls placed in time. The diagram is of the
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
  // A comparison draws no run.
  const drawsRuns = !!owned && !compareWith;
  const runs = useRunPicking(scriptId, drawsRuns);
  const runFlow = useScriptRunFlow(scriptId, runs.runId);
  const [view, setView] = useState<FlowView>(() => initialView());
  const [full, setFull] = useState(false);
  useEscape(full, setFull);
  const shownView: FlowView = view === "timeline" && !runs.runId ? "structure" : view;

  const toolbar = (
    <div className="flex flex-wrap items-center gap-2" data-testid="flow-toolbar">
      {drawsRuns && runs.hasHistory && <RunPicker {...runs} value={runs.runId} />}
      <ViewSwitch
        value={shownView}
        hasRun={!!runs.runId}
        onChange={(v) => {
          setView(v);
          rememberView(v);
        }}
      />
      <FullScreenButton full={full} onToggle={() => setFull(!full)} />
    </div>
  );
  return (
    <FullScreen full={full} toolbar={toolbar}>
      {runs.runId ? (
        <RunFlow {...props} query={runFlow} view={shownView} full={full} />
      ) : (
        <VersionFlow {...props} view={shownView} full={full} />
      )}
    </FullScreen>
  );
}

// useRunPicking is the run picker's state (#1907, #1972): the page of run
// history it lists, its status filter, and the run drawn, which is the latest
// until the reader picks one (null is the saved version with no run).
function useRunPicking(scriptId: string, drawsRuns: boolean) {
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState("");
  const history = useScriptRunPage(scriptId, drawsRuns, page, status);
  const [picked, setPicked] = useState<string | null | undefined>(undefined);
  const data = drawsRuns ? history.data : undefined;
  const runs = data?.data ?? [];
  const total = data?.total ?? 0;
  const latest = runs[0]?.id ?? null;
  return {
    runs,
    total,
    page,
    status,
    hasHistory: total > 0 || status !== "",
    runId: picked === undefined ? latest : picked,
    onChange: setPicked,
    onPage: (p: number) => {
      setPage(p);
      setPicked(undefined);
    },
    onStatus: (s: string) => {
      setStatus(s);
      setPage(1);
      setPicked(undefined);
    },
  };
}

// useEscape turns active off when Escape is pressed while it is on.
function useEscape(active: boolean, setActive: (on: boolean) => void) {
  useEffect(() => {
    if (!active) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setActive(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [active, setActive]);
}

// FullScreenButton enters and leaves full screen.
function FullScreenButton({ full, onToggle }: { full: boolean; onToggle: () => void }) {
  return (
    <Button type="button" variant="outline" size="sm" className="h-8" onClick={onToggle} aria-pressed={full}>
      {full ? <Minimize /> : <Maximize />}
      {full ? "Leave full screen" : "Full screen"}
    </Button>
  );
}

// FullScreen presents the toolbar and the diagram with its detail panel over
// the whole window (#1972), or in place. Both are one element tree, so
// entering and leaving full screen keeps the layout and the selection.
function FullScreen({ full, toolbar, children }: { full: boolean; toolbar: ReactNode; children: ReactNode }) {
  return (
    <div
      className={full ? "fixed inset-0 z-50 flex flex-col gap-3 bg-background p-4" : "space-y-3"}
      role={full ? "dialog" : undefined}
      aria-modal={full ? true : undefined}
      aria-label={full ? "Flow, full screen" : undefined}
      data-testid={full ? "flow-full-screen" : undefined}
    >
      {toolbar}
      <div className={full ? "min-h-0 flex-1" : undefined}>{children}</div>
    </div>
  );
}

// ViewSwitch chooses the picture: Structure, Calls, or Timeline, which needs a
// run to place in time.
function ViewSwitch({ value, hasRun, onChange }: { value: FlowView; hasRun: boolean; onChange: (v: FlowView) => void }) {
  return (
    <div role="radiogroup" aria-label="View" className="inline-flex h-8 items-center rounded-md border bg-muted/40 p-0.5">
      {FLOW_VIEWS.map((v) => {
        const disabled = v.value === "timeline" && !hasRun;
        const on = value === v.value;
        return (
          <button
            key={v.value}
            type="button"
            role="radio"
            aria-checked={on}
            disabled={disabled}
            title={disabled ? "Pick a run to place its calls in time" : undefined}
            onClick={() => onChange(v.value)}
            className={`h-full rounded px-3 text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
              on ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {v.label}
          </button>
        );
      })}
    </div>
  );
}

const STATUSES = [
  { value: "", label: "All runs" },
  { value: "failed", label: "Failed" },
  { value: "succeeded", label: "Succeeded" },
];

// RunPicker chooses the run drawn on the diagram (#1907), a page of the run
// history at a time (#1972), or none.
function RunPicker({
  runs,
  value,
  page,
  total,
  status,
  onChange,
  onPage,
  onStatus,
}: {
  runs: ScriptRun[];
  value: string | null;
  page: number;
  total: number;
  status: string;
  onChange: (id: string | null) => void;
  onPage: (page: number) => void;
  onStatus: (status: string) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / RUN_PAGE_SIZE));
  const first = total === 0 ? 0 : (page - 1) * RUN_PAGE_SIZE + 1;
  const last = Math.min(total, page * RUN_PAGE_SIZE);
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <span className="text-xs text-muted-foreground">Run</span>
      <Select value={value ?? NO_RUN} onValueChange={(v) => onChange(v === NO_RUN ? null : v)}>
        <SelectTrigger aria-label="Run drawn on the diagram" className="h-8 w-auto min-w-72">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={NO_RUN}>No run: the saved version</SelectItem>
          {runs.map((r) => (
            <SelectItem key={r.id} value={r.id}>
              {runPickerLabel(r)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select value={status || ALL} onValueChange={(v) => onStatus(v === ALL ? "" : v)}>
        <SelectTrigger aria-label="Runs listed" className="h-8 w-auto">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {STATUSES.map((s) => (
            <SelectItem key={s.label} value={s.value || ALL}>
              {s.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <span className="text-xs text-muted-foreground" data-testid="run-page">
        {total === 0 ? "no runs" : `${first}–${last} of ${total}`}
      </span>
      <Button
        type="button"
        variant="outline"
        size="icon-sm"
        aria-label="Newer runs"
        disabled={page <= 1}
        onClick={() => onPage(page - 1)}
      >
        <ChevronLeft />
      </Button>
      <Button
        type="button"
        variant="outline"
        size="icon-sm"
        aria-label="Older runs"
        disabled={page >= pages}
        onClick={() => onPage(page + 1)}
      >
        <ChevronRight />
      </Button>
    </div>
  );
}

const NO_RUN = "none";
const ALL = "all";

interface ViewProps extends Props {
  view: FlowView;
  full: boolean;
}

// RunFlow is one run drawn on the diagram of the version it executed, which
// may be older than the version that runs now.
function RunFlow(props: ViewProps & { query: ReturnType<typeof useScriptRunFlow> }) {
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
      scriptId={props.scriptId}
      graph={data.graph}
      run={data}
      view={props.view}
      full={props.full}
      source={data.version === props.version ? props.source : ""}
      sourceSelection={props.sourceSelection}
      onShowLines={data.version === props.version ? props.onShowLines : undefined}
    />
  );
}

// VersionFlow is the saved version's diagram, or a comparison of it.
function VersionFlow({ scriptId, version, source, sourceSelection, onShowLines, compareWith, view, full }: ViewProps) {
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
  if (data.nodes.length === 0 && data.structure.nodes.length <= 2) {
    return (
      <p className="text-sm text-muted-foreground" data-testid="flow-empty">
        This script makes no platform calls: it reads nothing, writes nothing and produces no
        output, so there is nothing to draw.
      </p>
    );
  }
  return (
    <LaidOutFlow
      scriptId={scriptId}
      graph={data}
      view={view}
      full={full}
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

// useMissedLines is the lines the drawn version's tests do not reach, from the
// report its save kept (#1972); undefined when it kept none.
function useMissedLines(scriptId: string, version: number): Set<number> | undefined {
  const versions = usePortalScriptVersions(scriptId, true);
  return useMemo(() => {
    const v = versions.data?.data.find((x) => x.version === version);
    return v?.tests ? new Set(v.tests.coverage.missed_lines) : undefined;
  }, [versions.data, version]);
}

type Laid =
  | { view: "calls"; graph: ScriptFlow; layout: FlowLayout }
  | { view: "structure"; graph: ScriptFlow; folded: Set<string>; layout: StructureLayout };

interface LaidOutProps {
  scriptId: string;
  graph: ScriptFlow;
  source: string;
  sourceSelection: SelectedLines | null;
  onShowLines?: (lines: number[]) => void;
  run?: ScriptRunFlow;
  view: FlowView;
  full: boolean;
}

function LaidOutFlow(props: LaidOutProps) {
  const { graph, run, view, full, source, onShowLines } = props;
  const [folded, toggleFold] = useFolds(graph, run);
  const [selection, setSelection] = useSelection(props.sourceSelection);
  const structureView = useMemo(() => fold(graph.structure, folded), [graph.structure, folded]);
  const { laid, failed } = useLayout(view, graph, run?.nodes, structureView, folded);
  const missed = useMissedLines(props.scriptId, graph.version);

  if (failed) {
    return (
      <Alert variant="destructive">
        <AlertDescription>The diagram could not be laid out.</AlertDescription>
      </Alert>
    );
  }
  return (
    <div className={`grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px] ${full ? "h-full" : ""}`} data-testid="script-flow" data-view={view}>
      <div className={`min-w-0 space-y-2 ${full ? "flex h-full flex-col" : ""}`}>
        {run?.unplaced && <UnplacedNotice />}
        <TruncatedNotice graph={graph} />
        <div className={full ? "min-h-0 flex-1" : undefined}>
          <Canvas
            {...props}
            laid={laid}
            folded={folded}
            missed={missed}
            selection={selection}
            onSelect={setSelection}
            onToggleFold={toggleFold}
          />
        </div>
      </div>
      <div className={full ? "min-h-0 overflow-y-auto" : undefined}>
        <FlowSidePanel
          graph={graph}
          source={source}
          selection={selection}
          onSelect={setSelection}
          onShowLines={onShowLines}
          run={run}
          view={view}
        />
      </div>
    </div>
  );
}

// useFolds is which helper boxes are folded: those that open folded, until the
// reader opens or folds one. A different run or version opens with its own.
function useFolds(graph: ScriptFlow, run?: ScriptRunFlow): [Set<string>, (id: string) => void] {
  const [folded, setFolded] = useState<Set<string>>(() => defaultFolded(graph.structure, run?.structure_failed));
  const key = `${graph.script_id}:${graph.version}:${run?.run_id ?? ""}`;
  const [seen, setSeen] = useState(key);
  if (seen !== key) {
    setSeen(key);
    setFolded(defaultFolded(graph.structure, run?.structure_failed));
  }
  const toggle = useCallback(
    (id: string) =>
      setFolded((f) => {
        const next = new Set(f);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        return next;
      }),
    [],
  );
  return [folded, toggle];
}

// useSelection is what the reader picked. Lines selected in Source replace
// whatever was picked on the diagram: the newest question the reader asked is
// which cards those lines produced.
function useSelection(sourceSelection: SelectedLines | null): [Selection, (s: Selection) => void] {
  const [selection, setSelection] = useState<Selection>(null);
  const [seenLines, setSeenLines] = useState(sourceSelection);
  if (seenLines !== sourceSelection) {
    setSeenLines(sourceSelection);
    if (sourceSelection) setSelection(null);
  }
  return [selection, setSelection];
}

// useLayout lays the chosen view out; the Timeline needs no layout.
function useLayout(
  view: FlowView,
  graph: ScriptFlow,
  runNodes: ScriptRunFlow["nodes"] | undefined,
  structureView: ReturnType<typeof fold>,
  folded: Set<string>,
) {
  const [laid, setLaid] = useState<Laid | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (view === "timeline") return;
    let live = true;
    const fail = () => live && setFailed(true);
    if (view === "calls") {
      layoutFlow(graph, runNodes).then((layout) => live && setLaid({ view: "calls", graph, layout }), fail);
    } else {
      layoutStructure(structureView, graph, runNodes).then(
        (layout) => live && setLaid({ view: "structure", graph, folded, layout }),
        fail,
      );
    }
    return () => {
      live = false;
    };
  }, [graph, runNodes, view, structureView, folded]);
  return { laid, failed };
}

interface CanvasProps extends LaidOutProps {
  laid: Laid | null;
  folded: Set<string>;
  missed?: Set<number>;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onToggleFold: (id: string) => void;
}

// Canvas draws the chosen view once its layout is ready.
function Canvas(props: CanvasProps) {
  const { graph, run, view, full, laid, folded, selection, onSelect, onShowLines } = props;
  if (view === "timeline" && run) {
    return <TimelineView run={run} selection={selection} fill={full} onSelect={onSelect} onShowLines={onShowLines} />;
  }
  const ready = laid?.graph === graph && laid.view === view;
  if (ready && laid.view === "calls") return <CallsCanvas {...props} layout={laid.layout} />;
  if (ready && laid.view === "structure" && laid.folded === folded) return <StructureFor {...props} layout={laid.layout} />;
  return <p className="text-sm text-muted-foreground">Laying out the diagram…</p>;
}

// openLines opens a card's lines in Source.
function openLines(onShowLines?: (lines: number[]) => void) {
  return (n: FlowNode) => onShowLines?.(nodeLines(n).flatMap((r) => range(r.from, r.to)));
}

// CallsCanvas is the Calls view: the value graph.
function CallsCanvas({ graph, run, full, sourceSelection, selection, onSelect, onShowLines, layout }: CanvasProps & { layout: FlowLayout }) {
  const lighted = useMemo(() => lit(graph, selection, sourceSelection), [graph, selection, sourceSelection]);
  return (
    <FlowCanvas
      layout={layout}
      selection={selection}
      lit={lighted}
      run={run?.unplaced ? undefined : run?.nodes}
      fill={full}
      onSelect={onSelect}
      onOpen={openLines(onShowLines)}
    />
  );
}

// StructureFor is the Structure view. A selected card lights the cards it
// takes data from and feeds.
function StructureFor(props: CanvasProps & { layout: StructureLayout }) {
  const { graph, run, full, sourceSelection, selection, missed, onSelect, onShowLines, onToggleFold, layout } = props;
  const lighted = useMemo(() => {
    if (selection?.kind === "node") return { nodes: neighbors(graph, selection.id), dimOthers: false };
    return lit(graph, selection, sourceSelection);
  }, [graph, selection, sourceSelection]);
  return (
    <StructureCanvas
      layout={layout}
      structure={graph.structure}
      graph={graph}
      selection={selection}
      lit={lighted}
      run={run}
      missed={missed}
      fill={full}
      onSelect={onSelect}
      onOpen={openLines(onShowLines)}
      onToggleFold={onToggleFold}
    />
  );
}

// TruncatedNotice says a diagram was cut at its bound.
function TruncatedNotice({ graph }: { graph: ScriptFlow }) {
  if (!graph.truncated && !graph.structure.truncated) return null;
  return (
    <p className="text-xs text-muted-foreground">
      This script expands into more steps than one diagram draws; the first {graph.nodes.length} are shown.
    </p>
  );
}

// UnplacedNotice says why a run's calls are not on the diagram (#1972),
// rather than drawing every card as though the run never reached it.
function UnplacedNotice() {
  return (
    <Alert data-testid="flow-unplaced">
      <AlertDescription>
        This run was made before the platform recorded which line made each call, so its calls
        can't be placed on the diagram.
      </AlertDescription>
    </Alert>
  );
}

function range(from: number, to: number): number[] {
  const out: number[] = [];
  for (let i = from; i <= to; i++) out.push(i);
  return out;
}

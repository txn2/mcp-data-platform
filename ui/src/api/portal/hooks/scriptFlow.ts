// A script version's flow graph (#1906): the diagram the script page's Flow tab
// draws, derived by the platform from the version's source. Nobody authors it,
// so it is read-only and changes only when the source does.
import { useQuery } from "@tanstack/react-query";
import type { ScriptFinding } from "@/api/admin/types";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";
import { RUN_POLL_MS, isRunInFlight } from "./scriptRuns";

// FlowRole is what a step does, which the diagram colors it by.
export type FlowRole = "input" | "reads" | "writes" | "output";

// FlowNode is one step, or the run.state input.
export interface FlowNode {
  id: string;
  role: FlowRole;
  kind: string;
  // title is the action and where it goes; subtitle the operation, name or
  // path; purpose the sentence the author wrote on the call.
  title: string;
  subtitle?: string;
  purpose?: string;
  // detail is the tables, paths or keys the step touches.
  detail: string[];
  // computed is true when what the step reaches is only known at run time; the
  // computed part is written as {its source} in the text.
  computed: boolean;
  // group is the id of the function box the step is drawn in.
  group?: string;
  line: number;
  end_line: number;
  // site is the line a folded wrapper, named in wrapper, is called from.
  site?: number;
  wrapper?: string;
  loops: string[];
  // call_site is the position of every call on the stack that makes this
  // step's call, outermost first, "line:col": what a run records on the
  // step's calls (#1907).
  call_site?: string[];
  // change and was mark a node of a compared graph (#1908): added, changed
  // (with what it said in the older version) or removed (carried over from
  // the older version).
  change?: FlowChange;
  was?: { title: string; subtitle?: string; purpose?: string; detail: string[] };
}

// FlowChange is how a node of a compared graph differs from the older version.
export type FlowChange = "added" | "changed" | "removed";

// FlowEdge is a value passed from one step to another ("data"), or this run
// saving state for the next ("state").
export interface FlowEdge {
  from: string;
  to: string;
  via: string[];
  kind: "data" | "state";
  // change is "removed" on an edge only the older version of a compared graph
  // had.
  change?: "removed";
}

// FlowGroup is a function box.
export interface FlowGroup {
  id: string;
  label: string;
  caption?: string;
  parent?: string;
  def_line: number;
  called_from: number[];
}

// FlowParam is a run parameter with the steps its value reaches.
export interface FlowParam {
  name: string;
  line: number;
  reaches: string[];
  // decides is true when a condition reads the value, so it decides which
  // steps run.
  decides: boolean;
}

// ScriptFlow is one version's graph. ok is false when the source does not
// parse, and findings then says why.
export interface ScriptFlow {
  script_id: string;
  version: number;
  ok: boolean;
  findings: ScriptFinding[];
  nodes: FlowNode[];
  edges: FlowEdge[];
  groups: FlowGroup[];
  params: FlowParam[];
  lines: number;
  truncated: boolean;
  // structure is the script drawn in the order it runs (#1972): the
  // Structure view.
  structure: FlowStructure;
  // compared_with is the older version a compared graph is compared against.
  compared_with?: number;
  // library is present when the script is a library (#1970): what it offers
  // a load, in place of a diagram it does not have.
  library?: FlowLibrary;
}

// FlowLibrary is what one version of a library offers a load: its functions,
// and the statement that loads them all from that version ("" when there are
// none to load).
export interface FlowLibrary {
  functions: LibraryFunction[];
  load?: string;
}

// LibraryFunction is one function a library defines: its parameters as the
// source writes them and its docstring's first sentence.
export interface LibraryFunction {
  name: string;
  params: string[];
  doc?: string;
  line: number;
}

// StructKind is what a Structure node is.
export type StructKind = "start" | "end" | "stop" | "return" | "if" | "step";

// StructNode is one node of the Structure view (#1972). A step names the
// value graph's card for the same call; an if carries its condition and a
// stop its fail() message as label.
export interface StructNode {
  id: string;
  kind: StructKind;
  label?: string;
  step?: string;
  box?: string;
  line: number;
  call_site?: string[];
}

// StructEdge is "runs next"; label is yes or no on an if's arms.
export interface StructEdge {
  from: string;
  to: string;
  label?: "yes" | "no";
}

// StructBox is a loop or a helper function around the nodes it runs.
export interface StructBox {
  id: string;
  kind: "loop" | "function";
  label: string;
  caption?: string;
  parent?: string;
  line: number;
  call_site?: string[];
}

// FuncSpan is one def and the lines it covers.
export interface FuncSpan {
  name: string;
  line: number;
  end_line: number;
}

export interface FlowStructure {
  nodes: StructNode[];
  edges: StructEdge[];
  boxes: StructBox[];
  functions: FuncSpan[];
  truncated: boolean;
}

// useScriptFlow reads one version's graph, or, with compareWith, that graph
// marked with what changed since an older version (#1908). A version never
// changes, so the graph is fetched once per version and kept.
export function useScriptFlow(scriptID: string, version: number, compareWith?: number) {
  const compare = compareWith ? `?compare=${compareWith}` : "";
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "flow", version, compareWith ?? 0],
    queryFn: () => apiFetch<ScriptFlow>(`/scripts/${scriptID}/versions/${version}/graph${compare}`),
    enabled: !!scriptID && version > 0,
    staleTime: Infinity,
  });
}

// FlowNodeRun is what one card did in one run (#1907).
export interface FlowNodeRun {
  // calls are the audited tool calls the card made, and duration_ms their
  // total; response_chars the size of what they answered.
  calls: number;
  duration_ms: number;
  response_chars: number;
  // outputs and rows are what the card wrote.
  outputs: number;
  rows: number;
  // failed_calls counts calls that did not succeed, which a run can outlive
  // (a rate-limited call is made again); last_error is the last one's message.
  failed_calls: number;
  last_error?: string;
  reached: boolean;
  // failed marks the card the run failed at, with error its message.
  failed: boolean;
  error?: string;
}

// FlowOtherCall is an audited call no card made.
export interface FlowOtherCall {
  tool: string;
  duration_ms: number;
  success: boolean;
  error?: string;
  call_site?: string[];
}

// FlowTimedCall is one call placed in time for the Timeline view (#1972):
// start_ms after the run started, with the card it was attributed to.
export interface FlowTimedCall {
  start_ms: number;
  duration_ms: number;
  tool: string;
  success: boolean;
  error?: string;
  response_chars: number;
  call_site?: string[];
  node?: string;
}

// ScriptRunFlow is one run drawn on the diagram of the version it executed.
export interface ScriptRunFlow {
  script_id: string;
  run_id: string;
  version: number;
  status: string;
  cause?: string;
  error?: string;
  graph: ScriptFlow;
  nodes: Record<string, FlowNodeRun>;
  other_calls: FlowOtherCall[];
  calls: number;
  failed_node?: string;
  // structure_failed is the Structure view's node or box the run failed at
  // (#1972); unplaced is true when none of the run's calls recorded where in
  // the script it was made, so none can be drawn on a card.
  structure_failed?: string;
  unplaced: boolean;
  timeline: FlowTimedCall[];
  run_ms: number;
  calls_truncated: boolean;
}

// useScriptRunFlow reads one run drawn on its version's diagram. A run still
// queued or executing is re-read on the run history's interval, so the diagram
// fills in as the run's calls are recorded.
export function useScriptRunFlow(scriptID: string, runID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", runID ?? "", "flow"],
    queryFn: () => apiFetch<ScriptRunFlow>(`/scripts/${scriptID}/runs/${runID}/flow`),
    enabled: !!scriptID && !!runID,
    refetchInterval: (query) =>
      query.state.data && isRunInFlight({ status: query.state.data.status }) ? RUN_POLL_MS : false,
  });
}

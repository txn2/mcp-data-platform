// A script version's flow graph (#1906): the diagram the script page's Flow tab
// draws, derived by the platform from the version's source. Nobody authors it,
// so it is read-only and changes only when the source does.
import { useQuery } from "@tanstack/react-query";
import type { ScriptFinding } from "@/api/admin/types";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

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
}

// FlowEdge is a value passed from one step to another ("data"), or this run
// saving state for the next ("state").
export interface FlowEdge {
  from: string;
  to: string;
  via: string[];
  kind: "data" | "state";
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
}

// useScriptFlow reads one version's graph. A version never changes, so the
// graph is fetched once per version and kept.
export function useScriptFlow(scriptID: string, version: number) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "flow", version],
    queryFn: () => apiFetch<ScriptFlow>(`/scripts/${scriptID}/versions/${version}/graph`),
    enabled: !!scriptID && version > 0,
    staleTime: Infinity,
  });
}

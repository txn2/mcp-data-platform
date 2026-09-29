import type { FlowNode, ScriptFlow } from "@/api/portal/hooks/scriptFlow";

// node builds a card with the fields a test does not care about filled in.
export function node(over: Partial<FlowNode> & Pick<FlowNode, "id">): FlowNode {
  return {
    role: "reads",
    kind: "query",
    title: over.id,
    detail: [],
    computed: false,
    line: 1,
    end_line: 1,
    loops: [],
    ...over,
  };
}

// sampleGraph is a small script: a query inside a function box feeding an API
// call folded through a helper, feeding an export that registers a table, with
// run.state read and saved and two parameters.
export function sampleGraph(): ScriptFlow {
  return {
    script_id: "script-001",
    version: 2,
    ok: true,
    findings: [],
    lines: 20,
    truncated: false,
    nodes: [
      node({ id: "state", role: "input", kind: "state", title: "run.state", subtitle: "what the last run saved", line: 2, end_line: 2 }),
      node({ id: "op:1", title: "Query trino", detail: ["hive.sales.orders"], group: "/load", line: 5, end_line: 6 }),
      node({
        id: "op:2",
        kind: "api",
        title: "API crm",
        subtitle: "GET /v1/accounts",
        purpose: "Daily load: look up each account.",
        wrapper: "fetch",
        site: 9,
        line: 14,
        end_line: 14,
        loops: ["for d in days"],
      }),
      node({ id: "op:3", role: "output", kind: "export", title: "Export CSV to {dest}", computed: true, line: 11, end_line: 11 }),
      node({ id: "op:4", role: "output", kind: "save_state", title: "Save state", subtitle: "run.state of the next run", line: 12, end_line: 12 }),
    ],
    edges: [
      { from: "state", to: "op:1", via: [], kind: "data" },
      { from: "op:1", to: "op:2", via: ["clean()"], kind: "data" },
      { from: "op:2", to: "op:3", via: [], kind: "data" },
      { from: "op:4", to: "state", via: [], kind: "state" },
    ],
    groups: [{ id: "/load", label: "load(day)", caption: "Load one day.", def_line: 4, called_from: [8] }],
    params: [
      { name: "day", line: 3, reaches: ["op:1"], decides: false },
      { name: "mode", line: 7, reaches: [], decides: true },
    ],
    structure: {
      nodes: [
        { id: "s:1", kind: "start", line: 0 },
        { id: "s:2", kind: "step", step: "op:1", box: "b:1", line: 5, call_site: ["8:9", "5:22"] },
        { id: "s:3", kind: "step", step: "op:2", box: "b:2", line: 14, call_site: ["9:14", "14:20"] },
        { id: "s:4", kind: "if", label: 'mode == "full"', line: 10 },
        { id: "s:5", kind: "step", step: "op:3", line: 11, call_site: ["11:16"] },
        { id: "s:6", kind: "stop", label: "unknown mode", line: 13, call_site: ["13:13"] },
        { id: "s:7", kind: "step", step: "op:4", line: 12, call_site: ["12:20"] },
        { id: "s:8", kind: "end", line: 0 },
      ],
      edges: [
        { from: "s:1", to: "s:2" },
        { from: "s:2", to: "s:3" },
        { from: "s:3", to: "s:4" },
        { from: "s:4", to: "s:5", label: "yes" },
        { from: "s:4", to: "s:6", label: "no" },
        { from: "s:5", to: "s:7" },
        { from: "s:7", to: "s:8" },
      ],
      boxes: [
        { id: "b:1", kind: "function", label: "load(day)", caption: "Load one day.", line: 8, call_site: ["8:9"] },
        { id: "b:2", kind: "loop", label: "for d in days", line: 9 },
      ],
      functions: [
        { name: "load", line: 4, end_line: 6 },
        { name: "main", line: 7, end_line: 14 },
      ],
      truncated: false,
    },
  };
}

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
  };
}

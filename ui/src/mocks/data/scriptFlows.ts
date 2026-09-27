// Flow graphs (#1906) for the mocked script versions, keyed "<script id>:<version>",
// and one comparison (#1908) keyed "<script id>:<version>:compare:<older>".
//
// They are not written by hand. Each is what GET .../versions/{version}/graph
// (with ?compare= for the comparison) answered on the local stack for the
// sources in ./scripts.ts, so the mocked diagram is the diagram the server
// derives. Regenerate them when a mocked source or the derivation changes.
import type { FlowNodeRun, ScriptFlow, ScriptRunFlow } from "@/api/portal/hooks/scriptFlow";

export const mockScriptFlows: Record<string, ScriptFlow> = {
 "script-001:2": {
  "script_id": "script-001",
  "version": 2,
  "ok": true,
  "findings": [],
  "nodes": [
   {
    "id": "state",
    "role": "input",
    "kind": "state",
    "title": "run.state",
    "subtitle": "what the last run saved",
    "detail": [],
    "computed": false,
    "line": 0,
    "end_line": 0,
    "loops": []
   },
   {
    "id": "op:1",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-warehouse",
    "detail": [
     "sales.orders"
    ],
    "computed": false,
    "line": 2,
    "end_line": 12,
    "loops": [],
    "call_site": [
     "2:22"
    ]
   },
   {
    "id": "op:2",
    "role": "output",
    "kind": "export",
    "title": "Export CSV to portal",
    "subtitle": "daily-sales",
    "detail": [],
    "computed": false,
    "line": 14,
    "end_line": 14,
    "loops": [],
    "call_site": [
     "14:16"
    ]
   },
   {
    "id": "op:3",
    "role": "output",
    "kind": "save_state",
    "title": "Save state",
    "subtitle": "run.state of the next run",
    "detail": [],
    "computed": false,
    "line": 18,
    "end_line": 18,
    "loops": [],
    "call_site": [
     "18:20"
    ]
   }
  ],
  "edges": [
   {
    "from": "op:1",
    "to": "op:2",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:1",
    "to": "op:3",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:3",
    "to": "state",
    "via": [],
    "kind": "state"
   }
  ],
  "groups": [],
  "params": [],
  "lines": 19,
  "truncated": false
 },
 "script-001:1": {
  "script_id": "script-001",
  "version": 1,
  "ok": true,
  "findings": [],
  "nodes": [
   {
    "id": "op:1",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-warehouse",
    "detail": [
     "sales.orders"
    ],
    "computed": false,
    "line": 2,
    "end_line": 12,
    "loops": [],
    "call_site": [
     "2:22"
    ]
   },
   {
    "id": "op:2",
    "role": "output",
    "kind": "export",
    "title": "Export CSV to portal",
    "subtitle": "daily-sales",
    "detail": [],
    "computed": false,
    "line": 14,
    "end_line": 14,
    "loops": [],
    "call_site": [
     "14:16"
    ]
   }
  ],
  "edges": [
   {
    "from": "op:1",
    "to": "op:2",
    "via": [],
    "kind": "data"
   }
  ],
  "groups": [],
  "params": [],
  "lines": 15,
  "truncated": false
 },
 "script-002:1": {
  "script_id": "script-002",
  "version": 1,
  "ok": true,
  "findings": [],
  "nodes": [
   {
    "id": "op:1",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-warehouse",
    "detail": [
     "sales.accounts"
    ],
    "computed": false,
    "line": 2,
    "end_line": 6,
    "loops": [],
    "call_site": [
     "2:22"
    ]
   },
   {
    "id": "op:2",
    "role": "output",
    "kind": "export",
    "title": "Export CSV to portal",
    "subtitle": "dormant-accounts",
    "detail": [],
    "computed": false,
    "line": 9,
    "end_line": 9,
    "loops": [],
    "call_site": [
     "9:16"
    ]
   },
   {
    "id": "op:3",
    "role": "output",
    "kind": "export",
    "title": "Export CSV to acme-crm-drop",
    "subtitle": "dormant-accounts",
    "detail": [
     "retention/dormant.csv"
    ],
    "computed": false,
    "line": 10,
    "end_line": 16,
    "loops": [],
    "call_site": [
     "10:16"
    ]
   }
  ],
  "edges": [
   {
    "from": "op:1",
    "to": "op:2",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:1",
    "to": "op:3",
    "via": [],
    "kind": "data"
   }
  ],
  "groups": [],
  "params": [],
  "lines": 17,
  "truncated": false
 },
 "script-004:1": {
  "script_id": "script-004",
  "version": 1,
  "ok": true,
  "findings": [],
  "nodes": [
   {
    "id": "op:1",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-finance",
    "detail": [
     "finance.margins"
    ],
    "computed": false,
    "line": 1,
    "end_line": 1,
    "loops": [],
    "call_site": [
     "1:22"
    ]
   },
   {
    "id": "op:2",
    "role": "output",
    "kind": "export",
    "title": "Export CSV to portal",
    "subtitle": "my-margins",
    "detail": [],
    "computed": false,
    "line": 2,
    "end_line": 2,
    "loops": [],
    "call_site": [
     "2:16"
    ]
   }
  ],
  "edges": [
   {
    "from": "op:1",
    "to": "op:2",
    "via": [],
    "kind": "data"
   }
  ],
  "groups": [],
  "params": [],
  "lines": 3,
  "truncated": false
 },
 "script-005:1": {
  "script_id": "script-005",
  "version": 1,
  "ok": true,
  "findings": [],
  "nodes": [
   {
    "id": "state",
    "role": "input",
    "kind": "state",
    "title": "run.state",
    "subtitle": "what the last run saved",
    "detail": [],
    "computed": false,
    "line": 34,
    "end_line": 34,
    "loops": []
   },
   {
    "id": "op:1",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-warehouse",
    "detail": [
     "sales.orders"
    ],
    "computed": false,
    "group": "/stage",
    "line": 17,
    "end_line": 18,
    "loops": [
     "for day in days"
    ],
    "call_site": [
     "37:10",
     "17:26"
    ]
   },
   {
    "id": "op:2",
    "role": "reads",
    "kind": "api",
    "title": "API acme-crm",
    "subtitle": "GET /v2/customers",
    "purpose": "Orders load: look up the customers who ordered on this day.",
    "detail": [],
    "computed": false,
    "group": "/stage",
    "line": 8,
    "end_line": 13,
    "site": 19,
    "wrapper": "crm",
    "loops": [
     "for day in days"
    ],
    "call_site": [
     "37:10",
     "19:20",
     "8:25"
    ]
   },
   {
    "id": "op:3",
    "role": "output",
    "kind": "export",
    "title": "Export JSONL to resources",
    "subtitle": "orders-{day}",
    "detail": [
     "orders/{day}.jsonl"
    ],
    "computed": false,
    "group": "/stage",
    "line": 21,
    "end_line": 23,
    "loops": [
     "for day in days"
    ],
    "call_site": [
     "37:10",
     "21:20"
    ]
   },
   {
    "id": "op:3:table",
    "role": "output",
    "kind": "table",
    "title": "Table on acme-warehouse",
    "subtitle": "staged_orders",
    "detail": [
     "follows each new version of the file"
    ],
    "computed": false,
    "group": "/stage",
    "line": 21,
    "end_line": 23,
    "loops": [
     "for day in days"
    ],
    "call_site": [
     "37:10",
     "21:20"
    ]
   },
   {
    "id": "op:4",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-warehouse",
    "detail": [
     "sales.orders"
    ],
    "computed": false,
    "group": "/summarize",
    "line": 28,
    "end_line": 29,
    "loops": [],
    "call_site": [
     "39:14",
     "28:28"
    ]
   },
   {
    "id": "op:5",
    "role": "reads",
    "kind": "api",
    "title": "API acme-crm",
    "subtitle": "GET /v2/regions",
    "purpose": "Orders load: read the region each customer belongs to.",
    "detail": [],
    "computed": false,
    "group": "/summarize",
    "line": 8,
    "end_line": 13,
    "site": 30,
    "wrapper": "crm",
    "loops": [],
    "call_site": [
     "39:14",
     "30:18",
     "8:25"
    ]
   },
   {
    "id": "op:6",
    "role": "output",
    "kind": "export",
    "title": "Export HTML to portal",
    "subtitle": "orders-summary",
    "detail": [],
    "computed": false,
    "group": "/summarize",
    "line": 31,
    "end_line": 31,
    "loops": [],
    "call_site": [
     "39:14",
     "31:20"
    ]
   },
   {
    "id": "op:7",
    "role": "output",
    "kind": "notify",
    "title": "Notify sales",
    "subtitle": "Orders loaded",
    "detail": [],
    "computed": false,
    "group": "/summarize",
    "line": 32,
    "end_line": 32,
    "loops": [],
    "call_site": [
     "39:14",
     "32:20"
    ]
   },
   {
    "id": "op:8",
    "role": "output",
    "kind": "save_state",
    "title": "Save state",
    "subtitle": "run.state of the next run",
    "detail": [],
    "computed": false,
    "line": 40,
    "end_line": 40,
    "loops": [],
    "call_site": [
     "40:20"
    ]
   }
  ],
  "edges": [
   {
    "from": "op:1",
    "to": "op:3",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:2",
    "to": "op:3",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:3",
    "to": "op:3:table",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:4",
    "to": "op:6",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:5",
    "to": "op:6",
    "via": [],
    "kind": "data"
   },
   {
    "from": "state",
    "to": "op:1",
    "via": [],
    "kind": "data"
   },
   {
    "from": "state",
    "to": "op:7",
    "via": [],
    "kind": "data"
   },
   {
    "from": "state",
    "to": "op:8",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:8",
    "to": "state",
    "via": [],
    "kind": "state"
   }
  ],
  "groups": [
   {
    "id": "/stage",
    "label": "stage(day)",
    "caption": "Stage one day's orders as a JSON-lines file with a table over it.",
    "def_line": 16,
    "called_from": [
     37
    ]
   },
   {
    "id": "/summarize",
    "label": "summarize(days)",
    "caption": "Summarize the staged days and refresh the dashboard.",
    "def_line": 27,
    "called_from": [
     39
    ]
   }
  ],
  "params": [
   {
    "name": "start",
    "line": 34,
    "reaches": [
     "op:1",
     "op:3",
     "op:7",
     "op:8"
    ],
    "decides": false
   },
   {
    "name": "summary",
    "line": 38,
    "reaches": [],
    "decides": true
   }
  ],
  "lines": 41,
  "truncated": false
 },
 "script-003:5": {
  "script_id": "script-003",
  "version": 5,
  "ok": true,
  "findings": [],
  "nodes": [
   {
    "id": "op:1",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-warehouse",
    "detail": [
     "ops.loads"
    ],
    "computed": false,
    "line": 1,
    "end_line": 1,
    "loops": [],
    "call_site": [
     "1:22"
    ]
   },
   {
    "id": "op:2",
    "role": "output",
    "kind": "export",
    "title": "Export CSV to portal",
    "subtitle": "freshness",
    "detail": [],
    "computed": false,
    "line": 2,
    "end_line": 2,
    "loops": [],
    "call_site": [
     "2:16"
    ]
   }
  ],
  "edges": [
   {
    "from": "op:1",
    "to": "op:2",
    "via": [],
    "kind": "data"
   }
  ],
  "groups": [],
  "params": [],
  "lines": 3,
  "truncated": false
 },
 "script-001:2:compare:1": {
  "script_id": "script-001",
  "version": 2,
  "ok": true,
  "findings": [],
  "nodes": [
   {
    "id": "state",
    "role": "input",
    "kind": "state",
    "title": "run.state",
    "subtitle": "what the last run saved",
    "detail": [],
    "computed": false,
    "line": 0,
    "end_line": 0,
    "loops": [],
    "change": "added"
   },
   {
    "id": "op:1",
    "role": "reads",
    "kind": "query",
    "title": "Query acme-warehouse",
    "detail": [
     "sales.orders"
    ],
    "computed": false,
    "line": 2,
    "end_line": 12,
    "loops": [],
    "call_site": [
     "2:22"
    ]
   },
   {
    "id": "op:2",
    "role": "output",
    "kind": "export",
    "title": "Export CSV to portal",
    "subtitle": "daily-sales",
    "detail": [],
    "computed": false,
    "line": 14,
    "end_line": 14,
    "loops": [],
    "call_site": [
     "14:16"
    ]
   },
   {
    "id": "op:3",
    "role": "output",
    "kind": "save_state",
    "title": "Save state",
    "subtitle": "run.state of the next run",
    "detail": [],
    "computed": false,
    "line": 18,
    "end_line": 18,
    "loops": [],
    "call_site": [
     "18:20"
    ],
    "change": "added"
   }
  ],
  "edges": [
   {
    "from": "op:1",
    "to": "op:2",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:1",
    "to": "op:3",
    "via": [],
    "kind": "data"
   },
   {
    "from": "op:3",
    "to": "state",
    "via": [],
    "kind": "state"
   }
  ],
  "groups": [],
  "params": [],
  "lines": 19,
  "truncated": false,
  "compared_with": 1
 }
};

// A stat is what one card did in one run (#1907).
const stat = (over: Partial<FlowNodeRun>): FlowNodeRun => ({
  calls: 0,
  duration_ms: 0,
  response_chars: 0,
  outputs: 0,
  rows: 0,
  failed_calls: 0,
  reached: false,
  failed: false,
  ...over,
});

// mockRunFlows are script-001's runs drawn on version 2's diagram (#1907). The
// overlay is run data, like the runs in ./scripts.ts: a succeeded run that
// reached every card, and the failed run, which failed at the query and never
// reached the export or the state it saves.
export const mockRunFlows: Record<string, Omit<ScriptRunFlow, "graph">> = {
  "run-001": {
    script_id: "script-001",
    run_id: "run-001",
    version: 2,
    status: "succeeded",
    nodes: {
      state: stat({ reached: true }),
      "op:1": stat({ calls: 1, duration_ms: 1_840, response_chars: 18_240, reached: true }),
      "op:2": stat({ outputs: 1, rows: 412, reached: true }),
      "op:3": stat({ reached: true }),
    },
    other_calls: [],
    calls: 1,
    calls_truncated: false,
  },
  "run-002": {
    script_id: "script-001",
    run_id: "run-002",
    version: 2,
    status: "failed",
    cause: "script",
    error:
      'Traceback (most recent call last):\n  daily-sales-report:2:22: in <toplevel>\nError in query: platform.query: connection "acme-warehouse" refused the query: relation "sales.orders" does not exist',
    nodes: {
      state: stat({ reached: true }),
      "op:1": stat({
        calls: 1,
        duration_ms: 310,
        failed_calls: 1,
        last_error: 'relation "sales.orders" does not exist',
        reached: true,
        failed: true,
        error: 'Error in query: platform.query: connection "acme-warehouse" refused the query: relation "sales.orders" does not exist',
      }),
    },
    other_calls: [],
    calls: 1,
    failed_node: "op:1",
    calls_truncated: false,
  },
};

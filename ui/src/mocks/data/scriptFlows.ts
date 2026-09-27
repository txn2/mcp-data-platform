// Flow graphs (#1906) for the mocked script versions, keyed "<script id>:<version>".
//
// They are not written by hand. Each is what GET .../versions/{version}/graph
// answered on the local stack for that version's source in ./scripts.ts, so the
// mocked diagram is the diagram the server derives. Regenerate them when a
// mocked source or the derivation changes.
import type { ScriptFlow } from "@/api/portal/hooks/scriptFlow";

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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
    "loops": []
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
 }
};

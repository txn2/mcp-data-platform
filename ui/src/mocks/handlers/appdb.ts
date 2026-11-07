import { http, HttpResponse } from "msw";
import type {
  AppDBConsoleResult,
  AppDBHealthReport,
  AppDBHealthSettings,
} from "@/api/admin/hooks";

// Operating an application database (#2406, #2409, #2410, #2427): the routes
// the connection page's Drift, Health, Maintenance and Console cards read.
//   GET  /connection-instances/appdb/:name/health
//   GET  /connection-instances/appdb/:name/health/history
//   GET  /connection-instances/appdb/:name/drift
//   POST /connection-instances/appdb/:name/adopt
//   POST /connection-instances/appdb/:name/maintenance
//   POST /connection-instances/appdb/:name/backends/:pid/:signal
//   POST /connection-instances/appdb/:name/console
//   GET  /appdb-health/settings, PUT /appdb-health/settings

const ADMIN_BASE = "/api/v1/admin";
const APPDB = `${ADMIN_BASE}/connection-instances/appdb/:name`;

// The acme-shop database: one table carrying deleted rows, an index never
// scanned, and a session left idle in a transaction.
export const mockAppDBHealth: AppDBHealthReport = {
  connection: "acme-shop",
  generated_at: "2025-01-23T10:00:00Z",
  status: "warning",
  findings: [
    {
      kind: "idle_in_transaction", level: "warning", pid: 4242, value: 612, threshold: 300,
      title: "A session has been idle in a transaction for 10 minutes",
      detail: "Its open transaction holds the locks it took and keeps vacuum from reclaiming rows. The warning alert is set at 5 minutes.",
    },
    {
      kind: "dead_tuples", level: "warning", table: "public.orders", value: 23, threshold: 20,
      title: "23% of orders's rows are dead",
      detail: "Rows are updated or deleted faster than autovacuum reclaims them, so the table and its indexes grow. The warning alert is set at 20%.",
    },
    {
      kind: "unused_index", level: "info", table: "public.orders", index: "orders_note_idx", value: 0,
      title: "orders_note_idx has not been used",
      detail: "16 KiB on public.orders, 0 scans since the statistics were reset. Every write to orders still updates it.",
    },
  ],
  server: {
    version: "17.8", version_num: 170008, database: "orders", size_bytes: 9_437_184,
    transaction_id_age: 1840, cache_hit_ratio: 0.993, in_recovery: false,
  },
  extensions: [{ name: "plpgsql", version: "1.0" }, { name: "vector", version: "0.8.1" }],
  connections: { max: 100, total: 7, by_state: { active: 1, idle: 5, "idle in transaction": 1 } },
  tables: [
    {
      schema: "public", name: "orders", size_bytes: 425_984, row_estimate: 4000, live_tuples: 4000, dead_tuples: 1200,
      dead_ratio: 0.23, seq_scans: 12, index_scans: 980, last_autovacuum: "2025-01-23T09:40:00Z",
      last_autoanalyze: "2025-01-23T09:40:01Z", transaction_id_age: 1800,
    },
  ],
  indexes: {
    unused: [{ schema: "public", table: "orders", name: "orders_note_idx", size_bytes: 16_384, scans: 0 }],
    duplicate: [],
    invalid: [],
  },
  sessions: [
    {
      pid: 4242, user: "orders_app", application: "psql", state: "idle in transaction",
      seconds: 640, state_seconds: 612, query: "UPDATE orders SET total = total WHERE id = 7",
    },
  ],
  blocking: [],
  replication: [],
  statements: {
    installed: true,
    hidden_other_roles: 4,
    top: [
      {
        query_id: "101", calls: 18402, total_ms: 44200, mean_ms: 2.4, max_ms: 31, rows: 18402,
        query: "SELECT o.id, o.total, c.email FROM orders o JOIN customers c ON c.id = o.customer_id WHERE o.status = $1 AND o.created_at >= $2 ORDER BY o.created_at DESC LIMIT $3",
      },
      {
        query_id: "102", calls: 1, total_ms: 4100, mean_ms: 4100, max_ms: 4100, rows: 3870,
        query: "DELETE FROM carts WHERE updated_at < now() - $1::interval",
      },
    ],
  },
  errors: [],
};

export let mockAppDBHealthSettings: AppDBHealthSettings = {
  channels: [], interval_seconds: 300, history_days: 30, long_running_seconds: 60,
  transaction_id_age: { warning: 500_000_000, critical: 1_000_000_000 },
  idle_in_transaction_seconds: { warning: 300, critical: 1800 },
  connections_percent: { warning: 80, critical: 95 },
  invalid_indexes: { warning: 1, critical: 0 },
  dead_tuple_percent: { warning: 20, critical: 50 },
  drift_objects: { warning: 1, critical: 0 },
};

const known = (name: unknown) => decodeURIComponent(String(name)) === "acme-shop";

export const appdbHandlers = [
  http.get(`${APPDB}/health`, ({ params }) =>
    known(params["name"]) ? HttpResponse.json(mockAppDBHealth) : new HttpResponse(null, { status: 404 }),
  ),

  http.get(`${APPDB}/health/history`, ({ params }) =>
    HttpResponse.json({
      connection: params["name"],
      days: 7,
      points: [
        {
          connection: "acme-shop", sampled_at: "2025-01-23T09:55:00Z",
          sample: {
            size_bytes: 9_437_184, transaction_id_age: 1838, connections: 7, max_connections: 100,
            idle_in_transaction: 1, longest_idle_seconds: 312, long_running: 0, blocked: 0,
            max_dead_ratio: 0.23, reachable: true, drift_objects: 1, drift_unchecked: 0,
          },
        },
      ],
    }),
  ),

  // A column added by hand, outside migrations.
  http.get(`${APPDB}/drift`, ({ params }) =>
    known(params["name"])
      ? HttpResponse.json({
          connection: "acme-shop",
          schemas: [
            {
              connection: "acme-shop", schema: "public", checked_at: "2025-01-23T10:00:00Z", checked: true,
              applied_version: 2, differences: [{ kind: "column", object: "orders.coupon", change: "added" }],
            },
          ],
        })
      : new HttpResponse(null, { status: 404 }),
  ),

  // The acme-shop public schema is managed, so adopting it is refused; any
  // other schema is adopted.
  http.post(`${APPDB}/adopt`, ({ request }) => {
    const schema = new URL(request.url).searchParams.get("schema") ?? "public";
    if (schema === "public") {
      return HttpResponse.json(
        { title: "Conflict", status: 409, detail: "the schema is already managed by migrations" },
        { status: 409 },
      );
    }
    return HttpResponse.json({
      connection: "acme-shop", schema, adopted: true, version: 1, checksum: "d4",
      up: "CREATE TABLE ...", unsupported: [], differences: [],
    });
  }),

  http.post(`${APPDB}/maintenance`, async ({ request }) => {
    const body = (await request.json()) as { command: string; table?: string };
    if (!["analyze", "vacuum", "vacuum_full", "reindex"].includes(body.command)) {
      return HttpResponse.json(
        { title: "Bad Request", status: 400, detail: "creating or dropping an index is a schema change: add it as a migration with manage_schema" },
        { status: 400 },
      );
    }
    const stmt = { analyze: "ANALYZE", vacuum: "VACUUM (ANALYZE)", vacuum_full: "VACUUM (FULL, ANALYZE)", reindex: "REINDEX TABLE CONCURRENTLY" }[
      body.command as "analyze"
    ];
    return HttpResponse.json({
      connection: "acme-shop", command: body.command, table: body.table,
      statement: body.table ? `${stmt} "${body.table}"` : stmt, duration_ms: 42,
    });
  }),

  http.post(`${APPDB}/backends/:pid/:signal`, ({ params }) =>
    HttpResponse.json({ connection: "acme-shop", signal: params["signal"], pid: Number(params["pid"]), sent: true }),
  ),

  // A read in the READ ONLY transaction answers rows; a write in it is
  // refused by the database; with read_only false it commits.
  http.post(`${APPDB}/console`, async ({ request }) => {
    const body = (await request.json()) as { sql: string; read_only?: boolean };
    const readOnly = body.read_only !== false;
    const write = /^\s*(update|insert|delete)/i.test(body.sql);
    const res: AppDBConsoleResult = { connection: "acme-shop", read_only: readOnly, committed: false, results: [] };
    if (write && readOnly) {
      res.error = {
        index: 0, code: "statement_failed",
        summary: "appdb: SQLSTATE 25006 (read_only_sql_transaction)",
        detail: "cannot execute UPDATE in a read-only transaction",
      };
      return HttpResponse.json(res);
    }
    res.results = write
      ? [{ index: 0, command: "UPDATE", rows_affected: 1, columns: [], rows: [], truncated: false }]
      : [{ index: 0, command: "SELECT", rows_affected: 1, columns: [{ name: "id", type: "int8" }, { name: "total", type: "numeric" }], rows: [[7, "12.50"]], truncated: false }];
    res.committed = !readOnly;
    return HttpResponse.json(res);
  }),

  http.get(`${ADMIN_BASE}/appdb-health/settings`, () => HttpResponse.json(mockAppDBHealthSettings)),

  http.put(`${ADMIN_BASE}/appdb-health/settings`, async ({ request }) => {
    mockAppDBHealthSettings = { ...((await request.json()) as AppDBHealthSettings), updated_by: "admin@acme.example.com" };
    return HttpResponse.json(mockAppDBHealthSettings);
  }),
];

import { http, HttpResponse } from "msw";
import type { Reconciliation, RepairItem, RepairOutcome } from "@/api/admin/types";

// The scratch-schema reconcile (#2136):
//   GET  /tables/reconcile?connection=  (orphans, missing, drifted)
//   POST /tables/reconcile              (repair the provable ones)

const ADMIN_BASE = "/api/v1/admin";

/** mockReconciliation is one connection's scratch schema: an orphan an
 * abandoned registration left over a stored file, one over a resource since
 * deleted, a table somebody made by hand, a registration whose table is gone,
 * a webhook source's whose view is gone, and a table that moved. */
export function mockReconciliation(connection: string): Reconciliation {
  const reg = (id: string, table: string, kind: "resource" | "asset" | "webhook", sourceId: string, location: string) => ({
    id, source_kind: kind, source_id: sourceId, connection, catalog: "scratch_resources", schema: "uploads",
    table, location, registered_by: "analyst@example.com", registered_at: "2026-10-14T09:12:00Z",
  });
  return {
    connection,
    catalog: "scratch_resources",
    schema: "uploads",
    truncated: false,
    orphans: [
      {
        table: "admin_daily_orders",
        location: "s3://resources/resources/user/u1/res_7f2a/v/rev_31/",
        source: { kind: "resource", id: "res_7f2a", exists: true },
        provable: true,
      },
      {
        table: "analyst_q3_vendors",
        location: "s3://resources/resources/persona/analyst/res_1c90/",
        source: { kind: "resource", id: "res_1c90", exists: false },
        provable: true,
      },
      { table: "scratch_by_hand", location: "s3://team-bucket/exports/2026/", provable: false },
    ],
    missing: [
      { registration: reg("reg_5d11", "analyst_store_rebates", "resource", "res_44b2", "s3://resources/resources/user/u2/res_44b2/"), provable: true },
      { registration: reg("reg_wh01", "webhook_esp_events", "webhook", "esp-events", "s3://resources/webhooks/esp-events/"), provable: false },
    ],
    drifted: [
      {
        registration: reg("reg_88e0", "analyst_weekly_kpis", "asset", "ast_3b7", "s3://portal-assets/artifacts/u3/ast_3b7/"),
        location: "s3://portal-assets/artifacts/u3/ast_3b7/v4/",
      },
    ],
  };
}

export const tableReconcileHandlers = [
  http.get(`${ADMIN_BASE}/tables/reconcile`, ({ request }) => {
    const connection = new URL(request.url).searchParams.get("connection") ?? "";
    if (!connection) {
      return HttpResponse.json({ status: 400, detail: "name the connection to reconcile" }, { status: 400 });
    }
    return HttpResponse.json(mockReconciliation(connection));
  }),

  http.post(`${ADMIN_BASE}/tables/reconcile`, async ({ request }) => {
    const body = (await request.json()) as { connection: string; repair: RepairItem[] };
    const outcomes: RepairOutcome[] = body.repair.map((it) => ({ ...it, done: true }));
    return HttpResponse.json({ outcomes });
  }),
];

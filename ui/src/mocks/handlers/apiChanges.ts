import { http, HttpResponse } from "msw";
import type {
  APICaller,
  APIChange,
  APIChangeSettings,
  APISpecWatch,
  APIUpstreamWarning,
} from "@/api/admin/hooks/apiChanges";

// Keeping API catalog specs current (#2469). The Stripe core spec is checked
// weekly and holds its changes for review, with one waiting, so the Updates
// section and the Changes list render populated; every other spec is checked
// on request only.

const ADMIN_BASE = "/api/v1/admin";
const DAY = 24 * 60 * 60 * 1000;
const at = (daysAgo: number) => new Date(Date.now() - daysAgo * DAY).toISOString();

const nightly: APICaller = {
  kind: "script", id: "6f1c2a9e-1b7e-4c55-9d2b-1f0f6e1d2a10", name: "nightly-revenue-sync", via: "source", connection: "stripe",
};
const refunds: APICaller = {
  kind: "prompt", id: "0d3e3a4c-2b1f-4a8e-9f3e-7f6c5b4a3d21", name: "refund-triage", via: "prompt", connection: "stripe",
};

const watches: Record<string, APISpecWatch> = {
  "stripe-api-2025-01/core": {
    refresh_interval: "168h0m0s", hold_changes: true, upstream_url: "",
    last_checked_at: at(1), next_check_at: new Date(Date.now() + 6 * DAY).toISOString(),
    pending_change: "chg-stripe-2", updated_by: "data-platform@acme.example.com",
  },
};

const changes: Record<string, APIChange[]> = {
  "stripe-api-2025-01/core": [
    {
      id: "chg-stripe-2", catalog_id: "stripe-api-2025-01", spec_name: "core", status: "pending", trigger: "schedule",
      created_at: at(1), summary: "1 added, 1 removed, 1 changed, 1 deprecated; version 2025-01-27 to 2025-02-24",
      report: {
        from_version: "2025-01-27", to_version: "2025-02-24",
        added: [{ id: "PostRefundsRefundCancel", method: "POST", path: "/v1/refunds/{refund}/cancel" }],
        removed: [{ id: "GetChargesSearch", method: "GET", path: "/v1/charges/search", used_by: [nightly] }],
        changed: [{ id: "PostRefunds", method: "POST", path: "/v1/refunds", parts: ["request_body"], used_by: [refunds] }],
        deprecated: [{ id: "GetBalanceHistory", method: "GET", path: "/v1/balance/history", used_by: [nightly] }],
      },
    },
    {
      id: "chg-stripe-1", catalog_id: "stripe-api-2025-01", spec_name: "core", status: "applied", trigger: "schedule",
      created_at: at(8), summary: "2 added, 0 removed, 0 changed, 0 deprecated", decided_by: "",
      report: {
        added: [
          { id: "GetBillingMeters", method: "GET", path: "/v1/billing/meters" },
          { id: "PostBillingMeters", method: "POST", path: "/v1/billing/meters" },
        ],
        removed: [], changed: [], deprecated: [],
      },
    },
  ],
};

const warnings: Record<string, APIUpstreamWarning[]> = {
  stripe: [
    {
      connection: "stripe", operation: "GetBalanceHistory", kind: "sunset", detail: "Tue, 30 Jun 2026 00:00:00 GMT",
      first_seen_at: at(3), last_seen_at: at(0), seen_count: 42, notified_at: at(3),
    },
  ],
};

let settings: APIChangeSettings = { channels: ["ops-alerts"] };

const emptyWatch: APISpecWatch = { refresh_interval: "", hold_changes: false, upstream_url: "" };
const specKey = (params: Record<string, unknown>) => `${String(params.id)}/${String(params.name)}`;

export const apiChangeHandlers = [
  http.post(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/refresh`, ({ params }) =>
    HttpResponse.json({
      spec_name: String(params.name), source_kind: "url", operation_count: 0, not_modified: true,
      last_fetched_at: new Date().toISOString(),
    }),
  ),
  http.get(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/watch`, ({ params }) =>
    HttpResponse.json(watches[specKey(params)] ?? emptyWatch),
  ),
  http.put(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/watch`, async ({ params, request }) => {
    const input = (await request.json()) as Pick<APISpecWatch, "refresh_interval" | "hold_changes" | "upstream_url">;
    const next = { ...(watches[specKey(params)] ?? emptyWatch), ...input };
    watches[specKey(params)] = next;
    return HttpResponse.json(next);
  }),
  http.get(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/changes`, ({ params }) =>
    HttpResponse.json({ changes: changes[specKey(params)] ?? [] }),
  ),
  http.get(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/changes/:change`, ({ params }) => {
    const c = (changes[specKey(params)] ?? []).find((x) => x.id === String(params.change));
    return c ? HttpResponse.json(c) : new HttpResponse(null, { status: 404 });
  }),
  http.post(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/changes/:change/apply`, ({ params }) => {
    const c = (changes[specKey(params)] ?? []).find((x) => x.id === String(params.change));
    if (!c || c.status !== "pending") return new HttpResponse(null, { status: 409 });
    c.status = "applied";
    return HttpResponse.json({ spec_name: String(params.name), source_kind: "url", operation_count: 0, not_modified: false, change: c });
  }),
  http.post(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/changes/:change/discard`, ({ params }) => {
    const c = (changes[specKey(params)] ?? []).find((x) => x.id === String(params.change));
    if (!c || c.status !== "pending") return new HttpResponse(null, { status: 409 });
    c.status = "discarded";
    return HttpResponse.json({ status: "discarded" });
  }),
  http.get(`${ADMIN_BASE}/api-catalogs/:id/specs/:name/operations/:operationId/used-by`, ({ params }) => {
    const op = String(params.operationId);
    const used = op === "GetBalanceHistory" || op === "GetChargesSearch" ? [nightly] : op === "PostRefunds" ? [refunds] : [];
    return HttpResponse.json({ operation: op, used_by: used });
  }),
  http.get(`${ADMIN_BASE}/connection-instances/api/:name/used-by`, ({ params }) => {
    const name = String(params.name);
    if (name !== "stripe") return HttpResponse.json({ connection: name, operations: [] });
    return HttpResponse.json({
      connection: name, catalog_id: "stripe-api-2025-01",
      operations: [
        { operation: "GetBalanceHistory", used_by: [nightly] },
        { operation: "PostRefunds", used_by: [refunds] },
      ],
    });
  }),
  http.get(`${ADMIN_BASE}/connection-instances/api/:name/warnings`, ({ params }) =>
    HttpResponse.json({ connection: String(params.name), warnings: warnings[String(params.name)] ?? [] }),
  ),
  http.get(`${ADMIN_BASE}/settings/api-changes`, () => HttpResponse.json(settings)),
  http.put(`${ADMIN_BASE}/settings/api-changes`, async ({ request }) => {
    settings = (await request.json()) as APIChangeSettings;
    return HttpResponse.json(settings);
  }),
];

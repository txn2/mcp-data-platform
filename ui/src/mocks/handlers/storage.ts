import { http, HttpResponse } from "msw";
import type { BotCheck, BotCheckInput, StorageSettings, WebhookAlertSettings } from "@/api/admin/types";
import { mockAdminEndpoints } from "../data/endpoints";
import { mockStorageHistory, mockStorageReport, mockStorageSettings } from "../data/storage";

// Storage capacity (#2148), the webhook alerts' settings (#2149) and the
// named bot checks a bot_check endpoint picks from (#2173, #2259):
//   GET /storage, GET /storage/history
//   GET|PUT /settings/storage, GET|PUT /settings/webhook-alert
//   GET /bot-checks, GET|PUT|DELETE /bot-checks/{name}

const ADMIN_BASE = "/api/v1/admin";

const storageSettings: StorageSettings = structuredClone(mockStorageSettings);

const webhookAlert: WebhookAlertSettings = {
  channels: ["ops-alerts"],
  spool_fill_percent: { warning: 50, critical: 80 },
  spool_age_percent: { warning: 25, critical: 60 },
  drain_failures: { warning: 3, critical: 10 },
  drain_failing_minutes: 5,
  not_reporting_seconds: 120,
  spool_volume_free_percent: { warning: 25, critical: 10 },
  defaults: {
    refusal_minutes: 5,
    unauthorized_percent: { warning: 10, critical: 0 },
    ack_p99_ms: { warning: 1000, critical: 5000 },
    compaction_overdue_minutes: { warning: 1, critical: 30 },
    silent_after_minutes: 0,
  },
  sources: { "email-events": { silent_after_minutes: 120 } },
  updated_by: "sarah.chen@example.com",
  updated_at: "2026-10-12T10:00:00Z",
};

// botChecks are the named checks; used_by is read from the endpoints that
// name each, as the server does.
const botChecks: Omit<BotCheck, "used_by" | "usable">[] = [
  {
    name: "default",
    provider: "pow",
    site_key: "",
    secret_name: "",
    difficulty: 16,
    max_difficulty: 22,
    rate_threshold: 30,
    challenge_ttl_seconds: 300,
    updated_by: "sarah.chen@example.com",
    updated_at: "2026-10-16T09:00:00Z",
  },
  {
    name: "signup",
    provider: "turnstile",
    site_key: "0x4AAAAAAAexample",
    secret_name: "turnstile-key",
    difficulty: 16,
    max_difficulty: 22,
    rate_threshold: 30,
    challenge_ttl_seconds: 300,
    updated_by: "sarah.chen@example.com",
    updated_at: "2026-10-20T14:00:00Z",
  },
];

function checkView(c: Omit<BotCheck, "used_by" | "usable">): BotCheck {
  const used_by = mockAdminEndpoints.filter((e) => e.bot_check === c.name).map((e) => e.name);
  return { ...c, used_by, usable: true };
}

const badRequest = (detail: string) =>
  HttpResponse.json({ type: "about:blank", title: "Bad Request", status: 400, detail }, { status: 400 });

export const storageHandlers = [
  http.get(`${ADMIN_BASE}/bot-checks`, () =>
    HttpResponse.json({ checks: botChecks.map(checkView), key_available: true }),
  ),

  http.get(`${ADMIN_BASE}/bot-checks/:name`, ({ params }) => {
    const c = botChecks.find((x) => x.name === params.name);
    if (!c) return HttpResponse.json({ status: 404, detail: "no bot check has this name" }, { status: 404 });
    return HttpResponse.json(checkView(c));
  }),

  http.put(`${ADMIN_BASE}/bot-checks/:name`, async ({ params, request }) => {
    const name = String(params.name);
    const body = (await request.json()) as BotCheckInput;
    const prev = botChecks.find((x) => x.name === name);
    const base = prev ?? {
      name, provider: "pow" as const, site_key: "", secret_name: "", difficulty: 16, max_difficulty: 22,
      rate_threshold: 30, challenge_ttl_seconds: 300,
    };
    const next: Omit<BotCheck, "used_by" | "usable"> = {
      ...base,
      ...Object.fromEntries(Object.entries(body).filter(([, v]) => v !== undefined)),
      updated_by: "sarah.chen@example.com",
      updated_at: new Date().toISOString(),
    };
    if (next.provider !== "pow" && (!next.site_key || !next.secret_name)) {
      return badRequest(`provider ${next.provider} needs a site_key and secret_name`);
    }
    if (prev) Object.assign(prev, next);
    else botChecks.push(next);
    return HttpResponse.json(checkView(next));
  }),

  http.delete(`${ADMIN_BASE}/bot-checks/:name`, ({ params }) => {
    const i = botChecks.findIndex((x) => x.name === params.name);
    const found = botChecks[i];
    if (!found) return HttpResponse.json({ status: 404, detail: "no bot check has this name" }, { status: 404 });
    const view = checkView(found);
    if (view.used_by.length > 0) {
      return HttpResponse.json(
        {
          status: 409,
          detail: `bot check "${view.name}" is used by the endpoints ${view.used_by.join(", ")}: bind them to another check first`,
        },
        { status: 409 },
      );
    }
    botChecks.splice(i, 1);
    return new HttpResponse(null, { status: 204 });
  }),

  http.get(`${ADMIN_BASE}/storage`, () => HttpResponse.json(mockStorageReport())),

  http.get(`${ADMIN_BASE}/storage/history`, ({ request }) => {
    const params = new URL(request.url).searchParams;
    const store = params.get("store") ?? "";
    if (!store) return badRequest("store must be database, seaweedfs, bucket:<name> or spool:<replica>");
    return HttpResponse.json(mockStorageHistory(store, Number(params.get("days") ?? "30")));
  }),

  http.get(`${ADMIN_BASE}/settings/storage`, () => HttpResponse.json(storageSettings)),

  http.put(`${ADMIN_BASE}/settings/storage`, async ({ request }) => {
    const body = (await request.json()) as Partial<StorageSettings>;
    if (body.budgets && Object.keys(body.budgets).some((b) => b.trim() === "")) return badRequest("a budget names no bucket");
    Object.assign(storageSettings, body, { updated_by: "sarah.chen@example.com", updated_at: new Date().toISOString() });
    return HttpResponse.json(storageSettings);
  }),

  http.get(`${ADMIN_BASE}/settings/webhook-alert`, () => HttpResponse.json(webhookAlert)),

  http.put(`${ADMIN_BASE}/settings/webhook-alert`, async ({ request }) => {
    const body = (await request.json()) as Partial<WebhookAlertSettings>;
    const fill = body.spool_fill_percent;
    if (fill && fill.warning > 0 && fill.critical > 0 && fill.warning >= fill.critical) {
      return badRequest("the spool_fill_percent warning must be below its critical");
    }
    Object.assign(webhookAlert, body, { updated_by: "sarah.chen@example.com", updated_at: new Date().toISOString() });
    return HttpResponse.json(webhookAlert);
  }),
];

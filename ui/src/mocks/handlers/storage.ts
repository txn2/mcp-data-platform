import { http, HttpResponse } from "msw";
import type { BotCheckSettings, BotCheckSettingsInput, StorageSettings, WebhookAlertSettings } from "@/api/admin/types";
import { mockStorageHistory, mockStorageReport, mockStorageSettings } from "../data/storage";

// Storage capacity (#2148), the webhook alerts' settings (#2149) and the
// bot check a public endpoint requires (#2173):
//   GET /storage, GET /storage/history
//   GET|PUT /settings/storage, GET|PUT /settings/webhook-alert
//   GET|PUT /settings/bot-check

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

const botCheck: BotCheckSettings = {
  provider: "pow",
  site_key: "",
  secret_name: "",
  allow_public_without_check: false,
  difficulty: 16,
  max_difficulty: 22,
  rate_threshold: 30,
  challenge_ttl_seconds: 300,
  updated_by: "sarah.chen@example.com",
  updated_at: "2026-10-16T09:00:00Z",
  key_available: true,
  public_allowed: true,
};

const badRequest = (detail: string) =>
  HttpResponse.json({ type: "about:blank", title: "Bad Request", status: 400, detail }, { status: 400 });

export const storageHandlers = [
  http.get(`${ADMIN_BASE}/settings/bot-check`, () => HttpResponse.json(botCheck)),

  http.put(`${ADMIN_BASE}/settings/bot-check`, async ({ request }) => {
    const body = (await request.json()) as BotCheckSettingsInput;
    const next = { ...botCheck, ...body };
    if ((next.provider === "turnstile" || next.provider === "hcaptcha") && (!next.site_key || !next.secret_name)) {
      return badRequest(`provider ${next.provider} needs a site_key and secret_name`);
    }
    Object.assign(botCheck, next, {
      public_allowed: next.provider !== "none" || next.allow_public_without_check,
      updated_by: "sarah.chen@example.com",
      updated_at: new Date().toISOString(),
    });
    return HttpResponse.json(botCheck);
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

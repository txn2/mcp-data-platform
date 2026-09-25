import { http, HttpResponse } from "msw";
import type { WebhookSource, WebhookSourceInput } from "@/api/admin/types";
import { mockWebhookSources, mockWebhookStatus } from "../data/webhooks";

// Webhook source admin routes (#1870):
//   GET    /webhooks/sources          (list)
//   POST   /webhooks/sources          (create)
//   GET    /webhooks/sources/:name    (source and status)
//   PUT    /webhooks/sources/:name    (update)
//   DELETE /webhooks/sources/:name    (delete)

const ADMIN_BASE = "/api/v1/admin";
const sources = [...mockWebhookSources];

const notFound = () =>
  HttpResponse.json({ type: "about:blank", title: "Not Found", status: 404, detail: "webhook source not found" }, { status: 404 });

export const webhookHandlers = [
  http.get(`${ADMIN_BASE}/webhooks/sources`, () => HttpResponse.json({ sources })),

  http.post(`${ADMIN_BASE}/webhooks/sources`, async ({ request }) => {
    const body = (await request.json()) as WebhookSourceInput;
    const name = body.name ?? "";
    if (sources.some((s) => s.name === name)) {
      return HttpResponse.json(
        { type: "about:blank", title: "Conflict", status: 409, detail: "a webhook source with that name already exists" },
        { status: 409 },
      );
    }
    const now = new Date().toISOString();
    const created: WebhookSource = {
      name,
      enabled: body.enabled ?? true,
      connection: body.connection ?? "",
      path: `/hooks/${name}`,
      table: `webhook_${name.replace(/-/g, "_")}`,
      auth: { ...body.auth, secret: undefined, secret_set: true } as WebhookSource["auth"],
      config: { raw_retention_days: 7, compacted_retention_days: 400, ...body.config },
      created_by: "admin@example.com",
      created_at: now,
      updated_at: now,
    };
    sources.push(created);
    mockWebhookStatus[name] = {
      last_hour: {},
      last_day: {},
      last_segment_at: null,
      last_compacted_window: null,
      pending: 0,
      failing: 0,
      oldest_window: null,
      rejections: [],
    };
    return HttpResponse.json(created, { status: 201 });
  }),

  http.get(`${ADMIN_BASE}/webhooks/sources/:name`, ({ params }) => {
    const source = sources.find((s) => s.name === params.name);
    if (!source) return notFound();
    return HttpResponse.json({ source, status: mockWebhookStatus[source.name] });
  }),

  http.put(`${ADMIN_BASE}/webhooks/sources/:name`, async ({ params, request }) => {
    const i = sources.findIndex((s) => s.name === params.name);
    if (i < 0) return notFound();
    const body = (await request.json()) as WebhookSourceInput;
    const cur = sources[i]!;
    const next: WebhookSource = {
      ...cur,
      enabled: body.enabled ?? cur.enabled,
      auth: { ...body.auth, secret: undefined, secret_set: true } as WebhookSource["auth"],
      config: { ...body.config, persona: cur.config.persona },
      updated_at: new Date().toISOString(),
    };
    sources[i] = next;
    return HttpResponse.json(next);
  }),

  http.delete(`${ADMIN_BASE}/webhooks/sources/:name`, ({ params }) => {
    const i = sources.findIndex((s) => s.name === params.name);
    if (i < 0) return notFound();
    sources.splice(i, 1);
    return new HttpResponse(null, { status: 204 });
  }),
];

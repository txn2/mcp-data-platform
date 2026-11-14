import { http, HttpResponse } from "msw";
import type { ScriptEndpointInput } from "@/api/portal/hooks/scriptEndpoint";
import type { HandlerOffer } from "@/api/portal/hooks/scriptHandlers";
import { handlerStore, MOCK_SOURCES } from "../data/handlerStore";
import { mockScripts } from "../data/scripts";

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";

// A script's handlers (#2441, #2434, #2435), mirroring
// internal/httpserver/scripthttp/handlerhttp. The mock caller is an
// administrator, so every kind is offered.

const MAX_PER_KIND = 20;

// MAX_LIST_LIMIT is the most handlers one page of Admin > Handlers holds.
const MAX_LIST_LIMIT = 500;

// LIST_TYPES are the types a handler list filters on; none matches no handler.
const LIST_TYPES = ["endpoint", "form", "schedule", "webhook", "none"];

const OFFERS: HandlerOffer[] = [
  { kind: "endpoint", allowed: true, contract: "handle(req), or form_spec(req) and form_submit(req, values) on a form" },
  { kind: "schedule", allowed: true, contract: "main()" },
  { kind: "webhook", allowed: true, contract: "main()" },
];

let nextID = 1;
const newID = (prefix: string) => `${prefix}-${nextID++}`;

// sameArgs reports whether two schedules bind the same args.
function sameArgs(a: Record<string, unknown> | undefined, b: Record<string, unknown> | undefined): boolean {
  return JSON.stringify(a ?? {}) === JSON.stringify(b ?? {});
}

function notFound() {
  return HttpResponse.json({ detail: "not found" }, { status: 404 });
}

function scriptOf(id: string) {
  return mockScripts.find((s) => s.id === id);
}

type Body = ScriptEndpointInput & {
  kind?: string;
  cron?: string;
  timezone?: string;
  args?: Record<string, unknown>;
  event_source?: string;
};

function addSchedule(scriptID: string, body: Body) {
  if (!body.cron?.trim()) {
    return HttpResponse.json(
      { detail: 'a schedule needs a cron expression, for example "0 7 * * 1-5" for 07:00 on weekdays' },
      { status: 400 },
    );
  }
  if (handlerStore.schedules(scriptID).length >= MAX_PER_KIND) {
    return HttpResponse.json({ detail: "this script already has 20 schedule handlers, the most a script may hold" }, { status: 403 });
  }
  // A schedule with the cadence and args of one the script holds is refused,
  // naming it: a repeated call would otherwise double the runs.
  const timezone = body.timezone?.trim() || "UTC";
  const dup = handlerStore
    .schedules(scriptID)
    .find((x) => x.cron_spec === body.cron?.trim() && x.timezone === timezone && sameArgs(x.params, body.args));
  if (dup) {
    return HttpResponse.json(
      { detail: `script ${scriptOf(scriptID)?.name ?? scriptID} already has schedule handler ${dup.id} on ${dup.cron_spec} (${dup.timezone}) with these args` },
      { status: 400 },
    );
  }
  const id = newID("sched");
  handlerStore.addSchedule(scriptID, {
    id,
    script_id: scriptID,
    cron_spec: body.cron.trim(),
    timezone,
    params: body.args ?? {},
    enabled: true,
    next_run_at: new Date(Date.now() + 22 * 3_600_000).toISOString(),
    missed_fires: 0,
  });
  return HttpResponse.json(handlerStore.detail(id)?.detail, { status: 201 });
}

function addEndpoint(scriptID: string, body: Body) {
  const name = (body.endpoint ?? "").trim();
  if (!/^[a-z0-9][a-z0-9_-]*$/.test(name)) {
    return HttpResponse.json(
      { detail: `endpoint name "${name}" must be lowercase letters, digits, hyphens and underscores` },
      { status: 400 },
    );
  }
  const holder = handlerStore.rows().find((r) => r.kind === "endpoint" && r.target === name);
  // An endpoint named like one the script already has changes that one.
  if (holder && holder.script_id === scriptID) {
    const res = change(holder.id, body);
    return HttpResponse.json(handlerStore.detail(holder.id)?.detail, { status: res.status === 200 ? 201 : res.status });
  }
  if (holder) {
    return HttpResponse.json(
      { detail: `endpoint name "${name}" is taken by endpoint handler ${holder.id} of script ${holder.script_name}; choose another` },
      { status: 400 },
    );
  }
  const id = newID("ep");
  handlerStore.addEndpoint(scriptID, {
    ...body,
    handler: id,
    name: scriptOf(scriptID)?.name ?? scriptID,
    bound: true,
    endpoint: name,
    url: `https://data.example.com/endpoints/${name}${body.path ?? ""}`,
    methods: body.methods ?? ["GET"],
    auth_mode: body.auth_mode ?? "authenticated",
    enabled: true,
    failures: [],
    updated_by: "admin@example.com",
    updated_at: new Date().toISOString(),
  });
  return HttpResponse.json(handlerStore.detail(id)?.detail, { status: 201 });
}

function addWebhook(scriptID: string, body: Body) {
  const source = (body.event_source ?? "").trim();
  if (!MOCK_SOURCES.includes(source)) {
    return HttpResponse.json({ detail: `no webhook source is named "${source}"` }, { status: 403 });
  }
  const held = handlerStore.webhooks(scriptID).find((w) => w.event_source === source);
  if (held) {
    held.enabled = true;
    return HttpResponse.json(handlerStore.detail(held.id)?.detail, { status: 201 });
  }
  const id = newID("wh");
  handlerStore.addWebhook(scriptID, {
    id,
    script_id: scriptID,
    event_source: source,
    enabled: true,
    created_by: "sarah.chen@example.com",
    created_at: new Date().toISOString(),
  });
  return HttpResponse.json(handlerStore.detail(id)?.detail, { status: 201 });
}

function change(id: string, body: Body) {
  const found = handlerStore.find(id);
  if (!found) return notFound();
  if (found.schedule) {
    if (!body.cron?.trim()) {
      return HttpResponse.json({ detail: "a schedule needs a cron expression" }, { status: 400 });
    }
    Object.assign(found.schedule, {
      cron_spec: body.cron.trim(),
      timezone: body.timezone?.trim() || "UTC",
      params: body.args ?? {},
      next_run_at: new Date(Date.now() + 22 * 3_600_000).toISOString(),
    });
  }
  if (found.webhook && body.event_source && body.event_source !== found.webhook.event_source) {
    return HttpResponse.json(
      {
        detail: `webhook handler ${id} runs for ${found.webhook.event_source}, and a handler's source does not change: add a handler for ${body.event_source} and delete this one`,
      },
      { status: 400 },
    );
  }
  if (found.endpoint) {
    const { kind: _kind, cron: _c, timezone: _t, args: _a, event_source: _e, ...rest } = body;
    Object.assign(found.endpoint, rest, { updated_at: new Date().toISOString() });
    if (rest.endpoint) {
      found.endpoint.url = `https://data.example.com/endpoints/${rest.endpoint}${found.endpoint.path ?? ""}`;
    }
  }
  return HttpResponse.json(handlerStore.detail(id)?.detail);
}

function setEnabled(id: string, enabled: boolean) {
  const found = handlerStore.find(id);
  if (!found) return notFound();
  const target = found.schedule ?? found.endpoint ?? found.webhook;
  if (target) target.enabled = enabled;
  return HttpResponse.json(handlerStore.detail(id)?.detail);
}

export const handlerRouteMocks = [
  http.get(`${PORTAL_BASE}/scripts/:id/handlers`, ({ params }) => {
    const id = String(params.id);
    const sc = scriptOf(id);
    if (!sc) return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    return HttpResponse.json({
      handlers: handlerStore.details(id),
      add: sc.library
        ? OFFERS.map((o) => ({ ...o, allowed: false, reason: "A library is loaded by other scripts and never runs." }))
        : OFFERS,
      endpoint: { name: sc.name, page_problem: "This deployment has no pages domain." },
      sources: MOCK_SOURCES,
      max_per_kind: MAX_PER_KIND,
    });
  }),

  http.post(`${PORTAL_BASE}/scripts/:id/handlers`, async ({ params, request }) => {
    const id = String(params.id);
    if (!scriptOf(id)) return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    const body = (await request.json()) as Body;
    switch (body.kind) {
      case "schedule":
        return addSchedule(id, body);
      case "endpoint":
        return addEndpoint(id, body);
      case "webhook":
        return addWebhook(id, body);
    }
    return HttpResponse.json(
      {
        detail: body.kind
          ? `unknown handler kind "${body.kind}": one of endpoint, schedule, webhook`
          : "kind is required: one of endpoint, schedule, webhook",
      },
      { status: 400 },
    );
  }),

  http.get(`${PORTAL_BASE}/handlers/:id`, ({ params }) => {
    const found = handlerStore.detail(String(params.id));
    return found ? HttpResponse.json(found.detail) : notFound();
  }),

  http.put(`${PORTAL_BASE}/handlers/:id`, async ({ params, request }) =>
    change(String(params.id), (await request.json()) as Body),
  ),

  http.delete(`${PORTAL_BASE}/handlers/:id`, ({ params }) => {
    const id = String(params.id);
    const found = handlerStore.detail(id);
    if (!found) return notFound();
    if (found.detail.kind === "schedule" && found.detail.last_fire_at) {
      return HttpResponse.json(
        { detail: `schedule handler ${id} produced runs, which name it, so it is kept: disable it with handler_disable` },
        { status: 403 },
      );
    }
    handlerStore.remove(id);
    return HttpResponse.json({ handler: id, kind: found.detail.kind, name: found.detail.name, deleted: true });
  }),

  http.post(`${PORTAL_BASE}/handlers/:id/enable`, ({ params }) => setEnabled(String(params.id), true)),
  http.post(`${PORTAL_BASE}/handlers/:id/disable`, ({ params }) => setEnabled(String(params.id), false)),

  // Admin > Handlers: every handler, narrowed as the server narrows them.
  http.get(`${ADMIN_BASE}/handlers`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    const types = q.getAll("type");
    const unknown = types.find((t) => !LIST_TYPES.includes(t));
    if (unknown) {
      return HttpResponse.json({ detail: `unknown handler type "${unknown}": one of ${LIST_TYPES.join(", ")}` }, { status: 400 });
    }
    const search = (q.get("search") ?? "").trim().toLowerCase();
    const owner = (q.get("owner") ?? "").trim().toLowerCase();
    const enabled = q.get("enabled");
    if (enabled !== null && enabled !== "" && !["true", "false"].includes(enabled)) {
      return HttpResponse.json({ detail: "enabled must be true or false" }, { status: 400 });
    }
    const matched = handlerStore
      .rows()
      .filter((r) => types.length === 0 || types.includes(r.type))
      .filter((r) => !search || r.script_name.toLowerCase().includes(search) || r.target.toLowerCase().includes(search))
      .filter((r) => !owner || r.owner_email.toLowerCase() === owner)
      .filter((r) => !enabled || String(r.enabled) === enabled)
      // The server orders by script name, then kind, then creation.
      .sort((a, b) => a.script_name.localeCompare(b.script_name) || a.kind.localeCompare(b.kind) || a.created_at.localeCompare(b.created_at));
    const limit = Number(q.get("limit")) || MAX_LIST_LIMIT;
    const offset = Math.max(Number(q.get("offset")) || 0, 0);
    const data = matched.slice(offset, offset + Math.min(limit, MAX_LIST_LIMIT));
    return HttpResponse.json({ data, total: matched.length });
  }),
];

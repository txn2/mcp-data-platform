import type { ScriptSchedule } from "@/api/portal/hooks/scripts";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import type { HandlerDetail, HandlerRow, HandlerType } from "@/api/portal/hooks/scriptHandlers";
import { mockScripts, mockScriptSchedules } from "./scripts";

// The mock session's handlers (#2441, #2434): each script's schedules,
// endpoints and webhook sources, mutable so a handler added, changed, turned
// off or deleted on a page is what the next read answers, as on the server.

export interface MockWebhook {
  id: string;
  script_id: string;
  event_source: string;
  enabled: boolean;
  created_by: string;
  created_at: string;
}

const CREATED = "2026-10-16T15:20:00Z";

const schedules: Record<string, ScriptSchedule[]> = {};
for (const sc of Object.values(JSON.parse(JSON.stringify(mockScriptSchedules)) as Record<string, ScriptSchedule>)) {
  (schedules[sc.script_id] ??= []).push(sc);
}

const endpoints: Record<string, ScriptEndpoint[]> = {
  "script-002": [
    {
      handler: "ep-dormant-accounts",
      name: "dormant-accounts",
      bound: true,
      endpoint: "dormant-accounts",
      url: "https://data.example.com/endpoints/dormant-accounts/{region}",
      methods: ["GET"],
      path: "/{region}",
      auth_mode: "authenticated",
      callers: ["persona:analyst"],
      checked_methods: [],
      enabled: true,
      timeout_seconds: 10,
      max_memory_bytes: 64 << 20,
      max_body_bytes: 1 << 20,
      allowed_origins: ["https://crm.example.com"],
      rate_limit: 600,
      caller_rate_limit: 120,
      updated_by: "admin@example.com",
      updated_at: "2026-10-16T15:20:00Z",
      failures: [
        {
          id: "epf-1",
          version: 4,
          method: "GET",
          status: 504,
          error:
            "halted: script exceeded its time limit: Traceback (most recent call last):\n  dormant-accounts:6:20: in handle\nError in platform.query: context deadline exceeded",
          duration_ms: 10004,
          created_at: "2026-10-17T09:41:00Z",
        },
      ],
    },
  ],
};

const webhooks: Record<string, MockWebhook[]> = {
  "script-001": [
    {
      id: "wh-shop-orders",
      script_id: "script-001",
      event_source: "shop-orders",
      enabled: true,
      created_by: "sarah.chen@example.com",
      created_at: CREATED,
    },
  ],
};

/** MOCK_SOURCES are the webhook sources the mock caller may name. */
export const MOCK_SOURCES = ["shop-orders", "form-contact", "github-pushes"];

/** endpointType is an endpoint handler's type: a form's is form. */
function endpointType(e: ScriptEndpoint): HandlerType {
  return e.is_form ? "form" : "endpoint";
}

export const handlerStore = {
  schedules(scriptID: string): ScriptSchedule[] {
    return schedules[scriptID] ?? [];
  },
  allSchedules(): ScriptSchedule[] {
    return Object.values(schedules).flat();
  },
  endpoints(scriptID: string): ScriptEndpoint[] {
    return endpoints[scriptID] ?? [];
  },
  webhooks(scriptID: string): MockWebhook[] {
    return webhooks[scriptID] ?? [];
  },
  types(scriptID: string): HandlerType[] {
    const out = new Set<HandlerType>();
    for (const e of handlerStore.endpoints(scriptID)) out.add(endpointType(e));
    if (handlerStore.schedules(scriptID).length > 0) out.add("schedule");
    if (handlerStore.webhooks(scriptID).length > 0) out.add("webhook");
    return [...out].sort();
  },
  details(scriptID: string): HandlerDetail[] {
    const name = mockScripts.find((s) => s.id === scriptID)?.name ?? scriptID;
    return [
      ...handlerStore.endpoints(scriptID).map(
        (e) => ({ ...e, kind: "endpoint", type: endpointType(e), name, handler: e.handler }) as unknown as HandlerDetail,
      ),
      ...handlerStore.schedules(scriptID).map((s) => scheduleDetail(name, s)),
      ...handlerStore.webhooks(scriptID).map((w) => webhookDetail(name, w)),
    ];
  },
  detail(id: string): { scriptID: string; detail: HandlerDetail } | undefined {
    for (const sc of mockScripts) {
      const found = handlerStore.details(sc.id).find((d) => d.handler === id);
      if (found) return { scriptID: sc.id, detail: found };
    }
    return undefined;
  },
  rows(): HandlerRow[] {
    return mockScripts.flatMap((sc) =>
      handlerStore.details(sc.id).map((d) => ({
        id: d.handler,
        kind: d.kind,
        type: d.type,
        script_id: sc.id,
        script_name: sc.name,
        owner_email: sc.owner_email ?? "",
        target: rowTarget(d),
        enabled: d.enabled,
        created_by: String(d.created_by ?? sc.owner_email ?? ""),
        created_at: String(d.created_at ?? CREATED),
      })),
    );
  },
  addSchedule(scriptID: string, s: ScriptSchedule) {
    (schedules[scriptID] ??= []).push(s);
  },
  addEndpoint(scriptID: string, e: ScriptEndpoint) {
    (endpoints[scriptID] ??= []).push(e);
  },
  addWebhook(scriptID: string, w: MockWebhook) {
    (webhooks[scriptID] ??= []).push(w);
  },
  find(id: string) {
    for (const [scriptID, list] of Object.entries(schedules)) {
      const s = list.find((x) => x.id === id);
      if (s) return { scriptID, schedule: s };
    }
    for (const [scriptID, list] of Object.entries(endpoints)) {
      const e = list.find((x) => x.handler === id);
      if (e) return { scriptID, endpoint: e };
    }
    for (const [scriptID, list] of Object.entries(webhooks)) {
      const w = list.find((x) => x.id === id);
      if (w) return { scriptID, webhook: w };
    }
    return undefined;
  },
  remove(id: string): boolean {
    for (const map of [schedules, endpoints, webhooks] as Record<string, { id?: string; handler?: string }[]>[]) {
      for (const [scriptID, list] of Object.entries(map)) {
        const next = list.filter((x) => (x.handler ?? x.id) !== id);
        if (next.length !== list.length) {
          map[scriptID] = next;
          return true;
        }
      }
    }
    return false;
  },
};

function scheduleDetail(name: string, s: ScriptSchedule): HandlerDetail {
  const out: HandlerDetail = {
    handler: s.id,
    kind: "schedule",
    type: "schedule",
    name,
    cron: s.cron_spec,
    timezone: s.timezone,
    args: s.params ?? {},
    enabled: s.enabled,
    missed_fires: s.missed_fires ?? 0,
    created_by: s.created_by ?? "",
  };
  if (s.enabled && s.next_run_at) out.next_run_at = s.next_run_at;
  if (s.last_fire_at) out.last_fire_at = s.last_fire_at;
  return out;
}

function webhookDetail(name: string, w: MockWebhook): HandlerDetail {
  return {
    handler: w.id,
    kind: "webhook",
    type: "webhook",
    name,
    event_source: w.event_source,
    enabled: w.enabled,
    created_by: w.created_by,
    created_at: w.created_at,
  };
}

function rowTarget(d: HandlerDetail): string {
  switch (d.kind) {
    case "endpoint":
      return String(d.endpoint ?? "");
    case "schedule":
      return `${String(d.cron ?? "")} ${String(d.timezone ?? "")}`;
    case "webhook":
      return String(d.event_source ?? "");
  }
}

import { describe, it, expect, beforeEach, vi } from "vitest";
import { getResponse, type RequestHandler } from "msw";
import type { HandlerDetail, HandlerRow, ScriptHandlers } from "@/api/portal/hooks/scriptHandlers";

// The mock server's handler routes (#2441, #2434, #2435) answer as
// internal/httpserver/scripthttp/handlerhttp does: the same routes, statuses
// and shapes, and a write is what the next read answers. The mock caller is an
// administrator. Each test reads a fresh copy of the mock state.

let routes: RequestHandler[];

beforeEach(async () => {
  vi.resetModules();
  const [handlerRoutes, scripts] = await Promise.all([import("./handlerRoutes"), import("./scripts")]);
  routes = [...handlerRoutes.handlerRouteMocks, ...scripts.scriptHandlers];
});

async function call(method: string, path: string, body?: unknown): Promise<{ status: number; json: unknown }> {
  const init: RequestInit = { method };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { "Content-Type": "application/json" };
  }
  const res = await getResponse(routes, new Request(new URL(path, window.location.origin), init));
  if (!res) throw new Error(`no mock answers ${method} ${path}`);
  return { status: res.status, json: await res.json() };
}

async function handlersOf(scriptID: string): Promise<ScriptHandlers> {
  const { status, json } = await call("GET", `/api/v1/portal/scripts/${scriptID}/handlers`);
  expect(status).toBe(200);
  return json as ScriptHandlers;
}

async function adminList(query = ""): Promise<{ data: HandlerRow[]; total: number }> {
  const { status, json } = await call("GET", `/api/v1/admin/handlers${query}`);
  expect(status).toBe(200);
  return json as { data: HandlerRow[]; total: number };
}

describe("GET /portal/scripts/{id}/handlers", () => {
  it("lists a script's handlers, what adding one offers, the sources and the bound", async () => {
    const out = await handlersOf("script-001");
    expect(out.max_per_kind).toBe(20);
    expect(out.sources.length).toBeGreaterThan(0);
    expect(out.add.map((o) => [o.kind, o.allowed])).toEqual([
      ["endpoint", true],
      ["schedule", true],
      ["webhook", true],
      ["policy", true],
      ["transform", true],
      ["watch", true],
    ]);
    for (const o of out.add) expect(o.contract).not.toBe("");
    const sched = out.handlers.find((h) => h.kind === "schedule")!;
    expect(sched).toMatchObject({ handler: "sched-001", type: "schedule", name: "daily-sales-report", cron: "0 7 * * 1-5" });
    expect(Object.keys(sched)).toEqual(
      expect.arrayContaining(["handler", "kind", "type", "name", "cron", "timezone", "args", "enabled", "missed_fires", "created_by"]),
    );
    const hook = out.handlers.find((h) => h.kind === "webhook")!;
    expect(hook).toMatchObject({ type: "webhook", event_source: "shop-orders", enabled: true });
  });

  it("offers a library nothing, saying why", async () => {
    const out = await handlersOf("script-006");
    expect(out.add.every((o) => !o.allowed && o.reason === "A library is loaded by other scripts and never runs.")).toBe(true);
  });

  it("answers 404 for a script that does not exist", async () => {
    expect((await call("GET", "/api/v1/portal/scripts/nope/handlers")).status).toBe(404);
  });
});

describe("POST /portal/scripts/{id}/handlers", () => {
  it("adds a schedule, answering 201 with the handler, and lists it next", async () => {
    const { status, json } = await call("POST", "/api/v1/portal/scripts/script-004/handlers", {
      kind: "schedule",
      cron: "0 6 * * *",
      timezone: "UTC",
      args: { cutoff: "${fire_date}" },
    });
    expect(status).toBe(201);
    const added = json as HandlerDetail;
    expect(added).toMatchObject({ kind: "schedule", type: "schedule", cron: "0 6 * * *", timezone: "UTC", enabled: true });
    expect(added.args).toEqual({ cutoff: "${fire_date}" });
    expect((await handlersOf("script-004")).handlers.map((h) => h.handler)).toContain(added.handler);
  });

  it("refuses a schedule the script already holds, naming it, and one with no cadence", async () => {
    const dup = await call("POST", "/api/v1/portal/scripts/script-001/handlers", {
      kind: "schedule",
      cron: "0 7 * * 1-5",
      timezone: "America/Los_Angeles",
      args: { report_date: "${fire_date}" },
    });
    expect(dup.status).toBe(400);
    expect((dup.json as { detail: string }).detail).toMatch(/already has schedule handler sched-001/);
    // Another cadence on the same script is a second schedule.
    expect((await call("POST", "/api/v1/portal/scripts/script-001/handlers", { kind: "schedule", cron: "0 18 * * *" })).status).toBe(201);
    expect((await call("POST", "/api/v1/portal/scripts/script-001/handlers", { kind: "schedule", cron: " " })).status).toBe(400);
  });

  it("adds a webhook, keeps the one a script holds for a source turned on, and refuses an unknown source", async () => {
    const first = await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "webhook", event_source: "github-pushes" });
    expect(first.status).toBe(201);
    const id = (first.json as HandlerDetail).handler;
    await call("POST", `/api/v1/portal/handlers/${id}/disable`);
    const again = await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "webhook", event_source: "github-pushes" });
    expect(again.status).toBe(201);
    expect(again.json).toMatchObject({ handler: id, enabled: true });
    expect((await handlersOf("script-004")).handlers.filter((h) => h.kind === "webhook")).toHaveLength(1);
    expect((await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "webhook", event_source: "nope" })).status).toBe(403);
  });

  it("adds an endpoint, changes the script's own one of that name, and refuses a name another script holds", async () => {
    const added = await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "endpoint", endpoint: "margins", path: "/{id}" });
    expect(added.status).toBe(201);
    expect(added.json).toMatchObject({ kind: "endpoint", type: "endpoint", endpoint: "margins", url: "https://data.example.com/endpoints/margins/{id}" });

    const same = await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "endpoint", endpoint: "margins", methods: ["POST"] });
    expect(same.status).toBe(201);
    expect(same.json).toMatchObject({ handler: (added.json as HandlerDetail).handler, methods: ["POST"] });
    expect((await handlersOf("script-004")).handlers.filter((h) => h.kind === "endpoint")).toHaveLength(1);

    const taken = await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "endpoint", endpoint: "dormant-accounts" });
    expect(taken.status).toBe(400);
    expect((taken.json as { detail: string }).detail).toMatch(/is taken by endpoint handler ep-dormant-accounts of script dormant-accounts/);
    expect((await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "endpoint", endpoint: "Bad Name" })).status).toBe(400);
  });

  it("refuses a body with no kind, or a kind the platform does not register", async () => {
    const none = await call("POST", "/api/v1/portal/scripts/script-004/handlers", { cron: "0 6 * * *" });
    expect(none.status).toBe(400);
    expect((none.json as { detail: string }).detail).toMatch(/^kind is required/);
    const unknown = await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "mailbox" });
    expect(unknown.status).toBe(400);
    expect((unknown.json as { detail: string }).detail).toMatch(/^unknown handler kind "mailbox"/);
  });
});

describe("/portal/handlers/{id}", () => {
  it("turns a handler off and on, answering it as GET does", async () => {
    const off = await call("POST", "/api/v1/portal/handlers/wh-shop-orders/disable");
    expect(off.status).toBe(200);
    expect(off.json).toMatchObject({ handler: "wh-shop-orders", kind: "webhook", enabled: false });
    expect((await call("GET", "/api/v1/portal/handlers/wh-shop-orders")).json).toMatchObject({ enabled: false });
    expect((await call("POST", "/api/v1/portal/handlers/wh-shop-orders/enable")).json).toMatchObject({ enabled: true });
    expect((await call("POST", "/api/v1/portal/handlers/nope/enable")).status).toBe(404);
  });

  it("changes a schedule's whole cadence, and refuses changing a webhook's source", async () => {
    const changed = await call("PUT", "/api/v1/portal/handlers/sched-001", { cron: "30 6 * * 1-5", timezone: "UTC", args: {} });
    expect(changed.status).toBe(200);
    expect(changed.json).toMatchObject({ handler: "sched-001", cron: "30 6 * * 1-5", timezone: "UTC", args: {} });
    const moved = await call("PUT", "/api/v1/portal/handlers/wh-shop-orders", { event_source: "github-pushes" });
    expect(moved.status).toBe(400);
    expect((moved.json as { detail: string }).detail).toMatch(/a handler's source does not change/);
  });

  it("deletes a handler, answering what went, and keeps a schedule that produced runs", async () => {
    const gone = await call("DELETE", "/api/v1/portal/handlers/wh-shop-orders");
    expect(gone.status).toBe(200);
    expect(gone.json).toEqual({ handler: "wh-shop-orders", kind: "webhook", name: "daily-sales-report", deleted: true });
    expect((await handlersOf("script-001")).handlers.map((h) => h.handler)).not.toContain("wh-shop-orders");
    expect((await call("DELETE", "/api/v1/portal/handlers/wh-shop-orders")).status).toBe(404);

    const kept = await call("DELETE", "/api/v1/portal/handlers/sched-001");
    expect(kept.status).toBe(403);
    expect((kept.json as { detail: string }).detail).toMatch(/produced runs, which name it, so it is kept/);
  });
});

describe("GET /admin/handlers", () => {
  it("lists every handler by script name, each with its target", async () => {
    const all = await adminList();
    expect(all.total).toBe(all.data.length);
    const names = all.data.map((r) => r.script_name);
    expect(names).toEqual([...names].sort((a, b) => a.localeCompare(b)));
    expect(all.data.find((r) => r.id === "sched-001")).toMatchObject({
      kind: "schedule",
      type: "schedule",
      script_id: "script-001",
      target: "0 7 * * 1-5 America/Los_Angeles",
      owner_email: "sarah.chen@example.com",
    });
    expect(all.data.find((r) => r.id === "ep-dormant-accounts")).toMatchObject({ type: "endpoint", target: "dormant-accounts" });
    expect(all.data.find((r) => r.id === "wh-shop-orders")).toMatchObject({ type: "webhook", target: "shop-orders" });
  });

  it("narrows by type, repeated, by search, by owner in any case, and by state", async () => {
    expect((await adminList("?type=webhook")).data.map((r) => r.id)).toEqual(["wh-shop-orders"]);
    const two = await adminList("?type=webhook&type=endpoint");
    expect(two.data.map((r) => r.type).sort()).toEqual(["endpoint", "webhook"]);
    expect((await adminList("?type=none")).total).toBe(0);
    expect((await adminList("?search=SHOP")).data.map((r) => r.id)).toEqual(["wh-shop-orders"]);
    const sarah = await adminList("?owner=Sarah.Chen@Example.com");
    expect(sarah.total).toBeGreaterThan(0);
    expect(sarah.data.every((r) => r.owner_email === "sarah.chen@example.com")).toBe(true);
    const off = await adminList("?enabled=false");
    expect(off.data.every((r) => !r.enabled)).toBe(true);
    expect(off.data.map((r) => r.id)).toContain("sched-003");
  });

  it("pages, counting every match", async () => {
    const all = await adminList();
    const page = await adminList("?limit=2&offset=1");
    expect(page.total).toBe(all.total);
    expect(page.data.map((r) => r.id)).toEqual(all.data.slice(1, 3).map((r) => r.id));
  });

  it("refuses a type it cannot filter on and an enabled that is not a boolean", async () => {
    const type = await call("GET", "/api/v1/admin/handlers?type=mailbox");
    expect(type.status).toBe(400);
    expect((type.json as { detail: string }).detail).toMatch(/unknown handler type "mailbox"/);
    expect((await call("GET", "/api/v1/admin/handlers?enabled=maybe")).status).toBe(400);
  });
});

// The script surfaces read the same handlers: the contract's schedules and
// the listing's type filter and badges.
describe("what the script routes read from the handlers", () => {
  it("states the schedules a script holds now in its contract", async () => {
    await call("POST", "/api/v1/portal/scripts/script-004/handlers", { kind: "schedule", cron: "0 6 * * *", timezone: "UTC" });
    const { json } = await call("GET", "/api/v1/portal/scripts/script-004");
    const schedules = (json as { contract: { schedules: { cron_spec: string; enabled: boolean; next_run_at?: string }[] } }).contract
      .schedules;
    expect(schedules).toHaveLength(1);
    expect(schedules[0]).toMatchObject({ cron_spec: "0 6 * * *", enabled: true });
    expect(schedules[0]!.next_run_at).toBeDefined();

    const paused = (await call("GET", "/api/v1/portal/scripts/script-003")).json as {
      contract: { schedules: { enabled: boolean; next_run_at?: string }[] };
    };
    expect(paused.contract.schedules).toEqual([{ cron_spec: "*/30 * * * *", timezone: "UTC", enabled: false }]);
  });

  it("narrows the listing by handler type and badges each row's types", async () => {
    const { json } = await call("GET", "/api/v1/portal/scripts?type=webhook");
    const rows = (json as { data: { script: { id: string }; handler_types: string[] }[] }).data;
    expect(rows.map((r) => r.script.id)).toEqual(["script-001"]);
    expect(rows[0]!.handler_types).toEqual(["schedule", "transform", "webhook"]);
    const none = (await call("GET", "/api/v1/portal/scripts?type=none")).json as {
      data: { handler_types: string[] }[];
    };
    expect(none.data.length).toBeGreaterThan(0);
    expect(none.data.every((r) => r.handler_types.length === 0)).toBe(true);
  });
});

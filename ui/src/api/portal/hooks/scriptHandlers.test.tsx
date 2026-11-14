import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import {
  HANDLER_TYPE_LABELS,
  endpointOf,
  handlersKey,
  scheduleOf,
  useDeleteHandler,
  useSaveHandler,
  useScriptHandlers,
  useSetHandlerEnabled,
  type HandlerDetail,
} from "./scriptHandlers";
import { useSetScriptEndpoint, useSetScriptEndpointEnabled } from "./scriptEndpoint";
import { scriptsKey } from "./scriptKeys";

// A script's handlers (#2441, #2434, #2435), read and written through the
// handler routes: each one is addressed by its own id, and a write refreshes
// every script query, since the listing's badges and the contract's
// schedules read the same handlers.

const scheduleDetail: HandlerDetail = {
  handler: "sched-1",
  kind: "schedule",
  type: "schedule",
  name: "daily-sales-report",
  cron: "0 7 * * 1-5",
  timezone: "America/Los_Angeles",
  args: { report_date: "${fire_date}" },
  enabled: false,
  missed_fires: 2,
  created_by: "sarah.chen@example.com",
  next_run_at: "2026-08-16T14:00:00Z",
  last_fire_at: "2026-08-15T14:00:00Z",
};

function stub(body: unknown = {}, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), { status }))),
  );
}

function harness<T>(hook: () => T) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidated: unknown[][] = [];
  vi.spyOn(qc, "invalidateQueries").mockImplementation((filters?: { queryKey?: unknown }) => {
    invalidated.push((filters?.queryKey as unknown[]) ?? []);
    return Promise.resolve();
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(hook, { wrapper });
  return { result, invalidated };
}

// sent is the one request the hook made: its address, method and body.
function sent() {
  const call = vi.mocked(fetch).mock.calls[0]!;
  const init = call[1] as RequestInit;
  return {
    url: call[0] as string,
    method: init.method ?? "GET",
    body: typeof init.body === "string" ? (JSON.parse(init.body) as unknown) : undefined,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("scheduleOf", () => {
  it("is a schedule handler in the shape the schedule editor reads", () => {
    expect(scheduleOf(scheduleDetail)).toEqual({
      id: "sched-1",
      script_id: "",
      cron_spec: "0 7 * * 1-5",
      timezone: "America/Los_Angeles",
      enabled: false,
      params: { report_date: "${fire_date}" },
      next_run_at: "2026-08-16T14:00:00Z",
      last_fire_at: "2026-08-15T14:00:00Z",
      missed_fires: 2,
      created_by: "sarah.chen@example.com",
    });
  });

  it("reads the fields the server leaves out as their defaults", () => {
    const bare: HandlerDetail = { handler: "sched-2", kind: "schedule", type: "schedule", name: "x", enabled: true };
    expect(scheduleOf(bare)).toMatchObject({
      id: "sched-2",
      cron_spec: "",
      timezone: "UTC",
      params: {},
      missed_fires: 0,
      next_run_at: undefined,
    });
  });
});

describe("endpointOf", () => {
  it("is an endpoint handler as the endpoint editor reads it: bound, with its handler id", () => {
    const e = endpointOf({
      handler: "ep-1",
      kind: "endpoint",
      type: "form",
      name: "contact",
      endpoint: "contact",
      methods: ["GET", "POST"],
      enabled: true,
      is_form: true,
    });
    expect(e).toMatchObject({ handler: "ep-1", name: "contact", endpoint: "contact", bound: true, is_form: true, enabled: true });
    expect(e.methods).toEqual(["GET", "POST"]);
  });
});

describe("HANDLER_TYPE_LABELS", () => {
  it("names every type a list filters and badges by", () => {
    expect(HANDLER_TYPE_LABELS).toEqual({
      endpoint: "Endpoint",
      form: "Form",
      schedule: "Schedule",
      webhook: "Webhook",
      policy: "Policy",
      transform: "Transform",
      watch: "Watch",
    });
  });
});

describe("useScriptHandlers", () => {
  it("reads a script's handlers from its handler route", async () => {
    stub({ handlers: [], add: [], sources: [], max_per_kind: 20 });
    const { result } = harness(() => useScriptHandlers("script-001", true));
    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(sent()).toMatchObject({ url: "/api/v1/portal/scripts/script-001/handlers", method: "GET" });
    expect(handlersKey("script-001")).toEqual([...scriptsKey, "script-001", "handlers"]);
  });

  it("asks nothing for a reader who does not own the script, or for no script", () => {
    stub();
    harness(() => useScriptHandlers("script-001", false));
    harness(() => useScriptHandlers(null, true));
    expect(fetch).not.toHaveBeenCalled();
  });
});

describe("useSaveHandler", () => {
  it("adds a handler to the script when it names none", async () => {
    stub({ handler: "sched-9" }, 201);
    const { result, invalidated } = harness(() => useSaveHandler("script-001"));
    await result.current.mutateAsync({ kind: "schedule", cron: "0 7 * * *", timezone: "UTC", args: {} });
    expect(sent()).toEqual({
      url: "/api/v1/portal/scripts/script-001/handlers",
      method: "POST",
      body: { kind: "schedule", cron: "0 7 * * *", timezone: "UTC", args: {} },
    });
    expect(invalidated).toEqual([scriptsKey]);
  });

  it("changes the handler it names", async () => {
    stub({ handler: "sched-1" });
    const { result, invalidated } = harness(() => useSaveHandler("script-001", "sched-1"));
    await result.current.mutateAsync({ kind: "schedule", cron: "0 8 * * *", timezone: "UTC", args: {} });
    expect(sent()).toMatchObject({ url: "/api/v1/portal/handlers/sched-1", method: "PUT" });
    expect(invalidated).toEqual([scriptsKey]);
  });

  it("refreshes nothing when the server refuses", async () => {
    stub({ detail: "kind is required: one of endpoint, schedule, webhook" }, 400);
    const { result, invalidated } = harness(() => useSaveHandler("script-001"));
    await expect(result.current.mutateAsync({})).rejects.toThrow(/kind is required/);
    expect(invalidated).toEqual([]);
  });
});

describe("useSetHandlerEnabled and useDeleteHandler", () => {
  it("turns the handler on and off on its own routes", async () => {
    stub({});
    const { result, invalidated } = harness(() => useSetHandlerEnabled("wh-1"));
    await result.current.mutateAsync(false);
    expect(sent()).toMatchObject({ url: "/api/v1/portal/handlers/wh-1/disable", method: "POST" });
    vi.mocked(fetch).mockClear();
    await result.current.mutateAsync(true);
    expect(sent()).toMatchObject({ url: "/api/v1/portal/handlers/wh-1/enable", method: "POST" });
    expect(invalidated).toEqual([scriptsKey, scriptsKey]);
  });

  it("deletes the handler by its id", async () => {
    stub({ handler: "wh-1", kind: "webhook", name: "x", deleted: true });
    const { result, invalidated } = harness(() => useDeleteHandler("wh-1"));
    await result.current.mutateAsync();
    expect(sent()).toMatchObject({ url: "/api/v1/portal/handlers/wh-1", method: "DELETE" });
    expect(invalidated).toEqual([scriptsKey]);
  });
});

// The endpoint editor saves through its own hooks, onto the same routes.
describe("the endpoint handler's writes", () => {
  it("adds an endpoint as kind endpoint, and changes one by its handler id", async () => {
    stub({ handler: "ep-9" }, 201);
    const added = harness(() => useSetScriptEndpoint("script-001"));
    await added.result.current.mutateAsync({ endpoint: "orders", methods: ["GET"] });
    expect(sent()).toEqual({
      url: "/api/v1/portal/scripts/script-001/handlers",
      method: "POST",
      body: { kind: "endpoint", endpoint: "orders", methods: ["GET"] },
    });
    expect(added.invalidated).toEqual([scriptsKey]);

    vi.mocked(fetch).mockClear();
    const changed = harness(() => useSetScriptEndpoint("script-001", "ep-9"));
    await changed.result.current.mutateAsync({ allowed_origins: [] });
    expect(sent()).toEqual({ url: "/api/v1/portal/handlers/ep-9", method: "PUT", body: { allowed_origins: [] } });
  });

  it("turns one endpoint handler on and off", async () => {
    stub({});
    const { result } = harness(() => useSetScriptEndpointEnabled("ep-9"));
    await result.current.mutateAsync(true);
    expect(sent()).toMatchObject({ url: "/api/v1/portal/handlers/ep-9/enable", method: "POST" });
  });
});

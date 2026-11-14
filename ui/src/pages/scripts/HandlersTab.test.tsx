import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { HandlerRow } from "@/api/portal/hooks/scriptHandlers";
import { HANDLERS_PATH, HandlersTab } from "./HandlersTab";
import { formatWhen } from "./runFormat";

// Admin > Automations > Handlers (#2435): every handler of every script,
// filtered and paged by the server. The tab's own hook runs against a fetch
// that records each request, so what is asserted is the query the page sends.

const rows: HandlerRow[] = [
  {
    id: "ep-1",
    kind: "endpoint",
    type: "endpoint",
    script_id: "script-002",
    script_name: "dormant-accounts",
    owner_email: "marcus.johnson@example.com",
    target: "dormant-accounts",
    enabled: true,
    created_by: "admin@example.com",
    created_at: "2026-10-16T15:20:00Z",
  },
  {
    id: "sched-1",
    kind: "schedule",
    type: "schedule",
    script_id: "script-001",
    script_name: "daily-sales-report",
    owner_email: "sarah.chen@example.com",
    target: "0 7 * * 1-5 America/Los_Angeles",
    enabled: false,
    created_by: "",
    created_at: "2026-10-16T15:20:00Z",
  },
  {
    id: "wh-1",
    kind: "webhook",
    type: "webhook",
    script_id: "script-001",
    script_name: "daily-sales-report",
    owner_email: "sarah.chen@example.com",
    target: "shop-orders",
    enabled: true,
    created_by: "sarah.chen@example.com",
    created_at: "2026-10-17T09:00:00Z",
  },
];

let requested: URL[];
let reply: (url: URL) => Response;
const onNavigate = vi.fn();

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

beforeEach(() => {
  requested = [];
  onNavigate.mockReset();
  reply = () => json({ data: rows, total: rows.length });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const url = new URL(input, window.location.origin);
      requested.push(url);
      return reply(url);
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function show() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <HandlersTab onNavigate={onNavigate} />
    </QueryClientProvider>,
  );
}

// lastQuery is the newest list request's query, as key=value pairs.
function lastQuery(): Record<string, string[]> {
  const url = requested[requested.length - 1]!;
  expect(url.pathname).toBe("/api/v1/admin/handlers");
  const out: Record<string, string[]> = {};
  url.searchParams.forEach((v, k) => (out[k] ??= []).push(v));
  return out;
}

function choose(label: string, option: string) {
  fireEvent.keyDown(screen.getByLabelText(label), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name: option }));
}

describe("HandlersTab", () => {
  it("is the tab's own address", () => {
    expect(HANDLERS_PATH).toBe("/admin/automations/handlers");
  });

  it("lists every handler with its type, target in words, automation, owner, state and creation", async () => {
    show();
    const listed = await screen.findAllByTestId("admin-handler-row");
    expect(listed).toHaveLength(3);
    const [ep, sched, wh] = listed.map((r) => within(r));
    expect(ep!.getByText("Endpoint")).toBeInTheDocument();
    expect(ep!.getByText("/endpoints/dormant-accounts")).toBeInTheDocument();
    expect(ep!.getByText("dormant-accounts", { selector: "span" })).toBeInTheDocument();
    expect(ep!.getByText("marcus.johnson@example.com")).toBeInTheDocument();
    expect(ep!.getByText("On")).toBeInTheDocument();
    // Who created it, with when exactly, is the Created cell's hover text.
    expect(ep!.getByTitle(/by admin@example\.com$/)).toBeInTheDocument();
    // A schedule's target is its cron and zone, stated in words with the
    // expression on hover.
    expect(sched!.getByText("Schedule")).toBeInTheDocument();
    expect(sched!.getByText("Every weekday at 7:00 AM, America/Los_Angeles")).toHaveAttribute(
      "title",
      "0 7 * * 1-5 America/Los_Angeles",
    );
    expect(sched!.getByText("Off")).toBeInTheDocument();
    expect(sched!.getByTitle(new RegExp(`^${formatWhen("2026-10-16T15:20:00Z").replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`))).toBeInTheDocument();
    expect(wh!.getByText("Webhook")).toBeInTheDocument();
    expect(wh!.getByText("Each event of shop-orders")).toBeInTheDocument();
    expect(lastQuery()).toEqual({ limit: ["50"] });
  });

  it("opens a row's automation, where its Handlers section acts on it", async () => {
    show();
    fireEvent.click((await screen.findAllByTestId("admin-handler-row"))[1]!);
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/script-001");
  });

  it("asks the server for the type chosen, and for none once All types is chosen", async () => {
    show();
    await screen.findAllByTestId("admin-handler-row");
    fireEvent.keyDown(screen.getByLabelText("Filter by type"), { key: "Enter" });
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      "All types",
      "Endpoint",
      "Form",
      "Schedule",
      "Webhook",
    ]);
    fireEvent.click(screen.getByRole("option", { name: "Form" }));
    await waitFor(() => expect(lastQuery()).toEqual({ type: ["form"], limit: ["50"] }));
    choose("Filter by type", "Schedule");
    await waitFor(() => expect(lastQuery()).toEqual({ type: ["schedule"], limit: ["50"] }));
    choose("Filter by type", "All types");
    await waitFor(() => expect(lastQuery()).toEqual({ limit: ["50"] }));
  });

  it("asks for the handlers that are on or off, and for both", async () => {
    show();
    await screen.findAllByTestId("admin-handler-row");
    choose("Filter by state", "On");
    await waitFor(() => expect(lastQuery()).toEqual({ enabled: ["true"], limit: ["50"] }));
    choose("Filter by state", "Off");
    await waitFor(() => expect(lastQuery()).toEqual({ enabled: ["false"], limit: ["50"] }));
    choose("Filter by state", "On and off");
    await waitFor(() => expect(lastQuery()).toEqual({ limit: ["50"] }));
  });

  it("offers the owners listed and asks for one owner's handlers", async () => {
    show();
    await screen.findAllByTestId("admin-handler-row");
    fireEvent.keyDown(screen.getByLabelText("Filter by owner"), { key: "Enter" });
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      "All owners",
      "marcus.johnson@example.com",
      "sarah.chen@example.com",
    ]);
    fireEvent.click(screen.getByRole("option", { name: "sarah.chen@example.com" }));
    await waitFor(() => expect(lastQuery()).toEqual({ owner: ["sarah.chen@example.com"], limit: ["50"] }));
  });

  it("searches by script or target once typing stops, trimmed", async () => {
    show();
    await screen.findAllByTestId("admin-handler-row");
    const before = requested.length;
    fireEvent.change(screen.getByLabelText("Search by script or target"), { target: { value: "  shop " } });
    await waitFor(() => expect(lastQuery()).toEqual({ search: ["shop"], limit: ["50"] }));
    // One request for the search, not one per keystroke.
    expect(requested.slice(before).filter((u) => u.searchParams.has("search"))).toHaveLength(1);
  });

  it("combines the filters in one request", async () => {
    show();
    await screen.findAllByTestId("admin-handler-row");
    choose("Filter by type", "Webhook");
    choose("Filter by state", "On");
    choose("Filter by owner", "sarah.chen@example.com");
    await waitFor(() =>
      expect(lastQuery()).toEqual({
        type: ["webhook"],
        owner: ["sarah.chen@example.com"],
        enabled: ["true"],
        limit: ["50"],
      }),
    );
  });

  it("pages through more handlers than one page holds, and starts again at the first on a new filter", async () => {
    reply = () => json({ data: rows, total: 120 });
    show();
    await screen.findAllByTestId("admin-handler-row");
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => expect(lastQuery()).toEqual({ limit: ["50"], offset: ["50"] }));
    choose("Filter by type", "Endpoint");
    await waitFor(() => expect(lastQuery()).toEqual({ type: ["endpoint"], limit: ["50"] }));
  });

  it("offers no pager when every handler fits one page", async () => {
    show();
    await screen.findAllByTestId("admin-handler-row");
    expect(screen.queryByRole("button", { name: "Next page" })).not.toBeInTheDocument();
  });

  it("says no automation has a handler yet, and that a filter matched nothing", async () => {
    reply = () => json({ data: [], total: 0 });
    show();
    expect(await screen.findByText(/No automation has a handler yet/)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    choose("Filter by type", "Form");
    expect(await screen.findByText("No handler matches these filters.")).toBeInTheDocument();
    expect(screen.queryByText(/No automation has a handler yet/)).not.toBeInTheDocument();
  });

  it("says the handlers could not be read", async () => {
    reply = () => json({ error: "the handler store failed" }, 500);
    show();
    expect(await screen.findByText("The handlers could not be read.")).toBeInTheDocument();
  });

  it("says it is loading before the first answer", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    show();
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });
});

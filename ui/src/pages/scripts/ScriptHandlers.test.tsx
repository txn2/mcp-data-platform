import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, fireEvent, cleanup, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers as mockRoutes } from "@/mocks/handlers";
import type { HandlerDetail, HandlerOffer, ScriptHandlers as Handlers } from "@/api/portal/hooks/scriptHandlers";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { signIn, type SentRequest } from "@/test/applicationHarness";
import { ScriptHandlers } from "./ScriptHandlers";

// The Handlers section of a script's page (#2435): what starts the script,
// read and written through the handler routes (#2441, #2434). The section's
// own hooks run against a fetch that answers the handler routes from the
// fixture each test sets, and every other route from the mock server's
// handlers, so what is asserted is the request the page actually sends.

const contract: ScriptContract = {
  id: "s1",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  params: [{ name: "report_date", type: "date", description: "The business date.", required: false }],
  version: 2,
};

const ALL_OFFERS: HandlerOffer[] = [
  { kind: "endpoint", allowed: true, contract: "handle(req)" },
  { kind: "schedule", allowed: true, contract: "main()" },
  { kind: "webhook", allowed: true, contract: "main()" },
];

const schedule: HandlerDetail = {
  handler: "sched-1",
  kind: "schedule",
  type: "schedule",
  name: "daily-sales-report",
  cron: "0 7 * * 1-5",
  timezone: "America/Los_Angeles",
  args: {},
  enabled: true,
  next_run_at: "2026-08-16T14:00:00Z",
  missed_fires: 0,
};

const evening: HandlerDetail = {
  ...schedule,
  handler: "sched-2",
  cron: "0 18 * * *",
  timezone: "UTC",
  enabled: false,
  next_run_at: undefined,
};

const endpoint: HandlerDetail = {
  handler: "ep-1",
  kind: "endpoint",
  type: "endpoint",
  name: "daily-sales-report",
  endpoint: "orders",
  path: "/{id}",
  url: "https://data.example.com/endpoints/orders/{id}",
  methods: ["GET"],
  auth_mode: "authenticated",
  callers: ["*"],
  enabled: false,
  failures: [],
};

const form: HandlerDetail = {
  ...endpoint,
  handler: "ep-2",
  type: "form",
  endpoint: "contact",
  path: "",
  url: "https://data.example.com/endpoints/contact",
  enabled: true,
  is_form: true,
  form: { source: "form-contact", table: "webhook_form_contact", connection: "scratch", decrypt_personas: [] },
};

const webhook: HandlerDetail = {
  handler: "wh-1",
  kind: "webhook",
  type: "webhook",
  name: "daily-sales-report",
  event_source: "shop-orders",
  enabled: true,
};

// state is what the handler routes answer, per script; a write moves it, so
// the read after a write is what the page shows next, as on the server.
let state: Record<string, Handlers>;
let sent: SentRequest[];
// answer overrides one route's response in a test.
let answer: ((method: string, path: string) => Response | Promise<Response> | undefined) | undefined;

function handlersOf(over: Partial<Handlers> = {}): Handlers {
  return { handlers: [], add: ALL_OFFERS, sources: ["shop-orders", "github-pushes"], max_per_kind: 20, ...over };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function handlerRoute(method: string, path: string, body: Record<string, unknown> | undefined): Response | undefined {
  const list = /^\/api\/v1\/portal\/scripts\/([^/]+)\/handlers$/.exec(path);
  if (list) {
    const scriptID = list[1]!;
    const s = (state[scriptID] ??= handlersOf());
    if (method === "GET") return json(s);
    const added = { handler: `new-${s.handlers.length + 1}`, name: contract.name, enabled: true, type: body?.kind, ...body };
    s.handlers.push(added as HandlerDetail);
    return json(
      body?.kind === "endpoint" ? { ...added, warning: "This deployment trusts no proxy for a visitor's address." } : added,
      201,
    );
  }
  const one = /^\/api\/v1\/portal\/handlers\/([^/]+)(\/(enable|disable))?$/.exec(path);
  if (one) {
    const id = one[1]!;
    for (const s of Object.values(state)) {
      const h = s.handlers.find((x) => x.handler === id);
      if (!h) continue;
      if (method === "DELETE") {
        s.handlers = s.handlers.filter((x) => x.handler !== id);
        return json({ handler: id, kind: h.kind, name: h.name, deleted: true });
      }
      if (one[3]) h.enabled = one[3] === "enable";
      return json(h);
    }
    return json({ detail: "not found" }, 404);
  }
  return undefined;
}

beforeEach(() => {
  state = { s1: handlersOf() };
  sent = [];
  answer = undefined;
  signIn("sarah.chen@example.com", true);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      const method = init?.method ?? "GET";
      const body = typeof init?.body === "string" ? (JSON.parse(init.body) as Record<string, unknown>) : undefined;
      sent.push({ method, path: url.pathname + url.search, body });
      const own = answer?.(method, url.pathname) ?? handlerRoute(method, url.pathname, body);
      if (own) return own;
      const res = await getResponse(mockRoutes, new Request(url, init));
      return res ?? json({ detail: "no handler" }, 404);
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function show(scriptId = "s1", c: ScriptContract = contract) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={qc}>
      <ScriptHandlers scriptId={scriptId} contract={c} />
    </QueryClientProvider>,
  );
  return {
    ...view,
    rerenderFor: (id: string) =>
      view.rerender(
        <QueryClientProvider client={qc}>
          <ScriptHandlers scriptId={id} contract={{ ...c, id }} />
        </QueryClientProvider>,
      ),
  };
}

function rows() {
  return screen.getAllByTestId("handler-row");
}

function rowFor(id: string) {
  return rows().find((r) => r.getAttribute("data-handler") === id)!;
}

// openRow expands a handler's row onto its kind's controls.
function openRow(id: string) {
  fireEvent.click(within(rowFor(id)).getAllByRole("button")[0]!);
}

function openAddMenu() {
  fireEvent.keyDown(screen.getByRole("button", { name: /Add handler/ }), { key: "Enter" });
}

function writes() {
  return sent.filter((r) => r.method !== "GET");
}

describe("ScriptHandlers: what starts the script", () => {
  it("says nothing starts a script with no handler but someone running it", async () => {
    show();
    expect(await screen.findByText(/Nothing starts this script but someone running it/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Handlers" })).toBeInTheDocument();
    expect(screen.queryByTestId("handler-row")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Add handler/ })).toBeEnabled();
    expect(sent[0]).toMatchObject({ method: "GET", path: "/api/v1/portal/scripts/s1/handlers" });
  });

  it("says it is loading before the handlers are read", () => {
    answer = (_m, path) => (path.endsWith("/handlers") ? new Promise<Response>(() => {}) : undefined);
    show();
    expect(screen.getByText("Loading the handlers...")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add handler/ })).not.toBeInTheDocument();
  });

  it("says the handlers could not be read, and offers no add over them", async () => {
    answer = (_m, path) => (path.endsWith("/handlers") ? json({ detail: "the handler store failed" }, 500) : undefined);
    show();
    expect(await screen.findByText("This script's handlers could not be read.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add handler/ })).not.toBeInTheDocument();
    expect(screen.queryByText(/Nothing starts this script/)).not.toBeInTheDocument();
  });

  it("lists each handler with its type, what it answers to, and whether it is on", async () => {
    state.s1 = handlersOf({ handlers: [endpoint, schedule, evening, webhook] });
    show();
    await screen.findAllByTestId("handler-row");
    expect(rows().map((r) => r.textContent)).toEqual([
      "Endpoint/endpoints/orders/{id}Off",
      "ScheduleEvery weekday at 7:00 AM, America/Los_AngelesOn",
      "ScheduleEvery day at 6:00 PM, UTCOff",
      "WebhookEach event of shop-ordersOn",
    ]);
    expect(screen.queryByText(/Nothing starts this script/)).not.toBeInTheDocument();
  });

  it("badges a form and a schedule on a script that holds both", async () => {
    state.s1 = handlersOf({ handlers: [form, schedule] });
    show();
    await screen.findAllByTestId("handler-row");
    expect(within(rowFor("ep-2")).getByText("Form")).toBeInTheDocument();
    expect(within(rowFor("ep-2")).getByText("/endpoints/contact")).toBeInTheDocument();
    expect(within(rowFor("sched-1")).getByText("Schedule")).toBeInTheDocument();
  });

  it("opens a row onto its kind's own controls, and folds it again", async () => {
    state.s1 = handlersOf({ handlers: [endpoint, schedule, webhook] });
    show();
    await screen.findAllByTestId("handler-row");
    expect(screen.queryByRole("button", { name: "Update schedule" })).not.toBeInTheDocument();

    openRow("sched-1");
    expect(within(rowFor("sched-1")).getByRole("button", { name: "Update schedule" })).toBeInTheDocument();
    expect(within(rowFor("sched-1")).getByRole("button", { name: "Pause" })).toBeInTheDocument();
    expect(within(rowFor("sched-1")).getAllByRole("button")[0]).toHaveAttribute("aria-expanded", "true");

    openRow("wh-1");
    expect(within(rowFor("wh-1")).getByText(/runs this script once, with the event as run.event/)).toBeInTheDocument();
    expect(within(rowFor("wh-1")).getByRole("button", { name: "Turn off" })).toBeInTheDocument();

    openRow("ep-1");
    expect(within(rowFor("ep-1")).getByText("https://data.example.com/endpoints/orders/{id}")).toBeInTheDocument();
    expect(within(rowFor("ep-1")).getByRole("button", { name: "Turn on" })).toBeInTheDocument();

    openRow("sched-1");
    expect(within(rowFor("sched-1")).queryByRole("button", { name: "Update schedule" })).not.toBeInTheDocument();
  });

  // A script holds several schedules (#2434), so two can be open at once, each
  // with its own controls: a label must reach the control beside it, not the
  // first schedule's.
  it("keeps two open schedules' controls apart", async () => {
    state.s1 = handlersOf({ handlers: [schedule, evening] });
    show();
    await screen.findAllByTestId("handler-row");
    openRow("sched-1");
    openRow("sched-2");
    const times = screen.getAllByLabelText("Time") as HTMLInputElement[];
    expect(times).toHaveLength(2);
    expect(times[0]).not.toBe(times[1]);
    expect(times.map((t) => t.value)).toEqual(["07:00", "18:00"]);
    expect((screen.getAllByLabelText("Timezone") as HTMLSelectElement[]).map((t) => t.value)).toEqual([
      "America/Los_Angeles",
      "UTC",
    ]);
  });

  // The section sits at the same place for every script, so an unsaved
  // cadence must not survive onto another script's schedule.
  it("does not carry a part-typed cadence onto another script", async () => {
    state.s1 = handlersOf({ handlers: [schedule] });
    state.s9 = handlersOf({ handlers: [{ ...schedule, handler: "sched-9", cron: "0 9 * * *" }] });
    const view = show();
    await screen.findAllByTestId("handler-row");
    openRow("sched-1");
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "23:45" } });

    view.rerenderFor("s9");
    await waitFor(() => expect(rowFor("sched-9")).toBeDefined());
    openRow("sched-9");
    expect(screen.getByLabelText("Time")).toHaveValue("09:00");
  });
});

describe("ScriptHandlers: adding a handler", () => {
  it("offers the kinds the viewer may add, and says why for one they may not", async () => {
    state.s1 = handlersOf({
      add: [
        { kind: "endpoint", allowed: false, reason: "An administrator adds an endpoint to a script that is not a form.", contract: "handle(req)" },
        { kind: "schedule", allowed: true, contract: "main()" },
        { kind: "webhook", allowed: true, contract: "main()" },
      ],
    });
    show();
    await screen.findByText(/Nothing starts this script/);
    openAddMenu();
    const items = screen.getAllByRole("menuitem");
    expect(items.map((i) => i.textContent)).toEqual([
      "EndpointAn administrator adds an endpoint to a script that is not a form.",
      "ScheduleRun on a cadence",
      "WebhookRun for each event a webhook source stores",
    ]);
    expect(items[0]).toHaveAttribute("data-disabled");
    expect(items[1]).not.toHaveAttribute("data-disabled");
    fireEvent.click(items[0]!);
    expect(screen.queryByTestId("add-endpoint")).not.toBeInTheDocument();
  });

  it("says why a deployment that cannot bind endpoints offers none", async () => {
    state.s1 = handlersOf({
      add: [
        { kind: "endpoint", allowed: false, reason: "This deployment cannot bind scripts to endpoints.", contract: "handle(req)" },
        { kind: "schedule", allowed: true, contract: "main()" },
        { kind: "webhook", allowed: false, reason: "This deployment has no webhook sources.", contract: "main()" },
      ],
    });
    show();
    await screen.findByText(/Nothing starts this script/);
    openAddMenu();
    expect(screen.getByRole("menuitem", { name: /Endpoint/ })).toHaveTextContent("This deployment cannot bind scripts to endpoints.");
    expect(screen.getByRole("menuitem", { name: /Webhook/ })).toHaveTextContent("This deployment has no webhook sources.");
  });

  it("offers no Add handler where no kind may be added", async () => {
    const reason = "A library is loaded by other scripts and never runs.";
    state.s1 = handlersOf({ add: ALL_OFFERS.map((o) => ({ ...o, allowed: false, reason })) });
    show();
    await screen.findByText(/Nothing starts this script/);
    expect(screen.queryByRole("button", { name: /Add handler/ })).not.toBeInTheDocument();
  });

  it("adds a schedule as kind, cron, timezone and args, and lists it once it is saved", async () => {
    show();
    await screen.findByText(/Nothing starts this script/);
    openAddMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: /Schedule/ }));
    const panel = within(screen.getByTestId("add-schedule"));
    expect(panel.getByText("Add schedule", { selector: "p" })).toBeInTheDocument();
    // One addition at a time.
    expect(screen.getByRole("button", { name: /Add handler/ })).toBeDisabled();
    expect(screen.queryByText(/Nothing starts this script/)).not.toBeInTheDocument();

    fireEvent.click(panel.getByRole("button", { name: "Daily" }));
    fireEvent.change(panel.getByLabelText("Time"), { target: { value: "06:30" } });
    fireEvent.change(panel.getByLabelText("Timezone"), { target: { value: "UTC" } });
    fireEvent.change(panel.getByLabelText("report_date (optional)"), { target: { value: "${fire_date}" } });
    fireEvent.click(panel.getByRole("button", { name: "Add schedule" }));

    await waitFor(() => expect(screen.queryByTestId("add-schedule")).not.toBeInTheDocument());
    expect(writes()).toEqual([
      {
        method: "POST",
        path: "/api/v1/portal/scripts/s1/handlers",
        body: { kind: "schedule", cron: "30 6 * * *", timezone: "UTC", args: { report_date: "${fire_date}" } },
      },
    ]);
    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(rows()[0]).toHaveTextContent("Every day at 6:30 AM, UTC");
    expect(screen.getByRole("button", { name: /Add handler/ })).toBeEnabled();
  });

  it("cancels an addition without sending anything", async () => {
    show();
    await screen.findByText(/Nothing starts this script/);
    openAddMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: /Schedule/ }));
    fireEvent.click(within(screen.getByTestId("add-schedule")).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByTestId("add-schedule")).not.toBeInTheDocument();
    expect(screen.getByText(/Nothing starts this script/)).toBeInTheDocument();
    expect(writes()).toEqual([]);
  });

  it("adds a webhook for a source the script does not run for yet", async () => {
    state.s1 = handlersOf({ handlers: [webhook] });
    show();
    await screen.findAllByTestId("handler-row");
    openAddMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: /Webhook/ }));
    const panel = within(screen.getByTestId("add-webhook"));
    const select = panel.getByLabelText("Webhook source") as HTMLSelectElement;
    expect(Array.from(select.options).map((o) => o.value)).toEqual(["github-pushes"]);
    fireEvent.click(panel.getByRole("button", { name: "Add webhook" }));
    await waitFor(() => expect(screen.queryByTestId("add-webhook")).not.toBeInTheDocument());
    expect(writes()).toEqual([
      { method: "POST", path: "/api/v1/portal/scripts/s1/handlers", body: { kind: "webhook", event_source: "github-pushes" } },
    ]);
    await waitFor(() => expect(rows()).toHaveLength(2));
  });

  it("adds an endpoint as kind endpoint, and shows what the save said", async () => {
    show();
    await screen.findByText(/Nothing starts this script/);
    openAddMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: /Endpoint/ }));
    const panel = within(screen.getByTestId("add-endpoint"));
    fireEvent.change(panel.getByLabelText("Name"), { target: { value: "orders" } });
    fireEvent.click(panel.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByTestId("add-endpoint")).not.toBeInTheDocument());
    const post = writes()[0]!;
    expect(post).toMatchObject({ method: "POST", path: "/api/v1/portal/scripts/s1/handlers" });
    expect(post.body).toMatchObject({ kind: "endpoint", endpoint: "orders", methods: ["GET"], auth_mode: "authenticated" });
    // The warning a save carries (#2556) is shown, not lost with the form.
    expect(await screen.findByTestId("endpoint-save-warning")).toHaveTextContent(
      "Saved. This deployment trusts no proxy for a visitor's address.",
    );
    await waitFor(() => expect(rows()).toHaveLength(1));

    // Starting the next addition clears it.
    openAddMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: /Schedule/ }));
    expect(screen.queryByTestId("endpoint-save-warning")).not.toBeInTheDocument();
  });

  // #2494: a form's owner adds its endpoint for signed-in people at once, and
  // asks for public access from the same place, before it is published.
  it("lets a form's owner preview the form and ask for public access while adding its endpoint", async () => {
    signIn("sarah.chen@example.com", false);
    state.s1 = handlersOf({ endpoint: { name: contract.name, is_form: true, form_connections: ["scratch"] } });
    show();
    await screen.findByText(/Nothing starts this script/);
    openAddMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: /Endpoint/ }));
    const panel = within(screen.getByTestId("add-endpoint"));
    expect(panel.getByRole("button", { name: "Preview form" })).toBeInTheDocument();
    expect(panel.getByRole("button", { name: "Request public access" })).toBeInTheDocument();
    expect(await panel.findByTestId("form-public-status")).toBeInTheDocument();
    const access = panel.getByLabelText("Access") as HTMLSelectElement;
    expect(Array.from(access.options).map((o) => o.value)).toContain("authenticated");
  });

  it("offers no form publishing when the endpoint being added is not a form's", async () => {
    show();
    await screen.findByText(/Nothing starts this script/);
    openAddMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: /Endpoint/ }));
    expect(within(screen.getByTestId("add-endpoint")).queryByRole("button", { name: "Request public access" })).not.toBeInTheDocument();
  });
});

describe("ScriptHandlers: deleting a handler", () => {
  it("asks for confirmation, saying what goes, and then deletes the handler", async () => {
    state.s1 = handlersOf({ handlers: [schedule, webhook] });
    show();
    await screen.findAllByTestId("handler-row");
    openRow("wh-1");
    fireEvent.click(within(rowFor("wh-1")).getByRole("button", { name: "Delete webhook" }));
    const dialog = within(screen.getByRole("dialog"));
    expect(dialog.getByText("Delete this webhook?")).toBeInTheDocument();
    expect(dialog.getByText(/Events of shop-orders stop starting runs of this script/)).toBeInTheDocument();
    expect(writes()).toEqual([]);
    fireEvent.click(dialog.getByRole("button", { name: "Delete webhook" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(writes()).toEqual([{ method: "DELETE", path: "/api/v1/portal/handlers/wh-1", body: undefined }]);
    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(rows()[0]).toHaveAttribute("data-handler", "sched-1");
  });

  it("deletes nothing when the confirmation is cancelled", async () => {
    state.s1 = handlersOf({ handlers: [schedule] });
    show();
    await screen.findAllByTestId("handler-row");
    openRow("sched-1");
    fireEvent.click(within(rowFor("sched-1")).getByRole("button", { name: "Delete schedule" }));
    expect(screen.getByText(/A schedule that already produced runs is kept/)).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(writes()).toEqual([]);
    expect(rows()).toHaveLength(1);
  });

  it("names an endpoint's address and a form's kept submissions before deleting it", async () => {
    state.s1 = handlersOf({ handlers: [form] });
    show();
    await screen.findAllByTestId("handler-row");
    openRow("ep-2");
    fireEvent.click(within(rowFor("ep-2")).getByRole("button", { name: "Delete form" }));
    expect(screen.getByText(/\/endpoints\/contact answers nothing from then on/)).toBeInTheDocument();
    expect(screen.getByText(/A form's submissions stay stored/)).toBeInTheDocument();
  });

  it("shows a refused delete in the dialog, in the server's words", async () => {
    state.s1 = handlersOf({ handlers: [schedule] });
    answer = (method) =>
      method === "DELETE"
        ? json({ detail: "schedule handler sched-1 of daily-sales-report produced runs, which name it, so it is kept" }, 403)
        : undefined;
    show();
    await screen.findAllByTestId("handler-row");
    openRow("sched-1");
    fireEvent.click(within(rowFor("sched-1")).getByRole("button", { name: "Delete schedule" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete schedule" }));
    expect(await within(screen.getByRole("dialog")).findByText(/produced runs, which name it, so it is kept/)).toBeInTheDocument();
    expect(rows()).toHaveLength(1);
  });
});

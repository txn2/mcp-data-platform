import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { getResponse } from "msw";
import { handlerRouteMocks } from "@/mocks/handlers/handlerRoutes";
import { scriptHandlers } from "@/mocks/handlers/scripts";
import { renderWithQuery, signIn, type SentRequest } from "@/test/applicationHarness";
import { ConnectionHandlersCard } from "./ConnectionHandlersCard";
import { Outcome } from "./TestCallDialog";

// A connection's Handlers section (#2436, #2634, #2635): policies, transforms
// in order and watches with their counts, for an administrator only; the add
// dialog and the test panel open from it.

afterEach(() => vi.unstubAllGlobals());

function serve(): SentRequest[] {
  const sent: SentRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
      sent.push({ method: init?.method ?? "GET", path: url.pathname + url.search, body });
      const res = await getResponse([...handlerRouteMocks, ...scriptHandlers], new Request(url, init));
      return res ?? new Response(JSON.stringify({ detail: "no handler" }), { status: 404 });
    }),
  );
  return sent;
}

describe("ConnectionHandlersCard", () => {
  it("lists a connection's policies and watches with what they match and their last day", async () => {
    serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ConnectionHandlersCard connectionName="crm-api" />);
    const policies = await screen.findByTestId("handlers-policy");
    expect(policies).toHaveTextContent("my-margin-check");
    expect(policies).toHaveTextContent("v1 runs · v2 waits for approval");
    expect(policies).toHaveTextContent("analyst");
    expect(policies).toHaveTextContent("createLead");
    expect(policies).toHaveTextContent("items: body.leads");
    expect(policies).toHaveTextContent("412 matched · 9 held · 1 error");
    const watches = screen.getByTestId("handlers-watch");
    expect(watches).toHaveTextContent("where id = 123");
    expect(watches).toHaveTextContent("2 runs");
    expect(screen.queryByTestId("handlers-transform")).toBeNull();
  });

  it("orders a connection's transforms", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ConnectionHandlersCard connectionName="acme-warehouse" />);
    const transforms = await screen.findByTestId("handlers-transform");
    expect(transforms).toHaveTextContent("hive.hr.employees.ssn");
    const rows = within(transforms).getAllByTestId("connection-handler-row");
    expect(rows.map((r) => r.getAttribute("data-handler"))).toEqual(["ch-mask", "ch-owner"]);
    expect(within(rows[0] as HTMLElement).queryByRole("button", { name: /up$/ })).toBeNull();
    fireEvent.click(within(transforms).getByRole("button", { name: "Move warehouse-freshness down" }));
    await waitFor(() =>
      expect(sent.find((r) => r.method === "PUT" && r.path.endsWith("/handlers/order"))?.body).toEqual({ ids: ["ch-owner", "ch-mask"] }),
    );
  });

  it("says when a connection has no handlers", async () => {
    serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ConnectionHandlersCard connectionName="nothing-here" />);
    expect(await screen.findByText(/No handlers on this connection/)).toBeInTheDocument();
  });

  it("shows nothing to someone who is not an administrator", () => {
    const sent = serve();
    signIn("jo@example.com", false);
    const { container } = renderWithQuery(<ConnectionHandlersCard connectionName="crm-api" />);
    expect(container).toBeEmptyDOMElement();
    expect(sent).toHaveLength(0);
  });

  it("tests a call and shows each policy it reached", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ConnectionHandlersCard connectionName="crm-api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Test a call" }));
    fireEvent.change(screen.getByLabelText("As"), { target: { value: "jo@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Test" }));
    const outcome = await screen.findByTestId("test-outcome");
    expect(outcome).toHaveTextContent("Held for approval");
    expect(outcome).toHaveTextContent("my-margin-check");
    expect(outcome).toHaveTextContent("Needs approval");
    expect(sent.find((r) => r.path.endsWith("/handlers/test"))?.body).toMatchObject({ tool: "api_invoke_endpoint", as_user: "jo@example.com" });
  });

  it("refuses arguments that are not JSON before asking", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ConnectionHandlersCard connectionName="crm-api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Test a call" }));
    fireEvent.change(screen.getByLabelText("Arguments"), { target: { value: "{" } });
    fireEvent.click(screen.getByRole("button", { name: "Test" }));
    expect(await screen.findByText("The arguments are not JSON.")).toBeInTheDocument();
    expect(sent.some((r) => r.path.endsWith("/handlers/test"))).toBe(false);
  });

  it("adds a policy to the connection from a chosen script, approving its version", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ConnectionHandlersCard connectionName="crm-api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Add handler" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect(within(dialog).getAllByRole("option").length).toBeGreaterThan(1));
    fireEvent.change(within(dialog).getByRole("combobox", { name: "Script" }), { target: { value: "script-001" } });
    await within(dialog).findByTestId("call-handler-form-policy");
    fireEvent.change(within(dialog).getByLabelText("Add an operation"), { target: { value: "createLead" } });
    fireEvent.keyDown(within(dialog).getByLabelText("Add an operation"), { key: "Enter" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add handler" }));
    await waitFor(() => {
      const body = sent.find((r) => r.method === "POST" && r.path === "/api/v1/portal/scripts/script-001/handlers")?.body as Record<string, unknown>;
      expect(body).toMatchObject({ kind: "policy", connection: "crm-api", operations: ["createLead"], approve: true, on_failure: "hold" });
    });
  });
});

describe("Outcome", () => {
  it("names a refusal, a run with what follows, and a call no handler applies to", () => {
    const { rerender } = renderWithQuery(
      <Outcome outcome={{ outcome: "denied", denied_by: "paying-check", reason: "No paid plan.", handlers: [], not_applying: [] }} who="" />,
    );
    expect(screen.getByTestId("test-outcome")).toHaveTextContent("Refused");
    expect(screen.getByTestId("test-outcome")).toHaveTextContent("paying-check: “No paid plan.”");
    rerender(<Outcome outcome={{ outcome: "runs", handlers: [], not_applying: [] }} who="" />);
    expect(screen.getByTestId("test-outcome")).toHaveTextContent("No handler applies");
    rerender(
      <Outcome
        outcome={{
          outcome: "runs",
          handlers: [{ id: "t", kind: "transform", script: "mask", outcome: "transformed", ms: 2 }],
          not_applying: [{ id: "x", kind: "policy", script: "other", operations: ["deleteList"] }],
          transforms: 1,
          watches: 2,
          result: { card: "•••• 1111" },
        }}
        who=""
      />,
    );
    const out = screen.getByTestId("test-outcome");
    expect(out).toHaveTextContent("1 transform would change the result; 2 watches would queue a run.");
    expect(out).toHaveTextContent("other: does not apply (deleteList only)");
    expect(out).toHaveTextContent("•••• 1111");
  });
});

import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers as mockRoutes } from "@/mocks/handlers";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { renderWithQuery, signIn, type SentRequest } from "@/test/applicationHarness";
import { ScriptHandlers } from "../ScriptHandlers";
import { typedArgs } from "./CallHandlerForm";
import { appliesTo, operationsText, statsText, versionText } from "./format";

// A script's tool-call handlers on its page (#2436, #2634, #2635): each opens
// onto what it matches, which version runs, and for an administrator its
// controls and failures; a newer version waiting is said above the list.

afterEach(() => vi.unstubAllGlobals());

function serve(): SentRequest[] {
  const sent: SentRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
      sent.push({ method: init?.method ?? "GET", path: url.pathname + url.search, body });
      const res = await getResponse(mockRoutes, new Request(url, init));
      return res ?? new Response(JSON.stringify({ detail: "no handler" }), { status: 404 });
    }),
  );
  return sent;
}

const contract = (id: string, version: number): ScriptContract => ({
  id,
  name: id,
  status: "active",
  enabled: true,
  params: [{ name: "limit", type: "int", description: "calls allowed", required: false }],
  version,
});

describe("a script's tool-call handlers", () => {
  // The owner reads first: the administrator's test approves the waiting
  // version in the mock session.
  it("tells the owner the version waits, and gives them no controls", async () => {
    serve();
    signIn("sarah.chen@example.com", false);
    renderWithQuery(<ScriptHandlers scriptId="script-004" contract={contract("script-004", 2)} />);
    const banner = await screen.findByTestId("handler-version-banner");
    expect(banner).toHaveTextContent("Version 2 waits for an administrator. Version 1 keeps running.");
    expect(within(banner).queryByRole("button")).toBeNull();
    const row = (await screen.findAllByTestId("handler-row")).find((r) => r.getAttribute("data-handler") === "ch-ratelimit");
    fireEvent.click(within(row as HTMLElement).getByRole("button", { expanded: false }));
    const panel = await screen.findByTestId("call-handler-panel");
    expect(panel).toHaveTextContent("An administrator adds, changes and turns these on and off.");
    expect(within(panel).queryByRole("button", { name: "Edit" })).toBeNull();
    expect(within(panel).queryByTestId("handler-failures")).toBeNull();
  });

  it("shows an administrator the waiting version, approves it, and opens a policy with its failures", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ScriptHandlers scriptId="script-004" contract={contract("script-004", 2)} />);
    const banner = await screen.findByTestId("handler-version-banner");
    expect(banner).toHaveTextContent("Version 2 waits for approval for handlers. Version 1 keeps running.");
    const row = (await screen.findAllByTestId("handler-row")).find((r) => r.getAttribute("data-handler") === "ch-ratelimit");
    expect(row).toHaveTextContent("Policy");
    expect(row).toHaveTextContent("crm-api · createLead");
    fireEvent.click(within(row as HTMLElement).getByRole("button", { expanded: false }));
    const panel = await screen.findByTestId("call-handler-panel");
    expect(panel).toHaveTextContent("analyst");
    expect(panel).toHaveTextContent("v1 runs, approved by admin@example.com; v2 waits for approval");
    expect(within(panel).getByTestId("handler-failures")).toHaveTextContent("ran past its 2,000 ms limit");
    fireEvent.click(within(banner).getByRole("button", { name: "Approve version 2" }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/handlers/ch-ratelimit")?.body).toEqual({ approve: true }));
  });

  it("adds a transform from the script's page with its connection and protected column", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ScriptHandlers scriptId="script-005" contract={contract("script-005", 1)} />);
    fireEvent.keyDown(await screen.findByRole("button", { name: /Add handler/ }), { key: "Enter" });
    fireEvent.click(await screen.findByRole("menuitem", { name: /Transform/ }));
    const form = await screen.findByTestId("call-handler-form-transform");
    fireEvent.change(within(form).getByLabelText("Connection"), { target: { value: "acme-warehouse" } });
    fireEvent.change(within(form).getByLabelText("Add a protected column"), { target: { value: "hive.hr.employees.ssn" } });
    fireEvent.keyDown(within(form).getByLabelText("Add a protected column"), { key: "Enter" });
    expect(within(form).getByLabelText(/Return the original result/)).toBeDisabled();
    fireEvent.click(within(form).getByRole("button", { name: "Add handler" }));
    await waitFor(() =>
      expect(sent.find((r) => r.method === "POST" && r.path === "/api/v1/portal/scripts/script-005/handlers")?.body).toMatchObject({
        kind: "transform",
        connection: "acme-warehouse",
        protected: ["hive.hr.employees.ssn"],
        on_failure: "fail",
        approve: true,
      }),
    );
  });

  it("refuses a handler with no connection before asking", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ScriptHandlers scriptId="script-005" contract={contract("script-005", 1)} />);
    fireEvent.keyDown(await screen.findByRole("button", { name: /Add handler/ }), { key: "Enter" });
    fireEvent.click(await screen.findByRole("menuitem", { name: /Watch/ }));
    const form = await screen.findByTestId("call-handler-form-watch");
    fireEvent.click(within(form).getByRole("button", { name: "Add handler" }));
    expect(await within(form).findByText("Name the connection the handler watches.")).toBeInTheDocument();
    expect(sent.some((r) => r.method === "POST" && r.path.endsWith("/handlers"))).toBe(false);
  });
});

describe("handler words", () => {
  it("reads a handler's match, version and day", () => {
    expect(appliesTo({ personas: [], users: [] })).toBe("Everyone");
    expect(appliesTo({ personas: ["support"], users: ["sam@example.com"] })).toBe("support, sam@example.com");
    expect(operationsText({ operations: [] })).toBe("Every call");
    expect(versionText({ running_version: 0 }, 3)).toBe("Not running: version 3 was never approved");
    expect(versionText({ running_version: 2, approved_by: "ana@example.com", waiting_version: 0 }, 2)).toBe("v2, approved by ana@example.com");
    expect(statsText(undefined, "watch")).toEqual(["No runs"]);
    expect(statsText({ matched: 88, denied: 12, held: 0, errors: 1, queued: 0, p50_ms: 41, p99_ms: 180 }, "policy")).toEqual([
      "88 matched · 12 denied · 1 error",
      "p50 41 ms · p99 180 ms",
    ]);
    expect(typedArgs({ limit: "3", on: "true", tags: '["a"]', who: "x", none: "" }, [
      { name: "limit", type: "int", required: false },
      { name: "on", type: "bool", required: false },
      { name: "tags", type: "list", required: false },
    ])).toEqual({ limit: 3, on: true, tags: ["a"], who: "x" });
  });
});

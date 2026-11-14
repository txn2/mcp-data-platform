import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { getResponse } from "msw";
import { approvalHandlers } from "@/mocks/handlers/approvals";
import { renderWithQuery, signIn, type SentRequest } from "@/test/applicationHarness";
import { ApprovalsPage, mayDecide } from "./ApprovalsPage";

// The Approvals queue (#2494): a pending request with what changed since the
// version approved before, a preview of the version, and an administrator's
// decision; a requester reads their own and decides nothing.

afterEach(() => {
  vi.unstubAllGlobals();
});

function serve(): SentRequest[] {
  const sent: SentRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
      sent.push({ method: init?.method ?? "GET", path: url.pathname + url.search, body });
      const res = await getResponse(approvalHandlers, new Request(url, init));
      return res ?? new Response(JSON.stringify({ detail: "no handler" }), { status: 404 });
    }),
  );
  return sent;
}

describe("ApprovalsPage", () => {
  it("lets an administrator preview a pending request's version, read what changed, and approve it", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    const open = vi.fn();
    vi.stubGlobal("open", open);
    renderWithQuery(<ApprovalsPage onNavigate={vi.fn()} />);

    const card = await screen.findByTestId("approval-apr_intake2");
    expect(card).toHaveTextContent("order-intake");
    expect(card).toHaveTextContent("version 2");
    expect(card).toHaveTextContent("Asked by j.martinez@example.com");
    expect(card).toHaveTextContent("Changed since version 1 (approved): 1 lines added, 0 removed");

    fireEvent.click(screen.getByRole("button", { name: "Preview version 2" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith("/forms/_preview/fp0123", "_blank", "noopener,noreferrer"));
    expect(sent.find((r) => r.path === "/api/v1/portal/scripts/script-intake/form-preview")?.body).toEqual({ version: 2 });

    fireEvent.change(within(card).getByLabelText("Comment for the requester"), { target: { value: "Ship it" } });
    fireEvent.click(within(card).getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(sent.find((r) => r.path === "/api/v1/portal/approvals/apr_intake2/decide")?.body).toEqual({
        decision: "approve",
        comment: "Ship it",
      }),
    );
  });

  it("shows a requester their requests with no decision controls", async () => {
    serve();
    signIn("j.martinez@example.com", false);
    renderWithQuery(<ApprovalsPage onNavigate={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "approved" } });
    const card = await screen.findByTestId("approval-apr_intake1");
    expect(card).toHaveTextContent("No version of this form was approved before.");
    expect(card).toHaveTextContent("approved by admin@example.com");
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  // A site release's approval (#2518) has no form to preview or compare: it says
  // which release would serve where.
  it("shows a site release waiting to serve an environment", async () => {
    const siteRequest = {
      id: "apr_site1", subject_kind: "site_release", purpose: "site_promote", script_id: "", version: 3,
      site_release_id: "srel_1", site_name: "marketing", target: "prod", source_sha256: "abc",
      requester_email: "owner@example.com", changes: { approved_version: 0, added: 0, removed: 0 },
      status: "pending", self_approved: false, created_at: "2026-11-08T00:00:00Z",
    };
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ requests: [siteRequest], count: 1 }), { status: 200 })));
    signIn("admin@example.com", true);
    renderWithQuery(<ApprovalsPage onNavigate={vi.fn()} />);
    const card = await screen.findByTestId("approval-apr_site1");
    expect(card).toHaveTextContent("marketing");
    expect(card).toHaveTextContent("release 3");
    expect(card).toHaveTextContent("Serve this release in prod.");
    expect(card).not.toHaveTextContent("Preview version");
    expect(screen.getByRole("button", { name: "Approve" })).toBeTruthy();
  });

  // A request to make a private site environment public (#2604) names the
  // environment and every address that would stop asking for a sign-in.
  it("shows a request to make a site environment public, with the addresses that would open", async () => {
    const publicRequest = {
      id: "apr_pub1", subject_kind: "site_release", purpose: "site_public", script_id: "", version: 0,
      site_name: "docs", target: "prod", source_sha256: "public:site_docs:prod:1",
      requester_email: "priya.nair@example.com", reason: "4.1 docs are ready for customers", changes: { approved_version: 0, added: 0, removed: 0 },
      status: "pending", self_approved: false, created_at: "2026-11-10T10:15:00Z",
    };
    const docs = {
      id: "site_docs", name: "docs", description: "", owner_email: "priya.nair@example.com", main_environment: "",
      environments: [{ name: "prod", release: 29, requires_approval: true, public: false, internal_view: false, updated_at: "2026-11-05T11:20:00Z" }],
      created_by: "priya.nair@example.com", created_at: "2026-08-21T00:00:00Z", updated_at: "2026-11-05T11:20:00Z", address: "",
      // As the server answers since #2607: addresses puts the domain first, pages_addresses keeps the pages address.
      addresses: { prod: "https://docs.example.com" }, pages_addresses: { prod: "https://docs--prod.pages.example.net" }, domains: [{ hostname: "docs.example.com", environment: "prod", created_by: "x", created_at: "2026-11-01T00:00:00Z" }],
      permissions: { manage: true, own: true, delete: true },
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string) =>
        String(input).includes("/sites/docs")
          ? new Response(JSON.stringify(docs), { status: 200 })
          : new Response(JSON.stringify({ requests: [publicRequest], count: 1 }), { status: 200 }),
      ),
    );
    signIn("admin@example.com", true);
    renderWithQuery(<ApprovalsPage onNavigate={vi.fn()} />);
    const card = await screen.findByTestId("approval-apr_pub1");
    expect(card).toHaveTextContent("Make public");
    expect(card).toHaveTextContent("Open prod to anyone, without a sign-in.");
    await waitFor(() => expect(card).toHaveTextContent("docs.example.com"));
    expect(card).toHaveTextContent("docs--prod.pages.example.net");
    expect(card).toHaveTextContent("prod serves release 29 today.");
  });

  // A held call (#2437): the platform's reading of it, what held it, who
  // decides, the requester's recent calls, and an approval of fewer calls than
  // asked with a deadline.
  it("shows a held call with its recent calls and approves an allowance", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ApprovalsPage onNavigate={vi.fn()} />);
    const card = await screen.findByTestId("approval-apr_call_subscribers");
    expect(card).toHaveTextContent("addSubscribers on billing-api");
    expect(card).toHaveTextContent("jo@example.com, as marketing");
    expect(card).toHaveTextContent("subscriber-rate-limit: “5 subscribers in one call; the limit is 3 per hour, 0 used.”");
    expect(card).toHaveTextContent("ops-leads, or an administrator");
    expect(card).toHaveTextContent("POST /lists/newsletter-weekly/subscribers");
    const recent = await within(card).findByTestId("recent-calls");
    expect(recent).toHaveTextContent("Held");
    expect(recent).toHaveTextContent("Ran");

    fireEvent.change(within(card).getByLabelText("Approve"), { target: { value: "1" } });
    fireEvent.change(within(card).getByLabelText("calls until"), { target: { value: "2026-11-13T23:59" } });
    fireEvent.click(within(card).getByRole("button", { name: "Approve" }));
    await waitFor(() => {
      const body = sent.find((r) => r.path === "/api/v1/portal/approvals/apr_call_subscribers/decide")?.body as Record<string, unknown>;
      expect(body.decision).toBe("approve");
      expect(body.calls).toBe(1);
      expect(String(body.until)).toMatch(/^2026-11-1[34]T/);
    });
  });

  it("filters by type and shows a handler version for administrators to decide", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    renderWithQuery(<ApprovalsPage onNavigate={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Type"), { target: { value: "handler_version" } });
    const card = await screen.findByTestId("approval-apr_handler_paying");
    expect(card).toHaveTextContent("Handler version");
    expect(card).toHaveTextContent("paying-customer-check");
    expect(card).toHaveTextContent("Saved by kim@example.com");
    expect(screen.queryByTestId("approval-apr_call_subscribers")).toBeNull();
    expect(sent.some((r) => r.path.includes("kind=handler_version"))).toBe(true);
  });

  it("shows a requester an approved allowance and how much of it is used", async () => {
    serve();
    signIn("sarah.chen@example.com", false);
    renderWithQuery(<ApprovalsPage onNavigate={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "approved" } });
    const card = await screen.findByTestId("approval-apr_call_leads");
    expect(within(card).getByTestId("allowance")).toHaveTextContent("10 calls until");
    expect(within(card).getByTestId("allowance")).toHaveTextContent("3 used. Make the call again to use it.");
    expect(within(card).queryByRole("button", { name: "Approve" })).toBeNull();
  });
});

describe("mayDecide", () => {
  const call = { subject_kind: "call", requester_email: "jo@example.com", approvers: ["ops-leads", "ana@example.com"] } as never;
  it("follows the server's rule", () => {
    expect(mayDecide(call, true, "jo@example.com", "")).toBe(true);
    expect(mayDecide(call, false, "jo@example.com", "ops-leads")).toBe(false);
    expect(mayDecide(call, false, "Ana@example.com", "")).toBe(true);
    expect(mayDecide(call, false, "sam@example.com", "ops-leads")).toBe(true);
    expect(mayDecide(call, false, "sam@example.com", "support")).toBe(false);
    expect(mayDecide({ subject_kind: "script_version" } as never, false, "sam@example.com", "ops-leads")).toBe(false);
  });
});

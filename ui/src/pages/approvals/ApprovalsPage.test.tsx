import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { getResponse } from "msw";
import { approvalHandlers } from "@/mocks/handlers/approvals";
import { renderWithQuery, signIn, type SentRequest } from "@/test/applicationHarness";
import { ApprovalsPage } from "./ApprovalsPage";

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

    fireEvent.change(screen.getByLabelText("Comment for the requester"), { target: { value: "Ship it" } });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
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
});

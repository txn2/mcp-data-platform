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
});

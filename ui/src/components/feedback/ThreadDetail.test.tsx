import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useThread: vi.fn(),
  useThreadEvents: vi.fn(() => ({ data: [], isLoading: false })),
  useThreadChain: vi.fn(),
  useAppendThreadEvent: vi.fn(() => ({ mutate: vi.fn(), isPending: false, isError: false })),
  useUpdateThread: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useDeleteThread: vi.fn(() => ({ mutate: vi.fn(), isPending: false, isError: false })),
  useRespondValidation: vi.fn(() => ({ mutate: vi.fn(), isPending: false, isError: false })),
}));

vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: { user: { email: string; is_admin: boolean } }) => unknown) =>
    sel({ user: { email: "viewer@example.com", is_admin: false } }),
}));

import { fireEvent } from "@testing-library/react";
import { ThreadDetail } from "./ThreadDetail";
import { useThread, useThreadChain, useRespondValidation } from "@/api/portal/hooks";

const mockUseThread = vi.mocked(useThread);
const mockUseThreadChain = vi.mocked(useThreadChain);
const mockUseRespondValidation = vi.mocked(useRespondValidation);

function baseThread(overrides: Record<string, unknown> = {}) {
  return {
    id: "t1",
    kind: "correction",
    target_type: "asset",
    asset_id: "a1",
    author_id: "sme@example.com",
    author_email: "sme@example.com",
    status: "resolved",
    requires_resolution: true,
    validation_state: "none",
    created_at: "2026-06-04T13:00:00Z",
    updated_at: "2026-06-04T18:45:00Z",
    ...overrides,
  };
}

describe("ThreadDetail knowledge chain", () => {
  it("renders the insight and changeset chain when the thread is linked", () => {
    mockUseThread.mockReturnValue({ data: baseThread({ insight_id: "ins_abc123def456" }) } as never);
    mockUseThreadChain.mockReturnValue({
      data: {
        thread_id: "t1",
        insight_id: "ins_abc123def456",
        changesets: [
          {
            id: "cs_1",
            target_urn: "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.churn,PROD)",
            change_type: "update_description",
            created_at: "2026-06-04T18:45:00Z",
            rolled_back: false,
          },
        ],
      },
      isLoading: false,
    } as never);

    render(<ThreadDetail threadId="t1" canModerate={false} onBack={() => {}} onDeleted={() => {}} />);

    expect(screen.getByText("Knowledge chain")).toBeInTheDocument();
    expect(screen.getByText("update_description")).toBeInTheDocument();
    expect(
      screen.getByText("urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.churn,PROD)"),
    ).toBeInTheDocument();
  });

  it("shows no chain panel for a thread without an insight", () => {
    mockUseThread.mockReturnValue({ data: baseThread() } as never);
    mockUseThreadChain.mockReturnValue({ data: undefined, isLoading: false } as never);

    render(<ThreadDetail threadId="t1" canModerate={false} onBack={() => {}} onDeleted={() => {}} />);

    expect(screen.queryByText("Knowledge chain")).not.toBeInTheDocument();
  });

  it("notes when a linked insight has produced no changes yet", () => {
    mockUseThread.mockReturnValue({ data: baseThread({ insight_id: "ins_xyz" }) } as never);
    mockUseThreadChain.mockReturnValue({
      data: { thread_id: "t1", insight_id: "ins_xyz", changesets: [] },
      isLoading: false,
    } as never);

    render(<ThreadDetail threadId="t1" canModerate={false} onBack={() => {}} onDeleted={() => {}} />);

    expect(screen.getByText(/no catalog changes applied/i)).toBeInTheDocument();
  });
});

describe("ThreadDetail validation control (#603)", () => {
  it("lets the feedback author validate or dispute a pending request", () => {
    const respond = vi.fn();
    mockUseRespondValidation.mockReturnValue({ mutate: respond, isPending: false, isError: false } as never);
    mockUseThreadChain.mockReturnValue({ data: undefined, isLoading: false } as never);
    // Author matches the mocked auth user; validation is pending.
    mockUseThread.mockReturnValue({
      data: baseThread({ author_email: "viewer@example.com", validation_state: "pending" }),
    } as never);

    render(<ThreadDetail threadId="t1" canModerate={false} onBack={() => {}} onDeleted={() => {}} />);

    expect(screen.getByText(/your validation was requested/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /validate/i }));
    expect(respond).toHaveBeenCalledWith({ threadId: "t1", result: "validated", reason: undefined });
  });

  it("hides the control when the viewer is not the author", () => {
    mockUseRespondValidation.mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false } as never);
    mockUseThreadChain.mockReturnValue({ data: undefined, isLoading: false } as never);
    mockUseThread.mockReturnValue({
      data: baseThread({ author_email: "someone-else@example.com", validation_state: "pending" }),
    } as never);

    render(<ThreadDetail threadId="t1" canModerate={false} onBack={() => {}} onDeleted={() => {}} />);
    expect(screen.queryByText(/your validation was requested/i)).not.toBeInTheDocument();
  });
});

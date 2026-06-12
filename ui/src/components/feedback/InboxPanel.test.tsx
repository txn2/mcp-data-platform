import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  usePractitionerWorklist: vi.fn(),
  useSMEWorklist: vi.fn(),
}));

import { InboxPanel } from "./InboxPanel";
import { usePractitionerWorklist, useSMEWorklist } from "@/api/portal/hooks";

const mockPractitioner = vi.mocked(usePractitionerWorklist);
const mockSME = vi.mocked(useSMEWorklist);

function row(overrides: Record<string, unknown> = {}) {
  return {
    id: "t1",
    kind: "correction",
    target_type: "asset",
    asset_id: "a1",
    author_id: "sme@example.com",
    author_email: "sme@example.com",
    status: "open",
    requires_resolution: true,
    validation_state: "none",
    title: "Churn is actually retention",
    created_at: "2026-06-04T13:00:00Z",
    updated_at: "2026-06-04T13:00:00Z",
    event_count: 1,
    last_event_at: "2026-06-04T13:00:00Z",
    ...overrides,
  };
}

describe("InboxPanel", () => {
  it("shows practitioner worklist items and opens a thread", () => {
    mockPractitioner.mockReturnValue({ data: { data: [row()], total: 1 }, isLoading: false, isError: false } as never);
    mockSME.mockReturnValue({ data: { data: [], total: 0 }, isLoading: false, isError: false } as never);
    const onOpen = vi.fn();

    render(<InboxPanel onOpenThread={onOpen} />);
    expect(screen.getByText("Churn is actually retention")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Churn is actually retention"));
    expect(onOpen).toHaveBeenCalledWith("t1");
  });

  it("switches to the SME tab and shows the empty state", () => {
    mockPractitioner.mockReturnValue({ data: { data: [row()], total: 1 }, isLoading: false, isError: false } as never);
    mockSME.mockReturnValue({ data: { data: [], total: 0 }, isLoading: false, isError: false } as never);

    render(<InboxPanel />);
    fireEvent.click(screen.getByRole("button", { name: /awaiting my validation/i }));
    expect(screen.getByText(/all caught up/i)).toBeInTheDocument();
  });
});

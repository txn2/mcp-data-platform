import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useFeedbackActivity: vi.fn(),
}));

import { ActivityFeed } from "./ActivityFeed";
import { useFeedbackActivity } from "@/api/portal/hooks";

const mockActivity = vi.mocked(useFeedbackActivity);

function item(overrides: Record<string, unknown> = {}) {
  return {
    id: "t1",
    kind: "correction",
    target_type: "asset",
    asset_id: "a1",
    target_label: "Revenue Dashboard",
    author_id: "u@example.com",
    author_email: "u@example.com",
    status: "open",
    requires_resolution: true,
    validation_state: "none",
    title: "Churn is actually retention",
    created_at: "2026-06-04T13:00:00Z",
    updated_at: "2026-06-04T13:00:00Z",
    event_count: 3,
    last_event_at: "2026-06-04T13:00:00Z",
    ...overrides,
  };
}

describe("ActivityFeed", () => {
  it("renders a row and opens the thread on click", () => {
    mockActivity.mockReturnValue({ data: { data: [item()], total: 1 }, isLoading: false, isError: false } as never);
    const onOpen = vi.fn();
    const onNavigate = vi.fn();

    render(<ActivityFeed onOpenThread={onOpen} onNavigate={onNavigate} />);
    expect(screen.getByText("Revenue Dashboard")).toBeInTheDocument();
    expect(screen.getByText("Churn is actually retention")).toBeInTheDocument();
    // 2 replies (event_count 3 minus the opening event).
    expect(screen.getByText(/2 replies/)).toBeInTheDocument();

    fireEvent.click(screen.getByText("Churn is actually retention"));
    expect(onOpen).toHaveBeenCalledWith("t1");
  });

  it("navigates to the target item without opening the thread", () => {
    mockActivity.mockReturnValue({ data: { data: [item()], total: 1 }, isLoading: false, isError: false } as never);
    const onOpen = vi.fn();
    const onNavigate = vi.fn();

    render(<ActivityFeed onOpenThread={onOpen} onNavigate={onNavigate} />);
    fireEvent.click(screen.getByText("Revenue Dashboard"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/a1");
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("shows the empty state when there is no feedback", () => {
    mockActivity.mockReturnValue({ data: { data: [], total: 0 }, isLoading: false, isError: false } as never);
    render(<ActivityFeed onOpenThread={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText(/no feedback yet/i)).toBeInTheDocument();
  });
});

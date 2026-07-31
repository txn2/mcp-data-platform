import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { NotificationHistory, NotificationItem } from "@/api/portal/hooks";

vi.mock("@/api/portal/hooks", () => ({
  useMyNotifications: vi.fn(),
}));

import { useMyNotifications } from "@/api/portal/hooks";
import { MyNotifications } from "./MyNotifications";

const mockUseHistory = vi.mocked(useMyNotifications);

function item(overrides: Partial<NotificationItem> = {}): NotificationItem {
  return {
    id: 1,
    category: "share",
    subject: 'alice@example.com shared the asset "Q3 Revenue" with you',
    item_title: "Q3 Revenue",
    actor: "alice@example.com",
    digest: false,
    status: "sent",
    sent_at: "2026-07-01T12:00:00Z",
    created_at: "2026-07-01T11:59:00Z",
    ...overrides,
  };
}

function history(overrides: Partial<NotificationHistory> = {}): NotificationHistory {
  return {
    data: [item()],
    total: 1,
    page: 1,
    per_page: 20,
    retention_days: 30,
    ...overrides,
  };
}

function show(data?: NotificationHistory, state: { isLoading?: boolean; error?: unknown } = {}) {
  mockUseHistory.mockReturnValue({
    data,
    isLoading: state.isLoading ?? false,
    error: state.error ?? null,
  } as ReturnType<typeof useMyNotifications>);
  render(<MyNotifications />);
}

beforeEach(() => {
  mockUseHistory.mockReset();
});

afterEach(cleanup);

describe("MyNotifications", () => {
  it("lists the notifications addressed to the user", () => {
    show(history());
    expect(screen.getByText(/shared the asset/i)).toBeInTheDocument();
    expect(screen.getByText("Sent")).toBeInTheDocument();
  });

  it("states the retention window so the list does not read as a full record", () => {
    show(history({ retention_days: 30 }));
    expect(screen.getByText(/removed after 30 days/i)).toBeInTheDocument();
  });

  it("falls back to a generic retention note when the server reports no window", () => {
    show(history({ retention_days: 0 }));
    expect(screen.getByText(/retention schedule/i)).toBeInTheDocument();
  });

  it("labels a failed delivery in the recipient's terms, with no error detail", () => {
    show(history({ data: [item({ status: "failed", sent_at: undefined })] }));
    expect(screen.getByText("Not delivered")).toBeInTheDocument();
    expect(screen.queryByText(/smtp|connection refused/i)).not.toBeInTheDocument();
  });

  it("explains the empty state instead of showing a bare blank", () => {
    show(history({ data: [], total: 0 }));
    expect(screen.getByText(/no notifications yet/i)).toBeInTheDocument();
  });

  it("reports a load failure", () => {
    show(undefined, { error: new Error("boom") });
    expect(screen.getByText(/failed to load your notifications/i)).toBeInTheDocument();
  });

  it("pages only when there is more than one page", () => {
    show(history());
    expect(screen.queryByRole("button", { name: /next/i })).not.toBeInTheDocument();

    cleanup();
    show(history({ total: 45 }));
    const next = screen.getByRole("button", { name: /next/i });
    expect(screen.getByRole("button", { name: /previous/i })).toBeDisabled();

    fireEvent.click(next);
    // The page number is what the hook is asked for, so the request advances.
    expect(mockUseHistory).toHaveBeenLastCalledWith({ page: 2, per_page: 20 });
  });
});

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { NotificationList, NotificationRow, NotificationStats } from "@/api/admin/hooks";

vi.mock("@/api/admin/hooks", () => ({
  useNotifications: vi.fn(),
  useNotificationStats: vi.fn(),
}));

// The recipient filter debounces; the tests assert on the immediate filter
// controls, so the debounce is collapsed to a passthrough.
vi.mock("@/lib/useDebounced", () => ({
  useDebounced: <T,>(value: T) => value,
}));

import { useNotifications, useNotificationStats } from "@/api/admin/hooks";
import { NotificationsTab } from "./NotificationsTab";

const mockUseList = vi.mocked(useNotifications);
const mockUseStats = vi.mocked(useNotificationStats);

function row(overrides: Partial<NotificationRow> = {}): NotificationRow {
  return {
    id: 42,
    recipient: "bob@example.com",
    category: "share",
    subject: 'alice@example.com shared the asset "Q3 Revenue" with you',
    digest: false,
    status: "failed",
    attempts: 5,
    last_error: "dial tcp 10.0.0.1:587: connection refused",
    item_title: "Q3 Revenue",
    actor: "alice@example.com",
    scheduled_for: "2026-07-01T11:59:00Z",
    created_at: "2026-07-01T11:59:00Z",
    ...overrides,
  };
}

function list(overrides: Partial<NotificationList> = {}): NotificationList {
  return { data: [row()], total: 1, page: 1, per_page: 20, ...overrides };
}

function stats(overrides: Partial<NotificationStats> = {}): NotificationStats {
  return { pending: 3, sending: 1, sent: 842, failed: 7, total: 853, retention_days: 30, ...overrides };
}

function show(data: NotificationList | undefined = list(), state: { isLoading?: boolean; error?: unknown } = {}) {
  mockUseList.mockReturnValue({
    data,
    isLoading: state.isLoading ?? false,
    error: state.error ?? null,
  } as ReturnType<typeof useNotifications>);
  mockUseStats.mockReturnValue({ data: stats() } as ReturnType<typeof useNotificationStats>);
  render(<NotificationsTab />);
}

beforeEach(() => {
  mockUseList.mockReset();
  mockUseStats.mockReset();
});

afterEach(cleanup);

describe("NotificationsTab", () => {
  it("shows the per-status counts and the retention window", () => {
    show();
    expect(screen.getByText("842")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
    expect(screen.getByText(/removed after 30 days/i)).toBeInTheDocument();
  });

  it("lists rows with their status and attempt count", () => {
    show();
    expect(screen.getByText("bob@example.com")).toBeInTheDocument();
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();
  });

  it("reveals the failure detail on drill-in", () => {
    show();
    // The error text is the reason this drawer exists.
    expect(screen.queryByText(/connection refused/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("bob@example.com"));

    expect(screen.getByRole("dialog", { name: /notification detail/i })).toBeInTheDocument();
    expect(screen.getByText(/dial tcp 10.0.0.1:587: connection refused/)).toBeInTheDocument();
  });

  it("filters by status when a count tile is clicked, and clears on a second click", () => {
    show();
    fireEvent.click(screen.getByTitle(/delivery attempts exhausted/i));
    expect(mockUseList).toHaveBeenLastCalledWith(
      expect.objectContaining({ status: "failed", page: 1 }),
    );

    fireEvent.click(screen.getByTitle(/delivery attempts exhausted/i));
    expect(mockUseList).toHaveBeenLastCalledWith(
      expect.objectContaining({ status: undefined }),
    );
  });

  it("filters by recipient and category", () => {
    show();
    fireEvent.change(screen.getByLabelText(/filter by recipient/i), {
      target: { value: "bob@example.com" },
    });
    expect(mockUseList).toHaveBeenLastCalledWith(
      expect.objectContaining({ recipient: "bob@example.com" }),
    );

    fireEvent.change(screen.getByLabelText(/filter by category/i), { target: { value: "mention" } });
    expect(mockUseList).toHaveBeenLastCalledWith(
      expect.objectContaining({ category: "mention" }),
    );
  });

  it("says so when nothing matches rather than rendering an empty table", () => {
    show(list({ data: [], total: 0 }));
    expect(screen.getByText(/no notifications match/i)).toBeInTheDocument();
  });

  it("reports a load failure", () => {
    show(undefined, { error: new Error("boom") });
    expect(screen.getByText(/failed to load notification history/i)).toBeInTheDocument();
  });
});

import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { PlatformFeedbackDetail, PlatformFeedbackList } from "@/api/admin/hooks";

const setStatus = vi.fn();
const resend = vi.fn();
let list: PlatformFeedbackList;
let enabled = true;
let lastFilter: unknown;
const at = new Date().toISOString();

const post: PlatformFeedbackDetail = {
  id: "pfb_1", trying_to: "List the assets shared with me", blocked_by: "manage_asset has no filter for shared items",
  tool: "manage_asset", suggestion: "A shared_with_me filter", status: "open", report_count: 7, created_at: at, last_reported_at: at,
  reports: [{ persona: "analyst", calls: ["mcp:call:9f3c21"], reported_at: at }],
  deliveries: [
    { channel: "ops-webhook", status: "sent", attempts: 1, sent_at: at },
    { channel: "platform-team", status: "failed", attempts: 3, last_error: "404 channel_not_found" },
  ],
};

vi.mock("@/api/admin/hooks", async (orig) => ({
  PLATFORM_FEEDBACK_STATUSES: (await orig<typeof import("@/api/admin/hooks")>()).PLATFORM_FEEDBACK_STATUSES,
  usePlatformFeedbackList: (f: unknown) => {
    lastFilter = f;
    return { data: list, isLoading: false, isError: false };
  },
  usePlatformFeedbackSettings: () => ({ data: { enabled, channels: [], personas: [], session_limit: 5, persona_daily_limit: 50 } }),
  usePlatformFeedbackPost: () => ({ data: post, isLoading: false, error: null }),
  useSetPlatformFeedbackStatus: () => ({ mutate: setStatus, isPending: false, error: null }),
  useResendPlatformFeedback: () => ({ mutate: resend, isPending: false, isSuccess: false, error: null }),
}));

import { PlatformFeedbackPage } from "./PlatformFeedbackPage";
import { PlatformFeedbackDetailPage } from "./PlatformFeedbackDetailPage";
import { AdminPlatformFeedbackRoutes } from "./AdminPlatformFeedbackRoutes";

const stats = { open: 6, reports_this_week: 19, refused_this_week: 2, delivery_failures: 1 };

describe("Platform feedback pages (#2820)", () => {
  beforeEach(() => {
    setStatus.mockReset();
    resend.mockReset();
    enabled = true;
    list = { posts: [post], total: 1, stats };
  });

  it("lists each post with its count and status, and opens one", () => {
    const onNavigate = vi.fn();
    render(<PlatformFeedbackPage onNavigate={onNavigate} />);
    expect(screen.getByText("19")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
    expect(screen.getByText("Open", { selector: "[data-slot=badge]" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "List the assets shared with me" }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/platform-feedback/pfb_1");
  });

  it("filters by status", () => {
    render(<PlatformFeedbackPage onNavigate={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "planned" } });
    expect(lastFilter).toMatchObject({ status: "planned" });
  });

  it("says when nothing is posted because the feature is off", () => {
    enabled = false;
    list = { posts: [], total: 0, stats: { open: 0, reports_this_week: 0, refused_this_week: 0, delivery_failures: 0 } };
    render(<PlatformFeedbackPage onNavigate={vi.fn()} />);
    expect(screen.getByText(/Platform feedback is off: turn it on in Settings/)).toBeInTheDocument();
  });

  it("shows a post's reports and deliveries, saves a status and sends a failed delivery again", () => {
    render(<PlatformFeedbackDetailPage id="pfb_1" onBack={vi.fn()} />);
    expect(screen.getByText("mcp:call:9f3c21")).toBeInTheDocument();
    expect(screen.getByText(/404 channel_not_found/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Send again" })).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Send again" }));
    expect(resend).toHaveBeenCalledWith("platform-team");
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "planned" } });
    fireEvent.click(save);
    expect(setStatus).toHaveBeenCalledWith("planned");
  });

  it("routes the list and one post", () => {
    const { rerender } = render(<AdminPlatformFeedbackRoutes route="/admin/platform-feedback" onNavigate={vi.fn()} onBack={vi.fn()} />);
    expect(screen.getByLabelText("Search posts")).toBeInTheDocument();
    rerender(<AdminPlatformFeedbackRoutes route="/admin/platform-feedback/pfb_1" onNavigate={vi.fn()} onBack={vi.fn()} />);
    expect(screen.getByText("Deliveries")).toBeInTheDocument();
    rerender(<AdminPlatformFeedbackRoutes route="/admin/platform-feedback/a/b" onNavigate={vi.fn()} onBack={vi.fn()} />);
    expect(screen.queryByText("Deliveries")).toBeNull();
  });
});

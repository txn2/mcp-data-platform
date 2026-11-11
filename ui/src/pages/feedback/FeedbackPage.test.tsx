import { describe, it, expect, vi, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  usePractitionerWorklist: () => ({ data: { total: 0 } }),
  useSMEWorklist: () => ({ data: { total: 0 } }),
  useUnreadNotificationCount: () => ({ data: { unread: 0 } }),
  useGeneralFeedbackReach: () => ({ data: { reaches_someone: true } }),
}));
vi.mock("@/components/feedback/ActivityFeed", () => ({ ActivityFeed: () => <div>recent activity</div> }));
vi.mock("@/components/feedback/InboxPanel", () => ({ InboxPanel: () => <div>worklist</div> }));
vi.mock("@/components/feedback/FeedbackPanel", () => ({ FeedbackPanel: () => <div>general channel</div> }));
vi.mock("@/components/feedback/NotificationsPanel", () => ({ NotificationsPanel: () => <div>notifications</div> }));
vi.mock("@/components/access/AccessRequestsList", () => ({ AccessRequestsList: () => null }));
vi.mock("@/components/feedback/NewThreadForm", () => ({ NewThreadForm: () => <div>new thread form</div> }));
vi.mock("@/components/feedback/ThreadSlideOver", () => ({
  ThreadSlideOver: ({ threadId, onClose }: { threadId: string; onClose: () => void }) => (
    <div>
      thread {threadId}
      <button type="button" onClick={onClose}>
        Close thread
      </button>
    </div>
  ),
}));

import { FeedbackPage } from "./FeedbackPage";

describe("FeedbackPage (#2603)", () => {
  afterEach(() => window.history.replaceState(null, "", "/portal/feedback"));

  it("opens the thread an email links to, on the General tab, and forgets it when closed", () => {
    window.history.replaceState(null, "", "/portal/feedback#thread-th_42");
    render(<FeedbackPage onNavigate={vi.fn()} />);
    expect(screen.getByText("thread th_42")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /General/ })).toHaveAttribute("aria-selected", "true");
    fireEvent.click(screen.getByRole("button", { name: "Close thread" }));
    expect(screen.queryByText("thread th_42")).toBeNull();
    expect(window.location.hash).toBe("");
  });

  it("opens a thread linked while the page is open", () => {
    window.history.replaceState(null, "", "/portal/feedback");
    render(<FeedbackPage onNavigate={vi.fn()} />);
    expect(screen.queryByText(/^thread /)).toBeNull();
    act(() => {
      window.history.replaceState(null, "", "/portal/feedback#thread-th_7");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    expect(screen.getByText("thread th_7")).toBeInTheDocument();
  });

  it("leaves an address that names no thread alone", () => {
    window.history.replaceState(null, "", "/portal/feedback#access-requests");
    render(<FeedbackPage onNavigate={vi.fn()} />);
    expect(screen.queryByText(/^thread /)).toBeNull();
    expect(screen.getByRole("tab", { name: /Recent/ })).toHaveAttribute("aria-selected", "true");
  });

  it("tells the author of a new post where it goes", () => {
    window.history.replaceState(null, "", "/portal/feedback");
    render(<FeedbackPage onNavigate={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /New feedback/ }));
    expect(screen.getByText("It is sent to the people who handle feedback.")).toBeInTheDocument();
  });
});

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ThreadEvent } from "@/api/portal/types";

const editMutate = vi.fn();
const deleteMutate = vi.fn();

vi.mock("@/api/portal/hooks", () => ({
  useEditThreadEvent: () => ({
    mutate: editMutate,
    isPending: false,
    isError: false,
  }),
  useDeleteThreadEvent: () => ({
    mutate: deleteMutate,
    isPending: false,
    isError: false,
  }),
  useMentionCandidates: vi.fn(() => ({ data: { candidates: [] } })),
  useMentionEligibility: vi.fn(() => ({})),
  useDirectoryUsers: vi.fn(() => ({ data: { users: [], total: 0 } })),
  useDirectoryNames: vi.fn(() => ({})),
}));

vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: { user: { email: string } }) => unknown) =>
    sel({ user: { email: "me@example.com" } }),
}));

import { ThreadTimeline } from "./ThreadTimeline";

function event(overrides: Partial<ThreadEvent> = {}): ThreadEvent {
  return {
    id: "evt_1",
    thread_id: "t1",
    event_type: "comment",
    author_id: "me",
    author_email: "me@example.com",
    body: "the total is wrong",
    created_at: "2026-11-01T10:00:00Z",
    ...overrides,
  };
}

function show(events: ThreadEvent[], canRemoveAny = false) {
  render(
    <ThreadTimeline
      events={events}
      isLoading={false}
      threadId="t1"
      target={{ type: "asset", id: "a1" }}
      canRemoveAny={canRemoveAny}
    />,
  );
}

beforeEach(() => {
  editMutate.mockReset();
  deleteMutate.mockReset();
});
afterEach(cleanup);

// #2514: a message's author edits and deletes it; the target's owner deletes
// any; a decision is never offered for change.
describe("ThreadTimeline message changes", () => {
  it("offers the author edit and delete on their own comment", () => {
    show([event()]);
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
  });

  it("offers nothing on someone else's comment", () => {
    show([event({ author_email: "other@example.com" })]);
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("offers the target's owner delete but not edit on someone else's comment", () => {
    show([event({ author_email: "other@example.com" })], true);
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
  });

  it("never offers to change an approval", () => {
    show([event({ event_type: "approval", body: undefined })], true);
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("shows a deleted message in its place, with no text and no actions", () => {
    show([event({ body: undefined, deleted_at: "2026-11-02T10:00:00Z" })]);
    expect(screen.getByText("Message deleted")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("marks an edited message", () => {
    show([event({ edited_at: "2026-11-02T10:00:00Z" })]);
    expect(screen.getByText(/edited/)).toBeInTheDocument();
  });

  it("saves an edit", () => {
    show([event()]);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Edit message"), {
      target: { value: "the Q4 total is wrong" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(editMutate).toHaveBeenCalledWith(
      { threadId: "t1", eventId: "evt_1", body: "the Q4 total is wrong" },
      expect.anything(),
    );
  });

  it("cancels an edit", () => {
    show([event()]);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("the total is wrong")).toBeInTheDocument();
    expect(editMutate).not.toHaveBeenCalled();
  });

  it("asks before deleting", () => {
    show([event()]);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(screen.getByText("Delete this message?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteMutate).toHaveBeenCalledWith({
      threadId: "t1",
      eventId: "evt_1",
    });
  });

  it("offers nothing without a thread to change", () => {
    render(<ThreadTimeline events={[event()]} isLoading={false} />);
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
  });
});

describe("ThreadTimeline lines and cards (#2532)", () => {
  it("draws a status change as a line, not a card, and offers its Undo", () => {
    const onUndo = vi.fn();
    render(
      <ThreadTimeline
        events={[
          event(),
          event({ id: "evt_2", event_type: "status_change", body: undefined, metadata: { old_status: "open", new_status: "acknowledged" } }),
        ]}
        isLoading={false}
        threadId="t1"
        target={{ type: "asset", id: "a1" }}
        undoableId="evt_2"
        onUndo={onUndo}
      />,
    );
    expect(screen.getByTestId("timeline-card-evt_1")).toHaveTextContent("the total is wrong");
    expect(screen.queryByTestId("timeline-card-evt_2")).toBeNull();
    const line = screen.getByTestId("timeline-line-evt_2");
    expect(line).toHaveTextContent("You marked Acknowledged");
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(onUndo).toHaveBeenCalledWith("evt_2");
  });
});


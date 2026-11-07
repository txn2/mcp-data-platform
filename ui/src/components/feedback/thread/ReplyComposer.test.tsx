import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const append = vi.fn();
const update = vi.fn();

vi.mock("@/api/portal/hooks", () => ({
  useAppendThreadEvent: () => ({ mutate: append, isPending: false, isError: false }),
  useUpdateThread: () => ({ mutate: update, isPending: false, isError: false }),
  useMentionCandidates: vi.fn(() => ({ data: { candidates: [] } })),
  useMentionEligibility: vi.fn(() => ({})),
  useDirectoryUsers: vi.fn(() => ({ data: { users: [], total: 0 } })),
  useDirectoryNames: vi.fn(() => ({})),
}));

vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: { user: { email: string } }) => unknown) => sel({ user: { email: "me@example.com" } }),
}));

import type { Thread } from "@/api/portal/types";
import { applyFormat, ReplyComposer } from "./ReplyComposer";

const thread = {
  id: "t1", kind: "question", target_type: "asset", asset_id: "a1", author_id: "b", author_email: "b@example.com",
  status: "open", requires_resolution: false, validation_state: "none", created_at: "", updated_at: "",
} as Thread;

beforeEach(() => {
  append.mockReset();
  update.mockReset();
  try {
    sessionStorage.clear();
  } catch {
    // no storage in this environment
  }
});

describe("ReplyComposer (#2532)", () => {
  it("opens at least 150px tall, resizable, and expands", () => {
    render(<ReplyComposer threadId="t1" thread={thread} mayResolve={false} />);
    const box = screen.getByLabelText("Reply");
    expect(box.className).toContain("min-h-[150px]");
    expect(box.className).toContain("resize-y");
    fireEvent.click(screen.getByRole("button", { name: "Expand the reply box" }));
    expect(screen.getByLabelText("Reply").className).toContain("min-h-[360px]");
  });

  it("formats from the toolbar", () => {
    render(<ReplyComposer threadId="t1" thread={thread} mayResolve={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Bold" }));
    expect(screen.getByLabelText("Reply")).toHaveValue("**bold**");
    for (const name of ["Italic", "Code", "Link", "List", "Mention"]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
  });

  it("replies and resolves for a moderator, writing the comment and the resolution", () => {
    append.mockImplementation((_input, opts: { onSuccess: () => void }) => opts.onSuccess());
    render(<ReplyComposer threadId="t1" thread={thread} mayResolve />);
    fireEvent.change(screen.getByLabelText("Reply"), { target: { value: " fixed in v3 " } });
    fireEvent.click(screen.getByRole("button", { name: "Reply and resolve" }));
    expect(append).toHaveBeenCalledWith({ threadId: "t1", event_type: "comment", body: "fixed in v3" }, expect.anything());
    expect(update).toHaveBeenCalledWith({ id: "t1", status: "resolved" });
    expect(screen.getByLabelText("Reply")).toHaveValue("");
  });

  it("offers no Reply and resolve to a reader who does not moderate, or on a closed thread", () => {
    const { unmount } = render(<ReplyComposer threadId="t1" thread={thread} mayResolve={false} />);
    expect(screen.queryByRole("button", { name: "Reply and resolve" })).toBeNull();
    unmount();
    render(<ReplyComposer threadId="t1" thread={{ ...thread, status: "resolved" }} mayResolve />);
    expect(screen.queryByRole("button", { name: "Reply and resolve" })).toBeNull();
  });

  it("wraps a selection, and puts a list marker on each line", () => {
    const bold = { label: "Bold", icon: () => null, before: "**", after: "**", placeholder: "bold" } as never;
    expect(applyFormat("make this loud", 10, 14, bold)).toEqual({ text: "make this **loud**", start: 12, end: 16 });
    const list = { label: "List", icon: () => null, before: "- ", after: "", placeholder: "item", line: true } as never;
    expect(applyFormat("a\nb", 0, 3, list).text).toBe("- a\n- b");
  });
});

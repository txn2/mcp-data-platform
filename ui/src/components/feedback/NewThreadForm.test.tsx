import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";

const { mutate } = vi.hoisted(() => ({ mutate: vi.fn() }));
vi.mock("@/api/portal/hooks", () => ({
  useCreateThread: () => ({ mutate, isPending: false, isError: false }),
  useMentionCandidates: () => ({ data: { candidates: [] } }),
  useMentionEligibility: () => ({}),
}));

import { useAuthStore } from "@/stores/auth";
import { NewThreadForm } from "./NewThreadForm";

afterEach(() => {
  cleanup();
  mutate.mockReset();
});

function open() {
  useAuthStore.setState({ user: { user_id: "u1", roles: [], is_admin: false } });
  render(
    <NewThreadForm target={{ type: "asset", id: "a1" }} availableAnchor={null} onCreated={vi.fn()} onCancel={vi.fn()} />,
  );
  return screen.getByRole("textbox", { name: "Message" });
}

// #2513: a thread is its messages. The form asks for no title, sends none,
// and Cmd/Ctrl+Enter in the message box posts it.
describe("NewThreadForm", () => {
  it("has no title field and sends no title", () => {
    const box = open();
    expect(screen.queryByLabelText(/title/i)).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/summary/i)).not.toBeInTheDocument();

    fireEvent.change(box, { target: { value: "The totals are off by one." } });
    fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(mutate.mock.calls[0]![0]).toMatchObject({ body: "The totals are off by one.", kind: "comment" });
    expect(mutate.mock.calls[0]![0]).not.toHaveProperty("title");
  });

  it("sends nothing for an empty message", () => {
    const box = open();
    fireEvent.keyDown(box, { key: "Enter", metaKey: true });
    expect(mutate).not.toHaveBeenCalled();
  });

  it("opens the message box at four rows", () => {
    expect(open()).toHaveAttribute("rows", "4");
  });
});

import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";

const { mutate } = vi.hoisted(() => ({ mutate: vi.fn() }));
vi.mock("@/api/portal/hooks", () => ({
  useCreateThread: () => ({ mutate, isPending: false, isError: false }),
}));
vi.mock("./MentionTextarea", () => ({
  MentionTextarea: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="Message" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

import { useAuthStore } from "@/stores/auth";
import { NewThreadForm } from "./NewThreadForm";
import { ThreadVisibilityBadge } from "./ThreadBadges";

afterEach(() => {
  cleanup();
  mutate.mockReset();
});

function open() {
  render(
    <NewThreadForm target={{ type: "asset", id: "a1" }} availableAnchor={null} onCreated={vi.fn()} onCancel={vi.fn()} />,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), { target: { value: "note" } });
}

describe("thread visibility (#2227)", () => {
  it("opens a thread Internal by default, and Shared when chosen", () => {
    useAuthStore.setState({ user: { user_id: "u1", roles: [], is_admin: false } });
    open();
    expect(screen.getByRole("radio", { name: "Internal" })).toBeChecked();
    fireEvent.click(screen.getByRole("radio", { name: "Shared" }));
    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);
    expect(mutate.mock.calls[0]![0]).toMatchObject({ visibility: "shared", body: "note" });
  });

  it("offers an external person no choice", () => {
    useAuthStore.setState({ user: { user_id: "ext:a1", roles: [], is_admin: false, portal: { external: true } } });
    open();
    expect(screen.queryByRole("radiogroup", { name: "Visibility" })).not.toBeInTheDocument();
    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);
    expect(mutate.mock.calls[0]![0]).not.toHaveProperty("visibility");
  });

  it("labels each thread Internal or Shared", () => {
    render(
      <>
        <ThreadVisibilityBadge visibility="shared" />
        <ThreadVisibilityBadge />
      </>,
    );
    expect(screen.getByText("Shared")).toBeInTheDocument();
    expect(screen.getByText("Internal")).toBeInTheDocument();
  });
});

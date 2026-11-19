import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Thread } from "@/api/portal/types";

const mutate = vi.fn();
vi.mock("@/api/portal/hooks", () => ({
  useUpdateThread: () => ({ mutate, isPending: false }),
}));
vi.mock("@/hooks/usePortalAccess", () => ({
  usePortalAccess: () => ({ external: false }),
}));

import { ThreadHeader } from "./ThreadHeader";

const thread = { id: "t1", kind: "correction", status: "open", visibility: "internal" } as Thread;

// #2705: More holds who sees the thread and deleting it on a phone's sheet.
describe("ThreadHeader More menu", () => {
  const openMore = () =>
    fireEvent.pointerDown(screen.getByRole("button", { name: "More for this thread" }), { button: 0, ctrlKey: false, pointerType: "mouse" });

  it("changes visibility and deletes from the menu", () => {
    const onDelete = vi.fn();
    const sheet = { size: "full" as const, setSize: vi.fn() };
    render(<ThreadHeader thread={thread} mayModerate deleting={false} onBack={vi.fn()} onDelete={onDelete} sheet={sheet} />);
    openMore();
    expect(screen.getByText("Visibility")).toBeTruthy();
    fireEvent.click(screen.getByRole("menuitemradio", { name: "Shared" }));
    expect(mutate).toHaveBeenCalledWith({ id: "t1", visibility: "shared" });
    openMore();
    fireEvent.click(screen.getByRole("menuitem", { name: /Delete thread/ }));
    expect(onDelete).toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Make the feedback sheet smaller" }));
    expect(sheet.setSize).toHaveBeenCalledWith("default");
  });
});

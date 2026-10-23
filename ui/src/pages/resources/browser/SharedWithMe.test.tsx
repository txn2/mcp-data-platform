import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SharedWithMe } from "./SharedWithMe";

const { shared } = vi.hoisted(() => ({ shared: { data: [] as unknown[], isLoading: false } }));
vi.mock("@/api/resources/shares", () => ({ useSharedResources: () => shared }));

// The files shared with the caller, each opening at its own page (#2258).
describe("Shared with me", () => {
  it("says when nothing is shared", () => {
    shared.data = [];
    render(<SharedWithMe onOpen={vi.fn()} />);
    expect(screen.getByText("Nothing is shared with you.")).toBeTruthy();
  });

  it("lists each file with who shared it and opens it", () => {
    shared.data = [
      {
        resource: { id: "res-1", display_name: "Extract" },
        share_id: "s1",
        shared_by: "owner@example.com",
        shared_at: "2026-10-22T10:00:00Z",
        permission: "editor",
        via_persona: "marketing",
      },
    ];
    const onOpen = vi.fn();
    render(<SharedWithMe onOpen={onOpen} />);
    expect(screen.getByText("Can edit")).toBeTruthy();
    expect(screen.getByText("to marketing")).toBeTruthy();
    fireEvent.click(screen.getByText("Extract"));
    expect(onOpen).toHaveBeenCalledWith("res-1");
  });
});

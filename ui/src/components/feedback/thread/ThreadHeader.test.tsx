import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { Thread } from "@/api/portal/types";

let external = false;
vi.mock("@/api/portal/hooks", () => ({
  useUpdateThread: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock("@/hooks/usePortalAccess", () => ({
  usePortalAccess: () => ({ external }),
}));

import { ThreadHeader } from "./ThreadHeader";

const thread = { id: "t1", kind: "correction", status: "open", visibility: "internal" } as Thread;
const show = (mayModerate: boolean) =>
  render(<ThreadHeader thread={thread} mayModerate={mayModerate} deleting={false} onBack={vi.fn()} onDelete={vi.fn()} />);

beforeEach(() => {
  external = false;
});

describe("ThreadHeader", () => {
  it("shows a moderator the visibility once, as the labelled choice", () => {
    show(true);
    expect(screen.getByRole("radiogroup", { name: "Visibility" })).toBeTruthy();
    expect(screen.getAllByText("Internal")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Delete thread" })).toBeTruthy();
  });

  it("shows someone who cannot change it the badge and no choice", () => {
    show(false);
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.getAllByText("Internal")).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "Delete thread" })).toBeNull();
  });

  it("shows an external moderator the badge and no choice", () => {
    external = true;
    show(true);
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.getAllByText("Internal")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Delete thread" })).toBeTruthy();
  });
});

// #2705: on a phone's sheet the thread's row is the panel's header, with
// Visibility and Delete in More, the sheet's size button and close.
describe("ThreadHeader on a phone's sheet", () => {
  const sheet = { size: "default" as const, setSize: vi.fn() };
  const showSheet = (mayModerate: boolean) =>
    render(
      <ThreadHeader thread={thread} mayModerate={mayModerate} deleting={false} onBack={vi.fn()} onDelete={vi.fn()} sheet={sheet} onClose={vi.fn()} />,
    );

  it("is one row with More, the size button and close, and no visibility row", () => {
    showSheet(true);
    expect(screen.getByTestId("thread-sheet-row")).toBeTruthy();
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete thread" })).toBeNull();
    expect(screen.getByRole("button", { name: "More for this thread" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Make the feedback sheet bigger" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Close feedback" })).toBeTruthy();
  });

  it("offers no More to someone who may not moderate, and shows the visibility badge", () => {
    showSheet(false);
    expect(screen.queryByRole("button", { name: "More" })).toBeNull();
    expect(screen.getAllByText("Internal")).toHaveLength(1);
  });
});

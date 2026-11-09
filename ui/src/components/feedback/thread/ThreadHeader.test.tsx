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

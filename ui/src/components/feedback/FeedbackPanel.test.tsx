import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useInfiniteThreads: () => ({
    data: { data: [{ id: "t1", status: "open", requires_resolution: true }] },
    isLoading: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
  }),
}));
vi.mock("./useTextQuoteAnchor", () => ({ useTextQuoteAnchor: () => ({ availableAnchor: null }) }));
vi.mock("./ThreadList", () => ({
  ThreadList: ({ onSelect }: { onSelect: (id: string) => void }) => (
    <button type="button" onClick={() => onSelect("t1")}>
      open thread t1
    </button>
  ),
}));
vi.mock("./ThreadDetail", () => ({
  ThreadDetail: ({ sheet }: { sheet?: unknown }) => <div>thread {sheet ? "on the sheet" : "in the column"}</div>,
}));

import { FeedbackPanel } from "./FeedbackPanel";

const target = { type: "asset", id: "a1", name: "Weekly sales" } as const;

// #2705: on a phone's sheet the panel carries a handle and a size button, and
// an open thread's own row replaces the panel's header; beside the page it is
// as it was.
describe("FeedbackPanel", () => {
  it("on a sheet: a handle, the size button, and the thread's row in place of the header", () => {
    const sheet = { size: "default" as const, setSize: vi.fn() };
    render(<FeedbackPanel target={target} canModerate={false} onClose={vi.fn()} sheet={sheet} />);
    expect(screen.getByTestId("sheet-handle")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Make the feedback sheet bigger" })).toHaveLength(2);
    expect(screen.getByText(/1 open · 1 need resolution/)).toBeInTheDocument();
    fireEvent.click(screen.getByText("open thread t1"));
    expect(screen.getByText("thread on the sheet")).toBeInTheDocument();
    expect(screen.queryByText(/1 open/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Close feedback" })).toBeNull();
  });

  it("beside the page: the header stays over an open thread, with no handle", () => {
    render(<FeedbackPanel target={target} canModerate={false} onClose={vi.fn()} />);
    expect(screen.queryByTestId("sheet-handle")).toBeNull();
    expect(screen.queryByRole("button", { name: "Make the feedback sheet bigger" })).toBeNull();
    fireEvent.click(screen.getByText("open thread t1"));
    expect(screen.getByText("thread in the column")).toBeInTheDocument();
    expect(screen.getByText(/1 open/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Close feedback" })).toBeInTheDocument();
  });
});

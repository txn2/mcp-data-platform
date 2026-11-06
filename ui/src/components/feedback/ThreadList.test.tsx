import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ThreadWithMeta } from "@/api/portal/types";
import { ThreadList } from "./ThreadList";

function thread(overrides: Partial<ThreadWithMeta> = {}): ThreadWithMeta {
  return {
    id: "t1",
    kind: "comment",
    target_type: "asset",
    asset_id: "a1",
    author_id: "sme@example.com",
    author_email: "sme@example.com",
    status: "open",
    requires_resolution: false,
    validation_state: "none",
    created_at: "2026-06-04T13:00:00Z",
    updated_at: "2026-06-04T13:00:00Z",
    event_count: 1,
    last_event_at: "2026-06-04T13:00:00Z",
    ...overrides,
  };
}

// The asset's feedback panel (#2513): a thread opened before titles were
// dropped keeps its title; every other thread is shown by the first line of
// its first message and its author.
describe("ThreadList", () => {
  it("shows a titled thread by its title and an untitled one by its first message", () => {
    render(
      <ThreadList
        isLoading={false}
        onSelect={vi.fn()}
        threads={[
          thread({ id: "t1", title: "We don't use that term", first_message: "We call these practitioners." }),
          thread({ id: "t2", first_message: "The totals are off by one.", author_email: "dana@example.com" }),
        ]}
      />,
    );
    expect(screen.getByText("We don't use that term")).toBeInTheDocument();
    expect(screen.queryByText("We call these practitioners.")).not.toBeInTheDocument();
    expect(screen.getByText("The totals are off by one.")).toBeInTheDocument();
    expect(screen.getByText("dana@example.com")).toBeInTheDocument();
    expect(screen.queryByText(/untitled/i)).not.toBeInTheDocument();
  });

  it("falls back to the kind when a thread has neither title nor message", () => {
    render(<ThreadList isLoading={false} onSelect={vi.fn()} threads={[thread({ kind: "rating" })]} />);
    // The kind badge and the heading line both read Rating.
    expect(screen.getAllByText("Rating")).toHaveLength(2);
  });
});

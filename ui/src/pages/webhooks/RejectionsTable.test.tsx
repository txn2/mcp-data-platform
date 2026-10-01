import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";

import type { WebhookSourceRejection } from "@/api/admin/types";
import { RejectionsTable, outcomesOf, rejectionTimes } from "./RejectionsTable";

const rows: WebhookSourceRejection[] = [
  {
    source: "esp",
    at: "2026-09-30T00:58:39Z",
    first_at: "2026-09-30T00:58:37Z",
    count: 42,
    outcome: "rate_limited",
    reason: "the source's rate limit was reached",
  },
  {
    source: "esp",
    at: "2026-09-30T00:55:00Z",
    first_at: "2026-09-30T00:55:00Z",
    count: 1,
    outcome: "unauthorized",
    reason: "the signature does not match",
  },
];

// A burst is one row with its count, and the Outcome filter finds the
// unauthorized rows a burst used to push out (#2001).
describe("RejectionsTable", () => {
  it("shows each run's count, and the span of a run of more than one", () => {
    render(<RejectionsTable rows={rows} showSource />);

    const burst = screen.getByText("42");
    expect(burst).toHaveAttribute("title", expect.stringContaining(" to "));
    expect(screen.getByText("1")).not.toHaveAttribute("title");
    expect(screen.getAllByText("esp")).toHaveLength(2);
  });

  it("narrows to one outcome, named as the Outcome column names it", async () => {
    render(<RejectionsTable rows={rows} />);

    fireEvent.click(screen.getByRole("combobox", { name: "Outcome" }));
    fireEvent.click(await screen.findByRole("option", { name: "Unauthorized" }));
    const table = screen.getByRole("table");
    expect(within(table).queryByText("the source's rate limit was reached")).not.toBeInTheDocument();
    expect(within(table).getByText("the signature does not match")).toBeInTheDocument();
  });

  it("offers no filter when there is one outcome, and opens a row", () => {
    const onOpen = vi.fn();
    render(<RejectionsTable rows={[rows[1]!]} onOpen={onOpen} />);

    expect(screen.queryByRole("combobox", { name: "Outcome" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("the signature does not match"));
    expect(onOpen).toHaveBeenCalledWith(rows[1]);
  });

  it("reads the outcomes present and a run's times", () => {
    expect(outcomesOf([...rows, rows[0]!])).toEqual(["rate_limited", "unauthorized"]);
    expect(rejectionTimes(rows[1]!).span).toBeUndefined();
    expect(rejectionTimes({ ...rows[0]!, first_at: "" }).span).toBeUndefined();
  });
});

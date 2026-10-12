import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";

import type { WebhookSourceRejection } from "@/api/admin/types";
import { RejectionsTable, eventsText, outcomesOf, rejectionTimes } from "./RejectionsTable";

const rows: WebhookSourceRejection[] = [
  {
    source: "esp",
    at: "2026-09-30T00:58:39Z",
    first_at: "2026-09-30T00:58:37Z",
    count: 42,
    outcome: "rate_limited",
    reason: "the source's rate limit was reached",
    request_hash: "",
    event_ids: [],
  },
  {
    source: "esp",
    at: "2026-09-30T00:55:00Z",
    first_at: "2026-09-30T00:55:00Z",
    count: 1,
    outcome: "unauthorized",
    reason: "the signature does not match",
    request_hash: "",
    event_ids: [],
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

  it("names a refused request's events, copies them with the request hash, and opens nothing from the copy", () => {
    const onOpen = vi.fn();
    const gone: WebhookSourceRejection = {
      ...rows[1]!,
      outcome: "sender_gone",
      reason: "the sender disconnected",
      request_hash: "abc123",
      event_ids: ["e1"],
    };
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    render(<RejectionsTable rows={[gone, rows[0]!]} onOpen={onOpen} />);
    expect(screen.getByText("e1")).toHaveAttribute("title", "Request SHA-256: abc123");
    expect(screen.getAllByText("-")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: /Copy event ids and request hash/ }));
    expect(writeText).toHaveBeenCalledWith("e1\nsha256:abc123");
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("reads a row's events as shown and as copied", () => {
    expect(eventsText(rows[0]!)).toEqual({ shown: "", copy: "" });
    expect(eventsText({ ...rows[0]!, event_ids: ["a", "b", "c", "d", "e"], request_hash: "h" })).toEqual({
      shown: "a, b, c +2 more",
      copy: "a\nb\nc\nd\ne\nsha256:h",
    });
    expect(eventsText({ ...rows[0]!, event_ids: ["a"] }).copy).toBe("a");
  });
});

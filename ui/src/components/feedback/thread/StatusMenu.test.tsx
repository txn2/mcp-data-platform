import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ThreadEvent } from "@/api/portal/types";

vi.mock("@/api/portal/hooks", () => ({ STATUS_UNDO_MS: 600_000 }));

import { StatusMenu, undoableStatusEvent } from "./StatusMenu";

function ev(id: string, type: ThreadEvent["event_type"], email: string, at: string): ThreadEvent {
  return { id, thread_id: "t1", event_type: type, author_id: email, author_email: email, created_at: at };
}

describe("StatusMenu (#2532)", () => {
  it("groups the statuses and says what each means, and picks one", () => {
    const onChange = vi.fn();
    render(<StatusMenu status="open" disabled={false} onChange={onChange} />);
    fireEvent.keyDown(screen.getByRole("button", { name: "Status: Open" }), { key: "Enter" });
    expect(screen.getByText("Still open")).toBeInTheDocument();
    expect(screen.getByText("Closes the thread")).toBeInTheDocument();
    for (const d of [
      "Waiting on the owner. Shows in their worklist.",
      "A reply was given; waiting on the person who asked.",
      "Seen and accepted, not acted on yet. Stays open.",
      "Done. Leaves every worklist.",
      "Closed without a change. Add a reason below.",
    ]) {
      expect(screen.getByText(d)).toBeInTheDocument();
    }
    fireEvent.click(screen.getByRole("menuitem", { name: /Acknowledged/ }));
    expect(onChange).toHaveBeenCalledWith("acknowledged");
  });
});

describe("undoableStatusEvent", () => {
  const now = Date.parse("2026-11-07T10:10:00Z");
  it("is the reader's last status change within the window", () => {
    const events = [ev("e1", "comment", "b@x.io", "2026-11-07T10:00:00Z"), ev("e2", "status_change", "me@x.io", "2026-11-07T10:05:00Z")];
    expect(undoableStatusEvent(events, "Me@x.io", now)?.id).toBe("e2");
  });
  it("survives the reader's own later reply, not someone else's", () => {
    const mine = [ev("e2", "resolution", "me@x.io", "2026-11-07T10:05:00Z"), ev("e3", "comment", "me@x.io", "2026-11-07T10:06:00Z")];
    expect(undoableStatusEvent(mine, "me@x.io", now)?.id).toBe("e2");
    const theirs = [ev("e2", "status_change", "me@x.io", "2026-11-07T10:05:00Z"), ev("e3", "comment", "b@x.io", "2026-11-07T10:06:00Z")];
    expect(undoableStatusEvent(theirs, "me@x.io", now)).toBeNull();
  });
  it("is nothing past 10 minutes, for someone else's change, or without a reader", () => {
    const old = [ev("e2", "status_change", "me@x.io", "2026-11-07T09:59:00Z")];
    expect(undoableStatusEvent(old, "me@x.io", now)).toBeNull();
    const other = [ev("e2", "status_change", "b@x.io", "2026-11-07T10:05:00Z")];
    expect(undoableStatusEvent(other, "me@x.io", now)).toBeNull();
    expect(undoableStatusEvent(other, "", now)).toBeNull();
    expect(undoableStatusEvent(undefined, "me@x.io", now)).toBeNull();
  });
});

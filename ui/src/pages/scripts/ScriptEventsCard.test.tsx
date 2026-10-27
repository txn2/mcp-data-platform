import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptEventSummary: vi.fn(),
  useSetScriptEventSource: vi.fn(),
}));

import { useScriptEventSummary, useSetScriptEventSource } from "@/api/portal/hooks/scripts";
import { ScriptEventsCard } from "./ScriptEventsCard";

const mutate = vi.fn();

function contract(eventSource?: string): ScriptContract {
  return { id: "s1", name: "on-sale", status: "active", enabled: true, params: [], version: 1, event_source: eventSource } as ScriptContract;
}

function show(c: ScriptContract, opts: { error?: string; message?: string; skipped?: number } = {}) {
  vi.mocked(useSetScriptEventSource).mockReturnValue({
    mutate,
    isPending: false,
    error: opts.error ? new Error(opts.error) : null,
    data: opts.message ? { source: "x", message: opts.message } : undefined,
  } as never);
  vi.mocked(useScriptEventSummary).mockReturnValue({
    data: {
      pending: 2, skipped_since: opts.skipped ?? 0, skipped_window_days: 7, max_queued: 100,
      last_event_run: "2026-10-26T09:30:00Z", last_skipped_at: "2026-10-25T18:02:00Z",
    },
  } as never);
  render(<ScriptEventsCard scriptId="s1" contract={c} />);
}

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

// #2332: the owner names the webhook source a script runs for, and reads how
// its event runs stand.
describe("ScriptEventsCard", () => {
  it("saves a source for a script that runs for none", () => {
    show(contract());
    expect(screen.getByText("Runs for no webhook source")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("heading", { name: "Events" }));
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Webhook source"), { target: { value: " form-contact " } });
    fireEvent.click(save);
    expect(mutate).toHaveBeenCalledWith("form-contact");
    expect(screen.queryByText("Waiting")).not.toBeInTheDocument();
  });

  it("shows the waiting and skipped event runs of a script that runs for a source", () => {
    show(contract("form-contact"), { skipped: 3, message: "Saved. Each event source form-contact stores runs this script." });
    expect(screen.getByText("2 of at most 100 event runs")).toBeInTheDocument();
    expect(screen.getByText(/3 in the last 7 days/)).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Each event source form-contact stores runs this script.");
    fireEvent.click(screen.getByRole("button", { name: "Stop running for events" }));
    expect(mutate).toHaveBeenCalledWith("");
  });

  it("says when nothing was skipped, and shows a refusal", () => {
    show(contract("form-contact"), { error: "only an administrator may run a script for source \"shop\"" });
    expect(screen.getByText("None in the last 7 days")).toBeInTheDocument();
    expect(screen.getByText(/only an administrator/)).toBeInTheDocument();
  });
});

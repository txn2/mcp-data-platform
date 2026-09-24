import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ScriptRun, ScriptRunDetail } from "@/api/portal/hooks/scripts";
import { RunAttempts, RunHolder, ScriptLiveRuns } from "./ScriptRunAttempts";

// A run's queue history (#1860): who holds a running run and whether that
// worker is still reporting, how each earlier attempt ended, and the runs of
// a script that have not ended.

vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptLiveRuns: vi.fn(),
  useCancelScriptRun: vi.fn(),
  isRunInFlight: (run?: { status: string }) => run?.status === "pending" || run?.status === "running",
}));

import { useCancelScriptRun, useScriptLiveRuns } from "@/api/portal/hooks/scripts";

const mockLive = vi.mocked(useScriptLiveRuns);
const mockCancel = vi.mocked(useCancelScriptRun);
const cancel = vi.fn();

const orphan: ScriptRunDetail = {
  id: "run-201",
  script_id: "script-004",
  status: "running",
  trigger: "schedule",
  version: 1,
  fire_time: "2026-08-14T08:36:00Z",
  started_at: "2026-08-14T08:36:00Z",
  duration_ms: 0,
  output_count: 0,
  liveness: "unresponsive",
  scheduled_for: "2026-08-14T08:36:00Z",
  attempt: 2,
  reclaims: 1,
  locked_by: "worker-7f3a91c2d4e5b608",
  locked_until: "2026-08-14T09:12:00Z",
  heartbeat_at: "2026-08-14T08:54:00Z",
  metrics: { steps: 0, duration_ms: 0, queries: 0, exports: 0 },
  attempts: [
    { attempt: 1, worker: "worker-0c1d2e3f4a5b6c7d", ended_at: "2026-08-14T08:51:00Z", outcome: "lease_expired" },
  ],
  created_at: "2026-08-14T08:36:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  mockCancel.mockReturnValue({ mutate: cancel, isPending: false, isSuccess: false, isError: false } as never);
});

afterEach(cleanup);

describe("RunHolder", () => {
  it("names the holder, says it stopped reporting, and counts the take-overs", () => {
    render(<RunHolder run={orphan} />);
    expect(screen.getByText("worker-7f3a91c2d4e5b608")).toBeTruthy();
    expect(screen.getByText(/stopped reporting/)).toBeTruthy();
    expect(screen.getByText(/taken over 1 time$/)).toBeTruthy();
  });

  it("shows nothing for a run that has ended", () => {
    const { container } = render(<RunHolder run={{ ...orphan, status: "succeeded" }} />);
    expect(container.textContent).toBe("");
  });

  it("names no stoppage for a worker that is executing", () => {
    render(<RunHolder run={{ ...orphan, liveness: "executing", reclaims: 0 }} />);
    expect(screen.queryByText(/stopped reporting/)).toBeNull();
    expect(screen.queryByText(/taken over/)).toBeNull();
  });
});

describe("RunAttempts", () => {
  it("lists how each attempt ended when one did not finish", () => {
    render(<RunAttempts run={orphan} />);
    expect(screen.getByText("Attempts")).toBeTruthy();
    expect(screen.getByText(/worker stopped reporting \(lease expired\)/)).toBeTruthy();
    expect(screen.getByText(/worker-0c1d2e3f4a5b6c7d/)).toBeTruthy();
  });

  it("says nothing for a run that finished on its first attempt", () => {
    const { container } = render(
      <RunAttempts run={{ ...orphan, attempts: [{ attempt: 1, worker: "w", outcome: "finished" }] }} />,
    );
    expect(container.textContent).toBe("");
  });

  it("shows an attempt's error under it", () => {
    render(
      <RunAttempts
        run={{ ...orphan, attempts: [{ attempt: 1, worker: "w", outcome: "retried", error: "trino unreachable" }] }}
      />,
    );
    expect(screen.getByText("trino unreachable")).toBeTruthy();
  });
});

describe("ScriptLiveRuns", () => {
  const live: ScriptRun = { ...orphan };

  it("lists a run whose worker stopped reporting, and stops it", () => {
    mockLive.mockReturnValue({ data: { data: [live], total: 1 } } as never);
    render(<ScriptLiveRuns scriptId="script-004" />);
    expect(screen.getByText("Running now")).toBeTruthy();
    expect(screen.getByText("worker not responding")).toBeTruthy();
    expect(screen.getByText(/stopped reporting/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Stop run" }));
    expect(cancel).toHaveBeenCalledWith("run-201");
  });

  it("offers to cancel a queued run and says a stopping one is stopping", () => {
    mockLive.mockReturnValue({
      data: { data: [{ ...live, id: "q", status: "pending", liveness: undefined }], total: 1 },
    } as never);
    render(<ScriptLiveRuns scriptId="script-004" />);
    expect(screen.getByRole("button", { name: "Cancel run" })).toBeTruthy();
    cleanup();

    mockLive.mockReturnValue({ data: { data: [{ ...live, cancel_requested: true }], total: 1 } } as never);
    render(<ScriptLiveRuns scriptId="script-004" />);
    expect(screen.getByText("Stopping")).toBeTruthy();
  });

  it("says a cancel that failed failed", () => {
    mockLive.mockReturnValue({ data: { data: [live], total: 1 } } as never);
    mockCancel.mockReturnValue({ mutate: cancel, isPending: false, isSuccess: false, isError: true } as never);
    render(<ScriptLiveRuns scriptId="script-004" />);
    expect(screen.getByText("The run could not be stopped.")).toBeTruthy();
  });

  it("shows nothing when every run has ended", () => {
    mockLive.mockReturnValue({ data: { data: [{ ...live, status: "succeeded" }], total: 1 } } as never);
    const { container } = render(<ScriptLiveRuns scriptId="script-004" />);
    expect(container.textContent).toBe("");
  });
});

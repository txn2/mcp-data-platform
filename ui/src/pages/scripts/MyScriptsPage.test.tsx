import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { MyScriptsPage } from "./MyScriptsPage";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useMyScripts: vi.fn(),
}));

import { useMyScripts } from "@/api/portal/hooks/scripts";

const mockScripts = vi.mocked(useMyScripts);
const onNavigate = vi.fn();

function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

function row(overrides: Partial<PortalScriptRow> = {}): PortalScriptRow {
  return {
    script: {
      id: "script-001",
      name: "daily-sales-report",
      display_name: "Daily Sales Report",
      description: "Yesterday's sales by region.",
      scope: "global",
      owner_email: "sarah.chen@example.com",
      status: "active",
      enabled: true,
      version: 2,
      approved_version_id: "sver-001-v2",
      updated_at: new Date().toISOString(),
    },
    schedule: {
      id: "sched-001",
      script_id: "script-001",
      cron_spec: "0 7 * * 1-5",
      timezone: "America/Los_Angeles",
      enabled: true,
      next_run_at: new Date("2026-08-15T14:00:00Z").toISOString(),
    },
    last_run: {
      id: "run-001",
      status: "succeeded",
      trigger: "schedule",
      version: 2,
      fire_time: new Date("2026-08-14T07:00:00Z").toISOString(),
      finished_at: new Date("2026-08-14T07:00:08Z").toISOString(),
      duration_ms: 8_420,
      output_count: 1,
    },
    owned: true,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockScripts.mockReturnValue(query({ data: [row()], total: 1 }));
});

afterEach(cleanup);

describe("MyScriptsPage", () => {
  it("reports what a script is executing, its cadence, and how it last ran", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);

    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.getByText("Approved v2")).toBeInTheDocument();
    expect(screen.getByText("succeeded")).toBeInTheDocument();
  });

  // #1358: the column an owner scans to answer "what is running and when" said
  // "0 7 * * 1-5", while the editor two clicks away said the same cadence in
  // words. The sentence is the one the editor states.
  it("states a cadence in the words the schedule editor states it in", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(
      screen.getByText("Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeInTheDocument();
    expect(screen.queryByText("0 7 * * 1-5")).not.toBeInTheDocument();
  });

  // An expression the builder cannot express is shown as itself: a phrase that
  // only says "custom" around it would cost a line and add nothing.
  it("falls back to the expression for a cadence it cannot state", () => {
    mockScripts.mockReturnValue(
      query({
        data: [row({ schedule: { ...row().schedule!, cron_spec: "*/7 3 * 2 1-5" } })],
        total: 1,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("*/7 3 * 2 1-5")).toBeInTheDocument();
  });

  it("says an enabled schedule with nothing due is not overdue", () => {
    mockScripts.mockReturnValue(
      query({
        data: [row({ schedule: { ...row().schedule!, next_run_at: undefined } })],
        total: 1,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("No fire due")).toBeInTheDocument();
  });

  it("says a script runs nothing when no version is approved", () => {
    mockScripts.mockReturnValue(
      query({
        data: [row({ script: { ...row().script, approved_version_id: undefined } })],
        total: 1,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Nothing approved")).toBeInTheDocument();
  });

  it("reports a paused schedule rather than a next fire that will not happen", () => {
    mockScripts.mockReturnValue(
      query({ data: [row({ schedule: { ...row().schedule!, enabled: false } })], total: 1 }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Paused")).toBeInTheDocument();
  });

  it("says a script with no schedule runs on demand", () => {
    mockScripts.mockReturnValue(query({ data: [row({ schedule: undefined })], total: 1 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("On demand")).toBeInTheDocument();
  });

  it("distinguishes a script that has never run from one whose runs are not the caller's", () => {
    mockScripts.mockReturnValue(
      query({
        data: [
          row({ last_run: undefined }),
          row({
            script: { ...row().script, id: "script-002", display_name: "Someone Else's" },
            owned: false,
            last_run: undefined,
          }),
        ],
        total: 2,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Never run")).toBeInTheDocument();
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
  });

  // #1360: the captions used to be asides ("you can see", "worth opening").
  // Each now states what its number counts, and the failure tile states the
  // population it counts over, which is NOT the one the other three count over:
  // a last run is absent from a row this caller does not own.
  it("states what each summary number counts and over what population", () => {
    mockScripts.mockReturnValue(
      query({
        data: [
          row({ last_run: { ...row().last_run!, status: "failed" } }),
          row({
            script: { ...row().script, id: "script-002", display_name: "Someone Else's" },
            owned: false,
            last_run: undefined,
          }),
        ],
        total: 2,
      }),
    );
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("scripts visible to you")).toBeInTheDocument();
    expect(screen.getByText("have a version the platform may execute")).toBeInTheDocument();
    expect(screen.getByText("run on a schedule, unattended")).toBeInTheDocument();
    expect(screen.getByText("of the 1 you own")).toBeInTheDocument();
  });

  // The row is the target, as it is on every other listing in the portal.
  it("opens a script's detail page from the row", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001");
  });

  it("says plainly when there are no scripts at all", () => {
    mockScripts.mockReturnValue(query({ data: [], total: 0 }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText(/You have no scripts yet/)).toBeInTheDocument();
  });

  it("reports a listing that could not be loaded instead of showing it as empty", () => {
    mockScripts.mockReturnValue(query(undefined, { error: new Error("boom") }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText(/scripts could not be loaded/)).toBeInTheDocument();
  });

  it("shows a loading state rather than an empty one while the listing is in flight", () => {
    mockScripts.mockReturnValue(query(undefined, { isLoading: true }));
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Loading scripts...")).toBeInTheDocument();
  });
});

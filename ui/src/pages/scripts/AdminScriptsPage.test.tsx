import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { AdminScriptsPage } from "./AdminScriptsPage";

// The administrator's section is the owners' two listings, told who is reading
// (#1407). Each listing has its own tests; what this file covers is that this
// page reads them as an administrator and links into its own section.
vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptListing: vi.fn(),
  useScriptRunListing: vi.fn(),
}));

// The Runs tab also reads the Prometheus proxy. It only has to answer here;
// the aggregation math has its own tests in runMetrics.test.ts.
vi.mock("@/api/observability/hooks", () => ({
  isBackendUnconfigured: vi.fn(() => true),
  useObservabilityQuery: vi.fn(() => ({ data: undefined, error: null, isLoading: false })),
  useObservabilityQueryRange: vi.fn(() => ({ data: undefined, error: null, isLoading: false })),
}));

import { useScriptListing, useScriptRunListing } from "@/api/portal/hooks/scripts";

const mockScripts = vi.mocked(useScriptListing);
const mockRuns = vi.mocked(useScriptRunListing);
const onNavigate = vi.fn();

function query<T>(data: T) {
  return { data, isLoading: false, error: null } as never;
}

const script = {
  script: {
    id: "script-001",
    name: "daily-sales-report",
    display_name: "Daily Sales Report",
    owner_email: "sarah.chen@example.com",
    status: "active",
    enabled: true,
    version: 2,
    updated_at: new Date().toISOString(),
  },
  owned: true,
};

const run = {
  id: "run-042",
  script_id: "script-001",
  script_name: "Daily Sales Report",
  status: "succeeded",
  trigger: "schedule",
  version: 2,
  fire_time: new Date("2026-08-14T07:00:00Z").toISOString(),
  finished_at: new Date("2026-08-14T07:00:08Z").toISOString(),
  duration_ms: 8_420,
  output_count: 1,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockScripts.mockReturnValue(query({ data: [script], total: 1 }));
  mockRuns.mockReturnValue(query({ data: [run], total: 1, limit: 50 }));
});

afterEach(cleanup);

describe("AdminScriptsPage", () => {
  it("lists every script with whose it is", () => {
    render(<AdminScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByRole("columnheader", { name: "Owner" })).toBeInTheDocument();
    expect(screen.getByRole("row", { name: /Daily Sales Report/ })).toHaveTextContent(
      "sarah.chen@example.com",
    );
  });

  it("opens the script itself, on the same page its owner opens", () => {
    render(<AdminScriptsPage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));

    // The listing lists; the detail page is where an administrator runs, edits
    // and schedules. A second surface here would have been a second answer to
    // what an administrator can do with a script.
    expect(onNavigate).toHaveBeenCalledWith("/admin/scripts/script-001");
  });

  it("switches to what the platform has been running", () => {
    render(<AdminScriptsPage onNavigate={onNavigate} />);
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Runs" }));

    // The metrics backend is unconfigured in this test, so the tab says so and
    // still stands the run history up from the platform's own records.
    expect(screen.getByText(/Every run the platform executed/)).toBeInTheDocument();
    expect(screen.getByText(/no metrics backend configured/)).toBeInTheDocument();
    expect(screen.getByText("Recent runs")).toBeInTheDocument();
  });

  // A run in the operator's listing opens where the reader is, not in the
  // owner's section (#1407).
  it("opens a run under the administrator's own section", () => {
    render(<AdminScriptsPage onNavigate={onNavigate} />);
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Runs" }));
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));

    expect(onNavigate).toHaveBeenCalledWith("/admin/scripts/script-001/runs/run-042");
  });
});

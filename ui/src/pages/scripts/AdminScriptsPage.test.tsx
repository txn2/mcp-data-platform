import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { Script } from "@/api/admin/types";
import { AdminScriptsPage } from "./AdminScriptsPage";

// The page is a listing over one hook; the components under it are real, so
// every assertion here exercises what an administrator actually sees.
vi.mock("@/api/admin/hooks", () => ({
  useAdminScripts: vi.fn(),
}));

// The Runs tab reads the cross-script run listing and the Prometheus proxy.
// Both only have to answer here; the tab's aggregation math has its own tests
// in runMetrics.test.ts.
vi.mock("@/api/admin/hooks/scripts", () => ({
  useAdminScriptRuns: vi.fn(),
}));
vi.mock("@/api/observability/hooks", () => ({
  isBackendUnconfigured: vi.fn(() => true),
  useObservabilityQuery: vi.fn(() => ({ data: undefined, error: null, isLoading: false })),
  useObservabilityQueryRange: vi.fn(() => ({ data: undefined, error: null, isLoading: false })),
}));

import { useAdminScripts } from "@/api/admin/hooks";
import { useAdminScriptRuns } from "@/api/admin/hooks/scripts";

const mockScripts = vi.mocked(useAdminScripts);
const mockRuns = vi.mocked(useAdminScriptRuns);
const navigate = vi.fn();

const script: Script = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  description: "Yesterday's sales by region.",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  version: 2,
  updated_at: new Date().toISOString(),
};

// query fakes the react-query result shape the page reads.
function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockScripts.mockReturnValue(query({ data: [script], total: 1 }));
  mockRuns.mockReturnValue(query({ data: [], total: 0, limit: 50 }));
});

afterEach(cleanup);

describe("AdminScriptsPage: the listing", () => {
  it("lists every script with what it is executing", () => {
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    // A saved script runs its latest version; there is no approval between the
    // save and the schedule.
    expect(screen.getByText("Runs v2")).toBeInTheDocument();
  });

  it("reports a disabled script as running nothing", () => {
    mockScripts.mockReturnValue(query({ data: [{ ...script, enabled: false }], total: 1 }));
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText("disabled")).toBeInTheDocument();
    expect(screen.queryByText("Runs v2")).not.toBeInTheDocument();
  });

  it("reports a retired script by its status rather than a version", () => {
    mockScripts.mockReturnValue(query({ data: [{ ...script, status: "deprecated" }], total: 1 }));
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText("deprecated")).toBeInTheDocument();
    expect(screen.queryByText("Runs v2")).not.toBeInTheDocument();
  });

  it("says when no scripts exist at all", () => {
    mockScripts.mockReturnValue(query({ data: [], total: 0 }));
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText(/No scripts have been authored yet/)).toBeInTheDocument();
  });

  it("reports a listing that could not be loaded instead of showing it as empty", () => {
    mockScripts.mockReturnValue(query(undefined, { error: new Error("boom") }));
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText(/script listing could not be loaded/)).toBeInTheDocument();
  });
});

describe("AdminScriptsPage: opening a script", () => {
  it("opens the script itself, on the same page its owner opens", () => {
    render(<AdminScriptsPage onNavigate={navigate} />);

    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));

    // The listing lists; the detail page is where an administrator runs, edits
    // and schedules (#1367). A second surface here would have been a second
    // answer to what an administrator can do with a script.
    expect(navigate).toHaveBeenCalledWith("/admin/scripts/script-001");
  });

  it("names who each script belongs to", () => {
    mockScripts.mockReturnValue(
      query({
        data: [
          script,
          {
            ...script,
            id: "script-002",
            name: "theirs",
            display_name: "Marcus Report",
            owner_email: "marcus.webb@example.com",
          },
          { ...script, id: "script-003", name: "orphan", display_name: "Orphan Report", owner_email: "" },
        ],
        total: 3,
      }),
    );
    render(<AdminScriptsPage onNavigate={navigate} />);

    expect(screen.getByRole("row", { name: /Daily Sales Report/ })).toHaveTextContent(
      "sarah.chen@example.com",
    );
    expect(screen.getByRole("row", { name: /Marcus Report/ })).toHaveTextContent(
      "marcus.webb@example.com",
    );
    // A script authored by a principal carrying no address belongs to nobody
    // until an administrator transfers it (#1404).
    expect(screen.getByRole("row", { name: /Orphan Report/ })).toHaveTextContent("nobody");
  });
});

describe("AdminScriptsPage: the runs tab", () => {
  it("switches to what the platform has been running", () => {
    render(<AdminScriptsPage onNavigate={navigate} />);

    fireEvent.mouseDown(screen.getByRole("tab", { name: "Runs" }));

    // The metrics backend is unconfigured in this test, so the tab says so and
    // still stands the run history up from the platform's own records.
    expect(screen.getByText(/Every run the platform executed/)).toBeInTheDocument();
    expect(screen.getByText(/no metrics backend configured/)).toBeInTheDocument();
  });
});

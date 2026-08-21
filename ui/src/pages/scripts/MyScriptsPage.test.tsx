import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { MyScriptsPage } from "./MyScriptsPage";

// The page is two tabs over two listings (#1405). Each listing has its own
// tests; what this file covers is that the page is wired to both of them and
// that each opens the owner's own section.
vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptListing: vi.fn(),
  useScriptRunListing: vi.fn(),
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
  status: "failed",
  trigger: "schedule",
  version: 2,
  fire_time: new Date("2026-08-14T07:00:00Z").toISOString(),
  finished_at: new Date("2026-08-14T07:00:02Z").toISOString(),
  duration_ms: 2_100,
  error: "trino: table not found: sales.daily",
  output_count: 0,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockScripts.mockReturnValue(query({ data: [script], total: 1 }));
  mockRuns.mockReturnValue(query({ data: [run], total: 1, limit: 50 }));
});

afterEach(cleanup);

describe("MyScriptsPage", () => {
  it("opens on the scripts a person owns", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "Owner" })).not.toBeInTheDocument();
  });

  // The other question the per-script history cannot answer: how are my
  // scripts going, all of them, with the reason each failure failed.
  it("shows every run of every script on the Runs tab", () => {
    render(<MyScriptsPage onNavigate={onNavigate} />);
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Runs" }));

    expect(screen.getByText("trino: table not found: sales.daily")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-001/runs/run-042");
  });
});

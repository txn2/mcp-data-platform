import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { ScriptMetricRows } from "./ScriptMetricRows";

// A metric that names a script leads to it (#1407): the panels used to name a
// script and leave the reader to go and find it.

const scripts: PortalScriptRow[] = [
  {
    script: {
      id: "script-001",
      name: "daily-sales-report",
      display_name: "Daily Sales Report",
      description: "Yesterday's sales by region.",
      owner_email: "sarah.chen@example.com",
      status: "active",
      enabled: true,
      version: 2,
      updated_at: new Date().toISOString(),
    },
    owned: true,
  },
];

const rows = [
  { dimension: "daily-sales-report", count: 42, success_rate: 0, avg_duration_ms: 0 },
  { dimension: "deleted-script", count: 7, success_rate: 0, avg_duration_ms: 0 },
];

function renderRows(data = rows, isLoading = false) {
  const onOpenScript = vi.fn();
  const onShowRuns = vi.fn();
  render(
    <ScriptMetricRows
      data={data}
      isLoading={isLoading}
      scripts={scripts}
      unit="runs"
      onOpenScript={onOpenScript}
      onShowRuns={onShowRuns}
    />,
  );
  return { onOpenScript, onShowRuns };
}

afterEach(cleanup);

describe("ScriptMetricRows", () => {
  it("opens the script the metric names", () => {
    const { onOpenScript } = renderRows();
    fireEvent.click(screen.getByRole("button", { name: "Daily Sales Report" }));
    expect(onOpenScript).toHaveBeenCalledWith("script-001");
  });

  it("opens the runs behind the number", () => {
    const { onShowRuns } = renderRows();
    fireEvent.click(screen.getAllByRole("button", { name: "Runs" })[0]!);
    expect(onShowRuns).toHaveBeenCalledWith("script-001", "Daily Sales Report");
  });

  // The series outlives the record: a script that has been deleted keeps its
  // history in Prometheus, and dropping the row would understate the load.
  it("keeps a row it cannot resolve, without links", () => {
    renderRows();
    expect(screen.getByText("deleted-script")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "deleted-script" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Runs" })).toHaveLength(1);
  });

  it("states the count with what it counts", () => {
    renderRows();
    expect(screen.getByText("42")).toBeInTheDocument();
    expect(screen.getAllByText("runs").length).toBeGreaterThan(0);
  });

  it("says a window holding nothing is empty rather than drawing nothing", () => {
    renderRows([]);
    expect(screen.getByText("Nothing in this window.")).toBeInTheDocument();
  });

  it("says it is still loading rather than showing an empty window", () => {
    renderRows([], true);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });
});

import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ToolBreakdownTable } from "./ToolBreakdownTable";
import { OtherEventsPanel } from "./OtherEventsPanel";
import { KPIRow } from "./KPIRow";

describe("holds on the dashboard (#2819)", () => {
  it("shows each tool's success rate and holds", () => {
    render(
      <ToolBreakdownTable
        isLoading={false}
        data={[
          { dimension: "search", count: 1106, success_rate: 0.992, avg_duration_ms: 50, hold_count: 0 },
          { dimension: "manage_site", count: 152, success_rate: 1, avg_duration_ms: 1900, hold_count: 29 },
        ]}
      />,
    );
    expect(screen.getByText("99.2%")).toBeInTheDocument();
    expect(screen.getByText("100%")).toBeInTheDocument();
    expect(screen.getByText("29")).toBeInTheDocument();
    expect(screen.getByText(/Success leaves holds out/)).toBeInTheDocument();
  });

  it("counts the events beside tool calls under their own names", () => {
    const onOpenEvents = vi.fn();
    render(
      <OtherEventsPanel
        isLoading={false}
        onOpenEvents={onOpenEvents}
        data={[
          { dimension: "mcp_tool_call", count: 4812, success_rate: 0.96, avg_duration_ms: 800 },
          { dimension: "script_run", count: 81394, success_rate: 0.97, avg_duration_ms: 2000 },
          { dimension: "new_kind", count: 3, success_rate: 1, avg_duration_ms: 1 },
        ]}
      />,
    );
    expect(screen.getByText("Scheduled and requested script runs")).toBeInTheDocument();
    expect(screen.getByText("81,394")).toBeInTheDocument();
    expect(screen.getByText("new_kind")).toBeInTheDocument();
    expect(screen.queryByText("4,812")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Open the Events tab" }));
    expect(onOpenEvents).toHaveBeenCalled();
  });

  it("shows the hold count and leaves holds out of the success trend", () => {
    render(
      <KPIRow
        overview={{ total_calls: 10, success_rate: 1, avg_duration_ms: 5, unique_users: 1, unique_tools: 1, enrichment_rate: 0, error_count: 0, hold_count: 4 }}
        buckets={[]}
      />,
    );
    expect(screen.getByText("Holds")).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
    expect(screen.getByText("holds are not counted here")).toBeInTheDocument();
  });
});

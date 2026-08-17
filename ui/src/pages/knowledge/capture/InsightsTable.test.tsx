import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { Insight } from "@/api/admin/types";

import { InsightsTable } from "./InsightsTable";

function insight(over: Partial<Insight> = {}): Insight {
  return {
    id: "ins-001",
    created_at: "2026-08-01T10:00:00Z",
    session_id: "sess-1",
    captured_by: "rachel.thompson@example.com",
    persona: "inventory-analyst",
    category: "data_quality",
    insight_text: "inventory_levels holds 1140 rows for warehouse WH-07.",
    confidence: "high",
    entity_urns: [],
    related_columns: [],
    suggested_actions: [],
    status: "pending",
    ...over,
  };
}

describe("InsightsTable", () => {
  // #1257: an insight whose application was rolled back is pending again, and
  // the queue says so rather than showing it as an ordinary fresh capture.
  it("marks a pending insight that came back from a rollback", () => {
    render(
      <InsightsTable
        insights={[
          insight({ id: "fresh" }),
          insight({
            id: "returned",
            applied_by: "admin@example.com",
            applied_at: "2026-08-02T10:00:00Z",
            changeset_ref: "cs-004",
          }),
        ]}
        loading={false}
        userLabels={{}}
        onSelect={vi.fn()}
      />,
    );

    expect(screen.getAllByText("Pending")).toHaveLength(2);
    const returned = screen.getAllByText("Returned");
    expect(returned).toHaveLength(1);
    expect(returned[0]?.closest("span[title]")).toHaveAttribute(
      "title",
      "Returned when changeset cs-004 was rolled back",
    );
  });

  it("does not mark an applied insight, which is not in the queue", () => {
    render(
      <InsightsTable
        insights={[
          insight({ status: "applied", applied_by: "admin@example.com", changeset_ref: "cs-004" }),
        ]}
        loading={false}
        userLabels={{}}
        onSelect={vi.fn()}
      />,
    );

    expect(screen.getByText("Applied")).toBeInTheDocument();
    expect(screen.queryByText("Returned")).not.toBeInTheDocument();
  });
});

import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { Insight } from "@/api/admin/types";

vi.mock("@/api/admin/hooks", () => ({
  useUpdateInsightStatus: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
}));

import { InsightDrawer } from "./InsightDrawer";

const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.levels,PROD)";

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
    entity_urns: [urn],
    related_columns: [],
    suggested_actions: [],
    status: "pending",
    ...over,
  };
}

describe("InsightDrawer", () => {
  it("puts the observed warehouse state beside the claim it is deciding", () => {
    render(
      <InsightDrawer
        insight={insight({
          observed_entities: [
            {
              urn,
              query_table: "iceberg.inventory.levels",
              connection: "primary",
              estimated_rows: 1200,
              conflict: {
                claimed_rows: 1140,
                observed_rows: 1200,
                message: "claim states 1140; the table currently estimates 1200",
              },
            },
          ],
        })}
        onClose={vi.fn()}
        userLabels={{}}
      />,
    );

    expect(screen.getByText("Observed Now")).toBeInTheDocument();
    expect(screen.getByText("iceberg.inventory.levels")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Claim states 1,140; the table currently estimates 1,200.",
    );
    // The advisory never takes the decision away from the reviewer.
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeEnabled();
  });

  // #1257: a rollback returns the insight to the queue rather than discarding
  // it, so the reviewer deciding it next sees the application that was reverted.
  it("explains a pending insight that came back from a rollback", () => {
    render(
      <InsightDrawer
        insight={insight({
          applied_by: "admin@example.com",
          applied_at: "2026-08-02T10:00:00Z",
          changeset_ref: "cs-004",
          review_notes: "Returned to review: changeset cs-004 was rolled back.",
        })}
        onClose={vi.fn()}
        userLabels={{}}
      />,
    );

    expect(
      screen.getByText(/returned to the queue when the changeset below was/i),
    ).toBeInTheDocument();
    expect(screen.getByText("cs-004")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeEnabled();
  });

  it("leaves a fresh capture without the rollback explanation", () => {
    render(<InsightDrawer insight={insight()} onClose={vi.fn()} userLabels={{}} />);

    expect(
      screen.queryByText(/returned to the queue when the changeset below was/i),
    ).not.toBeInTheDocument();
  });

  it("shows the drawer unchanged when nothing was observed", () => {
    render(<InsightDrawer insight={insight()} onClose={vi.fn()} userLabels={{}} />);

    expect(screen.queryByText("Observed Now")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText(urn)).toBeInTheDocument();
  });
});

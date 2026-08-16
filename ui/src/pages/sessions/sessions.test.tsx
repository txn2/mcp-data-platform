import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import type {
  SessionSummary,
  SessionTimelineEntry,
} from "@/api/admin/types";

import { SessionFilters, NO_SESSION_FILTERS } from "./SessionFilters";
import { SessionsTable } from "./SessionsTable";
import { SessionTimeline } from "./SessionTimeline";
import { SessionAssets, SessionInsights } from "./SessionOutputs";
import { kindLabel, shortSessionId } from "./kind";

const AGENT_ID = "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b";

function session(overrides: Partial<SessionSummary> = {}): SessionSummary {
  return {
    session_id: AGENT_ID,
    kind: "agent",
    user_id: "analyst@example.com",
    user_email: "analyst@example.com",
    persona: "data-engineer",
    started_at: "2026-08-16T10:00:00Z",
    last_active_at: "2026-08-16T10:05:00Z",
    call_count: 5,
    failure_count: 1,
    tools: ["search", "trino_query"],
    connections: ["acme-warehouse"],
    asset_count: 1,
    insight_count: 2,
    ...overrides,
  };
}

function entry(overrides: Partial<SessionTimelineEntry> = {}): SessionTimelineEntry {
  return {
    event_id: "evt-1",
    timestamp: "2026-08-16T10:00:00Z",
    tool_name: "trino_query",
    purpose: "Sizing Q3 revenue by region for the board deck.",
    toolkit_kind: "trino",
    connection: "acme-warehouse",
    success: true,
    duration_ms: 143,
    ...overrides,
  };
}

afterEach(cleanup);

describe("the sessions list", () => {
  it("shows one row per session with its kind, counts, and output", () => {
    render(
      <SessionsTable sessions={[session()]} isLoading={false} onOpen={vi.fn()} />,
    );

    expect(screen.getByText(shortSessionId(AGENT_ID))).toBeTruthy();
    expect(screen.getByText(kindLabel("agent"))).toBeTruthy();
    expect(screen.getByText("analyst@example.com")).toBeTruthy();
    expect(screen.getByText("data-engineer")).toBeTruthy();
    expect(screen.getByText("1 asset, 2 insights")).toBeTruthy();
  });

  it("opens the session on row click, the way every portal list opens", () => {
    const onOpen = vi.fn();
    render(
      <SessionsTable sessions={[session()]} isLoading={false} onOpen={onOpen} />,
    );

    fireEvent.click(screen.getByText(shortSessionId(AGENT_ID)));
    expect(onOpen).toHaveBeenCalledWith(AGENT_ID);
  });

  it("reads as empty rather than broken when nothing matches", () => {
    render(<SessionsTable sessions={[]} isLoading={false} onOpen={vi.fn()} />);
    expect(screen.getByText("No sessions found")).toBeTruthy();
  });

  it("says a session that produced nothing produced nothing", () => {
    render(
      <SessionsTable
        sessions={[session({ asset_count: 0, insight_count: 0, failure_count: 0 })]}
        isLoading={false}
        onOpen={vi.fn()}
      />,
    );
    // The produced cell and the failure cell both fall back to a dash.
    expect(screen.getAllByText("-").length).toBeGreaterThanOrEqual(2);
  });
});

describe("the session timeline", () => {
  it("shows the purpose the agent stated for each call", () => {
    render(
      <SessionTimeline
        entries={[entry()]}
        isLoading={false}
        onSelect={vi.fn()}
        titleMap={{}}
      />,
    );
    expect(screen.getByRole("columnheader", { name: "Purpose" })).toBeTruthy();
    expect(
      screen.getByText("Sizing Q3 revenue by region for the board deck."),
    ).toBeTruthy();
  });

  it("marks a failed call and opens its event on click", () => {
    const onSelect = vi.fn();
    render(
      <SessionTimeline
        entries={[entry({ success: false, purpose: "" })]}
        isLoading={false}
        onSelect={onSelect}
        titleMap={{}}
      />,
    );

    expect(screen.getByText("ERR")).toBeTruthy();
    fireEvent.click(screen.getByText("ERR"));
    expect(onSelect).toHaveBeenCalledWith("evt-1");
  });
});

describe("what a session produced", () => {
  const asset = {
    id: "ast-1",
    name: "Q3 revenue by region",
    content_type: "text/csv",
    created_at: "2026-08-16T10:04:00Z",
  };

  it("opens an asset where an admin already reads assets", () => {
    const onNavigate = vi.fn();
    render(<SessionAssets assets={[asset]} onNavigate={onNavigate} />);

    fireEvent.click(screen.getByText("Q3 revenue by region"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/assets/ast-1");
  });

  it("states plainly that a session saved no assets", () => {
    render(<SessionAssets assets={[]} onNavigate={vi.fn()} />);
    expect(screen.getByText("This session saved no assets.")).toBeTruthy();
    expect(screen.getByText("Assets (0)")).toBeTruthy();
  });

  it("shows a captured insight with the review status it sits at", () => {
    render(
      <SessionInsights
        insights={[
          {
            id: "ins-1",
            category: "correction",
            text: "revenue.amount excludes returns.",
            status: "pending",
            created_at: "2026-08-16T10:05:00Z",
          },
        ]}
      />,
    );
    expect(screen.getByText("revenue.amount excludes returns.")).toBeTruthy();
    expect(screen.getByText("pending")).toBeTruthy();
  });

  it("states plainly that a session captured no insights", () => {
    render(<SessionInsights insights={[]} />);
    expect(screen.getByText("This session captured no insights.")).toBeTruthy();
  });
});

describe("the session filter bar", () => {
  it("opens on a bounded window, and offers the reader a wider one", () => {
    const onChange = vi.fn();
    render(<SessionFilters value={NO_SESSION_FILTERS} onChange={onChange} />);

    const windowFacet = screen.getByLabelText("Filter by time window");
    expect(windowFacet.textContent).toContain("Last 7 Days");

    fireEvent.click(windowFacet);
    fireEvent.click(screen.getByRole("option", { name: "All Time" }));
    expect(onChange).toHaveBeenCalledWith({ window: "all" });
  });
});

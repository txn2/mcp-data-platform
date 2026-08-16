import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import type { AuditEvent, SessionDetail } from "@/api/admin/types";

const AGENT_ID = "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b";

// The page reads one session and, when a timeline row is opened, one audit
// event. Both are stubbed here so the assertions are about what the page
// renders rather than about the query layer.
const state: {
  detail?: SessionDetail;
  error?: Error;
  event?: AuditEvent;
} = {};

vi.mock("@/api/admin/hooks", () => ({
  useSession: () => ({
    data: state.detail,
    isLoading: false,
    error: state.error,
  }),
  useAuditEvent: (id: string | null) => ({ data: id ? state.event : undefined }),
  useToolSchemas: () => ({ data: { schemas: {} } }),
  useToolTitleMap: () => ({}),
}));

import { SessionDetailPage } from "./SessionDetailPage";

function detail(overrides: Partial<SessionDetail> = {}): SessionDetail {
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
    insight_count: 0,
    assets: [
      {
        id: "ast-1",
        name: "Q3 revenue by region",
        content_type: "text/csv",
        created_at: "2026-08-16T10:04:00Z",
      },
    ],
    insights: [],
    timeline: [
      {
        event_id: "evt-1",
        timestamp: "2026-08-16T10:00:00Z",
        tool_name: "trino_query",
        purpose: "Sizing Q3 revenue by region for the board deck.",
        toolkit_kind: "trino",
        connection: "acme-warehouse",
        success: true,
        duration_ms: 143,
      },
    ],
    timeline_total: 5,
    ...overrides,
  };
}

function auditEvent(): AuditEvent {
  return {
    id: "evt-1",
    timestamp: "2026-08-16T10:00:00Z",
    duration_ms: 143,
    request_id: "req-1",
    session_id: AGENT_ID,
    user_id: "analyst@example.com",
    user_email: "analyst@example.com",
    persona: "data-engineer",
    tool_name: "trino_query",
    toolkit_kind: "trino",
    toolkit_name: "prod",
    connection: "acme-warehouse",
    parameters: { sql: "SELECT 1" },
    success: true,
    response_chars: 120,
    request_chars: 40,
    content_blocks: 1,
    transport: "http",
    source: "mcp",
    enrichment_applied: true,
    authorized: true,
  };
}

function renderPage(onNavigate = vi.fn(), onBack = vi.fn()) {
  render(
    <SessionDetailPage
      sessionId={AGENT_ID}
      onNavigate={onNavigate}
      onBack={onBack}
    />,
  );
  return { onNavigate, onBack };
}

beforeEach(() => {
  state.detail = detail();
  state.error = undefined;
  state.event = undefined;
});

afterEach(cleanup);

describe("a session opened", () => {
  it("identifies the session and what it did", () => {
    renderPage();
    expect(screen.getByText(AGENT_ID)).toBeTruthy();
    expect(screen.getByText("Agent")).toBeTruthy();
    expect(screen.getByText("Calls")).toBeTruthy();
    expect(screen.getByText("5")).toBeTruthy();
    expect(screen.getByText("calls returned an error")).toBeTruthy();
  });

  it("reads the calls in order with the purpose each stated", () => {
    renderPage();
    expect(screen.getByText("Timeline (5)")).toBeTruthy();
    expect(
      screen.getByText("Sizing Q3 revenue by region for the board deck."),
    ).toBeTruthy();
  });

  it("lists the asset it saved and opens it", () => {
    const { onNavigate } = renderPage();
    fireEvent.click(screen.getByText("Q3 revenue by region"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/assets/ast-1");
  });

  it("says so when the session produced nothing", () => {
    state.detail = detail({ assets: [], asset_count: 0 });
    renderPage();
    expect(screen.getByText("This session saved no assets.")).toBeTruthy();
    expect(screen.getByText("This session captured no insights.")).toBeTruthy();
  });

  it("opens a timeline row in the event drawer", () => {
    state.event = auditEvent();
    renderPage();
    fireEvent.click(screen.getByText("OK"));
    // The drawer renders the event's own detail, which the timeline row does
    // not carry: its parameters.
    expect(screen.getByText("Parameters")).toBeTruthy();
  });

  it("goes back to the list", () => {
    const { onBack } = renderPage();
    fireEvent.click(screen.getByText("Sessions"));
    expect(onBack).toHaveBeenCalled();
  });

  it("explains an id with no recorded calls instead of rendering an empty shell", () => {
    state.detail = undefined;
    state.error = new Error("not found");
    renderPage();
    expect(
      screen.getByText(
        "No calls are recorded for this session. It may have aged out of audit history.",
      ),
    ).toBeTruthy();
  });
});

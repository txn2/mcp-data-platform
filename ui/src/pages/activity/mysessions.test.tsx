import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import type { SessionDetail, SessionSummary } from "@/api/admin/types";

const AGENT_ID = "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b";

// The two pages read the caller's own sessions. Both hooks are stubbed here so
// the assertions are about what a reader sees rather than about the query
// layer; that the request itself is scoped to the caller is the server's
// contract, asserted against a real database in
// internal/portal/sessionapi/sessions_realdb_integration_test.go.
const state: {
  list?: { data: SessionSummary[]; total: number };
  detail?: SessionDetail;
  error?: Error;
  listParams?: Record<string, unknown>;
} = {};

vi.mock("@/api/portal/hooks", () => ({
  useMySessions: (params: Record<string, unknown>) => {
    state.listParams = params;
    return { data: state.list, isLoading: false };
  },
  useMySession: () => ({
    data: state.detail,
    isLoading: false,
    error: state.error,
  }),
}));

import { MySessionDetailPage } from "./MySessionDetailPage";
import { MySessionsPage } from "./MySessionsPage";

function summary(overrides: Partial<SessionSummary> = {}): SessionSummary {
  return {
    session_id: AGENT_ID,
    kind: "agent",
    user_id: "sarah.chen@example.com",
    user_email: "sarah.chen@example.com",
    persona: "data-engineer",
    started_at: "2026-08-16T10:00:00Z",
    last_active_at: "2026-08-16T10:05:00Z",
    call_count: 5,
    failure_count: 1,
    tools: ["search", "trino_query"],
    connections: ["acme-warehouse"],
    asset_count: 1,
    insight_count: 0,
    ...overrides,
  };
}

function detail(overrides: Partial<SessionDetail> = {}): SessionDetail {
  return {
    ...summary(),
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

beforeEach(() => {
  state.list = { data: [summary()], total: 1 };
  state.detail = detail();
  state.error = undefined;
  state.listParams = undefined;
});

afterEach(cleanup);

describe("my sessions", () => {
  it("lists the reader's own sessions and opens one on row click", () => {
    const onNavigate = vi.fn();
    render(<MySessionsPage onNavigate={onNavigate} />);

    expect(screen.getByText("data-engineer")).toBeTruthy();
    fireEvent.click(screen.getByText("data-engineer"));
    expect(onNavigate).toHaveBeenCalledWith(`/activity/sessions/${AGENT_ID}`);
  });

  // Every row would carry the reader's own name, and the facet over it would
  // be a control the server ignores.
  it("carries neither a user column nor a user facet", () => {
    render(<MySessionsPage onNavigate={vi.fn()} />);
    expect(screen.queryByText("User")).toBeNull();
    expect(screen.queryByLabelText("Filter by user")).toBeNull();
    expect(screen.getByLabelText("Filter by time window")).toBeTruthy();
  });

  it("says plainly when the reader has run nothing in the window", () => {
    state.list = { data: [], total: 0 };
    render(<MySessionsPage onNavigate={vi.fn()} />);
    expect(screen.getByText("No sessions found")).toBeTruthy();
  });

  // The window is a bound on the query, not decoration: the list rolls up
  // every event in range, so widening it has to reach the request.
  it("bounds the query to the chosen window", () => {
    render(<MySessionsPage onNavigate={vi.fn()} />);
    expect(state.listParams?.startTime).toBeTruthy();
    expect(state.listParams?.page).toBe(1);

    fireEvent.click(screen.getByLabelText("Filter by time window"));
    fireEvent.click(screen.getByRole("option", { name: "All Time" }));
    expect(state.listParams?.startTime).toBeUndefined();
    expect(state.listParams?.page).toBe(1);
  });
});

describe("one of my sessions opened", () => {
  function renderDetail(onNavigate = vi.fn(), onBack = vi.fn()) {
    render(
      <MySessionDetailPage
        sessionId={AGENT_ID}
        onNavigate={onNavigate}
        onBack={onBack}
      />,
    );
    return { onNavigate, onBack };
  }

  it("reads the calls in order with the purpose each stated", () => {
    renderDetail();
    expect(screen.getByText(AGENT_ID)).toBeTruthy();
    expect(screen.getByText("Timeline (5)")).toBeTruthy();
    expect(
      screen.getByText("Sizing Q3 revenue by region for the board deck."),
    ).toBeTruthy();
  });

  it("opens an asset it saved where the owner reads their own assets", () => {
    const { onNavigate } = renderDetail();
    fireEvent.click(screen.getByText("Q3 revenue by region"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/ast-1");
  });

  it("goes back to My Sessions", () => {
    const { onBack } = renderDetail();
    fireEvent.click(screen.getByText("My Sessions"));
    expect(onBack).toHaveBeenCalled();
  });

  // A session that is not the reader's own is answered not-found, so this
  // state covers both an expired session and someone else's id — deliberately,
  // since telling them apart would answer a question about another user.
  it("explains a session it cannot read instead of an empty shell", () => {
    state.detail = undefined;
    state.error = new Error("not found");
    renderDetail();
    expect(
      screen.getByText(
        "No calls of yours are recorded for this session. It may have aged out of audit history.",
      ),
    ).toBeTruthy();
  });
});

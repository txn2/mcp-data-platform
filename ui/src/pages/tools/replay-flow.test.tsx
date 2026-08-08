import { StrictMode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { AuditEvent, ToolDetail, ToolInfo, ToolSchema } from "@/api/admin/types";

// The replay flow spans two components connected only by (a) the search-param
// URL the EventDrawer navigates to and (b) the Zustand replay-intent store. This
// is the exact seam that broke in the #340/#342 redesign, so the test drives the
// real EventDrawer navigation target into the real ToolsPage consumption rather
// than asserting either half in isolation. It mocks only the admin data hooks;
// EventDrawer, ToolsPage, ToolDetail, TryItTab, useTryItSession, and the store
// are all real.

const TRINO_SCHEMA: ToolSchema = {
  name: "trino_query",
  kind: "trino",
  description: "Execute a SQL query",
  parameters: {
    type: "object",
    required: ["sql"],
    properties: {
      sql: { type: "string", format: "sql", description: "SQL to run" },
    },
  },
};

const TOOLS: ToolInfo[] = [
  // datahub_browse sorts first so the buggy behavior (auto-select first tool)
  // would land here, NOT on trino_query; the regression this test guards.
  { name: "datahub_browse", toolkit: "datahub", kind: "datahub", connection: "", hidden: false },
  { name: "trino_query", toolkit: "trino", kind: "trino", connection: "primary", hidden: false },
];

const TRINO_DETAIL: ToolDetail = {
  name: "trino_query",
  description: "Execute a SQL query",
  toolkit_kind: "trino",
  toolkit_name: "prod",
  connection: "primary",
  personas: [],
  hidden_by_global_deny: false,
  description_overridden: false,
  enrichment_rule_count: 0,
};

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useToolSchemas: () => ({ data: { schemas: { trino_query: TRINO_SCHEMA } } }),
    useTools: () => ({ data: { tools: TOOLS }, isLoading: false }),
    useToolDetail: (name: string | null) => ({
      data: name === "trino_query" ? TRINO_DETAIL : undefined,
      isLoading: false,
      error: null,
    }),
    useCallTool: () => ({ mutate: vi.fn(), isPending: false }),
    useEffectiveConnections: () => ({ data: [] }),
  };
});

import { EventDrawer } from "@/components/EventDrawer";
import { ToolsPage } from "./ToolsPage";
import { useInspectorStore } from "@/stores/inspector";

const EVENT: AuditEvent = {
  id: "abcdef1234567890",
  timestamp: "2026-07-09T12:00:00Z",
  duration_ms: 12,
  request_id: "req-1",
  session_id: "sess-original",
  user_id: "analyst@example.com",
  tool_name: "trino_query",
  connection: "primary",
  parameters: { sql: "SELECT 42" },
  success: true,
  response_chars: 10,
  request_chars: 20,
  content_blocks: 1,
  transport: "http",
  source: "mcp",
  enrichment_applied: false,
  authorized: true,
};

describe("Replay in Inspector flow", () => {
  beforeEach(() => {
    // Reset the shared replay-intent store and the jsdom URL between tests.
    useInspectorStore.setState({ replayIntent: null });
    window.history.replaceState(null, "", "/admin/tools");
  });

  it("navigates to the search-param URL the Tools page reads", () => {
    const onNavigate = vi.fn();
    render(<EventDrawer event={EVENT} onClose={() => {}} onNavigate={onNavigate} />);

    fireEvent.click(screen.getByRole("button", { name: /Replay in Inspector/i }));

    // The Tools page reads ?selected=<tool>&tab=<tab>; the legacy "#explore"
    // hash set neither, which is what broke the flow.
    expect(onNavigate).toHaveBeenCalledWith(
      "/admin/tools?selected=trino_query&tab=tryit",
    );
    // The intent (with the params) is stashed for the matching ToolDetail mount.
    expect(useInspectorStore.getState().replayIntent).toMatchObject({
      tool_name: "trino_query",
      parameters: { sql: "SELECT 42" },
      event_id: EVENT.id,
    });
  });

  it("selects the tool, opens Try It, and pre-fills params after a replay click", () => {
    // 1. Click replay in the drawer; capture the navigation target.
    let navPath = "";
    const { unmount } = render(
      <EventDrawer event={EVENT} onClose={() => {}} onNavigate={(p) => (navPath = p)} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Replay in Inspector/i }));
    unmount();

    // 2. Apply that navigation to the jsdom URL, exactly as AppShell.navigate
    //    would (it preserves the query string), then mount the Tools page.
    expect(navPath).toBe("/admin/tools?selected=trino_query&tab=tryit");
    window.history.replaceState(null, "", navPath);
    // StrictMode double-invokes effects on mount, exactly as `make dev` does.
    // This reproduces the real app: without it, the unguarded reset effect in
    // useTryItSession re-runs and wipes the applied replay params, landing on an
    // empty form. The test must run under StrictMode or it gives false
    // confidence (the pre-fill "works" in a bare render but not in the app).
    render(
      <StrictMode>
        <ToolsPage />
      </StrictMode>,
    );

    // Selection + tab: the Try It tab is active for the event's tool (not the
    // first tool in the list, and not the Overview tab).
    expect(screen.getByRole("tab", { name: "Try It" })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    // Intent consumed: the replay banner renders (it lives only in TryItTab).
    expect(
      screen.getByText(/Replaying audit event/i),
    ).toBeInTheDocument();

    // Pre-filled params: the SQL field shows the event's parameters.
    expect(screen.getByDisplayValue("SELECT 42")).toBeInTheDocument();
  });
});
